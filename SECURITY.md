# Security Policy

## Supported versions

This project is pre-1.0. Only the latest released tarball is considered supported.

## Reporting a vulnerability

Do not post secrets, exploit details, or private local paths in a public issue. Report privately to the project maintainer if this repository is published. Include:

- affected version
- operating system
- relevant config, with tokens and private paths redacted
- steps to reproduce
- expected versus actual behavior

## Intended safety boundary

`personal-mcp-server` is a single-user localhost MCP server for controlled local coding workflows. It is not a remote multi-user service.

The intended hard boundaries are:

- localhost-only binding
- bearer-token authentication
- Host and Origin validation
- configured filesystem roots only
- denied secret-looking files
- bounded reads/searches/writes/output
- no raw shell
- named commands only
- no PID/process-management tools
- no network-fetch tools

## Known limitations

- A user who configures a broad root, such as their home directory, expands the blast radius.
- A named command can still do anything that command is designed to do inside the user account permissions.
- Prompt guidance is not a security boundary. Enforcement must happen in code.
- Localhost HTTP can be targeted by malicious websites, so auth plus Host/Origin checks remain required.
- TOCTOU-style filesystem races are reduced but not fully eliminated.

## Safe-use recommendations

- Use project-specific roots, not your whole home directory.
- Prefer `auth_token_file` for GUI-launched clients.
- Keep `fs_apply_patch`, `fs_create_file`, and `cmd_run_named` disabled until you need them.
- Configure only commands you would run manually in that project.
- Review audit logs when using write or command tools.
