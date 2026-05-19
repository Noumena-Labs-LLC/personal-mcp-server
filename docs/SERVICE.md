# User service setup

personal-mcp-server is intended to run as the current user, bound to `127.0.0.1`. It should not require system/root privileges.

Service management commands are explicit user-service commands. They require `--user` so the command cannot be mistaken for a system-wide install. Manifest rendering is driven by an embedded declarative service spec resource; the YAML describes service identity, process args, paths, restart policy, and backend selection while Go owns the actual launchctl/systemctl operations.


Inspect the resolved paths first:

```bash
personal-mcp-server service paths
```

This prints the user root, binary, config, token, trust store, state/log directories, macOS LaunchAgent path, and Linux systemd user-unit path.

## macOS LaunchAgent

Install the current user's LaunchAgent:

```bash
personal-mcp-server service install --user \
  --config /Users/you/.personal-mcp-server/config/config.toml
```

This writes:

```text
~/Library/LaunchAgents/com.noumenalabs.personal-mcp-server.plist
```

It copies the current binary to the resolved user-root binary path, requires the config file to already exist, and creates log output under:

```text
~/.personal-mcp-server/logs/
```

Manage the service:

```bash
personal-mcp-server service start --user
personal-mcp-server service stop --user
personal-mcp-server service restart --user
personal-mcp-server service status --user
personal-mcp-server service logs --user
personal-mcp-server service uninstall --user
```

You can still print the LaunchAgent without installing it:

```bash
personal-mcp-server service print-launchagent \
  --config /Users/you/.personal-mcp-server/config/config.toml
```

## Linux systemd user unit

Linux systemd user-service support is implemented but untested in the current release process. Treat it as best-effort until it has real Linux smoke coverage. macOS LaunchAgent service operations are the primary tested service path today.

Install the current user's systemd unit:

```bash
personal-mcp-server service install --user \
  --config /home/you/.personal-mcp-server/config/config.toml
```

This writes:

```text
~/.config/systemd/user/personal-mcp-server.service
```

Manage the service:

```bash
personal-mcp-server service start --user
personal-mcp-server service stop --user
personal-mcp-server service restart --user
personal-mcp-server service status --user
personal-mcp-server service logs --user
personal-mcp-server service uninstall --user
```

You can still print the systemd unit without installing it:

```bash
personal-mcp-server service print-systemd \
  --config /home/you/.personal-mcp-server/config/config.toml
```

## Safety notes

- Service install/uninstall/start/stop/restart/status/logs are user-only helpers.
- They do not require `sudo` and do not install a system-wide daemon.
- The server still binds to the configured local address, normally `127.0.0.1`.
- The service uses the same bearer-token auth, host/origin validation, roots, policies, and audit settings as a manually started server.

## LLM setup guides

LLMs can guide users through service setup by reading `personal-mcp://guide/setup`, `personal-mcp://guide/setup-macos`, `personal-mcp://guide/setup-linux`, `personal-mcp://guide/services`, and `personal-mcp://guide/logs` through `resource_read`.

Audit logs rotate according to `[audit].max_bytes` and `[audit].max_backups`. Server diagnostic logs are configured separately with `[server_logging]` and can rotate as numbered backups. Service stdout/stderr logs are fallback troubleshooting logs. Linux user services normally use journald retention; macOS LaunchAgent stdout/stderr paths are written under `~/.personal-mcp-server/logs/`.


## Server diagnostic logging with services

Prefer configuring server diagnostics in the main config file instead of hard-coding log flags into the service manifest:

```toml
[server_logging]
level = "info"
path = "~/.personal-mcp-server/logs/server.log"
max_bytes = 10485760
max_backups = 5
```

If the LaunchAgent or systemd user unit already starts `personal-mcp-server serve --config /absolute/path/config.toml`, it does not need additional logging flags. Keep LaunchAgent `StandardOutPath`/`StandardErrorPath` or systemd/journald output as fallback supervisor logs for startup errors before config loading or file logging succeeds. Error-level diagnostics are duplicated to stderr when file logging is enabled; info/debug diagnostics stay in the server log file.


## Local artifact upgrades

Use `personal-mcp-server upgrade local` to upgrade from a versioned source tarball that already exists on disk:

```sh
personal-mcp-server upgrade local ./personal-mcp-server-v0.5.2.tar.gz
```

When an adjacent `.sha256` file exists, it is verified automatically. To provide a specific checksum file:

```sh
personal-mcp-server upgrade local --sha256 ./personal-mcp-server-v0.5.2.tar.gz.sha256 ./personal-mcp-server-v0.5.2.tar.gz
```

The command verifies the checksum when available, validates the artifact module/version metadata, builds the binary from the source artifact, and installs it under `$PERSONAL_MCP_ROOT/bin` with rollback support. If a user-service manifest is present, the service is restarted after the binary is replaced. Use `--dry-run` to verify, inspect, and build without replacing the installed binary. Use `--no-restart-service` to skip the restart.

This command is intentionally local-only. It does not download releases or silently update the server.



### Service doctor

Run `personal-mcp-server service doctor --user` to validate the user-root service layout, config file, config loading, token permissions, installed binary version, log/state directories, platform service manager availability, and manifest references before troubleshooting service startup.
