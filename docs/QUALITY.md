# Quality gates

The project uses multiple overlapping checks because the server has access to local files and named commands.

## Required CI checks

- `gofmt` check
- `go vet ./...`
- `go test -race ./...`
- native integration tests
- native smoke tests
- standalone Staticcheck
- golangci-lint
- govulncheck
- release tarball checksum generation

## Local commands

```sh
just fmt
just test
just test-race
just integration-test
just smoke-test
just stress-test
just vet
just staticcheck
just golangci-lint
just govulncheck
just coverage
just coverage-profile
just coverage-html
just ci
```


## Integration and smoke tests

`just integration-test` runs MCP HTTP integration tests against the real runtime handler with temporary roots, config, token, and audit files. It covers initialize, tool listing, selected filesystem tool calls, and auth/Host/Origin checks.

`just smoke-test` runs subprocess smoke checks for CLI/server startup paths without using real user config. The smoke helper writes only to temporary config/root/audit/trust-store paths and cleans up the helper server with interrupt-first shutdown, kill fallback, and a single wait path.

`just stress-test` runs a separate opt-in stress tier. It reuses the subprocess helper path, but it adds repeated startup/shutdown, concurrent MCP traffic, background job churn, and persistent-shell contention to surface timeout and race failures. It is intentionally excluded from the default CI path.

The integration and smoke tests are native Go tests. They create temporary config files, roots, audit logs, trust stores, and token state for each run; they do not read or write the user's real `~/.personal-mcp-server/config` files.

## Linter posture

`.golangci.yml` intentionally enables a focused set of correctness, security, maintainability, and style linters. It is not meant to chase every possible stylistic preference.

The `gosec` `G204` check is excluded because this project intentionally runs configured command executables. The safety boundary for command execution is implemented by named command configuration, config validation, no shell expansion, cwd sandboxing, timeouts, output caps, and audit logging.

## False positives

Prefer fixing findings over suppressing them. When a suppression is necessary, keep it narrow and explain why the code is safe.


## Local tool bootstrap

Run `just tools` to install pinned developer tools into `.tools/bin`. The lint and vulnerability-check targets auto-bootstrap these tools if they are missing.


## Audit workflow

`just ci` is the required local quality gate and now explicitly runs native integration and smoke tests in addition to race-enabled package tests. The integration, smoke, and stress tests are also available as standalone targets for focused debugging, with stress kept out of default CI.

The current code quality, security, and documentation audit notes are recorded in `docs/AUDIT.md`. Update that document when audit posture changes materially.
