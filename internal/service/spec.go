package service

import (
	"bufio"
	"embed"
	"fmt"
	"html"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed specs/*.yaml
var specFS embed.FS

const DefaultSpecName = "personal-mcp-server.yaml"

type Spec struct {
	Version   int
	Service   Identity
	Process   Process
	Paths     Paths
	Restart   Restart
	Platforms map[string]Platform
}

type Identity struct {
	ID          string
	Label       string
	Description string
}

type Process struct {
	Executable       string
	Args             []string
	WorkingDirectory string
}

type Paths struct {
	Root       string
	ConfigDir  string
	ConfigFile string
	TokenFile  string
	TrustStore string
	StateDir   string
	LogsDir    string
	StdoutLog  string
	StderrLog  string
}

type Restart struct {
	Policy       string
	DelaySeconds int
}

type Platform struct {
	Backend      string
	ManifestPath string
}

type Vars map[string]string

func LoadDefaultSpec(vars Vars) (Spec, error) {
	return LoadSpec(DefaultSpecName, vars)
}

func LoadSpec(name string, vars Vars) (Spec, error) {
	body, err := specFS.ReadFile(filepath.Join("specs", name))
	if err != nil {
		return Spec{}, err
	}
	spec, err := ParseSpecYAML(string(body))
	if err != nil {
		return Spec{}, err
	}
	return ExpandSpec(spec, vars)
}

func ParseSpecYAML(body string) (Spec, error) {
	spec := Spec{Platforms: map[string]Platform{}}
	section := ""
	platformName := ""
	inArgs := false
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		indent := countLeadingSpaces(raw)
		if indent == 0 {
			inArgs = false
			platformName = ""
			key, value, ok := splitYAMLKey(line)
			if !ok {
				return Spec{}, fmt.Errorf("invalid top-level line %q", raw)
			}
			if value == "" {
				section = key
				continue
			}
			if key == "version" {
				version, err := strconv.Atoi(value)
				if err != nil {
					return Spec{}, fmt.Errorf("invalid version %q", value)
				}
				spec.Version = version
				section = ""
				continue
			}
			return Spec{}, fmt.Errorf("unsupported top-level key %q", key)
		}
		if section == "platforms" && indent == 2 && strings.HasSuffix(line, ":") {
			inArgs = false
			platformName = strings.TrimSuffix(line, ":")
			if platformName == "" {
				return Spec{}, fmt.Errorf("empty platform name")
			}
			spec.Platforms[platformName] = Platform{}
			continue
		}
		if section == "process" && inArgs && indent == 4 && strings.HasPrefix(line, "- ") {
			spec.Process.Args = append(spec.Process.Args, unquoteYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "- "))))
			continue
		}
		key, value, ok := splitYAMLKey(line)
		if !ok {
			return Spec{}, fmt.Errorf("invalid line %q", raw)
		}
		value = unquoteYAMLScalar(value)
		if section == "process" && indent == 2 && key == "args" && value == "" {
			inArgs = true
			continue
		}
		inArgs = false
		switch section {
		case "service":
			assignIdentity(&spec.Service, key, value)
		case "process":
			assignProcess(&spec.Process, key, value)
		case "paths":
			assignPaths(&spec.Paths, key, value)
		case "restart":
			if err := assignRestart(&spec.Restart, key, value); err != nil {
				return Spec{}, err
			}
		case "platforms":
			if platformName == "" {
				return Spec{}, fmt.Errorf("platform field %q without platform name", key)
			}
			platform := spec.Platforms[platformName]
			switch key {
			case "backend":
				platform.Backend = value
			case "manifest_path":
				platform.ManifestPath = value
			default:
				return Spec{}, fmt.Errorf("unsupported platform key %q", key)
			}
			spec.Platforms[platformName] = platform
		default:
			return Spec{}, fmt.Errorf("unsupported section %q", section)
		}
	}
	if err := scanner.Err(); err != nil {
		return Spec{}, err
	}
	return validateSpec(spec)
}

func assignIdentity(identity *Identity, key, value string) {
	switch key {
	case "id":
		identity.ID = value
	case "label":
		identity.Label = value
	case "description":
		identity.Description = value
	}
}

func assignProcess(process *Process, key, value string) {
	switch key {
	case "executable":
		process.Executable = value
	case "working_directory":
		process.WorkingDirectory = value
	}
}

func assignPaths(paths *Paths, key, value string) {
	switch key {
	case "root":
		paths.Root = value
	case "config_dir":
		paths.ConfigDir = value
	case "config_file":
		paths.ConfigFile = value
	case "token_file":
		paths.TokenFile = value
	case "trust_store":
		paths.TrustStore = value
	case "state_dir":
		paths.StateDir = value
	case "logs_dir":
		paths.LogsDir = value
	case "stdout_log":
		paths.StdoutLog = value
	case "stderr_log":
		paths.StderrLog = value
	}
}

func assignRestart(restart *Restart, key, value string) error {
	switch key {
	case "policy":
		restart.Policy = value
	case "delay_seconds":
		delay, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid restart delay %q", value)
		}
		restart.DelaySeconds = delay
	}
	return nil
}

func validateSpec(spec Spec) (Spec, error) {
	if spec.Version != 1 {
		return Spec{}, fmt.Errorf("unsupported service spec version %d", spec.Version)
	}
	if spec.Service.ID == "" || spec.Service.Label == "" || spec.Service.Description == "" {
		return Spec{}, fmt.Errorf("service id, label, and description are required")
	}
	if spec.Process.Executable == "" || len(spec.Process.Args) == 0 {
		return Spec{}, fmt.Errorf("process executable and args are required")
	}
	if spec.Paths.ConfigFile == "" || spec.Paths.StdoutLog == "" || spec.Paths.StderrLog == "" {
		return Spec{}, fmt.Errorf("config and log paths are required")
	}
	if spec.Restart.Policy != "on_failure" {
		return Spec{}, fmt.Errorf("unsupported restart policy %q", spec.Restart.Policy)
	}
	if len(spec.Platforms) == 0 {
		return Spec{}, fmt.Errorf("at least one platform is required")
	}
	for name, platform := range spec.Platforms {
		if platform.Backend == "" || platform.ManifestPath == "" {
			return Spec{}, fmt.Errorf("platform %q requires backend and manifest_path", name)
		}
	}
	return spec, nil
}

func ExpandSpec(spec Spec, vars Vars) (Spec, error) {
	vars = withServiceVars(vars, spec.Service)
	expand := func(s string) string { return expandVars(s, vars) }
	spec.Process.Executable = expand(spec.Process.Executable)
	spec.Process.WorkingDirectory = expand(spec.Process.WorkingDirectory)
	for i := range spec.Process.Args {
		spec.Process.Args[i] = expand(spec.Process.Args[i])
	}
	spec.Paths.Root = expand(spec.Paths.Root)
	spec.Paths.ConfigDir = expand(spec.Paths.ConfigDir)
	spec.Paths.ConfigFile = expand(spec.Paths.ConfigFile)
	spec.Paths.TokenFile = expand(spec.Paths.TokenFile)
	spec.Paths.TrustStore = expand(spec.Paths.TrustStore)
	spec.Paths.StateDir = expand(spec.Paths.StateDir)
	spec.Paths.LogsDir = expand(spec.Paths.LogsDir)
	spec.Paths.StdoutLog = expand(spec.Paths.StdoutLog)
	spec.Paths.StderrLog = expand(spec.Paths.StderrLog)
	for name, platform := range spec.Platforms {
		platform.ManifestPath = expand(platform.ManifestPath)
		spec.Platforms[name] = platform
	}
	if unresolvedSpecValue(spec) != "" {
		return Spec{}, fmt.Errorf("unresolved service spec variable in %q", unresolvedSpecValue(spec))
	}
	return validateSpec(spec)
}

func withServiceVars(vars Vars, identity Identity) Vars {
	out := Vars{}
	for key, value := range vars {
		out[key] = value
	}
	out["service.id"] = identity.ID
	out["service.label"] = identity.Label
	return out
}

func expandVars(s string, vars Vars) string {
	out := s
	for key, value := range vars {
		out = strings.ReplaceAll(out, "${"+key+"}", value)
	}
	return out
}

func unresolvedSpecValue(spec Spec) string {
	values := []string{spec.Process.Executable, spec.Process.WorkingDirectory, spec.Paths.Root, spec.Paths.ConfigDir, spec.Paths.ConfigFile, spec.Paths.TokenFile, spec.Paths.TrustStore, spec.Paths.StateDir, spec.Paths.LogsDir, spec.Paths.StdoutLog, spec.Paths.StderrLog}
	values = append(values, spec.Process.Args...)
	for _, platform := range spec.Platforms {
		values = append(values, platform.ManifestPath)
	}
	for _, value := range values {
		if strings.Contains(value, "${") {
			return value
		}
	}
	return ""
}

func LaunchAgentPlist(spec Spec) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
%s  </array>
  <key>WorkingDirectory</key><string>%s</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key><false/>
  </dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, xml(spec.Service.Label), launchAgentArgs(spec), xml(spec.Process.WorkingDirectory), xml(spec.Paths.StdoutLog), xml(spec.Paths.StderrLog))
}

func launchAgentArgs(spec Spec) string {
	parts := append([]string{spec.Process.Executable}, spec.Process.Args...)
	var b strings.Builder
	for _, part := range parts {
		b.WriteString("    <string>")
		b.WriteString(xml(part))
		b.WriteString("</string>\n")
	}
	return b.String()
}

func SystemdUserUnit(spec Spec) string {
	return fmt.Sprintf(`[Unit]
Description=%s
After=network.target

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=%d
NoNewPrivileges=true
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, spec.Service.Description, systemdExecStart(spec), systemdQuote(spec.Process.WorkingDirectory), spec.Restart.DelaySeconds, systemdQuote(spec.Paths.StdoutLog), systemdQuote(spec.Paths.StderrLog))
}

func systemdExecStart(spec Spec) string {
	parts := append([]string{spec.Process.Executable}, spec.Process.Args...)
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, systemdQuote(part))
	}
	return strings.Join(quoted, " ")
}

func systemdQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\\`, `\\\\`, `"`, `\"`, "\n", `\\n`, "\t", `\\t`).Replace(s) + `"`
}

func xml(s string) string {
	return html.EscapeString(s)
}

func countLeadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

func splitYAMLKey(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(key), strings.TrimSpace(value), true
}

func unquoteYAMLScalar(value string) string {
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}
