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
- `staticcheck`, optional locally but installed by the repo tooling when needed
- `golangci-lint`, optional locally but installed by the repo tooling when needed
- `govulncheck`, optional locally but installed by the repo tooling when needed

The lint targets install pinned developer tools into `.tools/bin` automatically when needed. You can also bootstrap them explicitly:

```sh
just tools
just lint-check
```

## Local workflow

Useful commands:

```sh
just fmt
just test
just test-race
just integration-test
just smoke-test
just vet
just staticcheck
just golangci-lint
just govulncheck
just lint-check
just ci
just build
```

Before proposing a change, run at least the relevant focused tests. Maintainers should run `just ci` before merging release-bound changes.

## Documentation style

- Use the current project name: `personal-mcp-server`.
- Use the current Go module path: `github.com/noumena-labs-llc/personal-mcp-server`.
- Prefer `snake_case` TOML keys in examples.
- Keep examples aligned with the low-friction trusted-local posture while still documenting hard boundaries such as roots, token auth, Host/Origin validation, file policy, secret-name deny rules, and command policy.
- Show `personal-mcp-server client --config CONFIG ...` with global flags before the client subcommand.

## Safety expectations

Do not propose changes that expose raw shell strings, remote binding, unbounded reads/searches, or bypasses for configured roots, secret-name deny rules, file policy, command policy, Host/Origin validation, or bearer-token authentication.

Filesystem mutation tools are intentionally low-friction for trusted single-user local workflows. Do not reintroduce hash gates, mandatory dry-run gates, expected-size gates, or plan-hash gates unless the maintainer explicitly asks for that product direction.

## Reporting security issues

Do not post secrets, exploit details, private local paths, credentials, private documents, or sensitive logs in public issues. See `SECURITY.md` for vulnerability reporting guidance.
