# Threat model

personal-mcp-server assumes Claude or another MCP client may receive malicious instructions from project files, web pages, pasted text, logs, or other untrusted content. Protocol handling is delegated to the official MCP Go SDK; project-specific security controls remain in this repo. The server treats prompts, tool descriptions, and embedded guides as guidance only, not as enforcement.

## Intended use

personal-mcp-server is intended for trusted single-user local workflows on a machine controlled by the user. It is not intended for remote access, multi-user authorization, hostile local users, or internet-facing deployment.

## Assets protected

- Files outside configured roots
- Secret-looking files inside configured roots
- The user's shell and environment
- Unrelated local processes
- Network exposure outside localhost
- Bearer tokens, config files, audit logs, and feedback logs

## Main controls

- Official MCP Go SDK Streamable HTTP transport
- Localhost-only bind host validation
- Bearer-token authentication
- Host and Origin validation
- Filesystem root sandboxing with symlink resolution
- File policy and trusted-project policy
- Secret filename and extension deny rules
- Directory refusal for file-only tools
- Overwrite protection unless explicitly requested
- Bounded file reads, searches, diffs, JSON/JSONL output, and command output
- Policy-checked file mutation tools for patch, unified patch, create, create-dir, replace, append, regex replace, delete, bulk delete, and move
- Named commands and command-policy evaluation
- No raw shell strings from tool callers
- Stripped command environment and output/time caps for argv commands
- Optional persistent shell mode only for configured trusted commands when globally enabled
- Rotating audit logs and diagnostic logs

## Low-friction local tradeoff

Filesystem mutation tools intentionally avoid hash gates, expected-size gates, mandatory dry-run gates, and plan-hash gates. That keeps trusted local workflows usable, but it means users should choose roots, tool settings, file policy, command policy, project policy, backups, and audit practices that match their risk tolerance.

Users who prefer a stricter posture can disable mutation tools, require approvals, use narrower roots, configure deny/prompt file-policy rules, restrict command-policy rules, and avoid persistent shell mode.

## Non-goals

- Hardened sandboxing
- Remote access
- Multi-user authorization
- Protection from hostile local users or malware already running as the same user
- General terminal emulation
- Raw shell-string execution from tool callers
- Browser automation or URL fetching
- Direct arbitrary PID/process-management tools
- Complete data-loss prevention for secrets or private documents

## Token handling

The server supports either `auth_token_env` or `auth_token_file`. Environment variables are convenient for shells, while token files are often easier for GUI-launched clients. Tokens are never logged intentionally, and `doctor` warns when token-file permissions are broad on Unix-like systems.

## Configuration versioning

Configs must include `config_version = 1`. Missing or unsupported versions fail closed so future config migrations do not silently weaken the configured posture.

## Config hot reload

Hot reload is fail-closed. The server builds a complete replacement runtime from the new TOML before swapping it into service. Invalid TOML, failed validation, missing token material, invalid roots, or command validation failures are logged and rejected while the previous valid runtime remains active. Host and port changes require a restart and are rejected during hot reload.

## Per-call cwd

Most path-based tools accept an optional `cwd` argument. `cwd` is resolved inside configured roots and is used only as the base for that tool call. The server never calls `os.Chdir` and does not maintain hidden session working-directory state. Use `cwd` when one configured root contains multiple projects, for example `{ "cwd": "personal-mcp-server", "path": "internal/fsx/tools.go" }`.

## Audit visibility

Audit logs can be configured with `[audit].path` or overridden with `--audit-log`. If no audit path is configured, audit events are written to stderr.

## Working directory state

The server does not expose a process-wide `pwd` and does not maintain hidden session cwd state. `cwd` is per-call, sandbox-resolved, and checked against configured roots to avoid cross-request confusion and concurrency bugs.
