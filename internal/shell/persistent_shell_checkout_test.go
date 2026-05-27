package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
)

func runPersistentShellCommandWithPID(ctx context.Context, t *testing.T, r *Runner, root, shellPath string, spec config.CommandSpec, argv []string) (pid int, stdout string, exitCode int, err error) {
	t.Helper()

	var truncated bool
	err = r.withPersistentShellSession(ctx, root, shellPath, root, spec, func(sess *persistentShell) {
		if sess != nil && sess.cmd != nil && sess.cmd.Process != nil {
			pid = sess.cmd.Process.Pid
		}
	}, func(sess *persistentShell) error {
		stdout, truncated, exitCode, err = sess.runUnlocked(ctx, root, argv, r.Cfg.Limits.MaxCommandOutputBytes)
		return err
	})
	if truncated {
		t.Fatalf("unexpected truncated output for argv=%q", argv)
	}
	return pid, stdout, exitCode, err
}

func requirePersistentShellTestShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	for _, shellPath := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(shellPath); err == nil {
			return shellPath
		}
	}
	t.Skip("no supported test shell available")
	return ""
}

func persistentShellCheckoutTestRunner(t *testing.T, root, shellPath string, poolSize int) (*Runner, config.CommandSpec) {
	t.Helper()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 3
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	cfg.CommandEnvironment.PersistentShellPoolSize = poolSize
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	spec := config.CommandSpec{Name: "echo", Exec: "/bin/sh", Args: []string{"-c", "printf ok"}, RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}
	return r, spec
}

func shellPoolSnapshot(r *Runner, key string) (sessions, count int) {
	r.shellMu.Lock()
	pool := r.shellPools[key]
	r.shellMu.Unlock()
	if pool == nil {
		return 0, 0
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return len(pool.sessions), pool.count
}

func TestPersistentShellRunReturnsBusyImmediatelyWhenPoolFull(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()

	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = r.withPersistentShellSession(context.Background(), root, shellPath, root, spec, nil, func(_ *persistentShell) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer close(release)

	started := time.Now()
	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("busy persistent shell should be a structured result, got error: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("busy checkout should return immediately, took %s", elapsed)
	}
	result := shellResultMap(t, out)
	if !shellResultBool(t, result, "busy") {
		t.Fatalf("expected busy result, got %#v", result)
	}
	if shellResultBool(t, result, "timed_out") {
		t.Fatalf("busy result should not be timed_out: %#v", result)
	}
	if got, _ := result["busy_phase"].(string); got != "persistent_shell_checkout" {
		t.Fatalf("expected busy_phase persistent_shell_checkout, got %#v", result["busy_phase"])
	}
	if got, _ := result["retryable"].(bool); !got {
		t.Fatalf("expected retryable busy result, got %#v", result)
	}
}

func TestPersistentShellCheckoutReturnsBusyWhenStartupSlotReserved(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, _ := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()

	key := root + "\x00" + shellPath
	pool := r.ensurePersistentPool(key)
	pool.mu.Lock()
	pool.count = 1
	pool.mu.Unlock()

	started := time.Now()
	sess, err := r.checkoutPersistentSession(key)
	elapsed := time.Since(started)
	if sess != nil {
		t.Fatalf("expected no session when startup slot fills pool")
	}
	if !isPersistentShellBusy(err) || !errors.Is(err, errPersistentShellBusy) {
		t.Fatalf("expected persistent shell busy error, got %T %[1]v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("reserved startup slot should return busy without waiting, took %s", elapsed)
	}
	sessions, count := shellPoolSnapshot(r, key)
	if sessions != 0 || count != 1 {
		t.Fatalf("busy checkout should not mutate reserved slot; sessions=%d count=%d", sessions, count)
	}
}

func TestPersistentShellInitFailureCleansReservedSlot(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()

	missingCwd := filepath.Join(root, "missing")
	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, missingCwd, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("init failure should be a structured result, got error: %v", err)
	}
	result := shellResultMap(t, out)
	if got, _ := result["failure_phase"].(string); got != "persistent_shell_init" {
		t.Fatalf("expected persistent_shell_init failure, got %#v", result)
	}
	if got, _ := result["busy"].(bool); got {
		t.Fatalf("init failure should not be busy: %#v", result)
	}

	key := root + "\x00" + shellPath
	sessions, count := shellPoolSnapshot(r, key)
	if sessions != 0 || count != 0 {
		t.Fatalf("failed init should clean pool slot; sessions=%d count=%d", sessions, count)
	}

	if err := os.MkdirAll(missingCwd, 0o750); err != nil {
		t.Fatalf("mkdir missing cwd: %v", err)
	}
	out, err = r.runPersistentShell(context.Background(), spec, spec.Args, missingCwd, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("expected retry after init failure to create a shell: %v", err)
	}
	if stdout := shellResultStdout(t, shellResultMap(t, out)); !strings.Contains(stdout, "ok") {
		t.Fatalf("expected successful retry output, got %q", stdout)
	}
}

func TestPersistentShellCheckoutDropsDeadPooledSession(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, _ := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()

	key := root + "\x00" + shellPath
	pool := r.ensurePersistentPool(key)
	dead := newUninitializedPersistentShell(key)
	dead.setState(persistentShellStateDead)

	pool.mu.Lock()
	pool.sessions = append(pool.sessions, dead)
	pool.count = 1
	pool.mu.Unlock()

	sess, err := r.checkoutPersistentSession(key)
	if err != nil {
		t.Fatalf("checkout after dead pooled session: %v", err)
	}
	if sess == nil {
		t.Fatal("expected replacement session after dropping dead pooled session")
	}
	if state := sess.currentState(); state != persistentShellStateNew {
		t.Fatalf("expected replacement session to start new, got %v", state)
	}

	sessions, count := shellPoolSnapshot(r, key)
	if sessions != 0 || count != 1 {
		t.Fatalf("expected dead pooled session to be dropped and slot replaced; sessions=%d count=%d", sessions, count)
	}
}

func TestPersistentShellReusesReadySessionForSequentialRuns(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 2)
	defer r.ClosePersistentShells()

	argv := []string{"/bin/sh", "-c", "printf reused-ok"}
	pid1, stdout1, exit1, err := runPersistentShellCommandWithPID(context.Background(), t, r, root, shellPath, spec, argv)
	if err != nil {
		t.Fatalf("first persistent-shell run: %v", err)
	}
	if exit1 != 0 || !strings.Contains(stdout1, "reused-ok") {
		t.Fatalf("unexpected first run stdout=%q exit=%d", stdout1, exit1)
	}

	pid2, stdout2, exit2, err := runPersistentShellCommandWithPID(context.Background(), t, r, root, shellPath, spec, argv)
	if err != nil {
		t.Fatalf("second persistent-shell run: %v", err)
	}
	if exit2 != 0 || !strings.Contains(stdout2, "reused-ok") {
		t.Fatalf("unexpected second run stdout=%q exit=%d", stdout2, exit2)
	}
	if pid1 == 0 || pid2 == 0 {
		t.Fatalf("expected shell pids, got pid1=%d pid2=%d", pid1, pid2)
	}
	if pid1 != pid2 {
		t.Fatalf("expected second run to reuse the same shell, got pid1=%d pid2=%d", pid1, pid2)
	}

	sessions, count := shellPoolSnapshot(r, root+"\x00"+shellPath)
	if sessions != 1 || count != 1 {
		t.Fatalf("expected one ready shell reused in pool; sessions=%d count=%d", sessions, count)
	}
}

func TestReturnPersistentSessionDoesNotRequeueDeadSession(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, _ := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()

	key := root + "\x00" + shellPath
	pool := r.ensurePersistentPool(key)
	sess := newUninitializedPersistentShell(key)
	sess.setState(persistentShellStateDead)

	pool.mu.Lock()
	pool.count = 1
	pool.mu.Unlock()

	r.returnPersistentSession(sess)

	sessions, count := shellPoolSnapshot(r, key)
	if sessions != 0 || count != 0 {
		t.Fatalf("expected dead session return to release pool slot; sessions=%d count=%d", sessions, count)
	}
}

func TestPersistentShellTimeoutDropsSessionAndCreatesReplacement(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 2)
	defer r.ClosePersistentShells()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pid1, stdout1, exit1, err := runPersistentShellCommandWithPID(timeoutCtx, t, r, root, shellPath, spec, []string{"/bin/sh", "-c", "printf timeout-partial; sleep 5"})
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if !strings.Contains(stdout1, "timeout-partial") {
		t.Fatalf("expected partial stdout on timeout, got %q", stdout1)
	}
	if exit1 != -1 {
		t.Fatalf("expected unset exit code on timeout, got %d", exit1)
	}
	if pid1 == 0 {
		t.Fatal("expected first shell pid on timeout run")
	}

	sessions, count := shellPoolSnapshot(r, root+"\x00"+shellPath)
	if sessions != 0 || count != 0 {
		t.Fatalf("expected timed-out shell to be dropped from the pool; sessions=%d count=%d", sessions, count)
	}

	pid2, stdout2, exit2, err := runPersistentShellCommandWithPID(context.Background(), t, r, root, shellPath, spec, []string{"/bin/sh", "-c", "printf replacement-ok"})
	if err != nil {
		t.Fatalf("replacement persistent-shell run: %v", err)
	}
	if exit2 != 0 || !strings.Contains(stdout2, "replacement-ok") {
		t.Fatalf("unexpected replacement run stdout=%q exit=%d", stdout2, exit2)
	}
	if pid2 == 0 || pid2 == pid1 {
		t.Fatalf("expected replacement shell pid distinct from timed-out shell, got pid1=%d pid2=%d", pid1, pid2)
	}

	sessions, count = shellPoolSnapshot(r, root+"\x00"+shellPath)
	if sessions != 1 || count != 1 {
		t.Fatalf("expected replacement shell to return to pool; sessions=%d count=%d", sessions, count)
	}
}

func TestPersistentShellStaleSessionIsReplacedByFreshShell(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 2)
	defer r.ClosePersistentShells()

	pid1, stdout1, exit1, err := runPersistentShellCommandWithPID(context.Background(), t, r, root, shellPath, spec, []string{"/bin/sh", "-c", "printf prime-ok"})
	if err != nil {
		t.Fatalf("prime persistent-shell run: %v", err)
	}
	if exit1 != 0 || !strings.Contains(stdout1, "prime-ok") {
		t.Fatalf("unexpected prime run stdout=%q exit=%d", stdout1, exit1)
	}

	key := root + "\x00" + shellPath
	r.shellMu.Lock()
	pool := r.shellPools[key]
	r.shellMu.Unlock()
	if pool == nil {
		t.Fatal("expected persistent shell pool after prime run")
	}
	pool.mu.Lock()
	if len(pool.sessions) != 1 {
		pool.mu.Unlock()
		t.Fatalf("expected one ready session in pool, got %d", len(pool.sessions))
	}
	sess := pool.sessions[0]
	pool.mu.Unlock()

	if err := sess.pty.Close(); err != nil {
		t.Fatalf("close stale PTY: %v", err)
	}

	pid2, stdout2, exit2, err := runPersistentShellCommandWithPID(context.Background(), t, r, root, shellPath, spec, []string{"/bin/sh", "-c", "printf retry-ok"})
	if err != nil {
		t.Fatalf("run after stale session: %v", err)
	}
	if exit2 != 0 || !strings.Contains(stdout2, "retry-ok") {
		t.Fatalf("unexpected retry run stdout=%q exit=%d", stdout2, exit2)
	}
	if pid2 == 0 || pid1 == 0 || pid2 == pid1 {
		t.Fatalf("expected stale session to be replaced, got pid1=%d pid2=%d", pid1, pid2)
	}

	sessions, count := shellPoolSnapshot(r, key)
	if sessions != 1 || count != 1 {
		t.Fatalf("expected one healthy replacement session in pool; sessions=%d count=%d", sessions, count)
	}
}

func TestPersistentShellRunDoesNotWaitForLockedSessionEvenWithLongAcquireTimeout(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()
	r.Cfg.CommandEnvironment.PersistentShellAcquireTimeoutSeconds = 30

	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = r.withPersistentShellSession(context.Background(), root, shellPath, root, spec, nil, func(_ *persistentShell) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer close(release)

	started := time.Now()
	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("busy persistent shell should return a structured result: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("checkout appears to have waited despite nonblocking behavior; took %s", elapsed)
	}
	if !shellResultBool(t, shellResultMap(t, out), "busy") {
		t.Fatalf("expected busy result, got %#v", out)
	}
}

func TestStartNamedPersistentShellConfiguredJobStillUsesBackgroundExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh command unavailable on Windows by default")
	}
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 5
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	cfg.Commands = []config.CommandSpec{{Name: "foreground-shell-job", Exec: "sh", Args: []string{"-c", "printf job-ok"}, Cwd: root, RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()

	started, err := r.StartNamed(json.RawMessage(`{"name":"foreground-shell-job"}`))
	if err != nil {
		t.Fatalf("start named job: %v", err)
	}
	startMap, ok := started.(StartNamedResult)
	if !ok {
		t.Fatalf("expected StartNamedResult, got %T", started)
	}
	waitForJobStatus(t, r, startMap.JobID, "exited")
	read, err := r.JobRead(json.RawMessage(fmt.Sprintf(`{"job_id":%q,"tail_bytes":2000}`, startMap.JobID)))
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	result := shellResultMap(t, read)
	if got, _ := result["run_mode"].(string); got != "background_exec" {
		t.Fatalf("expected background job to use background_exec, got %#v", result)
	}
	if got, _ := result["configured_run_mode"].(string); got != "persistent_shell" {
		t.Fatalf("expected configured run mode metadata, got %#v", result)
	}
	if stdout, _ := result["stdout_tail"].(string); !strings.Contains(stdout, "job-ok") {
		t.Fatalf("expected job output, got %#v", result)
	}

	if sessions, count := shellPoolSnapshot(r, root+"\x00"+shellPath); sessions != 0 || count != 0 {
		t.Fatalf("background job should not use foreground persistent shell pool; sessions=%d count=%d", sessions, count)
	}
}
