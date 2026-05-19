# Troubleshooting guide

Start with:

```bash
personal-mcp-server version
personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
```

Common issues:

- Claude Desktop does not show tools: restart Claude Desktop after config/tool changes.
- Unauthorized: check bearer token and `Authorization: Bearer ...` header.
- Forbidden host/origin: keep Host and Origin on localhost/127.0.0.1 unless explicitly configured.
- File denied: call `file_explain_policy` and inspect `personal-mcp://policy`.
- Command denied: call `cmd_explain_policy` and inspect `cmd_list_named`.
- Large read timeouts: use `fs_get_file_info`, `fs_tail_file`, `fs_search_text`, and bounded `fs_read_file` line windows.
- Project commands missing: call `project_info` and verify the project config is discovered and trusted.

For bug reports, include version, OS, config summary with secrets redacted, the relevant tool call, and a short audit tail if safe to share.

## Project commands still missing after trust

Call `project_info` with the same `cwd` you plan to use for `cmd_list_named` or `cmd_run_named`. The server refreshes project trust state for project-aware tools, so a restart should not normally be required. Verify that `[project_configs]` is enabled, the trust store path is correct, and the trusted root matches the project path under the configured root.

## Command works in my terminal but fails through MCP

Named commands run in argv mode by default, using the personal-mcp-server server process environment rather than your current interactive shell. This avoids shell injection and improves reproducibility, but it can miss pyenv/asdf/nvm/direnv setup, virtualenv activation, aliases, shell functions, and PATH changes from `.zshrc` or `.bashrc`.

Prefer making the command explicit, for example `uv run pytest` or `.venv/bin/python -m pytest`. If the command genuinely requires shell startup behavior, configure the trusted project command with `run_mode = "persistent_shell"` and enable `[command_environment].allow_persistent_shell = true` globally.
