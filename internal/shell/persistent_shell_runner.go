package shell

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
)

type persistentShellPool struct {
	key      string
	mu       sync.Mutex
	sessions []*persistentShell
	count    int
}

var errPersistentShellBusy = errors.New("persistent shell busy")

const (
	defaultPersistentShellStartupTimeout = 30 * time.Second
	defaultPersistentShellQuietPeriod    = time.Second
)

func (r *Runner) runPersistentShell(parentCtx context.Context, spec config.CommandSpec, args []string, cwd, source string, state project.State, extra map[string]any) (any, error) {
	if runtime.GOOS == "windows" {
		return nil, errors.New("persistent_shell run_mode is not supported on Windows")
	}
	if source != "project" || !state.Trusted {
		return nil, fmt.Errorf("command %q run_mode persistent_shell is only allowed for trusted project commands", spec.Name)
	}
	if !r.Cfg.CommandEnvironment.AllowPersistentShell {
		return nil, fmt.Errorf("command %q requires run_mode persistent_shell, but command_environment.allow_persistent_shell is false", spec.Name)
	}
	shellPath := strings.TrimSpace(spec.Shell)
	if shellPath == "" {
		return nil, fmt.Errorf("command %q run_mode persistent_shell requires shell", spec.Name)
	}
	if !r.shellAllowed(shellPath) {
		return nil, fmt.Errorf("command %q shell %q is not allowed by command_environment.allowed_shells", spec.Name, shellPath)
	}
	for _, file := range spec.StartupFiles {
		if strings.TrimSpace(file) == "" {
			return nil, fmt.Errorf("command %q startup_files cannot contain empty paths", spec.Name)
		}
	}
	if (isBashShell(shellPath) || isZshShell(shellPath)) && len(spec.StartupFiles) == 0 {
		return nil, fmt.Errorf("command %q run_mode persistent_shell with %s requires startup_files", spec.Name, filepath.Base(shellPath))
	}
	ctx, cancel := context.WithTimeout(parentCtx, time.Duration(r.Cfg.Limits.CommandTimeoutSeconds)*time.Second)
	defer cancel()
	started := time.Now()
	extra["shell"] = shellPath
	extra["shell_pool_size"] = r.persistentShellPoolSize()
	argv := append([]string{spec.Exec}, args...)
	var (
		stdout    string
		truncated bool
		exitCode  int
		runErr    error
	)
	err := r.withPersistentShellSession(ctx, state.Root, shellPath, cwd, spec, func(active *persistentShell) {
		extra["shell_session"] = active.key
	}, func(active *persistentShell) error {
		stdout, truncated, exitCode, runErr = active.runUnlocked(ctx, cwd, argv, r.Cfg.Limits.MaxCommandOutputBytes)
		return runErr
	})
	if err != nil {
		if isPersistentShellBusy(err) {
			return r.persistentShellBusyResult(started, extra), nil
		}
		if runErr == nil {
			return r.persistentShellInitFailureResult(started, err, extra), nil
		}
	}
	timedOut := errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled)
	if runErr != nil {
		if !timedOut {
			return nil, runErr
		}
	}
	result := map[string]any{
		"exit_code": exitCode, "timed_out": timedOut, "duration_ms": time.Since(started).Milliseconds(), "configured_timeout_seconds": r.Cfg.Limits.CommandTimeoutSeconds,
		"stdout": stripTerminalEscapes(stdout), "stderr": "", "stdout_truncated": truncated, "stderr_truncated": false,
	}
	if timedOut {
		result["timeout_phase"] = "command_run"
		result["timeout_guidance"] = commandTimeoutGuidance()
	}
	for k, v := range extra {
		result[k] = v
	}
	return result, nil
}

func (r *Runner) persistentShellBusyResult(started time.Time, extra map[string]any) map[string]any {
	result := map[string]any{
		"exit_code": -1, "ok": false, "busy": true, "retryable": true, "timed_out": false,
		"duration_ms": time.Since(started).Milliseconds(), "configured_timeout_seconds": r.Cfg.Limits.CommandTimeoutSeconds,
		"stdout": "", "stderr": "", "stdout_truncated": false, "stderr_truncated": false,
		"busy_phase": "persistent_shell_checkout", "message": "persistent shell pool is busy; retry the command later", "shell_pool_size": r.persistentShellPoolSize(),
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

func (r *Runner) persistentShellInitFailureResult(started time.Time, err error, extra map[string]any) map[string]any {
	result := map[string]any{
		"exit_code": -1, "ok": false, "busy": false, "retryable": true,
		"timed_out":   errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled),
		"duration_ms": time.Since(started).Milliseconds(), "configured_timeout_seconds": r.Cfg.Limits.CommandTimeoutSeconds,
		"stdout": "", "stderr": "", "stdout_truncated": false, "stderr_truncated": false,
		"failure_phase": "persistent_shell_init", "error": err.Error(), "shell_pool_size": r.persistentShellPoolSize(),
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

func isPersistentShellBusy(runErr error) bool {
	var busyErr persistentShellBusyError
	return errors.As(runErr, &busyErr)
}

func shouldRetryPersistentShellRun(ctx context.Context, runErr error, timedOut bool) bool {
	if runErr == nil || timedOut || ctx.Err() != nil {
		return false
	}
	var writeErr persistentShellWriteError
	return errors.As(runErr, &writeErr)
}

func (r *Runner) shellAllowed(shellPath string) bool {
	allowed := r.Cfg.CommandEnvironment.AllowedShells
	if len(allowed) == 0 {
		allowed = []string{"/bin/zsh", "/bin/bash", "/bin/sh"}
	}
	for _, candidate := range allowed {
		if shellPath == candidate {
			return true
		}
	}
	return false
}

func (r *Runner) persistentShellPoolSize() int {
	if r.Cfg != nil && r.Cfg.CommandEnvironment.PersistentShellPoolSize > 0 {
		return r.Cfg.CommandEnvironment.PersistentShellPoolSize
	}
	return 2
}

func (r *Runner) persistentShellStartupTimeout() time.Duration {
	if r.Cfg != nil && r.Cfg.CommandEnvironment.PersistentShellStartupTimeoutSeconds > 0 {
		return time.Duration(r.Cfg.CommandEnvironment.PersistentShellStartupTimeoutSeconds) * time.Second
	}
	return defaultPersistentShellStartupTimeout
}

func (r *Runner) persistentShellQuietPeriod() time.Duration {
	if r.Cfg != nil && r.Cfg.CommandEnvironment.PersistentShellQuietPeriodMs > 0 {
		return time.Duration(r.Cfg.CommandEnvironment.PersistentShellQuietPeriodMs) * time.Millisecond
	}
	return defaultPersistentShellQuietPeriod
}

func (r *Runner) persistentSession(ctx context.Context, root, shellPath, cwd string, spec config.CommandSpec) (*persistentShell, error) {
	var session *persistentShell
	err := r.withPersistentShellSession(ctx, root, shellPath, cwd, spec, nil, func(sess *persistentShell) error {
		session = sess
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (r *Runner) ensurePersistentPool(key string) *persistentShellPool {
	r.shellMu.Lock()
	defer r.shellMu.Unlock()
	if r.shellPools == nil {
		r.shellPools = map[string]*persistentShellPool{}
	}
	pool := r.shellPools[key]
	if pool == nil {
		pool = &persistentShellPool{key: key}
		r.shellPools[key] = pool
	}
	return pool
}

func (r *Runner) checkoutPersistentSessionCore(key string) (sess *persistentShell, discarded []*persistentShell, err error) {
	pool := r.ensurePersistentPool(key)

	pool.mu.Lock()
	defer pool.mu.Unlock()
	// invariant: every session in the pool is in the persistentShellStateReady state.
	for len(pool.sessions) > 0 {
		sess = pool.sessions[0]
		pool.sessions = pool.sessions[1:]
		if state := sess.currentState(); state != persistentShellStateReady {
			slog.Warn("persistent shell checkout observed non-ready pooled session",
				"shell_session", sess.key,
				"state", state,
				"remaining_ready_sessions", len(pool.sessions),
				"pool_count", pool.count,
			)
			discarded = append(discarded, sess)
			releasePersistentShellPoolSlotLocked(pool)
			continue
		}
		return sess, discarded, nil
	}
	if pool.count >= r.persistentShellPoolSize() {
		return nil, discarded, persistentShellBusyError{err: errPersistentShellBusy}
	}
	pool.count++
	return newUninitializedPersistentShell(key), discarded, nil
}

func (r *Runner) checkoutPersistentSession(key string) (*persistentShell, error) {
	sess, discarded, err := r.checkoutPersistentSessionCore(key)
	killPersistentShells(discarded)
	return sess, err
}

func (r *Runner) returnPersistentSession(sess *persistentShell) {
	if sess == nil {
		return
	}
	r.shellMu.Lock()
	pool := r.shellPools[sess.key]
	r.shellMu.Unlock()

	if pool == nil {
		slog.Error("failed to return persistent shell session to pool", "shell_session", sess.key, "reason", "pool_not_found", "state", sess.currentState())
		return
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()
	switch sess.currentState() {
	case persistentShellStateDead:
		slog.Warn("persistent shell session returned dead; releasing pool slot", "shell_session", sess.key, "count_before", pool.count)
		releasePersistentShellPoolSlotLocked(pool)
	case persistentShellStateNew:
		slog.Warn("persistent shell session returned uninitialized; releasing pool slot", "shell_session", sess.key, "count_before", pool.count)
		releasePersistentShellPoolSlotLocked(pool)
	// invariant: every session in the pool is in the persistentShellStateReady state.
	case persistentShellStateReady:
		pool.sessions = append(pool.sessions, sess)
	case persistentShellStateInUse:
		if !sess.transitionState(persistentShellStateInUse, persistentShellStateReady) {
			state := sess.currentState()
			if state == persistentShellStateDead {
				slog.Warn("persistent shell died before pool return completed; releasing pool slot", "shell_session", sess.key, "count_before", pool.count)
			} else {
				slog.Error("failed to transition persistent shell to ready during pool return", "shell_session", sess.key, "state", state, "count_before", pool.count)
			}
			releasePersistentShellPoolSlotLocked(pool)
			return
		}
		pool.sessions = append(pool.sessions, sess)
	default:
		slog.Error("failed to return persistent shell session to pool", "shell_session", sess.key, "reason", "unexpected_state", "state", sess.currentState())
		releasePersistentShellPoolSlotLocked(pool)
	}
}

func releasePersistentShellPoolSlotLocked(pool *persistentShellPool) {
	if pool != nil && pool.count > 0 {
		pool.count--
	}
}

func killPersistentShells(sessions []*persistentShell) {
	for _, sess := range sessions {
		if sess != nil {
			sess.kill()
		}
	}
}

func (r *Runner) withPersistentShellSession(ctx context.Context, root, shellPath, cwd string, spec config.CommandSpec, onSession func(*persistentShell), fn func(*persistentShell) error) error {
	key := root + "\x00" + shellPath
	for attempt := 0; attempt < 2; attempt++ {
		runErr, retry, err := func() (error, bool, error) {
			sess, err := r.checkoutPersistentSession(key)
			if err != nil {
				return nil, false, err
			}
			defer r.returnPersistentSession(sess)
			switch sess.currentState() {
			case persistentShellStateReady:
			case persistentShellStateNew:
				if err := sess.initialize(ctx, r.persistentShellStartupTimeout(), r.persistentShellQuietPeriod(), key, shellPath, cwd, spec); err != nil {
					sess.setState(persistentShellStateDead)
					return nil, false, err
				}
			case persistentShellStateInUse, persistentShellStateDead:
				return nil, false, fmt.Errorf("unexpected persistent shell state %d from checkout", sess.currentState())
			}
			if onSession != nil {
				onSession(sess)
			}
			sess.setState(persistentShellStateInUse)
			runErr := fn(sess)
			if shouldKillPersistentShellSession(runErr) {
				sess.kill()
			}
			timedOut := errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled)
			return runErr, shouldRetryPersistentShellRun(ctx, runErr, timedOut), nil
		}()
		if err != nil {
			return err
		}
		if retry {
			continue
		}
		return runErr
	}
	return nil
}

func shouldKillPersistentShellSession(err error) bool {
	if err == nil {
		return false
	}
	var writeErr persistentShellWriteError
	if errors.As(err, &writeErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// ClosePersistentShells terminates any cached persistent shell sessions owned by the runner.
func (r *Runner) ClosePersistentShells() {
	r.shellMu.Lock()
	pools := make([]*persistentShellPool, 0, len(r.shellPools))
	for _, pool := range r.shellPools {
		pools = append(pools, pool)
	}
	r.shellPools = map[string]*persistentShellPool{}
	r.shellMu.Unlock()

	var sessions []*persistentShell
	for _, pool := range pools {
		pool.mu.Lock()
		sessions = append(sessions, pool.sessions...)
		pool.sessions = nil
		pool.count = 0
		pool.mu.Unlock()
	}
	for _, sess := range sessions {
		sess.kill()
	}
}
