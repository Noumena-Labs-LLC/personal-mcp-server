# Approval CLI helpers

When a file or command policy returns `prompt`, the server creates a local pending approval. The HTTP endpoints remain available, and the CLI wraps those endpoints with the configured server address and bearer token. The server does not display a native macOS, Windows, Linux, or Claude Desktop dialog; keep `approvals watch` open or run `approvals list` from a terminal to see pending requests.

List pending approvals:

```bash
personal-mcp-server approvals list --config ~/.personal-mcp-server/config/config.toml
```

Watch pending approvals:

```bash
personal-mcp-server approvals watch --config ~/.personal-mcp-server/config/config.toml
```

Approve or deny a request:

```bash
personal-mcp-server approvals approve --config ~/.personal-mcp-server/config/config.toml approval-1
personal-mcp-server approvals deny --config ~/.personal-mcp-server/config/config.toml approval-1
```

The CLI only talks to the configured local server address (`localhost`, `127.0.0.1`, or `::1`). It refuses non-local server addresses, does not bypass policy, does not read unchecked project config, and does not approve anything automatically.


## Timeout and config

Approval timeout is configured in the global config, normally `~/.personal-mcp-server/config/config.toml`:

```toml
[approval]
enabled = true
timeout_seconds = 600
default_on_timeout = "deny"
remember_session_decisions = false
```

Project configs cannot lengthen approval timeouts or bypass the global approval policy.

## Disabling approval

When `approval.enabled = false`, commands whose policy action is `prompt` are **allowed automatically** — the approval gate is bypassed and the command runs without waiting for a decision. This is the intended behavior: disabling approval means no approval is required.

To disable approval globally:

```toml
[approval]
enabled = false
```

Command policy rules with `action = "prompt"` remain meaningful — they define which commands *would* require approval if approval were re-enabled. Explicit `deny` rules are unaffected and continue to block commands regardless of this setting.
