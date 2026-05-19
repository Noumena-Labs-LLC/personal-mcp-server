# Project config guide: `.personal-mcp-server.toml`

A project config is a checked-in, repo-specific manifest that tells personal MCP server how to work in one project. It is similar in spirit to a safe MCP-aware `justfile` or `Makefile` index.

Global config answers: "What may this MCP server do on this machine?"

Project config answers: "How should the MCP server work in this repo?"

Write TOML keys in documented `snake_case`: `allow_extra_args`, `max_extra_args`, `extra_args`, `run_mode`, and `cwd`. The global config loader is lenient with common CamelCase/PascalCase aliases, but generated examples should use `snake_case`.

## Trust model

Project configs can be discovered before trust, but sensitive effects require local trust stored outside the repo.

Safe before trust:

- project name and description
- guide text
- read-first file hints
- workflow names and descriptions
- search include/exclude hints
- command suggestions as untrusted hints

Trusted-only effects:

- executable project commands
- project file-policy allow/prompt/deny effects
- project command-policy effects
- tool enable/disable effects, if supported later

Project config must not control server host, port, auth token path, audit path, global roots, raw shell access, or global hard-deny policy.

## Minimal example

```toml
[project]
name = "my-project"
description = "Short description for the LLM."

[guide]
text = "Read README.md and docs/ARCHITECTURE.md first. Use just ci for verification."
read_first = ["README.md", "docs/ARCHITECTURE.md"]

[[workflows]]
name = "ci"
description = "Run the full local quality gate."
command = "ci"
```

## Named commands

Project commands are argv-only. Put complex logic in `justfile`, `Makefile`, package scripts, or scripts under version control.

```toml
[[commands]]
name = "ci"
exec = "just"
args = ["ci"]

[[commands]]
name = "test"
exec = "go"
args = ["test", "./..."]
```

## Constrained extra args

Use constrained extra args when the LLM needs to pass a test file or a safe flag.

```toml
[[commands]]
name = "pytest"
exec = "python"
args = ["-m", "pytest"]
allow_extra_args = true
max_extra_args = 8

[[commands.extra_args]]
kind = "path"
allow_globs = ["tests/**/*.py", "src/**/*.py"]
must_exist = true

[[commands.extra_args]]
kind = "enum"
values = ["-q", "-v", "-x", "--maxfail=1"]
```

## Protected and generated files

Use these sections to help the LLM avoid unsafe edits.

```toml
[protected_files]
deny_edit = ["package-lock.json", "uv.lock", "generated/**"]
prompt_edit = ["go.mod", "go.sum", ".github/workflows/**"]

[generated]
paths = ["dist/**", "build/**", "**/*.pb.go"]
default_action = "deny"
```

## File policy rules

Project file policy is scoped to the project root. Global deny rules still win.

```toml
[file_policy]
patch_default = "prompt"
create_default = "prompt"

[[file_policy.rules]]
name = "allow docs edits"
action = "allow"
operations = ["patch", "create"]
pattern = "(^|/)docs/.*\\.md$"
```

## LLM workflow

When helping a user configure a project:

1. Read `personal-mcp://guide/project-config`.
2. Inspect the repository with `fs_search_text`, bounded reads, and `fs_tree`/`fs_find` only when `server_info.features.native_find` is true.
3. Identify build/test/lint commands from README, justfile, Makefile, package files, or CI config.
4. Draft `.personal-mcp-server.toml` with conservative workflows and commands.
5. Avoid broad write permissions and raw shell assumptions.
6. Ask the user to trust the project locally before expecting project commands or policy effects to run.

## Trust refresh

Project trust is stored outside the repository in the configured trust store. Project-aware MCP tools refresh trust state when they inspect a project, so trusting or untrusting a project should become visible without restarting the server.

## Command run modes

Project commands use `run_mode = "argv"` unless configured otherwise. Argv mode is safest and does not load an interactive shell. The downside is that commands inherit the personal-mcp-server server process environment, not the user's current terminal shell, so shell-only setup such as pyenv, asdf, nvm, direnv, virtualenv activation, aliases, shell functions, and PATH changes may be missing.

If trusted project commands need that environment, a project can set defaults:

```toml
[command_environment]
run_mode = "persistent_shell"
shell = "/bin/zsh"
```

Individual commands can still override with `run_mode = "argv"` or a different allowed shell. Global config must also enable `[command_environment].allow_persistent_shell = true` and allow the shell path. Persistent shells are PTY-backed, pooled by project root and shell path, and treated as disposable: the server sends `cd` before every command, uses another pooled shell when one is busy, and kills/recreates a shell on timeout or framing failure. The model still cannot provide raw shell strings; only configured argv plus validated `extra_args` is run.
