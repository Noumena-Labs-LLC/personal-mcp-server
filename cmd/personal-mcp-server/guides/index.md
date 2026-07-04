# personal MCP server guide index

Use this index to orient yourself before configuring or operating personal MCP server.

Recommended first reads:

1. `personal-mcp://server` for version and enabled capabilities.
2. `tool_catalog_batch` for one-call startup discovery when the client only shows a flat tool list; use `tool_catalog_category` only for narrower follow-up discovery.
3. `personal-mcp://policy` for roots, tools, file policy, command policy, and approval behavior.
4. `personal-mcp://guide/tools` for safe tool-use workflow, or `guide_list` plus `guide_read` when the client is tool-only.
5. `personal-mcp://guide/project-config` before creating or editing `.personal-mcp-server.toml`.
6. `personal-mcp://guide/setup` before helping a user install or connect the server.
7. `personal-mcp://docs/release` before packaging or publishing an artifact.

Important setup resources:

- `personal-mcp://guide/setup-macos`
- `personal-mcp://guide/setup-linux`
- `personal-mcp://guide/claude-desktop`
- `personal-mcp://guide/services`
- `personal-mcp://guide/logs`
- `personal-mcp://guide/troubleshooting`

Documentation mirrors:

- `personal-mcp://docs/readme`
- `personal-mcp://docs/project-configs`
- `personal-mcp://docs/tools`
- `personal-mcp://docs/security`
- `personal-mcp://docs/threat-model`
- `personal-mcp://docs/quality`
- `personal-mcp://docs/release`

When the client does not expose MCP resources directly, use `resource_list` and `resource_read` to navigate these URIs.

For MCP clients that do not expose resources to the model, call `guide_list` and `guide_read` early in the session to access these same guides through tools without waiting for a later discovery pass.

## Audit and release checks

Use `guide_read` with `name: "docs/audit"` for the current code quality, security, documentation, and release-readiness audit posture. Use `guide_read` with `name: "docs/release"` before packaging source snapshots.
