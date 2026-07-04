package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func TestDuplicateErrorHandlerDuplicatesOnlyErrors(t *testing.T) {
	var primary bytes.Buffer
	var stderr bytes.Buffer
	handler := duplicateErrorHandler{
		primary: slog.NewTextHandler(&primary, &slog.HandlerOptions{Level: slog.LevelDebug}),
		stderr:  slog.NewTextHandler(&stderr, &slog.HandlerOptions{Level: slog.LevelError}),
	}
	logger := slog.New(handler)
	logger.InfoContext(context.Background(), "info message")
	logger.ErrorContext(context.Background(), "error message")

	primaryText := primary.String()
	if !strings.Contains(primaryText, "info message") || !strings.Contains(primaryText, "error message") {
		t.Fatalf("primary log missing expected messages: %q", primaryText)
	}
	stderrText := stderr.String()
	if strings.Contains(stderrText, "info message") {
		t.Fatalf("stderr log duplicated info message: %q", stderrText)
	}
	if !strings.Contains(stderrText, "error message") {
		t.Fatalf("stderr log missing error message: %q", stderrText)
	}
}

func TestDiagnosticsRecentSlowToolsReadsConfiguredLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	log := strings.Join([]string{
		`time=2026-05-19T12:00:00Z level=INFO msg=ok event=tool_call tool=fs_read_file duration_ms=10`,
		`time=2026-05-19T12:01:00Z level=WARN msg=slow event=tool_call_slow tool=fs_read_file duration_ms=3100 threshold_ms=3000 ok=true request_bytes=20 response_bytes=30`,
		`time=2026-05-19T12:02:00Z level=ERROR msg=very event=tool_call_very_slow tool=cmd_run_named duration_ms=12000 threshold_ms=10000 ok=false error=timeout`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.ServerLogging.Path = path
	out, err := diagnosticsRecentSlowToolsTool(cfg)(json.RawMessage(`{"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", out)
	}
	records, ok := m["records"].([]slowToolRecord)
	if !ok || len(records) != 2 {
		t.Fatalf("expected two slow records, got %#v", m["records"])
	}
	if records[0].Event != "tool_call_slow" || records[0].Tool != "fs_read_file" || records[0].DurationMS != 3100 {
		t.Fatalf("unexpected first record: %#v", records[0])
	}
	if records[1].Event != "tool_call_very_slow" || records[1].Tool != "cmd_run_named" || records[1].OK == nil || *records[1].OK {
		t.Fatalf("unexpected second record: %#v", records[1])
	}
}

func TestConfigValidateAndExplainTools(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	root := t.TempDir()
	configPath := filepath.Join(root, "personal-mcp.toml")
	body := `config_version = 1
roots = ["` + strings.ReplaceAll(root, `\`, `\\`) + `"]
[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_env = "PERSONAL_MCP_TOKEN"
validate_origin = true
allowed_origins = ["http://127.0.0.1"]
[limits]
max_read_bytes = 100
max_write_bytes = 100
max_search_results = 10
max_search_file_bytes = 100
command_timeout_seconds = 1
max_command_output_bytes = 1000
[secrets]
deny_names = [".env"]
deny_extensions = [".pem"]
[defaults]
allow_everything = true
`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	valid, err := configValidateTool()(json.RawMessage(`{"path":` + strconv.Quote(configPath) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	validMap, ok := valid.(map[string]any)
	if !ok || validMap["ok"] != true {
		t.Fatalf("expected valid config result, got %#v", valid)
	}
	warnings, ok := validMap["warnings"].([]string)
	if !ok || len(warnings) == 0 {
		t.Fatalf("expected allow_everything warning, got %#v", validMap["warnings"])
	}

	invalidPath := filepath.Join(root, "bad.toml")
	if err := os.WriteFile(invalidPath, []byte(`config_version = "bad"`), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid, err := configValidateTool()(json.RawMessage(`{"path":` + strconv.Quote(invalidPath) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidMap, ok := invalid.(map[string]any)
	if !ok || invalidMap["ok"] != false {
		t.Fatalf("expected invalid config as structured response, got %#v", invalid)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	explained, err := configExplainTool(cfg)(nil)
	if err != nil {
		t.Fatal(err)
	}
	explainedMap, ok := explained.(map[string]any)
	if !ok || explainedMap["allow_everything"] != true || explainedMap["command_policy_default"] == "" {
		t.Fatalf("expected effective config explanation, got %#v", explained)
	}
}
