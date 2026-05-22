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
	spec := config.CommandSpec{Name: "echo", Exec: "/bin/sh", Args: []string{"-c", "printf ok"}, RunMode: "persistent_shell", Shell: shellPath}
	return r, spec
}

func shellPoolSnapshot(r *Runner, key string) (sessions, starting int) {
	r.shellMu.Lock()
	pool := r.shellPools[key]
	r.shellMu.Unlock()
	if pool == nil {
		return 0, 0
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return len(pool.sessions), pool.starting
}

func TestPersistentShellRunReturnsBusyImmediatelyWhenPoolFull(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()

	sess, err := r.persistentSession(context.Background(), root, shellPath, root, spec)
	if err != nil {
		t.Fatalf("create persistent shell: %v", err)
	}
	if err := sess.lockRun(context.Background()); err != nil {
		t.Fatalf("lock persistent shell: %v", err)
	}
	defer sess.unlockRun()

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
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()

	key := root + "\x00" + shellPath
	pool := r.persistentPool(key)
	pool.mu.Lock()
	pool.starting = 1
	pool.mu.Unlock()

	started := time.Now()
	sess, err := r.checkoutPersistentSession(context.Background(), root, shellPath, root, spec)
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
	sessions, starting := shellPoolSnapshot(r, key)
	if sessions != 0 || starting != 1 {
		t.Fatalf("busy checkout should not mutate reserved slot; sessions=%d starting=%d", sessions, starting)
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
	sessions, starting := shellPoolSnapshot(r, key)
	if sessions != 0 || starting != 0 {
		t.Fatalf("failed init should clean pool slot; sessions=%d starting=%d", sessions, starting)
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

func TestPersistentShellRunDoesNotWaitForLockedSessionEvenWithLongAcquireTimeout(t *testing.T) {
	shellPath := requirePersistentShellTestShell(t)
	root := t.TempDir()
	r, spec := persistentShellCheckoutTestRunner(t, root, shellPath, 1)
	defer r.ClosePersistentShells()
	r.Cfg.CommandEnvironment.PersistentShellAcquireTimeoutSeconds = 30

	sess, err := r.persistentSession(context.Background(), root, shellPath, root, spec)
	if err != nil {
		t.Fatalf("create persistent shell: %v", err)
	}
	if err := sess.lockRun(context.Background()); err != nil {
		t.Fatalf("lock persistent shell: %v", err)
	}
	defer sess.unlockRun()

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
	cfg.Commands = []config.CommandSpec{{Name: "foreground-shell-job", Exec: "sh", Args: []string{"-c", "printf job-ok"}, Cwd: root, RunMode: "persistent_shell", Shell: shellPath}}
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

	if sessions, starting := shellPoolSnapshot(r, root+"\x00"+shellPath); sessions != 0 || starting != 0 {
		t.Fatalf("background job should not use foreground persistent shell pool; sessions=%d starting=%d", sessions, starting)
	}
}
