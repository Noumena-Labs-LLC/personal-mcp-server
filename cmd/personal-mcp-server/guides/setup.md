# Setup guide overview

Use these steps to guide a user through a local personal MCP server setup.

1. Install the binary into the user root: `just install-user`. For source-tree-only testing, `just build` still writes `bin/personal-mcp-server`.
2. Create a config directory: `mkdir -p ~/.personal-mcp-server/config`.
3. Generate or choose a bearer token.
4. Create `~/.personal-mcp-server/config/config.toml` with roots limited to the user's intended workspace.
5. Validate config: `personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml`.
6. Run doctor: `personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml`.
7. Start manually: `personal-mcp-server serve --config ~/.personal-mcp-server/config/config.toml`.
8. Connect Claude Desktop through `mcp-remote` using `http://127.0.0.1:3929/mcp` and the bearer token.
9. Verify with `tools/list` or the client UI.
10. Optionally install a user-level service after manual startup works.

Never bind the server to `0.0.0.0`. Keep it on `127.0.0.1` and use bearer-token auth.
