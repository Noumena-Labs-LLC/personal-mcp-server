# Architecture

personal-mcp-server is organized around small packages with narrow responsibilities.

## Packages

- `cmd/personal-mcp-server`: CLI commands plus focused files for runtime construction, tool registration, catalog/resources, config tools, diagnostics tools, and policy helpers.
- `internal/config`: TOML parsing, defaults, and fail-closed validation.
- `internal/fsx`: filesystem sandbox and filesystem/structured-data MCP tool handlers, split by read, delete, JSON, and JSONL concerns where practical.
- `internal/shell`: named command runner and command execution limits.
- `internal/mcphttp`: official MCP Go SDK wiring plus localhost safety middleware.
- `internal/audit`: bounded async JSON-lines audit logging and rotation.
- server diagnostics: stdlib `log/slog` setup in the CLI serve path, with optional bounded async rotating file output separate from audit logs.

## Request flow

```text
Claude Desktop or local MCP client
  -> localhost HTTP
  -> Host / Origin / bearer-token middleware
  -> MCP Go SDK Streamable HTTP handler
  -> registered tool handler
  -> sandbox / command runner
  -> audit event
```

## Safety boundaries

Filesystem safety lives in `internal/fsx/sandbox.go` and should not depend on prompt text or client behavior. Roots, file policy, secret deny rules, directory refusal for file-only tools, and overwrite protection remain hard boundaries. Write/delete/move tools intentionally avoid hash, expected-size, dry-run, and plan-hash gates so the local single-user workflow stays low-friction; read/search/output tools keep bounded output to protect model context and latency.

Command safety lives in `internal/shell/commands.go` and is based on named, allowlisted commands. Named commands run as direct argv by default and never accept arbitrary command arguments from the client. Trusted project commands may explicitly opt into `run_mode = "persistent_shell"` when global config allows it; even then, the server builds the shell command only from the configured argv plus validated `extra_args`.

Transport safety lives in `internal/mcphttp/server.go`: localhost-only Host validation, optional Origin validation, and bearer-token auth.

## Config reload

The HTTP server uses a small delegating handler backed by an atomically swapped runtime state. `serve` periodically hashes the TOML config file, attempts to build a complete new runtime from the edited file, and swaps the handler only after parsing and validation succeed. If the new TOML fails validation, the previous handler and configuration continue serving requests. Changes to `server.host` or `server.port` are rejected during reload because the listener address is fixed after startup. After a successful swap, the old runtime is closed so audit loggers, diagnostic writers, and persistent shell sessions do not leak across reloads.


<!-- v0.2.5 cwd note -->
## Per-call cwd

Most path-based tools accept an optional `cwd` argument. `cwd` is resolved inside configured roots and is used only as the base for that tool call. The server never calls `os.Chdir` and does not maintain hidden session working-directory state. Use `cwd` when one configured root contains multiple projects, for example `{ "cwd": "personal-mcp-server", "path": "internal/fsx/tools.go" }`.

## Logging visibility

Server diagnostic logs are configured with `[server_logging]` or overridden for one invocation with `serve --log-level`, `--log-file`, `--log-max-bytes`, and `--log-max-backups`. Diagnostics use Go `log/slog`; when a log file is configured, error-level diagnostics are also duplicated to stderr and info/debug diagnostics stay in the file. Diagnostic file writes use a bounded async writer that drains on close. Diagnostic logs are never written to stdout.

Audit logs can be configured with `[audit].path` or overridden with `--audit-log`. If no audit path is configured, audit events are written to stderr. Audit logs are bounded async JSON-lines security/action records and remain separate from server diagnostics.

## Feature/module organization

New capabilities should be added as small registration units rather than by
mixing protocol, policy, and tool logic together. The `mcphttp.Module` interface
allows future features to register tools, resources, and prompts as plug-in style
modules:

```go
type Module interface {
    Register(*mcphttp.Server)
}
```

A new feature should normally have:

- a focused package or file for its business logic;
- a small registration function/module that declares tool schemas;
- config toggles in `config.ToolConfig`;
- policy checks close to the operation;
- tests for root containment, limits, and deny/prompt behavior.

Core filesystem capabilities should stay Go-native and should not depend on
external `grep`, `find`, `sed`, `patch`, or `git` binaries.
