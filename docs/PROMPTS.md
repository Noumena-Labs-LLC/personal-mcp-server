# Prompt guide

personal-mcp-server registers optional MCP prompts from the `[prompts]` section of the TOML config. Prompts guide the model, but they are not security controls. Enforcement remains in Go code: roots, deny rules, read/search/output limits, command allowlists, and auth.

## Recommended default prompt

Use this in Claude Desktop when you want a careful coding workflow:

```text
Use personal MCP server carefully.

Rules:
- Work only inside the configured roots.
- Start by calling tool_catalog_batch with startup context, or just tool_catalog_batch with `{}` for the default startup bundle, then fs_list_roots.
- Use project_info, workflow_list, and cmd_list_named with cwd before guessing commands.
- Search before reading broadly.
- Read bounded line ranges, not huge files.
- Do not request denied secret files.
- For edits, call fs_apply_patch for scoped changes and verify with git_diff or an available test command.
- Explain the diff before applying non-trivial changes.
- After applying changes, call git_diff and a named verification command when available.
- Do not use cmd_run_named unless the command is clearly relevant to the user's request.
```

## Read-only inspection prompt

```text
Inspect the project using personal MCP server without modifying files. Call tool_catalog_batch first for startup discovery, then fs_list_roots, project_info, workflow_list, fs_list_dir, fs_search_text, fs_get_file_info, fs_tail_file, and fs_read_file to understand the relevant files. Do not call fs_apply_patch, fs_create_file, fs_create_dir, or cmd_run_named. Summarize what you inspected and recommend next steps.
```

## Edit-and-verify prompt

```text
Make the requested code change using personal MCP server. First call tool_catalog_batch for startup discovery, then inspect the relevant files. Use fs_apply_patch for scoped edits, optionally with dry_run=true when a preview is useful; use fs_apply_unified_patch only when server_info.features.unified_patch is true. Then call git_diff and run an available named verification command such as just-ci, just-test, go-test, or go-vet. Summarize files changed and verification results.
```

## Configuration

Example TOML:

```toml
[prompts.safe_code_edit]
enabled = true
description = "Safely inspect, patch, and verify code inside allowed roots."
template = """
Use personal MCP server carefully. Start with fs_list_roots, inspect before editing, use fs_apply_patch for scoped edits, or fs_apply_unified_patch only when server_info.features.unified_patch is true, then verify with git_diff and named commands.
"""
```

## Policy-first editing prompt

```text
Use personal MCP server. First call tool_catalog_batch or read personal-mcp://server, personal-mcp://policy, and personal-mcp://guide/tools. Stay inside configured roots. Prefer resources for read-only context. Search before reading broadly. For large files, use fs_get_file_info, fs_tail_file, fs_search_text, and bounded fs_read_file before requesting whole_file=true; only global config can raise max_read_bytes. Use fs_apply_patch for scoped edits, or fs_apply_unified_patch only when server_info.features.unified_patch is true. Treat fs_apply_patch warnings as review signals, not automatic failure, and only retry after re-reading when old text was not found. After edits, inspect git_diff and run an allowed verification command. If any operation requires approval, explain why it is needed and ask the local user to use personal-mcp-server approvals watch/list plus approve/deny; no native OS dialog is shown.
```

## Dynamic command prompt

```text
Use cmd_run_argv only for commands allowed by command_policy or after approval. Do not request shell strings, pipes, redirects, background jobs, or commands outside the configured root. Prefer cmd_run_named for exact configured commands.
```


Note: `fs_apply_patch` treats `expected_replacements` as optional and defaults it to `1` when omitted. It caps replacements and returns warnings when the found count differs; zero matches remains a hard error.


## Large file guidance

For large files, first use `fs_get_file_info`, `fs_tail_file`, and `fs_search_text`; use `fs_find` only when `server_info.features.native_find` is true. Then prefer `fs_tail_file` for recent log records or bounded `fs_read_file` calls with `start_line` and `max_lines`. Full-file reads require `whole_file=true` and still obey size limits. Raise `[limits].max_read_bytes` only in global config (`~/.personal-mcp-server/config/config.toml`) and verify with `policy_describe`; project configs cannot raise global safety limits. Patch diffs are compact and capped by `max_diff_bytes`.


## Structured data navigation prompt guidance

For JSON and JSONL files, prefer structured navigation before raw file reads. Use `json_outline`, `json_keys`, `json_slice`, and `json_search` to orient in JSON files, then `json_get` for the specific value needed. Use `jsonl_info` to discover log fields, `jsonl_tail` for recent records, `jsonl_filter` for predicates, and `jsonl_read` for pagination. Avoid whole-file reads for large JSON/JSONL files. These tools are read-only in v0.5.9.

When a workflow reveals a missing local capability, confusing result shape, docs gap, or safety-limit friction, submit concise local feedback with `feedback_submit`. Do not include secrets, credentials, private document excerpts, or large pasted file/log contents.

## Slow tool call diagnostics

When a tool call is slow or times out, prefer diagnosing before retrying repeatedly. Check server diagnostic logs for `tool_call_slow` and `tool_call_very_slow` records, which include tool name, duration, threshold, success/error state, and request/response byte counts. For large files or logs, prefer navigation/search/tail tools over full reads.

## Permissive local configs

When `[defaults].allow_everything = true`, assume missing tool and policy entries are intentionally permissive, but still respect explicit denies, disabled tools, roots, limits, secret rules, and tool-specific guards.

When writing TOML configs, use documented `snake_case` keys, for example `allow_extra_args`, `max_extra_args`, `allow_everything`, `tool_slow_ms`, and `read_default`. The server accepts common CamelCase/PascalCase aliases as a convenience, but generated guidance should prefer `snake_case`.

- For Markdown heading-only changes, use `md_replace_section_heading` instead of replacing a whole section. If `md_replace_section` uses `include_heading=true`, the replacement content must begin with the existing heading line. For adding content under an existing section, use `md_append_subsection` rather than raw text appends.
