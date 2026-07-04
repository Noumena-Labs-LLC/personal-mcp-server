# Client testing checklist

Use this checklist when testing a real MCP client.

## Startup

- `personal-mcp-server doctor --config <config>` passes.
- `/healthz` returns `{"ok":true}`.
- Wrong bearer token is rejected.
- Wrong `Host` is rejected.
- Unexpected browser `Origin` is rejected when origin validation is enabled.

## MCP methods

Verify:

- `initialize`
- `tools/list`
- `tools/call` for each enabled tool
- `prompts/list`
- `prompts/get` for each enabled prompt

## Filesystem tools

Verify:

- reads are bounded
- secret-looking files are rejected
- paths outside roots are rejected
- symlink escapes are rejected
- patch dry-runs return diffs without writing
- writes remain disabled until explicitly enabled

## Command tools

Verify:

- only configured command names run
- cwd outside roots is rejected
- stdout/stderr caps are enforced
- timeouts terminate command trees
- audit events are written

## Built-in MCP CLI client

For manual testing outside Claude Desktop, prefer the built-in Go client against a running server. It talks to the configured Streamable HTTP MCP endpoint over the localhost HTTP socket; it does not use stdio transport.

Start the server in one terminal. For local GUI-style testing, prefer a token file in the config directory:

```sh
mkdir -p ~/.personal-mcp-server/config
printf '%s\n' dev-token > ~/.personal-mcp-server/config/token
personal-mcp-server serve --config ~/.personal-mcp-server/config/config.toml
```

Run read-only discovery checks in another terminal. The client reads `--token` first, then `server.auth_token_file`, then `token` in the config directory, then `server.auth_token_env` as a compatibility fallback:

```sh
personal-mcp-server client ping
personal-mcp-server client tools
```

You can still point it at an example config and pass an explicit token for disposable test setups:

```sh
personal-mcp-server client --config ./configs/example.toml --token dev-token tools
```

Useful targeted checks:

```sh
# Call one read-only tool.
personal-mcp-server client call server_info '{}'

# Inspect configured named commands without running them.
personal-mcp-server client call cmd_list_named '{"include_args":true}'

# Manually run a trusted named project command. This can execute local code.
personal-mcp-server client run-named test --cwd /path/to/project

# Send a raw MCP method.
personal-mcp-server client raw prompts/list '{}'
```

`client run-named` calls `cmd_run_named` on the running server and can execute local code. Use it only when you intentionally want to run a configured project command.
