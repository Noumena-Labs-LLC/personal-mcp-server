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

If resources are not visible in the client UI, use `resource_list` and `resource_read` tools. They mirror important `personal-mcp://` resources for tool-only clients.
