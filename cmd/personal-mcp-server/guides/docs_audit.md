# Audit docs summary

The audit notes summarize the current code quality, security, and documentation posture for the repository. They are intended to help an LLM agent understand which checks are required before release and which areas need extra caution.

## Required gate

Run:

```sh
just ci
```

The CI gate includes formatting, vet, Staticcheck, golangci-lint, race-enabled tests, native integration tests, native smoke tests, and govulncheck.

## Security posture

The server remains localhost-only, bearer-token protected, Host/Origin validated, sandboxed to configured roots, and argv-only for command execution. Project trust is stored outside repositories and refreshed by project-aware MCP operations.

## Documentation posture

LLM-facing guidance is available through guide tools. Prefer `tool_catalog_batch`, `policy_describe`, and `guide_list` first, then `tool_catalog_category` only when narrower discovery is needed and `guide_read` for specific setup, project-config, tool, logging, troubleshooting, quality, release, and audit sections. Check catalog `enabled` fields and `policy_describe.cwd.disabled_tools` before using feature-gated tools.

## Platform coverage

macOS LaunchAgent service operations have manual smoke coverage. Linux systemd user-service support is implemented from the same service spec model but is currently untested in the release process. Source release artifacts are snapshots plus SHA-256 checksums and intentionally exclude git history.
