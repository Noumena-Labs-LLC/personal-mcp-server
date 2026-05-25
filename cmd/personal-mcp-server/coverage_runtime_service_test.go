package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	serviceres "github.com/noumena-labs-llc/personal-mcp-server/internal/service"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/shell"
)

func captureStdStreams(t *testing.T, fn func()) (stdout, stderr string) {
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

func writeFakeLaunchctl(t *testing.T, dir string) string {
	t.Helper()
	script := `case "${1:-}" in
  print)
    echo "pid = 123"
    echo "last exit code = 0"
    exit 0
    ;;
  bootout|bootstrap|kickstart)
    exit 0
    ;;
esac
echo "unexpected launchctl args: $*" >&2
exit 2`
	return writeExecutableScript(t, dir, "launchctl", script)
}

func TestRuntimeLifecycleHelpersDirect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	h1, err := fileHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("beta"), 0o600); err != nil {
		t.Fatal(err)
	}
	h2, err := fileHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("fileHash should change after file content changes")
	}

	state := &runtimeState{idle: make(chan struct{}), audit: nil, runner: nil}
	live := newLiveHandler(state)
	prev := live.Swap(&runtimeState{idle: make(chan struct{})})
	if prev != state {
		t.Fatalf("Swap returned %p, want previous state %p", prev, state)
	}
	prev.closeIdle()
	prev.Close()

	state = &runtimeState{
		handler: nil,
		idle:    make(chan struct{}),
		runner:  &shell.Runner{},
	}
	live = newLiveHandler(state)
	live.Close()
}

func TestServiceLifecycleHelpersDirect(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("service helper branch coverage targets macOS launchctl path")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("PERSONAL_MCP_ROOT", root)
	t.Setenv("PERSONAL_MCP_TOKEN", "service-token")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, dir := range []string{filepath.Join(root, "config"), filepath.Join(root, "bin"), filepath.Join(root, "logs"), filepath.Join(root, "state"), filepath.Join(home, "Library", "LaunchAgents"), filepath.Join(home, ".config")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	configPath := writeStarterConfigFile(t, root)
	if err := os.WriteFile(filepath.Join(root, "config", "token"), []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "trusted-projects.toml"), []byte("trusted = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBinary := writeExecutableScript(t, root, "fakebin", `if [ "$1" = "version" ]; then
  echo "personal-mcp-server `+version+`"
  exit 0
fi
echo "fake service binary"`)
	launchctl := writeFakeLaunchctl(t, root)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	spec, err := loadServiceSpec(fakeBinary, configPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := spec.Platforms["darwin"].ManifestPath
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(serviceres.LaunchAgentPlist(spec)), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut := captureStdStreams(t, func() {
		if err := serviceInstall(fakeBinary, configPath); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "installed LaunchAgent") || errOut != "" {
		t.Fatalf("serviceInstall output:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}

	out, errOut = captureStdStreams(t, func() {
		if err := serviceStatus(fakeBinary, configPath); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "loaded:   true") || errOut != "" {
		t.Fatalf("serviceStatus output:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}

	_, errOut = captureStdStreams(t, func() {
		if err := serviceRestart(); err != nil {
			t.Fatal(err)
		}
	})
	if errOut != "" {
		t.Fatalf("serviceRestart stderr:\n%s", errOut)
	}

	if err := os.WriteFile(manifestPath, []byte(serviceres.LaunchAgentPlist(spec)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut = captureStdStreams(t, func() {
		if err := printLaunchAgentStatus(spec); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "loaded:   true") || !strings.Contains(out, "manifest_check: ok") || errOut != "" {
		t.Fatalf("printLaunchAgentStatus output:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}

	out, errOut = captureStdStreams(t, func() {
		if err := serviceUninstall(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "removed LaunchAgent") || errOut != "" {
		t.Fatalf("serviceUninstall output:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}

	if got := launchctl; !strings.Contains(got, "launchctl") {
		t.Fatal("fake launchctl not created")
	}
	if _, err := samePath(fakeBinary, fakeBinary); err != nil {
		t.Fatal(err)
	}
	if got := boolStatus(false); got != "false" {
		t.Fatalf("boolStatus(false) = %q", got)
	}
	if got := valueOrUnknown("value"); got != "value" {
		t.Fatalf("valueOrUnknown(value) = %q", got)
	}
	if err := requireDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "some"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := requireOptionalParent(filepath.Join(root, "some", "path")); err != nil {
		t.Fatal(err)
	}
}

func TestStartLaunchAgentUsesKickstartWhenAlreadyLoaded(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("service helper branch coverage targets macOS launchctl path")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("PERSONAL_MCP_ROOT", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, dir := range []string{filepath.Join(root, "bin"), filepath.Join(home, "Library", "LaunchAgents"), filepath.Join(home, ".config")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(root, "bootstrap-called")
	writeExecutableScript(t, root, "launchctl", `case "${1:-}" in
  print)
    echo "pid = 123"
    exit 0
    ;;
  bootstrap)
    touch "`+marker+`"
    echo "Bootstrap failed: 5: Input/output error" >&2
    exit 5
    ;;
  kickstart)
    exit 0
    ;;
esac
echo "unexpected launchctl args: $*" >&2
exit 2`)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	plistPath, err := launchAgentPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("<plist/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := startLaunchAgent(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("bootstrap should not run for an already-loaded LaunchAgent, stat err = %v", err)
	}
}

func TestServiceRestartIgnoresStoppedLaunchAgent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("service helper branch coverage targets macOS launchctl path")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("PERSONAL_MCP_ROOT", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, dir := range []string{filepath.Join(root, "bin"), filepath.Join(home, "Library", "LaunchAgents"), filepath.Join(home, ".config")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutableScript(t, root, "launchctl", `case "${1:-}" in
  bootout)
    echo "Boot-out failed: 3: No such process" >&2
    exit 3
    ;;
  print)
    exit 1
    ;;
  bootstrap|kickstart)
    exit 0
    ;;
esac
echo "unexpected launchctl args: $*" >&2
exit 2`)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	plistPath, err := launchAgentPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plistPath, []byte("<plist/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureStdStreams(t, func() {
		if err := serviceRestart(); err != nil {
			t.Fatal(err)
		}
	})
	if stdout != "" {
		t.Fatalf("serviceRestart stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("serviceRestart stderr = %q, want empty", stderr)
	}
}
