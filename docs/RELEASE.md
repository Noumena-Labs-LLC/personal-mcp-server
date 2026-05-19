# Release process

Release artifacts are source snapshots, not git checkouts. They should build on a clean machine with Go `1.26.3`, `go.mod`, and `go.sum` present. Do not include `.git/`, local tool caches, generated binaries, coverage outputs, Python caches, or OS metadata in release tarballs.

## Versioning

Each feature, config, layout, documentation, or discovery change gets a new release version such as `v0.4.9`. Use `-rcN` only for bug, lint, CI, test, or packaging fixes inside that same release line.

Examples:

```text
v0.4.8      feature/docs/tool-discovery change
v0.4.8-rc1  bug fix in that line
v0.4.8-rc2  CI/test fix in that line
v0.4.9      next docs/release pass
v0.5.1      stabilization/responsiveness pass
v0.5.2      supervised background named-command jobs
```

Commit subjects should start with the exact artifact version, for example:

```text
v0.5.2: Add supervised background named-command jobs
```

## Required files

Every handoff must include:

```text
personal-mcp-server-vX.Y.Z[-rcN].tar.gz
personal-mcp-server-vX.Y.Z[-rcN].tar.gz.sha256
```

The tarball must include at least:

```text
go.mod
go.sum
cmd/
internal/
configs/
docs/
README.md
CHANGELOG.md
VERSION
justfile
```

The tarball must not include:

```text
.git/
.tools/
bin/
dist/
build/
coverage.out
coverage.html
__pycache__/
*.pyc
.DS_Store
```

## Local release checklist

1. Update `VERSION`.
2. Update the CLI/server version constant in `cmd/personal-mcp-server/main.go`.
3. Update `CHANGELOG.md`.
4. Keep `go.mod` and `go.sum` committed and consistent.
5. Run:

```sh
go mod tidy
just ci
```

6. Package from the persistent source tree, not from a stale temporary extraction.
7. Verify the checksum:

```sh
shasum -a 256 -c personal-mcp-server-vX.Y.Z[-rcN].tar.gz.sha256
```

8. Verify important files inside the tarball:

```sh
tar -tzf personal-mcp-server-vX.Y.Z[-rcN].tar.gz | head
tar -xOf personal-mcp-server-vX.Y.Z[-rcN].tar.gz personal-mcp-server-vX.Y.Z[-rcN]/VERSION
tar -xOf personal-mcp-server-vX.Y.Z[-rcN].tar.gz personal-mcp-server-vX.Y.Z[-rcN]/cmd/personal-mcp-server/main.go | grep 'const version'
```

9. Install or upgrade from the packaged artifact, not from the repo working tree:

```sh
personal-mcp-server upgrade local --sha256 ./personal-mcp-server-vX.Y.Z[-rcN].tar.gz.sha256 ./personal-mcp-server-vX.Y.Z[-rcN].tar.gz
personal-mcp-server service restart --user
personal-mcp-server client ping
```

10. Confirm `client ping` reports the current server version.

## Handoff format

Every package handoff should include:

```text
Artifact:
- personal-mcp-server-vX.Y.Z[-rcN].tar.gz
- personal-mcp-server-vX.Y.Z[-rcN].tar.gz.sha256

CI:
- passed / not run here / failed with ...

Commit message:
<subject>

<body bullets>

Files added:
- ...

Files modified:
- ...

Files deleted:
- ...

Repo files to delete:
- ...
```

## Private development, public artifacts

The development repository may remain private. Public distribution can still happen through a release-only repository or object-storage location that hosts the source snapshot and checksum. A release-only public repo should contain only minimal documentation such as `README.md`, `CHANGELOG.md`, `LICENSE`, `SECURITY.md`, and the release assets.

Do not include private git history in release artifacts. If public trust/provenance becomes a goal later, add signed tags, signed checksums, or a public mirror as a deliberate release-design change.

