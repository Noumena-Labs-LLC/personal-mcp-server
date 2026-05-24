package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
)

func runCLI(t *testing.T, args ...string) (stdout string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmdArgs := append([]string{"-test.run=TestMainHelperProcess", "--"}, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...) //nolint:gosec // helper re-execs the current test binary with fixed arguments.
	cmd.Env = append(os.Environ(), "GO_WANT_PERSONAL_MCP_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("runCLI %v: %v\n%s", args, err, string(out))
	return "", 1
}

func writeStarterConfigFile(t *testing.T, root string) string {
	t.Helper()
	configPath := filepath.Join(root, "config", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(starterConfig(root, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func writeAuditConfigFile(t *testing.T, root, auditPath string) string {
	t.Helper()
	content := strings.Split(starterConfig(root, ""), "\n")
	inAudit := false
	for i, line := range content {
		switch strings.TrimSpace(line) {
		case "[audit]":
			inAudit = true
		default:
			if inAudit && strings.HasPrefix(strings.TrimSpace(line), "path = \"\"") {
				content[i] = fmt.Sprintf("path = %q", auditPath)
				inAudit = false
			}
		}
	}
	configPath := filepath.Join(root, "config", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(content, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func writeExecutableScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitConfigAndDefaults(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "config.toml")
	tokenPath := filepath.Join(root, "config", "token")

	out, code := runCLI(t, "init", "--root", root, "--config", configPath, "--generate-token", "--token-file", tokenPath)
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, out)
	}
	for _, want := range []string{"wrote " + tokenPath, "wrote " + configPath} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tokenPath); err != nil {
		t.Fatal(err)
	}

	out, code = runCLI(t, "init", "--root", root, "--config", filepath.Join(root, "config", "config-no-token.toml"))
	if code != 0 {
		t.Fatalf("init without token exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "set PERSONAL_MCP_TOKEN before running serve or doctor") {
		t.Fatalf("init output missing token hint:\n%s", out)
	}
}

func TestHelpVersionAndUnknownCommand(t *testing.T) {
	out, code := runCLI(t, "version")
	if code != 0 {
		t.Fatalf("version exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, version) {
		t.Fatalf("version output missing %q:\n%s", version, out)
	}

	out, code = runCLI(t, "help")
	if code != 0 || !strings.Contains(out, "personal-mcp-server help [COMMAND...]") {
		t.Fatalf("help output:\n%d\n%s", code, out)
	}

	out, code = runCLI(t, "service", "help")
	if code != 0 || !strings.Contains(out, "service install|uninstall|start|stop|restart|status|logs|doctor --user") {
		t.Fatalf("service help output:\n%d\n%s", code, out)
	}

	out, code = runCLI(t, "upgrade", "help")
	if code != 0 || !strings.Contains(out, "upgrade local") {
		t.Fatalf("upgrade help output:\n%d\n%s", code, out)
	}

	out, code = runCLI(t, "help", "does-not-exist")
	if code == 0 || !strings.Contains(out, "unknown help topic") {
		t.Fatalf("help unknown topic should fail:\n%d\n%s", code, out)
	}

	out, code = runCLI(t, "not-a-command")
	if code == 0 || !strings.Contains(out, "usage:") {
		t.Fatalf("unknown command should fail:\n%d\n%s", code, out)
	}
}

func TestServeAndUpgradeCommandCoverage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PERSONAL_MCP_ROOT", root)

	out, code := runCLI(t, "serve", "--help")
	if code != 0 || !strings.Contains(out, "personal-mcp-server serve [--config CONFIG]") {
		t.Fatalf("serve help output:\n%d\n%s", code, out)
	}

	fakeBinary := writeExecutableScript(t, root, "personal-mcp-server", `if [ "$1" = "version" ]; then
  echo "personal-mcp-server 0.4.7"
  exit 0
fi
echo "unexpected binary args: $*" >&2
exit 2`)
	if err := runInstalledVersion(fakeBinary); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, "upgrade", "bogus")
	if code == 0 || !strings.Contains(out, "usage:") {
		t.Fatalf("upgrade bogus should fail:\n%d\n%s", code, out)
	}
}

func TestConfigAndProjectCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PERSONAL_MCP_ROOT", root)
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PERSONAL_MCP_TOKEN", "test-token")
	configPath := writeStarterConfigFile(t, root)

	out, code := runCLI(t, "config", "validate", "--config", configPath)
	if code != 0 {
		t.Fatalf("config validate exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "config: ok") {
		t.Fatalf("config validate output:\n%s", out)
	}

	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}

	out, code = runCLI(t, "project", "init", "--cwd", projectDir, "--name", "coverage-project")
	if code != 0 {
		t.Fatalf("project init exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "wrote ") {
		t.Fatalf("project init output:\n%s", out)
	}

	out, code = runCLI(t, "project", "validate", "--cwd", projectDir)
	if code != 0 || !strings.Contains(out, "project config: ok") {
		t.Fatalf("project validate failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "project", "trust", "--config", configPath, "--cwd", projectDir)
	if code != 0 || !strings.Contains(out, "trusted project") {
		t.Fatalf("project trust failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "project", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("project list exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, projectDir) {
		t.Fatalf("project list output missing project dir:\n%s", out)
	}

	out, code = runCLI(t, "project", "effective", "--config", configPath, "--cwd", projectDir, "--include-commands=false")
	if code != 0 {
		t.Fatalf("project effective exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, `"project"`) {
		t.Fatalf("project effective output:\n%s", out)
	}

	out, code = runCLI(t, "project", "untrust", "--config", configPath, "--cwd", projectDir)
	if code != 0 || !strings.Contains(out, "project untrusted") {
		t.Fatalf("project untrust failed %d:\n%s", code, out)
	}

	_, code = runCLI(t, "project", "init", "--cwd", projectDir, "--name", "coverage-project")
	if code == 0 {
		t.Fatal("project init without --force should fail when config exists")
	}
}

func TestDoctorAndServiceCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PERSONAL_MCP_ROOT", root)
	t.Setenv("PERSONAL_MCP_TOKEN", "test-token")

	configPath := writeStarterConfigFile(t, root)
	binaryDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binaryDir, 0o750); err != nil {
		t.Fatal(err)
	}
	fakeBinary := writeExecutableScript(t, binaryDir, "personal-mcp-server", fmt.Sprintf(`if [ "$1" = "version" ]; then
  echo "personal-mcp-server %s"
  exit 0
fi
echo "unexpected args: $*" >&2
exit 2
`, version))
	if canonical, err := filepath.EvalSymlinks(fakeBinary); err == nil {
		fakeBinary = canonical
	}
	spec, err := loadServiceSpec(fakeBinary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := spec.Platforms[runtime.GOOS].ManifestPath
	if err := os.Remove(manifestPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	platformDir := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(platformDir, 0o750); err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "darwin":
		writeExecutableScript(t, platformDir, "launchctl", `case "${1:-}" in
  print)
    echo "pid = 123"
    echo "last exit code = 0"
    ;;
  bootout|bootstrap|kickstart)
    exit 0
    ;;
esac`)
	case "linux":
		writeExecutableScript(t, platformDir, "systemctl", `if [ "${1:-}" = "--user" ] && [ "${2:-}" = "show" ]; then
  echo "LoadState=loaded"
  echo "ActiveState=active"
  echo "SubState=running"
  echo "MainPID=123"
  echo "ExecMainStatus=0"
  exit 0
fi
exit 0`)
	}
	t.Setenv("PATH", platformDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, dir := range []string{filepath.Join(root, "config"), filepath.Join(root, "state"), filepath.Join(root, "logs")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config", "token"), []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "trusted-projects.toml"), []byte("trusted = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(fakeBinary), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fakeBinary); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, "doctor", "--config", configPath)
	if code != 0 {
		t.Fatalf("doctor exited %d:\n%s", code, out)
	}
	for _, want := range []string{"config: ok", "auth: ok via env PERSONAL_MCP_TOKEN", "command"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}

	out, code = runCLI(t, "service", "paths", "--config", configPath, "--binary", fakeBinary)
	if code != 0 {
		t.Fatalf("service paths exited %d:\n%s", code, out)
	}
	for _, want := range []string{"root:", "binary:", "config:", "linux unit:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("service paths output missing %q:\n%s", want, out)
		}
	}

	out, code = runCLI(t, "service", "print-systemd", "--config", configPath, "--binary", fakeBinary)
	if code != 0 || !strings.Contains(out, "ExecStart=") {
		t.Fatalf("service print-systemd failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "service", "print-launchagent", "--config", configPath, "--binary", fakeBinary)
	if code != 0 || !strings.Contains(out, "<plist") {
		t.Fatalf("service print-launchagent failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "service", "logs", "--user", "--config", configPath, "--binary", fakeBinary)
	if code != 0 || !strings.Contains(out, "stdout:") || !strings.Contains(out, "stderr:") {
		t.Fatalf("service logs failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "service", "install", "--config", configPath, "--binary", fakeBinary)
	if code == 0 || !strings.Contains(out, "require --user") {
		t.Fatalf("service install without --user should fail:\n%d\n%s", code, out)
	}
}

func TestClientCommandsAgainstFakeServer(t *testing.T) {
	var (
		gotAuth string
		authMu  sync.Mutex
	)
	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		authMu.Lock()
		gotAuth = req.Header.Get("Authorization")
		authMu.Unlock()
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		method, ok := payload["method"].(string)
		if !ok {
			t.Fatalf("missing method string in payload: %#v", payload)
		}
		if method == "" {
			t.Fatalf("missing method in payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))

	args := []string{"client", "--url", server.URL, "--token", "test-token"}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "ping", args: append(args, "ping"), want: "ok"},
		{name: "tools", args: append(args, "tools"), want: "ok"},
		{name: "call", args: append(args, "call", "server_info", `{"x":1}`), want: "ok"},
		{name: "raw", args: append(args, "raw", "tools/list", `{"y":2}`), want: "ok"},
		{name: "run-named", args: append(args, "run-named", "--cwd", ".", "--extra-arg", "-x", "job"), want: "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runCLI(t, tc.args...)
			if code != 0 {
				t.Fatalf("%s exited %d:\n%s", tc.name, code, out)
			}
			authMu.Lock()
			auth := gotAuth
			authMu.Unlock()
			if auth != "Bearer test-token" {
				t.Fatalf("%s auth header = %q", tc.name, auth)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("%s output missing %q:\n%s", tc.name, tc.want, out)
			}
		})
	}

	out, code := runCLI(t, "client", "--url", server.URL, "--token", "test-token", "call", "server_info", "{not-json}")
	if code == 0 || !strings.Contains(out, "JSON_ARGS must be valid JSON object") {
		t.Fatalf("client call invalid JSON should fail, got %d:\n%s", code, out)
	}
}

func TestApprovalsCommandsAgainstFakeServer(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("/approvals", func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer approval-token"; got != want {
			t.Fatalf("auth header = %q, want %q", got, want)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"approvals":[{"id":"abc","decision":"pending"}]}`))
	})
	mux.HandleFunc("/approvals/abc/approve", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/approvals/abc/deny", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	server := newLocalHTTPServer(t, mux)

	configPath := filepath.Join(root, "config.toml")
	content := strings.Replace(starterConfig(root, ""), "port = 3929", fmt.Sprintf("port = %d", serverPortFromURL(t, server.URL)), 1)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONAL_MCP_TOKEN", "approval-token")

	out, code := runCLI(t, "approvals", "list", "--config", configPath)
	if code != 0 || !strings.Contains(out, "pending") {
		t.Fatalf("approvals list failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "approvals", "approve", "--config", configPath, "abc")
	if code != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("approvals approve failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "approvals", "deny", "--config", configPath, "abc")
	if code != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("approvals deny failed %d:\n%s", code, out)
	}

	out, code = runCLI(t, "approvals", "bogus", "--config", configPath)
	if code != 2 {
		t.Fatalf("approvals bogus should exit 2, got %d:\n%s", code, out)
	}
}

func serverPortFromURL(t *testing.T, rawURL string) int {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server URL %q: %v", rawURL, err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split server host %q: %v", parsed.Host, err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse server port %q: %v", port, err)
	}
	return p
}

func TestAuditCommands(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PERSONAL_MCP_TOKEN", "audit-token")
	auditPath := filepath.Join(root, "audit.log")
	body := `{"ts":"2026-01-01T00:00:00Z","tool":"fs_read_file","decision":"allow","path":"README.md"}` + "\n" +
		`{"ts":"2026-01-01T00:00:01Z","tool":"cmd_run_named","decision":"deny","name":"test"}` + "\n"
	if err := os.WriteFile(auditPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeAuditConfigFile(t, root, auditPath)

	out, code := runCLI(t, "audit", "show", "--config", configPath, "--last", "1", "--tool", "fs_read_file", "--format", "pretty")
	if code != 0 {
		t.Fatalf("audit show exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "tool=fs_read_file") || !strings.Contains(out, "decision=allow") {
		t.Fatalf("audit show output:\n%s", out)
	}

	out, code = runCLI(t, "audit", "tail", "--config", configPath, "--last", "1")
	if code != 0 {
		t.Fatalf("audit tail exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "audit tail is a snapshot") {
		t.Fatalf("audit tail output:\n%s", out)
	}
}

func TestResourceHelpers(t *testing.T) {
	t.Setenv("PERSONAL_MCP_TOKEN", "resource-token")
	if got, err := resourcePath("personal-mcp://file/some%20path.txt", "file"); err != nil || got != "some path.txt" {
		t.Fatalf("resourcePath(file) = %q, %v", got, err)
	}
	if _, err := resourcePath("personal-mcp://other/path", "file"); err == nil {
		t.Fatal("resourcePath should reject mismatched host")
	}
	if got := mustJSON(map[string]any{"a": 1}); string(got) != `{"a":1}` {
		t.Fatalf("mustJSON = %s", string(got))
	}

	g, ok := guideByName("project-config")
	if !ok || g.Name != "project-config-guide" {
		t.Fatalf("guideByName alias lookup failed: %#v %v", g, ok)
	}
	if !guideResourceByPath("guide", "project-config") {
		t.Fatal("guideResourceByPath should find guide")
	}
	if !guideResourceByPath("docs", "audit") {
		t.Fatal("guideResourceByPath should find docs")
	}
	if _, ok := guideByURI("personal-mcp://docs/audit"); !ok {
		t.Fatal("guideByURI should find docs file")
	}
	if len(resourceCatalog()) == 0 || len(guideCatalog()) == 0 {
		t.Fatal("resource catalogs should not be empty")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeStarterConfigFile(t, root)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	tools := fsx.NewTools(cfg, nil)
	rr := resourceReadTool(tools, cfg)
	out, err := rr(mustJSON(resourceReadArgs{URI: "personal-mcp://server"}))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := out.(map[string]any); !ok || m["content"] == nil {
		t.Fatalf("unexpected server resource output: %#v", out)
	}
	out, err = rr(mustJSON(resourceReadArgs{URI: "personal-mcp://file/sample.txt", Cwd: root}))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := out.(map[string]any); !ok || !strings.Contains(fmt.Sprint(m["content"]), "hello") {
		t.Fatalf("unexpected file resource output: %#v", out)
	}

	guideOut, err := guideReadTool(mustJSON(guideReadArgs{Name: "project-config"}))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := guideOut.(map[string]any); !ok || m["content"] == nil {
		t.Fatalf("unexpected guideReadTool output: %#v", guideOut)
	}
}
