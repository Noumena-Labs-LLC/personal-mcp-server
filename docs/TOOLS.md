# Tool guide

personal-mcp-server exposes a small, intentionally boring tool surface. Filesystem tools are limited to configured `roots`, denied secret patterns, and file policy. Read/search/diff-style tools keep bounded output so large files do not flood the model or timeout; write/delete/move tools intentionally avoid hash, expected-size, dry-run, and plan-hash gates for low-friction single-user local workflows. A tool can be visible only when its `[tools.<name>].enabled` value is `true` in the current TOML config.

The server reloads TOML periodically. If a new config fails validation, the running server keeps the last valid config and logs a rejection.

## Recommended workflow

1. Call `server_info`, then `tool_catalog_categories` and `tool_catalog_category` when the flat MCP tool list is hard to navigate.
2. Call `fs_list_roots` to learn the workspace boundary.
3. Use `project_info`, `workflow_list`, and `cmd_list_named` with `cwd` before guessing repo commands.
4. Use `fs_list_dir` or `fs_search_text` to find relevant files; use `fs_tree` and `fs_find` only when `server_info.features.native_find` is true.
5. Use `fs_get_file_info` before reading large or unknown files.
6. Use `fs_read_file` with a bounded line range; avoid `whole_file=true` on large files unless explicitly needed.
7. Use `fs_apply_patch` for scoped edits; optionally use `dry_run=true` when a preview is useful. Use `fs_apply_unified_patch` only when `server_info.features.unified_patch` is true.
8. Apply the patch only after reviewing the diff.
9. Use `git_status`, `git_diff`, and named test/lint commands to verify.

## Tool hierarchy

MCP `tools/list` is a flat protocol response. personal-mcp-server keeps that flat list for compatibility and adds progressive read-only catalog tools for LLM navigation. Start with `tool_catalog_categories`, then call `tool_catalog_category` for one category; use `tool_catalog_all` only when a full dump is needed. `tool_catalog` remains a compatibility alias for the full catalog. Use these when deciding which tool family to use first. The categories are:

- orientation and guidance
- project workflow discovery and named commands
- filesystem read and navigation
- filesystem edits
- git inspection and verification

Catalog responses include each tool's enabled/read-only status, feature requirement when applicable, and safety notes. Some tools are feature-gated by config and may be documented even when not registered in the current build/config; check `server_info.features`, `policy_describe.cwd.disabled_tools`, and catalog `enabled` fields before relying on `fs_tree`, `fs_find`, `fs_replace_regex`, or `fs_apply_unified_patch`. It is guidance only; enforcement still comes from Go policy checks, configured roots, approval rules, and command/file policy.

### `tool_catalog_categories`

Returns compact category summaries and enabled/disabled counts. Use this first for progressive discovery.

Arguments: none.

### `tool_catalog_category`

Returns tools in one category. Arguments: `category` is required; optional `include_disabled` includes feature-gated or disabled tools, and optional `query` filters within the category.

### `tool_catalog_all` and `tool_catalog`

`tool_catalog_all` returns the complete hierarchical catalog. `tool_catalog` is a compatibility alias for the same full response. Use full catalog calls only when the client can handle a larger response.

## Filesystem tools

### `fs_list_roots`

Lists configured filesystem roots. Use this first. It returns the only top-level areas where local filesystem operations are allowed.

Arguments: none.

### `fs_list_dir`

Lists entries under a directory inside configured roots.

Arguments:

```json
{
  "path": ".",
  "recursive": false,
  "include_hidden": false,
  "max_entries": 200
}
```

Notes:

- Hidden files are skipped unless `include_hidden=true`.
- Symlink escapes outside configured roots are skipped or rejected.
- Recursive listings are capped by `max_entries`.

### `fs_get_file_info`

Returns metadata for a file or directory without reading contents.

Arguments:

```json
{
  "path": "personal-mcp-server/README.md"
}
```

Use this before reading unknown or potentially large files.

### Markdown section tools

Use these tools for Markdown files instead of reading or editing entire large documents.

- `md_outline` returns headings, stable section ids, and line ranges.
- `md_read_section` reads one section by id or title.
- `md_replace_section` replaces a section body by default, preserving the heading.
- `md_replace_section_heading` renames or relevels a heading without replacing its body.
- `md_insert_section` inserts a new section before or after an existing section.
- `md_append_section` appends a new section to the end of a document.
- `md_append_subsection` appends a child subsection under an existing section.

All Markdown write tools respect file policy, optional `dry_run`, compact diffs, and atomic writes. Headings inside fenced code blocks are ignored.

### `fs_create_dir`

Creates a directory inside configured roots with `mkdir -p` semantics by default. It refuses file conflicts and supports `dry_run`.

Example:

```json
{"path":"internal-docs/ai/session-logs","parents":true,"dry_run":true}
```

Set `parents=false` to require the immediate parent directory to already exist.

### `fs_replace_file`

Replaces one existing text file. Use it for intentional whole-file overwrites, not for new files. Returns a compact diff, writes atomically, and supports optional `create_backup`. Refuses directories, missing files, binary files, and files outside configured roots.

### `fs_delete_file`

Deletes one existing file. Refuses directories and paths outside configured roots. Prefer this over raw `rm` when the desired operation is a single-file deletion inside an allowed root.

### `fs_move_file`

Moves or renames one existing file. Refuses directories and destination overwrites unless `overwrite=true`. Both source and destination must be inside configured roots and allowed by file policy.

### `fs_append_file`

Appends text to a file inside configured roots. Prefer Markdown section tools for Markdown documents when possible. Use `dry_run=true` only when a preview is useful.

### `fs_read_file`

Reads a bounded range from a text file.

Arguments:

```json
{
  "path": "personal-mcp-server/README.md",
  "start_line": 1,
  "max_lines": 120
}
```

Notes:

- Binary files are refused.
- Denied secret files are refused.
- Oversized `whole_file=true` reads fast-fail with `file_too_large_for_full_read` and suggested navigation tools instead of timing out.
- Prefer reading specific line ranges instead of whole files.
- For logs or recent diagnostics, prefer `fs_tail_file`.

### `fs_tail_file`

Reads the last lines of a text or log file without scanning the whole file from the beginning. It respects roots, file policy, binary-file refusal, and `limits.max_read_bytes` as the maximum tail window.

Example:

```json
{"path":"logs/server.log","lines":200}
```

Use this before `fs_read_file` on logs, diagnostics, JSONL, and other append-heavy files when the recent records are most relevant.

### `fs_search_text`

Searches text files inside configured roots.

Arguments:

```json
{
  "path": ".",
  "query": "cmd_run_named",
  "regex": false,
  "case_sensitive": false,
  "max_results": 50,
  "offset": 0
}
```

Notes:

- Plain substring search is the default.
- Regex is opt-in.
- `max_file_size` optionally lowers the per-call file-size ceiling; it cannot exceed `limits.max_search_file_bytes`.
- Searches skip binaries, denied files, and files over the applied search file-size limit. Results include `applied_max_file_size` plus skipped-file counts and samples.
- Traversal and JSONL scan tools now surface silent drops with `ignored_count`, `ignored_counts`, and bounded `ignored_samples`.
- If the response has `truncated=true`, repeat the same call with `offset` set to `next_offset`.

### `fs_apply_patch`

Edits an existing file using exact text replacements. This is the preferred write tool.

Single edit:

```json
{
  "path": "personal-mcp-server/README.md",
  "old": "old exact text",
  "new": "new exact text",
  "expected_replacements": 1,
  "dry_run": true,
  "create_backup": false
}
```

Multiple edits:

```json
{
  "path": "personal-mcp-server/README.md",
  "edits": [
    {
      "old": "old exact text",
      "new": "new exact text",
      "expected_replacements": 1
    }
  ],
  "dry_run": true
}
```

Notes:

- Use `dry_run=true` when you want a preview before writing.
- Exact matches and `expected_replacements` reduce accidental broad edits.
- `expected_replacements` is optional and defaults to `1`. It is a replacement cap, not an exact-match precondition: if more matches exist, the first N are replaced and the response includes a warning; if fewer matches exist, all found matches are replaced and the response includes a warning.
- If the `old` text is not found at all, the edit is rejected; re-read the target range before retrying.
- The result includes a compact unified diff and may include a `warnings` array. The `edits` array items must use `old` and `new`.
- Writes are atomic when `dry_run=false`.

### `fs_create_file`

Creates a new text file.

Arguments:

```json
{
  "path": "personal-mcp-server/notes/new-file.md",
  "content": "# Notes\n",
  "fail_if_exists": true,
  "create_dirs": true
}
```

Notes:

- Existing files are refused by default.
- Denied paths are refused.

## Project and command tools

### `git_diff`

Returns a bounded Git diff without going through a shell.

Arguments:

```json
{
  "path": "personal-mcp-server",
  "staged": false,
  "max_bytes": 50000
}
```

### `cmd_run_named`

Runs one named command from the TOML `[[commands]]` list.

Arguments:

```json
{
  "name": "just-ci",
  "cwd": "personal-mcp-server"
}
```

Notes:

- No raw shell.
- No arbitrary arguments.
- No PID management or shell job control. Use `cmd_start_named` for server-supervised background jobs.
- If `cwd` is supplied in the tool call, it wins. If it is omitted, the command may use its configured `cwd`. Global command `cwd` values may be absolute or root-relative and must resolve inside configured roots. Project command `cwd` values must be relative to the trusted project root.
- Configured command `args` may include `{{extra_args}}` to place validated `extra_args` before fixed trailing args. This is useful for `rg`/`grep`-style commands where the pattern should come before a configured search root such as `"."`.
- CWD must resolve inside configured roots.
- Output and runtime are capped.
- The default argv runner uses a stripped/server-controlled environment, not the user's interactive shell. This is safer and more reproducible, but it can miss pyenv/asdf/nvm/direnv, virtualenv activation, aliases, and shell PATH setup. Trusted project commands can opt into `run_mode = "persistent_shell"` when `[command_environment].allow_persistent_shell = true`.

### Background command jobs

`cmd_start_named` starts a configured named command and returns immediately with a `job_id`. Jobs are owned by the server, not by the shell. The same named-command validation, trusted-project checks, output caps, timeout policy, and process-tree cleanup rules apply. Background jobs capture stdout/stderr while running through server-owned process execution; they do not use shell job control or occupy a persistent-shell session.

Arguments match `cmd_run_named`:

```json
{
  "name": "just-ci",
  "cwd": "personal-mcp-server",
  "extra_args": []
}
```

Use:

- `cmd_job_status` with `{ "job_id": "..." }` to check state, timing, exit code, and timeout metadata.
- `cmd_job_read` with `{ "job_id": "...", "tail_bytes": 12000 }` to read bounded tail output.
- `cmd_job_cancel` with `{ "job_id": "..." }` to cancel a running job.
- `cmd_job_list` to list running and recently finished jobs.

Statuses are `running`, `exited`, `failed`, `timed_out`, and `cancelled`. Finished job summaries are retained temporarily for follow-up reads. This is intentionally not shell job control: there is no `&`, `jobs`, `fg`, `bg`, `disown`, interactive stdin, or direct PID management.

## Safe user prompt for Claude Desktop

Use this prompt when starting a coding task:

```text
Use the personal MCP server tools only inside the configured roots. First call fs_list_roots, then inspect relevant files with fs_search_text, fs_get_file_info, fs_tail_file, and fs_read_file. For edits, use fs_apply_patch for scoped replacements or fs_apply_unified_patch when server_info.features.unified_patch is true; review returned diffs when useful and apply only the intended changes. After edits, use git_diff and an available named command such as just-ci, just-test, or go-test to verify. Do not read denied secret files or request paths outside the configured roots.
```

## Dynamic command policy

`cmd_run_argv` runs an executable plus argv array after `command_policy` evaluation. It never uses a shell, so shell syntax such as pipes, redirects, glob expansion, `&&`, and shell-managed background jobs is not supported.

Example:

```json
{
  "exec": "git",
  "args": ["status", "--short"],
  "cwd": "personal-mcp-server"
}
```

Recommended workflow:

1. Call `server_info` and `policy_describe` first.
2. Prefer `cmd_run_named` for exact known commands.
3. Use `cmd_run_argv` for policy-covered dynamic commands such as Git.
4. If a command is prompt-required, explain why it is needed and wait for local approval.

## Policy discovery

`server_info` returns the running server version, module path, transport, feature summary, and large-file guidance. `policy_describe` returns the effective roots, enabled tools, file policy defaults and rules, command policy defaults and rules, and approval behavior. It intentionally does not expose bearer tokens.

## Read-only resources

The server also exposes read-only MCP resources:

- `personal-mcp://roots` — configured roots
- `personal-mcp://policy` — effective policy summary
- `personal-mcp://guide/tools` — this workflow guidance
- `personal-mcp://file/{path}` — bounded file read
- `personal-mcp://tree/{path}` — bounded directory listing
- `personal-mcp://info/{path}` — file or directory metadata

Use resources for context when the client supports them; use tools for mutation and command execution. If your client cannot list/read resources directly, use `resource_list` and `resource_read` as tool-based mirrors of the resource content.

### `resource_list`

Lists personal MCP resource URIs and templates for clients that do not expose MCP resources directly.

### `resource_read`

Reads one `personal-mcp://` resource by URI. Examples:

```json
{"uri": "personal-mcp://policy"}
```

```json
{"uri": "personal-mcp://guide/tools"}
```

```json
{"uri": "personal-mcp://tree/personal-mcp-server"}
```

## Per-call cwd

Most path-based tools accept an optional `cwd` argument. `cwd` is resolved inside configured roots and is used only as the base for that tool call. The server never calls `os.Chdir` and does not maintain hidden session working-directory state. Use `cwd` when one configured root contains multiple projects, for example `{ "cwd": "personal-mcp-server", "path": "internal/fsx/tools.go" }`.

## Audit visibility

Audit logs can be configured with `[audit].path` or overridden with `--audit-log`. If no audit path is configured, audit events are written to stderr.

Note: `fs_apply_patch` treats `expected_replacements` as optional and defaults it to `1` when omitted. It caps how many exact matches are replaced and reports warnings when the found count differs; only zero matches remains a hard error.

## Large file guidance

For large files, first use `fs_get_file_info`, `fs_tail_file`, and `fs_search_text`; use `fs_find` as an additional discovery tool only when `server_info.features.native_find` is true. Then prefer `fs_tail_file` for recent log records or bounded `fs_read_file` calls with `start_line` and `max_lines`. Full-file reads require `whole_file=true` and still obey size limits. To raise whole-file read limits, change global config `[limits].max_read_bytes` in `~/.personal-mcp-server/config/config.toml`, restart or let hot-reload apply the valid TOML, and confirm with `policy_describe`; project configs cannot raise this global safety limit. Patch diffs are compact and capped by `max_diff_bytes`.

## Go-native grep/find/sed-style tools

personal-mcp-server keeps core filesystem operations in Go so the server works on machines without GNU grep/find/sed or git.

### fs_find

Use `fs_find` to discover files and directories inside configured roots when `server_info.features.native_find` is true. It is bounded, root-aware, and supports `cwd`, type filters, glob filters, depth, size, and result caps. Prefer it over shell `find` when available. If `truncated=true`, repeat the same call with `offset` set to `next_offset`.

Example:

```json
{
  "cwd": "personal-mcp-server",
  "path": ".",
  "type": "file",
  "name_globs": ["*.go", "*.toml"],
  "exclude_globs": [".git/**", ".tools/**", "vendor/**"],
  "max_results": 200,
  "offset": 0
}
```

### fs_search_text

`fs_search_text` is the Go-native grep-like tool. It supports literal or regex search, include/exclude globs, context lines, result caps, and offset pagination.

Example:

```json
{
  "cwd": "personal-mcp-server",
  "path": ".",
  "query": "approval.NewManager",
  "include_globs": ["**/*.go"],
  "exclude_globs": [".git/**", ".tools/**"],
  "context_before": 2,
  "context_after": 2,
  "max_results": 50,
  "offset": 0
}
```

### fs_replace_regex

`fs_replace_regex` is the Go-native sed-like replacement tool. It uses Go RE2 regular expressions, never shells out, supports line ranges, defaults to one replacement, supports `dry_run`, and returns a compact diff.

Example:

```json
{
  "cwd": "personal-mcp-server",
  "path": "internal/fsx/tools.go",
  "pattern": "\\bexpected_replacements\\b",
  "replacement": "expectedReplacements",
  "start_line": 1,
  "end_line": 400,
  "max_replacements": 20,
  "dry_run": true
}
```

## Policy explanation tools

Use `cmd_explain_policy` and `file_explain_policy` before attempting operations that might be denied or prompt for approval. They return the policy decision and matched rule without performing the operation. `file_explain_policy` includes the global decision, any trusted project decision, and the effective decision used by file tools.

### cmd_list_named

Lists named commands configured for `cmd_run_named`. Use this before running a named command when the available names are unknown.

Example:

```json
{"include_args": true}
```

### fs_search_text large-file diagnostics

Search results include `skipped_too_large_count` and `skipped_too_large_samples` when files are skipped because they exceed `limits.max_search_file_bytes`. Narrow `path`, use `include_globs`/`exclude_globs`, or raise the limit if needed.

## Project config tools

### `project_info`

Discover the nearest checked-in `.personal-mcp-server.toml` project config for a given `cwd` and report whether it is trusted. The response includes guide metadata, workflow aliases, protected file summaries, and generated file summaries when present.

Example:

```json
{
  "cwd": "my-project",
  "include_commands": true
}
```

Use this before running project-specific named commands. Project commands are only executable after the project is trusted locally with `personal-mcp-server project trust --cwd <project>`.

### `cmd_list_named` with `cwd`

`cmd_list_named` accepts an optional `cwd`. When `cwd` points inside a trusted project, the response includes both global commands and project commands.

```json
{
  "cwd": "my-project",
  "include_args": true
}
```

### `cmd_run_named` with project commands

When `cwd` points inside a trusted project, `cmd_run_named` first checks project commands from `.personal-mcp-server.toml`, then falls back to global commands. If a project command defines `cwd`, it must be relative to the trusted project root and is used only when the tool call omits `cwd`; otherwise the tool-call `cwd` selects both the project and execution directory.

```json
{
  "cwd": "my-project",
  "name": "test"
}
```

## tool_catalog_batch

`tool_catalog_batch` returns multiple catalog categories in one call, with optional category summaries plus startup context such as `server_info`, `policy`, and `guides`. Use it when a client would otherwise issue several startup discovery calls just to preload the same tool groups.

```json
{
  "categories": ["filesystem_read", "project_workflow"],
  "include_summaries": true,
  "include_server_info": true,
  "include_policy": true,
  "include_guides": true
}
```

## fs_tree

`fs_tree` returns a compact, bounded directory tree inside configured roots. Prefer it for project orientation instead of recursive directory listings.

## workflow_list

`workflow_list` returns workflow aliases from the nearest project config, such as `test`, `lint`, `format`, `build`, `ci`, and `typecheck`. Use it before guessing command names.

## audit CLI

When `[audit].path` is set, `personal-mcp-server audit show --last 50` prints recent audit log lines. Use filters to narrow noisy logs:

```bash
personal-mcp-server audit show --config ~/.personal-mcp-server/config/config.toml --last 20 --tool fs_read_file
personal-mcp-server audit show --config ~/.personal-mcp-server/config/config.toml --decision deny --format pretty
personal-mcp-server audit tail --config ~/.personal-mcp-server/config/config.toml --follow --contains secret
```

Supported audit filters:

- `--tool TOOL` matches JSON audit events with a specific `tool` value.
- `--decision DECISION` matches JSON audit events with a matching `decision` or `action` value.
- `--contains TEXT` matches raw log lines containing the text.
- `--format raw|pretty` controls output. `raw` preserves JSON lines; `pretty` prints a compact summary.

Without `--follow`, `audit tail` prints a snapshot and exits. With `--follow`, it polls the configured audit log and prints new matching lines.

## LLM-readable guides and resources

The server exposes embedded markdown resources for LLM navigation:

- `personal-mcp://guide/index`
- `personal-mcp://guide/tools`
- `personal-mcp://guide/project-config`
- `personal-mcp://guide/setup`
- `personal-mcp://guide/setup-macos`
- `personal-mcp://guide/setup-linux`
- `personal-mcp://guide/claude-desktop`
- `personal-mcp://guide/services`
- `personal-mcp://guide/logs`
- `personal-mcp://guide/troubleshooting`
- `personal-mcp://docs/readme`
- `personal-mcp://docs/project-configs`
- `personal-mcp://docs/tools`
- `personal-mcp://docs/security`
- `personal-mcp://docs/threat-model`
- `personal-mcp://docs/quality`

Use `resource_list` and `resource_read` when the MCP client does not expose resources directly. Use `project_config_describe` before generating `.personal-mcp-server.toml`, and `setup_guide` when guiding a user through macOS, Linux, Claude Desktop, service, logging, or troubleshooting setup.

## LLM guide discovery tools

Some MCP clients expose tools but not resources to the model. Use these tools for portable LLM navigation:

- `guide_list` lists embedded setup, project-config, troubleshooting, and docs guides.
- `guide_read` reads one guide by name, for example `project-config`, `setup-macos`, `claude-desktop`, `logs`, or `docs/readme`.
- `project_config_describe` returns the project config guide directly.
- `setup_guide` returns setup guidance by topic.

`cmd_list_named`, `project_info`, `workflow_list`, `policy_describe`, `resource_list`, and `resource_read` are read-only discovery tools intended to be available at session start.

## Git status

`git_status` returns structured git working-tree status for a repository inside an allowed root. Pass `include_untracked=true` when you need to see new files that do not appear in `git_diff`.

## Command environments

`cmd_run_named` uses direct argv execution by default. This avoids shell parsing and makes command execution easier to audit, but it does not load the user's interactive shell environment. If a command depends on pyenv/asdf/nvm/direnv, virtualenv activation, shell functions, aliases, or PATH setup in `.zshrc`/`.bashrc`, it may work in a terminal but fail under the default runner.

Trusted project commands can opt into `run_mode = "persistent_shell"` when global config sets `[command_environment].allow_persistent_shell = true`. In that mode, the server keeps a disposable shell session per project/shell, sends an explicit `cd` before each command, and runs only configured named commands with validated `extra_args`. If the shell misbehaves or times out, the server kills it. Captured persistent-shell output is stripped of common terminal control sequences before it is returned to clients, but projects should still prefer argv mode unless they genuinely need shell startup behavior.

Background jobs always run as server-owned `background_exec` processes even when the configured named command would normally use `persistent_shell`; responses include both the configured and actual run modes plus a note.

Use `cmd_list_named` with `include_args=true` to inspect each command's `cwd`, `run_mode`, `shell`, `allow_extra_args`, and active `max_extra_args` metadata before calling `cmd_run_named`.

## Diagnostics and config inspection

### `diagnostics_recent_slow_tools`

Reads recent `tool_call_slow` and `tool_call_very_slow` diagnostic records from the configured server diagnostic log. Use it when tools feel slow or time out before repeatedly retrying the same operation. Requires `[server_logging].path` to point at a readable log file.

### `config_validate`

Validates a TOML config file and returns structured `ok`, `errors`, `warnings`, and `canonical_suggestions` fields. Use it before installing or trusting generated config changes.

### `config_explain`

Summarizes the effective config, including defaults, limits, policies, tools, server logging, roots, and safety notes. Use it to answer why a tool or policy behaves a certain way.

## Structured JSON and JSONL navigation

Use these read-only tools to navigate structured data without dumping whole files into the model. JSON tools accept any valid JSON root type: object, array, string, number, boolean, or null. Select locations with RFC 6901 JSON Pointer; the empty pointer `""` selects the root, `~1` escapes `/`, and `~0` escapes `~`.

- `json_outline`: compact structure map with bounded depth and children.
- `json_keys`: object keys or array index windows at one pointer.
- `json_get`: one targeted value by pointer.
- `json_slice`: bounded page from an array.
- `json_search`: search keys and scalar values, returning pointers and previews; use `type_filter` and `pointer_prefix` to narrow large documents.
- `json_validate`: validate JSON and report the root type.

For JSONL logs, prefer `jsonl_info` first to discover fields, then use `jsonl_filter`, `jsonl_tail`, or `jsonl_read` for bounded records. `jsonl_filter` supports exact matches, contains, exists/missing, nested dotted fields, numeric ranges, and timestamp ranges. Malformed and empty lines are counted instead of crashing the workflow. JSON and JSONL tools are read-only.

## Local feedback

Use `feedback_submit` when a task reveals a missing tool, confusing schema, documentation gap, safety-limit friction, or useful feature request. Keep feedback concise and structured. Do not include secrets, credentials, large file contents, raw private logs, or private document excerpts. Feedback is local-only and appended to the configured JSONL file.
