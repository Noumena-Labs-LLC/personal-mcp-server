package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func doctor(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		printCommandHelp("doctor")
		return
	}
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	_ = fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("config: ok")
	fmt.Printf("config_version: %d\n", cfg.ConfigVersion)
	fmt.Printf("listen: http://%s%s\n", cfg.ListenAddr(), cfg.Server.Endpoint)
	if cfg.Audit.Path != "" {
		fmt.Printf("audit: %s\n", cfg.Audit.Path)
	} else {
		fmt.Println("audit: stderr unless --audit-log is provided")
	}
	if cfg.Server.AuthTokenEnv != "" && os.Getenv(cfg.Server.AuthTokenEnv) != "" {
		fmt.Printf("auth: ok via env %s\n", cfg.Server.AuthTokenEnv)
	} else if cfg.Server.AuthTokenFile != "" {
		fmt.Printf("auth: ok via token file %s\n", cfg.Server.AuthTokenFile)
		checkTokenFilePermissions(cfg.Server.AuthTokenFile)
	}
	fmt.Printf("project_configs: enabled=%t filename=%s auto_load=%t trust_store=%s\n", cfg.ProjectConfigs.Enabled, cfg.ProjectConfigs.Filename, cfg.ProjectConfigs.AutoLoad, cfg.ProjectConfigs.TrustStore)
	fmt.Printf("roots: %d\n", len(cfg.Roots))
	for _, root := range cfg.Roots {
		fmt.Printf("  ok: %s\n", root)
	}
	failed := false
	for i := range cfg.Commands {
		cmd := &cfg.Commands[i]
		if _, err := exec.LookPath(cmd.Exec); err != nil {
			fmt.Printf("command %s: FAIL: %s not found on PATH\n", cmd.Name, cmd.Exec)
			failed = true
		} else {
			fmt.Printf("command %s: ok\n", cmd.Name)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func checkTokenFilePermissions(path string) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Printf("auth token file permissions: WARN: %v\n", err)
		return
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		fmt.Printf("auth token file permissions: WARN: %s is readable by group or others (%s)\n", path, info.Mode().Perm())
		return
	}
	fmt.Println("auth token file permissions: ok")
}
