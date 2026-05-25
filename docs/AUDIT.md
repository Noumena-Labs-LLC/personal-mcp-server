# Audit notes

This document records the current code quality, security, and documentation audit posture for `personal-mcp-server`. It is intended to be readable by both humans and LLM agents through the guide/document tools.

## Current release audit

Audit date: May 2026  
Scope: local MCP server source tree, test workflow, service posture, prompt/tool guidance, logging guidance, progressive tool discovery, feature-gated tool documentation, low-friction filesystem mutation posture, slow-tool diagnostics, stress-test posture, and docs consistency before the v0.5.10 release candidate pass.

## Code quality audit

Current quality gates are expected to run through:

```sh
just ci
```

`just ci` includes:

- formatting check
- `go vet ./...`
- Staticcheck
- golangci-lint
- race-enabled tests
- native integration tests
- native smoke tests
- govulncheck

Stress testing is available separately through `just stress-test`. It is intentionally not part of the default CI gate because it is tuned for longer-running timeout and race discovery rather than the fast path.

Integration and smoke tests are native Go tests. They create temporary roots, config files, audit logs, trust stores, and token state. They do not use the user's real `~/.personal-mcp-server/config` files.

Recent code-quality cleanup:

- Long LLM-facing guide text moved out of large Go string literals into embedded markdown guides.
- Shared mutable guide/doc catalog slices replaced with fresh-returning helper functions.
- Command-package tool catalog, resources, diagnostics, config tools, and registration code were split out of `main.go`.
- Filesystem read/delete and JSONL tool handlers were split into focused files.
- Background job output/status now use per-job locks instead of a runner-wide output lock.
- Persistent shell pools use per-pool locks.
- Project trust-store refreshes use metadata-aware caching to reduce repeated disk reads while still noticing external edits.
- Markdown parsing, guide-section navigation, structured-data navigation, bulk deletion, config tools, diagnostics tools, and filesystem sandbox behavior have focused coverage.
- Integration tests exercise tool discovery, guide access, policy/project/workflow discovery, auth, Host, Origin, and filesystem sandbox behavior.
- Stress tests exercise repeated startup/shutdown, concurrent MCP traffic, background job churn, and persistent-shell contention to catch timeout and race regressions.

Current follow-ups to watch:

- Keep new command-package features in focused files rather than growing `main.go` again.
- Keep tool response schemas stable and update tests whenever response shapes intentionally change.
- Prefer structured tests for new tools before adding more feature scope.
- Watch async audit/diagnostic logging behavior during shutdown and config reload; bounded queues should drain on close and avoid request-path file I/O.
- Treat profiling as the next step before adding more speculative performance work.
- Keep coverage improvement focused on the command package and remaining branch-heavy helpers; stress tests are for concurrency and timeout confidence, not for lifting statement coverage by themselves.

## Security audit

Core security posture:

- Server binds to localhost only by default.
- Bearer-token authentication is required for MCP calls.
- Host and Origin validation are enforced when configured.
- Filesystem operations resolve paths inside configured roots before accessing files.
- Secret-looking paths remain denied by file policy.
- File mutation tools intentionally avoid hash, expected-size, dry-run, and plan-hash gates for a low-friction single-user local model while keeping roots, policy, secret deny rules, directory refusal, and overwrite protection.
- Dynamic command execution is argv-based, policy-checked, timeout-bounded, and output-capped.
- Raw shell execution is not exposed by default.
- Project trust is stored outside the repository and refreshed by project-aware MCP operations.
- Config, token, project config, service file, and trust-store writes use atomic replacement.

Current follow-ups to watch:

- Keep all new write/edit tools on the same policy path as existing filesystem tools.
- Keep all command execution on argv-only APIs with no shell expansion.
- Keep user-service commands user-level only; do not introduce root/system service management without a separate design review.
- Keep feedback/bug-report tooling local-only and redacted by default if added later.
- Treat service stdout/stderr logs as fallback troubleshooting logs. Server diagnostics use separate `[server_logging]` configuration with optional rotating file output. Audit logs remain the structured, rotating security/action log.

## Documentation audit

Current LLM navigation posture:

- Important setup, project config, tool, log, service, troubleshooting, and documentation summaries are exposed through embedded guides.
- Because some MCP clients do not expose MCP resources to the model, guidance is mirrored through tools such as `guide_list`, `guide_read`, `setup_guide`, and `project_config_describe`.
- `guide_list` exposes guide/document outlines, and `guide_read` can return a full guide or a specific section.
- MCP `tools/list` remains flat for protocol compatibility; `tool_catalog_categories`, `tool_catalog_category`, and `tool_catalog_all` provide progressive workflow discovery for LLMs that need grouped or complete catalog views. `tool_catalog` remains a compatibility alias for the full catalog.
- Release packaging guidance is available through `docs/RELEASE.md` and the embedded `personal-mcp://docs/release` summary.
- The repository now also documents a separate stress tier in `just stress-test`; it is intended for race and timeout discovery rather than regular CI.

Docs checked in this pass:

- README command examples should prefer `./bin/personal-mcp-server` for local builds and `personal-mcp-server` for installed/PATH usage.
- Quality docs should state that `just ci` includes integration and smoke tests.
- Quality docs should state that `just ci` includes integration and smoke tests, while stress remains a separate target.
- LLM-facing guides should point agents toward `server_info`, `tool_catalog_categories`, `tool_catalog_category`, `guide_list`, `guide_read`, `project_info`, `workflow_list`, `cmd_list_named`, `policy_describe`, and `git_status` for orientation.
- Service docs should state that macOS LaunchAgent operations have manual smoke coverage, Linux systemd user-service support is implemented but currently untested in release validation, and normal diagnostic logging belongs in `[server_logging]` rather than plist/unit overrides.
- Release docs should state that source snapshots intentionally exclude git history and local/generated files, and that private development repos can publish public release-only artifacts.

Current follow-ups to watch:

- Add real Linux systemd user-service smoke coverage before claiming Linux service support is tested.
- Keep README, `docs/TOOLS.md`, `docs/SERVICE.md`, embedded guides, prompts, example config, and tool schemas synchronized.
- When adding tools, add both human docs and LLM-readable guide coverage.
- Avoid documenting planned CLI commands before confirming they are implemented and reachable in the current binary.

## Release checklist

Before tagging a release, run:

```sh
go mod tidy
just ci
just coverage-profile
```

Then verify:

- no generated binaries or coverage files are committed
- no private paths, tokens, `.env` files, or secrets are committed
- changed tool schemas have corresponding docs and tests
- source snapshots exclude `.git/`, `.tools/`, `bin/`, `dist/`, build outputs, coverage outputs, Python caches, and OS metadata
- repo files to delete are listed in the release handoff
