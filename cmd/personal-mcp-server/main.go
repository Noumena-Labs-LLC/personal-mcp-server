package main

import (
	"embed"
	"fmt"
	"os"
	"strings"
)

const version = "0.5.12"

//go:embed guides/*.md
var guideFS embed.FS

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if os.Args[1] == "help" {
		if len(os.Args) == 2 {
			printCommandHelp("top")
			return
		}
		if !printCommandHelp(strings.Join(os.Args[2:], " ")) {
			fmt.Fprintf(os.Stderr, "unknown help topic %q\n", strings.Join(os.Args[2:], " "))
			usage()
			os.Exit(2)
		}
		return
	}
	if isHelpArg(os.Args[1]) {
		printCommandHelp("top")
		return
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "doctor":
		doctor(os.Args[2:])
	case "init":
		initConfig(os.Args[2:])
	case "config":
		configCommand(os.Args[2:])
	case "client":
		clientCommand(os.Args[2:])
	case "project":
		projectCommand(os.Args[2:])
	case "service":
		serviceCommand(os.Args[2:])
	case "upgrade":
		upgradeCommand(os.Args[2:])
	case "approvals":
		approvalsCommand(os.Args[2:])
	case "audit":
		auditCommand(os.Args[2:])
	case "version":
		printVersion()
	default:
		usage()
		os.Exit(2)
	}
}
