package policy

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

const (
	ActionAllow  = "allow"
	ActionDeny   = "deny"
	ActionPrompt = "prompt"
)

type Decision struct {
	Action string `json:"action"`
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func ValidateAction(action string) error {
	switch action {
	case "", ActionAllow, ActionDeny, ActionPrompt:
		return nil
	default:
		return fmt.Errorf("invalid action %q", action)
	}
}

func DecideCommand(cp config.CommandPolicyConfig, execName string, args []string) (Decision, error) {
	execBase := filepath.Base(execName)
	joinedArgs := JoinArgs(args)
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}
	var allow, prompt *config.CommandPolicyRule
	for i := range cp.Rules {
		rule := &cp.Rules[i]
		matched, err := matchCommandRule(*rule, execName, execBase, joinedArgs, subcommand)
		if err != nil {
			return Decision{}, err
		}
		if !matched {
			continue
		}
		switch rule.Action {
		case ActionDeny:
			return Decision{Action: ActionDeny, Rule: rule.Name, Reason: "matched deny command policy"}, nil
		case ActionAllow:
			if allow == nil {
				allow = rule
			}
		case ActionPrompt:
			if prompt == nil {
				prompt = rule
			}
		}
	}
	if allow != nil {
		return Decision{Action: ActionAllow, Rule: allow.Name}, nil
	}
	if prompt != nil {
		return Decision{Action: ActionPrompt, Rule: prompt.Name, Reason: "matched prompt command policy"}, nil
	}
	def := cp.Default
	if def == "" {
		def = ActionDeny
	}
	return Decision{Action: def, Rule: "default", Reason: "command policy default"}, nil
}

func matchCommandRule(rule config.CommandPolicyRule, execName, execBase, joinedArgs, subcommand string) (bool, error) {
	if rule.Exec != "" && rule.Exec != execName && rule.Exec != execBase {
		return false, nil
	}
	if rule.ExecRegex != "" {
		re, err := regexp.Compile(rule.ExecRegex)
		if err != nil {
			return false, fmt.Errorf("command policy rule %q exec_regex: %w", rule.Name, err)
		}
		if !re.MatchString(execName) && !re.MatchString(execBase) {
			return false, nil
		}
	}
	if len(rule.Subcommands) > 0 {
		ok := false
		for _, s := range rule.Subcommands {
			if s == subcommand {
				ok = true
				break
			}
		}
		if !ok {
			return false, nil
		}
	}
	if rule.ArgsRegex != "" {
		re, err := regexp.Compile(rule.ArgsRegex)
		if err != nil {
			return false, fmt.Errorf("command policy rule %q args_regex: %w", rule.Name, err)
		}
		if !re.MatchString(joinedArgs) {
			return false, nil
		}
	}
	return true, nil
}

func DecideFile(fp config.FilePolicyConfig, operation, displayPath, resolvedPath string) (Decision, error) {
	var allow, prompt *config.FilePolicyRule
	for i := range fp.Rules {
		rule := &fp.Rules[i]
		if !operationIn(rule.Operations, operation) {
			continue
		}
		matched, err := matchPath(rule.Pattern, displayPath, resolvedPath)
		if err != nil {
			return Decision{}, fmt.Errorf("file policy rule %q: %w", rule.Name, err)
		}
		if !matched {
			continue
		}
		switch rule.Action {
		case ActionDeny:
			return Decision{Action: ActionDeny, Rule: rule.Name, Reason: "matched deny file policy"}, nil
		case ActionAllow:
			if allow == nil {
				allow = rule
			}
		case ActionPrompt:
			if prompt == nil {
				prompt = rule
			}
		}
	}
	if allow != nil {
		return Decision{Action: ActionAllow, Rule: allow.Name}, nil
	}
	if prompt != nil {
		return Decision{Action: ActionPrompt, Rule: prompt.Name, Reason: "matched prompt file policy"}, nil
	}
	return Decision{Action: defaultForOperation(fp, operation), Rule: "default", Reason: "file policy default"}, nil
}

func defaultForOperation(fp config.FilePolicyConfig, operation string) string {
	switch operation {
	case "read", "list", "info", "search":
		if fp.ReadDefault != "" {
			return fp.ReadDefault
		}
		return ActionAllow
	case "patch":
		if fp.PatchDefault != "" {
			return fp.PatchDefault
		}
	case "unified_patch":
		if fp.UnifiedPatchDefault != "" {
			return fp.UnifiedPatchDefault
		}
		if fp.PatchDefault != "" {
			return fp.PatchDefault
		}
	case "create":
		if fp.CreateDefault != "" {
			return fp.CreateDefault
		}
	case "write", "delete":
		if fp.WriteDefault != "" {
			return fp.WriteDefault
		}
	}
	return ActionDeny
}

func operationIn(ops []string, op string) bool {
	if len(ops) == 0 {
		return true
	}
	for _, candidate := range ops {
		if candidate == op {
			return true
		}
	}
	return false
}

func matchPath(pattern, displayPath, resolvedPath string) (bool, error) {
	if pattern == "" {
		return true, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(displayPath) || re.MatchString(resolvedPath), nil
}

func JoinArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\'' || r == '"' || r == '\\' }) < 0 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func Describe(c *config.Config, roots []string, serverVersion string) map[string]any {
	commands := map[string]any{
		"named_enabled": c.Tools.RunCommand.Enabled,
		"argv_enabled":  c.Tools.RunArgv.Enabled,
		"default":       firstNonEmpty(c.CommandPolicy.Default, ActionDeny),
		"rules":         commandRulesForDescription(c.CommandPolicy.Rules),
	}
	files := map[string]any{
		"read_default":          firstNonEmpty(c.FilePolicy.ReadDefault, ActionAllow),
		"write_default":         firstNonEmpty(c.FilePolicy.WriteDefault, ActionDeny),
		"create_default":        firstNonEmpty(c.FilePolicy.CreateDefault, ActionDeny),
		"patch_default":         firstNonEmpty(c.FilePolicy.PatchDefault, ActionDeny),
		"unified_patch_default": firstNonEmpty(c.FilePolicy.UnifiedPatchDefault, firstNonEmpty(c.FilePolicy.PatchDefault, ActionDeny)),
		"rules":                 c.FilePolicy.Rules,
	}
	tools := map[string]bool{
		"server_info":                   true,
		"fs_list_roots":                 c.Tools.ListRoots.Enabled,
		"fs_list_dir":                   c.Tools.ListDir.Enabled,
		"fs_get_file_info":              c.Tools.GetFileInfo.Enabled,
		"fs_tail_file":                  c.Tools.TailFile.Enabled,
		"fs_read_file":                  c.Tools.ReadFile.Enabled,
		"fs_search_text":                c.Tools.SearchText.Enabled,
		"fs_find":                       c.Tools.Find.Enabled,
		"fs_tree":                       c.Tools.Tree.Enabled,
		"fs_replace_regex":              c.Tools.ReplaceRegex.Enabled,
		"fs_apply_patch":                c.Tools.ApplyPatch.Enabled,
		"fs_apply_unified_patch":        c.Tools.ApplyUnifiedPatch.Enabled,
		"fs_create_file":                c.Tools.CreateFile.Enabled,
		"fs_create_dir":                 c.Tools.CreateDir.Enabled,
		"fs_replace_file":               c.Tools.ReplaceFile.Enabled,
		"fs_delete_file":                c.Tools.DeleteFile.Enabled,
		"fs_delete_files":               c.Tools.DeleteFiles.Enabled,
		"fs_move_file":                  c.Tools.MoveFile.Enabled,
		"fs_append_file":                c.Tools.AppendFile.Enabled,
		"md_outline":                    c.Tools.MarkdownOutline.Enabled,
		"md_read_section":               c.Tools.MarkdownReadSection.Enabled,
		"md_replace_section":            c.Tools.MarkdownReplaceSection.Enabled,
		"md_replace_section_heading":    c.Tools.MarkdownReplaceSectionHeading.Enabled,
		"md_insert_section":             c.Tools.MarkdownInsertSection.Enabled,
		"md_append_section":             c.Tools.MarkdownAppendSection.Enabled,
		"md_append_subsection":          c.Tools.MarkdownAppendSubsection.Enabled,
		"cmd_run_named":                 c.Tools.RunCommand.Enabled,
		"cmd_run_sequence":              c.Tools.RunSequence.Enabled,
		"cmd_start_named":               c.Tools.RunCommand.Enabled,
		"cmd_job_status":                c.Tools.RunCommand.Enabled,
		"cmd_job_read":                  c.Tools.RunCommand.Enabled,
		"cmd_job_cancel":                c.Tools.RunCommand.Enabled,
		"cmd_job_list":                  c.Tools.RunCommand.Enabled,
		"cmd_run_argv":                  c.Tools.RunArgv.Enabled,
		"git_diff":                      c.Tools.GitDiff.Enabled,
		"git_status":                    c.Tools.GitStatus.Enabled || c.Tools.GitDiff.Enabled,
		"tool_catalog":                  true,
		"tool_catalog_categories":       true,
		"tool_catalog_category":         true,
		"tool_catalog_batch":            true,
		"tool_catalog_all":              true,
		"policy_describe":               true,
		"project_info":                  true,
		"workflow_list":                 true,
		"project_config_describe":       true,
		"setup_guide":                   true,
		"guide_list":                    true,
		"guide_read":                    true,
		"cmd_explain_policy":            true,
		"file_explain_policy":           true,
		"resource_list":                 true,
		"resource_read":                 true,
		"json_outline":                  c.Tools.JSONOutline.Enabled,
		"json_keys":                     c.Tools.JSONKeys.Enabled,
		"json_get":                      c.Tools.JSONGet.Enabled,
		"json_slice":                    c.Tools.JSONSlice.Enabled,
		"json_search":                   c.Tools.JSONSearch.Enabled,
		"json_validate":                 c.Tools.JSONValidate.Enabled,
		"jsonl_info":                    c.Tools.JSONLInfo.Enabled,
		"jsonl_read":                    c.Tools.JSONLRead.Enabled,
		"jsonl_tail":                    c.Tools.JSONLTail.Enabled,
		"jsonl_filter":                  c.Tools.JSONLFilter.Enabled,
		"jsonl_validate":                c.Tools.JSONLValidate.Enabled,
		"feedback_submit":               c.Tools.FeedbackSubmit.Enabled && c.Feedback.Enabled,
		"diagnostics_recent_slow_tools": c.Tools.DiagnosticsRecentSlowTools.Enabled,
		"config_validate":               c.Tools.ConfigValidate.Enabled,
		"config_explain":                c.Tools.ConfigExplain.Enabled,
	}
	enabledCWDTools := []string{}
	disabledCWDTools := []map[string]any{}
	toolRequirements := map[string]string{
		"fs_tree":                "native_find",
		"fs_find":                "native_find",
		"fs_replace_regex":       "regex_replace",
		"fs_apply_unified_patch": "unified_patch",
	}
	for _, name := range []string{"fs_list_dir", "fs_get_file_info", "fs_tail_file", "fs_read_file", "fs_search_text", "fs_find", "fs_replace_regex", "fs_apply_patch", "fs_apply_unified_patch", "fs_create_file", "fs_create_dir", "fs_replace_file", "fs_delete_file", "fs_delete_files", "fs_move_file", "fs_append_file", "md_outline", "md_read_section", "md_replace_section", "md_replace_section_heading", "md_insert_section", "md_append_section", "md_append_subsection", "json_outline", "json_keys", "json_get", "json_slice", "json_search", "json_validate", "jsonl_info", "jsonl_read", "jsonl_tail", "jsonl_filter", "jsonl_validate", "diagnostics_recent_slow_tools", "config_validate", "config_explain", "git_diff", "resource_read", "cmd_run_named", "cmd_run_sequence", "cmd_start_named", "cmd_job_status", "cmd_job_read", "cmd_job_cancel", "cmd_job_list", "cmd_run_argv"} {
		if tools[name] {
			enabledCWDTools = append(enabledCWDTools, name)
			continue
		}
		entry := map[string]any{"name": name, "enabled": false}
		if requirement := toolRequirements[name]; requirement != "" {
			entry["requires_feature"] = requirement
		}
		disabledCWDTools = append(disabledCWDTools, entry)
	}

	return map[string]any{
		"server": map[string]any{
			"name":           "personal-mcp-server",
			"version":        serverVersion,
			"module":         "github.com/noumena-labs-llc/personal-mcp-server",
			"config_version": c.ConfigVersion,
		},
		"roots":          roots,
		"tools":          tools,
		"command_policy": commands,
		"file_policy":    files,
		"project_configs": map[string]any{
			"enabled":     c.ProjectConfigs.Enabled,
			"filename":    c.ProjectConfigs.Filename,
			"auto_load":   c.ProjectConfigs.AutoLoad,
			"trust_store": c.ProjectConfigs.TrustStore,
		},
		"cwd": map[string]any{
			"supported":      true,
			"scope":          "per-tool-call only; no process-wide or hidden session cwd",
			"tools":          enabledCWDTools,
			"enabled_tools":  enabledCWDTools,
			"disabled_tools": disabledCWDTools,
		},
		"defaults": map[string]any{
			"allow_everything": c.Defaults.AllowEverything,
		},
		"feedback": map[string]any{
			"enabled":           c.Feedback.Enabled,
			"path":              c.Feedback.Path,
			"max_summary_bytes": c.Feedback.MaxSummaryBytes,
			"max_details_bytes": c.Feedback.MaxDetailsBytes,
			"max_context_bytes": c.Feedback.MaxContextBytes,
		},
		"approval": map[string]any{
			"enabled":             c.Approval.Enabled,
			"timeout_seconds":     c.Approval.TimeoutSeconds,
			"default_on_timeout":  firstNonEmpty(c.Approval.DefaultOnTimeout, ActionDeny),
			"session_decisions":   c.Approval.RememberSessionDecisions,
			"approval_http_paths": []string{"GET /approvals", "POST /approvals/{id}/approve", "POST /approvals/{id}/deny"},
		},
	}
}

func DescribeJSON(c *config.Config, roots []string, serverVersion string) string {
	b, err := json.MarshalIndent(Describe(c, roots, serverVersion), "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

func commandRulesForDescription(rules []config.CommandPolicyRule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		m := map[string]any{"name": rule.Name, "action": rule.Action}
		if rule.Exec != "" {
			m["exec"] = rule.Exec
		}
		if rule.ExecRegex != "" {
			m["exec_regex"] = rule.ExecRegex
		}
		if len(rule.Subcommands) > 0 {
			subs := append([]string(nil), rule.Subcommands...)
			sort.Strings(subs)
			m["subcommands"] = subs
		}
		if rule.ArgsRegex != "" {
			m["args_regex"] = rule.ArgsRegex
		}
		out = append(out, m)
	}
	return out
}

func firstNonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
