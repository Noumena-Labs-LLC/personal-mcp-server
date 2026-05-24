# Tools docs summary

Prefer discovery before action:

- `server_info`
- `tool_catalog_categories`
- `tool_catalog_category`
- `tool_catalog_batch` can batch category discovery and optional startup context (`server_info`, `policy`, `guides`)
- `tool_catalog_all`
- `tool_catalog`
- `policy_describe`
- `resource_list`
- `resource_read`
- `project_info`
- `workflow_list`

Prefer bounded file operations:

- `fs_search_text`
- `fs_search_text` supports per-call `max_file_size`, bounded by `limits.max_search_file_bytes`
- `fs_get_file_info`
- `fs_tail_file`
- `fs_read_file` with line ranges; for files over `limits.max_read_bytes`, avoid whole-file retries and use global config to raise the limit if needed
- `fs_tree` and `fs_find` only when `server_info.features.native_find` is true
- Traversal and JSONL tools surface silent drops with `ignored_count`, `ignored_counts`, and `ignored_samples`

Prefer scoped edits and review:

- `fs_apply_patch` (`expected_replacements` is a replacement cap; review warnings, and re-read before retrying zero-match edits)
- `fs_apply_unified_patch` only when `server_info.features.unified_patch` is true
- `fs_replace_regex` only when `server_info.features.regex_replace` is true
- `git_diff`

Prefer named commands, workflow discovery, and policy explanations:

- `project_info`
- `workflow_list`
- `cmd_list_named`
- `cmd_run_named`
- `cmd_run_named` supports `{{extra_args}}` placeholders inside configured command args for rg/grep-style positional passthrough
- `cmd_explain_policy`
- `cmd_run_argv`


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
