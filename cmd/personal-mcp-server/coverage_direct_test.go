package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	serviceres "github.com/noumena-labs-llc/personal-mcp-server/internal/service"
)

var stdStreamCaptureMu sync.Mutex

func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	// Non-parallel-safe: temporarily swaps process-global stdout/stderr.
	stdStreamCaptureMu.Lock()
	defer stdStreamCaptureMu.Unlock()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = outW
	os.Stderr = errW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	outDone := make(chan struct{})
	errDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outBuf, outR)
		close(outDone)
	}()
	go func() {
		_, _ = io.Copy(&errBuf, errR)
		close(errDone)
	}()

	fn()
	_ = outW.Close()
	_ = errW.Close()
	<-outDone
	<-errDone
	_ = outR.Close()
	_ = errR.Close()
	return outBuf.String(), errBuf.String()
}

func TestInitAndVersionHelpersDirect(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	t.Setenv("PERSONAL_MCP_ROOT", root)

	if got, want := defaultAppRoot(), root; got != want {
		t.Fatalf("defaultAppRoot = %q, want %q", got, want)
	}
	if got, want := defaultConfigDir(), filepath.Join(root, "config"); got != want {
		t.Fatalf("defaultConfigDir = %q, want %q", got, want)
	}
	if got, want := defaultConfigPath(), filepath.Join(root, "config", "config.toml"); got != want {
		t.Fatalf("defaultConfigPath = %q, want %q", got, want)
	}
	if got, want := defaultTokenPath(), filepath.Join(root, "config", "token"); got != want {
		t.Fatalf("defaultTokenPath = %q, want %q", got, want)
	}
	if got, want := defaultTrustStorePath(), filepath.Join(root, "config", "trusted-projects.toml"); got != want {
		t.Fatalf("defaultTrustStorePath = %q, want %q", got, want)
	}
	if got, want := defaultBinaryPath(), filepath.Join(root, "bin", "personal-mcp-server"); got != want {
		t.Fatalf("defaultBinaryPath = %q, want %q", got, want)
	}
	if got := firstNonEmpty("", "   ", "x"); got != "x" {
		t.Fatalf("firstNonEmpty = %q, want x", got)
	}
	tok, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("generateToken returned empty token")
	}
	if other, err := generateToken(); err != nil {
		t.Fatal(err)
	} else if other == tok {
		t.Fatal("generateToken returned duplicate token")
	}
	cfg := &config.Config{}
	info := serverInfo(cfg)
	if info["name"] != "personal-mcp-server" || info["transport"] != "streamable_http" {
		t.Fatalf("unexpected serverInfo: %#v", info)
	}

	out, _ := captureOutput(t, printVersion)
	for _, want := range []string{"personal-mcp-server ", "go: ", "mcp-go-sdk:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("printVersion output missing %q:\n%s", want, out)
		}
	}

	missing := filepath.Join(t.TempDir(), "missing-token")
	out, _ = captureOutput(t, func() { checkTokenFilePermissions(missing) })
	if !strings.Contains(out, "WARN:") {
		t.Fatalf("missing file warning not printed:\n%s", out)
	}
	if runtime.GOOS != "windows" {
		tokenPath := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(tokenPath, []byte("secret\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, _ = captureOutput(t, func() { checkTokenFilePermissions(tokenPath) })
		if !strings.Contains(out, "readable by group or others") {
			t.Fatalf("insecure token warning not printed:\n%s", out)
		}
	}
}

func TestInitConfigDirect(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "config.toml")
	tokenPath := filepath.Join(root, "config", "token")

	out, _ := captureOutput(t, func() {
		initConfig([]string{"--root", root, "--config", configPath, "--generate-token", "--token-file", tokenPath})
	})
	for _, want := range []string{"wrote " + tokenPath, "wrote " + configPath} {
		if !strings.Contains(out, want) {
			t.Fatalf("initConfig output missing %q:\n%s", want, out)
		}
	}
	for _, path := range []string{configPath, tokenPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSetupServerLoggingDirect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	cfg := &config.Config{}
	cfg.ServerLogging.Level = "info"
	cfg.ServerLogging.Path = path
	cfg.ServerLogging.MaxBytes = 64
	cfg.ServerLogging.MaxBackups = 1

	prevLogger := slog.Default()
	prevLogWriter := log.Writer()
	closeFn, err := setupServerLogging(cfg, "debug", "", 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeFn()
		slog.SetDefault(prevLogger)
		log.SetOutput(prevLogWriter)
	}()

	slog.Info("coverage logging message")
	slog.Error("coverage error message")
	closeFn()

	body, err := os.ReadFile(path) //nolint:gosec // path is a test-owned temp log file.
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "coverage error message") {
		t.Fatalf("log file missing expected entries:\n%s", text)
	}

	if _, err := setupServerLogging(cfg, "nope", "", 0, -1); err == nil {
		t.Fatal("expected unknown log level error")
	}
}

func TestUpgradeLocalDryRunDirect(t *testing.T) {
	root := t.TempDir()
	artifact := filepath.Join(root, "personal-mcp-server-v0.4.7.tar.gz")
	moduleRoot := filepath.Join(root, "personal-mcp-server-v0.4.7")
	if err := os.MkdirAll(filepath.Join(moduleRoot, "cmd", "personal-mcp-server"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "go.mod"), []byte("module github.com/noumena-labs-llc/personal-mcp-server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "VERSION"), []byte("0.4.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "cmd", "personal-mcp-server", "main.go"), []byte(`package main

const version = "0.4.7"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeTestTarGz(artifact, filepath.Base(moduleRoot)+"/go.mod", []byte("module github.com/noumena-labs-llc/personal-mcp-server\n")); err != nil {
		t.Fatal(err)
	}
	// Replace the tarball with a proper module tree so upgradeLocal exercises extraction and version detection.
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		filepath.Base(moduleRoot) + "/go.mod":                          "module github.com/noumena-labs-llc/personal-mcp-server\n",
		filepath.Base(moduleRoot) + "/VERSION":                         "0.4.7\n",
		filepath.Base(moduleRoot) + "/cmd/personal-mcp-server/main.go": "package main\n\nconst version = \"0.4.7\"\n",
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	shaPath := artifact + ".sha256"
	if err := os.WriteFile(shaPath, []byte(fmt.Sprintf("%x  %s\n", sum, filepath.Base(artifact))), 0o600); err != nil {
		t.Fatal(err)
	}

	fakeGo := writeExecutableScript(t, root, "go", `if [ "$1" = "build" ] && [ "$2" = "-o" ]; then
  : > "$3"
  exit 0
fi
echo "unexpected go args: $*" >&2
exit 2`)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, _ := captureOutput(t, func() {
		if err := upgradeLocal(upgradeOptions{
			ArtifactPath:   artifact,
			SHAPath:        shaPath,
			BinaryPath:     filepath.Join(root, "bin", "personal-mcp-server"),
			RestartService: false,
			DryRun:         true,
		}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"artifact version: 0.4.7", "dry run: built artifact successfully"} {
		if !strings.Contains(out, want) {
			t.Fatalf("upgradeLocal output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(fakeGo); err != nil {
		t.Fatal(err)
	}
}

func TestClientHelpersDirect(t *testing.T) {
	t.Setenv("TEST_CLIENT_TOKEN", "env-token")
	t.Setenv("HOME", t.TempDir())

	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Fatalf("auth header = %q, want %q", got, want)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		method, ok := payload["method"].(string)
		if !ok {
			t.Fatalf("missing method string in payload: %#v", payload)
		}
		switch method {
		case "initialize":
			_, _ = w.Write([]byte(`{"result":{"serverInfo":{"name":"ok"}}}`))
		case "tools/list":
			_, _ = w.Write([]byte(`{"result":{"tools":[{"name":"alpha"}]}}`))
		case "tools/call":
			params, ok := payload["params"].(map[string]any)
			if !ok {
				t.Fatalf("missing params map in payload: %#v", payload)
			}
			if params["name"] == "cmd_run_named" {
				_, _ = w.Write([]byte(`{"result":{"content":[{"type":"text","text":"named"}]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"result":{"content":[{"type":"text","text":"called"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"result":{"ok":true}}`))
		}
	}))

	cfg, err := loadMCPClientConfig("", "", server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client := &mcpCLIClient{cfg: cfg, http: server.Client(), nextID: 1}

	for _, tc := range []struct {
		name string
		fn   func()
		want string
	}{
		{name: "help", fn: printClientHelp, want: "client [GLOBAL FLAGS] ping"},
		{name: "run help", fn: printClientRunNamedHelp, want: "run-named [--cwd DIR]"},
		{name: "ping", fn: func() { clientPing(client, nil) }, want: "serverInfo"},
		{name: "tools", fn: func() { clientTools(client, nil) }, want: "alpha"},
		{name: "call", fn: func() { clientCall(client, []string{"server_info", `{"x":1}`}) }, want: "called"},
		{name: "run-named", fn: func() { clientRunNamed(client, []string{"--cwd", ".", "--extra-arg", "-x", "job"}) }, want: "named"},
		{name: "raw", fn: func() { clientRaw(client, []string{"tools/list", `{"y":2}`}) }, want: "tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := captureOutput(t, tc.fn)
			if !strings.Contains(out, tc.want) {
				t.Fatalf("%s output missing %q:\n%s", tc.name, tc.want, out)
			}
		})
	}

	if got, err := parseJSONObject(`{"a":1}`, "JSON_ARGS"); err != nil {
		t.Fatal(err)
	} else if value, ok := got["a"].(float64); !ok || value != 1 {
		t.Fatalf("parseJSONObject valid = %#v, %v", got, err)
	}
	if _, err := parseJSONObject(`not-json`, "JSON_ARGS"); err == nil {
		t.Fatal("parseJSONObject should reject invalid JSON")
	}
	if got := expandUserPath("~/notes"); !strings.Contains(got, "notes") {
		t.Fatalf("expandUserPath = %q", got)
	}
	out, _ := captureOutput(t, func() { printJSONValue(map[string]any{"ok": true}) })
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("printJSONValue output:\n%s", out)
	}
}

func TestAuditHelpersDirect(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PERSONAL_MCP_TOKEN", "audit-token")
	auditPath := filepath.Join(root, "audit.log")
	body := `{"ts":"2026-01-01T00:00:00Z","tool":"fs_read_file","decision":"allow","path":"README.md"}` + "\n" +
		`{"ts":"2026-01-01T00:00:01Z","tool":"cmd_run_named","decision":"deny","name":"test"}` + "\n"
	if err := os.WriteFile(auditPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeAuditConfigFile(t, root, auditPath)

	if got := auditPathFromConfig(configPath); got != auditPath {
		t.Fatalf("auditPathFromConfig = %q, want %q", got, auditPath)
	}
	lines, err := lastLines(auditPath, 1, auditFilter{Tool: "fs_read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "README.md") {
		t.Fatalf("lastLines = %#v", lines)
	}
	chunk, nextOffset, err := readAuditChunk(auditPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if nextOffset == 0 || !strings.Contains(chunk, "cmd_run_named") {
		t.Fatalf("readAuditChunk = %q, %d", chunk, nextOffset)
	}
	if got := formatAuditLine(`{"ts":"2026-01-01T00:00:00Z","tool":"fs_read_file","decision":"allow","path":"README.md"}`, "pretty"); !strings.Contains(got, "tool=fs_read_file") {
		t.Fatalf("formatAuditLine = %q", got)
	}

	out, _ := captureOutput(t, func() {
		auditCommand([]string{"show", "--config", configPath, "--last", "1", "--tool", "fs_read_file", "--format", "pretty"})
	})
	if !strings.Contains(out, "tool=fs_read_file") || !strings.Contains(out, "decision=allow") {
		t.Fatalf("audit show output:\n%s", out)
	}

	out, errOut := captureOutput(t, func() {
		auditCommand([]string{"tail", "--config", configPath, "--last", "1"})
	})
	if !strings.Contains(out, "cmd_run_named") || !strings.Contains(errOut, "audit tail is a snapshot") {
		t.Fatalf("audit tail output:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
}

func TestApprovalsHelpersDirect(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("/approvals", func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer approval-token"; got != want {
			t.Fatalf("auth header = %q, want %q", got, want)
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

	if _, err := newApprovalClient(configPath); err != nil {
		t.Fatal(err)
	}

	out, _ := captureOutput(t, func() {
		approvalsCommand([]string{"list", "--config", configPath})
	})
	if !strings.Contains(out, "pending") {
		t.Fatalf("approvals list output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		approvalsCommand([]string{"approve", "--config", configPath, "abc"})
	})
	if !strings.Contains(out, "ok") {
		t.Fatalf("approvals approve output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		approvalsCommand([]string{"deny", "--config", configPath, "abc"})
	})
	if !strings.Contains(out, "ok") {
		t.Fatalf("approvals deny output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		_ = printPrettyJSON([]byte(`{"x":1}`))
	})
	if !strings.Contains(out, "{") || !strings.Contains(out, "\"x\": 1") {
		t.Fatalf("printPrettyJSON output:\n%s", out)
	}
}

func TestConfigProjectAndServiceHelpersDirect(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONAL_MCP_ROOT", root)
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PERSONAL_MCP_TOKEN", "test-token")
	configPath := writeStarterConfigFile(t, root)
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "token"), []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "trusted-projects.toml"), []byte("trusted = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _ := captureOutput(t, func() {
		configCommand([]string{"validate", "--config", configPath})
	})
	if !strings.Contains(out, "config: ok") {
		t.Fatalf("config validate output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		projectCommand([]string{"init", "--cwd", projectDir, "--name", "coverage-project"})
	})
	if !strings.Contains(out, "wrote ") {
		t.Fatalf("project init output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		projectCommand([]string{"validate", "--cwd", projectDir})
	})
	if !strings.Contains(out, "project config: ok") {
		t.Fatalf("project validate output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		projectCommand([]string{"trust", "--config", configPath, "--cwd", projectDir})
	})
	if !strings.Contains(out, "trusted project") {
		t.Fatalf("project trust output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		projectCommand([]string{"list", "--config", configPath})
	})
	if !strings.Contains(out, projectDir) {
		t.Fatalf("project list output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		projectCommand([]string{"effective", "--config", configPath, "--cwd", projectDir, "--include-commands=false"})
	})
	if !strings.Contains(out, `"project"`) {
		t.Fatalf("project effective output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		projectCommand([]string{"untrust", "--config", configPath, "--cwd", projectDir})
	})
	if !strings.Contains(out, "project untrusted") {
		t.Fatalf("project untrust output:\n%s", out)
	}

	if got := mustAbsDir(projectDir); got != projectDir {
		t.Fatalf("mustAbsDir = %q, want %q", got, projectDir)
	}
	pm := loadProjectManager(configPath)
	if pm == nil {
		t.Fatal("loadProjectManager returned nil")
	}

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
	default:
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
	t.Setenv("PATH", binaryDir+string(os.PathListSeparator)+platformDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	spec, err := loadServiceSpec(fakeBinary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "linux":
		if err := os.MkdirAll(filepath.Dir(spec.Platforms["linux"].ManifestPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(spec.Platforms["linux"].ManifestPath, []byte(serviceres.SystemdUserUnit(spec)), 0o600); err != nil {
			t.Fatal(err)
		}
	case "darwin":
		if err := os.MkdirAll(filepath.Dir(spec.Platforms["darwin"].ManifestPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(spec.Platforms["darwin"].ManifestPath, []byte(serviceres.LaunchAgentPlist(spec)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(spec.Paths.StateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(spec.Paths.LogsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	out, _ = captureOutput(t, func() {
		doctor([]string{"--config", configPath})
	})
	if !strings.Contains(out, "config: ok") || !strings.Contains(out, "command") {
		t.Fatalf("doctor output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		serviceCommand([]string{"paths", "--config", configPath, "--binary", fakeBinary})
	})
	for _, want := range []string{"root:", "binary:", "config:", "linux unit:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("service paths output missing %q:\n%s", want, out)
		}
	}

	out, _ = captureOutput(t, func() {
		serviceCommand([]string{"print-systemd", "--config", configPath, "--binary", fakeBinary})
	})
	if !strings.Contains(out, "ExecStart=") {
		t.Fatalf("service print-systemd output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		serviceCommand([]string{"print-launchagent", "--config", configPath, "--binary", fakeBinary})
	})
	if !strings.Contains(out, "<plist") {
		t.Fatalf("service print-launchagent output:\n%s", out)
	}

	out, _ = captureOutput(t, func() {
		if err := serviceLogs(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "stdout:") || !strings.Contains(out, "stderr:") {
		t.Fatalf("service logs output:\n%s", out)
	}

	if err := serviceStatus(fakeBinary, configPath); err != nil {
		t.Fatal(err)
	}
	if err := serviceDoctor(fakeBinary, configPath); err != nil {
		t.Fatal(err)
	}
	if err := serviceStart(); err != nil {
		t.Fatal(err)
	}
	if err := serviceStop(); err != nil {
		t.Fatal(err)
	}
	if err := serviceRestart(); err != nil {
		t.Fatal(err)
	}

	if got := boolStatus(true); got != "true" {
		t.Fatalf("boolStatus(true) = %q", got)
	}
	if got := valueOrUnknown(""); got != "unknown" {
		t.Fatalf("valueOrUnknown(\"\") = %q", got)
	}
	if got := launchAgentDomain(); !strings.HasPrefix(got, "gui/") {
		t.Fatalf("launchAgentDomain = %q", got)
	}
	if got := launchAgentServiceTarget(); !strings.Contains(got, serviceLabel) {
		t.Fatalf("launchAgentServiceTarget = %q", got)
	}
	same, err := samePath(projectDir, projectDir)
	if err != nil || !same {
		t.Fatalf("samePath(projectDir, projectDir) = %t, %v", same, err)
	}
	if err := requireExecutableOnPath(filepath.Base(fakeBinary)); err != nil {
		t.Fatalf("requireExecutableOnPath(fake binary) = %v", err)
	}
	if err := requireConfigFileExists(configPath); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigFile(configPath); err != nil {
		t.Fatal(err)
	}
	if err := requireInstalledBinaryVersion(fakeBinary); err != nil {
		t.Fatal(err)
	}
	if err := requireManifestReferences(spec.Platforms[runtime.GOOS].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
	if !manifestContainsPath(spec.Process.Executable, spec.Process.Executable) {
		t.Fatal("manifestContainsPath should match exact path")
	}
	if got := systemdManifestQuote(`a b`); got != `"a b"` {
		t.Fatalf("systemdManifestQuote = %q", got)
	}
	if err := serviceInstall(fakeBinary, configPath); err != nil {
		t.Fatal(err)
	}
	if err := serviceUninstall(); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyAndResourceHelpersDirect(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PERSONAL_MCP_TOKEN", "resource-token")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "sample.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("world\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeStarterConfigFile(t, root)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	tools := fsx.NewTools(cfg, nil)

	commandTool := commandExplainTool(cfg)
	if _, err := commandTool(json.RawMessage(`{"exec":""}`)); err == nil {
		t.Fatal("commandExplainTool should reject empty exec")
	}
	commandOut, err := commandTool(json.RawMessage(`{"exec":"printf","args":["hi"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := commandOut.(map[string]any); !ok || m["decision"] == nil {
		t.Fatalf("unexpected commandExplainTool output: %#v", commandOut)
	}

	fileTool := fileExplainTool(tools)
	if _, err := fileTool(json.RawMessage(`{"operation":"","path":"sample.txt"}`)); err == nil {
		t.Fatal("fileExplainTool should reject empty operation")
	}
	if _, err := fileTool(json.RawMessage(`{"operation":"read","path":""}`)); err == nil {
		t.Fatal("fileExplainTool should reject empty path")
	}
	fileOut, err := fileTool(json.RawMessage(`{"operation":"read","path":"sample.txt","cwd":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := fileOut.(map[string]any); !ok || m["decision"] == nil {
		t.Fatalf("unexpected fileExplainTool output: %#v", fileOut)
	}

	resourceTool := resourceReadTool(tools, cfg)
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"uri":"personal-mcp://server"}`),
		json.RawMessage(`{"uri":"personal-mcp://roots"}`),
		json.RawMessage(`{"uri":"personal-mcp://policy"}`),
		json.RawMessage(`{"uri":"personal-mcp://file/sample.txt","cwd":"."}`),
		json.RawMessage(`{"uri":"personal-mcp://tree/docs","cwd":"."}`),
		json.RawMessage(`{"uri":"personal-mcp://info/sample.txt","cwd":"."}`),
	} {
		out, err := resourceTool(raw)
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := out.(map[string]any); !ok || m["content"] == nil {
			t.Fatalf("unexpected resource output: %#v", out)
		}
	}
	if _, err := resourceTool(json.RawMessage(`{"uri":"personal-mcp://bad/path"}`)); err == nil {
		t.Fatal("resourceReadTool should reject bad URIs")
	}

	if !printCommandHelp("service") {
		t.Fatal("expected service help topic to exist")
	}
	if printCommandHelp("missing-topic") {
		t.Fatal("expected missing help topic to return false")
	}
}
