package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

func testManager(t *testing.T, root, body string, trusted bool) *Manager {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, DefaultFilename), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Roots: []string{root},
		ProjectConfigs: config.ProjectConfigSettings{
			Enabled:    true,
			Filename:   DefaultFilename,
			TrustStore: filepath.Join(t.TempDir(), "trusted-projects.toml"),
		},
	}
	if trusted {
		cfg.ProjectConfigs.TrustedProjects = []string{root}
	}
	m, err := NewManager(cfg, fsx.NewSandbox(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDecideProjectFileProtectedAndGenerated(t *testing.T) {
	root := t.TempDir()
	m := testManager(t, root, `config_kind = "project"
config_version = 1

[project]
name = "demo"

[protected_files]
deny_edit = ["dist/**"]
prompt_edit = ["go.mod"]

[generated]
paths = ["generated/**"]
default_action = "deny"
`, true)

	cases := []struct {
		path   string
		action string
		rule   string
	}{
		{"dist/app.js", policy.ActionDeny, "project:protected_files.deny_edit"},
		{"generated/client.pb.go", policy.ActionDeny, "project:generated.paths"},
		{"go.mod", policy.ActionPrompt, "project:protected_files.prompt_edit"},
	}
	for _, tc := range cases {
		resolved := filepath.Join(root, filepath.FromSlash(tc.path))
		decision, details, matched, err := m.DecideProjectFile("patch", tc.path, resolved)
		if err != nil {
			t.Fatal(err)
		}
		if !matched {
			t.Fatalf("expected %s to match", tc.path)
		}
		if decision.Action != tc.action || decision.Rule != tc.rule {
			t.Fatalf("%s: got %#v", tc.path, decision)
		}
		if details["relative_path"] != tc.path {
			t.Fatalf("expected relative path detail, got %#v", details)
		}
	}
}

func TestDecideProjectFileRequiresTrust(t *testing.T) {
	root := t.TempDir()
	m := testManager(t, root, `config_kind = "project"
config_version = 1

[project]
name = "demo"

[protected_files]
deny_edit = ["dist/**"]
`, false)
	decision, _, matched, err := m.DecideProjectFile("patch", "dist/app.js", filepath.Join(root, "dist", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if matched || decision.Action != "" {
		t.Fatalf("untrusted project policy should not match, got matched=%v decision=%#v", matched, decision)
	}
}

func TestDecideProjectFilePolicyRule(t *testing.T) {
	root := t.TempDir()
	m := testManager(t, root, `config_kind = "project"
config_version = 1

[project]
name = "demo"

[[file_policy.rules]]
name = "allow-doc-patches"
action = "allow"
operations = ["patch"]
pattern = "^docs/.*\\.md$"
`, true)
	decision, _, matched, err := m.DecideProjectFile("patch", "docs/guide.md", filepath.Join(root, "docs", "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !matched || decision.Action != policy.ActionAllow || decision.Rule != "project:allow-doc-patches" {
		t.Fatalf("expected project allow rule, got matched=%v decision=%#v", matched, decision)
	}
}

func TestDiscoverRefreshesTrustStoreChanges(t *testing.T) {
	root := t.TempDir()
	m := testManager(t, root, `config_kind = "project"
config_version = 1

[project]
name = "demo"

[[commands]]
name = "test"
exec = "just"
args = ["test"]
`, false)
	state := m.Discover(root)
	if !state.Found {
		t.Fatal("expected project config to be discovered")
	}
	if state.Trusted {
		t.Fatal("project should start untrusted")
	}
	trustBody := `[[projects]]
root = "` + filepath.ToSlash(root) + `"
config = "` + filepath.ToSlash(filepath.Join(root, DefaultFilename)) + `"
trusted = true
trusted_at = "test"
`
	if err := os.WriteFile(m.TrustPath, []byte(trustBody), 0o600); err != nil {
		t.Fatalf("write trust store: %v", err)
	}
	state = m.Discover(root)
	if !state.Trusted {
		t.Fatalf("expected trust-store change to be visible without new manager, got %#v", state)
	}
}

func TestTrustStoreSaveIsAtomicAndReadable(t *testing.T) {
	root := t.TempDir()
	m := testManager(t, root, `config_kind = "project"
config_version = 1

[project]
name = "demo"
`, false)
	entry, err := m.Trust(root)
	if err != nil {
		t.Fatalf("trust: %v", err)
	}
	if !entry.Trusted {
		t.Fatal("trusted entry not marked trusted")
	}
	b, err := os.ReadFile(m.TrustPath) //nolint:gosec // test-owned trust store.
	if err != nil {
		t.Fatalf("read trust store: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("trust store is empty")
	}
	state := m.Discover(root)
	if !state.Trusted {
		t.Fatalf("saved trust store did not reload as trusted: %#v", state)
	}
}

func TestLoadRejectsPersistentShellBashWithoutStartupFiles(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, DefaultFilename)
	body := `config_kind = "project"
config_version = 1

[project]
name = "demo"

[command_environment]
run_mode = "persistent_shell"
shell = "/bin/bash"

[[commands]]
name = "pytest"
exec = "python3"
args = ["-m", "pytest"]
`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(configPath)
	if err == nil || err.Error() != `project command "pytest" run_mode persistent_shell with bash requires startup_files` {
		t.Fatalf("expected bash startup_files validation error, got %v", err)
	}
}

func TestLoadRejectsPersistentShellZshWithoutStartupFiles(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, DefaultFilename)
	body := `config_kind = "project"
config_version = 1

[project]
name = "demo"

[command_environment]
run_mode = "persistent_shell"
shell = "/bin/zsh"

[[commands]]
name = "pytest"
exec = "python3"
args = ["-m", "pytest"]
`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(configPath)
	if err == nil || err.Error() != `project command "pytest" run_mode persistent_shell with zsh requires startup_files` {
		t.Fatalf("expected zsh startup_files validation error, got %v", err)
	}
}
