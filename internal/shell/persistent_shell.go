package shell

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

type persistentShell struct {
	key     string
	cmd     *exec.Cmd
	pty     *os.File
	runLock chan struct{}
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

func persistentShellEnv(spec config.CommandSpec) []string {
	env := os.Environ()
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

func newPersistentShell(ctx context.Context, key, shellPath, cwd string, spec config.CommandSpec) (*persistentShell, error) {
	master, slave, err := openPty()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, shellPath, "-li") //nolint:gosec // shellPath is restricted by command_environment.allowed_shells and persistent shell mode is trusted-project opt-in.
	cmd.Dir = cwd
	cmd.Env = persistentShellEnv(spec)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = shellSysProcAttr()
	}
	if err := cmd.Start(); err != nil {
		_ = master.Close()
		_ = slave.Close()
		return nil, err
	}
	_ = slave.Close()
	if err := syscall.SetNonblock(int(master.Fd()), true); err != nil {
		shell := newPersistentShellHandle(key, cmd, master)
		shell.kill()
		shell.waitAfterKill()
		return nil, fmt.Errorf("set persistent shell PTY nonblocking: %w", err)
	}
	shell := newPersistentShellHandle(key, cmd, master)
	if err := shell.waitReady(ctx); err != nil {
		shell.kill()
		shell.waitAfterKill()
		return nil, err
	}
	return shell, nil
}

func (p *persistentShell) waitReady(ctx context.Context) error {
	sentinel := "__PERSONAL_MCP_READY_" + randomHex(12) + "__"
	// Print the ready marker with the same framed format used for command
	// completion, but keep the sentinel and status separated in the input text.
	// Some PTYs echo shell input even after stty -echo is requested; splitting the
	// printf arguments prevents echoed startup input from containing a valid
	// sentinel:<digits> frame that could be mistaken for shell readiness.
	if err := p.writeString(ctx, "stty -echo 2>/dev/null || true\nprintf '\n%s:%s\n' "+shellQuote(sentinel)+" 0\n"); err != nil {
		return err
	}
	readyOutput, readyTruncated, readyExitCode, err := p.readUntilSentinel(ctx, sentinel, 0)
	_ = readyOutput
	_ = readyTruncated
	_ = readyExitCode
	if err != nil {
		return fmt.Errorf("persistent shell startup marker not observed: %w", err)
	}
	return nil
}

func newPersistentShellHandle(key string, cmd *exec.Cmd, pty *os.File) *persistentShell {
	return &persistentShell{key: key, cmd: cmd, pty: pty, runLock: make(chan struct{}, 1)}
}

func (p *persistentShell) lockRun(ctx context.Context) error {
	select {
	case p.runLock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *persistentShell) tryLockRun() bool {
	select {
	case p.runLock <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *persistentShell) unlockRun() {
	select {
	case <-p.runLock:
	default:
	}
}

func (p *persistentShell) run(ctx context.Context, cwd string, argv []string, maxOutput int64) (output string, truncated bool, exitCode int, err error) {
	return p.runWithLockContext(ctx, ctx, cwd, argv, maxOutput)
}

func (p *persistentShell) runWithLockContext(lockCtx, runCtx context.Context, cwd string, argv []string, maxOutput int64) (output string, truncated bool, exitCode int, err error) {
	if err := p.lockRun(lockCtx); err != nil {
		return "", false, -1, persistentShellBusyError{err: err}
	}
	return p.runLocked(runCtx, cwd, argv, maxOutput)
}

func (p *persistentShell) runLocked(runCtx context.Context, cwd string, argv []string, maxOutput int64) (output string, truncated bool, exitCode int, err error) {
	defer p.unlockRun()
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return "", false, -1, errors.New("persistent shell already exited")
	}
	sentinel := "__PERSONAL_MCP_DONE_" + randomHex(12) + "__"
	script := strings.Join([]string{
		"cd " + shellQuote(cwd),
		"__personal_mcp_server_status=$?",
		"if [ \"$__personal_mcp_server_status\" -eq 0 ]; then " + shellQuoteJoin(argv) + "; __personal_mcp_server_status=$?; fi",
		"printf '\\n" + sentinel + ":%s\\n' \"$__personal_mcp_server_status\"",
	}, "\n") + "\n"
	if err := p.writeString(runCtx, script); err != nil {
		return "", false, -1, persistentShellWriteError{err: err}
	}
	return p.readUntilSentinel(runCtx, sentinel, maxOutput)
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
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if p.pty != nil {
		_ = p.pty.Close()
	}
	if runtime.GOOS == "windows" {
		_ = p.cmd.Process.Kill()
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.cmd.Process.Kill()
}

func (p *persistentShell) waitAfterKill() {
	if p == nil || p.cmd == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
