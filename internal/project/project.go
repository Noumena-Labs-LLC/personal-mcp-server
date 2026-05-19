package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/atomicfile"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

const DefaultFilename = ".personal-mcp-server.toml"

type Metadata struct {
	Name        string   `toml:"name" json:"name,omitempty"`
	Description string   `toml:"description" json:"description,omitempty"`
	Languages   []string `toml:"languages" json:"languages,omitempty"`
}

type SearchDefaults struct {
	IncludeGlobs []string `toml:"include_globs" json:"include_globs,omitempty"`
	ExcludeGlobs []string `toml:"exclude_globs" json:"exclude_globs,omitempty"`
}

type WorkflowAliases struct {
	Test      string `toml:"test" json:"test,omitempty"`
	Lint      string `toml:"lint" json:"lint,omitempty"`
	Format    string `toml:"format" json:"format,omitempty"`
	Build     string `toml:"build" json:"build,omitempty"`
	CI        string `toml:"ci" json:"ci,omitempty"`
	Typecheck string `toml:"typecheck" json:"typecheck,omitempty"`
}

type Guide struct {
	Summary   string   `toml:"summary" json:"summary,omitempty"`
	ReadFirst []string `toml:"read_first" json:"read_first,omitempty"`
}

type ProtectedFiles struct {
	DenyEdit   []string `toml:"deny_edit" json:"deny_edit,omitempty"`
	PromptEdit []string `toml:"prompt_edit" json:"prompt_edit,omitempty"`
}

type GeneratedFiles struct {
	Paths         []string `toml:"paths" json:"paths,omitempty"`
	DefaultAction string   `toml:"default_action" json:"default_action,omitempty"`
}

type CommandEnvironment struct {
	RunMode string `toml:"run_mode" json:"run_mode,omitempty"`
	Shell   string `toml:"shell" json:"shell,omitempty"`
}

type Config struct {
	ConfigKind       string                       `toml:"config_kind" json:"config_kind"`
	ConfigVersion    int                          `toml:"config_version" json:"config_version"`
	Project          Metadata                     `toml:"project" json:"project"`
	Guide            Guide                        `toml:"guide" json:"guide,omitempty"`
	Search           SearchDefaults               `toml:"search" json:"search,omitempty"`
	Workflows        WorkflowAliases              `toml:"workflows" json:"workflows,omitempty"`
	Commands         []config.CommandSpec         `toml:"commands" json:"commands,omitempty"`
	CommandSequences []config.CommandSequenceSpec `toml:"command_sequences" json:"command_sequences,omitempty"`
	CommandPolicy    config.CommandPolicyConfig   `toml:"command_policy" json:"command_policy,omitempty"`
	FilePolicy       config.FilePolicyConfig      `toml:"file_policy" json:"file_policy,omitempty"`
	ProtectedFiles   ProtectedFiles               `toml:"protected_files" json:"protected_files,omitempty"`
	Generated        GeneratedFiles               `toml:"generated" json:"generated,omitempty"`
	CommandEnv       CommandEnvironment           `toml:"command_environment" json:"command_environment,omitempty"`
}

type TrustedProject struct {
	Root      string `toml:"root" json:"root"`
	Config    string `toml:"config" json:"config"`
	Trusted   bool   `toml:"trusted" json:"trusted"`
	TrustedAt string `toml:"trusted_at" json:"trusted_at,omitempty"`
}

type TrustStore struct {
	Projects []TrustedProject `toml:"projects" json:"projects"`
}

type State struct {
	Found   bool    `json:"found"`
	Trusted bool    `json:"trusted"`
	Root    string  `json:"root,omitempty"`
	Path    string  `json:"path,omitempty"`
	Config  *Config `json:"config,omitempty"`
	Error   string  `json:"error,omitempty"`
}

const trustStoreRefreshTTL = 2 * time.Second

type Manager struct {
	mu               sync.RWMutex
	Global           *config.Config
	Sandbox          *fsx.Sandbox
	Filename         string
	TrustPath        string
	AutoLoad         bool
	Trusted          map[string]TrustedProject
	lastTrustRefresh time.Time
	lastTrustModTime time.Time
	lastTrustSize    int64
}

func NewManager(global *config.Config, sandbox *fsx.Sandbox) (*Manager, error) {
	filename := strings.TrimSpace(global.ProjectConfigs.Filename)
	if filename == "" {
		filename = DefaultFilename
	}
	trustPath := strings.TrimSpace(global.ProjectConfigs.TrustStore)
	if trustPath == "" {
		trustPath = defaultTrustStorePath()
	}
	trustPath, err := expandAbs(trustPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{Global: global, Sandbox: sandbox, Filename: filename, TrustPath: trustPath, AutoLoad: global.ProjectConfigs.AutoLoad, Trusted: map[string]TrustedProject{}}
	_ = m.refreshTrustStore(true)
	return m, nil
}

func Enabled(global *config.Config) bool {
	return global != nil && global.ProjectConfigs.Enabled
}

func (m *Manager) Discover(cwd string) State {
	if m == nil || !Enabled(m.Global) {
		return State{Found: false}
	}
	if err := m.RefreshTrustStore(); err != nil {
		return State{Found: false, Error: err.Error()}
	}
	base, err := m.Sandbox.ResolveWithCwd(".", cwd)
	if err != nil {
		return State{Found: false, Error: err.Error()}
	}
	info, err := os.Stat(base)
	if err == nil && !info.IsDir() {
		base = filepath.Dir(base)
	}
	rootLimit, ok := m.Sandbox.RootFor(base)
	if !ok {
		return State{Found: false}
	}
	cur := base
	for {
		candidate := filepath.Join(cur, m.Filename)
		if _, err := os.Stat(candidate); err == nil {
			pc, loadErr := Load(candidate)
			state := State{Found: true, Root: cur, Path: candidate}
			if loadErr != nil {
				state.Error = loadErr.Error()
				return state
			}
			state.Config = pc
			state.Trusted = m.IsTrusted(cur)
			return state
		}
		if cur == rootLimit || cur == filepath.Dir(cur) {
			break
		}
		cur = filepath.Dir(cur)
	}
	return State{Found: false}
}

func (m *Manager) IsTrusted(root string) bool {
	if m == nil {
		return false
	}
	if m.AutoLoad {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.Trusted[canonical(root)]
	return ok && entry.Trusted
}

func (m *Manager) Trust(cwd string) (TrustedProject, error) {
	state := m.Discover(cwd)
	if !state.Found {
		return TrustedProject{}, errors.New("no project config found")
	}
	if state.Error != "" {
		return TrustedProject{}, fmt.Errorf("project config invalid: %s", state.Error)
	}
	entry := TrustedProject{Root: canonical(state.Root), Config: state.Path, Trusted: true, TrustedAt: time.Now().UTC().Format(time.RFC3339)}
	m.mu.Lock()
	m.Trusted[entry.Root] = entry
	m.mu.Unlock()
	if err := m.saveTrustStore(); err != nil {
		return TrustedProject{}, err
	}
	m.markTrustStoreFresh()
	return entry, nil
}

func (m *Manager) Untrust(cwd string) error {
	state := m.Discover(cwd)
	if !state.Found {
		return errors.New("no project config found")
	}
	m.mu.Lock()
	delete(m.Trusted, canonical(state.Root))
	m.mu.Unlock()
	if err := m.saveTrustStore(); err != nil {
		return err
	}
	m.markTrustStoreFresh()
	return nil
}

func (m *Manager) ListTrusted() []TrustedProject {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listTrustedLocked()
}

func (m *Manager) listTrustedLocked() []TrustedProject {
	items := make([]TrustedProject, 0, len(m.Trusted))
	for _, entry := range m.Trusted {
		items = append(items, entry)
	}
	return items
}

func (m *Manager) EffectiveInfo(cwd string, includeCommands bool) map[string]any {
	state := m.Discover(cwd)
	out := map[string]any{"enabled": Enabled(m.Global), "filename": m.Filename, "trust_store": m.TrustPath, "auto_load": m.AutoLoad, "project": state}
	if state.Found && state.Config != nil {
		cmds := make([]map[string]any, 0, len(state.Config.Commands))
		for i := range state.Config.Commands {
			cmd := &state.Config.Commands[i]
			item := map[string]any{"name": cmd.Name, "description": cmd.Description}
			if includeCommands {
				item["exec"] = cmd.Exec
				item["args"] = cmd.Args
			}
			cmds = append(cmds, item)
		}
		out["project_commands"] = cmds
		out["workflows"] = state.Config.Workflows
		out["guide"] = state.Config.Guide
		out["protected_files"] = state.Config.ProtectedFiles
		out["generated"] = state.Config.Generated
	}
	return out
}

func (m *Manager) WorkflowInfo(cwd string) map[string]any {
	state := m.Discover(cwd)
	out := map[string]any{"enabled": Enabled(m.Global), "project": state}
	if state.Found && state.Config != nil {
		out["workflows"] = state.Config.Workflows
		out["trusted"] = state.Trusted
		if !state.Trusted {
			out["hint"] = "Project config is discovered but not trusted; trust it before running project commands."
		}
	}
	return out
}

func (m *Manager) DecideProjectFile(operation, requestedPath, resolvedPath string) (decision policy.Decision, details map[string]any, matched bool, err error) {
	if m == nil || !Enabled(m.Global) {
		return policy.Decision{}, nil, false, nil
	}
	entry, ok := m.trustedProjectForResolved(resolvedPath)
	if !ok {
		return policy.Decision{}, map[string]any{"trusted": false}, false, nil
	}
	pc, loadErr := Load(entry.Config)
	if loadErr != nil {
		//nolint:nilerr // Project config load failures are reported as metadata and should not fail the file operation.
		return policy.Decision{}, map[string]any{"trusted": true, "error": loadErr.Error()}, false, nil
	}
	rel, err := projectRelativePath(entry.Root, requestedPath, resolvedPath)
	if err != nil {
		return policy.Decision{}, nil, false, err
	}
	details = map[string]any{"trusted": true, "project_root": entry.Root, "project_config": entry.Config, "relative_path": rel}
	if isEditOperation(operation) {
		if matched, pattern, err := matchAnyProjectGlob(pc.ProtectedFiles.DenyEdit, rel); err != nil {
			return policy.Decision{}, details, false, err
		} else if matched {
			details["source"] = "protected_files.deny_edit"
			details["pattern"] = pattern
			return policy.Decision{Action: policy.ActionDeny, Rule: "project:protected_files.deny_edit", Reason: "matched trusted project protected file deny rule"}, details, true, nil
		}
		if matched, pattern, err := matchAnyProjectGlob(pc.Generated.Paths, rel); err != nil {
			return policy.Decision{}, details, false, err
		} else if matched {
			action := strings.TrimSpace(pc.Generated.DefaultAction)
			if action == "" {
				action = policy.ActionPrompt
			}
			if err := policy.ValidateAction(action); err != nil {
				return policy.Decision{}, details, false, fmt.Errorf("project generated.default_action: %w", err)
			}
			details["source"] = "generated.paths"
			details["pattern"] = pattern
			return policy.Decision{Action: action, Rule: "project:generated.paths", Reason: "matched trusted project generated file rule"}, details, true, nil
		}
		if matched, pattern, err := matchAnyProjectGlob(pc.ProtectedFiles.PromptEdit, rel); err != nil {
			return policy.Decision{}, details, false, err
		} else if matched {
			details["source"] = "protected_files.prompt_edit"
			details["pattern"] = pattern
			return policy.Decision{Action: policy.ActionPrompt, Rule: "project:protected_files.prompt_edit", Reason: "matched trusted project protected file prompt rule"}, details, true, nil
		}
	}
	decision, err = policy.DecideFile(pc.FilePolicy, operation, rel, resolvedPath)
	if err != nil {
		return policy.Decision{}, details, false, err
	}
	if decision.Rule != "default" {
		details["source"] = "file_policy.rules"
		return policy.Decision{Action: decision.Action, Rule: "project:" + decision.Rule, Reason: decision.Reason}, details, true, nil
	}
	return policy.Decision{}, details, false, nil
}

func (m *Manager) trustedProjectForResolved(resolvedPath string) (TrustedProject, bool) {
	if err := m.RefreshTrustStore(); err != nil {
		return TrustedProject{}, false
	}
	root, ok := m.projectRootForResolved(resolvedPath)
	if !ok || !m.IsTrusted(root) {
		return TrustedProject{}, false
	}
	m.mu.RLock()
	entry, ok := m.Trusted[canonical(root)]
	m.mu.RUnlock()
	if !ok || !entry.Trusted {
		return TrustedProject{}, false
	}
	if strings.TrimSpace(entry.Config) == "" {
		entry.Config = filepath.Join(entry.Root, m.Filename)
	}
	if strings.TrimSpace(entry.Root) == "" {
		entry.Root = root
	}
	return entry, true
}

func projectRelativePath(root, requestedPath, resolvedPath string) (string, error) {
	rel, err := filepath.Rel(root, resolvedPath)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		if requestedPath != "" && !filepath.IsAbs(requestedPath) {
			return filepath.ToSlash(filepath.Clean(requestedPath)), nil
		}
	}
	return filepath.ToSlash(rel), nil
}

func (m *Manager) projectRootForResolved(resolvedPath string) (string, bool) {
	if m == nil {
		return "", false
	}
	best := ""
	clean := canonical(resolvedPath)
	m.mu.RLock()
	for root := range m.Trusted {
		if pathInside(root, clean) && len(root) > len(best) {
			best = root
		}
	}
	m.mu.RUnlock()
	if best != "" {
		return best, true
	}
	state := m.Discover(clean)
	if state.Found {
		return state.Root, true
	}
	return "", false
}

func isEditOperation(operation string) bool {
	switch operation {
	case "patch", "unified_patch", "create", "write":
		return true
	default:
		return false
	}
}

func matchAnyProjectGlob(patterns []string, rel string) (matched bool, pattern string, err error) {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matched, err := matchProjectGlob(pattern, rel)
		if err != nil {
			return false, pattern, err
		}
		if matched {
			return true, pattern, nil
		}
	}
	return false, "", nil
}

func matchProjectGlob(pattern, rel string) (bool, error) {
	pattern = filepath.ToSlash(pattern)
	rel = filepath.ToSlash(rel)
	re, err := regexp.Compile("^" + globToRegex(pattern) + "$")
	if err != nil {
		return false, fmt.Errorf("invalid project glob %q: %w", pattern, err)
	}
	if re.MatchString(rel) {
		return true, nil
	}
	if !strings.Contains(pattern, "/") {
		return re.MatchString(path.Base(rel)), nil
	}
	return false, nil
}

func globToRegex(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	return b.String()
}

func pathInside(root, p string) bool {
	root = canonical(root)
	p = canonical(p)
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

func Load(configPath string) (*Config, error) {
	b, err := os.ReadFile(configPath) //nolint:gosec // project config is discovered under configured roots.
	if err != nil {
		return nil, err
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) Validate() error {
	if c.ConfigKind == "" {
		c.ConfigKind = "project"
	}
	if c.ConfigKind != "project" {
		return fmt.Errorf("config_kind must be project, got %q", c.ConfigKind)
	}
	if c.ConfigVersion == 0 {
		return errors.New("config_version is required")
	}
	if c.ConfigVersion != 1 {
		return fmt.Errorf("unsupported project config_version %d", c.ConfigVersion)
	}
	seen := map[string]bool{}
	defaultRunMode := strings.TrimSpace(c.CommandEnv.RunMode)
	defaultShell := strings.TrimSpace(c.CommandEnv.Shell)
	if defaultRunMode != "" && defaultRunMode != "argv" && defaultRunMode != "persistent_shell" {
		return fmt.Errorf("command_environment has invalid run_mode %q", defaultRunMode)
	}
	if defaultRunMode == "persistent_shell" && defaultShell == "" {
		return errors.New("command_environment run_mode persistent_shell requires shell")
	}
	for _, pattern := range append(append([]string{}, c.ProtectedFiles.DenyEdit...), c.ProtectedFiles.PromptEdit...) {
		if strings.TrimSpace(pattern) == "" {
			return errors.New("protected_files patterns cannot be empty")
		}
		if _, err := regexp.Compile("^" + globToRegex(filepath.ToSlash(pattern)) + "$"); err != nil {
			return fmt.Errorf("invalid protected_files glob %q: %w", pattern, err)
		}
	}
	for _, pattern := range c.Generated.Paths {
		if strings.TrimSpace(pattern) == "" {
			return errors.New("generated paths cannot contain empty patterns")
		}
		if _, err := regexp.Compile("^" + globToRegex(filepath.ToSlash(pattern)) + "$"); err != nil {
			return fmt.Errorf("invalid generated path glob %q: %w", pattern, err)
		}
	}
	if err := policy.ValidateAction(strings.TrimSpace(c.Generated.DefaultAction)); err != nil {
		return fmt.Errorf("generated.default_action: %w", err)
	}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		if strings.TrimSpace(cmd.Name) == "" || strings.TrimSpace(cmd.Exec) == "" {
			return errors.New("project commands require name and exec")
		}
		if seen[cmd.Name] {
			return fmt.Errorf("duplicate project command name %q", cmd.Name)
		}
		seen[cmd.Name] = true
		if strings.ContainsAny(cmd.Exec, ";&|`$><\n") {
			return fmt.Errorf("project command %q exec contains shell syntax", cmd.Name)
		}
		for _, arg := range cmd.Args {
			if strings.Contains(arg, "\x00") {
				return fmt.Errorf("project command %q arg contains NUL", cmd.Name)
			}
		}
		if cmd.MaxExtraArgs < 0 {
			return fmt.Errorf("project command %q max_extra_args cannot be negative", cmd.Name)
		}
		if !cmd.AllowExtraArgs && len(cmd.ExtraArgs) > 0 {
			return fmt.Errorf("project command %q has extra_args rules but allow_extra_args is false", cmd.Name)
		}
		runMode := strings.TrimSpace(cmd.RunMode)
		if runMode == "" {
			runMode = defaultRunMode
		}
		shell := strings.TrimSpace(cmd.Shell)
		if shell == "" {
			shell = defaultShell
		}
		if runMode != "" && runMode != "argv" && runMode != "persistent_shell" {
			return fmt.Errorf("project command %q has invalid run_mode %q", cmd.Name, runMode)
		}
		if runMode == "persistent_shell" && shell == "" {
			return fmt.Errorf("project command %q run_mode persistent_shell requires shell", cmd.Name)
		}
		if strings.Contains(cmd.Cwd, "\x00") {
			return fmt.Errorf("project command %q cwd contains NUL", cmd.Name)
		}
		if filepath.IsAbs(strings.TrimSpace(cmd.Cwd)) {
			return fmt.Errorf("project command %q cwd must be relative", cmd.Name)
		}
		for _, rule := range cmd.ExtraArgs {
			switch rule.Kind {
			case "any", "enum", "regex", "path":
			default:
				return fmt.Errorf("project command %q extra_args rule has invalid kind %q", cmd.Name, rule.Kind)
			}
		}
	}
	return nil
}

func Starter(name string) string {
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(mustGetwd())
	}
	return fmt.Sprintf(`config_kind = "project"
config_version = 1

[project]
name = %q
description = "Project-specific personal MCP server guidance."

[guide]
summary = "Use project commands for test, lint, format, and CI. Prefer bounded reads and compact patches."
read_first = ["README.md", "docs/ARCHITECTURE.md", "pyproject.toml", "go.mod"]

[search]
exclude_globs = [".git/**", ".venv/**", "node_modules/**", "dist/**", "build/**", "__pycache__/**", ".pytest_cache/**"]

[protected_files]
deny_edit = ["dist/**", "build/**", "generated/**"]
prompt_edit = ["go.mod", "go.sum", "package.json", "pyproject.toml", ".github/workflows/**"]

[generated]
paths = ["dist/**", "build/**", "generated/**", "**/*.pb.go", "**/*_generated.py"]
default_action = "deny"

[workflows]
test = "test"
lint = "lint"
format = "format"
ci = "ci"

[[commands]]
name = "test"
exec = "just"
args = ["test"]
description = "Run the project test suite."
allow_extra_args = true
max_extra_args = 5

[[commands.extra_args]]
kind = "path"
allow_globs = ["tests/**", "src/**"]
must_exist = true
must_be_inside_project = true

[[commands.extra_args]]
kind = "enum"
values = ["-q", "-v", "-x"]

[[commands]]
name = "lint"
exec = "just"
args = ["lint"]
description = "Run project lint checks."

[[commands]]
name = "format"
exec = "just"
args = ["format"]
description = "Format the project."

[[commands]]
name = "ci"
exec = "just"
args = ["ci"]
description = "Run the full project CI workflow."
`, name)
}

func defaultTrustStorePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "trusted-projects.toml"
	}
	return filepath.Join(home, ".config", "personal-mcp-server", "trusted-projects.toml")
}

func expandAbs(p string) (string, error) {
	if strings.HasPrefix(p, "~/") || p == "~" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			p = h
		} else {
			p = filepath.Join(h, strings.TrimPrefix(p, "~/"))
		}
	}
	return filepath.Abs(p)
}

func canonical(p string) string {
	abs, err := filepath.Abs(p)
	if err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	if resolved, err := resolveExistingProjectParent(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

func resolveExistingProjectParent(p string) (string, error) {
	clean := filepath.Clean(p)
	parts := []string{}
	cur := clean
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(parts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, parts[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", os.ErrNotExist
		}
		parts = append(parts, filepath.Base(cur))
		cur = parent
	}
}

func (m *Manager) RefreshTrustStore() error {
	return m.refreshTrustStore(false)
}

func (m *Manager) refreshTrustStore(force bool) error {
	if !force {
		m.mu.RLock()
		recent := !m.lastTrustRefresh.IsZero() && time.Since(m.lastTrustRefresh) < trustStoreRefreshTTL
		lastMod := m.lastTrustModTime
		lastSize := m.lastTrustSize
		m.mu.RUnlock()
		if recent {
			modTime, size, statErr := m.trustStoreSignature()
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) && lastMod.IsZero() && lastSize == 0 {
					return nil
				}
			} else if modTime.Equal(lastMod) && size == lastSize {
				return nil
			}
		}
	}
	trusted, err := m.loadTrustStoreSnapshot()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	modTime, size, statErr := m.trustStoreSignature()
	if statErr != nil {
		modTime = time.Time{}
		size = 0
	}
	for _, root := range m.Global.ProjectConfigs.TrustedProjects {
		abs, expandErr := expandAbs(root)
		if expandErr == nil {
			canon := canonical(abs)
			trusted[canon] = TrustedProject{Root: canon, Config: filepath.Join(canon, m.Filename), Trusted: true, TrustedAt: "config"}
		}
	}
	m.mu.Lock()
	m.Trusted = trusted
	m.lastTrustRefresh = time.Now()
	m.lastTrustModTime = modTime
	m.lastTrustSize = size
	m.mu.Unlock()
	return nil
}

func (m *Manager) loadTrustStoreSnapshot() (map[string]TrustedProject, error) {
	trusted := map[string]TrustedProject{}
	b, err := os.ReadFile(m.TrustPath) //nolint:gosec // local user trust store path from config.
	if err != nil {
		return trusted, err
	}
	var store TrustStore
	if err := toml.Unmarshal(b, &store); err != nil {
		return trusted, err
	}
	for _, entry := range store.Projects {
		if entry.Trusted {
			canon := canonical(entry.Root)
			if strings.TrimSpace(entry.Root) == "" {
				entry.Root = canon
			}
			trusted[canon] = entry
		}
	}
	return trusted, nil
}

func (m *Manager) markTrustStoreFresh() {
	modTime, size, err := m.trustStoreSignature()
	if err != nil {
		modTime = time.Time{}
		size = 0
	}
	m.mu.Lock()
	m.lastTrustRefresh = time.Now()
	m.lastTrustModTime = modTime
	m.lastTrustSize = size
	m.mu.Unlock()
}

func (m *Manager) trustStoreSignature() (modTime time.Time, size int64, err error) {
	info, err := os.Stat(m.TrustPath)
	if err != nil {
		return time.Time{}, 0, err
	}
	return info.ModTime(), info.Size(), nil
}

func (m *Manager) saveTrustStore() error {
	m.mu.RLock()
	store := TrustStore{Projects: m.listTrustedLocked()}
	m.mu.RUnlock()
	b, err := toml.Marshal(store)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(m.TrustPath, b, 0o600)
}

func Marshal(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "project"
	}
	return wd
}
