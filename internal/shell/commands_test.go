package shell

import (
	"context"
	"encoding/json"
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

func shellTestConfig(root string) *config.Config {
	return &config.Config{
		Roots:         []string{root},
		Limits:        config.LimitsConfig{MaxReadBytes: 1024, MaxWriteBytes: 1024, MaxSearchResults: 10, MaxSearchFileBytes: 1024, CommandTimeoutSeconds: 1, MaxCommandOutputBytes: 4},
		Commands:      []config.CommandSpec{{Name: "echo", Exec: "printf", Args: []string{"abcdef"}}},
		CommandPolicy: config.CommandPolicyConfig{Default: "deny", Rules: []config.CommandPolicyRule{{Name: "allow printf", Action: "allow", Exec: "printf", ArgsRegex: ".*"}}},
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks for %q: %v", path, err)
	}
	return resolved
}

func shellResultMap(t *testing.T, out any) map[string]any {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", out)
	}
	return m
}

func shellResultStdout(t *testing.T, m map[string]any) string {
	t.Helper()
	v, ok := m["stdout"].(string)
	if !ok {
		t.Fatalf("expected stdout to be string, got %T", m["stdout"])
	}
	return v
}

func shellResultBool(t *testing.T, m map[string]any, key string) bool {
	t.Helper()
	v, ok := m[key].(bool)
	if !ok {
		t.Fatalf("expected %q to be bool, got %T", key, m[key])
	}
	return v
}

func TestRunNamedCapsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	out, err := r.RunNamed(json.RawMessage(`{"name":"echo","cwd":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	m := shellResultMap(t, out)
	if got := shellResultStdout(t, m); got != "abcd" {
		t.Fatalf("expected capped stdout abcd, got %q", got)
	}
	if trunc := shellResultBool(t, m, "stdout_truncated"); !trunc {
		t.Fatal("expected stdout_truncated")
	}
}

func TestRunNamedUsesConfiguredCwdWhenCallOmitsCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pwd command unavailable on Windows by default")
	}
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o750); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	cfg := shellTestConfig(root)
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.Commands = []config.CommandSpec{{Name: "pwd", Exec: "pwd", Cwd: subdir}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	out, err := r.RunNamed(json.RawMessage(`{"name":"pwd"}`))
	if err != nil {
		t.Fatal(err)
	}
	m := shellResultMap(t, out)
	want := canonicalTestPath(t, subdir)
	got := canonicalTestPath(t, strings.TrimSpace(shellResultStdout(t, m)))
	if got != want {
		t.Fatalf("expected configured cwd %q, got %q", want, got)
	}
	if got := m["cwd_source"]; got != "command_config" {
		t.Fatalf("expected command_config cwd_source, got %#v", got)
	}
}

func TestRunNamedToolCallCwdOverridesConfiguredCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pwd command unavailable on Windows by default")
	}
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(subdir, 0o750); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.MkdirAll(other, 0o750); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}
	cfg := shellTestConfig(root)
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.Commands = []config.CommandSpec{{Name: "pwd", Exec: "pwd", Cwd: subdir}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	raw := json.RawMessage(fmt.Sprintf(`{"name":"pwd","cwd":%q}`, other))
	out, err := r.RunNamed(raw)
	if err != nil {
		t.Fatal(err)
	}
	m := shellResultMap(t, out)
	want := canonicalTestPath(t, other)
	got := canonicalTestPath(t, strings.TrimSpace(shellResultStdout(t, m)))
	if got != want {
		t.Fatalf("expected tool-call cwd %q, got %q", want, got)
	}
	if got := m["cwd_source"]; got != "tool_call" {
		t.Fatalf("expected tool_call cwd_source, got %#v", got)
	}
}

func TestRunNamedRejectsCwdOutsideRoot(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	_, err := r.RunNamed(json.RawMessage(`{"name":"echo","cwd":".."}`))
	if err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("expected cwd rejection, got %v", err)
	}
}

func TestRunNamedTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Commands = []config.CommandSpec{{Name: "sleep", Exec: "sleep", Args: []string{"5"}}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	out, err := r.RunNamed(json.RawMessage(`{"name":"sleep","cwd":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	m := shellResultMap(t, out)
	if !shellResultBool(t, m, "timed_out") {
		t.Fatal("expected timeout")
	}
	if got, ok := m["configured_timeout_seconds"].(int); !ok || got != cfg.Limits.CommandTimeoutSeconds {
		t.Fatalf("expected configured timeout metadata, got %#v", m["configured_timeout_seconds"])
	}
	if got, ok := m["timeout_phase"].(string); !ok || got == "" {
		t.Fatalf("expected timeout phase, got %#v", m["timeout_phase"])
	}
}

func TestRunArgvPolicyAllow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	out, err := r.RunArgv(json.RawMessage(`{"exec":"printf","args":["abcdef"],"cwd":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := shellResultStdout(t, shellResultMap(t, out)); got != "abcd" {
		t.Fatalf("expected capped stdout abcd, got %q", got)
	}
}

func TestListNamedIncludesExtraArgMetadata(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Commands = []config.CommandSpec{{
		Name:           "pytest",
		Exec:           "python",
		Args:           []string{"-m", "pytest"},
		Description:    "run pytest",
		AllowExtraArgs: true,
		MaxExtraArgs:   3,
		ExtraArgs:      []config.ExtraArgRule{{Kind: "enum", Values: []string{"-q"}}},
	}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	out, err := r.ListNamed(json.RawMessage(`{"include_args":true}`))
	if err != nil {
		t.Fatalf("list named: %v", err)
	}
	m := shellResultMap(t, out)
	// ListNamed returns a local struct type; assert through formatted output to avoid coupling to that unexported type.
	text := fmt.Sprintf("%#v", m["global_commands"])
	if !strings.Contains(text, "AllowExtraArgs:true") || !strings.Contains(text, "MaxExtraArgs:3") {
		t.Fatalf("expected extra-arg metadata in list output, got %s", text)
	}
}

func TestFinalCommandArgsExtraArgRules(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	spec := config.CommandSpec{
		Name:           "pytest",
		Args:           []string{"-m", "pytest"},
		AllowExtraArgs: true,
		MaxExtraArgs:   3,
		ExtraArgs: []config.ExtraArgRule{
			{Kind: "enum", Values: []string{"-q"}},
			{Kind: "regex", Pattern: `^-k=.+$`},
		},
	}
	args, err := r.finalCommandArgs(spec, []string{"-q", "-k=test_name"}, ".", project.State{})
	if err != nil {
		t.Fatalf("valid extra args: %v", err)
	}
	if got := strings.Join(args, " "); got != "-m pytest -q -k=test_name" {
		t.Fatalf("unexpected args %q", got)
	}
	if _, err := r.finalCommandArgs(spec, []string{"--unsafe"}, ".", project.State{}); err == nil {
		t.Fatal("expected invalid extra arg rejection")
	}
	noExtra := spec
	noExtra.AllowExtraArgs = false
	if _, err := r.finalCommandArgs(noExtra, []string{"-q"}, ".", project.State{}); err == nil {
		t.Fatal("expected extra args disabled rejection")
	}
}

func TestListNamedOmitsMaxExtraArgsWhenDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Commands = []config.CommandSpec{{Name: "pytest", Exec: "python", Args: []string{"-m", "pytest"}, Description: "run pytest"}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	out, err := r.ListNamed(json.RawMessage(`{"include_args":true}`))
	if err != nil {
		t.Fatalf("list named: %v", err)
	}
	text := fmt.Sprintf("%#v", shellResultMap(t, out)["global_commands"])
	if strings.Contains(text, "MaxExtraArgs:10") || strings.Contains(text, "max_extra_args:10") {
		t.Fatalf("extra args are disabled; list output should not show max_extra_args=10: %s", text)
	}
}

func TestPersistentShellRunModeRequiresGlobalOptIn(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	spec := config.CommandSpec{Name: "pwd", Exec: "pwd", RunMode: "persistent_shell", Shell: "/bin/sh"}
	_, err := r.runPersistentShell(context.Background(), spec, nil, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "allow_persistent_shell") {
		t.Fatalf("expected persistent shell opt-in error, got %v", err)
	}
}

func persistentShellTestStartupFiles(shellPath string) []string {
	if override := strings.TrimSpace(os.Getenv("PERSONAL_MCP_TEST_STARTUP_FILE")); override != "" {
		return []string{override}
	}
	if isBashShell(shellPath) || isZshShell(shellPath) {
		return []string{"/dev/null"}
	}
	return nil
}

func persistentShellTestMaxOutput() int64 {
	if strings.TrimSpace(os.Getenv("PERSONAL_MCP_TEST_STARTUP_FILE")) != "" {
		return 32 * 1024
	}
	return 1024
}

func TestPersistentShellRunsInRequestedCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skip("/bin/bash unavailable")
	}
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o750); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 3
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()
	spec := config.CommandSpec{Name: "pwd", Exec: "pwd", RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}
	out, err := r.runPersistentShell(context.Background(), spec, nil, subdir, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("run persistent shell: %v", err)
	}
	stdout := shellResultStdout(t, shellResultMap(t, out))
	if !strings.Contains(stdout, subdir) {
		t.Fatalf("expected stdout to contain cwd %q, got %q", subdir, stdout)
	}
}

func TestPersistentShellRunsWithZsh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/zsh"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skip("/bin/zsh unavailable")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 5
	cfg.Limits.MaxCommandOutputBytes = 20000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()
	spec := config.CommandSpec{Name: "pwd", Exec: "pwd", RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}
	out, err := r.runPersistentShell(context.Background(), spec, nil, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("run persistent shell zsh: %v", err)
	}
	result := shellResultMap(t, out)
	if got, _ := result["duration_ms"].(int64); got == 0 {
		t.Fatalf("expected duration metadata, got %#v", result)
	} else if got > (30*time.Second + time.Second).Milliseconds() {
		t.Fatalf("expected zsh startup result within startup timeout window, got %#v", result)
	}
	if failurePhase, _ := result["failure_phase"].(string); failurePhase != "" {
		errText, _ := result["error"].(string)
		if !strings.Contains(errText, "zsh compinit confirmation") && !strings.Contains(errText, "startup marker not observed") {
			t.Fatalf("expected zsh startup failure to explain compinit prompt, got %#v", result)
		}
		return
	}
	stdout := shellResultStdout(t, result)
	if !strings.Contains(stdout, root) {
		t.Fatalf("expected stdout to contain cwd %q, got %q", root, stdout)
	}
}

func TestPersistentShellInitThenFirstCommandTiming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/zsh"
	if _, err := os.Stat(shellPath); err != nil {
		if _, bashErr := os.Stat("/bin/bash"); bashErr != nil {
			t.Skip("no supported test shell available")
		}
		shellPath = "/bin/bash"
	}

	root := t.TempDir()
	spec := config.CommandSpec{
		Name:         "hello",
		Exec:         "/bin/sh",
		Args:         []string{"-c", "printf 'hello world'"},
		RunMode:      "persistent_shell",
		Shell:        shellPath,
		StartupFiles: persistentShellTestStartupFiles(shellPath),
	}

	started := time.Now()
	sess := newUninitializedPersistentShell(root + "\x00" + shellPath)
	err := sess.initialize(context.Background(), defaultPersistentShellStartupTimeout, defaultPersistentShellQuietPeriod, root+"\x00"+shellPath, shellPath, root, spec)
	initElapsed := time.Since(started)
	if err != nil {
		t.Fatalf("create persistent shell: %v", err)
	}
	defer func() {
		sess.kill()
	}()

	runStarted := time.Now()
	output, truncated, exitCode, err := sess.run(context.Background(), root, []string{"/bin/sh", "-c", "printf 'hello world'"}, persistentShellTestMaxOutput())
	runElapsed := time.Since(runStarted)
	if err != nil {
		t.Fatalf("run first command after init: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected zero exit code, got %d with output %q", exitCode, output)
	}
	if !strings.Contains(output, "hello world") {
		t.Fatalf("expected first command output to contain %q, got %q", "hello world", output)
	}
	t.Logf("persistent shell timing shell=%s init=%s first_command=%s total=%s truncated=%v", shellPath, initElapsed, runElapsed, initElapsed+runElapsed, truncated)
	t.Logf("persistent shell first command raw output: %q", output)
}

func TestPersistentShellCapturesPartialOutputWithoutNewlineOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skip("/bin/bash unavailable")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 1
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	cfg.CommandEnvironment.PersistentShellQuietPeriodMs = 100
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()
	spec := config.CommandSpec{Name: "partial", Exec: "/bin/sh", Args: []string{"-c", "printf partial-output; sleep 5"}, RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}
	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("run persistent shell: %v", err)
	}
	result := shellResultMap(t, out)
	if timedOut, _ := result["timed_out"].(bool); !timedOut {
		t.Fatalf("expected timeout result, got %#v", result)
	}
	stdout := shellResultStdout(t, result)
	if !strings.Contains(stdout, "partial-output") {
		t.Fatalf("expected stdout to contain partial output before newline, got %q", stdout)
	}
}

func TestPersistentShellHandlesUnsafeArgumentBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		if _, shErr := os.Stat("/bin/sh"); shErr != nil {
			t.Skip("no supported test shell available")
		}
		shellPath = "/bin/sh"
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 3
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()

	payload := "subject line\n\nbody line\x1b[118;1:3u"
	spec := config.CommandSpec{
		Name:         "hex-arg",
		Exec:         "/bin/sh",
		Args:         []string{"-c", `printf %s "$1" | od -An -tx1 | tr -d ' \n'`, "sh", payload},
		RunMode:      "persistent_shell",
		Shell:        shellPath,
		StartupFiles: persistentShellTestStartupFiles(shellPath),
	}
	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("run persistent shell: %v", err)
	}
	result := shellResultMap(t, out)
	if timedOut, _ := result["timed_out"].(bool); timedOut {
		t.Fatalf("expected unsafe-byte argument to complete without timeout, got %#v", result)
	}
	wantHex := fmt.Sprintf("%x", payload)
	if got := strings.TrimSpace(shellResultStdout(t, result)); !strings.Contains(got, wantHex) {
		t.Fatalf("expected payload hex %q in output, got %q", wantHex, got)
	}
}

func TestPersistentShellStagesLongMultilineArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/zsh"
	if _, err := os.Stat(shellPath); err != nil {
		if _, bashErr := os.Stat("/bin/bash"); bashErr != nil {
			t.Skip("no supported interactive test shell available")
		}
		shellPath = "/bin/bash"
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 5
	cfg.Limits.MaxCommandOutputBytes = 20000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()

	bodyLines := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		bodyLines = append(bodyLines, fmt.Sprintf("line %03d: bullets - unicode ok - 'quoted' text", i))
	}
	payload := "subject line\n\n" + strings.Join(bodyLines, "\n")
	spec := config.CommandSpec{
		Name:         "long-hex-arg",
		Exec:         "/bin/sh",
		Args:         []string{"-c", `printf %s "$1" | od -An -tx1 | tr -d ' \n'`, "sh", payload},
		RunMode:      "persistent_shell",
		Shell:        shellPath,
		StartupFiles: persistentShellTestStartupFiles(shellPath),
	}
	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("run persistent shell: %v", err)
	}
	result := shellResultMap(t, out)
	if timedOut, _ := result["timed_out"].(bool); timedOut {
		t.Fatalf("expected long multiline argument to complete without timeout, got %#v", result)
	}
	wantHex := fmt.Sprintf("%x", payload)
	if got := strings.TrimSpace(shellResultStdout(t, result)); !strings.Contains(got, wantHex) {
		t.Fatalf("expected payload hex %q in output, got %q", wantHex, got)
	}
}

func TestPersistentShellRunLockHonorsContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skip("/bin/bash unavailable")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 3
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()
	spec := config.CommandSpec{Name: "echo", Exec: "/bin/sh", Args: []string{"-c", "printf ok"}, RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}
	sess, err := r.persistentSession(context.Background(), root, shellPath, root, spec)
	if err != nil {
		t.Fatalf("create persistent shell: %v", err)
	}
	out, truncated, exitCode, err := sess.run(context.Background(), root, []string{"/bin/sh", "-c", "printf after-busy"}, 10000)
	if err != nil {
		t.Fatalf("expected shell to remain usable: %v", err)
	}
	if truncated || exitCode != 0 || !strings.Contains(out, "after-busy") {
		t.Fatalf("unexpected run output=%q truncated=%v exit=%d", out, truncated, exitCode)
	}
}

func TestPersistentShellPoolCreatesSecondSessionWhenFirstBusy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skip("/bin/bash unavailable")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 3
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	cfg.CommandEnvironment.PersistentShellPoolSize = 2
	cfg.CommandEnvironment.PersistentShellAcquireTimeoutSeconds = 1
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()
	spec := config.CommandSpec{Name: "echo", Exec: "/bin/sh", Args: []string{"-c", "printf pool-ok"}, RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}
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

	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("run persistent shell with busy first session: %v", err)
	}
	stdout := shellResultStdout(t, shellResultMap(t, out))
	if !strings.Contains(stdout, "pool-ok") {
		t.Fatalf("expected stdout to contain pool output, got %q", stdout)
	}
	key := root + "\x00" + shellPath
	r.shellMu.Lock()
	pool := r.shellPools[key]
	r.shellMu.Unlock()
	poolLen := 0
	poolCount := 0
	if pool != nil {
		pool.mu.Lock()
		poolLen = len(pool.sessions)
		poolCount = pool.count
		pool.mu.Unlock()
	}
	if poolLen != 1 || poolCount != 2 {
		t.Fatalf("expected pool to contain one ready session and track two total sessions, got sessions=%d count=%d", poolLen, poolCount)
	}
}

func TestPersistentShellRetriesStaleSessionWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persistent shell mode is Unix-only")
	}
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		t.Skip("/bin/bash unavailable")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 3
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.CommandEnvironment.AllowPersistentShell = true
	cfg.CommandEnvironment.AllowedShells = []string{shellPath}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	defer r.ClosePersistentShells()
	spec := config.CommandSpec{Name: "echo", Exec: "/bin/sh", Args: []string{"-c", "printf retry-ok"}, RunMode: "persistent_shell", Shell: shellPath, StartupFiles: persistentShellTestStartupFiles(shellPath)}
	if _, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{}); err != nil {
		t.Fatalf("prime persistent shell: %v", err)
	}

	key := root + "\x00" + shellPath
	r.shellMu.Lock()
	pool := r.shellPools[key]
	r.shellMu.Unlock()
	var sess *persistentShell
	if pool != nil {
		pool.mu.Lock()
		if len(pool.sessions) > 0 {
			sess = pool.sessions[0]
		}
		pool.mu.Unlock()
	}
	if sess == nil {
		t.Fatalf("expected cached persistent shell session")
	}
	if err := sess.pty.Close(); err != nil {
		t.Fatalf("close cached PTY: %v", err)
	}

	out, err := r.runPersistentShell(context.Background(), spec, spec.Args, root, "project", project.State{Found: true, Trusted: true, Root: root}, map[string]any{})
	if err != nil {
		t.Fatalf("expected retry to recover stale persistent shell: %v", err)
	}
	stdout := shellResultStdout(t, shellResultMap(t, out))
	if !strings.Contains(stdout, "retry-ok") {
		t.Fatalf("expected stdout to contain retry output, got %q", stdout)
	}
}

func TestEffectiveCommandSpecUsesProjectCommandEnvironmentDefaults(t *testing.T) {
	state := project.State{Found: true, Trusted: true, Config: &project.Config{CommandEnv: project.CommandEnvironment{RunMode: "persistent_shell", Shell: "/bin/zsh", StartupFiles: []string{"~/.zshrc"}}}}
	spec := effectiveCommandSpec(config.CommandSpec{Name: "test", Exec: "make"}, "project", state)
	if spec.RunMode != "persistent_shell" {
		t.Fatalf("expected project default run_mode, got %q", spec.RunMode)
	}
	if spec.Shell != "/bin/zsh" {
		t.Fatalf("expected project default shell, got %q", spec.Shell)
	}
	if len(spec.StartupFiles) != 1 || spec.StartupFiles[0] != "~/.zshrc" {
		t.Fatalf("expected project default startup_files, got %#v", spec.StartupFiles)
	}
	override := effectiveCommandSpec(config.CommandSpec{Name: "lint", Exec: "make", RunMode: "argv", Shell: "/bin/bash"}, "project", state)
	if override.RunMode != "argv" || override.Shell != "/bin/bash" {
		t.Fatalf("expected command override to win, got run_mode=%q shell=%q", override.RunMode, override.Shell)
	}
}

func TestStartNamedJobCompletesAndCanBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.Commands = []config.CommandSpec{{Name: "echo", Exec: "printf", Args: []string{"job-output"}}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	started, err := r.StartNamed(json.RawMessage(`{"name":"echo","cwd":"."}`))
	if err != nil {
		t.Fatalf("start named job: %v", err)
	}
	startMap, ok := started.(StartNamedResult)
	if !ok {
		t.Fatalf("expected StartNamedResult, got %T", started)
	}
	if startMap.JobID == "" {
		t.Fatal("expected job id")
	}
	waitForJobStatus(t, r, startMap.JobID, "exited")
	read, err := r.JobRead(json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, startMap.JobID)))
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	m := shellResultMap(t, read)
	if got, _ := m["stdout_tail"].(string); !strings.Contains(got, "job-output") {
		t.Fatalf("expected job output, got %#v", m)
	}
}

func TestStartNamedJobCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 10
	cfg.Commands = []config.CommandSpec{{Name: "sleep", Exec: "sleep", Args: []string{"5"}}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	started, err := r.StartNamed(json.RawMessage(`{"name":"sleep","cwd":"."}`))
	if err != nil {
		t.Fatalf("start named job: %v", err)
	}
	startMap, ok := started.(StartNamedResult)
	if !ok {
		t.Fatalf("expected StartNamedResult, got %T", started)
	}
	jobID := startMap.JobID
	if jobID == "" {
		t.Fatal("expected job id")
	}
	if _, err := r.JobCancel(json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, jobID))); err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	waitForJobStatus(t, r, jobID, "cancelled")
}

func waitForJobStatus(t *testing.T, r *Runner, jobID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		status, err := r.JobStatus(json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, jobID)))
		if err != nil {
			t.Fatalf("job status: %v", err)
		}
		m := shellResultMap(t, status)
		if got, _ := m["status"].(string); got == want {
			return
		}
		<-ticker.C
	}
	status, _ := r.JobStatus(json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, jobID)))
	t.Fatalf("job did not reach status %q; last status %#v", want, status)
}

func TestFinalCommandArgsAllowsExtraArgsWithoutRules(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	spec := config.CommandSpec{
		Name:           "pytest",
		Args:           []string{"-m", "pytest"},
		AllowExtraArgs: true,
		MaxExtraArgs:   3,
	}
	args, err := r.finalCommandArgs(spec, []string{"-k", "test_name"}, ".", project.State{})
	if err != nil {
		t.Fatalf("valid unrestricted extra args: %v", err)
	}
	if got := strings.Join(args, " "); got != "-m pytest -k test_name" {
		t.Fatalf("unexpected args %q", got)
	}
	if _, err := r.finalCommandArgs(spec, []string{"ok", "bad\x00arg"}, ".", project.State{}); err == nil {
		t.Fatal("expected NUL rejection")
	}
}

func TestFinalCommandArgsInsertsExtraArgsAtPlaceholder(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	spec := config.CommandSpec{
		Name:           "rg",
		Args:           []string{"--line-number", extraArgsPlaceholder, "."},
		AllowExtraArgs: true,
		MaxExtraArgs:   2,
	}
	args, err := r.finalCommandArgs(spec, []string{"needle"}, ".", project.State{})
	if err != nil {
		t.Fatalf("valid placeholder extra args: %v", err)
	}
	if got := strings.Join(args, " "); got != "--line-number needle ." {
		t.Fatalf("unexpected args %q", got)
	}

	args, err = r.finalCommandArgs(spec, nil, ".", project.State{})
	if err != nil {
		t.Fatalf("empty extra args with placeholder: %v", err)
	}
	if got := strings.Join(args, " "); got != "--line-number ." {
		t.Fatalf("unexpected args without extra args %q", got)
	}
}

func TestStartNamedRejectsInvalidExtraArgsSynchronously(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Commands = []config.CommandSpec{{
		Name:           "commit",
		Exec:           "printf",
		Args:           []string{"%s"},
		AllowExtraArgs: true,
		MaxExtraArgs:   1,
		ExtraArgs:      []config.ExtraArgRule{{Kind: "regex", Pattern: `^[^\x00]{1,4000}$`}},
	}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	if _, err := r.StartNamed(json.RawMessage(`{"name":"commit","cwd":".","extra_args":["fix filter dispatch"]}`)); err != nil {
		t.Fatalf("valid regex extra arg should be accepted: %v", err)
	}
	if _, err := r.StartNamed(json.RawMessage("{\"name\":\"commit\",\"cwd\":\".\",\"extra_args\":[\"bad\\u0000arg\"]}")); err == nil {
		t.Fatal("expected invalid extra arg rejection")
	}
}

func TestStartNamedJobReadShowsOutputWhileRunning(t *testing.T) {
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.CommandTimeoutSeconds = 5
	cfg.Limits.MaxCommandOutputBytes = 100
	cfg.Commands = []config.CommandSpec{{Name: "slow-output", Exec: "sh", Args: []string{"-c", "printf first; sleep 2; printf second"}}}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	started, err := r.StartNamed(json.RawMessage(`{"name":"slow-output","cwd":"."}`))
	if err != nil {
		t.Fatalf("start named job: %v", err)
	}
	startMap, ok := started.(StartNamedResult)
	if !ok {
		t.Fatalf("expected StartNamedResult, got %T", started)
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		read, err := r.JobRead(json.RawMessage(fmt.Sprintf(`{"job_id":%q,"tail_bytes":2000}`, startMap.JobID)))
		if err != nil {
			t.Fatalf("read job: %v", err)
		}
		m := shellResultMap(t, read)
		if got, _ := m["stdout_tail"].(string); strings.Contains(got, "first") {
			return
		}
		<-ticker.C
	}
	t.Fatalf("expected running job output to be readable")
}
