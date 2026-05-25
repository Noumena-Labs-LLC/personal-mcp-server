package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func minimalConfig(root string) string {
	return `config_version = 1
roots = ["` + strings.ReplaceAll(root, `\`, `\\`) + `"]
[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_env = "PERSONAL_MCP_TOKEN"
validate_origin = true
allowed_origins = ["http://127.0.0.1"]
[limits]
max_read_bytes = 10
max_write_bytes = 10
max_search_results = 10
max_search_file_bytes = 10
command_timeout_seconds = 1
max_command_output_bytes = 10
[secrets]
deny_names = [".env"]
deny_extensions = [".pem"]
`
}

func TestLoadRejectsRemoteBind(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfg := strings.Replace(minimalConfig(root), `host = "127.0.0.1"`, `host = "0.0.0.0"`, 1)
	_, err := Load(writeConfig(t, root, cfg))
	if err == nil || !strings.Contains(err.Error(), "non-localhost") {
		t.Fatalf("expected non-localhost rejection, got %v", err)
	}
}

func TestLoadRequiresToken(t *testing.T) {
	root := t.TempDir()
	_, err := Load(writeConfig(t, root, minimalConfig(root)))
	if err == nil || !strings.Contains(err.Error(), "auth token") {
		t.Fatalf("expected token error, got %v", err)
	}
}

func TestLoadRejectsShellSyntaxInExec(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfg := minimalConfig(root) + `
[[commands]]
name = "bad"
exec = "sh;rm"
args = []
`
	_, err := Load(writeConfig(t, root, cfg))
	if err == nil || !strings.Contains(err.Error(), "shell syntax") {
		t.Fatalf("expected shell syntax error, got %v", err)
	}
}

func TestLoadAcceptsTokenFile(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "token")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgBody := strings.Replace(minimalConfig(root), `auth_token_env = "PERSONAL_MCP_TOKEN"`, `auth_token_file = "`+strings.ReplaceAll(tokenPath, `\`, `\\`)+`"`, 1)
	cfg, err := Load(writeConfig(t, root, cfgBody))
	if err != nil {
		t.Fatalf("expected token file config to load: %v", err)
	}
	if got := cfg.AuthToken(); got != "file-token" {
		t.Fatalf("expected token from file, got %q", got)
	}
}

func TestLoadRequiresConfigVersion(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfg := strings.Replace(minimalConfig(root), "config_version = 1\n", "", 1)
	_, err := Load(writeConfig(t, root, cfg))
	if err == nil || !strings.Contains(err.Error(), "config_version") {
		t.Fatalf("expected config_version error, got %v", err)
	}
}

func TestLoadRejectsDuplicateCommands(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfg := minimalConfig(root) + `
[[commands]]
name = "dupe"
exec = "git"
args = []
[[commands]]
name = "dupe"
exec = "git"
args = []
`
	_, err := Load(writeConfig(t, root, cfg))
	if err == nil || !strings.Contains(err.Error(), "duplicate command") {
		t.Fatalf("expected duplicate command error, got %v", err)
	}
}

func TestAllowEverythingDefaultsArePermissive(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfgBody := minimalConfig(root) + "\n[defaults]\nallow_everything = true\n"
	cfg, err := Load(writeConfig(t, root, cfgBody))
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}
	if cfg.Approval.Enabled {
		t.Fatal("expected approval to default disabled")
	}
	if cfg.CommandPolicy.Default != "allow" {
		t.Fatalf("expected command default allow, got %q", cfg.CommandPolicy.Default)
	}
	for name, action := range map[string]string{
		"read":    cfg.FilePolicy.ReadDefault,
		"write":   cfg.FilePolicy.WriteDefault,
		"create":  cfg.FilePolicy.CreateDefault,
		"patch":   cfg.FilePolicy.PatchDefault,
		"unified": cfg.FilePolicy.UnifiedPatchDefault,
	} {
		if action != "allow" {
			t.Fatalf("expected %s default allow, got %q", name, action)
		}
	}
}

func TestAllowEverythingPreservesExplicitDenies(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfgBody := minimalConfig(root) + "\n[defaults]\nallow_everything = true\n" + `
[approval]
enabled = true
[command_policy]
default = "deny"
[file_policy]
write_default = "deny"
`
	cfg, err := Load(writeConfig(t, root, cfgBody))
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}
	if !cfg.Approval.Enabled {
		t.Fatal("expected explicit approval enabled to be preserved")
	}
	if cfg.CommandPolicy.Default != "deny" {
		t.Fatalf("expected explicit command deny, got %q", cfg.CommandPolicy.Default)
	}
	if cfg.FilePolicy.WriteDefault != "deny" {
		t.Fatalf("expected explicit write deny, got %q", cfg.FilePolicy.WriteDefault)
	}
	if cfg.FilePolicy.CreateDefault != "allow" {
		t.Fatalf("expected absent create default allow, got %q", cfg.FilePolicy.CreateDefault)
	}
}

func TestLoadAcceptsCamelCaseAliases(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfgBody := minimalConfig(root) + `
[Defaults]
AllowEverything = true
[Approval]
Enabled = true
[[Commands]]
Name = "echo"
Exec = "echo"
Args = ["ok"]
AllowExtraArgs = true
MaxExtraArgs = 2
RunMode = "exec"
Cwd = "."
[[Commands.ExtraArgs]]
Kind = "literal"
Values = ["--help"]
`
	cfg, err := Load(writeConfig(t, root, cfgBody))
	if err != nil {
		t.Fatalf("expected CamelCase aliases to load: %v", err)
	}
	if !cfg.Defaults.AllowEverything {
		t.Fatal("expected AllowEverything alias to set defaults.allow_everything")
	}
	if !cfg.Approval.Enabled {
		t.Fatal("expected Approval.Enabled alias to be preserved")
	}
	if len(cfg.Commands) != 1 {
		t.Fatalf("expected one command, got %d", len(cfg.Commands))
	}
	cmd := cfg.Commands[0]
	if !cmd.AllowExtraArgs || cmd.MaxExtraArgs != 2 || cmd.RunMode != "argv" || cmd.Cwd != "." {
		t.Fatalf("expected command aliases to decode, got %+v", cmd)
	}
	if len(cmd.ExtraArgs) != 1 || cmd.ExtraArgs[0].Kind != "enum" || len(cmd.ExtraArgs[0].Values) != 1 || cmd.ExtraArgs[0].Values[0] != "--help" {
		t.Fatalf("expected nested ExtraArgs aliases to decode, got %+v", cmd.ExtraArgs)
	}
}

func TestCommandEnvironmentPersistentShellDefaults(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfg, err := Load(writeConfig(t, root, minimalConfig(root)))
	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}
	if got := cfg.CommandEnvironment.PersistentShellPoolSize; got != 2 {
		t.Fatalf("persistent shell pool size = %d, want 2", got)
	}
	if got := cfg.CommandEnvironment.PersistentShellAcquireTimeoutSeconds; got != 6 {
		t.Fatalf("persistent shell acquire timeout = %d, want 6", got)
	}
	if got := cfg.CommandEnvironment.PersistentShellStartupTimeoutSeconds; got != 30 {
		t.Fatalf("persistent shell startup timeout = %d, want 30", got)
	}
	if got := cfg.CommandEnvironment.PersistentShellQuietPeriodMs; got != 1000 {
		t.Fatalf("persistent shell quiet period = %d, want 1000", got)
	}
}

func TestLoadRejectsNegativePersistentShellStartupSettings(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "startup_timeout", body: "persistent_shell_startup_timeout_seconds = -1"},
		{name: "quiet_period", body: "persistent_shell_quiet_period_ms = -1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := minimalConfig(root) + "\n[command_environment]\n" + tc.body + "\n"
			_, err := Load(writeConfig(t, root, cfg))
			if err == nil || !strings.Contains(err.Error(), "persistent shell pool settings cannot be negative") {
				t.Fatalf("expected persistent shell settings error, got %v", err)
			}
		})
	}
}

func TestSnakeCaseWinsOverCamelCaseAlias(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfgBody := minimalConfig(root) + `
[defaults]
allow_everything = false
AllowEverything = true
`
	cfg, err := Load(writeConfig(t, root, cfgBody))
	if err != nil {
		t.Fatalf("expected mixed-case config to load: %v", err)
	}
	if cfg.Defaults.AllowEverything {
		t.Fatal("expected canonical snake_case value to win over CamelCase alias")
	}
}

func TestLoadRejectsPersistentShellBashWithoutStartupFiles(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfgBody := minimalConfig(root) + `
[[commands]]
name = "pytest"
exec = "python3"
args = ["-m", "pytest"]
run_mode = "persistent_shell"
shell = "/bin/bash"
`
	_, err := Load(writeConfig(t, root, cfgBody))
	if err == nil || !strings.Contains(err.Error(), `run_mode persistent_shell with bash requires startup_files`) {
		t.Fatalf("expected bash startup_files validation error, got %v", err)
	}
}

func TestLoadRejectsPersistentShellZshWithoutStartupFiles(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	cfgBody := minimalConfig(root) + `
[[commands]]
name = "pytest"
exec = "python3"
args = ["-m", "pytest"]
run_mode = "persistent_shell"
shell = "/bin/zsh"
`
	_, err := Load(writeConfig(t, root, cfgBody))
	if err == nil || !strings.Contains(err.Error(), `run_mode persistent_shell with zsh requires startup_files`) {
		t.Fatalf("expected zsh startup_files validation error, got %v", err)
	}
}
