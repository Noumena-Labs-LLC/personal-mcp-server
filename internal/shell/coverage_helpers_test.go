package shell

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
)

func TestRunnerHelperBranches(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	projectRoot := projectDir
	if canonical, err := filepath.EvalSymlinks(projectDir); err == nil {
		projectRoot = canonical
	}
	if err := os.WriteFile(filepath.Join(projectDir, project.DefaultFilename), []byte(project.Starter("coverage")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "allowed.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "nested", "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := shellTestConfig(root)
	cfg.Limits.MaxCommandOutputBytes = 10000
	cfg.ProjectConfigs.Enabled = true
	cfg.ProjectConfigs.AutoLoad = true
	mgr, err := project.NewManager(cfg, fsx.NewSandbox(cfg))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, mgr)

	if spec, source, state, ok := r.lookupNamed("echo", ""); !ok || source != "global" || state.Found {
		t.Fatalf("lookupNamed(global) = %v %q %#v", ok, source, state)
	} else if spec.Name != "echo" {
		t.Fatalf("lookupNamed(global) returned wrong spec: %#v", spec)
	}
	if spec, source, state, ok := r.lookupNamed("test", projectRoot); !ok || source != "project" || !state.Found || !state.Trusted {
		t.Fatalf("lookupNamed(project) = %v %q %#v", ok, source, state)
	} else if spec.Exec != "just" {
		t.Fatalf("lookupNamed(project) returned wrong spec: %#v", spec)
	}

	if cwd, source, err := r.effectiveCommandCwd("call-cwd", config.CommandSpec{Name: "named", Cwd: "ignored"}, "global", project.State{}); err != nil || cwd != "call-cwd" || source != "tool_call" {
		t.Fatalf("effectiveCommandCwd tool_call = %q %q %v", cwd, source, err)
	}
	if _, _, err := r.effectiveCommandCwd("", config.CommandSpec{Name: "named"}, "global", project.State{}); err == nil {
		t.Fatal("expected empty configured cwd to fail")
	}
	if _, _, err := r.effectiveCommandCwd("", config.CommandSpec{Name: "named", Cwd: string([]byte{0})}, "global", project.State{}); err == nil {
		t.Fatal("expected NUL cwd to fail")
	}
	if cwd, source, err := r.effectiveCommandCwd("", config.CommandSpec{Name: "named", Cwd: "nested"}, "project", project.State{Found: true, Root: projectRoot}); err != nil || cwd != filepath.Join(projectRoot, "nested") || source != "command_config" {
		t.Fatalf("effectiveCommandCwd project relative = %q %q %v", cwd, source, err)
	}
	if _, _, err := r.effectiveCommandCwd("", config.CommandSpec{Name: "named", Cwd: filepath.Join(projectRoot, "nested")}, "project", project.State{Found: true, Root: projectRoot}); err == nil {
		t.Fatal("expected absolute project cwd to fail")
	}
	if _, _, err := r.effectiveCommandCwd("", config.CommandSpec{Name: "named", Cwd: "nested"}, "project", project.State{}); err == nil {
		t.Fatal("expected missing project root to fail")
	}

	enumSpec := config.CommandSpec{
		Name:           "enum",
		AllowExtraArgs: true,
		MaxExtraArgs:   1,
		ExtraArgs:      []config.ExtraArgRule{{Kind: "enum", Values: []string{"ok"}}},
	}
	regexSpec := config.CommandSpec{
		Name:           "regex",
		AllowExtraArgs: true,
		ExtraArgs:      []config.ExtraArgRule{{Kind: "regex", Pattern: "^a+$"}},
	}
	pathSpec := config.CommandSpec{
		Name:           "path",
		AllowExtraArgs: true,
		ExtraArgs:      []config.ExtraArgRule{{Kind: "path", MustExist: true, MustBeInsideProject: true, AllowGlobs: []string{"nested/**"}}},
	}
	if got := effectiveMaxExtraArgs(enumSpec); got != 1 {
		t.Fatalf("effectiveMaxExtraArgs = %d", got)
	}
	if args, err := r.finalCommandArgs(enumSpec, nil, ".", project.State{}); err != nil || len(args) != 0 {
		t.Fatalf("finalCommandArgs no extra = %v %v", args, err)
	}
	if _, err := r.finalCommandArgs(enumSpec, []string{"x"}, ".", project.State{}); err == nil {
		t.Fatal("expected disallowed extra args to fail")
	}
	if _, err := r.finalCommandArgs(enumSpec, []string{"one", "two"}, ".", project.State{}); err == nil {
		t.Fatal("expected max extra args limit to fail")
	}
	if _, err := r.finalCommandArgs(enumSpec, []string{"bad\x00arg"}, ".", project.State{}); err == nil {
		t.Fatal("expected NUL extra arg to fail")
	}
	if !r.extraArgAllowed("ok", enumSpec, projectRoot, project.State{Found: true, Root: projectRoot}) {
		t.Fatal("expected enum extra arg to be allowed")
	}
	if !r.extraArgAllowed("aaa", regexSpec, projectDir, project.State{}) {
		t.Fatal("expected regex extra arg to be allowed")
	}
	if !r.extraArgAllowed(filepath.Join("nested", "inside.txt"), pathSpec, projectRoot, project.State{Found: true, Root: projectRoot}) {
		t.Fatal("expected path extra arg to be allowed")
	}
	if r.extraArgAllowed("--flag", pathSpec, projectDir, project.State{}) {
		t.Fatal("expected shell-like flag to be rejected by path rule")
	}

	if out, err := r.RunArgv(json.RawMessage(`{"exec":"printf","args":["argv"],"cwd":"."}`)); err != nil {
		t.Fatal(err)
	} else if got := shellResultStdout(t, shellResultMap(t, out)); !strings.Contains(got, "argv") {
		t.Fatalf("RunArgv allow = %q", got)
	}
	if _, err := r.RunArgv(json.RawMessage(`{"exec":"printf;rm","args":["argv"],"cwd":"."}`)); err == nil {
		t.Fatal("expected shell syntax in exec to fail")
	}
	if _, err := r.RunArgv(json.RawMessage(`{"exec":"printf","args":["bad\u0000arg"],"cwd":"."}`)); err == nil {
		t.Fatal("expected NUL arg to fail")
	}

	denyCfg := shellTestConfig(root)
	denyCfg.CommandPolicy = config.CommandPolicyConfig{Default: "deny"}
	denyRunner := NewRunner(denyCfg, fsx.NewSandbox(denyCfg), nil, nil)
	if _, err := denyRunner.RunArgv(json.RawMessage(`{"exec":"printf","args":["argv"],"cwd":"."}`)); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected command policy deny, got %v", err)
	}

	promptCfg := shellTestConfig(root)
	promptCfg.CommandPolicy = config.CommandPolicyConfig{Default: "prompt"}
	promptRunner := NewRunner(promptCfg, fsx.NewSandbox(promptCfg), nil, nil)
	if _, err := promptRunner.RunArgv(json.RawMessage(`{"exec":"printf","args":["argv"],"cwd":"."}`)); err == nil || !strings.Contains(err.Error(), "approval is disabled") {
		t.Fatalf("expected prompt policy to require approval, got %v", err)
	}
}

func TestOutputAndPersistentShellErrorHelpers(t *testing.T) {
	limits := normalizeJobOutputLimits(jobOutputLimits{})
	if limits.MaxLines == 0 || limits.MaxLineBytes == 0 || limits.MaxTailBytes == 0 || limits.ChannelSize == 0 {
		t.Fatalf("normalizeJobOutputLimits returned zero values: %#v", limits)
	}
	if dst, short := appendLimitedBytes(nil, []byte("abc"), 0, false); len(dst) != 0 || !short {
		t.Fatalf("appendLimitedBytes zero limit = %q %v", dst, short)
	}
	if dst, short := appendLimitedBytes([]byte("ab"), []byte("cdef"), 3, false); string(dst) != "def" || !short {
		t.Fatalf("appendLimitedBytes src trim = %q %v", dst, short)
	}
	if dst, short := appendLimitedBytes([]byte("abc"), []byte("d"), 3, false); string(dst) != "bcd" || !short {
		t.Fatalf("appendLimitedBytes dst trim = %q %v", dst, short)
	}
	ring := newOutputLineRing(0)
	if len(ring.lines) == 0 {
		t.Fatal("newOutputLineRing should allocate default capacity")
	}
	ring.add(outputLine{Text: "one"})
	ring.add(outputLine{Text: "two"})
	if snap := ring.snapshot(); len(snap.Lines) == 0 {
		t.Fatalf("expected ring snapshot to contain lines: %#v", snap)
	}
	if got := (persistentShellWriteError{err: os.ErrClosed}).Error(); got == "" || !strings.Contains(got, "file already closed") {
		t.Fatalf("unexpected persistentShellWriteError: %q", got)
	}
	if got := (persistentShellBusyError{err: os.ErrDeadlineExceeded}).Error(); got == "" || !strings.Contains(got, "persistent shell busy") {
		t.Fatalf("unexpected persistentShellBusyError: %q", got)
	}
	if got := (persistentShellWriteError{err: os.ErrClosed}).Unwrap(); !errors.Is(got, os.ErrClosed) {
		t.Fatalf("unexpected persistentShellWriteError unwrap: %v", got)
	}
	var p *persistentShell
	p.kill()
	p.waitAfterKill()
	if got := randomHex(4); len(got) != 8 {
		t.Fatalf("randomHex length = %d, want 8", len(got))
	}
}
