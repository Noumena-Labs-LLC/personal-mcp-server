# Installation and first-run setup

This guide covers local installation, first-run configuration, token setup, client checks, and optional user-service setup.

personal-mcp-server is intended to run as the current user, bound to `127.0.0.1` or `localhost`.

## Build and install from the repo

From a checked-out source tree:

```sh
just install-user
```

This installs the current binary into the user install location used by the project tooling.

## Create a starter config

Generate a starter config and token file:

```sh
personal-mcp-server init --root ~/code/my-project --generate-token
```

The generated config normally lives at:

```text
~/.personal-mcp-server/config/config.toml
```

Prefer token-file auth for GUI-launched clients:

```toml
[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
auth_token_file = "~/.personal-mcp-server/config/token"
validate_origin = true
allowed_origins = ["http://127.0.0.1", "http://localhost"]
```

Configs must include:

```toml
config_version = 1
```

## Validate the global config

Use the global config validator for the main server config:

```sh
personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
```

Use project-config commands only for checked-in per-repo manifests:

```sh
personal-mcp-server project validate --cwd .
personal-mcp-server project effective --cwd .
```

## Run manually

Start the server directly:

```sh
personal-mcp-server serve \
  --config ~/.personal-mcp-server/config/config.toml \
  --audit-log ~/.personal-mcp-server/state/audit.log
```

Health check:

```sh
curl http://127.0.0.1:3929/healthz
```

The built-in local MCP client expects global flags before the client subcommand:

```sh
personal-mcp-server client --config ~/.personal-mcp-server/config/config.toml ping
personal-mcp-server client --config ~/.personal-mcp-server/config/config.toml tools
```

## Low-friction local profile

For a trusted single-user setup that should avoid repeated prompts, use permissive defaults in the global config:

```toml
[defaults]
allow_everything = true

[approval]
enabled = false
timeout_seconds = 120
default_on_timeout = "allow"
remember_session_decisions = true

[file_policy]
read_default = "allow"
write_default = "allow"
create_default = "allow"
patch_default = "allow"
unified_patch_default = "allow"

[command_policy]
default = "allow"
```

Explicit config entries still win. If a tool or policy is explicitly set to `prompt`, `deny`, or `enabled = false`, that explicit setting remains in effect. Roots, secret deny rules, directory refusal, overwrite protection, and read/search/diff/output caps still apply.

Enable the mutation and command tools you actually want in the same global config, for example:

```toml
[tools.fs_apply_patch]
enabled = true
[tools.fs_edit_lines]
enabled = true
[tools.fs_apply_unified_patch]
enabled = true
[tools.fs_create_file]
enabled = true
[tools.fs_create_dir]
enabled = true
[tools.fs_replace_file]
enabled = true
[tools.fs_delete_file]
enabled = true
[tools.fs_delete_files]
enabled = true
[tools.fs_move_file]
enabled = true
[tools.fs_append_file]
enabled = true
[tools.fs_replace_regex]
enabled = true

[tools.cmd_run_named]
enabled = true
[tools.cmd_run_sequence]
enabled = true
[tools.cmd_run_argv]
enabled = true
```

## Install as a user service

For always-on local use, install a current-user service:

```sh
personal-mcp-server service paths
personal-mcp-server service install --user --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server service restart --user
personal-mcp-server service status --user
personal-mcp-server service doctor --user
personal-mcp-server service logs --user
```

See `docs/SERVICE.md` for macOS LaunchAgent and Linux systemd user-service details.

## Troubleshooting setup

- If the client cannot authenticate, confirm `auth_token_file` points to an existing readable token file with restrictive permissions.
- If a tool is missing, check the matching `[tools.<name>] enabled = true` entry.
- If a tool prompts unexpectedly, look for explicit `prompt` policy entries; explicit settings override permissive defaults.
- If a path is rejected, confirm it resolves inside a configured root and does not match secret deny rules.
