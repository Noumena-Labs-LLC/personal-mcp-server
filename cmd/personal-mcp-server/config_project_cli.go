package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/atomicfile"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
)

func configCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("config")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("config " + args[0]) {
			printCommandHelp("config")
		}
		return
	}
	switch args[0] {
	case "validate":
		validateConfig(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

func validateConfig(args []string) {
	fs := flag.NewFlagSet("config validate", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	_ = fs.Parse(args)
	if _, err := config.Load(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "config: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("config: ok")
}

func projectCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("project")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("project " + args[0]) {
			printCommandHelp("project")
		}
		return
	}
	switch args[0] {
	case "init":
		projectInit(args[1:])
	case "validate":
		projectValidate(args[1:])
	case "trust":
		projectTrust(args[1:])
	case "untrust":
		projectUntrust(args[1:])
	case "list":
		projectList(args[1:])
	case "effective":
		projectEffective(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

func projectInit(args []string) {
	fs := flag.NewFlagSet("project init", flag.ExitOnError)
	cwd := fs.String("cwd", ".", "project directory")
	force := fs.Bool("force", false, "overwrite existing project config")
	name := fs.String("name", "", "project name")
	_ = fs.Parse(args)
	abs, err := filepath.Abs(*cwd)
	if err != nil {
		log.Fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		log.Fatal(err)
	}
	if !info.IsDir() {
		log.Fatalf("project cwd is not a directory: %s", abs)
	}
	path := filepath.Join(abs, project.DefaultFilename)
	if _, err := os.Stat(path); err == nil && !*force {
		log.Fatalf("project config already exists: %s (use --force to overwrite)", path)
	}
	projectName := *name
	if strings.TrimSpace(projectName) == "" {
		projectName = filepath.Base(abs)
	}
	if err := atomicfile.WriteFile(path, []byte(project.Starter(projectName)), 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s\n", path)
	fmt.Println("review it, then run personal-mcp-server project trust --cwd " + abs)
}

func projectValidate(args []string) {
	fs := flag.NewFlagSet("project validate", flag.ExitOnError)
	cwd := fs.String("cwd", ".", "project directory")
	_ = fs.Parse(args)
	path := filepath.Join(mustAbsDir(*cwd), project.DefaultFilename)
	if _, err := project.Load(path); err != nil {
		fmt.Fprintf(os.Stderr, "project config: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("project config: ok")
}

func projectTrust(args []string) {
	fs := flag.NewFlagSet("project trust", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to global TOML config")
	cwd := fs.String("cwd", ".", "project directory")
	_ = fs.Parse(args)
	pm := loadProjectManager(*configPath)
	entry, err := pm.Trust(*cwd)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("trusted project %s\n", entry.Root)
}

func projectUntrust(args []string) {
	fs := flag.NewFlagSet("project untrust", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to global TOML config")
	cwd := fs.String("cwd", ".", "project directory")
	_ = fs.Parse(args)
	pm := loadProjectManager(*configPath)
	if err := pm.Untrust(*cwd); err != nil {
		log.Fatal(err)
	}
	fmt.Println("project untrusted")
}

func projectList(args []string) {
	fs := flag.NewFlagSet("project list", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to global TOML config")
	_ = fs.Parse(args)
	pm := loadProjectManager(*configPath)
	for _, entry := range pm.ListTrusted() {
		fmt.Printf("%s\t%s\n", entry.Root, entry.Config)
	}
}

func projectEffective(args []string) {
	fs := flag.NewFlagSet("project effective", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to global TOML config")
	cwd := fs.String("cwd", ".", "project directory")
	includeCommands := fs.Bool("include-commands", true, "include executable argv in output")
	_ = fs.Parse(args)
	pm := loadProjectManager(*configPath)
	fmt.Println(project.Marshal(pm.EffectiveInfo(*cwd, *includeCommands)))
}

func loadProjectManager(configPath string) *project.Manager {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	sandbox := fsx.NewSandbox(cfg)
	pm, err := project.NewManager(cfg, sandbox)
	if err != nil {
		log.Fatal(err)
	}
	return pm
}

func mustAbsDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		log.Fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		log.Fatal(err)
	}
	if !info.IsDir() {
		log.Fatalf("not a directory: %s", abs)
	}
	return abs
}
