# Upgrading

personal-mcp-server upgrades are local and explicit. The project does not silently download releases or update itself in the background.

## Local source artifact upgrades

Use `personal-mcp-server upgrade local` to upgrade from a versioned source tarball that already exists on disk:

```sh
personal-mcp-server upgrade local ./personal-mcp-server-vX.Y.Z.tar.gz
```

When an adjacent `.sha256` file exists, it is verified automatically. To provide a specific checksum file:

```sh
personal-mcp-server upgrade local \
  --sha256 ./personal-mcp-server-vX.Y.Z.tar.gz.sha256 \
  ./personal-mcp-server-vX.Y.Z.tar.gz
```

The command verifies the checksum when available, validates the artifact module/version metadata, builds the binary from the source artifact, and installs it under `$PERSONAL_MCP_ROOT/bin` with rollback support.

If a user-service manifest is present, the service is restarted after the binary is replaced. Use `--no-restart-service` to skip the restart.

## Dry run

Use `--dry-run` to verify, inspect, and build without replacing the installed binary:

```sh
personal-mcp-server upgrade local \
  --dry-run \
  --sha256 ./personal-mcp-server-vX.Y.Z.tar.gz.sha256 \
  ./personal-mcp-server-vX.Y.Z.tar.gz
```

## Verify after upgrade

Restart the service if needed:

```sh
personal-mcp-server service restart --user
```

Then check the running server with the global config:

```sh
personal-mcp-server client --config ~/.personal-mcp-server/config/config.toml ping
```

Confirm the reported server version matches the expected release.

## Config compatibility

Configs must include:

```toml
config_version = 1
```

Validate global config after an upgrade:

```sh
personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml
personal-mcp-server doctor --config ~/.personal-mcp-server/config/config.toml
```

Validate project configs separately:

```sh
personal-mcp-server project validate --cwd .
personal-mcp-server project effective --cwd .
```
