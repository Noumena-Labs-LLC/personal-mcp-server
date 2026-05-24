package project

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
)

func TestManagerTrustWorkflowAndHelpers(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONAL_MCP_TOKEN", "project-token")
	configPath := filepath.Join(root, "config.toml")
	configBody := `config_version = 1
roots = [` + strconv.Quote(root) + `]

[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_env = "PERSONAL_MCP_TOKEN"
validate_origin = true

[limits]
max_read_bytes = 100000
max_search_results = 100
max_search_file_bytes = 1000000
command_timeout_seconds = 20
max_command_output_bytes = 100000

[project_configs]
enabled = true
filename = ".personal-mcp-server.toml"
auto_load = false
trust_store = ` + strconv.Quote(filepath.Join(root, "trusted-projects.toml")) + `
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, DefaultFilename), []byte(Starter("coverage-project")), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	sandbox := fsx.NewSandbox(cfg)
	mgr, err := NewManager(cfg, sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if !Enabled(cfg) {
		t.Fatal("expected project configs to be enabled")
	}
	if mgr == nil || mgr.Filename != DefaultFilename {
		t.Fatalf("NewManager = %#v", mgr)
	}
	if got := mgr.ListTrusted(); len(got) != 0 {
		t.Fatalf("ListTrusted = %#v, want empty", got)
	}

	state := mgr.Discover(projectDir)
	if !state.Found || state.Config == nil {
		t.Fatalf("Discover = %#v", state)
	}
	if !strings.Contains(Marshal(state), `"found": true`) {
		t.Fatalf("Marshal(state) missing found flag")
	}
	wf := mgr.WorkflowInfo(projectDir)
	if wf["enabled"] != true {
		t.Fatalf("WorkflowInfo = %#v", wf)
	}
	info := mgr.EffectiveInfo(projectDir, true)
	if _, ok := info["project_commands"]; !ok {
		t.Fatalf("EffectiveInfo missing project_commands: %#v", info)
	}

	entry, err := mgr.Trust(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if !entry.Trusted || entry.Root == "" || entry.Config == "" {
		t.Fatalf("Trust = %#v", entry)
	}
	trusted := mgr.ListTrusted()
	if len(trusted) != 1 || trusted[0].Root != entry.Root {
		t.Fatalf("ListTrusted after trust = %#v", trusted)
	}
	if !mgr.IsTrusted(projectDir) {
		t.Fatal("expected project to be trusted")
	}
	if err := mgr.Untrust(projectDir); err != nil {
		t.Fatal(err)
	}
	if mgr.IsTrusted(projectDir) {
		t.Fatal("expected project to be untrusted after Untrust")
	}
	if got := defaultTrustStorePath(); !strings.Contains(got, ".config") {
		t.Fatalf("defaultTrustStorePath = %q", got)
	}
	tmpHome := filepath.Join(root, "home")
	if err := os.MkdirAll(tmpHome, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmpHome)
	if got := defaultTrustStorePath(); got != filepath.Join(tmpHome, ".config", "personal-mcp-server", "trusted-projects.toml") {
		t.Fatalf("defaultTrustStorePath with HOME = %q", got)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	if got := mustGetwd(); !strings.HasSuffix(got, filepath.ToSlash(filepath.Base(projectDir))) {
		t.Fatalf("mustGetwd = %q, want suffix %q", got, filepath.Base(projectDir))
	}
	if got := Starter(""); !strings.Contains(got, `name = "project"`) {
		t.Fatalf("Starter fallback name = %q", got)
	}
	if got := Marshal(map[string]int{"x": 1}); !strings.Contains(got, `"x": 1`) {
		t.Fatalf("Marshal(map) = %q", got)
	}
}
