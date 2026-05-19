# personal MCP server tool guide

Start with discovery:

1. Call `server_info`, `tool_catalog_categories`, `guide_list`, and `policy_describe`, or read `personal-mcp://server` and `personal-mcp://policy`. Use `tool_catalog_category` for one category and `tool_catalog_all` only when a complete catalog is needed.
2. Read `personal-mcp://roots`, `personal-mcp://guide/index`, and `personal-mcp://guide/tools`.
3. Use `project_info` and `workflow_list` with `cwd` before guessing project commands.
4. Use read-only resources for context and tools for actions.

Path and `cwd` rules:

- Work only inside configured roots.
- `cwd` is per-call only; it never changes the server process directory.
- When `cwd` already points at a project, use `path="."` for that project root instead of repeating the project name.
- Absolute paths are allowed only when they resolve inside configured roots.

Read-only workflow:

- Use `fs_list_roots` or `personal-mcp://roots`.
- Use `fs_tree` for a compact bounded directory view when `server_info.features.native_find` is true.
- Use `fs_find` to discover files when `server_info.features.native_find` is true; otherwise use `fs_search_text` for Go-native grep-like search before reading many files.
- Use `fs_get_file_info` before reading unfamiliar files; it returns size, text sniffing, a line-count estimate, and large-file suggestions.
- Use `fs_tail_file` for recent log output and `fs_read_file` with `start_line` and `max_lines` for source or targeted context. If a file is over `limits.max_read_bytes`, do not retry whole-file reads; use targeted search/ranges or ask the user to raise `[limits].max_read_bytes` in the global config (`~/.personal-mcp-server/config/config.toml`) and verify with `policy_describe`.
- For Markdown files, prefer `md_outline` and `md_read_section` before reading whole documents.
- Avoid `whole_file=true` on large files unless the user explicitly needs the whole file.

Edit workflow:

- Use `file_explain_policy` before risky edits.
- Use `fs_apply_patch` for scoped edits. `dry_run=true` is optional when a preview is useful. `expected_replacements` caps replacements and may return warnings when the found count differs; treat warnings as review signals. If the old text is not found, re-read the exact target range before retrying. Use `fs_apply_unified_patch` only when `server_info.features.unified_patch` is true and `fs_replace_regex` only when `server_info.features.regex_replace` is true.
- For Markdown docs, prefer `md_replace_section`, `md_replace_section_heading`, `md_insert_section`, `md_append_section`, or `md_append_subsection`; use `dry_run=true` only when a preview is useful. Use `md_replace_section_heading` for heading-only renames and `md_append_subsection` for child sections under an existing parent.
- Use `fs_append_file` for simple append-only updates when a structured Markdown section tool does not fit.
- Review compact diffs before applying.
- Use `git_diff` after edits when the target is inside a git repository.
- Use `fs_create_file` only for new files. Use `fs_create_dir` for directory creation with `mkdir -p` semantics. Use `fs_replace_file` for intentional whole-file overwrites, `fs_delete_file` or `fs_delete_files` for file cleanup, and `fs_move_file` for renames instead of raw `rm`/`mv`.

Command workflow:

- Use `cmd_list_named` and `workflow_list` before `cmd_run_named` or `cmd_start_named`. With `include_args=true`, check configured `cwd`, `run_mode`, `shell`, and extra-arg rules.
- Prefer trusted project named commands over dynamic argv commands. If a named command has configured `cwd`, the tool call may omit `cwd`; when the tool call supplies `cwd`, it wins.
- Use `cmd_explain_policy` before `cmd_run_argv`.
- Use `cmd_run_sequence` for configured multi-step workflows instead of raw `&&` or `;` chaining.
- No raw user-provided shell strings are supported. Direct argv commands do not use pipes, redirects, shell glob expansion, or shell-managed background jobs. Trusted project commands may opt into `run_mode = "persistent_shell"`, but the command string is still built from configured argv and validated `extra_args`. Use `cmd_start_named` plus `cmd_job_status`, `cmd_job_read`, `cmd_job_cancel`, and `cmd_job_list` for server-supervised background jobs with server-owned stdout/stderr capture.

Approval workflow:

- If a tool call requires approval, explain what will happen and why it is needed.
- The server does not show a native OS or Claude Desktop dialog. The local user can inspect and decide approvals through the approval CLI (`personal-mcp-server approvals watch/list/approve/deny`) or local approval HTTP endpoints.


When MCP resources are not visible to the model, use `guide_read` instead of `personal-mcp://guide/*` URIs. MCP `tools/list` is flat; use `tool_catalog_categories` then `tool_catalog_category` for grouped progressive discovery. `tool_catalog_all` returns the full catalog, and `tool_catalog` remains a compatibility alias. Some tools described in guides are feature-gated; check `server_info.features`, `policy_describe.cwd.disabled_tools`, or catalog `enabled` fields before using them.


## Structured JSON and JSONL navigation

Use these read-only tools to navigate structured data without dumping whole files into the model. JSON tools accept any valid JSON root type: object, array, string, number, boolean, or null. Select locations with RFC 6901 JSON Pointer; the empty pointer `""` selects the root, `~1` escapes `/`, and `~0` escapes `~`.

- `json_outline`: compact structure map with bounded depth and children.
- `json_keys`: object keys or array index windows at one pointer.
- `json_get`: one targeted value by pointer.
- `json_slice`: bounded page from an array.
- `json_search`: search keys and scalar values, returning pointers and previews; use `type_filter` and `pointer_prefix` to narrow large documents.
- `json_validate`: validate JSON and report the root type.

For JSONL logs, prefer `jsonl_info` first to discover fields, then use `jsonl_filter`, `jsonl_tail`, or `jsonl_read` for bounded records. `jsonl_filter` supports exact matches, contains, exists/missing, nested dotted fields, numeric ranges, and timestamp ranges. Malformed and empty lines are counted instead of crashing the workflow. JSON and JSONL tools remain read-only in v0.5.7.


## Local feedback

Use `feedback_submit` when a task reveals a missing tool, confusing schema, documentation gap, safety-limit friction, or useful feature request. Keep feedback concise and structured. Do not include secrets, credentials, large file contents, raw private logs, or private document excerpts. Feedback is local-only and appended to the configured JSONL file.
