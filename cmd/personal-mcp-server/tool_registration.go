package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/mcphttp"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/shell"
)

func registerTools(s *mcphttp.Server, cfg *config.Config, ft *fsx.Tools, r *shell.Runner, projects *project.Manager) {
	noArgsSchema := map[string]any{"type": "object", "additionalProperties": false}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "server_info", Description: cfg.ToolDescription("server_info", "Return personal MCP server server version, module path, transport, enabled feature summary, and large-file usage guidance."), InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
			return serverInfo(cfg), nil
		}})
		s.Register(mcphttp.Tool{Name: "tool_catalog", Description: cfg.ToolDescription("tool_catalog", "Compatibility alias for tool_catalog_all. Prefer tool_catalog_categories then tool_catalog_category for progressive reveal in MCP clients."), InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
			start := time.Now()
			out := toolCatalogAll(cfg)
			slog.Debug("tool_catalog completed", "duration_ms", time.Since(start).Milliseconds())
			return out, nil
		}})
		s.Register(mcphttp.Tool{Name: "tool_catalog_all", Description: "Return the complete hierarchical catalog of personal MCP server tools. Prefer tool_catalog_categories and tool_catalog_category when a client may time out on large responses.", InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
			start := time.Now()
			out := toolCatalogAll(cfg)
			slog.Debug("tool_catalog_all completed", "duration_ms", time.Since(start).Milliseconds())
			return out, nil
		}})
		s.Register(mcphttp.Tool{Name: "tool_catalog_categories", Description: "Return compact tool catalog categories and enabled/disabled counts for progressive discovery.", InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
			start := time.Now()
			out := toolCatalogCategories(cfg)
			slog.Debug("tool_catalog_categories completed", "duration_ms", time.Since(start).Milliseconds())
			return out, nil
		}})
		s.Register(mcphttp.Tool{Name: "tool_catalog_category", Description: "Return tools in one catalog category, optionally including disabled tools or filtering by query.", InputSchema: map[string]any{
			"type": "object", "required": []string{"category"}, "additionalProperties": false,
			"properties": map[string]any{"category": map[string]any{"type": "string"}, "include_disabled": map[string]any{"type": "boolean"}, "query": map[string]any{"type": "string"}},
		}, Handler: func(raw json.RawMessage) (any, error) {
			var args toolCatalogCategoryArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, err
			}
			start := time.Now()
			out, err := buildToolCatalogCategory(cfg, args)
			slog.Debug("tool_catalog_category completed", "category", args.Category, "duration_ms", time.Since(start).Milliseconds(), "err", err)
			return out, err
		}})
	}
	pathSchema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema()},
		"additionalProperties": false,
	}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "policy_describe", Description: cfg.ToolDescription("policy_describe", "Describe effective personal MCP server policy: roots, enabled tools, file policy, command policy, and approval behavior. Does not expose secrets."), InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
			return policy.Describe(cfg, ft.Sandbox.Roots, version), nil
		}})
	}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "cmd_explain_policy", Description: cfg.ToolDescription("cmd_explain_policy", "Explain whether an argv-style command would be allowed, denied, or require approval by command_policy. Does not run the command."), InputSchema: map[string]any{
			"type": "object", "required": []string{"exec"}, "additionalProperties": false,
			"properties": map[string]any{"exec": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		}, Handler: commandExplainTool(cfg)})
	}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "file_explain_policy", Description: cfg.ToolDescription("file_explain_policy", "Explain whether a file operation would be allowed, denied, or require approval by file_policy. Does not perform the operation."), InputSchema: map[string]any{
			"type": "object", "required": []string{"operation", "path"}, "additionalProperties": false,
			"properties": map[string]any{"operation": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}},
		}, Handler: fileExplainTool(ft)})
	}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "workflow_list", Description: cfg.ToolDescription("workflow_list", "List project workflow aliases such as test, lint, format, build, ci, and typecheck for the project nearest cwd."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"cwd": map[string]any{"type": "string"}},
		}, Handler: func(raw json.RawMessage) (any, error) {
			var a struct {
				Cwd string `json:"cwd"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, err
				}
			}
			if projects == nil {
				return map[string]any{"enabled": false}, nil
			}
			return projects.WorkflowInfo(a.Cwd), nil
		}})
	}

	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "project_info", Description: cfg.ToolDescription("project_info", "Discover the nearest .personal-mcp-server.toml project config for a cwd and report trust status, project commands, workflows, and guidance."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"cwd": map[string]any{"type": "string"}, "include_commands": map[string]any{"type": "boolean"}},
		}, Handler: func(raw json.RawMessage) (any, error) {
			var a struct {
				Cwd             string `json:"cwd"`
				IncludeCommands bool   `json:"include_commands"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, err
				}
			}
			if projects == nil {
				return map[string]any{"enabled": false}, nil
			}
			return projects.EffectiveInfo(a.Cwd, a.IncludeCommands), nil
		}})
	}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "project_config_describe", Description: cfg.ToolDescription("project_config_describe", "Return LLM-readable guidance for creating and understanding .personal-mcp-server.toml project configs."), InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
			content, err := readGuideFile("project_config.md")
			if err != nil {
				return nil, err
			}
			return map[string]any{"filename": ".personal-mcp-server.toml", "mime_type": "text/markdown", "sections": fsx.ParseMarkdownSections(content), "content": content}, nil
		}})
	}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "setup_guide", Description: cfg.ToolDescription("setup_guide", "Return LLM-readable setup guidance for macOS, Linux, Claude Desktop, services, logs, and troubleshooting."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"topic": map[string]any{"type": "string", "enum": []string{"overview", "macos", "linux", "claude-desktop", "services", "logs", "troubleshooting"}}},
		}, Handler: func(raw json.RawMessage) (any, error) {
			var a struct {
				Topic string `json:"topic"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, err
				}
			}
			file := map[string]string{
				"": "setup.md", "overview": "setup.md", "macos": "setup_macos.md", "linux": "setup_linux.md",
				"claude-desktop": "claude_desktop.md", "services": "services.md", "logs": "logs.md", "troubleshooting": "troubleshooting.md",
			}[a.Topic]
			if file == "" {
				return nil, fmt.Errorf("unknown setup guide topic %q", a.Topic)
			}
			content, err := readGuideFile(file)
			if err != nil {
				return nil, err
			}
			topic := a.Topic
			if topic == "" {
				topic = "overview"
			}
			return map[string]any{"topic": topic, "mime_type": "text/markdown", "sections": fsx.ParseMarkdownSections(content), "content": content}, nil
		}})
	}
	// Guide tools are always registered because they are read-only, non-secret, and help tool-only MCP clients discover setup/project guidance.
	s.Register(mcphttp.Tool{Name: "guide_list", Description: cfg.ToolDescription("guide_list", "List LLM-readable setup, project-config, troubleshooting, and documentation guides available through guide_read."), InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
		return map[string]any{"guides": guideCatalog()}, nil
	}})
	s.Register(mcphttp.Tool{Name: "guide_read", Description: cfg.ToolDescription("guide_read", "Read one embedded LLM-readable guide by name, optionally limited to a Markdown section id or heading title."), InputSchema: map[string]any{
		"type": "object", "required": []string{"name"}, "additionalProperties": false,
		"properties": map[string]any{"name": map[string]any{"type": "string"}, "section": map[string]any{"type": "string"}},
	}, Handler: guideReadTool})
	if cfg.Tools.DiagnosticsRecentSlowTools.Enabled {
		s.Register(mcphttp.Tool{Name: "diagnostics_recent_slow_tools", Description: cfg.ToolDescription("diagnostics_recent_slow_tools", "Return recent slow and very slow MCP tool diagnostic records from the configured server log."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}, "since": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}},
		}, Handler: diagnosticsRecentSlowToolsTool(cfg)})
	}
	if cfg.Tools.ConfigValidate.Enabled {
		s.Register(mcphttp.Tool{Name: "config_validate", Description: cfg.ToolDescription("config_validate", "Validate a personal MCP server TOML config file and return errors, warnings, and canonical suggestions."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		}, Handler: configValidateTool()})
	}
	if cfg.Tools.ConfigExplain.Enabled {
		s.Register(mcphttp.Tool{Name: "config_explain", Description: cfg.ToolDescription("config_explain", "Explain effective personal MCP server config defaults, limits, policy settings, and logging without exposing secrets."), InputSchema: noArgsSchema, Handler: configExplainTool(cfg)})
	}

	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "resource_list", Description: cfg.ToolDescription("resource_list", "List personal MCP resource URIs and templates. Use this when your MCP client cannot list resources directly."), InputSchema: noArgsSchema, Handler: func(json.RawMessage) (any, error) {
			return map[string]any{"resources": resourceCatalog()}, nil
		}})
	}
	if discoveryToolsEnabled() {
		s.Register(mcphttp.Tool{Name: "resource_read", Description: cfg.ToolDescription("resource_read", "Read one personal-mcp:// resource by URI. This mirrors MCP resources for clients that only expose tools."), InputSchema: map[string]any{
			"type": "object", "required": []string{"uri"}, "additionalProperties": false,
			"properties": map[string]any{"uri": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}},
		}, Handler: resourceReadTool(ft, cfg)})
	}
	if cfg.Tools.ListRoots.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_list_roots", Description: cfg.ToolDescription("fs_list_roots", "List configured filesystem roots. Returns only roots explicitly configured for this server."), InputSchema: noArgsSchema, Handler: ft.ListRoots})
	}
	if cfg.Tools.ListDir.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_list_dir", Description: cfg.ToolDescription("fs_list_dir", "List entries under a directory inside configured roots. Bounded by max_entries and does not follow symlinks outside roots."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "recursive": map[string]any{"type": "boolean"}, "include_hidden": map[string]any{"type": "boolean"}, "max_entries": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}},
		}, Handler: ft.ListDir})
	}
	if cfg.Tools.GetFileInfo.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_get_file_info", Description: cfg.ToolDescription("fs_get_file_info", "Get safe metadata for a file or directory inside configured roots without reading contents, including size, text sniffing, line estimate, and large-file navigation hints."), InputSchema: pathSchema, Handler: ft.GetFileInfo})
	}
	if cfg.Tools.TailFile.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_tail_file", Description: cfg.ToolDescription("fs_tail_file", "Read the last lines of a text or log file inside configured roots without scanning the whole file. Use this for large logs and recent diagnostics."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "lines": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}},
		}, Handler: ft.TailFile})
	}
	if cfg.Tools.ReadFile.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_read_file", Description: cfg.ToolDescription("fs_read_file", "Read a bounded line range from a text file inside configured roots. Omitted max_lines defaults to 200; whole_file=true must be explicit for full-file reads. Secret-looking and binary files are refused."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "start_line": map[string]any{"type": "integer", "minimum": 1}, "max_lines": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}, "whole_file": map[string]any{"type": "boolean"}},
		}, Handler: ft.ReadFile})
	}
	if cfg.Tools.SearchText.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_search_text", Description: cfg.ToolDescription("fs_search_text", "Search text files inside configured roots. Plain substring search by default, regex optional, with result/file-size limits and offset pagination."), InputSchema: map[string]any{
			"type": "object", "required": []string{"query"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "query": map[string]any{"type": "string"}, "regex": map[string]any{"type": "boolean"}, "case_sensitive": map[string]any{"type": "boolean"}, "max_results": map[string]any{"type": "integer", "minimum": 1}, "offset": map[string]any{"type": "integer", "minimum": 0}, "include_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "exclude_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "context_before": map[string]any{"type": "integer", "minimum": 0}, "context_after": map[string]any{"type": "integer", "minimum": 0}, "max_matches_per_file": map[string]any{"type": "integer", "minimum": 1}},
		}, Handler: ft.SearchText})
	}
	if cfg.Tools.Find.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_find", Description: cfg.ToolDescription("fs_find", "Find files and directories inside configured roots using Go-native glob filtering with offset pagination. Use this instead of shell find."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "type": map[string]any{"type": "string", "enum": []string{"any", "file", "dir", "symlink"}},
				"name_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "path_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "exclude_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}, "offset": map[string]any{"type": "integer", "minimum": 0}, "max_depth": map[string]any{"type": "integer", "minimum": 1}, "min_size_bytes": map[string]any{"type": "integer", "minimum": 0}, "max_size_bytes": map[string]any{"type": "integer", "minimum": 0},
			},
		}, Handler: ft.Find})
	}
	if cfg.Tools.Tree.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_tree", Description: cfg.ToolDescription("fs_tree", "Return a compact bounded directory tree inside configured roots. Use this to orient within a repo before broad reads."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "max_depth": map[string]any{"type": "integer", "minimum": 1, "maximum": 10}, "max_entries": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000}, "include_hidden": map[string]any{"type": "boolean"}, "exclude_globs": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		}, Handler: ft.Tree})
	}

	if cfg.Tools.ReplaceRegex.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_replace_regex", Description: cfg.ToolDescription("fs_replace_regex", "Go-native sed-like regex replacement in one file. Supports line ranges, dry_run, replacement caps, and compact diffs. Does not call external sed."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "pattern", "replacement"}, "additionalProperties": false,
			"properties": map[string]any{
				"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "pattern": map[string]any{"type": "string"}, "replacement": map[string]any{"type": "string"},
				"start_line": map[string]any{"type": "integer", "minimum": 1}, "end_line": map[string]any{"type": "integer", "minimum": 1}, "max_replacements": map[string]any{"type": "integer", "minimum": 1, "default": 1}, "allow_unlimited": map[string]any{"type": "boolean"},
				"dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"},
			},
		}, Handler: ft.ReplaceRegex})
	}
	if cfg.Tools.ApplyPatch.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_apply_patch", Description: cfg.ToolDescription("fs_apply_patch", "Apply exact old/new text replacements to one file inside configured roots. Use either top-level old/new or edits=[{old,new,expected_replacements?}]. expected_replacements defaults to 1 and caps replacements; mismatched found counts return warnings, while zero matches fail. Supports optional dry_run and returns a compact diff."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{
				"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "old": map[string]any{"type": "string"}, "new": map[string]any{"type": "string"}, "expected_replacements": map[string]any{"type": "integer", "minimum": 1, "default": 1},
				"edits":   map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"old", "new"}, "additionalProperties": false, "properties": map[string]any{"old": map[string]any{"type": "string"}, "new": map[string]any{"type": "string"}, "expected_replacements": map[string]any{"type": "integer", "minimum": 1, "default": 1}}}},
				"dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"},
			},
		}, Handler: ft.ApplyPatch})
	}
	if cfg.Tools.ApplyUnifiedPatch.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_apply_unified_patch", Description: cfg.ToolDescription("fs_apply_unified_patch", "Apply a standard unified diff patch inside configured roots. Supports optional dry_run and rejects deletes, renames, binary patches, and paths outside roots."), InputSchema: map[string]any{
			"type": "object", "required": []string{"patch"}, "additionalProperties": false,
			"properties": map[string]any{"patch": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "dry_run": map[string]any{"type": "boolean"}, "strip": map[string]any{"type": "integer", "minimum": 0}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.ApplyUnifiedPatch})
	}
	if cfg.Tools.CreateFile.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_create_file", Description: cfg.ToolDescription("fs_create_file", "Create a new text file inside configured roots. Refuses to overwrite existing files."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "content"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "content": map[string]any{"type": "string"}, "fail_if_exists": map[string]any{"type": "boolean"}, "create_dirs": map[string]any{"type": "boolean"}},
		}, Handler: ft.CreateFile})
	}
	if cfg.Tools.CreateDir.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_create_dir", Description: cfg.ToolDescription("fs_create_dir", "Create a directory inside configured roots with mkdir -p semantics by default. Refuses file conflicts and supports optional dry_run."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "parents": map[string]any{"type": "boolean", "default": true}, "dry_run": map[string]any{"type": "boolean"}},
		}, Handler: ft.CreateDir})
	}
	if cfg.Tools.ReplaceFile.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_replace_file", Description: cfg.ToolDescription("fs_replace_file", "Replace one existing text file inside configured roots. Refuses directories and binary files, supports create_backup, and returns a compact diff."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "content"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "content": map[string]any{"type": "string"}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.ReplaceFile})
	}
	if cfg.Tools.DeleteFile.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_delete_file", Description: cfg.ToolDescription("fs_delete_file", "Delete one existing file inside configured roots. Refuses directories."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema()},
		}, Handler: ft.DeleteFile})
	}
	if cfg.Tools.DeleteFiles.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_delete_files", Description: cfg.ToolDescription("fs_delete_files", "Delete multiple files inside configured roots. Refuses directories and enforces max_files."), InputSchema: map[string]any{
			"type": "object", "required": []string{"paths"}, "additionalProperties": false,
			"properties": map[string]any{"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "allow_missing": map[string]any{"type": "boolean"}, "max_files": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}},
		}, Handler: ft.DeleteFiles})
	}
	if cfg.Tools.MoveFile.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_move_file", Description: cfg.ToolDescription("fs_move_file", "Move or rename one existing file inside configured roots. Refuses directories and refuses destination overwrites unless overwrite=true."), InputSchema: map[string]any{
			"type": "object", "required": []string{"source_path", "dest_path"}, "additionalProperties": false,
			"properties": map[string]any{"source_path": map[string]any{"type": "string"}, "dest_path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "overwrite": map[string]any{"type": "boolean"}},
		}, Handler: ft.MoveFile})
	}
	if cfg.Tools.AppendFile.Enabled {
		s.Register(mcphttp.Tool{Name: "fs_append_file", Description: cfg.ToolDescription("fs_append_file", "Append text to a file inside configured roots, optionally creating it. Respects file policy and supports optional dry_run, compact diffs, and atomic writes."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "content"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "content": map[string]any{"type": "string"}, "create_if_missing": map[string]any{"type": "boolean"}, "ensure_newline": map[string]any{"type": "boolean"}, "dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.AppendFile})
	}
	if cfg.Tools.MarkdownOutline.Enabled {
		s.Register(mcphttp.Tool{Name: "md_outline", Description: cfg.ToolDescription("md_outline", "Return a section outline for a Markdown file without reading the full document into the model. Ignores headings inside fenced code blocks."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "max_sections": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}},
		}, Handler: ft.MarkdownOutline})
	}
	if cfg.Tools.MarkdownReadSection.Enabled {
		s.Register(mcphttp.Tool{Name: "md_read_section", Description: cfg.ToolDescription("md_read_section", "Read one Markdown section by heading id or title, including nested subsections, instead of reading the whole file."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "section"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "section": map[string]any{"type": "string"}},
		}, Handler: ft.MarkdownReadSection})
	}
	if cfg.Tools.MarkdownReplaceSection.Enabled {
		s.Register(mcphttp.Tool{Name: "md_replace_section", Description: cfg.ToolDescription("md_replace_section", "Replace one Markdown section body by heading id or title. By default preserves the heading line; set include_heading=true to replace the whole section."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "section", "content"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "section": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "include_heading": map[string]any{"type": "boolean"}, "dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.MarkdownReplaceSection})
	}
	if cfg.Tools.MarkdownReplaceSectionHeading.Enabled {
		s.Register(mcphttp.Tool{Name: "md_replace_section_heading", Description: cfg.ToolDescription("md_replace_section_heading", "Rename or relevel one Markdown section heading by heading id or title without changing the section body."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "section", "title"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "section": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "level": map[string]any{"type": "integer", "minimum": 1, "maximum": 6}, "dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.MarkdownReplaceSectionHeading})
	}
	if cfg.Tools.MarkdownInsertSection.Enabled {
		s.Register(mcphttp.Tool{Name: "md_insert_section", Description: cfg.ToolDescription("md_insert_section", "Insert a new Markdown section before or after an existing section by heading id or title. Supports optional dry_run previews."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "title"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "after_section": map[string]any{"type": "string"}, "before_section": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "level": map[string]any{"type": "integer", "minimum": 1, "maximum": 6}, "content": map[string]any{"type": "string"}, "dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.MarkdownInsertSection})
	}
	if cfg.Tools.MarkdownAppendSection.Enabled {
		s.Register(mcphttp.Tool{Name: "md_append_section", Description: cfg.ToolDescription("md_append_section", "Append a new Markdown section at the end of a Markdown file. Prefer this over raw append for docs updates."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "title"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "title": map[string]any{"type": "string"}, "level": map[string]any{"type": "integer", "minimum": 1, "maximum": 6}, "content": map[string]any{"type": "string"}, "create_if_missing": map[string]any{"type": "boolean"}, "dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.MarkdownAppendSection})
	}
	if cfg.Tools.MarkdownAppendSubsection.Enabled {
		s.Register(mcphttp.Tool{Name: "md_append_subsection", Description: cfg.ToolDescription("md_append_subsection", "Append a new child subsection under an existing Markdown section. If level is omitted, it defaults to parent level + 1."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "parent_section", "title"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "parent_section": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "level": map[string]any{"type": "integer", "minimum": 1, "maximum": 6}, "content": map[string]any{"type": "string"}, "dry_run": map[string]any{"type": "boolean"}, "create_backup": map[string]any{"type": "boolean"}},
		}, Handler: ft.MarkdownAppendSubsection})
	}
	jsonPointerProps := map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "pointer": map[string]any{"type": "string", "description": "RFC 6901 JSON Pointer. Empty string selects the root."}}
	if cfg.Tools.JSONOutline.Enabled {
		s.Register(mcphttp.Tool{Name: "json_outline", Description: cfg.ToolDescription("json_outline", "Return a compact read-only structural outline for a JSON file. Supports object, array, string, number, boolean, and null roots; use pointer, max_depth, and max_children to navigate without dumping the file."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "pointer": map[string]any{"type": "string"}, "max_depth": map[string]any{"type": "integer", "minimum": 1, "maximum": 10}, "max_children": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}},
		}, Handler: ft.JSONOutline})
	}
	if cfg.Tools.JSONKeys.Enabled {
		s.Register(mcphttp.Tool{Name: "json_keys", Description: cfg.ToolDescription("json_keys", "List object keys or array index windows at one JSON Pointer without returning child values."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "pointer": map[string]any{"type": "string"}, "offset": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}},
		}, Handler: ft.JSONKeys})
	}
	if cfg.Tools.JSONGet.Enabled {
		s.Register(mcphttp.Tool{Name: "json_get", Description: cfg.ToolDescription("json_get", "Return one targeted JSON value by RFC 6901 JSON Pointer. Empty pointer returns the root value, regardless of root type."), InputSchema: map[string]any{"type": "object", "required": []string{"path"}, "additionalProperties": false, "properties": jsonPointerProps}, Handler: ft.JSONGet})
	}
	if cfg.Tools.JSONSlice.Enabled {
		s.Register(mcphttp.Tool{Name: "json_slice", Description: cfg.ToolDescription("json_slice", "Return a bounded page of array items at one JSON Pointer."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "pointer": map[string]any{"type": "string"}, "offset": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}},
		}, Handler: ft.JSONSlice})
	}
	if cfg.Tools.JSONSearch.Enabled {
		s.Register(mcphttp.Tool{Name: "json_search", Description: cfg.ToolDescription("json_search", "Search JSON keys and scalar values and return matching JSON Pointers with short previews."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path", "query"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "query": map[string]any{"type": "string"}, "case_sensitive": map[string]any{"type": "boolean"}, "search_keys": map[string]any{"type": "boolean"}, "search_values": map[string]any{"type": "boolean"}, "type_filter": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"object", "array", "string", "number", "boolean", "null"}}}, "pointer_prefix": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1}},
		}, Handler: ft.JSONSearch})
	}
	if cfg.Tools.JSONValidate.Enabled {
		s.Register(mcphttp.Tool{Name: "json_validate", Description: cfg.ToolDescription("json_validate", "Validate a JSON file and report root type without returning the document."), InputSchema: map[string]any{"type": "object", "required": []string{"path"}, "additionalProperties": false, "properties": jsonPointerProps}, Handler: ft.JSONValidate})
	}
	if cfg.Tools.JSONLInfo.Enabled {
		s.Register(mcphttp.Tool{Name: "jsonl_info", Description: cfg.ToolDescription("jsonl_info", "Sample a JSONL file to discover top-level fields and observed types without dumping records."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "sample": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}, "max_fields": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}},
		}, Handler: ft.JSONLInfo})
	}
	if cfg.Tools.JSONLRead.Enabled {
		s.Register(mcphttp.Tool{Name: "jsonl_read", Description: cfg.ToolDescription("jsonl_read", "Read a bounded page of valid JSONL records by logical record offset."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "offset": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}},
		}, Handler: ft.JSONLRead})
	}
	if cfg.Tools.JSONLTail.Enabled {
		s.Register(mcphttp.Tool{Name: "jsonl_tail", Description: cfg.ToolDescription("jsonl_tail", "Return the latest valid JSONL records with counts for empty and malformed lines."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "records": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}},
		}, Handler: ft.JSONLTail})
	}
	if cfg.Tools.JSONLFilter.Enabled {
		s.Register(mcphttp.Tool{Name: "jsonl_filter", Description: cfg.ToolDescription("jsonl_filter", "Filter JSONL records by top-level exact fields, string contains, exists/missing, and RFC3339 timestamp ranges."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "where": map[string]any{"type": "object"}, "contains": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "exists": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "missing": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "numeric_gte": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "number"}}, "numeric_lte": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "number"}}, "timestamp_field": map[string]any{"type": "string"}, "ts_gte": map[string]any{"type": "string"}, "ts_lte": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1}, "reverse": map[string]any{"type": "boolean"}},
		}, Handler: ft.JSONLFilter})
	}
	if cfg.Tools.JSONLValidate.Enabled {
		s.Register(mcphttp.Tool{Name: "jsonl_validate", Description: cfg.ToolDescription("jsonl_validate", "Validate JSONL lines and count valid, empty, and malformed records."), InputSchema: map[string]any{
			"type": "object", "required": []string{"path"}, "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "limit_errors": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}},
		}, Handler: ft.JSONLValidate})
	}
	if cfg.Tools.FeedbackSubmit.Enabled && cfg.Feedback.Enabled {
		s.Register(mcphttp.Tool{Name: "feedback_submit", Description: cfg.ToolDescription("feedback_submit", "Submit concise local feedback about tool gaps, confusing schemas, docs gaps, safety-limit friction, or feature requests. Stored locally as JSONL; do not include secrets, credentials, large file contents, or private document excerpts."), InputSchema: map[string]any{
			"type": "object", "required": []string{"summary"}, "additionalProperties": false,
			"properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"tool_gap", "tool_confusing", "tool_error", "docs_gap", "safety_limit", "workflow_friction", "feature_request", "other"}}, "summary": map[string]any{"type": "string"}, "details": map[string]any{"type": "string"}, "tool": map[string]any{"type": "string"}, "severity": map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}}, "context": map[string]any{"type": "object"}},
		}, Handler: ft.FeedbackSubmit})
	}
	if cfg.Tools.GitDiff.Enabled {
		s.Register(mcphttp.Tool{Name: "git_diff", Description: cfg.ToolDescription("git_diff", "Return a bounded git diff for a file or directory inside an allowed root. Does not use shell."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "staged": map[string]any{"type": "boolean"}, "max_bytes": map[string]any{"type": "integer", "minimum": 1}},
		}, Handler: ft.GitDiff})
	}
	if cfg.Tools.GitStatus.Enabled || cfg.Tools.GitDiff.Enabled {
		s.Register(mcphttp.Tool{Name: "git_status", Description: cfg.ToolDescription("git_status", "Return structured git working-tree status, including optional untracked files, for a repository inside an allowed root."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"path": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "path_mode": pathModeSchema(), "include_untracked": map[string]any{"type": "boolean"}, "max_entries": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000}},
		}, Handler: ft.GitStatus})
	}
	if cfg.Tools.RunArgv.Enabled {
		s.Register(mcphttp.Tool{Name: "cmd_run_argv", Description: cfg.ToolDescription("cmd_run_argv", "Run an argv-style command if command_policy allows it or after approval. No shell strings, pipes, redirects, glob expansion, or background jobs."), InputSchema: map[string]any{
			"type": "object", "required": []string{"exec"}, "additionalProperties": false,
			"properties": map[string]any{"exec": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "cwd": map[string]any{"type": "string"}},
		}, Handler: r.RunArgv})
	}
	if cfg.Tools.RunSequence.Enabled {
		s.Register(mcphttp.Tool{Name: "cmd_run_sequence", Description: cfg.ToolDescription("cmd_run_sequence", "Run one configured command sequence from global config or a trusted project .personal-mcp-server.toml. Steps reference existing named commands; mode is stop_on_failure or continue. No raw shell chaining syntax."), InputSchema: map[string]any{
			"type": "object", "required": []string{"name"}, "additionalProperties": false,
			"properties": map[string]any{"name": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}},
		}, Handler: r.RunSequence})
	}
	// Command discovery is read-only and is registered even when cmd_run_named is disabled so agents can inspect available configured workflows.
	s.Register(mcphttp.Tool{Name: "cmd_list_named", Description: cfg.ToolDescription("cmd_list_named", "List configured named commands available to cmd_run_named. Use cwd to include trusted project commands; include_args=true returns constrained extra_args metadata."), InputSchema: map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"include_args": map[string]any{"type": "boolean"}, "cwd": map[string]any{"type": "string"}},
	}, Handler: r.ListNamed})
	if cfg.Tools.RunCommand.Enabled {
		namedCommandSchema := map[string]any{
			"type": "object", "required": []string{"name"}, "additionalProperties": false,
			"properties": map[string]any{"name": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "extra_args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		}
		s.Register(mcphttp.Tool{Name: "cmd_run_named", Description: cfg.ToolDescription("cmd_run_named", "Run one named command from global config or a trusted project .personal-mcp-server.toml. If cwd is supplied in the tool call it wins; otherwise a command-level cwd may be used. Default run_mode is direct argv; trusted project commands may opt into persistent_shell when globally enabled. Use cmd_list_named with cwd to discover commands, cwd, run_mode, shell, and extra_args rules. No raw user-provided shell strings or shell job control; use cmd_start_named for server-supervised background jobs."), InputSchema: namedCommandSchema, Handler: r.RunNamed})
		s.Register(mcphttp.Tool{Name: "cmd_start_named", Description: cfg.ToolDescription("cmd_start_named", "Start one named command as a server-supervised background job and return a job_id. Uses the same named-command cwd resolution, validation, and timeout policy as cmd_run_named."), InputSchema: namedCommandSchema, Handler: r.StartNamed})
		jobIDSchema := map[string]any{
			"type": "object", "required": []string{"job_id"}, "additionalProperties": false,
			"properties": map[string]any{"job_id": map[string]any{"type": "string"}},
		}
		s.Register(mcphttp.Tool{Name: "cmd_job_status", Description: cfg.ToolDescription("cmd_job_status", "Return status, timing, exit code, and timeout metadata for a server-supervised background command job."), InputSchema: jobIDSchema, Handler: r.JobStatus})
		s.Register(mcphttp.Tool{Name: "cmd_job_cancel", Description: cfg.ToolDescription("cmd_job_cancel", "Cancel a running server-supervised background command job."), InputSchema: jobIDSchema, Handler: r.JobCancel})
		s.Register(mcphttp.Tool{Name: "cmd_job_read", Description: cfg.ToolDescription("cmd_job_read", "Read bounded tail output for a server-supervised background command job."), InputSchema: map[string]any{
			"type": "object", "required": []string{"job_id"}, "additionalProperties": false,
			"properties": map[string]any{"job_id": map[string]any{"type": "string"}, "tail_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": 200000}},
		}, Handler: r.JobRead})
		s.Register(mcphttp.Tool{Name: "cmd_job_list", Description: cfg.ToolDescription("cmd_job_list", "List running and recently finished server-supervised background command jobs."), InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"cwd": map[string]any{"type": "string"}, "include_finished": map[string]any{"type": "boolean"}},
		}, Handler: r.JobList})
	}
	for name, prompt := range cfg.Prompts {
		if prompt.Enabled {
			s.RegisterPrompt(mcphttp.Prompt{Name: name, Description: prompt.Description, Template: prompt.Template})
		}
	}
}
