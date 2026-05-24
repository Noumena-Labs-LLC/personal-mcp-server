package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
)

func shellRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestShellHelperFunctions(t *testing.T) {
	t.Setenv("HOST_ONE", "value-one")
	if got := shellQuote("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("shellQuote = %q", got)
	}
	if got := shellQuoteJoin([]string{"a b", "c"}); got != `'a b' 'c'` {
		t.Fatalf("shellQuoteJoin = %q", got)
	}
	env := upsertEnv([]string{"A=1", "B=2"}, "A", "3")
	if got := strings.Join(env, ","); !strings.Contains(got, "A=3") {
		t.Fatalf("upsertEnv replace = %v", env)
	}
	env = upsertEnv(env, "C", "4")
	if got := strings.Join(env, ","); !strings.Contains(got, "C=4") {
		t.Fatalf("upsertEnv append = %v", env)
	}
	spec := config.CommandSpec{Env: map[string]string{"X": "1"}, EnvFromHost: []string{"HOST_ONE"}}
	persistent := persistentShellEnv(spec)
	if !containsEnvEntry(persistent, "X=1") || !containsEnvEntry(persistent, "HOST_ONE=value-one") {
		t.Fatalf("persistentShellEnv = %v", persistent)
	}
	if !globAny([]string{"*.txt", "**/*.md"}, "docs/readme.md") {
		t.Fatal("globAny should match recursive glob")
	}
	if globAny([]string{"*.txt"}, "docs/readme.md") {
		t.Fatal("globAny should reject non-matching glob")
	}
	if !containsShellSyntax("echo | cat") || containsShellSyntax("printf") {
		t.Fatal("containsShellSyntax mismatch")
	}
	if got := effectiveMaxExtraArgs(config.CommandSpec{}); got != 10 {
		t.Fatalf("effectiveMaxExtraArgs = %d", got)
	}
	if got := commandRunMode(config.CommandSpec{}); got != "argv" {
		t.Fatalf("commandRunMode = %q", got)
	}
}

func TestRunnerSequenceAndCommandHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helpers are not portable to Windows in this test")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.MaxCommandOutputBytes = 1024
	cfg.CommandPolicy.Default = "allow"
	cfg.Commands = []config.CommandSpec{
		{Name: "good", Exec: "printf", Args: []string{"good"}},
		{Name: "bad", Exec: "false"},
		{Name: "argv", Exec: "printf", Args: []string{"argv"}},
	}
	cfg.CommandSequences = []config.CommandSequenceSpec{
		{Name: "seq", Steps: []config.CommandSequenceStep{{Name: "good"}, {Name: "bad"}, {Name: "good"}}},
		{Name: "continue", Mode: "continue", Steps: []config.CommandSequenceStep{{Name: "good"}, {Name: "bad"}, {Name: "good"}}},
	}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)

	if names := r.availableCommandNames(""); len(names) < 3 {
		t.Fatalf("availableCommandNames = %v", names)
	}
	if _, source, _, ok := r.lookupSequence("seq", ""); !ok || source != "global" {
		t.Fatalf("lookupSequence(global) = %v %q", ok, source)
	}
	if _, source, _, ok := r.lookupNamed("good", ""); !ok || source != "global" {
		t.Fatalf("lookupNamed(global) = %v %q", ok, source)
	}

	out, err := r.RunNamedContext(context.TODO(), shellRaw(t, RunArgs{Name: "good", Cwd: "."}))
	if err != nil {
		t.Fatal(err)
	}
	m := shellResultMap(t, out)
	if got := shellResultStdout(t, m); !strings.Contains(got, "good") {
		t.Fatalf("RunNamedContext stdout = %q", got)
	}

	out, err = r.RunArgvContext(context.TODO(), shellRaw(t, RunArgvArgs{Exec: "printf", Args: []string{"argv"}, Cwd: "."}))
	if err != nil {
		t.Fatal(err)
	}
	m = shellResultMap(t, out)
	if got := shellResultStdout(t, m); !strings.Contains(got, "argv") {
		t.Fatalf("RunArgvContext stdout = %q", got)
	}

	seqOut, err := r.RunSequence(shellRaw(t, SequenceArgs{Name: "seq", Cwd: "."}))
	if err != nil {
		t.Fatal(err)
	}
	seq := shellResultMap(t, seqOut)
	if ok, _ := seq["ok"].(bool); ok {
		t.Fatalf("expected seq to fail, got %#v", seq)
	}
	if steps, ok := seq["steps"].([]map[string]any); !ok || len(steps) != 2 {
		t.Fatalf("expected stop_on_failure sequence to stop after second step, got %#v", seq["steps"])
	}

	contOut, err := r.RunSequence(shellRaw(t, SequenceArgs{Name: "continue", Cwd: "."}))
	if err != nil {
		t.Fatal(err)
	}
	cont := shellResultMap(t, contOut)
	if ok, _ := cont["ok"].(bool); ok {
		t.Fatalf("expected continue sequence to report failure, got %#v", cont)
	}
	if steps, ok := cont["steps"].([]map[string]any); !ok || len(steps) != 3 {
		t.Fatalf("expected continue sequence to run all steps, got %#v", cont["steps"])
	}
}

func TestRunnerJobList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helpers are not portable to Windows in this test")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Limits.MaxCommandOutputBytes = 1024
	cfg.CommandPolicy.Default = "allow"
	cfg.Commands = []config.CommandSpec{
		{Name: "job", Exec: "/bin/sh", Args: []string{"-c", "printf job-output"}},
	}
	r := NewRunner(cfg, fsx.NewSandbox(cfg), nil, nil)
	startOut, err := r.StartNamed(shellRaw(t, RunArgs{Name: "job", Cwd: "."}))
	if err != nil {
		t.Fatal(err)
	}
	start, ok := startOut.(StartNamedResult)
	if !ok {
		t.Fatalf("expected StartNamedResult, got %T", startOut)
	}
	waitForJobStatus(t, r, start.JobID, "exited")
	listOut, err := r.JobList(shellRaw(t, JobListArgs{IncludeFinished: true}))
	if err != nil {
		t.Fatal(err)
	}
	list := shellResultMap(t, listOut)
	items, ok := list["jobs"].([]map[string]any)
	if !ok || len(items) == 0 {
		t.Fatalf("JobList output = %#v", list)
	}
	if !strings.Contains(fmt.Sprint(listOut), start.JobID) {
		t.Fatalf("JobList output missing job id %q: %#v", start.JobID, list)
	}
}

func containsEnvEntry(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}
