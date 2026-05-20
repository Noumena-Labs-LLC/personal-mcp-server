# personal-mcp-server

A localhost-only MCP server for controlled local filesystem access and named command execution. It uses the official `github.com/modelcontextprotocol/go-sdk` Streamable HTTP server transport.

## Why this exists

personal-mcp-server was built for Claude Desktop users who want local filesystem, structured-data, and command workflows without relying on stdio-only MCP servers.

The common alternatives, such as desktop command tools and filesystem servers, usually run over stdio. That works for small requests, but it can become fragile when request or response payloads get large. Wrapping stdio servers with a proxy can help, but it does not fully address the underlying ergonomics and reliability problems.

This project is built from the ground up around Streamable HTTP MCP. It is meant to be a local, long-running, localhost-only server that handles larger local workflows more predictably, exposes clearer diagnostics, and gives models navigation-first tools instead of encouraging whole-file reads.

The main use case is trusted personal/local automation:

- local filesystem navigation and edits under configured roots
- Markdown section navigation and editing
- JSON and JSONL navigation, validation, search, and filtering
- named command execution without exposing raw shell strings
- per-project/repo `.personal-mcp-server.toml` configs
- local diagnostics, audit logs, and feedback records

The default posture is intentionally low-friction for a single-user local machine. Security controls still exist — localhost binding, bearer-token auth, Host and Origin validation, roots, file policy, secret-name deny rules, command policy, and bounded read/search outputs — and users can tighten the policy when they want a more restrictive setup.

Design goals:

- Bind only to `127.0.0.1` / localhost
- Require bearer-token auth
- Validate `Host` and `Origin`
- Restrict all filesystem operations to configured roots
- Deny secret-looking files
- Prefer patch-based edits over whole-file writes
- Run only named, allowlisted commands
- Never expose raw shell, OS PID management, shell-managed jobs, or remote binding
- Use TOML for human-editable, commented configuration

## Go and MCP SDK versions

This repo pins the Go module to `go 1.26` with `toolchain go1.26.3`. It also pins the official MCP Go SDK at `github.com/modelcontextprotocol/go-sdk v1.6.0`.

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
personal-mcp-server client tools
```

The client discovers the token from `--token`, `server.auth_token_file`, the config-directory `token` file, or `server.auth_token_env`. You can also make a raw MCP request with curl:

```sh
curl -sS http://127.0.0.1:3929/mcp \
  -H "Authorization: Bearer $PERSONAL_MCP_TOKEN" \
  -H "Accept: application/json, text/event-stream" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
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
personal-mcp-server service install|uninstall|start|stop|restart|status|logs --user [--config CONFIG] [--binary BIN]
personal-mcp-server serve --config CONFIG [--audit-log PATH] [--log-level LEVEL] [--log-file PATH] [--log-max-bytes N] [--log-max-backups N] [--reload-interval DURATION]
personal-mcp-server version
```

### `init`

Generates a safe starter TOML config. The generated config defaults to read-oriented tools, enables `git_diff`, and keeps `fs_apply_patch`, `fs_create_file`, `fs_create_dir`, and `cmd_run_named` off until you choose to enable them. Use `--generate-token` to create a `0600` token file and configure `auth_token_file`, which is friendlier for GUI-launched clients than shell environment variables.

### `doctor`

Checks that the config parses, `config_version` is supported, an auth token is available through env or token file, token-file permissions are reasonable, roots exist, localhost binding is configured, and named command executables are available on `PATH`.

### `config validate`

Parses and validates the TOML config without starting the HTTP server. This is useful for CI, editor hooks, and setup checks.


### `client`

Talks to a running local personal-mcp-server server over the configured Streamable HTTP MCP endpoint. This is useful for debugging without Claude Desktop or another GUI client.

```sh
personal-mcp-server client ping
personal-mcp-server client tools
personal-mcp-server client call server_info '{}'
personal-mcp-server client call cmd_list_named '{"include_args":true}'
personal-mcp-server client run-named test --cwd /path/to/project
personal-mcp-server client raw prompts/list '{}'
```

`client run-named` calls `cmd_run_named` on the running server and can execute local code when the named command is enabled and allowed.

### `serve`

Starts the localhost Streamable HTTP MCP server using the official MCP Go SDK. The project still wraps the SDK handler with its own localhost-only auth, Host, and Origin middleware. Diagnostic logging normally comes from `[server_logging]` in config; `--log-level`, `--log-file`, `--log-max-bytes`, and `--log-max-backups` are one-run overrides.

## Configure

Copy and edit the example config:

```sh
mkdir -p ~/.personal-mcp-server/config
cp configs/example.toml ~/.personal-mcp-server/config/config.toml
$EDITOR ~/.personal-mcp-server/config/config.toml
```

Use either a long random token in the environment:

```sh
export PERSONAL_MCP_TOKEN="$(openssl rand -hex 32)"
```

or a token file:

```toml
[server]
auth_token_file = "~/.personal-mcp-server/config/token"
```

The server refuses to start if no auth token is available, `config_version` is missing or unsupported, no roots are configured, a root does not exist, or the bind host is not localhost.

TOML examples and docs use `snake_case` keys such as `allow_extra_args`, `max_extra_args`, and `allow_everything`. The config loader also accepts common model-generated CamelCase/PascalCase aliases such as `AllowExtraArgs` or `[Defaults] AllowEverything`; when both forms are present, the documented `snake_case` key wins.

## Global permissive defaults

Single-user local configs can opt into permissive missing defaults:

```toml
[defaults]
allow_everything = true
```

When enabled, absent built-in tool entries default enabled, approval defaults disabled, and missing command/file policy defaults become `allow`. Explicit config entries still win: a tool with `enabled = false` stays disabled, and an explicit policy default such as `write_default = "deny"` stays denied. Roots, limits, secret deny rules, and tool-specific guards still apply.

## Configuration versioning

Configs must include:

```toml
config_version = 1
```

The server fails closed on missing or unsupported config versions so future config changes can be handled explicitly.

## MCP transport

The server uses the official MCP Go SDK Streamable HTTP handler in stateless JSON-response mode, then wraps it with local safety middleware. This avoids maintaining a custom JSON-RPC/MCP implementation while preserving the project-specific security controls.

## Tools

The tool surface is fixed in Go code. TOML can enable/disable tools and override descriptions, but it cannot invent arbitrary new tools. MCP `tools/list` is flat; call `tool_catalog_categories` then `tool_catalog_category` for progressive workflow discovery, or `tool_catalog_all` for a full dump.

Filesystem mutation tools are optimized for low-friction single-user local workflows: roots, file policy, secret deny rules, directory refusal, overwrite protection, and bounded read/search/diff outputs remain, but delete/move/replace/write tools do not require hashes, expected sizes, dry-run plans, or plan hashes.

- `tool_catalog_categories` / `tool_catalog_category` — progressive read-only catalog discovery; `tool_catalog_all` and `tool_catalog` return the full catalog.
- `fs_list_roots` — list configured roots.
- `fs_list_dir` — bounded directory listing inside roots.
- `fs_get_file_info` — metadata only, no file contents.
- `fs_tail_file` — recent lines from logs or large text files without scanning from the beginning.
- `server_info` — server version, module path, transport, feature summary, and large-file guidance.
- `fs_read_file` — bounded line-range read for text files.
- `fs_search_text` — bounded text search, plain substring by default with regex opt-in and offset pagination.
- `fs_apply_patch` — exact old/new replacement with optional `expected_replacements` replacement cap, dry-run support, compact unified diff output, warnings when found counts differ, atomic writes, and optional backup.
- `fs_apply_unified_patch` — feature-gated standard unified diff patch application inside configured roots without requiring Git or external patch tools; rejects deletes, renames, binary patches, and paths outside roots.
- `fs_create_file` — create a new text file only; refuses overwrites.
- `fs_replace_file` — replace one existing text file with a compact diff and optional backup.
- `fs_delete_file` — delete one existing file; refuses directories.
- `fs_delete_files` — bulk file deletion with root/policy checks and directory refusal.
- `fs_move_file` — move or rename one existing file; refuses directories and overwrites by default.
- `fs_append_file` — append text with policy checks, dry-run diffs, atomic writes, and optional create-if-missing.
- `md_outline` / `md_read_section` — navigate large Markdown files by section instead of reading the whole file.
- `md_replace_section` / `md_replace_section_heading` / `md_insert_section` / `md_append_section` / `md_append_subsection` — make structure-aware Markdown edits with dry-run support.
- `git_diff` — purpose-built bounded git diff tool, safer than using a generic command for diff inspection.
- `cmd_run_named` — synchronous named command execution; no raw shell, arbitrary args, PIDs, or shell job control.
- `cmd_run_sequence` — run a configured sequence of named commands in order, using `stop_on_failure` or `continue` mode instead of raw `&&` / `;` shell chaining.
- `cmd_start_named` / `cmd_job_*` — server-supervised background jobs for named commands.
- `diagnostics_recent_slow_tools` — recent slow-tool diagnostic records from the configured server log.
- `config_validate` / `config_explain` — validate TOML config and summarize effective defaults, limits, and policies.

Recommended first-run posture:

```toml
[tools.fs_apply_patch]
enabled = false
[tools.fs_create_file]
enabled = false
[tools.fs_create_dir]
enabled = false

[tools.fs_replace_file]
enabled = false
[tools.fs_delete_file]
enabled = false
[tools.fs_delete_files]
enabled = false
[tools.fs_move_file]
enabled = false
[tools.fs_append_file]
enabled = false
[tools.md_outline]
enabled = true
[tools.md_read_section]
enabled = true
[tools.md_replace_section]
enabled = false
[tools.md_replace_section_heading]
enabled = false
[tools.md_insert_section]
enabled = false
[tools.md_append_section]
enabled = false
[tools.md_append_subsection]
enabled = false
[tools.cmd_run_named]
enabled = false
[tools.git_diff]
enabled = true
```

Turn on patching and named commands only after the roots and command list are scoped to a project you trust.

## Prompts

The server can expose optional MCP prompts from TOML, such as:

- `safe_code_edit`
- `inspect_project`

Prompts are workflow guidance only. Security enforcement lives in Go code: path sandboxing, secret-deny checks, command allowlists, auth, and localhost binding.

## Command safety

Commands are configured by name:

```toml
[[commands]]
name = "go-test"
exec = "go"
args = ["test", "./..."]
# Optional default cwd for calls that omit cwd. Tool-call cwd wins when supplied.
# cwd = "/Users/me/src/personal-mcp-server"
```

The client can call `cmd_run_named` with `{ "name": "go-test" }` when the command has a configured `cwd`, or pass `cwd` explicitly in the tool call. It cannot provide raw shell or arbitrary extra arguments.

The runner:

- strips the environment by default
- sets a conservative `PATH`
- allows fixed env values through `commands.env`
- allows selected host env vars through `env_from_host`
- uses tool-call `cwd` when supplied, otherwise uses command-level `cwd` when configured
- enforces a timeout
- kills the process group on timeout on Unix-like systems
- caps stdout/stderr

## Server diagnostic logging and audit logging

personal-mcp-server keeps diagnostic logs separate from audit logs.

Server diagnostics use Go's standard `log/slog` logger. Configure them in TOML:

```toml
[server_logging]
level = "info" # debug, info, warn, or error
path = "~/.personal-mcp-server/logs/server.log"
max_bytes = 10485760
max_backups = 5
tool_slow_ms = 3000
tool_very_slow_ms = 10000
```

Tool calls that meet or exceed `tool_slow_ms` are written to diagnostics as `tool_call_slow`; calls that meet or exceed `tool_very_slow_ms` are written as `tool_call_very_slow`. Records include the tool name, duration, threshold, success/error state, and request/response byte counts so slow `fs_read_file`, `cmd_run_named`, and other calls can be diagnosed before changing behavior.

When `server_logging.path` is set, diagnostic logs go to that file and rotate as numbered backups:

```text
server.log
server.log.1
server.log.2
server.log.3
```

Error-level diagnostics are also duplicated to stderr so service managers and terminals still show important failures. Info/debug diagnostics stay in the file. If no diagnostic log file is configured, diagnostics go to stderr. Diagnostic logs are never written to stdout, so CLI output remains pipe-friendly.

You can override the TOML settings for one server invocation:

```sh
personal-mcp-server serve \
  --config ~/.personal-mcp-server/config/config.toml \
  --log-level debug \
  --log-file ~/.personal-mcp-server/logs/server.log \
  --log-max-bytes 10485760 \
  --log-max-backups 5
```

For a LaunchAgent or systemd user service, the service file normally only needs `serve --config ...` when the config contains `[server_logging]`. Keep `StandardOutPath`/`StandardErrorPath` or journald as fallback supervisor logs for startup failures before file logging is initialized.

Audit logs are JSON-lines security/action records. Configure them separately:

```toml
[audit]
path = "~/.personal-mcp-server/logs/audit.jsonl"
max_bytes = 10485760
max_backups = 5
```

Passing `--audit-log PATH` overrides `[audit].path`. Without an audit path, audit events go to stderr. Audit rotation is independent from server diagnostic rotation.

## Hot reload

`serve` polls the TOML config and reloads it without restarting the process:

```sh
./bin/personal-mcp-server serve --config ~/.personal-mcp-server/config/config.toml
```

The default interval is 5 seconds. Set it explicitly or disable reloads:

```sh
./bin/personal-mcp-server serve --config ~/.personal-mcp-server/config/config.toml --reload-interval 2s
./bin/personal-mcp-server serve --config ~/.personal-mcp-server/config/config.toml --reload-interval 0
```

If the edited TOML fails parsing or validation, the server logs an error and keeps the previous valid configuration. Changes to `server.host` or `server.port` require a restart because the listening socket cannot move safely during hot reload.

## Tool and prompt guides

See [`docs/TOOLS.md`](docs/TOOLS.md) for each tool, argument shape, and recommended usage. See [`docs/PROMPTS.md`](docs/PROMPTS.md) for clear prompts to use in Claude Desktop.


### Local artifact upgrades

`personal-mcp-server upgrade local` upgrades from a local source release artifact. It does not download releases and it does not perform silent upgrades.

```sh
personal-mcp-server upgrade local ./personal-mcp-server-v0.5.2.tar.gz
```

If `./personal-mcp-server-v0.5.2.tar.gz.sha256` exists, the checksum is verified automatically. You can also pass an explicit checksum file:

```sh
personal-mcp-server upgrade local --sha256 ./personal-mcp-server-v0.5.2.tar.gz.sha256 ./personal-mcp-server-v0.5.2.tar.gz
```

The upgrade command verifies the checksum when available, validates the extracted module/version metadata, builds `./cmd/personal-mcp-server`, replaces `$PERSONAL_MCP_ROOT/bin/personal-mcp-server` with rollback support, and restarts an installed user service unless `--no-restart-service` is set. Use `--dry-run` to verify, inspect, and build the artifact without replacing the installed binary.

## Release artifacts and distribution

The release artifact is a buildable source snapshot, not a git checkout. It intentionally excludes `.git/`, repo-local tool caches, generated binaries, coverage outputs, Python caches, and OS metadata. Each package handoff should include both files:

```text
personal-mcp-server-vX.Y.Z[-rcN].tar.gz
personal-mcp-server-vX.Y.Z[-rcN].tar.gz.sha256
```

A private development repository is compatible with public source-snapshot releases. If publishing publicly while keeping development history private, use a release-only repository or object-storage path that contains a minimal README, changelog, license/security notes, and the versioned artifacts. Before calling an artifact clean, run `just ci`, verify the checksum, install or upgrade from the packaged artifact, and confirm `personal-mcp-server client ping` reports the current server version after service restart.

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

Equivalent plain Go commands:

```sh
gofmt -w ./cmd ./internal
go test ./...
go test -race ./...
go test ./cmd/personal-mcp-server -run 'Integration'
go test ./cmd/personal-mcp-server -run 'Smoke'
go vet ./...
staticcheck ./...
golangci-lint run
govulncheck ./...
go build -trimpath -ldflags="-s -w" -o bin/personal-mcp-server ./cmd/personal-mcp-server
```

See `CONTRIBUTING.md`, `docs/QUALITY.md`, and `docs/RELEASE.md` for contributor workflow, linting posture, and release steps.


## CI

The repo includes `.github/workflows/ci.yml`, which runs:

- `gofmt` check
- `go vet ./...`
- `go test -race ./...`
- standalone Staticcheck
- golangci-lint using `.golangci.yml`
- govulncheck
- binary build
- versioned tarball packaging and checksum generation

## Tooling files

The repo includes:

- `.golangci.yml` — focused golangci-lint configuration.
- `.editorconfig` — editor formatting defaults.
- `.gitattributes` — LF line endings and text normalization.
- `.markdownlint.json` — lightweight Markdown lint preferences.
- `.pre-commit-config.yaml` — optional local hooks.
- `.github/dependabot.yml.disabled` — disabled Dependabot config kept for later open-source maintenance; rename to `.github/dependabot.yml` to re-enable monthly grouped updates.
- `.github/pull_request_template.md` and issue templates.

## Testing focus

The test suite covers:

- path traversal rejection
- symlink escape and symlink-chain rejection
- secret-name denial
- large-file and binary-file read rejection
- patch dry-runs and writes
- create-file overwrite and secret-file rejection
- search result caps
- command cwd sandboxing
- command output caps and timeouts
- HTTP auth and Host/Origin validation
- SDK-backed MCP initialize, tools, and prompts behavior
- real runtime MCP HTTP integration with temp roots and config
- subprocess smoke coverage for version and server health/tool calls
- built-in local MCP client testing through `personal-mcp-server client`
- canonical root matching without OS-specific path assumptions
- config version, duplicate command, and token-file validation

## Threat model

This server is meant for a single-user local coding workflow. It is not a remote multi-user service.

It intentionally does not include:

- raw shell
- remote binding
- file delete/move/chmod/chown
- network fetch
- process/PID management
- package installation tools

Localhost HTTP is still protected with bearer auth plus Host/Origin checks to reduce risk from unwanted browser-originated local requests.

See `THREAT_MODEL.md` for more detail.


### Developer tool bootstrap

The lint targets install pinned developer tools into `.tools/bin` automatically when needed. You can also bootstrap them explicitly:

```sh
just tools
just lint-check
```

This avoids requiring global `staticcheck`, `golangci-lint`, or `govulncheck` installs.

## Versioned artifacts

Release artifacts should be named `personal-mcp-server-v<version>.tar.gz`, and the version should be increased for each new tarball.

## Dynamic policy commands and resources

`v0.2.0` adds policy-based dynamic commands via `cmd_run_argv`. The tool accepts an executable and argv array, never a shell string. The request is evaluated against `[command_policy]` rules in TOML. Deny rules win, allow rules run immediately, prompt rules create a local pending approval, and the default applies last.

The server also exposes `server_info` and `policy_describe` so the model can discover server version/features, effective roots, enabled tools, file policy, command policy, and approval behavior before acting. For clients that cannot access MCP resources directly, `resource_list` and `resource_read` mirror the same resource content through tools.

Read-only MCP resources are available for clients that support resources:

- `personal-mcp://roots`
- `personal-mcp://policy`
- `personal-mcp://guide/tools`
- `personal-mcp://file/{path}`
- `personal-mcp://tree/{path}`
- `personal-mcp://info/{path}`

Tool-only clients can use `resource_list` to discover those URIs and `resource_read` with `{"uri":"personal-mcp://policy"}` or another personal-mcp URI to read them.

Prompt-required operations appear at:

```bash
curl -H "Authorization: Bearer $PERSONAL_MCP_TOKEN" http://127.0.0.1:3929/approvals
```

The CLI can use the configured server address and bearer token for the same local endpoints. The server does not show a native OS or Claude Desktop dialog, so keep `approvals watch` open or run `approvals list` from a terminal. See `docs/APPROVALS.md` for details:

```bash
personal-mcp-server approvals list --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server approvals watch --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server approvals approve --config ~/.personal-mcp-server/config/config.toml approval-1
personal-mcp-server approvals deny --config ~/.personal-mcp-server/config/config.toml approval-1
```

Approve or deny with curl if you prefer direct HTTP:

```bash
curl -X POST -H "Authorization: Bearer $PERSONAL_MCP_TOKEN" http://127.0.0.1:3929/approvals/approval-1/approve
curl -X POST -H "Authorization: Bearer $PERSONAL_MCP_TOKEN" http://127.0.0.1:3929/approvals/approval-1/deny
```

Example command policy:

```toml
[tools.cmd_run_argv]
enabled = true

[command_policy]
default = "prompt"

[[command_policy.rules]]
name = "allow git read-only"
action = "allow"
exec = "git"
subcommands = ["status", "diff", "log", "show", "branch"]

[[command_policy.rules]]
name = "prompt other git commands"
action = "prompt"
exec = "git"
args_regex = ".*"
```


<!-- v0.2.5 cwd note -->
## Per-call cwd

Most path-based tools accept an optional `cwd` argument. `cwd` is resolved inside configured roots and is used only as the base for that tool call. The server never calls `os.Chdir` and does not maintain hidden session working-directory state. Use `cwd` when one configured root contains multiple projects, for example `{ "cwd": "personal-mcp-server", "path": "internal/fsx/tools.go" }`.

## Audit visibility

Audit logs can be configured with `[audit].path` or overridden with `--audit-log`. If no audit path is configured, audit events are written to stderr.


Note: `fs_apply_patch` treats `expected_replacements` as optional and defaults it to `1` when omitted. It caps how many exact matches are replaced, returns warnings when the found count differs, and rejects only zero-match edits.


## Large file guidance

For large files, first use `fs_get_file_info`, `fs_tail_file`, and `fs_search_text`; use `fs_find` as an additional discovery tool only when `server_info.features.native_find` is true. Then prefer `fs_tail_file` for recent log records or bounded `fs_read_file` calls with `start_line` and `max_lines`. Full-file reads require `whole_file=true` and still obey size limits. Raise `[limits].max_read_bytes` only in the global config, normally `~/.personal-mcp-server/config/config.toml`; project configs cannot raise global safety limits. Patch diffs are compact and capped by `max_diff_bytes`.

### Go-native discovery and replacement tools

personal-mcp-server includes Go-native equivalents for common shell workflows so the
core server does not depend on external `find`, `grep`, or `sed` binaries:

- `fs_find` discovers files and directories with bounded glob filters and offset pagination when `server_info.features.native_find` is true.
- `fs_search_text` performs literal or regex text search with optional context.
- `fs_replace_regex` performs sed-like regex replacements with optional `dry_run`,
  line ranges, replacement caps, and compact diffs.
- `cmd_explain_policy` and `file_explain_policy` let the model check policy
  decisions before attempting an operation.

New features should be added as small modules/registration units rather than by
expanding a monolithic server function. See `docs/ARCHITECTURE.md` for the
module guidance.

## Project configs

Repositories can check in a `.personal-mcp-server.toml` manifest with project-specific named commands, workflow aliases, search defaults, and guidance. Project configs are discovered automatically but are not trusted by default; trust is stored outside the repository. See `docs/PROJECT_CONFIGS.md`.

```bash
personal-mcp-server project init --cwd ~/RnD/my-project
personal-mcp-server project validate --cwd ~/RnD/my-project
personal-mcp-server project trust --cwd ~/RnD/my-project
personal-mcp-server project effective --cwd ~/RnD/my-project
```

## User service setup

For always-on local use, personal-mcp-server can install and manage user-level service files for macOS LaunchAgents and Linux systemd user services. These run as the current user and do not require root. See `docs/SERVICE.md`.

macOS LaunchAgent service operations have current manual smoke coverage. Linux systemd user-service support is implemented but untested in the current release process, so treat it as best-effort until real Linux smoke coverage exists.

```bash
personal-mcp-server service paths
personal-mcp-server service install --user --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server service restart --user
personal-mcp-server service status --user
personal-mcp-server service logs --user
personal-mcp-server service doctor --user
personal-mcp-server service stop --user
personal-mcp-server service uninstall --user
```

Service install copies the current binary into the configured user-root binary path and requires an existing config file. Service manifests are rendered from an embedded declarative service spec. Use `service paths` to inspect the resolved user-root, binary, config, token, log, and OS manifest paths. `service status --user` prints those resolved paths with service-manager state, process ID when available, and a manifest reference check. `service doctor --user` also validates config loading, token permissions, installed binary version, and manifest references. The `print-launchagent` and `print-systemd` helpers are still available when you want to inspect the generated service file manually.

### v0.3.13 native integration and smoke tests

`v0.3.13` keeps native integration/smoke tests as the supported workflow and does not reintroduce Docker. The tests use temporary roots, configs, tokens, trust stores, and audit logs and do not touch user config or packaged install paths. Subprocess smoke tests use interrupt-first cleanup, kill fallback, and wait exactly once for helper processes.

### v0.3.11 integration and smoke coverage

`v0.3.11` added MCP HTTP integration tests and subprocess smoke tests using temporary roots, configs, tokens, and audit logs.

### v0.3.10 coverage expansion

`v0.3.10` is a test coverage release before the v0.4.0 project polish milestone. It adds focused tests for the approval manager and HTTP handler, MCP module registration, and policy decision helpers without adding feature scope.

### v0.3.9 audit UX polish

- `audit show` and `audit tail` support `--tool`, `--decision`, and `--contains` filters.
- `audit show` and `audit tail` support `--format pretty` for compact summaries.
- `audit tail --follow` can keep watching the configured audit log for new matching entries.

### v0.3.8 CI stabilization

This release focuses on getting CI clean after `v0.3.7` by fixing the remaining project file-policy `go vet` failure. No feature scope was added.

### v0.3.7 large-output hardening

This release adds offset pagination for `fs_find` and `fs_search_text`. When `truncated=true`, call the same tool again with `offset` set to `next_offset` to continue without requesting a larger response.

### v0.3.6 stabilization

This release fixes the CI/lint findings reported after `v0.3.5`, including approval response-body close handling, local-only approval CLI address validation, LaunchAgent log path handling, and remaining gocritic/gosec/errcheck/nilerr issues.

### v0.3.5 approval CLI helpers

This release adds `personal-mcp-server approvals list`, `watch`, `approve`, and `deny` helpers. They talk to the already-running local server over the existing `/approvals` endpoints using the configured bearer token, so approval decisions stay local. It also tightens service-file permissions and fixes the lint findings reported after `v0.3.4`.

### v0.3.4 service install commands

This release adds user-only service install, uninstall, start, stop, and status commands for macOS LaunchAgents and Linux systemd user units. It also fixes the lint findings reported after `v0.3.3`.

### v0.3.3 project policy hardening

This release enforces trusted project `protected_files` and `generated` edit rules across create, patch, unified patch, and regex replacement tools. `project_info` now surfaces guide, workflow, protected-file, and generated-file summaries, and `file_explain_policy` shows global, project, and effective file-policy decisions.

### v0.3.2 ergonomics

This release added `fs_tree` for compact project orientation, `workflow_list` for checked-in project workflow aliases, and `audit show` / `audit tail` helpers for inspecting audit logs when `[audit].path` is configured. Project configs may also include `guide.read_first` files to suggest important files for agents to inspect first.


### LLM-readable setup and configuration guides

The server exposes embedded markdown guidance as MCP resources so an LLM can help a user configure and operate personal MCP server without relying on external docs. Start with:

- `personal-mcp://guide/index`
- `personal-mcp://guide/tools`
- `personal-mcp://guide/project-config`
- `personal-mcp://guide/setup`
- `personal-mcp://guide/setup-macos`
- `personal-mcp://guide/setup-linux`
- `personal-mcp://guide/claude-desktop`
- `personal-mcp://guide/logs`

For clients that do not expose MCP resources directly, use the mirrored tools:

- `tool_catalog_categories`
- `tool_catalog_category`
- `tool_catalog_all`
- `tool_catalog`
- `resource_list`
- `resource_read`
- `project_config_describe`
- `setup_guide`

Server diagnostic logs are configured with `[server_logging]` and can rotate as `server.log`, `server.log.1`, and so on. Audit logs remain separate JSON-lines security/action records and rotate according to `[audit].max_bytes` and `[audit].max_backups`. Service stdout/stderr logs or journald are fallback troubleshooting logs, especially for startup failures before file logging is initialized.

### v0.4.0

- Added `personal-mcp-server client` as a built-in local MCP CLI client.
- `client ping`, `client tools`, `client call`, `client run-named`, and `client raw` talk to a running server over the configured localhost Streamable HTTP endpoint.
- The client discovers bearer tokens from `--token`, `server.auth_token_file`, the config-directory `token` file, or `server.auth_token_env`.

### v0.3.22

- Added opt-in persistent shell mode for trusted project named commands.
- Direct argv remains the default; persistent shell mode must be enabled globally and per command.
- `cmd_list_named` now avoids showing an active `max_extra_args` when `allow_extra_args` is false.
- Documentation now explains why commands may differ from an interactive terminal shell and when to opt into persistent shell mode.
- Persistent shell PTY output is read incrementally so commands that print progress without trailing newlines still return captured stdout on timeout.
- Persistent shell startup and completion markers are framed to avoid false matches from echoed PTY input.

## v0.3.21

- `just ci` now explicitly runs native integration and smoke tests.
- Added consolidated audit notes in `docs/AUDIT.md` for code quality, security, and documentation posture.

### v0.3.20

- Quality cleanup: guide and docs resource catalogs now use fresh-returning helpers instead of package-level mutable slices.

### v0.3.19

- Expanded test coverage for guide tools, Markdown section tools, git_status, command discovery, and extra_args handling.
- Added integration checks for LLM-visible discovery tools.

### v0.3.18

- Reuses Markdown section parsing for embedded LLM guides.
- `guide_list`, `guide_read`, `project_config_describe`, and `setup_guide` include section outlines.
- `guide_read` can return a specific section by id or heading title.

### v0.3.17

- Added `fs_append_file` for policy-checked appends with dry-run diffs and atomic writes.
- Added Markdown section tools: `md_outline`, `md_read_section`, `md_replace_section`, `md_insert_section`, and `md_append_section`. v0.5.6 extends them with `md_replace_section_heading` and `md_append_subsection`.

### v0.3.16

This release mirrors LLM-readable resources through tools for MCP clients that do not expose resources to the model. Use `guide_list` to discover embedded setup/project/docs guidance and `guide_read` to read a guide such as `project-config`, `setup-macos`, `claude-desktop`, `logs`, or `docs/readme`. Safe read-only discovery tools such as `policy_describe`, `project_info`, `workflow_list`, `resource_list`, and `resource_read` are registered by default so agents can orient themselves before acting.

It also adds `git_status` for structured working-tree status, including untracked files when requested, and ensures `cmd_list_named` is available for command discovery before using `cmd_run_named`.

### v0.3.15

This release refreshes project trust state for project-aware MCP tools so newly trusted projects become visible without a server restart. It also adds a shared atomic file writer and uses it for trust store, generated config/token, project config init, and user service file writes.


## Structured data navigation

`v0.5.7` includes read-only JSON and JSONL navigation tools with richer filtering. Use `json_outline`, `json_keys`, `json_get`, `json_slice`, and `json_search` to explore JSON by structure and RFC 6901 JSON Pointer rather than reading whole files; `json_search` can narrow with `type_filter` and `pointer_prefix`. Use `jsonl_info`, `jsonl_tail`, `jsonl_filter`, and `jsonl_read` to inspect logs and audit-style JSONL files with bounded output; `jsonl_filter` supports nested dotted fields and numeric ranges. JSON and JSONL editing are intentionally out of scope.

`feedback_submit` can append concise, local-only tool and workflow feedback to a configured JSONL file.
