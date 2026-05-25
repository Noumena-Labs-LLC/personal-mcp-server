package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	ConfigVersion      int                      `toml:"config_version"`
	Defaults           DefaultsConfig           `toml:"defaults"`
	Server             ServerConfig             `toml:"server"`
	ServerLogging      ServerLoggingConfig      `toml:"server_logging"`
	Roots              []string                 `toml:"roots"`
	Tools              ToolConfig               `toml:"tools"`
	Limits             LimitsConfig             `toml:"limits"`
	Secrets            SecretsConfig            `toml:"secrets"`
	Commands           []CommandSpec            `toml:"commands"`
	Prompts            map[string]PromptSpec    `toml:"prompts"`
	Audit              AuditConfig              `toml:"audit"`
	Approval           ApprovalConfig           `toml:"approval"`
	CommandPolicy      CommandPolicyConfig      `toml:"command_policy"`
	FilePolicy         FilePolicyConfig         `toml:"file_policy"`
	ProjectConfigs     ProjectConfigSettings    `toml:"project_configs"`
	CommandEnvironment CommandEnvironmentConfig `toml:"command_environment"`
	CommandSequences   []CommandSequenceSpec    `toml:"command_sequences"`
	Feedback           FeedbackConfig           `toml:"feedback"`
}

type DefaultsConfig struct {
	// AllowEverything makes absent safety defaults permissive for single-user local setups.
	// Explicit config entries still win.
	AllowEverything bool `toml:"allow_everything" json:"allow_everything"`
}

type ServerConfig struct {
	Host           string   `toml:"host"`
	Port           int      `toml:"port"`
	Endpoint       string   `toml:"endpoint"`
	AuthTokenEnv   string   `toml:"auth_token_env"`
	AuthTokenFile  string   `toml:"auth_token_file"`
	ValidateOrigin bool     `toml:"validate_origin"`
	AllowedOrigins []string `toml:"allowed_origins"`
}

type ServerLoggingConfig struct {
	Level          string `toml:"level" json:"level"`
	Path           string `toml:"path" json:"path,omitempty"`
	MaxBytes       int64  `toml:"max_bytes" json:"max_bytes"`
	MaxBackups     int    `toml:"max_backups" json:"max_backups"`
	ToolSlowMS     int64  `toml:"tool_slow_ms" json:"tool_slow_ms"`
	ToolVerySlowMS int64  `toml:"tool_very_slow_ms" json:"tool_very_slow_ms"`
}

type ToolConfig struct {
	ServerInfo                    ToolSpec `toml:"server_info"`
	ToolCatalog                   ToolSpec `toml:"tool_catalog"`
	ListRoots                     ToolSpec `toml:"fs_list_roots"`
	ListDir                       ToolSpec `toml:"fs_list_dir"`
	GetFileInfo                   ToolSpec `toml:"fs_get_file_info"`
	TailFile                      ToolSpec `toml:"fs_tail_file"`
	ReadFile                      ToolSpec `toml:"fs_read_file"`
	SearchText                    ToolSpec `toml:"fs_search_text"`
	Find                          ToolSpec `toml:"fs_find"`
	Tree                          ToolSpec `toml:"fs_tree"`
	ReplaceRegex                  ToolSpec `toml:"fs_replace_regex"`
	ApplyPatch                    ToolSpec `toml:"fs_apply_patch"`
	ApplyUnifiedPatch             ToolSpec `toml:"fs_apply_unified_patch"`
	RunCommand                    ToolSpec `toml:"cmd_run_named"`
	RunSequence                   ToolSpec `toml:"cmd_run_sequence"`
	RunArgv                       ToolSpec `toml:"cmd_run_argv"`
	CreateFile                    ToolSpec `toml:"fs_create_file"`
	CreateDir                     ToolSpec `toml:"fs_create_dir"`
	ReplaceFile                   ToolSpec `toml:"fs_replace_file"`
	DeleteFile                    ToolSpec `toml:"fs_delete_file"`
	DeleteFiles                   ToolSpec `toml:"fs_delete_files"`
	MoveFile                      ToolSpec `toml:"fs_move_file"`
	AppendFile                    ToolSpec `toml:"fs_append_file"`
	MarkdownOutline               ToolSpec `toml:"md_outline"`
	MarkdownReadSection           ToolSpec `toml:"md_read_section"`
	MarkdownReplaceSection        ToolSpec `toml:"md_replace_section"`
	MarkdownReplaceSectionHeading ToolSpec `toml:"md_replace_section_heading"`
	MarkdownInsertSection         ToolSpec `toml:"md_insert_section"`
	MarkdownAppendSection         ToolSpec `toml:"md_append_section"`
	MarkdownAppendSubsection      ToolSpec `toml:"md_append_subsection"`
	GitDiff                       ToolSpec `toml:"git_diff"`
	GitStatus                     ToolSpec `toml:"git_status"`
	PolicyDescribe                ToolSpec `toml:"policy_describe"`
	ProjectInfo                   ToolSpec `toml:"project_info"`
	WorkflowList                  ToolSpec `toml:"workflow_list"`
	ProjectConfigDescribe         ToolSpec `toml:"project_config_describe"`
	SetupGuide                    ToolSpec `toml:"setup_guide"`
	GuideList                     ToolSpec `toml:"guide_list"`
	GuideRead                     ToolSpec `toml:"guide_read"`
	CommandExplain                ToolSpec `toml:"cmd_explain_policy"`
	FileExplain                   ToolSpec `toml:"file_explain_policy"`
	ResourceList                  ToolSpec `toml:"resource_list"`
	ResourceRead                  ToolSpec `toml:"resource_read"`
	JSONOutline                   ToolSpec `toml:"json_outline"`
	JSONKeys                      ToolSpec `toml:"json_keys"`
	JSONGet                       ToolSpec `toml:"json_get"`
	JSONSlice                     ToolSpec `toml:"json_slice"`
	JSONSearch                    ToolSpec `toml:"json_search"`
	JSONValidate                  ToolSpec `toml:"json_validate"`
	JSONLInfo                     ToolSpec `toml:"jsonl_info"`
	JSONLRead                     ToolSpec `toml:"jsonl_read"`
	JSONLTail                     ToolSpec `toml:"jsonl_tail"`
	JSONLFilter                   ToolSpec `toml:"jsonl_filter"`
	JSONLValidate                 ToolSpec `toml:"jsonl_validate"`
	FeedbackSubmit                ToolSpec `toml:"feedback_submit"`
	DiagnosticsRecentSlowTools    ToolSpec `toml:"diagnostics_recent_slow_tools"`
	ConfigValidate                ToolSpec `toml:"config_validate"`
	ConfigExplain                 ToolSpec `toml:"config_explain"`
}

type ToolSpec struct {
	Enabled     bool   `toml:"enabled"`
	Description string `toml:"description"`
}

type LimitsConfig struct {
	MaxReadBytes          int64 `toml:"max_read_bytes"`
	MaxWriteBytes         int64 `toml:"max_write_bytes"`
	MaxSearchResults      int   `toml:"max_search_results"`
	MaxSearchFileBytes    int64 `toml:"max_search_file_bytes"`
	CommandTimeoutSeconds int   `toml:"command_timeout_seconds"`
	MaxCommandOutputBytes int64 `toml:"max_command_output_bytes"`
	DiffContextLines      int   `toml:"diff_context_lines"`
	MaxDiffBytes          int64 `toml:"max_diff_bytes"`
	MaxPatchBytes         int64 `toml:"max_patch_bytes"`
}

type AuditConfig struct {
	Path       string `toml:"path"`
	MaxBytes   int64  `toml:"max_bytes"`
	MaxBackups int    `toml:"max_backups"`
}

// FeedbackConfig controls local-only MCP feedback collection. The tool never accepts
// a caller-supplied output path; records are appended to this configured JSONL file.
type FeedbackConfig struct {
	Enabled         bool   `toml:"enabled" json:"enabled"`
	Path            string `toml:"path" json:"path,omitempty"`
	MaxSummaryBytes int    `toml:"max_summary_bytes" json:"max_summary_bytes"`
	MaxDetailsBytes int    `toml:"max_details_bytes" json:"max_details_bytes"`
	MaxContextBytes int    `toml:"max_context_bytes" json:"max_context_bytes"`
}

type ApprovalConfig struct {
	Enabled                  bool   `toml:"enabled"`
	TimeoutSeconds           int    `toml:"timeout_seconds"`
	DefaultOnTimeout         string `toml:"default_on_timeout"`
	RememberSessionDecisions bool   `toml:"remember_session_decisions"`
}

type CommandEnvironmentConfig struct {
	AllowPersistentShell                 bool     `toml:"allow_persistent_shell" json:"allow_persistent_shell"`
	AllowedShells                        []string `toml:"allowed_shells" json:"allowed_shells,omitempty"`
	PersistentShellPoolSize              int      `toml:"persistent_shell_pool_size" json:"persistent_shell_pool_size,omitempty"`
	PersistentShellAcquireTimeoutSeconds int      `toml:"persistent_shell_acquire_timeout_seconds" json:"persistent_shell_acquire_timeout_seconds,omitempty"`
	PersistentShellStartupTimeoutSeconds int      `toml:"persistent_shell_startup_timeout_seconds" json:"persistent_shell_startup_timeout_seconds,omitempty"`
	PersistentShellQuietPeriodMs         int      `toml:"persistent_shell_quiet_period_ms" json:"persistent_shell_quiet_period_ms,omitempty"`
	StartupFiles                         []string `toml:"startup_files" json:"startup_files,omitempty"`
}

type CommandPolicyConfig struct {
	Default string              `toml:"default"`
	Rules   []CommandPolicyRule `toml:"rules"`
}

type CommandPolicyRule struct {
	Name        string   `toml:"name" json:"name"`
	Action      string   `toml:"action" json:"action"`
	Exec        string   `toml:"exec" json:"exec,omitempty"`
	ExecRegex   string   `toml:"exec_regex" json:"exec_regex,omitempty"`
	ArgsRegex   string   `toml:"args_regex" json:"args_regex,omitempty"`
	Subcommands []string `toml:"subcommands" json:"subcommands,omitempty"`
}

type FilePolicyConfig struct {
	ReadDefault         string           `toml:"read_default"`
	WriteDefault        string           `toml:"write_default"`
	CreateDefault       string           `toml:"create_default"`
	PatchDefault        string           `toml:"patch_default"`
	UnifiedPatchDefault string           `toml:"unified_patch_default"`
	Rules               []FilePolicyRule `toml:"rules"`
}

type ProjectConfigSettings struct {
	Enabled         bool     `toml:"enabled" json:"enabled"`
	Filename        string   `toml:"filename" json:"filename"`
	AutoLoad        bool     `toml:"auto_load" json:"auto_load"`
	TrustStore      string   `toml:"trust_store" json:"trust_store"`
	TrustedProjects []string `toml:"trusted_projects" json:"trusted_projects,omitempty"`
}

type FilePolicyRule struct {
	Name       string   `toml:"name" json:"name"`
	Action     string   `toml:"action" json:"action"`
	Operations []string `toml:"operations" json:"operations"`
	Pattern    string   `toml:"pattern" json:"pattern"`
}

type SecretsConfig struct {
	DenyNames      []string `toml:"deny_names"`
	DenyExtensions []string `toml:"deny_extensions"`
}

type CommandSpec struct {
	Name           string            `toml:"name"`
	Exec           string            `toml:"exec"`
	Args           []string          `toml:"args"`
	Description    string            `toml:"description"`
	Env            map[string]string `toml:"env"`
	EnvFromHost    []string          `toml:"env_from_host"`
	AllowExtraArgs bool              `toml:"allow_extra_args"`
	MaxExtraArgs   int               `toml:"max_extra_args"`
	ExtraArgs      []ExtraArgRule    `toml:"extra_args"`
	RunMode        string            `toml:"run_mode" json:"run_mode,omitempty"`
	Shell          string            `toml:"shell" json:"shell,omitempty"`
	StartupFiles   []string          `toml:"startup_files" json:"startup_files,omitempty"`
	Cwd            string            `toml:"cwd" json:"cwd,omitempty"`
}

type CommandSequenceSpec struct {
	Name        string                `toml:"name" json:"name"`
	Description string                `toml:"description" json:"description,omitempty"`
	Mode        string                `toml:"mode" json:"mode,omitempty"`
	Steps       []CommandSequenceStep `toml:"steps" json:"steps"`
}

type CommandSequenceStep struct {
	Name      string   `toml:"name" json:"name"`
	ExtraArgs []string `toml:"extra_args" json:"extra_args,omitempty"`
}

type ExtraArgRule struct {
	Kind                string   `toml:"kind" json:"kind"`
	Values              []string `toml:"values" json:"values,omitempty"`
	Pattern             string   `toml:"pattern" json:"pattern,omitempty"`
	AllowGlobs          []string `toml:"allow_globs" json:"allow_globs,omitempty"`
	MustExist           bool     `toml:"must_exist" json:"must_exist,omitempty"`
	MustBeInsideProject bool     `toml:"must_be_inside_project" json:"must_be_inside_project,omitempty"`
}

type PromptSpec struct {
	Enabled     bool   `toml:"enabled"`
	Description string `toml:"description"`
	Template    string `toml:"template"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path) //nolint:gosec // config path is supplied by the local user.
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := toml.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	raw = normalizeTOMLAliases(raw)
	normalized, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("normalize config aliases: %w", err)
	}
	var c Config
	if err := toml.Unmarshal(normalized, &c); err != nil {
		return nil, err
	}
	c.applyUpgradeDefaults(raw)
	c.normalizeLenientValues()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func normalizeTOMLAliases(raw map[string]any) map[string]any {
	return normalizeTOMLMap(raw)
}

func normalizeTOMLMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = normalizeTOMLValue(value)
	}
	for key, value := range in {
		alias := camelKeyToSnake(key)
		if alias == key {
			continue
		}
		if _, exists := out[alias]; exists {
			continue
		}
		out[alias] = normalizeTOMLValue(value)
	}
	return out
}

func normalizeTOMLValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return normalizeTOMLMap(v)
	case []map[string]any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeTOMLMap(item))
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeTOMLValue(item))
		}
		return out
	default:
		return value
	}
}

func camelKeyToSnake(key string) string {
	if key == "" {
		return key
	}
	var b strings.Builder
	lastWritten := rune(0)
	runes := []rune(key)
	for i, r := range runes {
		if r == '-' {
			r = '_'
		}
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				var next rune
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				if lastWritten != '_' && (isLowerOrDigit(prev) || (isUpper(prev) && isLower(next))) {
					b.WriteByte('_')
				}
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
		lastWritten = r
	}
	return b.String()
}

func isUpper(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isLower(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isLowerOrDigit(r rune) bool {
	return isLower(r) || (r >= '0' && r <= '9')
}

func (c *Config) applyUpgradeDefaults(raw map[string]any) {
	if c.Defaults.AllowEverything {
		c.applyAllowEverythingDefaults(raw)
	}
	for _, tool := range allToolNames() {
		if !nestedTableExists(raw, "tools", tool) {
			c.setToolEnabled(tool, true)
		}
	}
	if !tableExists(raw, "feedback") {
		c.Feedback.Enabled = true
	}
}

func (c *Config) applyAllowEverythingDefaults(raw map[string]any) {
	if !nestedKeyExists(raw, "approval", "enabled") {
		c.Approval.Enabled = false
	}
	if !nestedKeyExists(raw, "command_policy", "default") {
		c.CommandPolicy.Default = "allow"
	}
	for _, key := range []string{"read_default", "write_default", "create_default", "patch_default", "unified_patch_default"} {
		if !nestedKeyExists(raw, "file_policy", key) {
			c.setFilePolicyDefault(key, "allow")
		}
	}
}

func allToolNames() []string {
	return []string{
		"server_info",
		"tool_catalog",
		"fs_list_roots",
		"fs_list_dir",
		"fs_get_file_info",
		"fs_tail_file",
		"fs_read_file",
		"fs_search_text",
		"fs_find",
		"fs_tree",
		"fs_replace_regex",
		"fs_apply_patch",
		"fs_apply_unified_patch",
		"cmd_run_named",
		"cmd_run_sequence",
		"cmd_run_argv",
		"fs_create_file",
		"fs_create_dir",
		"fs_replace_file",
		"fs_delete_file",
		"fs_delete_files",
		"fs_move_file",
		"fs_append_file",
		"md_outline",
		"md_read_section",
		"md_replace_section",
		"md_replace_section_heading",
		"md_insert_section",
		"md_append_section",
		"md_append_subsection",
		"git_diff",
		"git_status",
		"policy_describe",
		"project_info",
		"workflow_list",
		"project_config_describe",
		"setup_guide",
		"guide_list",
		"guide_read",
		"cmd_explain_policy",
		"file_explain_policy",
		"resource_list",
		"resource_read",
		"json_outline",
		"json_keys",
		"json_get",
		"json_slice",
		"json_search",
		"json_validate",
		"jsonl_info",
		"jsonl_read",
		"jsonl_tail",
		"jsonl_filter",
		"jsonl_validate",
		"feedback_submit",
		"diagnostics_recent_slow_tools",
		"config_validate",
		"config_explain",
	}
}

func tableExists(raw map[string]any, name string) bool {
	_, ok := raw[name]
	return ok
}

func nestedTableExists(raw map[string]any, parent, child string) bool {
	return nestedKeyExists(raw, parent, child)
}

func nestedKeyExists(raw map[string]any, parent, child string) bool {
	v, ok := raw[parent]
	if !ok {
		return false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[child]
	return ok
}

func (c *Config) setFilePolicyDefault(name, action string) {
	switch name {
	case "read_default":
		c.FilePolicy.ReadDefault = action
	case "write_default":
		c.FilePolicy.WriteDefault = action
	case "create_default":
		c.FilePolicy.CreateDefault = action
	case "patch_default":
		c.FilePolicy.PatchDefault = action
	case "unified_patch_default":
		c.FilePolicy.UnifiedPatchDefault = action
	}
}

func (c *Config) setToolEnabled(name string, enabled bool) {
	switch name {
	case "server_info":
		c.Tools.ServerInfo.Enabled = enabled
	case "tool_catalog":
		c.Tools.ToolCatalog.Enabled = enabled
	case "fs_list_roots":
		c.Tools.ListRoots.Enabled = enabled
	case "fs_list_dir":
		c.Tools.ListDir.Enabled = enabled
	case "fs_get_file_info":
		c.Tools.GetFileInfo.Enabled = enabled
	case "fs_tail_file":
		c.Tools.TailFile.Enabled = enabled
	case "fs_read_file":
		c.Tools.ReadFile.Enabled = enabled
	case "fs_search_text":
		c.Tools.SearchText.Enabled = enabled
	case "fs_find":
		c.Tools.Find.Enabled = enabled
	case "fs_tree":
		c.Tools.Tree.Enabled = enabled
	case "fs_replace_regex":
		c.Tools.ReplaceRegex.Enabled = enabled
	case "fs_apply_patch":
		c.Tools.ApplyPatch.Enabled = enabled
	case "fs_apply_unified_patch":
		c.Tools.ApplyUnifiedPatch.Enabled = enabled
	case "cmd_run_named":
		c.Tools.RunCommand.Enabled = enabled
	case "cmd_run_sequence":
		c.Tools.RunSequence.Enabled = enabled
	case "cmd_run_argv":
		c.Tools.RunArgv.Enabled = enabled
	case "fs_create_file":
		c.Tools.CreateFile.Enabled = enabled
	case "fs_create_dir":
		c.Tools.CreateDir.Enabled = enabled
	case "fs_replace_file":
		c.Tools.ReplaceFile.Enabled = enabled
	case "fs_delete_file":
		c.Tools.DeleteFile.Enabled = enabled
	case "fs_delete_files":
		c.Tools.DeleteFiles.Enabled = enabled
	case "fs_move_file":
		c.Tools.MoveFile.Enabled = enabled
	case "fs_append_file":
		c.Tools.AppendFile.Enabled = enabled
	case "md_outline":
		c.Tools.MarkdownOutline.Enabled = enabled
	case "md_read_section":
		c.Tools.MarkdownReadSection.Enabled = enabled
	case "md_replace_section":
		c.Tools.MarkdownReplaceSection.Enabled = enabled
	case "md_replace_section_heading":
		c.Tools.MarkdownReplaceSectionHeading.Enabled = enabled
	case "md_insert_section":
		c.Tools.MarkdownInsertSection.Enabled = enabled
	case "md_append_section":
		c.Tools.MarkdownAppendSection.Enabled = enabled
	case "md_append_subsection":
		c.Tools.MarkdownAppendSubsection.Enabled = enabled
	case "git_diff":
		c.Tools.GitDiff.Enabled = enabled
	case "git_status":
		c.Tools.GitStatus.Enabled = enabled
	case "policy_describe":
		c.Tools.PolicyDescribe.Enabled = enabled
	case "project_info":
		c.Tools.ProjectInfo.Enabled = enabled
	case "workflow_list":
		c.Tools.WorkflowList.Enabled = enabled
	case "project_config_describe":
		c.Tools.ProjectConfigDescribe.Enabled = enabled
	case "setup_guide":
		c.Tools.SetupGuide.Enabled = enabled
	case "guide_list":
		c.Tools.GuideList.Enabled = enabled
	case "guide_read":
		c.Tools.GuideRead.Enabled = enabled
	case "cmd_explain_policy":
		c.Tools.CommandExplain.Enabled = enabled
	case "file_explain_policy":
		c.Tools.FileExplain.Enabled = enabled
	case "resource_list":
		c.Tools.ResourceList.Enabled = enabled
	case "resource_read":
		c.Tools.ResourceRead.Enabled = enabled
	case "json_outline":
		c.Tools.JSONOutline.Enabled = enabled
	case "json_keys":
		c.Tools.JSONKeys.Enabled = enabled
	case "json_get":
		c.Tools.JSONGet.Enabled = enabled
	case "json_slice":
		c.Tools.JSONSlice.Enabled = enabled
	case "json_search":
		c.Tools.JSONSearch.Enabled = enabled
	case "json_validate":
		c.Tools.JSONValidate.Enabled = enabled
	case "jsonl_info":
		c.Tools.JSONLInfo.Enabled = enabled
	case "jsonl_read":
		c.Tools.JSONLRead.Enabled = enabled
	case "jsonl_tail":
		c.Tools.JSONLTail.Enabled = enabled
	case "jsonl_filter":
		c.Tools.JSONLFilter.Enabled = enabled
	case "jsonl_validate":
		c.Tools.JSONLValidate.Enabled = enabled
	case "feedback_submit":
		c.Tools.FeedbackSubmit.Enabled = enabled
	}
}

func (c *Config) Validate() error {
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.Endpoint == "" {
		c.Server.Endpoint = "/mcp"
	}
	if !strings.HasPrefix(c.Server.Endpoint, "/") {
		return fmt.Errorf("server.endpoint must start with /: %q", c.Server.Endpoint)
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if !isLocalhost(c.Server.Host) {
		return fmt.Errorf("refusing non-localhost bind host %q", c.Server.Host)
	}
	if c.ConfigVersion == 0 {
		return errors.New("config_version is required")
	}
	if c.ConfigVersion != 1 {
		return fmt.Errorf("unsupported config_version %d", c.ConfigVersion)
	}
	if c.Server.AuthTokenEnv == "" && c.Server.AuthTokenFile == "" {
		return errors.New("server.auth_token_env or server.auth_token_file is required")
	}
	if c.Server.AuthTokenFile != "" {
		path, err := expandAbs(c.Server.AuthTokenFile)
		if err != nil {
			return fmt.Errorf("server.auth_token_file: %w", err)
		}
		c.Server.AuthTokenFile = path
	}
	if c.authToken() == "" {
		if c.Server.AuthTokenEnv != "" && c.Server.AuthTokenFile != "" {
			return fmt.Errorf("auth token not found in env var %s or token file %s", c.Server.AuthTokenEnv, c.Server.AuthTokenFile)
		}
		if c.Server.AuthTokenEnv != "" {
			return fmt.Errorf("auth token env var %s is not set", c.Server.AuthTokenEnv)
		}
		return fmt.Errorf("auth token file %s is empty or unreadable", c.Server.AuthTokenFile)
	}
	if c.ServerLogging.Level == "" {
		c.ServerLogging.Level = "info"
	}
	switch strings.ToLower(strings.TrimSpace(c.ServerLogging.Level)) {
	case "debug", "info", "warn", "error":
		c.ServerLogging.Level = strings.ToLower(strings.TrimSpace(c.ServerLogging.Level))
	default:
		return fmt.Errorf("server_logging.level must be one of debug, info, warn, or error, got %q", c.ServerLogging.Level)
	}
	if c.ServerLogging.Path != "" {
		path, err := expandAbs(c.ServerLogging.Path)
		if err != nil {
			return fmt.Errorf("server_logging.path: %w", err)
		}
		c.ServerLogging.Path = path
	}
	if c.ServerLogging.MaxBytes == 0 {
		c.ServerLogging.MaxBytes = 10 * 1024 * 1024
	}
	if c.ServerLogging.MaxBackups == 0 {
		c.ServerLogging.MaxBackups = 5
	}
	if c.ServerLogging.ToolSlowMS == 0 {
		c.ServerLogging.ToolSlowMS = 3000
	}
	if c.ServerLogging.ToolVerySlowMS == 0 {
		c.ServerLogging.ToolVerySlowMS = 10000
	}
	if c.ServerLogging.MaxBytes < 0 || c.ServerLogging.MaxBackups < 0 {
		return errors.New("server_logging rotation settings cannot be negative")
	}
	if c.ServerLogging.ToolSlowMS < 0 || c.ServerLogging.ToolVerySlowMS < 0 {
		return errors.New("server_logging tool latency thresholds cannot be negative")
	}
	if c.ServerLogging.ToolSlowMS > c.ServerLogging.ToolVerySlowMS {
		return errors.New("server_logging.tool_slow_ms cannot exceed tool_very_slow_ms")
	}
	if len(c.Roots) == 0 {
		return errors.New("at least one root is required")
	}
	for i, root := range c.Roots {
		abs, err := expandAbs(root)
		if err != nil {
			return fmt.Errorf("root %q: %w", root, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("root %q: %w", root, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("root %q is not a directory", root)
		}
		c.Roots[i] = abs
	}
	if c.Limits.MaxReadBytes <= 0 || c.Limits.MaxSearchResults <= 0 || c.Limits.MaxSearchFileBytes <= 0 || c.Limits.CommandTimeoutSeconds <= 0 || c.Limits.MaxCommandOutputBytes <= 0 {
		return errors.New("read/search/command limits must be positive")
	}
	if c.Limits.DiffContextLines == 0 {
		c.Limits.DiffContextLines = 3
	}
	if c.Limits.MaxDiffBytes == 0 {
		c.Limits.MaxDiffBytes = 200000
	}
	if c.Limits.MaxPatchBytes == 0 {
		c.Limits.MaxPatchBytes = c.Limits.MaxReadBytes * 4
	}
	if c.Limits.DiffContextLines < 0 || c.Limits.MaxDiffBytes < 0 || c.Limits.MaxPatchBytes < 0 {
		return errors.New("diff and patch limits cannot be negative")
	}
	if c.Audit.Path != "" {
		path, err := expandAbs(c.Audit.Path)
		if err != nil {
			return fmt.Errorf("audit.path: %w", err)
		}
		c.Audit.Path = path
	}
	if c.Audit.MaxBytes == 0 {
		c.Audit.MaxBytes = 10 * 1024 * 1024
	}
	if c.Audit.MaxBackups == 0 {
		c.Audit.MaxBackups = 5
	}
	if c.Audit.MaxBytes < 0 || c.Audit.MaxBackups < 0 {
		return errors.New("audit rotation settings cannot be negative")
	}

	if c.Feedback.Path != "" {
		path, err := expandAbs(c.Feedback.Path)
		if err != nil {
			return fmt.Errorf("feedback.path: %w", err)
		}
		c.Feedback.Path = path
	}
	if c.Feedback.Enabled && c.Feedback.Path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("feedback.path default: %w", err)
		}
		c.Feedback.Path = filepath.Join(home, ".personal-mcp-server", "feedback", "feedback.jsonl")
	}
	if c.Feedback.MaxSummaryBytes == 0 {
		c.Feedback.MaxSummaryBytes = 500
	}
	if c.Feedback.MaxDetailsBytes == 0 {
		c.Feedback.MaxDetailsBytes = 4000
	}
	if c.Feedback.MaxContextBytes == 0 {
		c.Feedback.MaxContextBytes = 12000
	}
	if c.Feedback.MaxSummaryBytes < 0 || c.Feedback.MaxDetailsBytes < 0 || c.Feedback.MaxContextBytes < 0 {
		return errors.New("feedback size limits cannot be negative")
	}

	if c.CommandEnvironment.PersistentShellPoolSize == 0 {
		c.CommandEnvironment.PersistentShellPoolSize = 2
	}
	if c.CommandEnvironment.PersistentShellAcquireTimeoutSeconds == 0 {
		c.CommandEnvironment.PersistentShellAcquireTimeoutSeconds = 6
	}
	if c.CommandEnvironment.PersistentShellStartupTimeoutSeconds == 0 {
		c.CommandEnvironment.PersistentShellStartupTimeoutSeconds = 30
	}
	if c.CommandEnvironment.PersistentShellQuietPeriodMs == 0 {
		c.CommandEnvironment.PersistentShellQuietPeriodMs = 1000
	}
	if c.CommandEnvironment.PersistentShellPoolSize < 0 || c.CommandEnvironment.PersistentShellAcquireTimeoutSeconds < 0 || c.CommandEnvironment.PersistentShellStartupTimeoutSeconds < 0 || c.CommandEnvironment.PersistentShellQuietPeriodMs < 0 {
		return errors.New("persistent shell pool settings cannot be negative")
	}
	if c.CommandEnvironment.PersistentShellPoolSize > 8 {
		return errors.New("command_environment.persistent_shell_pool_size cannot exceed 8")
	}

	if c.ProjectConfigs.Filename == "" {
		c.ProjectConfigs.Filename = ".personal-mcp-server.toml"
	}
	if strings.ContainsAny(c.ProjectConfigs.Filename, string(os.PathSeparator)+"\x00") || filepath.Base(c.ProjectConfigs.Filename) != c.ProjectConfigs.Filename {
		return fmt.Errorf("project_configs.filename must be a single file name, got %q", c.ProjectConfigs.Filename)
	}
	if c.ProjectConfigs.TrustStore != "" {
		path, err := expandAbs(c.ProjectConfigs.TrustStore)
		if err != nil {
			return fmt.Errorf("project_configs.trust_store: %w", err)
		}
		c.ProjectConfigs.TrustStore = path
	}
	seen := map[string]bool{}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		if cmd.Name == "" || cmd.Exec == "" {
			return errors.New("commands require name and exec")
		}
		if seen[cmd.Name] {
			return fmt.Errorf("duplicate command name %q", cmd.Name)
		}
		seen[cmd.Name] = true
		if strings.ContainsAny(cmd.Exec, ";&|`$><\n") {
			return fmt.Errorf("command %q exec contains shell syntax", cmd.Name)
		}
		for _, arg := range cmd.Args {
			if strings.Contains(arg, "\x00") {
				return fmt.Errorf("command %q arg contains NUL", cmd.Name)
			}
		}
		for k := range cmd.Env {
			if !isSafeEnvName(k) {
				return fmt.Errorf("command %q has invalid env var name %q", cmd.Name, k)
			}
		}
		for _, k := range cmd.EnvFromHost {
			if !isSafeEnvName(k) {
				return fmt.Errorf("command %q has invalid env_from_host name %q", cmd.Name, k)
			}
		}
		if err := validateCommandSpec(*cmd); err != nil {
			return err
		}
	}
	if c.Approval.TimeoutSeconds == 0 {
		c.Approval.TimeoutSeconds = 120
	}
	if c.Approval.TimeoutSeconds < 0 {
		return errors.New("approval.timeout_seconds cannot be negative")
	}
	if c.Approval.DefaultOnTimeout == "" {
		c.Approval.DefaultOnTimeout = "deny"
	}
	if err := validateAction("approval.default_on_timeout", c.Approval.DefaultOnTimeout); err != nil {
		return err
	}
	if c.CommandPolicy.Default == "" {
		c.CommandPolicy.Default = "deny"
	}
	if err := validateAction("command_policy.default", c.CommandPolicy.Default); err != nil {
		return err
	}
	for _, rule := range c.CommandPolicy.Rules {
		if strings.TrimSpace(rule.Name) == "" {
			return errors.New("command_policy rule name cannot be empty")
		}
		if err := validateAction("command_policy rule "+rule.Name, rule.Action); err != nil {
			return err
		}
		if rule.Exec == "" && rule.ExecRegex == "" {
			return fmt.Errorf("command_policy rule %q requires exec or exec_regex", rule.Name)
		}
		if rule.ExecRegex != "" {
			if _, err := regexp.Compile(rule.ExecRegex); err != nil {
				return fmt.Errorf("command_policy rule %q exec_regex: %w", rule.Name, err)
			}
		}
		if rule.ArgsRegex != "" {
			if _, err := regexp.Compile(rule.ArgsRegex); err != nil {
				return fmt.Errorf("command_policy rule %q args_regex: %w", rule.Name, err)
			}
		}
	}
	if c.FilePolicy.ReadDefault == "" {
		c.FilePolicy.ReadDefault = "allow"
	}
	if c.FilePolicy.WriteDefault == "" {
		c.FilePolicy.WriteDefault = "deny"
	}
	if c.FilePolicy.CreateDefault == "" {
		c.FilePolicy.CreateDefault = "deny"
	}
	if c.FilePolicy.PatchDefault == "" {
		c.FilePolicy.PatchDefault = "deny"
	}
	if c.FilePolicy.UnifiedPatchDefault == "" {
		c.FilePolicy.UnifiedPatchDefault = c.FilePolicy.PatchDefault
	}
	for field, action := range map[string]string{"file_policy.read_default": c.FilePolicy.ReadDefault, "file_policy.write_default": c.FilePolicy.WriteDefault, "file_policy.create_default": c.FilePolicy.CreateDefault, "file_policy.patch_default": c.FilePolicy.PatchDefault, "file_policy.unified_patch_default": c.FilePolicy.UnifiedPatchDefault} {
		if err := validateAction(field, action); err != nil {
			return err
		}
	}
	for _, rule := range c.FilePolicy.Rules {
		if strings.TrimSpace(rule.Name) == "" {
			return errors.New("file_policy rule name cannot be empty")
		}
		if err := validateAction("file_policy rule "+rule.Name, rule.Action); err != nil {
			return err
		}
		if rule.Pattern != "" {
			if _, err := regexp.Compile(rule.Pattern); err != nil {
				return fmt.Errorf("file_policy rule %q pattern: %w", rule.Name, err)
			}
		}
	}
	for name, prompt := range c.Prompts {
		if strings.TrimSpace(name) == "" {
			return errors.New("prompt name cannot be empty")
		}
		if prompt.Enabled && strings.TrimSpace(prompt.Template) == "" {
			return fmt.Errorf("prompt %q is enabled but has no template", name)
		}
	}
	return nil
}

func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) AuthToken() string {
	return c.authToken()
}

func (c *Config) authToken() string {
	if c.Server.AuthTokenEnv != "" {
		if v := strings.TrimSpace(os.Getenv(c.Server.AuthTokenEnv)); v != "" {
			return v
		}
	}
	if c.Server.AuthTokenFile != "" {
		b, err := os.ReadFile(c.Server.AuthTokenFile) //nolint:gosec // token file path comes from trusted local config.
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

func (c *Config) ToolDescription(name, fallback string) string {
	switch name {
	case "server_info":
		return firstNonEmpty(c.Tools.ServerInfo.Description, fallback)
	case "tool_catalog":
		return firstNonEmpty(c.Tools.ToolCatalog.Description, fallback)
	case "fs_list_roots":
		return firstNonEmpty(c.Tools.ListRoots.Description, fallback)
	case "fs_list_dir":
		return firstNonEmpty(c.Tools.ListDir.Description, fallback)
	case "fs_get_file_info":
		return firstNonEmpty(c.Tools.GetFileInfo.Description, fallback)
	case "fs_tail_file":
		return firstNonEmpty(c.Tools.TailFile.Description, fallback)
	case "fs_read_file":
		return firstNonEmpty(c.Tools.ReadFile.Description, fallback)
	case "fs_search_text":
		return firstNonEmpty(c.Tools.SearchText.Description, fallback)
	case "fs_find":
		return firstNonEmpty(c.Tools.Find.Description, fallback)
	case "fs_tree":
		return firstNonEmpty(c.Tools.Tree.Description, fallback)
	case "fs_replace_regex":
		return firstNonEmpty(c.Tools.ReplaceRegex.Description, fallback)
	case "fs_apply_patch":
		return firstNonEmpty(c.Tools.ApplyPatch.Description, fallback)
	case "fs_apply_unified_patch":
		return firstNonEmpty(c.Tools.ApplyUnifiedPatch.Description, fallback)
	case "cmd_list_named":
		return fallback
	case "cmd_run_named":
		return firstNonEmpty(c.Tools.RunCommand.Description, fallback)
	case "cmd_run_sequence":
		return firstNonEmpty(c.Tools.RunSequence.Description, fallback)
	case "cmd_run_argv":
		return firstNonEmpty(c.Tools.RunArgv.Description, fallback)
	case "policy_describe":
		return firstNonEmpty(c.Tools.PolicyDescribe.Description, fallback)
	case "project_info":
		return firstNonEmpty(c.Tools.ProjectInfo.Description, fallback)
	case "workflow_list":
		return firstNonEmpty(c.Tools.WorkflowList.Description, fallback)
	case "project_config_describe":
		return firstNonEmpty(c.Tools.ProjectConfigDescribe.Description, fallback)
	case "setup_guide":
		return firstNonEmpty(c.Tools.SetupGuide.Description, fallback)
	case "guide_list":
		return firstNonEmpty(c.Tools.GuideList.Description, fallback)
	case "guide_read":
		return firstNonEmpty(c.Tools.GuideRead.Description, fallback)
	case "cmd_explain_policy":
		return firstNonEmpty(c.Tools.CommandExplain.Description, fallback)
	case "file_explain_policy":
		return firstNonEmpty(c.Tools.FileExplain.Description, fallback)
	case "resource_list":
		return firstNonEmpty(c.Tools.ResourceList.Description, fallback)
	case "resource_read":
		return firstNonEmpty(c.Tools.ResourceRead.Description, fallback)
	case "fs_create_file":
		return firstNonEmpty(c.Tools.CreateFile.Description, fallback)
	case "fs_create_dir":
		return firstNonEmpty(c.Tools.CreateDir.Description, fallback)
	case "fs_replace_file":
		return firstNonEmpty(c.Tools.ReplaceFile.Description, fallback)
	case "fs_delete_file":
		return firstNonEmpty(c.Tools.DeleteFile.Description, fallback)
	case "fs_delete_files":
		return firstNonEmpty(c.Tools.DeleteFiles.Description, fallback)
	case "fs_move_file":
		return firstNonEmpty(c.Tools.MoveFile.Description, fallback)
	case "fs_append_file":
		return firstNonEmpty(c.Tools.AppendFile.Description, fallback)
	case "md_outline":
		return firstNonEmpty(c.Tools.MarkdownOutline.Description, fallback)
	case "md_read_section":
		return firstNonEmpty(c.Tools.MarkdownReadSection.Description, fallback)
	case "md_replace_section":
		return firstNonEmpty(c.Tools.MarkdownReplaceSection.Description, fallback)
	case "md_replace_section_heading":
		return firstNonEmpty(c.Tools.MarkdownReplaceSectionHeading.Description, fallback)
	case "md_insert_section":
		return firstNonEmpty(c.Tools.MarkdownInsertSection.Description, fallback)
	case "md_append_section":
		return firstNonEmpty(c.Tools.MarkdownAppendSection.Description, fallback)
	case "md_append_subsection":
		return firstNonEmpty(c.Tools.MarkdownAppendSubsection.Description, fallback)
	case "git_diff":
		return firstNonEmpty(c.Tools.GitDiff.Description, fallback)
	case "git_status":
		return firstNonEmpty(c.Tools.GitStatus.Description, fallback)
	case "json_outline":
		return firstNonEmpty(c.Tools.JSONOutline.Description, fallback)
	case "json_keys":
		return firstNonEmpty(c.Tools.JSONKeys.Description, fallback)
	case "json_get":
		return firstNonEmpty(c.Tools.JSONGet.Description, fallback)
	case "json_slice":
		return firstNonEmpty(c.Tools.JSONSlice.Description, fallback)
	case "json_search":
		return firstNonEmpty(c.Tools.JSONSearch.Description, fallback)
	case "json_validate":
		return firstNonEmpty(c.Tools.JSONValidate.Description, fallback)
	case "jsonl_info":
		return firstNonEmpty(c.Tools.JSONLInfo.Description, fallback)
	case "jsonl_read":
		return firstNonEmpty(c.Tools.JSONLRead.Description, fallback)
	case "jsonl_tail":
		return firstNonEmpty(c.Tools.JSONLTail.Description, fallback)
	case "jsonl_filter":
		return firstNonEmpty(c.Tools.JSONLFilter.Description, fallback)
	case "jsonl_validate":
		return firstNonEmpty(c.Tools.JSONLValidate.Description, fallback)
	case "feedback_submit":
		return firstNonEmpty(c.Tools.FeedbackSubmit.Description, fallback)
	case "diagnostics_recent_slow_tools":
		return firstNonEmpty(c.Tools.DiagnosticsRecentSlowTools.Description, fallback)
	case "config_validate":
		return firstNonEmpty(c.Tools.ConfigValidate.Description, fallback)
	case "config_explain":
		return firstNonEmpty(c.Tools.ConfigExplain.Description, fallback)
	default:
		return fallback
	}
}

func firstNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
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

func isLocalhost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isSafeEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' && i > 0) {
			continue
		}
		return false
	}
	return true
}

func (c *Config) normalizeLenientValues() {
	for i := range c.Commands {
		switch strings.TrimSpace(strings.ToLower(c.Commands[i].RunMode)) {
		case "exec", "direct", "direct_argv":
			c.Commands[i].RunMode = "argv"
		}
		for j := range c.Commands[i].ExtraArgs {
			switch strings.TrimSpace(strings.ToLower(c.Commands[i].ExtraArgs[j].Kind)) {
			case "literal", "literals", "value", "values":
				c.Commands[i].ExtraArgs[j].Kind = "enum"
			case "regexp":
				c.Commands[i].ExtraArgs[j].Kind = "regex"
			}
		}
	}
}

func validateCommandSpec(cmd CommandSpec) error {
	if cmd.MaxExtraArgs < 0 {
		return fmt.Errorf("command %q max_extra_args cannot be negative", cmd.Name)
	}
	if !cmd.AllowExtraArgs && len(cmd.ExtraArgs) > 0 {
		return fmt.Errorf("command %q has extra_args rules but allow_extra_args is false", cmd.Name)
	}
	if cmd.RunMode != "" && cmd.RunMode != "argv" && cmd.RunMode != "persistent_shell" {
		return fmt.Errorf("command %q has invalid run_mode %q", cmd.Name, cmd.RunMode)
	}
	if cmd.RunMode == "persistent_shell" && strings.TrimSpace(cmd.Shell) == "" {
		return fmt.Errorf("command %q run_mode persistent_shell requires shell", cmd.Name)
	}
	for _, file := range cmd.StartupFiles {
		if strings.TrimSpace(file) == "" {
			return fmt.Errorf("command %q startup_files cannot contain empty paths", cmd.Name)
		}
	}
	if cmd.RunMode == "persistent_shell" {
		shellBase := filepath.Base(strings.TrimSpace(cmd.Shell))
		if (shellBase == "bash" || shellBase == "zsh") && len(cmd.StartupFiles) == 0 {
			return fmt.Errorf("command %q run_mode persistent_shell with %s requires startup_files", cmd.Name, shellBase)
		}
	}
	if strings.Contains(cmd.Cwd, "\x00") {
		return fmt.Errorf("command %q cwd contains NUL", cmd.Name)
	}
	for _, rule := range cmd.ExtraArgs {
		switch rule.Kind {
		case "any", "enum", "regex", "path":
		default:
			return fmt.Errorf("command %q extra_args rule has invalid kind %q", cmd.Name, rule.Kind)
		}
		if rule.Pattern != "" {
			if _, err := regexp.Compile(rule.Pattern); err != nil {
				return fmt.Errorf("command %q extra_args regex: %w", cmd.Name, err)
			}
		}
	}
	return nil
}

func validateAction(name, action string) error {
	switch action {
	case "allow", "deny", "prompt":
		return nil
	default:
		return fmt.Errorf("%s has invalid action %q", name, action)
	}
}
