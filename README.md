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

## Getting started

Installation, first-run config, token setup, client checks, and service setup are documented in [`docs/INSTALL.md`](docs/INSTALL.md).

The most common first-run flow is:

```sh
just install-user
personal-mcp-server init --root ~/code/my-project --generate-token
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server client --config ~/.personal-mcp-server/config/config.toml ping
```

The `client` command expects global flags such as `--config`, `--url`, and `--token` before the client subcommand.

Upgrade instructions are in [`docs/UPGRADING.md`](docs/UPGRADING.md).

## Low-friction local profile

For a trusted single-user setup that should avoid repeated prompts, use permissive defaults in the global config. See [`docs/INSTALL.md`](docs/INSTALL.md) for the full config example and caveats.

Explicit config entries still win. If a tool or policy is explicitly set to `prompt`, `deny`, or `enabled = false`, that explicit setting remains in effect. Roots, secret deny rules, directory refusal, overwrite protection, and read/search/diff/output caps still apply.

## Tool overview

The tool surface is fixed in Go code. TOML can enable or disable tools and override descriptions, but it cannot invent arbitrary tools. MCP `tools/list` is flat; use `tool_catalog_categories` and `tool_catalog_category` for progressive discovery.

Major tool families include:

- Orientation and catalog tools
- Filesystem navigation and mutation tools
- Markdown section tools
- JSON and JSONL navigation tools
- Git inspection and verification tools
- Named commands, command policy, and server-supervised jobs
- Policy, config, diagnostics, resource, guide, and feedback tools

See [`docs/TOOLS.md`](docs/TOOLS.md) for detailed tool guidance.

## Project configs

Repositories can check in `.personal-mcp-server.toml` manifests with project-specific commands, workflow aliases, search defaults, protected/generated file rules, and guidance. Project configs are discovered automatically but are not trusted by default; trust is stored outside the repository.

See [`docs/PROJECT_CONFIGS.md`](docs/PROJECT_CONFIGS.md).

## User service setup

For always-on local use, personal-mcp-server can install a user-level macOS LaunchAgent or Linux systemd user service. See [`docs/SERVICE.md`](docs/SERVICE.md).

macOS LaunchAgent service operations have current manual smoke coverage. Linux systemd user-service support is implemented but should be treated as best-effort until real Linux smoke coverage exists.

## Diagnostics and audit logs

Server diagnostics are configured with `[server_logging]` and can rotate as numbered backups. Audit logs are separate JSONL security/action records configured with `[audit]`.

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
