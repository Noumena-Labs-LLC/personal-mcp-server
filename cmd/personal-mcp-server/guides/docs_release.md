# Release docs summary

Release artifacts are versioned source snapshots plus SHA-256 checksums. They intentionally exclude git history, local tool caches, generated binaries, coverage outputs, Python caches, and OS metadata.

Use a new release version for feature, config, layout, documentation, or discovery changes. Use `-rcN` only for bug, lint, CI, test, or packaging fixes within the same release line.

Required gate:

```sh
just ci
```

Before handoff, verify the checksum, inspect important files inside the tarball, and test install or local upgrade from the packaged artifact. After service restart, `personal-mcp-server client ping` should report the current server version.

Private development repos are compatible with public source-snapshot releases. A public release-only repo or object-storage path can host the tarball and checksum without publishing private git history.
