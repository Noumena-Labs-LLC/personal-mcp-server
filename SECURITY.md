# Security Policy

## Supported versions

This project is pre-1.0. Only the latest released version is considered supported.

## Reporting a vulnerability

Do not post secrets, exploit details, private local paths, credentials, private documents, or sensitive logs in a public issue. Use [GitHub's private vulnerability reporting](https://github.com/Noumena-Labs-LLC/personal-mcp-server/security/advisories/new) to report privately. Include:

- affected version
- operating system
- relevant config, with tokens and private paths redacted
- steps to reproduce
- expected versus actual behavior

## Intended safety boundary

`personal-mcp-server` is a trusted, single-user, localhost MCP server for local coding and structured-data workflows. It is not a hardened sandbox, a remote multi-user service, or a security boundary for untrusted users, untrusted models, untrusted prompts, hostile local processes, or internet-facing use.

The intended hard boundaries are:

- localhost-only binding
- bearer-token authentication
- Host and Origin validation
- configured filesystem roots only
- file policy and project policy
- denied secret-looking files
- directory refusal for file-only tools
- overwrite protection unless explicitly requested
- bounded reads, searches, diffs, and command output
- named commands and command-policy evaluation
- no raw shell strings from tool callers
- no direct PID/process-management tools
- no network-fetch tools

Filesystem mutation tools are intentionally low-friction for trusted single-user local workflows. They can write, patch, append, replace, delete, bulk-delete, move, and create files inside configured roots when enabled and allowed by policy. They do not require hash gates, expected-size gates, mandatory dry-run gates, or plan hashes.

## Known limitations

- A user who configures a broad root, such as their home directory, expands the blast radius.
- A named command can still do anything that command is designed to do within the user account's permissions.
- A command-policy allow rule can permit powerful local programs if configured too broadly.
- Prompt guidance is not a security boundary. Enforcement must happen in code.
- Localhost HTTP can be targeted by malicious websites, so auth plus Host/Origin checks remain required.
- TOCTOU-style filesystem races are reduced but not fully eliminated.
- Secret-name deny rules reduce accidental access to obvious secrets, but they are not a complete data-loss-prevention system.

## Safe-use recommendations

- Keep the server bound to `127.0.0.1` or `localhost`.
- Keep bearer-token auth enabled; prefer `auth_token_file` for GUI-launched clients.
- Use project-specific roots instead of your whole home directory.
- Enable only the mutation and command tools you actually want.
- Configure only commands you would run manually in that project.
- Use command-policy and file-policy defaults that match your risk tolerance.
- Review audit logs when using write, delete, move, or command tools.
- Do not include secrets, credentials, private documents, or sensitive logs in feedback or public issue reports.
