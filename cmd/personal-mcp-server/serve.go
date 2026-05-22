package main

import (
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func serve(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		printCommandHelp("serve")
		return
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	auditPath := fs.String("audit-log", "", "optional audit log path")
	logLevel := fs.String("log-level", "", "server diagnostic log level: debug, info, warn, or error; overrides [server_logging].level")
	logPath := fs.String("log-file", "", "optional server diagnostic log file; overrides [server_logging].path")
	logMaxBytes := fs.Int64("log-max-bytes", 0, "server diagnostic log rotation size in bytes; overrides [server_logging].max_bytes")
	logMaxBackups := fs.Int("log-max-backups", -1, "number of rotated server diagnostic logs to keep; overrides [server_logging].max_backups")
	reloadInterval := fs.Duration("reload-interval", 5*time.Second, "TOML reload poll interval; set to 0 to disable")
	_ = fs.Parse(args)

	rt, err := buildRuntime(*configPath, *auditPath)
	if err != nil {
		log.Fatalf("startup: %v", err)
	}
	closeLog, err := setupServerLogging(rt.cfg, *logLevel, *logPath, *logMaxBytes, *logMaxBackups)
	if err != nil {
		rt.Close()
		log.Fatalf("server logging: %v", err)
	}
	addr := rt.cfg.ListenAddr()
	live := newLiveHandler(rt)
	if *reloadInterval > 0 {
		go watchConfig(*configPath, *auditPath, addr, *reloadInterval, live)
	}

	slog.Info("personal-mcp-server listening", "addr", addr, "endpoint", rt.cfg.Server.Endpoint, "version", version)
	if *reloadInterval > 0 {
		slog.Info("config reload enabled", "config", *configPath, "interval", reloadInterval.String())
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           live,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		slog.Error("server exited", "error", err)
		live.Close()
		closeLog()
		os.Exit(1)
	}
	live.Close()
	closeLog()
}
