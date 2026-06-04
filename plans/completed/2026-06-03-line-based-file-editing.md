## Context

The current filesystem surface already supports bounded reads, regex replacement with line windows, and exact text patching, but it still lacks a dedicated line-oriented edit path. That makes it awkward to do the common workflow of “read around a line, anchor the target by text, then replace or insert a nearby range” without either relying on brittle whole-file patching or overloading regex behavior for simple text edits.

The current `fs_get_file_info` also only returns a sampled line estimate. For the line-edit workflow, the server needs an exact line count path that stays read-only and does not require reading file contents into the model.

## Approach

- Add a new line-oriented filesystem edit tool, likely `fs_edit_lines`, with explicit operations for `replace`, `insert_before`, `insert_after`, and `delete`.
- Require line-number anchors for all operations and add a literal `line_starts_with` guard so callers can validate the target line before editing and reduce off-by-one mistakes.
- Keep the guard textual rather than regex-based in the first version so the API stays predictable and easy to call from clients.
- Implement the edit path as an atomic file rewrite that streams content through a temporary file, so large files do not require loading the whole file into memory.
- Add exact line-count support to `fs_get_file_info` behind an explicit request flag, so callers can ask for `count_lines=true` when they need a precise count for editing.
- Update registration, catalog metadata, docs, prompts, examples, and config defaults so the new tool and file-info behavior are discoverable and consistently described.
- Add focused tests around line counting, line-anchored replacement, insertion, delete behavior, and the guard failures for mismatched or out-of-range anchors.

## Verification

- Run focused Go tests for `internal/fsx` and the registration/config paths that surface the new tool and file-info output.
- Exercise representative cases for:
  - exact line counting on small and large text files,
  - replacing a line range with a matching `line_starts_with` guard,
  - inserting before and after a line anchor,
  - rejecting mismatched anchors and invalid line ranges,
  - preserving atomic write behavior and diff output.
- Run the repo’s relevant documentation or config checks if the plan touches generated prompt/config files.

## Outcome

Completed on 2026-06-03.

Added `fs_edit_lines` with `replace`, `insert_before`, `insert_after`, and
`delete` operations, all anchored by line number and optional
`line_starts_with` guards. The implementation streams the source file through a
temporary rewrite file and preserves compact diff output without requiring
whole-file patch text.

Added `count_lines=true` support to `fs_get_file_info` so callers can request an
exact line count without returning file contents to the model.

Updated tool registration, config wiring, catalog metadata, generated config
templates, prompts, and docs so the new line-edit workflow is discoverable and
described consistently.

Verified with:

- `go test ./internal/fsx ./internal/config ./internal/policy ./cmd/personal-mcp-server`
- `just ci`
