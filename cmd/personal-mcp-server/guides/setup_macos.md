# macOS setup guide

Recommended local build:

```bash
just build
personal-mcp-server version
```

Recommended config location:

```text
~/.personal-mcp-server/config/config.toml
~/.personal-mcp-server/config/token
```

Generate a token:

```bash
mkdir -p ~/.personal-mcp-server/config
openssl rand -base64 32 > ~/.personal-mcp-server/config/token
chmod 600 ~/.personal-mcp-server/config/token
```

Use a config with:

```toml
[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_file = "~/.personal-mcp-server/config/token"
validate_origin = true
allowed_origins = ["http://127.0.0.1", "http://localhost"]

[server_logging]
level = "info"
path = "~/.personal-mcp-server/logs/server.log"
max_bytes = 10485760
max_backups = 5
```

Validate and start:

```bash
personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server serve --config ~/.personal-mcp-server/config/config.toml
```

User-level service:

- LaunchAgent path: `~/Library/LaunchAgents/com.noumenalabs.personal-mcp-server.plist`
- Server diagnostic log: `~/.personal-mcp-server/logs/server.log` when `[server_logging].path` is set.
- LaunchAgent stdout/stderr logs: fallback troubleshooting logs under `~/.personal-mcp-server/logs/`.
- Install only after manual startup works.
- Do not use sudo for user-level service setup.
