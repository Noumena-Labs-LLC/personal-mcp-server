# personal-mcp-server

Personal MCP server for trusted local filesystem, command, diagnostics, and structured-data workflows.

`personal-mcp-server` is a localhost-only Model Context Protocol server built around the official `github.com/modelcontextprotocol/go-sdk` Streamable HTTP transport. It is intended for single-user local workflows, especially Claude Desktop and other MCP clients that need more robust local tooling than stdio-only servers provide for larger request and result payloads.

## Why this exists

Many local MCP tools run over stdio. That works well for small calls, but it can become fragile when request or response payloads get large. Wrapping stdio servers with a proxy can help, but it does not fully solve the ergonomics and reliability problems.

This project is built from the ground up as a local, long-running Streamable HTTP MCP server. It emphasizes navigation-first tools, bounded reads/searches, diagnostics, audit logs, and explicit local configuration.

## Main use cases

- Local filesystem navigation and edits under configured roots
- Markdown section navigation and editing
- JSON and JSONL navigation, validation, search, and filtering
- Named command execution and command-policy workflows
- Per-project `.personal-mcp-server.toml` configs
- Local diagnostics, audit logs, and feedback records
- Claude Desktop and other trusted local MCP workflows

## Safety posture

The default posture is intentionally low-friction for a trusted single-user local machine. This is not a hardened sandbox, not a remote multi-user service, and not a security boundary for untrusted users, untrusted prompts, hostile local processes, or internet-facing use.

The project still keeps important local boundaries:

- Localhost-only binding
- Bearer-token authentication
- Host and Origin validation
- Configured filesystem roots
- File policy and project policy
- Secret-name deny rules
- Directory refusal for file-only tools
- Overwrite protection unless explicitly requested
- Bounded reads, searches, diffs, and command output
- Named-command and command-policy controls
- Audit logs and diagnostics

Filesystem mutation tools are optimized for low-friction local use. They do not require hashes, expected-size gates, mandatory dry runs, or plan hashes. Users who want stricter behavior can tighten tool settings, file policy, command policy, project policy, and approval settings.

Read [`DISCLAIMER.md`](DISCLAIMER.md), [`SECURITY.md`](SECURITY.md), and [`THREAT_MODEL.md`](THREAT_MODEL.md) before using the server on important systems.

## Requirements

This repo pins:

- Go module version: `go 1.26`
- Go toolchain: `go1.26.3`
- MCP Go SDK: `github.com/modelcontextprotocol/go-sdk v1.6.0`

## Quick start

```sh
just install-user
personal-mcp-server init --root ~/code/my-project --generate-token
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server serve \
  --config ~/.personal-mcp-server/config/config.toml \
  --audit-log ~/.personal-mcp-server/state/audit.log
```

Health check:

```sh
curl http://127.0.0.1:3929/healthz
```

List tools with the built-in local MCP client:

```sh
personal-mcp-server client --config ~/.personal-mcp-server/config/config.toml tools
```

Ping the running server:

```sh
personal-mcp-server client --config ~/.personal-mcp-server/config/config.toml ping
```

The `client` command expects global flags such as `--config`, `--url`, and `--token` before the client subcommand.

## Configuration

Copy and edit the example config, or use `init`:

```sh
mkdir -p ~/.personal-mcp-server/config
cp configs/example.toml ~/.personal-mcp-server/config/config.toml
$EDITOR ~/.personal-mcp-server/config/config.toml
```

Prefer a token file for GUI-launched clients:

```sh
mkdir -p ~/.personal-mcp-server/config
openssl rand -hex 32 > ~/.personal-mcp-server/config/token
chmod 600 ~/.personal-mcp-server/config/token
```

```toml
[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_file = "~/.personal-mcp-server/config/token"
validate_origin = true
allowed_origins = ["http://127.0.0.1", "http://localhost"]
```

Configs must include:

```toml
config_version = 1
```

Validate the global server config:

```sh
personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
```

Project configs are validated separately:

```sh
personal-mcp-server project validate --cwd .
personal-mcp-server project effective --cwd .
```

## Low-friction local profile

For a trusted single-user setup that should avoid repeated prompts, use permissive defaults in the global config:

```toml
[defaults]
allow_everything = true

[approval]
enabled = false
timeout_seconds = 120
default_on_timeout = "allow"
remember_session_decisions = true

[file_policy]
read_default = "allow"
write_default = "allow"
create_default = "allow"
patch_default = "allow"
unified_patch_default = "allow"

[command_policy]
default = "allow"
```

Explicit config entries still win. If a tool or policy is explicitly set to `prompt`, `deny`, or `enabled = false`, that explicit setting remains in effect. Roots, secret deny rules, directory refusal, overwrite protection, and read/search/diff/output caps still apply.

Enable the mutation and command tools you actually want in the same global config, for example:

```toml
[tools.fs_apply_patch]
enabled = true
[tools.fs_apply_unified_patch]
enabled = true
[tools.fs_create_file]
enabled = true
[tools.fs_create_dir]
enabled = true
[tools.fs_replace_file]
enabled = true
[tools.fs_delete_file]
enabled = true
[tools.fs_delete_files]
enabled = true
[tools.fs_move_file]
enabled = true
[tools.fs_append_file]
enabled = true
[tools.fs_replace_regex]
enabled = true

[tools.cmd_run_named]
enabled = true
[tools.cmd_run_sequence]
enabled = true
[tools.cmd_run_argv]
enabled = true
```

## Commands

```text
personal-mcp-server init [--config CONFIG] [--root ROOT] [--generate-token] [--token-file PATH] [--force]
personal-mcp-server doctor --config CONFIG
personal-mcp-server config validate --config CONFIG
personal-mcp-server client [--config CONFIG] [--url URL] [--token TOKEN] ping|tools|call|run-named|raw ...
personal-mcp-server project init|validate|trust|untrust|list|effective [--config CONFIG] [--cwd DIR]
personal-mcp-server approvals list|watch|approve|deny --config CONFIG [ID]
personal-mcp-server audit show|tail --config CONFIG [--last N] [--tool TOOL] [--decision DECISION] [--contains TEXT] [--format raw|pretty]
personal-mcp-server service paths|print-launchagent|print-systemd [--config CONFIG] [--binary BIN]
personal-mcp-server service install|uninstall|start|stop|restart|status|logs|doctor --user [--config CONFIG] [--binary BIN]
personal-mcp-server serve --config CONFIG [--audit-log PATH] [--log-level LEVEL] [--log-file PATH] [--log-max-bytes N] [--log-max-backups N] [--reload-interval DURATION]
personal-mcp-server version
```

## Tool overview

The tool surface is fixed in Go code. TOML can enable or disable tools and override descriptions, but it cannot invent arbitrary tools. MCP `tools/list` is flat; use `tool_catalog_categories` and `tool_catalog_category` for progressive discovery.

Major tool families include:

- Orientation and catalog tools: `server_info`, `tool_catalog_categories`, `tool_catalog_category`, `tool_catalog_all`
- Filesystem navigation: `fs_list_roots`, `fs_list_dir`, `fs_tree`, `fs_find`, `fs_get_file_info`, `fs_tail_file`, `fs_read_file`, `fs_search_text`
- Filesystem edits: `fs_apply_patch`, `fs_apply_unified_patch`, `fs_create_file`, `fs_create_dir`, `fs_replace_file`, `fs_delete_file`, `fs_delete_files`, `fs_move_file`, `fs_append_file`, `fs_replace_regex`
- Markdown tools: `md_outline`, `md_read_section`, `md_replace_section`, `md_replace_section_heading`, `md_insert_section`, `md_append_section`, `md_append_subsection`
- JSON and JSONL tools: `json_outline`, `json_keys`, `json_get`, `json_slice`, `json_search`, `json_validate`, `jsonl_info`, `jsonl_tail`, `jsonl_filter`, `jsonl_read`
- Git and verification: `git_status`, `git_diff`
- Commands and jobs: `cmd_list_named`, `cmd_run_named`, `cmd_run_sequence`, `cmd_run_argv`, `cmd_start_named`, `cmd_job_status`, `cmd_job_read`, `cmd_job_cancel`, `cmd_job_list`
- Policy/config/diagnostics: `policy_describe`, `file_explain_policy`, `cmd_explain_policy`, `config_validate`, `config_explain`, `diagnostics_recent_slow_tools`
- Resources and guides: `resource_list`, `resource_read`, `guide_list`, `guide_read`, `project_config_describe`, `setup_guide`
- Feedback: `feedback_submit`

See [`docs/TOOLS.md`](docs/TOOLS.md) for detailed tool guidance.

## Large file guidance

For large files, start with metadata and navigation tools:

1. `fs_get_file_info`
2. `fs_tail_file` for recent logs or append-heavy files
3. `fs_search_text` for targeted search
4. `fs_read_file` with `start_line` and `max_lines`

Whole-file reads require `whole_file=true` and still obey configured size limits. Project configs cannot raise global read/search/diff limits.

## Named commands

Commands are configured by name:

```toml
[[commands]]
name = "go-test"
exec = "go"
args = ["test", "./..."]
# Optional default cwd for calls that omit cwd. Tool-call cwd wins when supplied.
# cwd = "/Users/me/src/personal-mcp-server"
```

`cmd_run_named` can run configured commands without exposing raw shell strings. Direct argv execution is the default. Trusted project commands can opt into persistent shell mode only when global config allows it.

## Project configs

Repositories can check in `.personal-mcp-server.toml` manifests with project-specific commands, workflow aliases, search defaults, protected/generated file rules, and guidance. Project configs are discovered automatically but are not trusted by default; trust is stored outside the repository.

```sh
personal-mcp-server project init --cwd ~/RnD/my-project
personal-mcp-server project validate --cwd ~/RnD/my-project
personal-mcp-server project trust --cwd ~/RnD/my-project
personal-mcp-server project effective --cwd ~/RnD/my-project
```

See [`docs/PROJECT_CONFIGS.md`](docs/PROJECT_CONFIGS.md).

## User service setup

For always-on local use, install a user-level macOS LaunchAgent or Linux systemd user service:

```sh
personal-mcp-server service paths
personal-mcp-server service install --user --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server service restart --user
personal-mcp-server service status --user
personal-mcp-server service doctor --user
personal-mcp-server service logs --user
```

macOS LaunchAgent service operations have current manual smoke coverage. Linux systemd user-service support is implemented but should be treated as best-effort until real Linux smoke coverage exists.

See [`docs/SERVICE.md`](docs/SERVICE.md).

## Diagnostics and audit logs

Server diagnostics are configured with `[server_logging]` and can rotate as numbered backups. Audit logs are separate JSONL security/action records configured with `[audit]`.

```toml
[server_logging]
level = "info"
path = "~/.personal-mcp-server/logs/server.log"
max_bytes = 10485760
max_backups = 5
tool_slow_ms = 3000
tool_very_slow_ms = 10000

[audit]
path = "~/.personal-mcp-server/logs/audit.jsonl"
max_bytes = 10485760
max_backups = 5
```

## Development

Useful commands:

```sh
just fmt
just test
just test-race
just integration-test
just smoke-test
just vet
just staticcheck
just golangci-lint
just govulncheck
just lint-check
just ci
just build
just dist
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md), [`docs/QUALITY.md`](docs/QUALITY.md), and [`docs/RELEASE.md`](docs/RELEASE.md).

## License

MIT License. See [`LICENSE`](LICENSE).
