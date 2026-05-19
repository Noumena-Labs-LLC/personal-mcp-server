# Project configs

personal-mcp-server supports checked-in project manifests named `.personal-mcp-server.toml`.

The global config remains the machine-level safety boundary. A project config is a repo-level manifest: it can describe project commands, workflow names, search defaults, file-operation hints, and project guidance. It must not be treated as a way to expand roots or bypass global deny rules.

Use documented `snake_case` TOML keys in generated project configs, especially command fields like `allow_extra_args`, `max_extra_args`, `extra_args`, `run_mode`, and `cwd`. The loader is lenient for common CamelCase/PascalCase aliases, but guides should still emit `snake_case` so configs stay readable and stable.

## Trust model

Project configs are discoverable but not trusted automatically by default.

```toml
[project_configs]
enabled = true
filename = ".personal-mcp-server.toml"
auto_load = false
trust_store = "~/.personal-mcp-server/config/trusted-projects.toml"
```

Trust is stored outside the repo, so a cloned repository cannot approve itself:

```bash
personal-mcp-server project trust --cwd ~/RnD/my-project
```

Useful commands:

```bash
personal-mcp-server project init --cwd ~/RnD/my-project
personal-mcp-server project validate --cwd ~/RnD/my-project
personal-mcp-server project trust --cwd ~/RnD/my-project
personal-mcp-server project untrust --cwd ~/RnD/my-project
personal-mcp-server project list
personal-mcp-server project effective --cwd ~/RnD/my-project
```

## Command model

Project configs define named argv commands, similar to a safe MCP-aware makefile/justfile manifest. Complex workflows should live in `justfile`, `Makefile`, or scripts inside the repo. The project config should expose a single entry point.

```toml
config_kind = "project"
config_version = 1

[project]
name = "my-project"
description = "Project-specific personal MCP server guidance."

[[commands]]
name = "test"
exec = "just"
args = ["test"]
description = "Run the project test suite."
# Optional default cwd, relative to the trusted project root.
# Used only when the tool call omits cwd; tool-call cwd wins.
# cwd = "packages/api"

[[commands]]
name = "ci"
exec = "just"
args = ["ci"]
description = "Run local CI."
```

No raw user-provided shell strings are supported. Put pipelines, redirects, or compound workflows in normal project automation such as `make`, `just`, or scripts, then expose that entry point as a named command. Direct argv is the default; trusted project commands can opt into `run_mode = "persistent_shell"` only when global config allows it.

Named commands may opt into constrained `extra_args`. Extra args are never shell-interpolated and must match declared rules:

```toml
[[commands]]
name = "pytest"
exec = "python"
args = ["-m", "pytest"]
description = "Run pytest with optional test paths and common flags."
allow_extra_args = true
max_extra_args = 10

[[commands.extra_args]]
kind = "path"
allow_globs = ["tests/**", "src/**"]
must_exist = true
must_be_inside_project = true

[[commands.extra_args]]
kind = "enum"
values = ["-q", "-v", "-x", "--maxfail=1"]
```

Claude can then call `cmd_run_named` with `extra_args`, but only arguments accepted by these rules will run.

## Discovery from tools

Use `project_info` with a `cwd` to see whether a project config was found and trusted:

```json
{
  "cwd": "my-project",
  "include_commands": true
}
```

Use `cmd_list_named` with the same `cwd` to see global and project commands:

```json
{
  "cwd": "my-project",
  "include_args": true
}
```

`cmd_run_named` resolves project commands only when `cwd` points inside a trusted project. If no trusted project command is found, it falls back to global commands. A project command may configure `cwd`, but it must be relative to the trusted project root and is used only when the tool call omits `cwd`; when the tool call supplies `cwd`, that value wins.

## Other project hints

Project configs may also include search defaults, workflow aliases, important guidance, protected file rules, generated-file rules, and project-scoped policy suggestions. Trusted project file rules participate in file-policy decisions, but global deny rules and root restrictions still win.

## Project guide metadata

Project configs may include `guide.read_first` to suggest important files for agents to inspect before broad exploration:

```toml
[guide]
summary = "Use project commands and bounded reads."
read_first = ["README.md", "docs/ARCHITECTURE.md", "pyproject.toml"]
```

Workflow aliases are exposed through the `workflow_list` tool.

## Protected and generated files

Trusted project configs can harden edit operations for files that should not be changed casually. These rules apply to `fs_apply_patch`, `fs_apply_unified_patch`, `fs_replace_regex`, `fs_create_file`, and `fs_create_dir`.

```toml
[protected_files]
deny_edit = ["dist/**", "build/**", "generated/**"]
prompt_edit = ["go.mod", "go.sum", "package.json", ".github/workflows/**"]

[generated]
paths = ["dist/**", "build/**", "generated/**", "**/*.pb.go"]
default_action = "deny"
```

Precedence is intentionally conservative: global deny rules still win, trusted project deny rules beat project allow rules, and trusted project prompt rules still require approval. Use `file_explain_policy` before editing sensitive paths to see the global, project, and effective decisions.

## LLM discovery

LLMs should call `project_config_describe` or read `personal-mcp://guide/project-config` before creating or editing `.personal-mcp-server.toml`. The guide explains the trust model, safe-before-trust metadata, trusted-only effects, workflow examples, named command examples, protected/generated file hints, and project-scoped file policy examples.

## Command run modes

Named commands run with `run_mode = "argv"` by default. In argv mode, the server starts the configured executable directly and passes arguments without shell parsing. This is the safest and most predictable mode, but it inherits the personal-mcp-server server process environment rather than the user's current interactive terminal shell. That means project commands may not see shell-only setup such as pyenv, asdf, nvm, direnv, virtualenv activation, aliases, shell functions, or PATH changes made in `.zshrc`/`.bashrc`.

Trusted project commands may opt into a persistent shell session:

```toml
[command_environment]
allow_persistent_shell = true
allowed_shells = ["/bin/zsh", "/bin/bash"]

[[commands]]
name = "test"
exec = "make"
args = ["unit-test"]
description = "Run tests through the user's shell environment."
run_mode = "persistent_shell"
shell = "/bin/zsh"
```

Persistent shell mode is opt-in for trusted project commands only. A project can set `[command_environment] run_mode = "persistent_shell"` and `shell = "/bin/zsh"` as defaults, and individual commands can override those defaults. Shell sessions are PTY-backed and pooled by project root and shell path. The server prefers an idle shell, creates another pooled shell when capacity allows, and waits only briefly when the pool is full. Before each command, the server sends an explicit `cd` to the requested `cwd`; if the shell times out, exits, loses output framing, or exceeds limits, the server terminates that shell session and recreates it on a later command. The model still cannot send arbitrary shell strings: the command line is built from the configured `exec`, configured `args`, and validated `extra_args`.

Prefer argv mode for reproducible commands. Use persistent shell mode only when the command genuinely depends on the user's interactive shell environment.



## Global permissive defaults

For single-user local setups that prefer an allow-by-default posture, the global config can opt in with:

```toml
[defaults]
allow_everything = true
```

When enabled, absent safety defaults become permissive: built-in tools default enabled, approval defaults disabled, and missing command/file policy defaults become `allow`. Explicit settings still win, so individual tools or policy defaults can still be disabled or denied in the same config. Project configs cannot enable this setting or weaken global safety boundaries.

## Feedback configuration

The global config can enable local-only feedback collection:

```toml
[feedback]
enabled = true
path = "~/.personal-mcp-server/feedback/feedback.jsonl"
max_summary_bytes = 500
max_details_bytes = 4000
max_context_bytes = 12000

[tools.feedback_submit]
enabled = true
```

The tool appends JSONL to the configured path. Tool callers cannot choose an output path.

## Tool latency diagnostics

Global `[server_logging]` settings include `tool_slow_ms` and `tool_very_slow_ms`. These are machine-level diagnostic thresholds for all tool calls and should be set in the main config rather than project configs.
