# Contributing

Thanks for improving personal-mcp-server. This project intentionally keeps the tool surface small because it can read files and run named commands on a local machine.

## Development setup

Required:

- Go version from `go.mod`
- `just`, optional but recommended
- `staticcheck`, optional locally but required in CI
- `golangci-lint`, optional locally but required in CI
- `govulncheck`, optional locally but required in CI

Install local tools:

```sh
go install honnef.co/go/tools/cmd/staticcheck@2026.1
go install golang.org/x/vuln/cmd/govulncheck@latest
```

Install golangci-lint using the upstream installer, Homebrew, or your package manager. CI pins golangci-lint to the version in `.github/workflows/ci.yml`.

## Before opening a PR

Run:

```sh
just ci
```

Or without `just`:

```sh
test -z "$(gofmt -l ./cmd ./internal)"
go vet ./...
go test -race ./...
staticcheck ./...
golangci-lint run
govulncheck ./...
```

## Security-sensitive changes

Treat these areas as security-sensitive:

- `internal/fsx/sandbox.go`
- `internal/shell/commands.go`
- `internal/mcphttp/server.go`
- `internal/config/config.go`

Changes to filesystem access, command execution, auth, Host/Origin validation, config validation, or MCP tool registration should include tests and an update to `THREAT_MODEL.md` when behavior changes.

## Tool-surface rule

Do not add broad tools casually. New tools should be:

- scoped to configured roots
- bounded by size/time limits
- auditable
- disabled by default if they write, execute, delete, move, install, or access the network

The project intentionally does not provide raw shell, PID management, remote binding, network fetch, package installation, or delete/move/chmod/chown tools.
