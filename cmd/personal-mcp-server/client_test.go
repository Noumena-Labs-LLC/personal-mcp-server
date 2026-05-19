package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMCPClientConfigTokenOverride(t *testing.T) {
	cfg, err := loadMCPClientConfig("", t.TempDir(), "http://127.0.0.1:3929/mcp", "override-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "override-token" {
		t.Fatalf("token = %q", cfg.Token)
	}
}

func TestLoadMCPClientConfigDiscoversConfiguredTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "configured-token")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.toml")
	content := `config_version = 1
[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_file = "` + tokenPath + `"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadMCPClientConfig(configPath, dir, "", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "file-token" {
		t.Fatalf("token = %q", cfg.Token)
	}
}

func TestLoadMCPClientConfigDiscoversConfigDirTokenBeforeEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("dir-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.toml")
	content := `config_version = 1
[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_env = "PERSONAL_MCP_CLIENT_TEST_TOKEN"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONAL_MCP_CLIENT_TEST_TOKEN", "env-token")
	cfg, err := loadMCPClientConfig(configPath, dir, "", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "dir-token" {
		t.Fatalf("token = %q", cfg.Token)
	}
}

func TestLoadMCPClientConfigRejectsRemoteURL(t *testing.T) {
	_, err := loadMCPClientConfig("", t.TempDir(), "http://example.com:3929/mcp", "token", time.Second)
	if err == nil || !strings.Contains(err.Error(), "non-local") {
		t.Fatalf("expected non-local URL error, got %v", err)
	}
}

func TestMCPCLIClientPostsJSONRPC(t *testing.T) {
	var gotAuth string
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gotMethod, _ = payload["method"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer server.Close()

	client := &mcpCLIClient{cfg: mcpClientConfig{URL: server.URL, HostHeader: "127.0.0.1:3929", Token: "test-token", Timeout: time.Second}, http: server.Client(), nextID: 1}
	result, err := client.request("tools/list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotMethod != "tools/list" {
		t.Fatalf("method = %q", gotMethod)
	}
	obj, ok := result.(map[string]any)
	if !ok || obj["result"] == nil {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestParseRunNamedArgsAllowsNameBeforeFlags(t *testing.T) {
	parsed, err := parseRunNamedArgs([]string{"test", "--cwd", "~/RnD/aphoria", "--extra-arg", "-x", "--extra-arg=-q"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.name != "test" {
		t.Fatalf("name = %q", parsed.name)
	}
	if parsed.cwd != "~/RnD/aphoria" {
		t.Fatalf("cwd = %q", parsed.cwd)
	}
	if strings.Join(parsed.extraArgs, ",") != "-x,-q" {
		t.Fatalf("extraArgs = %#v", parsed.extraArgs)
	}
}

func TestParseRunNamedArgsAllowsFlagsBeforeName(t *testing.T) {
	parsed, err := parseRunNamedArgs([]string{"--cwd=~/RnD/aphoria", "--extra-arg=-x", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.name != "test" {
		t.Fatalf("name = %q", parsed.name)
	}
	if parsed.cwd != "~/RnD/aphoria" {
		t.Fatalf("cwd = %q", parsed.cwd)
	}
	if strings.Join(parsed.extraArgs, ",") != "-x" {
		t.Fatalf("extraArgs = %#v", parsed.extraArgs)
	}
}
