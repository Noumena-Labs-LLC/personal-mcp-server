# Release process

Release artifacts are source snapshots, not git checkouts. They should build on a clean machine with the Go toolchain pinned by `go.mod`, with `go.mod` and `go.sum` present. Do not include `.git/`, local tool caches, generated binaries, coverage outputs, Python caches, or OS metadata in release tarballs.

The public repository uses `dev` as the default integration branch and `main` as the production/release branch:

```text
working branches -> PR -> dev
release/vX.Y.Z (main + unreleased dev commits) -> PR -> main
```

Releases reach `main` through a `release/vX.Y.Z` branch, not a `dev -> main` PR. Rebase merges into `main` give commits new IDs, so `dev` never contains `main`'s tip, and `main` requires PR branches to be up to date. A PR from `dev` can never meet that, and `dev` can't be brought up to date because it only accepts squash-merged PRs. For the same reason, ignore GitHub's "Compare & pull request" prompt for `main` after a release.

## Versioning

Each feature, config, layout, documentation, or discovery change gets a new release version such as `v0.5.10`. Use `-rcN` only for bug, lint, CI, test, or packaging fixes inside that same release line.

Examples:

```text
v0.5.10      feature/docs/tool-discovery change
v0.5.10-rc1  bug fix in that line
v0.5.10-rc2  CI/test fix in that line
v0.5.10      next docs/release pass
```

Commit subjects for release preparation may start with the exact artifact version, for example:

```text
v0.5.10: Prepare public release
```

## Required files

Every source artifact handoff should include:

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
LICENSE
SECURITY.md
DISCLAIMER.md
THREAT_MODEL.md
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

`CHANGELOG.md` is not required for the initial public release. Release notes should be written in the GitHub Release description unless a public changelog is intentionally added later.

## Local release checklist

1. Update `VERSION`.
2. Update the CLI/server version constant in `cmd/personal-mcp-server/main.go`.
3. Keep `go.mod` and `go.sum` committed and consistent.
4. Run the commands below. Run `just tools` first if the pinned linter versions changed; `just ci` only installs tools that are missing.

```sh
go mod tidy
just ci
```

5. Package from the persistent source tree, not from a stale temporary extraction.
6. Verify the checksum:

```sh
shasum -a 256 -c personal-mcp-server-vX.Y.Z[-rcN].tar.gz.sha256
```

7. Verify important files inside the tarball:

```sh
tar -tzf personal-mcp-server-vX.Y.Z[-rcN].tar.gz | head
tar -xOf personal-mcp-server-vX.Y.Z[-rcN].tar.gz personal-mcp-server-vX.Y.Z[-rcN]/VERSION
tar -xOf personal-mcp-server-vX.Y.Z[-rcN].tar.gz personal-mcp-server-vX.Y.Z[-rcN]/cmd/personal-mcp-server/main.go | grep 'const version'
```

8. Install or upgrade from the packaged artifact, not from the repo working tree:

```sh
personal-mcp-server upgrade local --sha256 ./personal-mcp-server-vX.Y.Z[-rcN].tar.gz.sha256 ./personal-mcp-server-vX.Y.Z[-rcN].tar.gz
personal-mcp-server service restart --user
personal-mcp-server client --config ~/.personal-mcp-server/config/config.toml ping
```

9. Confirm `client ping` reports the current server version.

## GitHub release checklist

1. Merge all release-bound working branches into `dev` through PRs.
2. Confirm `dev` CI is green.
3. Build the release branch from `main` plus the `dev` commits that aren't on `main` yet. `--cherry-pick` compares commits by content, so it skips `dev` commits whose rebased copies are already on `main`:

```sh
git fetch origin
git rev-list --reverse --cherry-pick --right-only --no-merges origin/main...origin/dev  # should list only this release's commits
git switch --no-track -c release/vX.Y.Z origin/main
git cherry-pick $(git rev-list --reverse --cherry-pick --right-only --no-merges origin/main...origin/dev)
git diff --stat origin/dev HEAD  # expect no output: same tree as dev
git push -u origin release/vX.Y.Z
```

4. Open a `release/vX.Y.Z -> main` PR titled `Release vX.Y.Z` and confirm CI is green.
5. Merge it with **Rebase and merge**, the only merge method `main` allows.
6. Confirm `main` CI is green, then tag `main`:

```sh
git fetch origin
git checkout main
git pull origin main
git tag -a vX.Y.Z -m "vX.Y.Z — <summary>"
git push origin vX.Y.Z
```

7. Create a GitHub Release from the tag, named the same as the tag message (`vX.Y.Z — <summary>`).
8. Include concise public release notes that summarize the product, target users, and highlights.
9. Attach source artifacts and checksums if desired, or rely on GitHub-generated source archives plus CI artifacts.
10. After publishing, verify branch protections/rulesets, repository topics, issue templates, and funding metadata.

## Suggested initial public release notes

```md
# personal-mcp-server v0.5.10

First public release of personal-mcp-server, a localhost Streamable HTTP MCP server for trusted single-user filesystem, Markdown, JSON/JSONL, diagnostics, and named-command workflows.

This release is aimed especially at Claude Desktop users who need a local MCP server that avoids stdio transport fragility for larger local workflows.

Highlights:

- Streamable HTTP MCP transport
- Local filesystem navigation and mutation tools under configured roots
- Markdown section tools
- JSON and JSONL navigation/filtering tools
- Named command execution and command policy
- Per-project `.personal-mcp-server.toml` configs
- Diagnostics, audit logs, feedback records
- Low-friction local posture with configurable safety controls

See `DISCLAIMER.md`, `SECURITY.md`, and `THREAT_MODEL.md` before using on important systems.
```

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

## Private development and public history

Do not include private git history in release artifacts. If public trust/provenance becomes a goal later, add signed tags, signed checksums, SLSA provenance, or a public mirror as a deliberate release-design change.
