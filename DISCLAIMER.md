# Disclaimer

personal-mcp-server is intended for trusted, single-user, localhost-only workflows. It is not designed or represented as a hardened sandbox, a remote multi-user service, or a security boundary for untrusted users, untrusted models, untrusted prompts, or hostile local processes.

When configured to allow filesystem writes, deletes, moves, patches, or named commands, the server can modify local files and run local programs with the permissions of the user account that starts it. You are responsible for choosing appropriate roots, tool settings, command allowlists, backups, and review practices for your environment.

Do not expose the server to a network. Keep bearer tokens, config files, audit logs, and feedback logs private. Do not include secrets, credentials, private documents, or sensitive logs in feedback or issue reports.

The project provides local safety controls such as localhost binding, bearer-token authentication, Host and Origin validation, configured filesystem roots, file policy, secret-name deny rules, command policy, and bounded read/search outputs. These controls reduce accidental misuse, but they do not make the server safe to use with untrusted local access or as an internet-facing service.

The software is provided under the license terms in `LICENSE`, without warranty. Review the license, documentation, and configuration before using it on important files or systems.
