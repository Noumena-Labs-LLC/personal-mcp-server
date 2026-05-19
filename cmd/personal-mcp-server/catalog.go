package main

import (
	"fmt"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

type toolCatalogEntry struct {
	Name            string `json:"name"`
	Purpose         string `json:"purpose"`
	Enabled         bool   `json:"enabled"`
	ReadOnly        bool   `json:"read_only"`
	Guide           string `json:"guide,omitempty"`
	SafetyNotes     string `json:"safety_notes,omitempty"`
	RequiresFeature string `json:"requires_feature,omitempty"`
}

type toolCatalogCategory struct {
	Name        string             `json:"name"`
	Title       string             `json:"title"`
	Purpose     string             `json:"purpose"`
	Recommended bool               `json:"recommended_first,omitempty"`
	Tools       []toolCatalogEntry `json:"tools"`
}

func toolCatalogAll(cfg *config.Config) map[string]any {
	categories := []toolCatalogCategory{
		{
			Name:        "orientation",
			Title:       "Orientation and guidance",
			Purpose:     "Start here to learn server capabilities, policy, guides, resources, and safe workflows.",
			Recommended: true,
			Tools: []toolCatalogEntry{
				{Name: "server_info", Purpose: "Return version, module, transport, enabled feature summary, and large-file guidance.", Enabled: true, ReadOnly: true, Guide: "guide/tools"},
				{Name: "tool_catalog", Purpose: "Compatibility alias for tool_catalog_all.", Enabled: true, ReadOnly: true, Guide: "guide/tools"},
				{Name: "tool_catalog_categories", Purpose: "Return compact catalog category summaries and enabled/disabled counts.", Enabled: true, ReadOnly: true, Guide: "guide/tools"},
				{Name: "tool_catalog_category", Purpose: "Return tools for one catalog category with optional disabled/query filtering.", Enabled: true, ReadOnly: true, Guide: "guide/tools"},
				{Name: "tool_catalog_all", Purpose: "Return the complete hierarchical tool catalog.", Enabled: true, ReadOnly: true, Guide: "guide/tools"},
				{Name: "policy_describe", Purpose: "Describe roots, enabled tools, file policy, command policy, and approval behavior without secrets.", Enabled: true, ReadOnly: true, Guide: "docs/security"},
				{Name: "guide_list", Purpose: "List embedded LLM-readable guides and release docs available through guide_read.", Enabled: true, ReadOnly: true, Guide: "guide/index"},
				{Name: "guide_read", Purpose: "Read one embedded guide, optionally by Markdown section.", Enabled: true, ReadOnly: true, Guide: "guide/index"},
				{Name: "resource_list", Purpose: "List personal MCP resource URIs and templates for tool-only clients.", Enabled: true, ReadOnly: true, Guide: "guide/index"},
				{Name: "resource_read", Purpose: "Read one personal-mcp:// resource by URI for tool-only clients.", Enabled: true, ReadOnly: true, Guide: "guide/index"},
				{Name: "project_config_describe", Purpose: "Explain .personal-mcp-server.toml project config shape and trust behavior.", Enabled: true, ReadOnly: true, Guide: "project-config"},
				{Name: "setup_guide", Purpose: "Return setup, service, logs, and troubleshooting guidance by topic.", Enabled: true, ReadOnly: true, Guide: "setup"},
				{Name: "diagnostics_recent_slow_tools", Purpose: "Return recent slow/very slow MCP tool diagnostic records from the configured server log.", Enabled: cfg.Tools.DiagnosticsRecentSlowTools.Enabled, ReadOnly: true, Guide: "guide/logs"},
				{Name: "config_validate", Purpose: "Validate a personal MCP server config file and return errors, warnings, and canonical suggestions.", Enabled: cfg.Tools.ConfigValidate.Enabled, ReadOnly: true, Guide: "guide/tools"},
				{Name: "config_explain", Purpose: "Explain effective config defaults, limits, policies, and logging settings without secrets.", Enabled: cfg.Tools.ConfigExplain.Enabled, ReadOnly: true, Guide: "guide/tools"},
			},
		},
		{
			Name:        "project_workflow",
			Title:       "Project workflow discovery and named commands",
			Purpose:     "Use cwd-aware project guidance before running commands or assuming repo workflow.",
			Recommended: true,
			Tools: []toolCatalogEntry{
				{Name: "project_info", Purpose: "Discover nearest project config and trust status for cwd.", Enabled: true, ReadOnly: true, Guide: "project-config"},
				{Name: "workflow_list", Purpose: "List project workflow aliases such as test, lint, format, build, and ci.", Enabled: true, ReadOnly: true, Guide: "project-config"},
				{Name: "cmd_list_named", Purpose: "List configured named commands and optional extra-args metadata.", Enabled: true, ReadOnly: true, Guide: "tools"},
				{Name: "cmd_run_named", Purpose: "Run one trusted named command from global or project config; command cwd may be configured when the tool call omits cwd.", Enabled: cfg.Tools.RunCommand.Enabled, ReadOnly: false, Guide: "tools", SafetyNotes: "Prefer after cmd_list_named/workflow_list; no raw shell strings."},
				{Name: "cmd_run_sequence", Purpose: "Run a configured sequence of named commands in order.", Enabled: cfg.Tools.RunSequence.Enabled, ReadOnly: false, Guide: "tools", SafetyNotes: "Config-defined only; supports stop_on_failure or continue; no shell chaining syntax."},
				{Name: "cmd_start_named", Purpose: "Start one named command as a server-supervised background job and return a job_id.", Enabled: cfg.Tools.RunCommand.Enabled, ReadOnly: false, Guide: "tools", SafetyNotes: "Server owns timeout, cancellation, output retention, and audit."},
				{Name: "cmd_job_status", Purpose: "Return status, timing, exit code, and timeout metadata for a background command job.", Enabled: cfg.Tools.RunCommand.Enabled, ReadOnly: true, Guide: "tools"},
				{Name: "cmd_job_read", Purpose: "Read bounded tail output for a background command job.", Enabled: cfg.Tools.RunCommand.Enabled, ReadOnly: true, Guide: "tools"},
				{Name: "cmd_job_cancel", Purpose: "Cancel a running background command job through server-owned cancellation.", Enabled: cfg.Tools.RunCommand.Enabled, ReadOnly: false, Guide: "tools"},
				{Name: "cmd_job_list", Purpose: "List running and recently finished background command jobs.", Enabled: cfg.Tools.RunCommand.Enabled, ReadOnly: true, Guide: "tools"},
			},
		},
		{
			Name:        "filesystem_read",
			Title:       "Filesystem read and navigation",
			Purpose:     "Find and read bounded content inside configured roots without exposing secrets or giant files.",
			Recommended: true,
			Tools: []toolCatalogEntry{
				{Name: "fs_list_roots", Purpose: "List configured roots.", Enabled: cfg.Tools.ListRoots.Enabled, ReadOnly: true},
				{Name: "fs_tree", Purpose: "Return a compact bounded directory tree.", Enabled: cfg.Tools.Tree.Enabled, ReadOnly: true, RequiresFeature: "native_find"},
				{Name: "fs_list_dir", Purpose: "List entries in one directory.", Enabled: cfg.Tools.ListDir.Enabled, ReadOnly: true},
				{Name: "fs_find", Purpose: "Find files/directories using Go-native glob filtering.", Enabled: cfg.Tools.Find.Enabled, ReadOnly: true, RequiresFeature: "native_find"},
				{Name: "fs_search_text", Purpose: "Search text files with bounded results and pagination.", Enabled: cfg.Tools.SearchText.Enabled, ReadOnly: true},
				{Name: "fs_get_file_info", Purpose: "Get safe file metadata before reading.", Enabled: cfg.Tools.GetFileInfo.Enabled, ReadOnly: true},
				{Name: "fs_tail_file", Purpose: "Read the end of logs or large text files efficiently.", Enabled: cfg.Tools.TailFile.Enabled, ReadOnly: true, SafetyNotes: "Prefer for logs and recent diagnostics."},
				{Name: "fs_read_file", Purpose: "Read bounded line ranges from text files.", Enabled: cfg.Tools.ReadFile.Enabled, ReadOnly: true, SafetyNotes: "Prefer line ranges; whole_file must be explicit."},
				{Name: "md_outline", Purpose: "Return Markdown section outline and stable ids.", Enabled: cfg.Tools.MarkdownOutline.Enabled, ReadOnly: true},
				{Name: "md_read_section", Purpose: "Read one Markdown section by id or title.", Enabled: cfg.Tools.MarkdownReadSection.Enabled, ReadOnly: true},
			},
		},
		{
			Name:    "structured_data",
			Title:   "Structured JSON and JSONL navigation",
			Purpose: "Navigate JSON and JSONL files by structure, keys, slices, searches, records, and filters without dumping whole files.",
			Tools: []toolCatalogEntry{
				{Name: "json_outline", Purpose: "Return a compact structural outline for any valid JSON root value.", Enabled: cfg.Tools.JSONOutline.Enabled, ReadOnly: true, SafetyNotes: "Read-only; use max_depth/max_children to stay compact."},
				{Name: "json_keys", Purpose: "List object keys or array index windows at one JSON Pointer.", Enabled: cfg.Tools.JSONKeys.Enabled, ReadOnly: true},
				{Name: "json_get", Purpose: "Return one targeted JSON value by RFC 6901 JSON Pointer.", Enabled: cfg.Tools.JSONGet.Enabled, ReadOnly: true},
				{Name: "json_slice", Purpose: "Return a bounded page of array items at one JSON Pointer.", Enabled: cfg.Tools.JSONSlice.Enabled, ReadOnly: true},
				{Name: "json_search", Purpose: "Search JSON keys and scalar values and return matching pointers with previews.", Enabled: cfg.Tools.JSONSearch.Enabled, ReadOnly: true},
				{Name: "json_validate", Purpose: "Validate a JSON file and report root type without returning the whole document.", Enabled: cfg.Tools.JSONValidate.Enabled, ReadOnly: true},
				{Name: "jsonl_info", Purpose: "Sample JSONL records to discover top-level fields and observed types.", Enabled: cfg.Tools.JSONLInfo.Enabled, ReadOnly: true},
				{Name: "jsonl_read", Purpose: "Read a bounded page of valid JSONL records by logical record offset.", Enabled: cfg.Tools.JSONLRead.Enabled, ReadOnly: true},
				{Name: "jsonl_tail", Purpose: "Return the latest valid JSONL records with malformed/empty counts.", Enabled: cfg.Tools.JSONLTail.Enabled, ReadOnly: true},
				{Name: "jsonl_filter", Purpose: "Filter JSONL records by top-level fields, contains, exists/missing, and timestamp ranges.", Enabled: cfg.Tools.JSONLFilter.Enabled, ReadOnly: true},
				{Name: "jsonl_validate", Purpose: "Validate JSONL lines and count valid, empty, and malformed records.", Enabled: cfg.Tools.JSONLValidate.Enabled, ReadOnly: true},
			},
		},
		{
			Name:    "feedback",
			Title:   "Local feedback collection",
			Purpose: "Capture concise local feedback about tool gaps, confusing schemas, docs gaps, and workflow friction as JSONL on disk.",
			Tools: []toolCatalogEntry{
				{Name: "feedback_submit", Purpose: "Append one local feedback record to the configured JSONL feedback file.", Enabled: cfg.Tools.FeedbackSubmit.Enabled && cfg.Feedback.Enabled, ReadOnly: false, SafetyNotes: "Local-only; do not include secrets, credentials, large file contents, or private document excerpts."},
			},
		},
		{
			Name:    "filesystem_write",
			Title:   "Filesystem edits",
			Purpose: "Apply scoped writes inside configured roots; use compact diffs when useful.",
			Tools: []toolCatalogEntry{
				{Name: "file_explain_policy", Purpose: "Explain whether a file operation would be allowed, denied, or approval-gated.", Enabled: true, ReadOnly: true},
				{Name: "fs_apply_patch", Purpose: "Apply exact old/new text replacements to one file.", Enabled: cfg.Tools.ApplyPatch.Enabled, ReadOnly: false, SafetyNotes: "Supports dry_run for optional preview."},
				{Name: "fs_apply_unified_patch", Purpose: "Apply a standard unified diff patch.", Enabled: cfg.Tools.ApplyUnifiedPatch.Enabled, ReadOnly: false, RequiresFeature: "unified_patch", SafetyNotes: "Deletes/renames/binary patches are rejected."},
				{Name: "fs_replace_regex", Purpose: "Perform Go-native regex replacement in one file.", Enabled: cfg.Tools.ReplaceRegex.Enabled, ReadOnly: false, RequiresFeature: "regex_replace", SafetyNotes: "Supports optional dry_run and replacement caps."},
				{Name: "fs_create_file", Purpose: "Create a new text file.", Enabled: cfg.Tools.CreateFile.Enabled, ReadOnly: false},
				{Name: "fs_create_dir", Purpose: "Create a directory path with mkdir -p semantics.", Enabled: cfg.Tools.CreateDir.Enabled, ReadOnly: false, SafetyNotes: "Creates directories only; refuses file conflicts; supports dry_run."},
				{Name: "fs_replace_file", Purpose: "Replace one existing text file inside configured roots.", Enabled: cfg.Tools.ReplaceFile.Enabled, ReadOnly: false, SafetyNotes: "Refuses directories and binary files; optional create_backup."},
				{Name: "fs_delete_file", Purpose: "Delete one existing file inside configured roots.", Enabled: cfg.Tools.DeleteFile.Enabled, ReadOnly: false, SafetyNotes: "Files only; refuses directories."},
				{Name: "fs_delete_files", Purpose: "Delete multiple files inside configured roots.", Enabled: cfg.Tools.DeleteFiles.Enabled, ReadOnly: false, SafetyNotes: "Files only; refuses directories; enforces roots, file policy, and max_files."},
				{Name: "fs_move_file", Purpose: "Move or rename one existing file inside configured roots.", Enabled: cfg.Tools.MoveFile.Enabled, ReadOnly: false, SafetyNotes: "Files only; no directories; no overwrite unless requested."},
				{Name: "fs_append_file", Purpose: "Append text, optionally creating a file.", Enabled: cfg.Tools.AppendFile.Enabled, ReadOnly: false, SafetyNotes: "Prefer Markdown section tools for docs."},
				{Name: "md_replace_section", Purpose: "Replace one Markdown section body or full section.", Enabled: cfg.Tools.MarkdownReplaceSection.Enabled, ReadOnly: false, SafetyNotes: "Use md_outline first; dry_run is optional."},
				{Name: "md_replace_section_heading", Purpose: "Rename or relevel one Markdown section heading without replacing its body.", Enabled: cfg.Tools.MarkdownReplaceSectionHeading.Enabled, ReadOnly: false, SafetyNotes: "Use md_outline first; dry_run is optional."},
				{Name: "md_insert_section", Purpose: "Insert a new Markdown section before or after an existing section.", Enabled: cfg.Tools.MarkdownInsertSection.Enabled, ReadOnly: false, SafetyNotes: "Use md_outline first; dry_run is optional."},
				{Name: "md_append_section", Purpose: "Append a new Markdown section at document end.", Enabled: cfg.Tools.MarkdownAppendSection.Enabled, ReadOnly: false, SafetyNotes: "Prefer this over raw append for docs."},
				{Name: "md_append_subsection", Purpose: "Append a new child subsection under an existing Markdown section.", Enabled: cfg.Tools.MarkdownAppendSubsection.Enabled, ReadOnly: false, SafetyNotes: "Use md_outline first; dry_run is optional."},
			},
		},
		{
			Name:    "git_and_verification",
			Title:   "Git inspection and verification",
			Purpose: "Inspect working-tree state and use configured commands for verification.",
			Tools: []toolCatalogEntry{
				{Name: "git_status", Purpose: "Return structured git working-tree status.", Enabled: cfg.Tools.GitStatus.Enabled || cfg.Tools.GitDiff.Enabled, ReadOnly: true},
				{Name: "git_diff", Purpose: "Return a bounded git diff.", Enabled: cfg.Tools.GitDiff.Enabled, ReadOnly: true},
				{Name: "cmd_explain_policy", Purpose: "Explain whether an argv-style command would be allowed before running it.", Enabled: true, ReadOnly: true},
				{Name: "cmd_run_argv", Purpose: "Run a policy-covered argv-style command.", Enabled: cfg.Tools.RunArgv.Enabled, ReadOnly: false, SafetyNotes: "No shell strings, pipes, redirects, globs, or background jobs."},
			},
		},
	}
	return map[string]any{
		"note":       "MCP tools/list is flat; prefer tool_catalog_categories then tool_catalog_category for progressive reveal. tool_catalog_all returns the complete catalog.",
		"categories": categories,
	}
}

type toolCatalogCategoryArgs struct {
	Category        string `json:"category"`
	IncludeDisabled bool   `json:"include_disabled"`
	Query           string `json:"query"`
}

func toolCatalogCategories(cfg *config.Config) map[string]any {
	all := toolCatalogAll(cfg)
	categories, _ := all["categories"].([]toolCatalogCategory)
	summaries := make([]map[string]any, 0, len(categories))
	for _, category := range categories {
		enabledCount := 0
		for _, tool := range category.Tools {
			if tool.Enabled {
				enabledCount++
			}
		}
		summaries = append(summaries, map[string]any{
			"name": category.Name, "title": category.Title, "purpose": category.Purpose,
			"recommended_first": category.Recommended, "tool_count": len(category.Tools),
			"enabled_count": enabledCount, "disabled_count": len(category.Tools) - enabledCount,
		})
	}
	return map[string]any{"categories": summaries, "count": len(summaries)}
}

func buildToolCatalogCategory(cfg *config.Config, args toolCatalogCategoryArgs) (map[string]any, error) {
	name := strings.TrimSpace(args.Category)
	if name == "" {
		return nil, fmt.Errorf("category is required")
	}
	query := strings.ToLower(strings.TrimSpace(args.Query))
	all := toolCatalogAll(cfg)
	categories, _ := all["categories"].([]toolCatalogCategory)
	for _, category := range categories {
		if category.Name != name {
			continue
		}
		tools := make([]toolCatalogEntry, 0, len(category.Tools))
		for _, tool := range category.Tools {
			if !args.IncludeDisabled && !tool.Enabled {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(tool.Name+" "+tool.Purpose+" "+tool.SafetyNotes+" "+tool.RequiresFeature), query) {
				continue
			}
			tools = append(tools, tool)
		}
		return map[string]any{"category": category.Name, "title": category.Title, "purpose": category.Purpose, "tools": tools, "count": len(tools), "include_disabled": args.IncludeDisabled}, nil
	}
	return nil, fmt.Errorf("unknown category %q", name)
}
