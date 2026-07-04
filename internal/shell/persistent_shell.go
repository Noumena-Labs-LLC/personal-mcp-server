package shell

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

type persistentShellState int32

const (
	persistentShellStateNew persistentShellState = iota
	persistentShellStateReady
	persistentShellStateInUse
	persistentShellStateDead
)

type persistentShell struct {
	key           string
	cmd           *exec.Cmd
	pty           *os.File
	shellPath     string
	startupDir    string
	startupMarker string
	waitDone      chan struct{}
	cleanupOnce   sync.Once
	state         atomic.Int32
}

type persistentShellWriteError struct {
	err error
}

type persistentShellBusyError struct {
	err error
}

func (e persistentShellWriteError) Error() string { return e.err.Error() }
func (e persistentShellWriteError) Unwrap() error { return e.err }

func (e persistentShellBusyError) Error() string { return "persistent shell busy: " + e.err.Error() }
func (e persistentShellBusyError) Unwrap() error { return e.err }

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func shellQuoteJoin(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellInputQuote(shellPath, s string) string {
	if s == "" {
		return "''"
	}
	if !shellInputNeedsEscaping(s) {
		return shellQuote(s)
	}
	if isBashShell(shellPath) || isZshShell(shellPath) {
		return shellANSIQuote(s)
	}
	return shellQuote(s)
}

func shellInputQuoteJoin(shellPath string, argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellInputQuote(shellPath, arg))
	}
	return strings.Join(parts, " ")
}

func shellInputNeedsEscaping(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7f {
			return true
		}
	}
	return false
}

func shellANSIQuote(s string) string {
	var quoted strings.Builder
	quoted.WriteString("$'")
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b == '\\' || b == '\'':
			quoted.WriteByte('\\')
			quoted.WriteByte(b)
		case b >= 0x20 && b <= 0x7e:
			quoted.WriteByte(b)
		default:
			fmt.Fprintf(&quoted, "\\%03o", b)
		}
	}
	quoted.WriteByte('\'')
	return quoted.String()
}

func persistentShellEnv(spec config.CommandSpec) []string {
	env := os.Environ()
	if runtime.GOOS == "darwin" {
		env = removeEnv(env, "LC_ALL", "C.UTF-8")
	}
	for k, v := range spec.Env {
		env = upsertEnv(env, k, v)
	}
	for _, name := range spec.EnvFromHost {
		if v, ok := os.LookupEnv(name); ok {
			env = upsertEnv(env, name, v)
		}
	}
	return env
}

func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func removeEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := env[:0]
	for _, entry := range env {
		if entry == prefix+value {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (p *persistentShell) initialize(ctx context.Context, startupTimeout, quietPeriod time.Duration, key, shellPath, cwd string, spec config.CommandSpec) error {
	startupCtx, cancel := persistentShellStartupContext(ctx, startupTimeout)
	defer cancel()
	started := time.Now()
	master, slave, err := openPty()
	if err != nil {
		return err
	}
	env := persistentShellEnv(spec)
	startupDir := ""
	startupMarker := ""
	if isZshShell(shellPath) {
		startupDir, startupMarker, err = prepareZshStartupWrapper(spec.StartupFiles)
		if err != nil {
			_ = master.Close()
			_ = slave.Close()
			return err
		}
		env = upsertEnv(env, "ZDOTDIR", startupDir)
	} else if isBashShell(shellPath) {
		startupDir, startupMarker, err = prepareBashStartupWrapper(spec.StartupFiles)
		if err != nil {
			_ = master.Close()
			_ = slave.Close()
			return err
		}
		env = upsertEnv(env, "HOME", startupDir)
	}
	cmd := exec.Command(shellPath, "-li") //nolint:gosec,noctx // shellPath is restricted by command_environment.allowed_shells and persistent shell mode is trusted-project opt-in; pooled shells must outlive the per-request context.
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = shellSysProcAttr()
	}
	if err := cmd.Start(); err != nil {
		_ = master.Close()
		_ = slave.Close()
		if startupDir != "" {
			_ = os.RemoveAll(startupDir)
		}
		return err
	}
	slog.DebugContext(startupCtx, "persistent shell process started",
		"shell_session", key,
		"shell", shellPath,
		"cwd", cwd,
		"startup_timeout_ms", startupTimeout.Milliseconds(),
		"quiet_period_ms", quietPeriod.Milliseconds(),
		"pid", cmd.Process.Pid,
		"startup_wrapper", startupDir != "",
	)
	_ = slave.Close()
	p.key = key
	p.cmd = cmd
	p.pty = master
	p.shellPath = shellPath
	p.startupDir = startupDir
	p.startupMarker = startupMarker
	p.waitDone = make(chan struct{})
	go p.watchProcessExit()
	if err := syscall.SetNonblock(int(master.Fd()), true); err != nil {
		p.kill()
		return fmt.Errorf("set persistent shell PTY nonblocking: %w", err)
	}
	if err := p.waitReady(startupCtx, quietPeriod); err != nil {
		slog.WarnContext(startupCtx, "persistent shell init failed",
			"shell_session", key,
			"shell", shellPath,
			"cwd", cwd,
			"duration_ms", time.Since(started).Milliseconds(),
			"error", err.Error(),
		)
		p.kill()
		return err
	}
	slog.DebugContext(startupCtx, "persistent shell init completed",
		"shell_session", key,
		"shell", shellPath,
		"cwd", cwd,
		"duration_ms", time.Since(started).Milliseconds(),
	)
	p.setState(persistentShellStateReady)
	return nil
}

func (p *persistentShell) watchProcessExit() {
	if p == nil || p.cmd == nil {
		return
	}
	pid := 0
	if p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	waitErr := p.cmd.Wait()
	stateBefore := p.currentState()
	exitCode := 0
	exited := false
	success := false
	if p.cmd.ProcessState != nil {
		exitCode = p.cmd.ProcessState.ExitCode()
		exited = p.cmd.ProcessState.Exited()
		success = p.cmd.ProcessState.Success()
	}
	slog.Warn("persistent shell process exited",
		"shell_session", p.key,
		"pid", pid,
		"wait_err", errorString(waitErr),
		"state_before_dead", stateBefore,
		"exited", exited,
		"success", success,
		"exit_code", exitCode,
	)
	p.setState(persistentShellStateDead)
	p.cleanupStartupDir()
	if p.waitDone != nil {
		close(p.waitDone)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func persistentShellStartupContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if parent == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	if deadline, ok := parent.Deadline(); ok {
		if remaining := time.Until(deadline); remaining <= timeout {
			return context.WithCancel(parent)
		}
	}
	return context.WithTimeout(parent, timeout)
}

func (p *persistentShell) waitReady(ctx context.Context, quietPeriod time.Duration) error {
	if p.startupMarker != "" {
		markerStarted := time.Now()
		readyOutput, readyTruncated, err := p.readUntilMarker(ctx, p.startupMarker, 8192)
		slog.DebugContext(ctx, "persistent shell startup marker read completed",
			"shell_session", p.key,
			"duration_ms", time.Since(markerStarted).Milliseconds(),
			"ready_marker", p.startupMarker,
			"output_bytes", len(readyOutput),
			"output_truncated", readyTruncated,
			"output_preview", previewPersistentShellOutput(readyOutput),
			"ok", err == nil,
			"startup_wrapper", true,
		)
		if err != nil {
			preview := previewPersistentShellOutput(readyOutput)
			if preview != "" {
				return fmt.Errorf("persistent shell startup marker not observed: %w; startup output=%q", err, preview)
			}
			return fmt.Errorf("persistent shell startup marker not observed: %w", err)
		}
		return nil
	}

	quiescenceStarted := time.Now()
	quiescenceOutput, quiescenceErr := p.waitForQuiescence(ctx, quietPeriod, 8192)
	slog.DebugContext(ctx, "persistent shell pre-marker quiescence completed",
		"shell_session", p.key,
		"duration_ms", time.Since(quiescenceStarted).Milliseconds(),
		"quiet_period_ms", quietPeriod.Milliseconds(),
		"output_bytes", len(quiescenceOutput),
		"output_preview", previewPersistentShellOutput(quiescenceOutput),
		"timed_out", errors.Is(quiescenceErr, context.DeadlineExceeded) || errors.Is(quiescenceErr, context.Canceled),
	)
	markerStarted := time.Now()
	readyOutput, readyTruncated, readyMarker, probeCount, err := p.readUntilLatestReadyMarker(ctx, 8192)
	slog.DebugContext(ctx, "persistent shell startup marker read completed",
		"shell_session", p.key,
		"duration_ms", time.Since(markerStarted).Milliseconds(),
		"ready_marker", readyMarker,
		"probe_count", probeCount,
		"output_bytes", len(readyOutput),
		"output_truncated", readyTruncated,
		"output_preview", previewPersistentShellOutput(readyOutput),
		"ok", err == nil,
	)
	if err != nil {
		preview := previewPersistentShellOutput(readyOutput)
		if preview != "" {
			return fmt.Errorf("persistent shell startup marker not observed: %w; startup output=%q", err, preview)
		}
		return fmt.Errorf("persistent shell startup marker not observed: %w", err)
	}
	return nil
}

func (p *persistentShell) readUntilMarker(ctx context.Context, marker string, maxOutput int64) (output string, truncated bool, err error) {
	var out limitBuffer
	out.Limit = maxOutput

	buf := make([]byte, 4096)
	pending := make([]byte, 0, len(marker)+4096)
	markerBytes := []byte(marker)
	keepBytes := len(markerBytes) - 1
	if keepBytes < 0 {
		keepBytes = 0
	}

	flushPending := func() {
		if maxOutput != 0 && len(pending) > 0 {
			_, _ = out.Write(pending)
		}
		pending = pending[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flushPending()
			p.kill()
			return out.String(), out.Truncated, ctx.Err()
		default:
		}

		n, readErr := p.pty.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			if bytes.Contains(pending, []byte("Ignore insecure directories and files and continue")) {
				if maxOutput != 0 {
					_, _ = out.Write(pending)
				}
				return out.String(), out.Truncated, errors.New("persistent shell startup is waiting for zsh compinit confirmation about insecure directories; run compaudit/compinit fixups, disable compfix prompts, or use argv mode or a different shell")
			}
			if idx := bytes.Index(pending, markerBytes); idx >= 0 {
				if maxOutput != 0 && idx > 0 {
					_, _ = out.Write(pending[:idx])
				}
				return out.String(), out.Truncated, nil
			}
			if len(pending) > keepBytes {
				flushLen := len(pending) - keepBytes
				if maxOutput != 0 {
					_, _ = out.Write(pending[:flushLen])
				}
				copy(pending, pending[flushLen:])
				pending = pending[:keepBytes]
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, syscall.EAGAIN) || errors.Is(readErr, syscall.EWOULDBLOCK) {
			select {
			case <-ctx.Done():
				flushPending()
				p.kill()
				return out.String(), out.Truncated, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		flushPending()
		return out.String(), out.Truncated, readErr
	}
}

func (p *persistentShell) readUntilLatestReadyMarker(ctx context.Context, maxOutput int64) (output string, truncated bool, marker string, probeCount int, err error) {
	const readyPrefix = "__PERSONAL_MCP_READY__"
	const probeInterval = time.Second

	var out limitBuffer
	out.Limit = maxOutput

	buf := make([]byte, 4096)
	pending := make([]byte, 0, 4096)
	var latestMarker string
	var latestMarkerBytes []byte
	keepBytes := 0

	flushPending := func() {
		if maxOutput != 0 && len(pending) > 0 {
			_, _ = out.Write(pending)
		}
		pending = pending[:0]
	}
	sendProbe := func() error {
		readySuffix := randomHex(24)
		latestMarker = readyPrefix + readySuffix
		latestMarkerBytes = []byte(latestMarker)
		keepBytes = len(latestMarkerBytes) - 1
		if keepBytes < 0 {
			keepBytes = 0
		}
		probeCount++
		// Emit the ready marker as two independent printf arguments. Echoed shell
		// input contains the quoted source strings, not the concatenated runtime
		// marker, so startup only succeeds when the shell actually executes printf.
		if err := p.writeString(ctx, "stty -echo 2>/dev/null || true\nprintf '\\n%s%s\\n' "+shellQuote(readyPrefix)+" "+shellQuote(readySuffix)+"\n"); err != nil {
			return err
		}
		slog.DebugContext(ctx, "persistent shell startup marker sent",
			"shell_session", p.key,
			"ready_marker", latestMarker,
			"probe_count", probeCount,
		)
		return nil
	}
	if err := sendProbe(); err != nil {
		return out.String(), out.Truncated, latestMarker, probeCount, err
	}
	nextProbe := time.Now().Add(probeInterval)

	for {
		select {
		case <-ctx.Done():
			flushPending()
			p.kill()
			return out.String(), out.Truncated, latestMarker, probeCount, ctx.Err()
		default:
		}
		if time.Now().After(nextProbe) || time.Now().Equal(nextProbe) {
			if err := sendProbe(); err != nil {
				return out.String(), out.Truncated, latestMarker, probeCount, err
			}
			nextProbe = time.Now().Add(probeInterval)
		}

		n, readErr := p.pty.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			if bytes.Contains(pending, []byte("Ignore insecure directories and files and continue")) {
				if maxOutput != 0 {
					_, _ = out.Write(pending)
				}
				return out.String(), out.Truncated, latestMarker, probeCount, errors.New("persistent shell startup is waiting for zsh compinit confirmation about insecure directories; run compaudit/compinit fixups, disable compfix prompts, or use argv mode or a different shell")
			}
			if idx := bytes.Index(pending, latestMarkerBytes); idx >= 0 {
				if maxOutput != 0 && idx > 0 {
					_, _ = out.Write(pending[:idx])
				}
				return out.String(), out.Truncated, latestMarker, probeCount, nil
			}
			if len(pending) > keepBytes {
				flushLen := len(pending) - keepBytes
				if maxOutput != 0 {
					_, _ = out.Write(pending[:flushLen])
				}
				copy(pending, pending[flushLen:])
				pending = pending[:keepBytes]
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, syscall.EAGAIN) || errors.Is(readErr, syscall.EWOULDBLOCK) {
			select {
			case <-ctx.Done():
				flushPending()
				p.kill()
				return out.String(), out.Truncated, latestMarker, probeCount, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		flushPending()
		return out.String(), out.Truncated, latestMarker, probeCount, readErr
	}
}

func (p *persistentShell) waitForQuiescence(ctx context.Context, quietWindow time.Duration, maxOutput int64) (string, error) {
	var out limitBuffer
	out.Limit = maxOutput
	buf := make([]byte, 4096)
	lastActivity := time.Now()
	for {
		select {
		case <-ctx.Done():
			return out.String(), ctx.Err()
		default:
		}

		n, err := p.pty.Read(buf)
		if n > 0 {
			lastActivity = time.Now()
			_, _ = out.Write(buf[:n])
			continue
		}
		if err == nil {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			if time.Since(lastActivity) >= quietWindow {
				return out.String(), nil
			}
			select {
			case <-ctx.Done():
				return out.String(), ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		return out.String(), err
	}
}

func newUninitializedPersistentShell(key string) *persistentShell {
	shell := &persistentShell{key: key}
	shell.setState(persistentShellStateNew)
	return shell
}

func (p *persistentShell) setState(state persistentShellState) {
	p.state.Store(int32(state))
}

func (p *persistentShell) transitionState(from, to persistentShellState) bool {
	return p.state.CompareAndSwap(int32(from), int32(to))
}

func (p *persistentShell) currentState() persistentShellState {
	return persistentShellState(p.state.Load())
}

func (p *persistentShell) run(ctx context.Context, cwd string, argv []string, maxOutput int64) (output string, truncated bool, exitCode int, err error) {
	return p.runUnlocked(ctx, cwd, argv, maxOutput)
}

func (p *persistentShell) runUnlocked(runCtx context.Context, cwd string, argv []string, maxOutput int64) (output string, truncated bool, exitCode int, err error) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.currentState() == persistentShellStateDead {
		return "", false, -1, errors.New("persistent shell already exited")
	}
	if p.waitDone != nil {
		select {
		case <-p.waitDone:
			return "", false, -1, errors.New("persistent shell already exited")
		default:
		}
	}
	sentinel := "__PERSONAL_MCP_DONE_" + randomHex(12) + "__"
	stagedPath, stageErr := stagePersistentShellCommand(p.shellPath, cwd, argv, sentinel)
	if stageErr != nil {
		return "", false, -1, stageErr
	}
	defer func() {
		_ = os.Remove(stagedPath)
	}()
	invoke := ". " + shellQuote(stagedPath) + "\n"
	if err := p.writeString(runCtx, invoke); err != nil {
		return "", false, -1, persistentShellWriteError{err: err}
	}
	return p.readUntilSentinel(runCtx, sentinel, maxOutput)
}

func stagePersistentShellCommand(shellPath, cwd string, argv []string, sentinel string) (string, error) {
	script, err := os.CreateTemp("", "personal-mcp-persistent-shell-*.sh")
	if err != nil {
		return "", err
	}
	path := script.Name()
	body := "(cd " + shellInputQuote(shellPath, cwd) + " && " + shellInputQuoteJoin(shellPath, argv) + ")\n" +
		"__personal_mcp_server_status=$?\n" +
		"printf '\\n" + sentinel + ":%s\\n' \"$__personal_mcp_server_status\"\n"
	if _, err := script.WriteString(body); err != nil {
		_ = script.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := script.Chmod(0o600); err != nil {
		_ = script.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := script.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (p *persistentShell) writeString(ctx context.Context, s string) error {
	data := []byte(s)
	for len(data) > 0 {
		n, err := p.pty.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err == nil {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			p.drainAvailable()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		return err
	}
	return nil
}

func (p *persistentShell) drainAvailable() {
	if p == nil || p.pty == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		_, err := p.pty.Read(buf)
		if err == nil {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			return
		}
		return
	}
}

func (p *persistentShell) readUntilSentinel(ctx context.Context, sentinel string, maxOutput int64) (output string, truncated bool, exitCode int, err error) {
	var out limitBuffer
	out.Limit = maxOutput
	exitCode = -1

	buf := make([]byte, 4096)
	pending := make([]byte, 0, len(sentinel)+4096)
	sentinelBytes := []byte(sentinel)
	keepBytes := len(sentinelBytes) - 1
	if keepBytes < 0 {
		keepBytes = 0
	}

	flushPending := func() {
		if maxOutput != 0 && len(pending) > 0 {
			_, _ = out.Write(pending)
		}
		pending = pending[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flushPending()
			p.kill()
			return out.String(), out.Truncated, exitCode, ctx.Err()
		default:
		}

		n, readErr := p.pty.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			if bytes.Contains(pending, []byte("Ignore insecure directories and files and continue")) {
				if maxOutput != 0 {
					_, _ = out.Write(pending)
				}
				return out.String(), out.Truncated, exitCode, errors.New("persistent shell startup is waiting for zsh compinit confirmation about insecure directories; run compaudit/compinit fixups, disable compfix prompts, or use argv mode or a different shell")
			}
			holdPending := false
			for {
				idx := bytes.Index(pending, sentinelBytes)
				if idx < 0 {
					break
				}

				statusStart := idx + len(sentinelBytes)
				if statusStart >= len(pending) {
					// The sentinel may be split from its status bytes. Keep it buffered
					// until more data arrives.
					holdPending = true
					break
				}
				if pending[statusStart] != ':' {
					// PTYs can echo the command text. The echoed printf command contains
					// the sentinel string but not the completion frame. Treat that
					// occurrence as ordinary output and keep scanning.
					if maxOutput != 0 {
						_, _ = out.Write(pending[:statusStart])
					}
					pending = pending[statusStart:]
					continue
				}

				statusBytes := pending[statusStart+1:]
				lineEnd := bytes.IndexAny(statusBytes, "\r\n")
				if lineEnd < 0 {
					// The completion frame arrived without its terminating newline yet.
					// Keep reading instead of turning a partial frame into success.
					holdPending = true
					break
				}
				statusText := strings.TrimSpace(string(statusBytes[:lineEnd]))
				if statusText == "" || strings.IndexFunc(statusText, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
					// This is an echoed sentinel, for example SENTINEL:%s from the
					// printf command text. Do not mistake it for command completion.
					flushThrough := statusStart + 1 + lineEnd + 1
					if maxOutput != 0 {
						_, _ = out.Write(pending[:flushThrough])
					}
					pending = pending[flushThrough:]
					continue
				}
				if maxOutput != 0 && idx > 0 {
					_, _ = out.Write(pending[:idx])
				}
				_, _ = fmt.Sscanf(statusText, "%d", &exitCode)
				return out.String(), out.Truncated, exitCode, nil
			}

			if !holdPending && len(pending) > keepBytes {
				flushLen := len(pending) - keepBytes
				if maxOutput != 0 {
					_, _ = out.Write(pending[:flushLen])
				}
				copy(pending, pending[flushLen:])
				pending = pending[:keepBytes]
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, syscall.EAGAIN) || errors.Is(readErr, syscall.EWOULDBLOCK) {
			select {
			case <-ctx.Done():
				flushPending()
				p.kill()
				return out.String(), out.Truncated, exitCode, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		flushPending()
		return out.String(), out.Truncated, exitCode, readErr
	}
}

func (p *persistentShell) kill() {
	if p != nil {
		p.setState(persistentShellStateDead)
	}
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		if p != nil {
			p.cleanupStartupDir()
		}
		return
	}
	if p.pty != nil {
		_ = p.pty.Close()
	}
	if runtime.GOOS == "windows" {
		_ = p.cmd.Process.Kill()
		p.waitForExit()
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.cmd.Process.Kill()
	p.waitForExit()
}

func (p *persistentShell) waitForExit() {
	if p == nil || p.waitDone == nil {
		p.cleanupStartupDir()
		return
	}
	select {
	case <-p.waitDone:
	case <-time.After(2 * time.Second):
	}
}

func (p *persistentShell) cleanupStartupDir() {
	if p == nil {
		return
	}
	p.cleanupOnce.Do(func() {
		if p.startupDir != "" {
			_ = os.RemoveAll(p.startupDir)
			p.startupDir = ""
		}
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func previewPersistentShellOutput(output string) string {
	const maxLen = 240
	preview := stripTerminalEscapes(output)
	preview = strings.TrimSpace(preview)
	if len(preview) > maxLen {
		preview = preview[:maxLen]
	}
	return preview
}

func isZshShell(shellPath string) bool {
	return filepath.Base(shellPath) == "zsh"
}

func isBashShell(shellPath string) bool {
	return filepath.Base(shellPath) == "bash"
}

func prepareZshStartupWrapper(startupFiles []string) (dir, marker string, err error) {
	dir, err = os.MkdirTemp("", "personal-mcp-zdotdir-*")
	if err != nil {
		return "", "", err
	}
	marker = "__PERSONAL_MCP_READY__" + randomHex(24)
	files := map[string]string{
		".zshenv":   "",
		".zprofile": "",
		".zshrc":    "",
		".zlogin":   strings.Join(append(startupSourceLines(startupFiles), `printf '\n%s\n' `+shellQuote(marker), ""), "\n"),
	}
	for name, body := range files {
		if writeErr := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); writeErr != nil {
			_ = os.RemoveAll(dir)
			return "", "", writeErr
		}
	}
	return dir, marker, nil
}

func prepareBashStartupWrapper(startupFiles []string) (dir, marker string, err error) {
	dir, err = os.MkdirTemp("", "personal-mcp-bash-home-*")
	if err != nil {
		return "", "", err
	}
	marker = "__PERSONAL_MCP_READY__" + randomHex(24)
	files := map[string]string{
		".bash_profile": strings.Join(append(startupSourceLines(startupFiles), `printf '\n%s\n' `+shellQuote(marker), ""), "\n"),
		".bashrc":       "",
	}
	for name, body := range files {
		if writeErr := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); writeErr != nil {
			_ = os.RemoveAll(dir)
			return "", "", writeErr
		}
	}
	return dir, marker, nil
}

func startupSourceLines(startupFiles []string) []string {
	lines := make([]string, 0, len(startupFiles))
	for _, file := range startupFiles {
		resolved := resolveStartupFilePath(file)
		lines = append(lines, `[[ -r `+shellQuote(resolved)+` ]] && source `+shellQuote(resolved))
	}
	return lines
}

func resolveStartupFilePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if strings.HasPrefix(trimmed, "~/") || trimmed == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			if trimmed == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
		}
	}
	return trimmed
}
