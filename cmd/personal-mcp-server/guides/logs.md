# Logs and rotation guide

personal MCP server has three output channels: server diagnostics, audit logs, and service-manager stdout/stderr fallback logs.

## Server diagnostic logs

Server diagnostics use Go's standard `log/slog` logger. Configure them in global config:

```toml
[server_logging]
level = "info" # debug, info, warn, or error
path = "~/.personal-mcp-server/logs/server.log"
max_bytes = 10485760
max_backups = 5
tool_slow_ms = 3000
tool_very_slow_ms = 10000
```

Tool calls at or above `tool_slow_ms` produce `tool_call_slow` diagnostics. Calls at or above `tool_very_slow_ms` produce `tool_call_very_slow` diagnostics. Use these records to identify slow reads, named commands, searches, JSONL filters, or other tool calls on overloaded machines. Call `diagnostics_recent_slow_tools` for a bounded recent view when `server_logging.path` is configured.

When `path` is set, diagnostics go to a bounded async file writer and rotate as numbered backups. Normal diagnostic writes are kept off the MCP request path; shutdown and config reload close the writer and drain queued records before closing the file:


```text
server.log
server.log.1
server.log.2
server.log.3
```

Error-level diagnostics are also duplicated to stderr. Info/debug diagnostics stay in the file. If no diagnostic log file is configured, diagnostics go to stderr.

`serve` flags can override the config for one invocation:

```bash
personal-mcp-server serve \
  --config ~/.personal-mcp-server/config/config.toml \
  --log-level debug \
  --log-file ~/.personal-mcp-server/logs/server.log \
  --log-max-bytes 10485760 \
  --log-max-backups 5
```

For a LaunchAgent or systemd user service, prefer putting normal logging settings in `config.toml`. The service plist/unit usually only needs `serve --config ...` unless you want a service-specific override.

## Audit logs

Audit logs are structured JSON-lines security/action logs. Configure them separately from server diagnostics:

```toml
[audit]
path = "~/.personal-mcp-server/logs/audit.jsonl"
max_bytes = 10485760
max_backups = 5
```

Audit rotation is controlled by `[audit].max_bytes` and `[audit].max_backups` and is independent from server diagnostic rotation. Audit writes use a bounded async queue and drain on close, so normal tool completion is not blocked on audit file I/O.

## Service stdout/stderr logs

Service stdout/stderr logs are fallback troubleshooting logs.

- Linux user services usually write stdout/stderr to journald; journald retention is managed by the OS.
- macOS LaunchAgents can write stdout/stderr under `~/.personal-mcp-server/logs/` through `StandardOutPath` and `StandardErrorPath`.
- Keep these paths as fallbacks for startup failures before file logging is initialized.

Do not put auth tokens or secrets in logs. Diagnostic logs are never written to stdout, so CLI output remains pipe-friendly.

## Atomic config writes

Generated config, token, project config, trust store, and user service files are written with temp-file-and-rename semantics so interrupted writes are less likely to leave partial TOML or service files behind.
