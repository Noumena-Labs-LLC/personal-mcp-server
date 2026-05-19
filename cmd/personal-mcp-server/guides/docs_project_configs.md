# Project config docs summary

Use `.personal-mcp-server.toml` to describe repo-specific guidance, workflows, commands, read-first files, protected files, generated files, and project-scoped policy.

Project configs are checked into repos, but trust is local and stored outside repos. Treat project configs as workflow guidance until trusted.

Call `project_info` with `cwd` to inspect the active config. Call `workflow_list` with `cwd` to see workflow aliases. Use `personal-mcp://guide/project-config` for examples.

Use canonical `snake_case` keys in generated TOML, such as `allow_extra_args` and `max_extra_args`. The loader accepts common CamelCase/PascalCase aliases for ergonomics, but `snake_case` wins when both are present.
