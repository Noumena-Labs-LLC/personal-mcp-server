package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/atomicfile"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	serviceres "github.com/noumena-labs-llc/personal-mcp-server/internal/service"
)

func serviceCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("service")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("service " + args[0]) {
			printCommandHelp("service")
		}
		return
	}
	fs := flagSet("service")
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	binary := fs.String("binary", defaultBinaryPath(), "path to personal-mcp-server binary")
	userOnly := fs.Bool("user", false, "install/manage the current user's service")
	_ = fs.Parse(args[1:])
	switch args[0] {
	case "print-launchagent":
		spec, err := loadServiceSpec(*binary, *configPath)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(serviceres.LaunchAgentPlist(spec))
	case "print-systemd":
		spec, err := loadServiceSpec(*binary, *configPath)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(serviceres.SystemdUserUnit(spec))
	case "paths":
		spec, err := loadServiceSpec(*binary, *configPath)
		if err != nil {
			log.Fatal(err)
		}
		printServicePaths(spec)
	case "install":
		requireUserFlag(*userOnly)
		if err := serviceInstall(*binary, *configPath); err != nil {
			log.Fatal(err)
		}
	case "uninstall":
		requireUserFlag(*userOnly)
		if err := serviceUninstall(); err != nil {
			log.Fatal(err)
		}
	case "start":
		requireUserFlag(*userOnly)
		if err := serviceStart(); err != nil {
			log.Fatal(err)
		}
	case "stop":
		requireUserFlag(*userOnly)
		if err := serviceStop(); err != nil {
			log.Fatal(err)
		}
	case "restart":
		requireUserFlag(*userOnly)
		if err := serviceRestart(); err != nil {
			log.Fatal(err)
		}
	case "logs":
		requireUserFlag(*userOnly)
		if err := serviceLogs(); err != nil {
			log.Fatal(err)
		}
	case "doctor":
		requireUserFlag(*userOnly)
		if err := serviceDoctor(*binary, *configPath); err != nil {
			log.Fatal(err)
		}
	case "status":
		requireUserFlag(*userOnly)
		if err := serviceStatus(*binary, *configPath); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

const (
	serviceLabel       = "com.noumenalabs.personal-mcp-server"
	systemdServiceName = "personal-mcp-server.service"
)

func requireUserFlag(userOnly bool) {
	if !userOnly {
		log.Fatal("service management commands require --user")
	}
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		log.Fatal("refusing to manage a user service as root; run this command as the target user")
	}
}

func serviceInstall(binary, configPath string) error {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(binary, configPath)
	case "linux":
		return installSystemdUserUnit(binary, configPath)
	default:
		return fmt.Errorf("service install is only supported on macOS and Linux")
	}
}

func serviceUninstall() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchAgent()
	case "linux":
		return uninstallSystemdUserUnit()
	default:
		return fmt.Errorf("service uninstall is only supported on macOS and Linux")
	}
}

func serviceStart() error {
	switch runtime.GOOS {
	case "darwin":
		return startLaunchAgent()
	case "linux":
		return runServiceCommand("systemctl", "--user", "start", systemdServiceName)
	default:
		return fmt.Errorf("service start is only supported on macOS and Linux")
	}
}

func serviceStop() error {
	switch runtime.GOOS {
	case "darwin":
		return stopLaunchAgent()
	case "linux":
		return runServiceCommand("systemctl", "--user", "stop", systemdServiceName)
	default:
		return fmt.Errorf("service stop is only supported on macOS and Linux")
	}
}

func serviceRestart() error {
	switch runtime.GOOS {
	case "darwin":
		if err := stopLaunchAgent(); err != nil {
			fmt.Fprintf(os.Stderr, "launchctl stop warning: %v\n", err)
		}
		return startLaunchAgent()
	case "linux":
		return runServiceCommand("systemctl", "--user", "restart", systemdServiceName)
	default:
		return fmt.Errorf("service restart is only supported on macOS and Linux")
	}
}

func serviceLogs() error {
	spec, err := loadServiceSpec(defaultBinaryPath(), defaultConfigPath())
	if err != nil {
		return err
	}
	fmt.Printf("stdout: %s\n", spec.Paths.StdoutLog)
	fmt.Printf("stderr: %s\n", spec.Paths.StderrLog)
	fmt.Printf("tail:   tail -f %q %q\n", spec.Paths.StdoutLog, spec.Paths.StderrLog)
	return nil
}

func serviceDoctor(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	ok := true
	check := func(name string, err error) {
		if err != nil {
			ok = false
			fmt.Printf("%s: FAIL: %v\n", name, err)
			return
		}
		fmt.Printf("%s: ok\n", name)
	}
	warn := func(name string, err error) {
		if err != nil {
			fmt.Printf("%s: WARN: %v\n", name, err)
			return
		}
		fmt.Printf("%s: ok\n", name)
	}
	checkManifest := func(name, path, binaryPath, configFile string) {
		err := requireManifestReferences(path, binaryPath, configFile)
		if os.IsNotExist(err) {
			warn(name, fmt.Errorf("manifest is not installed yet: %s", path))
			return
		}
		check(name, err)
	}
	check("root directory", requireDirectory(spec.Paths.Root))
	check("config file", requireReadableFile(spec.Paths.ConfigFile))
	check("config validation", validateConfigFile(spec.Paths.ConfigFile))
	check("token file", requirePrivateReadableFile(spec.Paths.TokenFile))
	check("trusted projects store", requireOptionalParent(spec.Paths.TrustStore))
	check("state directory", requireDirectory(spec.Paths.StateDir))
	check("logs directory", requireDirectory(spec.Paths.LogsDir))
	check("install binary", requireExecutableFile(spec.Process.Executable))
	check("installed binary version", requireInstalledBinaryVersion(spec.Process.Executable))
	switch runtime.GOOS {
	case "darwin":
		check("launchctl", requireExecutableOnPath("launchctl"))
		check("LaunchAgent manifest", requireOptionalParent(spec.Platforms["darwin"].ManifestPath))
		checkManifest("LaunchAgent manifest content", spec.Platforms["darwin"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile)
	case "linux":
		check("systemctl", requireExecutableOnPath("systemctl"))
		check("systemd user unit", requireOptionalParent(spec.Platforms["linux"].ManifestPath))
		checkManifest("systemd user unit content", spec.Platforms["linux"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile)
	default:
		fmt.Printf("service backend: WARN: unsupported platform %s\n", runtime.GOOS)
	}
	if !ok {
		return fmt.Errorf("service doctor found problems")
	}
	return nil
}

func requireDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return nil
}

func requireReadableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory: %s", path)
	}
	f, err := os.Open(path) //nolint:gosec // service doctor checks explicit local service paths.
	if err != nil {
		return err
	}
	return f.Close()
}

func requirePrivateReadableFile(path string) error {
	if err := requireReadableFile(path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by group or others (%s)", path, info.Mode().Perm())
	}
	return nil
}

func requireOptionalParent(path string) error {
	return requireDirectory(filepath.Dir(path))
}

func requireExecutableFile(path string) error {
	if err := requireReadableFile(path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("not executable: %s", path)
	}
	return nil
}

func requireExecutableOnPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func validateConfigFile(path string) error {
	_, err := config.Load(path)
	return err
}

func requireInstalledBinaryVersion(path string) error {
	cmd := exec.CommandContext(context.Background(), path, "version") //nolint:gosec // service doctor runs the configured user-local personal-mcp-server binary.
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	got := strings.TrimSpace(string(out))
	want := "personal-mcp-server " + version
	firstLine, _, _ := strings.Cut(got, "\n")
	if firstLine != want {
		return fmt.Errorf("installed binary reports first line %q, want %q", firstLine, want)
	}
	return nil
}

func requireManifestReferences(path, binary, configPath string) error {
	body, err := os.ReadFile(path) //nolint:gosec // service doctor checks the explicit local service manifest path.
	if err != nil {
		return err
	}
	text := string(body)
	if !manifestContainsPath(text, binary) {
		return fmt.Errorf("manifest does not reference expected binary %s", binary)
	}
	if !manifestContainsPath(text, configPath) {
		return fmt.Errorf("manifest does not reference expected config %s", configPath)
	}
	return nil
}

func manifestContainsPath(text, path string) bool {
	return strings.Contains(text, path) || strings.Contains(text, html.EscapeString(path)) || strings.Contains(text, systemdManifestQuote(path))
}

func systemdManifestQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}

func serviceStatus(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	printServiceStatusHeader(spec)
	switch runtime.GOOS {
	case "darwin":
		return printLaunchAgentStatus(spec)
	case "linux":
		return printSystemdUserStatus(spec)
	default:
		return fmt.Errorf("service status is only supported on macOS and Linux")
	}
}

func printServiceStatusHeader(spec serviceres.Spec) {
	manifestPath := ""
	backend := "unsupported"
	if platform, ok := spec.Platforms[runtime.GOOS]; ok {
		manifestPath = platform.ManifestPath
		backend = platform.Backend
	}
	fmt.Printf("service:  %s\n", spec.Service.Label)
	fmt.Printf("backend:  %s\n", backend)
	fmt.Printf("manifest: %s\n", manifestPath)
	fmt.Printf("binary:   %s\n", spec.Process.Executable)
	fmt.Printf("config:   %s\n", spec.Paths.ConfigFile)
	fmt.Printf("token:    %s\n", spec.Paths.TokenFile)
	fmt.Printf("stdout:   %s\n", spec.Paths.StdoutLog)
	fmt.Printf("stderr:   %s\n", spec.Paths.StderrLog)
}

func printLaunchAgentStatus(spec serviceres.Spec) error {
	fmt.Printf("target:   %s\n", launchAgentServiceTarget())
	if _, err := exec.LookPath("launchctl"); err != nil {
		fmt.Printf("manager:  missing launchctl: %v\n", err)
		return nil
	}
	out, err := captureServiceCommand("launchctl", "print", launchAgentServiceTarget())
	if err != nil {
		fmt.Printf("loaded:   false\n")
		fmt.Printf("manager:  launchctl print failed: %v\n", err)
		return nil
	}
	status := parseLaunchctlPrint(out)
	fmt.Printf("loaded:   true\n")
	fmt.Printf("running:  %s\n", boolStatus(status.PID != ""))
	if status.PID != "" {
		fmt.Printf("pid:      %s\n", status.PID)
	}
	if status.LastExitCode != "" {
		fmt.Printf("last_exit_code: %s\n", status.LastExitCode)
	}
	if err := requireManifestReferences(spec.Platforms["darwin"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile); err != nil {
		fmt.Printf("manifest_check: WARN: %v\n", err)
	} else {
		fmt.Printf("manifest_check: ok\n")
	}
	return nil
}

func printSystemdUserStatus(spec serviceres.Spec) error {
	fmt.Printf("unit:     %s\n", systemdServiceName)
	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Printf("manager:  missing systemctl: %v\n", err)
		return nil
	}
	out, err := captureServiceCommand("systemctl", "--user", "show", systemdServiceName, "--property=LoadState,ActiveState,SubState,MainPID,ExecMainStatus")
	if err != nil {
		fmt.Printf("manager:  systemctl show failed: %v\n", err)
		return nil
	}
	status := parseSystemctlShow(out)
	fmt.Printf("load_state: %s\n", valueOrUnknown(status["LoadState"]))
	fmt.Printf("active_state: %s\n", valueOrUnknown(status["ActiveState"]))
	fmt.Printf("sub_state: %s\n", valueOrUnknown(status["SubState"]))
	if pid := status["MainPID"]; pid != "" && pid != "0" {
		fmt.Printf("pid:      %s\n", pid)
	}
	if exitStatus := status["ExecMainStatus"]; exitStatus != "" {
		fmt.Printf("exec_main_status: %s\n", exitStatus)
	}
	if err := requireManifestReferences(spec.Platforms["linux"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile); err != nil {
		fmt.Printf("manifest_check: WARN: %v\n", err)
	} else {
		fmt.Printf("manifest_check: ok\n")
	}
	return nil
}

type launchctlStatus struct {
	PID          string
	LastExitCode string
}

func parseLaunchctlPrint(out string) launchctlStatus {
	status := launchctlStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "pid = "):
			status.PID = strings.TrimSpace(strings.TrimPrefix(line, "pid = "))
		case strings.HasPrefix(line, "last exit code = "):
			status.LastExitCode = strings.TrimSpace(strings.TrimPrefix(line, "last exit code = "))
		}
	}
	return status
}

func parseSystemctlShow(out string) map[string]string {
	status := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		status[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return status
}

func captureServiceCommand(name string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed service-management commands, not user-controlled shell.
	body, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(body)), err
}

func boolStatus(ok bool) string {
	if ok {
		return "true"
	}
	return "false"
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func installLaunchAgent(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	if err := prepareServiceInstall(spec); err != nil {
		return err
	}
	plistPath := spec.Platforms["darwin"].ManifestPath
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o750); err != nil {
		return err
	}
	if err := atomicfile.WriteFile(plistPath, []byte(serviceres.LaunchAgentPlist(spec)), 0o600); err != nil {
		return err
	}
	fmt.Printf("installed LaunchAgent: %s\n", plistPath)
	if err := bootoutLaunchAgentIfLoaded(); err != nil {
		fmt.Fprintf(os.Stderr, "launchctl bootout warning: %v\n", err)
	}
	return startLaunchAgent()
}

func uninstallLaunchAgent() error {
	plistPath, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := bootoutLaunchAgentIfLoaded(); err != nil {
		fmt.Fprintf(os.Stderr, "launchctl bootout warning: %v\n", err)
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("removed LaunchAgent: %s\n", plistPath)
	return nil
}

func installSystemdUserUnit(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	if err := prepareServiceInstall(spec); err != nil {
		return err
	}
	unitPath := spec.Platforms["linux"].ManifestPath
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o750); err != nil {
		return err
	}
	if err := atomicfile.WriteFile(unitPath, []byte(serviceres.SystemdUserUnit(spec)), 0o600); err != nil {
		return err
	}
	fmt.Printf("installed systemd user unit: %s\n", unitPath)
	if err := runServiceCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return runServiceCommand("systemctl", "--user", "enable", "--now", systemdServiceName)
}

func uninstallSystemdUserUnit() error {
	unitPath, err := systemdUserUnitPath()
	if err != nil {
		return err
	}
	if err := runServiceCommand("systemctl", "--user", "disable", "--now", systemdServiceName); err != nil {
		fmt.Fprintf(os.Stderr, "systemctl disable warning: %v\n", err)
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("removed systemd user unit: %s\n", unitPath)
	return runServiceCommand("systemctl", "--user", "daemon-reload")
}

func startLaunchAgent() error {
	plistPath, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := runServiceCommand("launchctl", "bootstrap", launchAgentDomain(), plistPath); err != nil {
		return err
	}
	return runServiceCommand("launchctl", "kickstart", "-k", launchAgentServiceTarget())
}

func stopLaunchAgent() error {
	return runServiceCommand("launchctl", "bootout", launchAgentServiceTarget())
}

func bootoutLaunchAgentIfLoaded() error {
	out, err := captureServiceCommand("launchctl", "bootout", launchAgentServiceTarget())
	if err == nil {
		return nil
	}
	if isLaunchctlNoSuchProcess(out, err) {
		return nil
	}
	if out != "" {
		fmt.Fprintln(os.Stderr, out)
	}
	return err
}

func isLaunchctlNoSuchProcess(out string, err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(out, "Boot-out failed: 3: No such process") ||
		strings.Contains(out, "No such process") ||
		strings.Contains(out, "Could not find service")
}

func prepareServiceInstall(spec serviceres.Spec) error {
	if err := requireConfigFileExists(spec.Paths.ConfigFile); err != nil {
		return err
	}
	for _, dir := range []string{spec.Paths.Root, spec.Paths.ConfigDir, spec.Paths.StateDir, spec.Paths.LogsDir, filepath.Dir(spec.Process.Executable)} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return installCurrentBinary(spec.Process.Executable)
}

func requireConfigFileExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config file does not exist: %s; run personal-mcp-server init --generate-token first", path)
		}
		return err
	}
	return nil
}

func installCurrentBinary(target string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	source = expandUserPath(source)
	target = expandUserPath(target)
	same, err := samePath(source, target)
	if err != nil {
		return err
	}
	if same {
		return nil
	}
	if err := copyExecutable(source, target); err != nil {
		return err
	}
	fmt.Printf("installed binary: %s\n", target)
	return nil
}

func samePath(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	realA, errA := filepath.EvalSymlinks(absA)
	realB, errB := filepath.EvalSymlinks(absB)
	if errA == nil {
		absA = realA
	}
	if errB == nil {
		absB = realB
	}
	return absA == absB, nil
}

func copyExecutable(source, target string) error {
	in, err := os.Open(source) //nolint:gosec // service install copies the current executable path reported by the OS.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // target is the validated user-local service binary path.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, 0o755) //nolint:gosec // installed service binary must be executable by the current user.
}

func launchAgentPath() (string, error) {
	spec, err := loadServiceSpec(defaultBinaryPath(), defaultConfigPath())
	if err != nil {
		return "", err
	}
	return spec.Platforms["darwin"].ManifestPath, nil
}

func systemdUserUnitPath() (string, error) {
	spec, err := loadServiceSpec(defaultBinaryPath(), defaultConfigPath())
	if err != nil {
		return "", err
	}
	return spec.Platforms["linux"].ManifestPath, nil
}

func launchAgentDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func launchAgentServiceTarget() string {
	return launchAgentDomain() + "/" + serviceLabel
}

func runServiceCommand(name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed service-management commands, not user-controlled shell.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func loadServiceSpec(binary, configPath string) (serviceres.Spec, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return serviceres.Spec{}, err
	}
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		configDir = filepath.Join(home, ".config")
	}
	return serviceres.LoadDefaultSpec(serviceres.Vars{
		"app_root":        defaultAppRoot(),
		"install_bin":     expandUserPath(binary),
		"config_file":     expandUserPath(configPath),
		"home":            home,
		"user_config_dir": configDir,
	})
}

func printServicePaths(spec serviceres.Spec) {
	fmt.Printf("root:        %s\n", spec.Paths.Root)
	fmt.Printf("binary:      %s\n", spec.Process.Executable)
	fmt.Printf("config:      %s\n", spec.Paths.ConfigFile)
	fmt.Printf("token:       %s\n", spec.Paths.TokenFile)
	fmt.Printf("trust:       %s\n", spec.Paths.TrustStore)
	fmt.Printf("state:       %s\n", spec.Paths.StateDir)
	fmt.Printf("logs:        %s\n", spec.Paths.LogsDir)
	fmt.Printf("macos plist: %s\n", spec.Platforms["darwin"].ManifestPath)
	fmt.Printf("linux unit:  %s\n", spec.Platforms["linux"].ManifestPath)
}
