# User-level service guide

Use service helpers only after manual `serve` works.

Principles:

- User-level only.
- No sudo.
- No system-wide service install.
- Server remains bound to `127.0.0.1`.

macOS:

- LaunchAgent file: `~/Library/LaunchAgents/com.noumenalabs.personal-mcp-server.plist`
- Path helper: `personal-mcp-server service paths`
- Print helper: `personal-mcp-server service print-launchagent --config CONFIG`
- Install helper: `personal-mcp-server service install --user --config CONFIG`
- Restart helper: `personal-mcp-server service restart --user`
- Status helper: `personal-mcp-server service status --user`
- Logs helper: `personal-mcp-server service logs --user`

Linux:

- Linux systemd user-service support is implemented but untested in the current release process; treat it as best-effort until real Linux smoke coverage exists.
- systemd user unit: `~/.config/systemd/user/personal-mcp-server.service`
- Path helper: `personal-mcp-server service paths`
- Print helper: `personal-mcp-server service print-systemd --config CONFIG`
- Install helper: `personal-mcp-server service install --user --config CONFIG`
- Restart helper: `personal-mcp-server service restart --user`
- Status helper: `personal-mcp-server service status --user`
- Logs helper: `personal-mcp-server service logs --user`

Server diagnostic logging should normally be configured in `config.toml` with `[server_logging]`. If the service already starts `personal-mcp-server serve --config CONFIG`, you do not need to add log flags to the plist or unit file. Keep service stdout/stderr as fallback logs for startup failures.

Service install copies the current binary into the user-root binary path and requires an existing config file. Service manifests are rendered from an embedded declarative service spec resource. The YAML describes the desired service; Go remains responsible for validated platform operations. `service status --user` prints resolved service paths, manager state, PID when available, and a manifest reference check.


## Local upgrades

Use a local, versioned source artifact for upgrades:

```sh
personal-mcp-server upgrade local ./personal-mcp-server-v0.5.2.tar.gz
```

The command verifies an adjacent `.sha256` file when present, validates the artifact module/version metadata, builds the binary from the source artifact, installs it under `$PERSONAL_MCP_ROOT/bin` with rollback support, and restarts an installed user service unless `--no-restart-service` is set. Use `--dry-run` to verify, inspect, and build without replacing the installed binary. It does not auto-download releases.



### Service doctor

Run `personal-mcp-server service doctor --user` to validate the user-root service layout, config file, config loading, token permissions, installed binary version, log/state directories, platform service manager availability, and manifest references before troubleshooting service startup.
