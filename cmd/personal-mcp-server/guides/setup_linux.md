# Linux setup guide

Linux systemd user-service support is implemented but untested in the current release process. Prefer manual `serve` testing first, and treat user-service helpers as best-effort until Linux smoke coverage exists.

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

Validate and start:

```bash
personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server serve --config ~/.personal-mcp-server/config/config.toml
```

User-level service:

- systemd user unit path: `~/.config/systemd/user/personal-mcp-server.service`
- Use `systemctl --user`, not system-wide root services.
- Server diagnostic log: `~/.personal-mcp-server/logs/server.log` when `[server_logging].path` is set.
- Linux service stdout/stderr normally goes to journald as fallback troubleshooting output; journald retention is managed by the OS.
