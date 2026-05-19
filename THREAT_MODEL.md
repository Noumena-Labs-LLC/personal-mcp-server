# Threat model

personal-mcp-server assumes Claude or another MCP client may receive malicious instructions from project files, web pages, pasted text, or other untrusted content. Protocol handling is delegated to the official MCP Go SDK; project-specific security controls remain in this repo. The server therefore treats prompts and tool descriptions as guidance only, not as enforcement.

## Assets protected

- Files outside configured roots
- Secret-looking files inside configured roots
- The user's shell and environment
- Unrelated local processes
- Network exposure outside localhost

## Main controls

- Official MCP Go SDK Streamable HTTP transport
- Localhost-only bind host validation
- Bearer-token authentication
- Host and Origin validation
- Filesystem root sandboxing with symlink resolution
- Secret filename and extension deny rules
- Bounded file reads and searches
- Patch-based writes with exact-match counts and atomic replacement
- New-file creation only when explicitly enabled; overwrites are refused
- Purpose-built bounded git diff tool for inspection
- Named commands only, no shell interpolation
- Stripped command environment and output/time caps
- Rotating audit log for tool calls

## Non-goals

- Remote access
- Multi-user authorization
- General terminal emulation
- Background job or PID management
- Browser automation or URL fetching


## Token handling

The server supports either `auth_token_env` or `auth_token_file`. Environment variables are convenient for shells, while token files are often easier for GUI-launched clients. Tokens are never logged intentionally, and `doctor` warns when token-file permissions are broad on Unix-like systems.

## Configuration versioning

Configs must include `config_version = 1`. Missing or unsupported versions fail closed so future config migrations do not silently weaken the security posture.

## Config hot reload

Hot reload is fail-closed. The server builds a complete replacement runtime from the new TOML before swapping it into service. Invalid TOML, failed validation, missing token material, invalid roots, or command validation failures are logged and rejected while the previous valid runtime remains active. Host and port changes require a restart and are rejected during hot reload.


<!-- v0.2.5 cwd note -->
## Per-call cwd

Most path-based tools accept an optional `cwd` argument. `cwd` is resolved inside configured roots and is used only as the base for that tool call. The server never calls `os.Chdir` and does not maintain hidden session working-directory state. Use `cwd` when one configured root contains multiple projects, for example `{ "cwd": "personal-mcp-server", "path": "internal/fsx/tools.go" }`.

## Audit visibility

Audit logs can be configured with `[audit].path` or overridden with `--audit-log`. If no audit path is configured, audit events are written to stderr.


## Working directory state

The server does not expose a process-wide `pwd` and does not maintain hidden session cwd state. `cwd` is per-call, sandbox-resolved, and checked against configured roots to avoid cross-request confusion and concurrency bugs.
