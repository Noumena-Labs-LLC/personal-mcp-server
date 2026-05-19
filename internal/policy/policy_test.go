package policy

import (
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func TestDecideCommandDenyWins(t *testing.T) {
	decision, err := DecideCommand(config.CommandPolicyConfig{Default: ActionPrompt, Rules: []config.CommandPolicyRule{
		{Name: "allow git", Action: ActionAllow, Exec: "git", ArgsRegex: ".*"},
		{Name: "deny git push", Action: ActionDeny, Exec: "git", Subcommands: []string{"push"}},
	}}, "git", []string{"push", "origin", "main"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionDeny || decision.Rule != "deny git push" {
		t.Fatalf("expected deny git push, got %#v", decision)
	}
}

func TestDecideCommandSubcommandAllow(t *testing.T) {
	decision, err := DecideCommand(config.CommandPolicyConfig{Default: ActionDeny, Rules: []config.CommandPolicyRule{
		{Name: "allow git read", Action: ActionAllow, Exec: "git", Subcommands: []string{"status", "diff"}},
	}}, "git", []string{"status", "--short"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionAllow {
		t.Fatalf("expected allow, got %#v", decision)
	}
}

func TestDecideFileDenyWins(t *testing.T) {
	decision, err := DecideFile(config.FilePolicyConfig{ReadDefault: ActionAllow, Rules: []config.FilePolicyRule{
		{Name: "allow all", Action: ActionAllow, Operations: []string{"read"}, Pattern: ".*"},
		{Name: "deny env", Action: ActionDeny, Operations: []string{"read"}, Pattern: `(^|/)\.env$`},
	}}, "read", ".env", "/tmp/project/.env")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionDeny {
		t.Fatalf("expected deny, got %#v", decision)
	}
}

func TestValidateAction(t *testing.T) {
	for _, action := range []string{"", ActionAllow, ActionDeny, ActionPrompt} {
		if err := ValidateAction(action); err != nil {
			t.Fatalf("ValidateAction(%q) returned %v", action, err)
		}
	}
	if err := ValidateAction("maybe"); err == nil {
		t.Fatalf("expected invalid action error")
	}
}

func TestDecideCommandRegexAndDefaults(t *testing.T) {
	decision, err := DecideCommand(config.CommandPolicyConfig{Default: ActionPrompt, Rules: []config.CommandPolicyRule{
		{Name: "prompt python module", Action: ActionPrompt, ExecRegex: `python[0-9.]*$`, ArgsRegex: `^-m pytest`},
	}}, "/usr/bin/python3", []string{"-m", "pytest", "tests"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionPrompt || decision.Rule != "prompt python module" {
		t.Fatalf("expected prompt regex rule, got %#v", decision)
	}

	decision, err = DecideCommand(config.CommandPolicyConfig{}, "unknown", nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionDeny || decision.Rule != "default" {
		t.Fatalf("expected default deny, got %#v", decision)
	}
}

func TestDecideCommandInvalidRegex(t *testing.T) {
	_, err := DecideCommand(config.CommandPolicyConfig{Rules: []config.CommandPolicyRule{
		{Name: "bad exec", Action: ActionAllow, ExecRegex: "["},
	}}, "git", nil)
	if err == nil {
		t.Fatalf("expected invalid exec_regex error")
	}

	_, err = DecideCommand(config.CommandPolicyConfig{Rules: []config.CommandPolicyRule{
		{Name: "bad args", Action: ActionAllow, ArgsRegex: "["},
	}}, "git", []string{"status"})
	if err == nil {
		t.Fatalf("expected invalid args_regex error")
	}
}

func TestDecideFileDefaultsAndPrompt(t *testing.T) {
	cases := []struct {
		operation string
		want      string
	}{
		{operation: "read", want: ActionAllow},
		{operation: "list", want: ActionAllow},
		{operation: "info", want: ActionAllow},
		{operation: "search", want: ActionAllow},
		{operation: "patch", want: ActionDeny},
		{operation: "unified_patch", want: ActionDeny},
		{operation: "create", want: ActionDeny},
		{operation: "write", want: ActionDeny},
		{operation: "other", want: ActionDeny},
	}
	for _, tc := range cases {
		decision, err := DecideFile(config.FilePolicyConfig{}, tc.operation, "file.txt", "/tmp/file.txt")
		if err != nil {
			t.Fatalf("DecideFile(%q) returned %v", tc.operation, err)
		}
		if decision.Action != tc.want {
			t.Fatalf("DecideFile(%q) action = %q, want %q", tc.operation, decision.Action, tc.want)
		}
	}

	decision, err := DecideFile(config.FilePolicyConfig{PatchDefault: ActionAllow, Rules: []config.FilePolicyRule{
		{Name: "prompt go mod", Action: ActionPrompt, Operations: []string{"patch"}, Pattern: `go\.mod$`},
	}}, "patch", "go.mod", "/tmp/project/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != ActionPrompt || decision.Rule != "prompt go mod" {
		t.Fatalf("expected prompt rule, got %#v", decision)
	}
}

func TestDecideFileInvalidRegex(t *testing.T) {
	_, err := DecideFile(config.FilePolicyConfig{Rules: []config.FilePolicyRule{
		{Name: "bad", Action: ActionAllow, Pattern: "["},
	}}, "read", "file.txt", "/tmp/file.txt")
	if err == nil {
		t.Fatalf("expected invalid file policy regex error")
	}
}

func TestJoinArgsQuotesShellSensitiveValues(t *testing.T) {
	got := JoinArgs([]string{"simple", "two words", "quote'arg", "", `slash\arg`})
	want := `simple 'two words' 'quote'\''arg' '' 'slash\arg'`
	if got != want {
		t.Fatalf("JoinArgs = %q, want %q", got, want)
	}
}

func TestDescribeUsesServerVersion(t *testing.T) {
	const serverVersion = "0.5.3-rc3"
	desc := Describe(&config.Config{}, []string{"/tmp/project"}, serverVersion)
	server, ok := desc["server"].(map[string]any)
	if !ok {
		t.Fatalf("server section missing or wrong type: %#v", desc["server"])
	}
	if got := server["version"]; got != serverVersion {
		t.Fatalf("server.version = %#v, want %q", got, serverVersion)
	}
}

func TestDescribeCWDToolsSeparatesDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.ReadFile.Enabled = true
	cfg.Tools.SearchText.Enabled = true
	desc := Describe(cfg, []string{"/tmp/project"}, "0.5.4-rc1")
	cwd, ok := desc["cwd"].(map[string]any)
	if !ok {
		t.Fatalf("cwd section missing or wrong type: %#v", desc["cwd"])
	}
	enabled, ok := cwd["enabled_tools"].([]string)
	if !ok {
		t.Fatalf("enabled_tools wrong type: %#v", cwd["enabled_tools"])
	}
	if !containsString(enabled, "fs_read_file") || !containsString(enabled, "fs_search_text") {
		t.Fatalf("expected enabled read/search tools, got %#v", enabled)
	}
	disabled, ok := cwd["disabled_tools"].([]map[string]any)
	if !ok {
		t.Fatalf("disabled_tools wrong type: %#v", cwd["disabled_tools"])
	}
	foundFind := false
	for _, tool := range disabled {
		if tool["name"] == "fs_find" {
			foundFind = tool["requires_feature"] == "native_find"
		}
	}
	if !foundFind {
		t.Fatalf("expected disabled fs_find with native_find requirement, got %#v", disabled)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
