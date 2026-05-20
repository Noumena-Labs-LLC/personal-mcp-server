# Contributing

Thanks for improving personal-mcp-server. This project intentionally keeps the tool surface focused because it can read files, modify files, and run named commands on a local machine.

## Contributions

Issues, bug reports, and feature requests are welcome.

Pull request creation is limited to collaborators. If you would like to propose a code or documentation change, please open an issue first with the problem, motivation, and suggested approach. A maintainer may then choose to make the change or invite collaboration.

This policy keeps the project scope manageable while still allowing public discussion and feedback through issues.

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
