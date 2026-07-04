# personal-mcp-server README summary for LLMs

personal-mcp-server is a local-only MCP server for controlled filesystem, search, patch, project workflow, audit, approval, and command access under configured roots.

Core safety properties:

- Binds to `127.0.0.1`.
- Requires bearer-token auth.
- Validates Host and Origin when configured.
- Keeps filesystem access inside configured roots.
- Uses argv-style command execution and policy checks instead of raw shell strings.
- Provides bounded reads/searches and explicit edit tools.
- Supports project-specific `.personal-mcp-server.toml` manifests with local trust stored outside the repo.

For setup, read `personal-mcp://guide/setup`. For project config, read `personal-mcp://guide/project-config`. For tools, read `personal-mcp://guide/tools`.

For packaging and public/private artifact distribution, read `personal-mcp://docs/release`.
