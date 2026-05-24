package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	serviceres "github.com/noumena-labs-llc/personal-mcp-server/internal/service"
)

func TestLaunchAgentPlistEscapesValues(t *testing.T) {
	spec := testServiceSpec(t, `/tmp/personal & local`, `/tmp/config "dev".toml`)
	plist := serviceres.LaunchAgentPlist(spec)
	if strings.Contains(plist, `/tmp/personal & local`) {
		t.Fatalf("plist did not escape binary path: %s", plist)
	}
	if !strings.Contains(plist, `/tmp/personal &amp; local`) {
		t.Fatalf("plist missing escaped binary path: %s", plist)
	}
	if !strings.Contains(plist, `/tmp/config &#34;dev&#34;.toml`) {
		t.Fatalf("plist missing escaped config path: %s", plist)
	}
}

func TestSystemdUserUnitQuotesExecStart(t *testing.T) {
	spec := testServiceSpec(t, `/home/me/bin/personal mcp`, `/home/me/.personal-mcp-server/config/config dev.toml`)
	unit := serviceres.SystemdUserUnit(spec)
	want := `ExecStart="/home/me/bin/personal mcp" serve --config "/home/me/.personal-mcp-server/config/config dev.toml"`
	if !strings.Contains(unit, want) {
		t.Fatalf("unit missing quoted ExecStart %q in:\n%s", want, unit)
	}
}

func testServiceSpec(t *testing.T, binary, configPath string) serviceres.Spec {
	t.Helper()
	spec, err := serviceres.LoadDefaultSpec(serviceres.Vars{
		"app_root":        "/tmp/personal-mcp-server",
		"install_bin":     binary,
		"config_file":     configPath,
		"home":            "/home/me",
		"user_config_dir": "/home/me/.config",
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestApprovalClientDecideEscapesID(t *testing.T) {
	server := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.EscapedPath(), "/approvals/approval-1%2Fneeds-escaping/approve"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	client := &approvalClient{baseURL: server.URL, token: "test-token", client: server.Client()}
	if err := client.decide("approval-1/needs-escaping", "approve"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLocalApprovalAddr(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{name: "localhost", addr: "localhost:3929"},
		{name: "ipv4 loopback", addr: "127.0.0.1:3929"},
		{name: "ipv6 loopback", addr: "[::1]:3929"},
		{name: "wildcard", addr: "0.0.0.0:3929", wantErr: true},
		{name: "remote", addr: "example.com:3929", wantErr: true},
		{name: "invalid", addr: "not-a-host-port", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLocalApprovalAddr(tt.addr)
			if tt.wantErr && err == nil {
				t.Fatalf("validateLocalApprovalAddr(%q) succeeded, want error", tt.addr)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateLocalApprovalAddr(%q) error = %v", tt.addr, err)
			}
		})
	}
}

func TestAuditLineMatchesFilters(t *testing.T) {
	line := `{"ts":"2026-01-01T00:00:00Z","tool":"fs_read_file","decision":"allow","path":"README.md"}`
	if !auditLineMatches(line, auditFilter{Tool: "fs_read_file", Decision: "allow", Contains: "README"}) {
		t.Fatal("expected matching audit line")
	}
	if auditLineMatches(line, auditFilter{Tool: "cmd_run_named"}) {
		t.Fatal("unexpected tool match")
	}
	if auditLineMatches(line, auditFilter{Decision: "deny"}) {
		t.Fatal("unexpected decision match")
	}
}

func TestLastLinesFiltersBeforeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	body := strings.Join([]string{
		`{"tool":"fs_read_file","decision":"allow","path":"a.txt"}`,
		`{"tool":"cmd_run_named","decision":"allow","name":"test"}`,
		`{"tool":"fs_read_file","decision":"deny","path":"secret.txt"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := lastLines(path, 1, auditFilter{Tool: "fs_read_file"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "secret.txt") {
		t.Fatalf("filtered last line = %#v", lines)
	}
}

func TestFormatAuditLinePretty(t *testing.T) {
	line := `{"ts":"2026-01-01T00:00:00Z","tool":"fs_read_file","decision":"allow","path":"README.md"}`
	got := formatAuditLine(line, "pretty")
	for _, want := range []string{"tool=fs_read_file", "decision=allow", "path=README.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("pretty line %q missing %q", got, want)
		}
	}
}

func TestRequireConfigFileExistsReportsInitHint(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "config.toml")
	err := requireConfigFileExists(missing)
	if err == nil {
		t.Fatal("expected missing config error")
	}
	if !strings.Contains(err.Error(), "personal-mcp-server init --generate-token") {
		t.Fatalf("missing init hint in error: %v", err)
	}
}

func TestCopyExecutableWritesExecutableFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "nested", "target")
	if err := os.WriteFile(source, []byte("binary-ish"), 0o755); err != nil { //nolint:gosec // test source intentionally mimics an executable binary.
		t.Fatal(err)
	}
	if err := copyExecutable(source, target); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target) //nolint:gosec // target is created under t.TempDir by this test.
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "binary-ish" {
		t.Fatalf("target body = %q", string(body))
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o755); got != want {
		t.Fatalf("target mode = %s, want %s", got, want)
	}
}

func TestReadSHA256SumFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.tar.gz.sha256")
	digest := strings.Repeat("a", sha256.Size*2)
	if err := os.WriteFile(path, []byte(digest+"  artifact.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readSHA256SumFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != digest {
		t.Fatalf("digest = %q, want %q", got, digest)
	}
}

func TestReadSHA256SumFileRejectsInvalidHex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.tar.gz.sha256")
	digest := strings.Repeat("g", sha256.Size*2)
	if err := os.WriteFile(path, []byte(digest+"  artifact.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSHA256SumFile(path); err == nil {
		t.Fatal("expected invalid hex digest error")
	}
}

func TestRequireUpgradeModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/noumena-labs-llc/personal-mcp-server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireUpgradeModule(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireUpgradeModule(dir); err == nil {
		t.Fatal("expected module mismatch error")
	}
}

func TestUpgradeArtifactVersion(t *testing.T) {
	dir := t.TempDir()
	mainDir := filepath.Join(dir, "cmd", "personal-mcp-server")
	if err := os.MkdirAll(mainDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("0.4.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainDir, "main.go"), []byte(`package main

const version = "0.4.7"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := upgradeArtifactVersion(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.4.7" {
		t.Fatalf("artifact version = %q, want 0.4.7", got)
	}
}

func TestReplaceExecutableWithRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "personal-mcp-server")
	next := filepath.Join(dir, "personal-mcp-server-next")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil { //nolint:gosec // test target intentionally mimics an installed executable.
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte("new"), 0o755); err != nil { //nolint:gosec // test source intentionally mimics a built executable.
		t.Fatal(err)
	}
	replacement, err := replaceExecutableWithRollback(next, target)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target) //nolint:gosec // target is created under t.TempDir by this test.
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "new" {
		t.Fatalf("target body = %q, want new", string(body))
	}
	replacement.restore()
	body, err = os.ReadFile(target) //nolint:gosec // target is created under t.TempDir by this test.
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "old" {
		t.Fatalf("restored target body = %q, want old", string(body))
	}
	replacement.cleanup()
}

func TestExtractTarGzRejectsUnsafePath(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "artifact.tar.gz")
	if err := writeTestTarGz(artifact, "../escape.txt", []byte("nope")); err != nil {
		t.Fatal(err)
	}
	err := extractTarGz(artifact, t.TempDir())
	if err == nil {
		t.Fatal("expected unsafe tar path error")
	}
	if !strings.Contains(err.Error(), "unsafe tar entry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFindExtractedModuleRoot(t *testing.T) {
	dir := t.TempDir()
	module := filepath.Join(dir, "personal-mcp-server-vtest")
	if err := os.MkdirAll(module, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := findExtractedModuleRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != module {
		t.Fatalf("module root = %q, want %q", got, module)
	}
}

func writeTestTarGz(path, name string, body []byte) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
		return err
	}
	if _, err := tw.Write(body); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

func TestRequirePrivateReadableFileRejectsGroupReadableToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions are not portable on Windows")
	}
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("token\n"), 0o644); err != nil { //nolint:gosec // test intentionally creates an insecure token file.
		t.Fatal(err)
	}
	err := requirePrivateReadableFile(path)
	if err == nil {
		t.Fatal("expected group-readable token permission error")
	}
	if !strings.Contains(err.Error(), "readable by group or others") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireExecutableFileRejectsNonExecutableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bits are not portable on Windows")
	}
	path := filepath.Join(t.TempDir(), "personal-mcp-server")
	if err := os.WriteFile(path, []byte("binary-ish"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := requireExecutableFile(path)
	if err == nil {
		t.Fatal("expected executable permission error")
	}
	if !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseLaunchctlPrint(t *testing.T) {
	out := strings.Join([]string{
		"com.noumenalabs.personal-mcp-server = {",
		"\tpid = 12345",
		"\tlast exit code = 7",
		"}",
	}, "\n")
	status := parseLaunchctlPrint(out)
	if status.PID != "12345" {
		t.Fatalf("PID = %q, want 12345", status.PID)
	}
	if status.LastExitCode != "7" {
		t.Fatalf("LastExitCode = %q, want 7", status.LastExitCode)
	}
}

func TestIsLaunchctlNoSuchProcess(t *testing.T) {
	if !isLaunchctlNoSuchProcess("Boot-out failed: 3: No such process", os.ErrNotExist) {
		t.Fatal("expected launchctl no-such-process output to be ignored")
	}
	if isLaunchctlNoSuchProcess("Boot-out failed: 5: Input/output error", os.ErrNotExist) {
		t.Fatal("expected unrelated launchctl failure to remain an error")
	}
}

func TestParseSystemctlShow(t *testing.T) {
	out := strings.Join([]string{
		"LoadState=loaded",
		"ActiveState=active",
		"SubState=running",
		"MainPID=2468",
		"ExecMainStatus=0",
	}, "\n")
	status := parseSystemctlShow(out)
	for key, want := range map[string]string{
		"LoadState":      "loaded",
		"ActiveState":    "active",
		"SubState":       "running",
		"MainPID":        "2468",
		"ExecMainStatus": "0",
	} {
		if got := status[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestRequireManifestReferences(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "service.manifest")
	binary := filepath.Join(t.TempDir(), "bin", "personal-mcp-server")
	config := filepath.Join(t.TempDir(), "config", "config.toml")
	body := "ExecStart=" + binary + " serve --config " + config + "\n"
	if err := os.WriteFile(manifest, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireManifestReferences(manifest, binary, config); err != nil {
		t.Fatal(err)
	}
	if err := requireManifestReferences(manifest, binary, config+".other"); err == nil {
		t.Fatal("expected config mismatch")
	}
}
