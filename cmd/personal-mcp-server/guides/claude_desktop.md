# Claude Desktop connection guide

Claude Desktop can connect to this local HTTP MCP server through an MCP bridge such as `mcp-remote`.

Example shape:

```json
{
  "mcpServers": {
    "personal-mcp": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote@latest",
        "http://127.0.0.1:3929/mcp",
        "--header",
        "Authorization: Bearer YOUR_TOKEN_HERE"
      ]
    }
  }
}
```

After changing server tools or Claude Desktop config, restart Claude Desktop.

Recommended session start inside Claude Desktop:

1. Call `tool_catalog_batch` with `{}` for the default startup bundle.
2. If resources are visible, read `personal-mcp://guide/index` and `personal-mcp://guide/tools`.
3. If resources are not visible, call `guide_list` and `guide_read` early instead of waiting for an extra discovery round-trip later.

If resources are not visible in the client UI, use `resource_list` and `resource_read` tools. They mirror important `personal-mcp://` resources for tool-only clients.

Known client-side limits:

- Long Claude/Desktop sessions can drop lower-frequency tools from the active tool cache. If file-edit or guide tools disappear, refresh discovery and restore the personal-mcp tool set before continuing.
- Persistent-shell-backed commands can still be fragile with long inline multi-paragraph strings in some clients. Prefer file-backed commit-message flows when your project config or client exposes them.
