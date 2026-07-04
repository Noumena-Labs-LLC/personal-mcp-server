# Security docs summary

Do not expose personal MCP server beyond localhost. Keep bearer tokens private. Do not allow project configs to expand roots or weaken global hard-deny rules.

Use global config for machine-level permission and project config for repo workflow guidance. Global deny rules always win.

Never include auth tokens, SSH keys, `.env` files, or private config contents in bug reports.
