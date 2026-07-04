package main

import (
	"flag"
	"fmt"
	"os"
)

func flagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ExitOnError)
}

func osExit2() {
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, commandHelp["top"])
}

func isHelpArg(arg string) bool {
	return arg == "help" || arg == "-h" || arg == "--help"
}

func printCommandHelp(topic string) bool {
	switch topic {
	case "client", "client ping", "client tools", "client call", "client raw":
		printClientHelp()
		return true
	case "client run-named":
		printClientRunNamedHelp()
		return true
	}
	text, ok := commandHelp[topic]
	if !ok {
		return false
	}
	_, _ = fmt.Fprint(os.Stdout, text)
	return true
}

var commandHelp = map[string]string{
	"top": `usage:
  personal-mcp-server help [COMMAND...]
  personal-mcp-server init [--config CONFIG] [--root ROOT] [--generate-token] [--token-file PATH] [--force]
  personal-mcp-server doctor --config CONFIG
  personal-mcp-server config validate --config CONFIG
  personal-mcp-server client [--config CONFIG] [--url URL] [--token TOKEN] ping|tools|call|run-named|raw ...
  personal-mcp-server project init|validate|trust|untrust|list|effective [--config CONFIG] [--cwd DIR]
  personal-mcp-server approvals list|watch|approve|deny --config CONFIG [ID]
  personal-mcp-server audit show|tail --config CONFIG [--last N] [--tool TOOL] [--decision DECISION] [--contains TEXT] [--format raw|pretty]
  personal-mcp-server service paths|print-launchagent|print-systemd [--config CONFIG] [--binary BIN]
  personal-mcp-server service install|uninstall|start|stop|restart|status|logs|doctor --user [--config CONFIG] [--binary BIN]
  personal-mcp-server upgrade local [--sha256 SHA256] [--binary BIN] [--dry-run] [--no-restart-service] ARTIFACT.tar.gz
  personal-mcp-server serve --config CONFIG [--audit-log PATH] [--log-level LEVEL] [--log-file PATH] [--log-max-bytes N] [--log-max-backups N] [--reload-interval DURATION]
  personal-mcp-server version

Use "personal-mcp-server help COMMAND" or "personal-mcp-server COMMAND help" for command-specific help.
`,
	"serve": `usage:
  personal-mcp-server serve [--config CONFIG] [--audit-log PATH] [--log-level LEVEL] [--log-file PATH] [--log-max-bytes N] [--log-max-backups N] [--reload-interval DURATION]

Start the local MCP HTTP server. Diagnostic logs go to stderr by default, or --log-file when set. File logs rotate with numbered backups, and error-level diagnostics are also duplicated to stderr.
`,
	"doctor": `usage:
  personal-mcp-server doctor [--config CONFIG]

Validate config and print local runtime diagnostics.
`,
	"init": `usage:
  personal-mcp-server init [--config CONFIG] [--root ROOT] [--generate-token] [--token-file PATH] [--force]

Write a starter global config.
`,
	"version": `usage:
  personal-mcp-server version

Print the personal-mcp-server version.
`,
	"config": `usage:
  personal-mcp-server config validate [--config CONFIG]

Commands:
  validate    Load and validate the global TOML config.

Examples:
  personal-mcp-server config validate --config ~/.personal-mcp-server/config/config.toml
`,
	"config validate": `usage:
  personal-mcp-server config validate [--config CONFIG]

Load and validate the global TOML config.
`,
	"project": `usage:
  personal-mcp-server project init [--cwd DIR] [--name NAME] [--force]
  personal-mcp-server project validate [--cwd DIR]
  personal-mcp-server project trust [--config CONFIG] [--cwd DIR]
  personal-mcp-server project untrust [--config CONFIG] [--cwd DIR]
  personal-mcp-server project list [--config CONFIG]
  personal-mcp-server project effective [--config CONFIG] [--cwd DIR] [--include-commands]

Commands:
  init         Write a starter .personal-mcp-server.toml in a project.
  validate     Validate a project config.
  trust        Trust a project config for auto-load.
  untrust      Remove a project trust entry.
  list         List trusted projects.
  effective    Print effective project policy as JSON.
`,
	"project init": `usage:
  personal-mcp-server project init [--cwd DIR] [--name NAME] [--force]

Write a starter project config.
`,
	"project validate": `usage:
  personal-mcp-server project validate [--cwd DIR]

Validate a project config.
`,
	"project trust": `usage:
  personal-mcp-server project trust [--config CONFIG] [--cwd DIR]

Trust a project config for auto-load.
`,
	"project untrust": `usage:
  personal-mcp-server project untrust [--config CONFIG] [--cwd DIR]

Remove a project trust entry.
`,
	"project list": `usage:
  personal-mcp-server project list [--config CONFIG]

List trusted projects.
`,
	"project effective": `usage:
  personal-mcp-server project effective [--config CONFIG] [--cwd DIR] [--include-commands]

Print effective project policy as JSON.
`,
	"approvals": `usage:
  personal-mcp-server approvals list [--config CONFIG]
  personal-mcp-server approvals watch [--config CONFIG] [--interval DURATION]
  personal-mcp-server approvals approve [--config CONFIG] APPROVAL_ID
  personal-mcp-server approvals deny [--config CONFIG] APPROVAL_ID

Commands:
  list       Print pending approvals.
  watch      Poll and print pending approvals. No native OS or Claude Desktop approval dialog is shown; keep this command open to see prompt-required operations.
  approve    Approve one pending request.
  deny       Deny one pending request.
`,
	"approvals list": `usage:
  personal-mcp-server approvals list [--config CONFIG]

Print pending approvals.
`,
	"approvals watch": `usage:
  personal-mcp-server approvals watch [--config CONFIG] [--interval DURATION]

Poll and print pending approvals. No native OS or Claude Desktop approval dialog is shown; keep this command open to see prompt-required operations.
`,
	"approvals approve": `usage:
  personal-mcp-server approvals approve [--config CONFIG] APPROVAL_ID

Approve one pending request.
`,
	"approvals deny": `usage:
  personal-mcp-server approvals deny [--config CONFIG] APPROVAL_ID

Deny one pending request.
`,
	"audit": `usage:
  personal-mcp-server audit show [--config CONFIG] [--last N] [--tool TOOL] [--decision DECISION] [--contains TEXT] [--format raw|pretty]
  personal-mcp-server audit tail [--config CONFIG] [--last N] [--follow] [--interval DURATION] [--tool TOOL] [--decision DECISION] [--contains TEXT] [--format raw|pretty]

Commands:
  show    Show recent matching audit events.
  tail    Show recent matching audit events, optionally following new events.
`,
	"audit show": `usage:
  personal-mcp-server audit show [--config CONFIG] [--last N] [--tool TOOL] [--decision DECISION] [--contains TEXT] [--format raw|pretty]

Show recent matching audit events.
`,
	"audit tail": `usage:
  personal-mcp-server audit tail [--config CONFIG] [--last N] [--follow] [--interval DURATION] [--tool TOOL] [--decision DECISION] [--contains TEXT] [--format raw|pretty]

Show recent matching audit events, optionally following new events.
`,
	"service": `usage:
  personal-mcp-server service paths [--config CONFIG] [--binary BIN]
  personal-mcp-server service print-launchagent [--config CONFIG] [--binary BIN]
  personal-mcp-server service print-systemd [--config CONFIG] [--binary BIN]
  personal-mcp-server service install|uninstall|start|stop|restart|status|logs|doctor --user [--config CONFIG] [--binary BIN]

Commands:
  paths               Print resolved service paths.
  print-launchagent   Print the macOS LaunchAgent plist.
  print-systemd       Print the Linux systemd user unit.
  install             Install the user service.
  uninstall           Remove the user service.
  start               Start the user service.
  stop                Stop the user service.
  restart             Restart the user service.
  status              Print service status.
  logs                Follow service logs.
  doctor              Check service installation and runtime state.
`,
	"service paths": `usage:
  personal-mcp-server service paths [--config CONFIG] [--binary BIN]

Print resolved service paths.
`,
	"service print-launchagent": `usage:
  personal-mcp-server service print-launchagent [--config CONFIG] [--binary BIN]

Print the macOS LaunchAgent plist.
`,
	"service print-systemd": `usage:
  personal-mcp-server service print-systemd [--config CONFIG] [--binary BIN]

Print the Linux systemd user unit.
`,
	"service install": `usage:
  personal-mcp-server service install --user [--config CONFIG] [--binary BIN]

Install the current user's service.
`,
	"service uninstall": `usage:
  personal-mcp-server service uninstall --user [--config CONFIG] [--binary BIN]

Remove the current user's service.
`,
	"service start": `usage:
  personal-mcp-server service start --user [--config CONFIG] [--binary BIN]

Start the current user's service.
`,
	"service stop": `usage:
  personal-mcp-server service stop --user [--config CONFIG] [--binary BIN]

Stop the current user's service.
`,
	"service restart": `usage:
  personal-mcp-server service restart --user [--config CONFIG] [--binary BIN]

Restart the current user's service.
`,
	"service status": `usage:
  personal-mcp-server service status --user [--config CONFIG] [--binary BIN]

Print current user's service status.
`,
	"service logs": `usage:
  personal-mcp-server service logs --user [--config CONFIG] [--binary BIN]

Follow current user's service logs.
`,
	"service doctor": `usage:
  personal-mcp-server service doctor --user [--config CONFIG] [--binary BIN]

Check service installation and runtime state.
`,
	"upgrade": `usage:
  personal-mcp-server upgrade local [--sha256 SHA256] [--binary BIN] [--dry-run] [--no-restart-service] ARTIFACT.tar.gz

Commands:
  local    Verify and install a local source artifact.

Examples:
  personal-mcp-server upgrade local --dry-run ./personal-mcp-server-v0.5.3.tar.gz
`,
	"upgrade local": `usage:
  personal-mcp-server upgrade local [--sha256 SHA256] [--binary BIN] [--dry-run] [--no-restart-service] ARTIFACT.tar.gz

Verify and install a local source artifact.
`,
}

func discoveryToolsEnabled() bool {
	return true
}
