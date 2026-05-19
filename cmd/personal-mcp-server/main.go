package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/atomicfile"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/audit"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/mcphttp"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
	serviceres "github.com/noumena-labs-llc/personal-mcp-server/internal/service"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/shell"
)

const version = "0.5.7-rc17"

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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func setupServerLogging(cfg *config.Config, levelOverride, pathOverride string, maxBytesOverride int64, maxBackupsOverride int) (func(), error) {
	levelName := strings.ToLower(strings.TrimSpace(firstNonEmpty(levelOverride, cfg.ServerLogging.Level, "info")))
	var level slog.Level
	switch levelName {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q", levelName)
	}
	path := strings.TrimSpace(firstNonEmpty(pathOverride, cfg.ServerLogging.Path))
	maxBytes := cfg.ServerLogging.MaxBytes
	if maxBytesOverride > 0 {
		maxBytes = maxBytesOverride
	}
	maxBackups := cfg.ServerLogging.MaxBackups
	if maxBackupsOverride >= 0 {
		maxBackups = maxBackupsOverride
	}
	writer := io.Writer(os.Stderr)
	closeFn := func() {}
	duplicateErrors := false
	if path != "" {
		rotating, err := newRotatingLogWriter(path, maxBytes, maxBackups)
		if err != nil {
			return nil, err
		}
		writer = rotating
		closeFn = func() { _ = rotating.Close() }
		duplicateErrors = true
	}
	primaryHandler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})
	var handler slog.Handler = primaryHandler
	if duplicateErrors {
		handler = duplicateErrorHandler{
			primary: primaryHandler,
			stderr:  slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}),
		}
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	log.SetOutput(writer)
	log.SetFlags(log.LstdFlags)
	slog.Debug("server logging configured", "level", levelName, "path", path, "max_bytes", maxBytes, "max_backups", maxBackups, "duplicate_errors_to_stderr", duplicateErrors)
	return closeFn, nil
}

type rotatingLogWriter struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	maxBytes   int64
	maxBackups int
	sizeBytes  int64
	queue      chan []byte
	done       chan struct{}
	closed     bool
	dropped    uint64
}

func newRotatingLogWriter(path string, maxBytes int64, maxBackups int) (*rotatingLogWriter, error) {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	if maxBackups < 0 {
		maxBackups = 0
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // local user-selected diagnostic log path.
	if err != nil {
		return nil, err
	}
	sizeBytes := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		sizeBytes = info.Size()
	}
	w := &rotatingLogWriter{file: f, path: path, maxBytes: maxBytes, maxBackups: maxBackups, sizeBytes: sizeBytes, queue: make(chan []byte, 1024), done: make(chan struct{})}
	go w.writeLoop()
	return w, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	entry := append([]byte(nil), p...)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return 0, os.ErrClosed
	}
	select {
	case w.queue <- entry:
	default:
		w.dropped++
	}
	w.mu.Unlock()
	return len(p), nil
}

func (w *rotatingLogWriter) writeLoop() {
	defer close(w.done)
	for p := range w.queue {
		_ = w.writeSync(p)
	}
}

func (w *rotatingLogWriter) writeSync(p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	incoming := int64(len(p))
	if err := w.rotateIfNeeded(incoming); err != nil {
		return err
	}
	n, err := w.file.Write(p)
	w.sizeBytes += int64(n)
	return err
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	close(w.queue)
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingLogWriter) rotateIfNeeded(incoming int64) error {
	if w.file == nil || w.path == "" || w.maxBytes <= 0 {
		return nil
	}
	if w.sizeBytes+incoming <= w.maxBytes {
		return nil
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	if w.maxBackups > 0 {
		oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
		_ = os.Remove(oldest)
		for i := w.maxBackups - 1; i >= 1; i-- {
			old := fmt.Sprintf("%s.%d", w.path, i)
			rotated := fmt.Sprintf("%s.%d", w.path, i+1)
			_ = os.Rename(old, rotated)
		}
		_ = os.Rename(w.path, w.path+".1")
	} else {
		_ = os.Remove(w.path)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // local user-selected diagnostic log path.
	if err != nil {
		return err
	}
	w.file = f
	w.sizeBytes = 0
	return nil
}

type duplicateErrorHandler struct {
	primary slog.Handler
	stderr  slog.Handler
}

func (h duplicateErrorHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.primary.Enabled(ctx, level)
}

func (h duplicateErrorHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.primary.Handle(ctx, record); err != nil {
		return err
	}
	if record.Level >= slog.LevelError {
		return h.stderr.Handle(ctx, record)
	}
	return nil
}

func (h duplicateErrorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return duplicateErrorHandler{primary: h.primary.WithAttrs(attrs), stderr: h.stderr.WithAttrs(attrs)}
}

func (h duplicateErrorHandler) WithGroup(name string) slog.Handler {
	return duplicateErrorHandler{primary: h.primary.WithGroup(name), stderr: h.stderr.WithGroup(name)}
}

type runtimeState struct {
	cfg     *config.Config
	handler http.Handler
	audit   *audit.Logger
	runner  *shell.Runner
}

func buildRuntime(configPath, auditPath string) (*runtimeState, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	effectiveAuditPath := auditPath
	if effectiveAuditPath == "" {
		effectiveAuditPath = cfg.Audit.Path
	}
	aud, err := audit.New(effectiveAuditPath, cfg.Audit.MaxBytes, cfg.Audit.MaxBackups)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	approvals := approval.NewManagerWithDefault(time.Duration(cfg.Approval.TimeoutSeconds)*time.Second, cfg.Approval.DefaultOnTimeout)
	fileTools := fsx.NewTools(cfg, approvals)
	projects, err := project.NewManager(cfg, fileTools.Sandbox)
	if err != nil {
		_ = aud.Close()
		return nil, fmt.Errorf("project config manager: %w", err)
	}
	fileTools.ProjectPolicy = projects
	runner := shell.NewRunner(cfg, fileTools.Sandbox, approvals, projects)
	srv := mcphttp.New(cfg, aud, approvals, version)
	registerTools(srv, cfg, fileTools, runner, projects)
	registerResources(srv, cfg, fileTools)
	return &runtimeState{cfg: cfg, handler: srv.Handler(), audit: aud, runner: runner}, nil
}

func (r *runtimeState) Close() {
	if r == nil {
		return
	}
	if r.runner != nil {
		r.runner.ClosePersistentShells()
	}
	if r.audit != nil {
		_ = r.audit.Close()
	}
}

type liveHandler struct {
	runtime atomic.Value
}

func newLiveHandler(state *runtimeState) *liveHandler {
	l := &liveHandler{}
	l.runtime.Store(state)
	return l
}

func (l *liveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	state, ok := l.runtime.Load().(*runtimeState)
	if !ok || state == nil || state.handler == nil {
		http.Error(w, "handler unavailable", http.StatusServiceUnavailable)
		return
	}
	state.handler.ServeHTTP(w, r)
}

func (l *liveHandler) Swap(state *runtimeState) *runtimeState {
	previous, _ := l.runtime.Swap(state).(*runtimeState)
	return previous
}

func (l *liveHandler) Close() {
	state, _ := l.runtime.Load().(*runtimeState)
	if state != nil {
		state.Close()
	}
}

func watchConfig(configPath, auditPath, listenAddr string, interval time.Duration, live *liveHandler) {
	currentHash, err := fileHash(configPath)
	if err != nil {
		slog.Warn("config reload disabled", "err", err)
		return
	}
	var lastRejected [32]byte
	haveRejected := false
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		nextHash, hashErr := fileHash(configPath)
		if hashErr != nil {
			slog.Warn("config reload check failed", "err", hashErr)
			continue
		}
		if nextHash == currentHash {
			continue
		}
		nextRuntime, buildErr := buildRuntime(configPath, auditPath)
		if buildErr != nil {
			if !haveRejected || nextHash != lastRejected {
				slog.Warn("config reload rejected; keeping previous valid config", "err", buildErr)
				lastRejected = nextHash
				haveRejected = true
			}
			continue
		}
		if nextRuntime.cfg.ListenAddr() != listenAddr {
			nextRuntime.Close()
			if !haveRejected || nextHash != lastRejected {
				slog.Warn("config reload rejected; server host or port changed", "old", listenAddr, "new", nextRuntime.cfg.ListenAddr())
				lastRejected = nextHash
				haveRejected = true
			}
			continue
		}
		previous := live.Swap(nextRuntime)
		previous.Close()
		currentHash = nextHash
		haveRejected = false
		slog.Info("config reloaded successfully", "config", configPath)
	}
}

func fileHash(path string) ([32]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // config path is supplied by the local user.
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

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

func initConfig(args []string) {
	if len(args) > 0 && isHelpArg(args[0]) {
		printCommandHelp("init")
		return
	}
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "where to write config.toml")
	root := fs.String("root", ".", "allowed root directory")
	force := fs.Bool("force", false, "overwrite existing config")
	generateTokenFlag := fs.Bool("generate-token", false, "write a token file and configure auth_token_file")
	tokenPath := fs.String("token-file", defaultTokenPath(), "where to write generated token")
	_ = fs.Parse(args)

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(absRoot); err != nil {
		log.Fatalf("root: %v", err)
	}
	if _, err := os.Stat(*configPath); err == nil && !*force {
		log.Fatalf("config already exists: %s (use --force to overwrite)", *configPath)
	}
	if err := os.MkdirAll(filepath.Dir(*configPath), 0o700); err != nil {
		log.Fatal(err)
	}
	tokenConfigPath := ""
	if *generateTokenFlag {
		if err := os.MkdirAll(filepath.Dir(*tokenPath), 0o700); err != nil {
			log.Fatal(err)
		}
		tok, err := generateToken()
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stat(*tokenPath); err == nil && !*force {
			log.Fatalf("token file already exists: %s (use --force to overwrite)", *tokenPath)
		}
		if err := atomicfile.WriteFile(*tokenPath, []byte(tok+"\n"), 0o600); err != nil {
			log.Fatal(err)
		}
		tokenConfigPath = *tokenPath
		fmt.Printf("wrote %s\n", *tokenPath)
	}
	content := starterConfig(absRoot, tokenConfigPath)
	if err := atomicfile.WriteFile(*configPath, []byte(content), 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s\n", *configPath)
	if !*generateTokenFlag {
		fmt.Println("set PERSONAL_MCP_TOKEN before running serve or doctor, or re-run init with --generate-token")
	}
}

func defaultAppRoot() string {
	if root := strings.TrimSpace(os.Getenv("PERSONAL_MCP_ROOT")); root != "" {
		return expandUserPath(root)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".personal-mcp-server")
	}
	return filepath.Join(home, ".personal-mcp-server")
}

func defaultConfigDir() string {
	return filepath.Join(defaultAppRoot(), "config")
}

func defaultConfigPath() string {
	return filepath.Join(defaultConfigDir(), "config.toml")
}

func defaultTokenPath() string {
	return filepath.Join(defaultConfigDir(), "token")
}

func defaultTrustStorePath() string {
	return filepath.Join(defaultConfigDir(), "trusted-projects.toml")
}

func defaultBinaryPath() string {
	return filepath.Join(defaultAppRoot(), "bin", "personal-mcp-server")
}

func starterConfig(root, tokenFile string) string {
	root = strings.ReplaceAll(root, "\\", "\\\\")
	authLine := `auth_token_env = "PERSONAL_MCP_TOKEN"`
	if tokenFile != "" {
		tokenFile = strings.ReplaceAll(tokenFile, "\\", "\\\\")
		authLine = fmt.Sprintf("auth_token_file = %q", tokenFile)
	}
	return fmt.Sprintf(`# Generated by personal-mcp-server init.
config_version = 1
roots = [%q]

[defaults]
# Optional single-user convenience mode. Explicit settings below still win.
allow_everything = false

[server]
host = "127.0.0.1"
port = 3929
endpoint = "/mcp"
%s
validate_origin = true
allowed_origins = ["http://127.0.0.1", "http://localhost"]

[server_logging]
# Diagnostic server logs. Audit logs remain separate.
# When path is set, diagnostics go to the file; error-level diagnostics are also duplicated to stderr.
# Rotated files are kept as server.log.1, server.log.2, and so on.
level = "info"
path = ""
max_bytes = 10485760
max_backups = 5
tool_slow_ms = 3000
tool_very_slow_ms = 10000

[audit]
# Optional. If omitted, audit logs go to stderr unless --audit-log is provided.
path = ""
max_bytes = 10485760
max_backups = 5

[feedback]
# Local-only feedback collection. feedback_submit appends JSONL here and never sends network data.
enabled = true
path = "~/.personal-mcp-server/feedback/feedback.jsonl"
max_summary_bytes = 500
max_details_bytes = 4000
max_context_bytes = 12000

[project_configs]
enabled = true
filename = ".personal-mcp-server.toml"
auto_load = false
trust_store = %q

# max_write_bytes is retained for config compatibility; write/delete/move tools no longer use it as a gate.
[limits]
max_read_bytes = 200000
max_write_bytes = 50000
max_search_results = 100
max_search_file_bytes = 1000000
command_timeout_seconds = 20
max_command_output_bytes = 200000
diff_context_lines = 3
max_diff_bytes = 200000
max_patch_bytes = 200000

[secrets]
deny_names = [".env", ".env.local", ".env.production", "id_rsa", "id_ed25519", "credentials", "known_hosts"]
deny_extensions = [".pem", ".key", ".p12", ".pfx"]

[tools.fs_list_roots]
enabled = true
[tools.fs_list_dir]
enabled = true
[tools.fs_get_file_info]
enabled = true
[tools.fs_tail_file]
enabled = true
[tools.fs_read_file]
enabled = true
[tools.fs_search_text]
enabled = true
[tools.fs_find]
enabled = true
[tools.fs_tree]
enabled = true
[tools.fs_replace_regex]
enabled = false
[tools.fs_apply_patch]
enabled = false
[tools.fs_apply_unified_patch]
enabled = false
[tools.fs_create_file]
enabled = false
[tools.fs_create_dir]
enabled = false
[tools.fs_replace_file]
enabled = false
[tools.fs_delete_file]
enabled = false
[tools.fs_delete_files]
enabled = false
[tools.fs_move_file]
enabled = false
[tools.fs_append_file]
enabled = false
[tools.md_outline]
enabled = true
[tools.md_read_section]
enabled = true
[tools.md_replace_section]
enabled = false
[tools.md_insert_section]
enabled = false
[tools.md_append_section]
enabled = false
[tools.json_outline]
enabled = true
[tools.json_keys]
enabled = true
[tools.json_get]
enabled = true
[tools.json_slice]
enabled = true
[tools.json_search]
enabled = true
[tools.json_validate]
enabled = true
[tools.jsonl_info]
enabled = true
[tools.jsonl_read]
enabled = true
[tools.jsonl_tail]
enabled = true
[tools.jsonl_filter]
enabled = true
[tools.jsonl_validate]
enabled = true
[tools.feedback_submit]
enabled = true
[tools.diagnostics_recent_slow_tools]
enabled = true
[tools.config_validate]
enabled = true
[tools.config_explain]
enabled = true
[tools.git_diff]
enabled = true
[tools.git_status]
enabled = true
[tools.cmd_run_named]
enabled = false
[tools.cmd_run_sequence]
enabled = false
[tools.cmd_run_argv]
enabled = false
[tools.server_info]
enabled = true
[tools.policy_describe]
enabled = true
[tools.project_info]
enabled = true
[tools.workflow_list]
enabled = true
[tools.project_config_describe]
enabled = true
[tools.setup_guide]
enabled = true
[tools.guide_list]
enabled = true
[tools.guide_read]
enabled = true
[tools.cmd_explain_policy]
enabled = true
[tools.file_explain_policy]
enabled = true
[tools.resource_list]
enabled = true
[tools.resource_read]
enabled = true

[approval]
enabled = true
timeout_seconds = 120
default_on_timeout = "deny"
remember_session_decisions = false

[file_policy]
read_default = "allow"
write_default = "deny"
create_default = "prompt"
patch_default = "prompt"
unified_patch_default = "prompt"

[[file_policy.rules]]
name = "deny secrets"
action = "deny"
operations = ["read", "list", "info", "search", "patch", "create", "write"]
pattern = "(^|/)(\\.env|id_rsa|id_ed25519|credentials)(\\.|$|/)"

[command_environment]
# Direct argv is the default. Enable persistent shells only when trusted project commands need interactive shell setup.
allow_persistent_shell = false
allowed_shells = ["/bin/zsh", "/bin/bash"]
persistent_shell_pool_size = 2
persistent_shell_acquire_timeout_seconds = 6

[command_policy]
default = "deny"

[[command_policy.rules]]
name = "allow git read-only"
action = "allow"
exec = "git"
subcommands = ["status", "diff", "log", "show", "branch"]

[[command_policy.rules]]
name = "prompt other git commands"
action = "prompt"
exec = "git"
args_regex = ".*"

[[commands]]
name = "git-status"
exec = "git"
args = ["status", "--short"]
description = "Show concise working tree status."

[[commands]]
name = "git-log"
exec = "git"
args = ["log", "--oneline", "-20"]
description = "Show recent commits."

[[commands]]
name = "git-add-all"
exec = "git"
args = ["add", "."]
description = "Stage all changes under the current repository path."

[[commands]]
name = "git-commit"
exec = "git"
args = ["commit", "--verbose"]
description = "Create a commit from staged changes. This may fail if no commit message is supplied by Git configuration. Prefer cmd_run_argv for commit messages when policy allows it."

[prompts.safe_code_edit]
enabled = true
description = "Safely inspect, patch, and verify code inside allowed roots."
template = """
Use personal MCP server carefully. Start by calling server_info and policy_describe or reading personal-mcp://server, personal-mcp://policy, and personal-mcp://guide/tools. Work only inside configured roots. Prefer resources for read-only context when available. Search before reading broadly. For large files, never request whole_file=true unless the user explicitly needs the entire file; prefer fs_get_file_info, fs_tail_file, fs_search_text, and fs_read_file with start_line/max_lines. Only global config can raise max_read_bytes. Use fs_apply_patch or fs_apply_unified_patch for scoped edits; review fs_apply_patch warnings when found counts differ and re-read before retrying zero-match edits. After edits, use git_diff and an allowed verification command when available. If approval is required, explain why and ask the local user to use personal-mcp-server approvals watch/list plus approve/deny; no native OS dialog is shown.
"""

[prompts.inspect_project]
enabled = true
description = "Read-only project inspection workflow."
template = """
Inspect the project without modifying files. Call server_info and policy_describe first, then use resources such as personal-mcp://roots, personal-mcp://policy, and personal-mcp://guide/tools when available. Use fs_list_dir, fs_search_text, fs_get_file_info, and fs_read_file as needed. Do not call fs_apply_patch, fs_create_file, fs_replace_file, fs_delete_file, fs_delete_files, fs_move_file, fs_create_dir, cmd_run_named, cmd_run_sequence, or cmd_run_argv.
"""

[prompts.edit_and_verify]
enabled = true
description = "Make a scoped edit, show the diff, and run configured verification commands."
template = """
Inspect relevant files, apply the intended scoped edit, then review git_diff and run an available named or policy-allowed verification command. Use dry_run only when a preview is useful. Treat fs_apply_patch warnings as review signals and retry zero-match edits only after re-reading. If a command or file operation requires approval, explain why it is needed and ask the local user to use personal-mcp-server approvals watch/list plus approve/deny.
"""
`, root, authLine, strings.ReplaceAll(defaultTrustStorePath(), "\\", "\\\\"))
}

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

func approvalsCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("approvals")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("approvals " + args[0]) {
			printCommandHelp("approvals")
		}
		return
	}
	fs := flag.NewFlagSet("approvals", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to global TOML config")
	interval := fs.Duration("interval", 2*time.Second, "watch polling interval")
	_ = fs.Parse(args[1:])

	client, err := newApprovalClient(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	switch args[0] {
	case "list":
		if err := client.list(); err != nil {
			log.Fatal(err)
		}
	case "watch":
		if err := client.watch(*interval); err != nil {
			log.Fatal(err)
		}
	case "approve":
		remaining := fs.Args()
		if len(remaining) != 1 {
			log.Fatal("approvals approve requires exactly one approval ID")
		}
		if err := client.decide(remaining[0], "approve"); err != nil {
			log.Fatal(err)
		}
	case "deny":
		remaining := fs.Args()
		if len(remaining) != 1 {
			log.Fatal("approvals deny requires exactly one approval ID")
		}
		if err := client.decide(remaining[0], "deny"); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

type approvalClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newApprovalClient(configPath string) (*approvalClient, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	token := cfg.AuthToken()
	if token == "" {
		return nil, fmt.Errorf("auth token is empty; set %s or configure server.auth_token_file", cfg.Server.AuthTokenEnv)
	}
	listenAddr := cfg.ListenAddr()
	if err := validateLocalApprovalAddr(listenAddr); err != nil {
		return nil, err
	}
	return &approvalClient{
		baseURL: fmt.Sprintf("http://%s", listenAddr),
		token:   token,
		client:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func validateLocalApprovalAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid configured server listen address %q: %w", addr, err)
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	return fmt.Errorf("approval CLI refuses non-local server address %q", addr)
}

func (c *approvalClient) list() error {
	body, err := c.do(http.MethodGet, "/approvals")
	if err != nil {
		return err
	}
	return printPrettyJSON(body)
}

func (c *approvalClient) watch(interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if err := c.list(); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if err := c.list(); err != nil {
			return err
		}
	}
	return nil
}

func (c *approvalClient) decide(id, decision string) error {
	body, err := c.do(http.MethodPost, "/approvals/"+url.PathEscape(id)+"/"+decision)
	if err != nil {
		return err
	}
	return printPrettyJSON(body)
}

func (c *approvalClient) do(method, reqPath string) ([]byte, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	decodedPath, err := url.PathUnescape(reqPath)
	if err != nil {
		return nil, err
	}
	endpoint.Path = decodedPath
	if decodedPath != reqPath {
		endpoint.RawPath = reqPath
	}
	req, err := http.NewRequestWithContext(context.Background(), method, endpoint.String(), http.NoBody) // #nosec G704 -- approval CLI only contacts the configured local personal-mcp-server server.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req) // #nosec G704 -- request URL is derived from validated local server config.
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s failed: HTTP %d: %s", method, reqPath, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func printPrettyJSON(body []byte) error {
	if !json.Valid(body) {
		fmt.Println(string(body))
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func serviceCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("service")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("service " + args[0]) {
			printCommandHelp("service")
		}
		return
	}
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	binary := fs.String("binary", defaultBinaryPath(), "path to personal-mcp-server binary")
	userOnly := fs.Bool("user", false, "install/manage the current user's service")
	_ = fs.Parse(args[1:])
	switch args[0] {
	case "print-launchagent":
		spec, err := loadServiceSpec(*binary, *configPath)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(serviceres.LaunchAgentPlist(spec))
	case "print-systemd":
		spec, err := loadServiceSpec(*binary, *configPath)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(serviceres.SystemdUserUnit(spec))
	case "paths":
		spec, err := loadServiceSpec(*binary, *configPath)
		if err != nil {
			log.Fatal(err)
		}
		printServicePaths(spec)
	case "install":
		requireUserFlag(*userOnly)
		if err := serviceInstall(*binary, *configPath); err != nil {
			log.Fatal(err)
		}
	case "uninstall":
		requireUserFlag(*userOnly)
		if err := serviceUninstall(); err != nil {
			log.Fatal(err)
		}
	case "start":
		requireUserFlag(*userOnly)
		if err := serviceStart(); err != nil {
			log.Fatal(err)
		}
	case "stop":
		requireUserFlag(*userOnly)
		if err := serviceStop(); err != nil {
			log.Fatal(err)
		}
	case "restart":
		requireUserFlag(*userOnly)
		if err := serviceRestart(); err != nil {
			log.Fatal(err)
		}
	case "logs":
		requireUserFlag(*userOnly)
		if err := serviceLogs(); err != nil {
			log.Fatal(err)
		}
	case "doctor":
		requireUserFlag(*userOnly)
		if err := serviceDoctor(*binary, *configPath); err != nil {
			log.Fatal(err)
		}
	case "status":
		requireUserFlag(*userOnly)
		if err := serviceStatus(*binary, *configPath); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

const (
	serviceLabel       = "com.noumenalabs.personal-mcp-server"
	systemdServiceName = "personal-mcp-server.service"
)

func requireUserFlag(userOnly bool) {
	if !userOnly {
		log.Fatal("service management commands require --user")
	}
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		log.Fatal("refusing to manage a user service as root; run this command as the target user")
	}
}

func serviceInstall(binary, configPath string) error {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(binary, configPath)
	case "linux":
		return installSystemdUserUnit(binary, configPath)
	default:
		return fmt.Errorf("service install is only supported on macOS and Linux")
	}
}

func serviceUninstall() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchAgent()
	case "linux":
		return uninstallSystemdUserUnit()
	default:
		return fmt.Errorf("service uninstall is only supported on macOS and Linux")
	}
}

func serviceStart() error {
	switch runtime.GOOS {
	case "darwin":
		return startLaunchAgent()
	case "linux":
		return runServiceCommand("systemctl", "--user", "start", systemdServiceName)
	default:
		return fmt.Errorf("service start is only supported on macOS and Linux")
	}
}

func serviceStop() error {
	switch runtime.GOOS {
	case "darwin":
		return stopLaunchAgent()
	case "linux":
		return runServiceCommand("systemctl", "--user", "stop", systemdServiceName)
	default:
		return fmt.Errorf("service stop is only supported on macOS and Linux")
	}
}

func serviceRestart() error {
	switch runtime.GOOS {
	case "darwin":
		if err := stopLaunchAgent(); err != nil {
			fmt.Fprintf(os.Stderr, "launchctl stop warning: %v\n", err)
		}
		return startLaunchAgent()
	case "linux":
		return runServiceCommand("systemctl", "--user", "restart", systemdServiceName)
	default:
		return fmt.Errorf("service restart is only supported on macOS and Linux")
	}
}

func serviceLogs() error {
	spec, err := loadServiceSpec(defaultBinaryPath(), defaultConfigPath())
	if err != nil {
		return err
	}
	fmt.Printf("stdout: %s\n", spec.Paths.StdoutLog)
	fmt.Printf("stderr: %s\n", spec.Paths.StderrLog)
	fmt.Printf("tail:   tail -f %q %q\n", spec.Paths.StdoutLog, spec.Paths.StderrLog)
	return nil
}

func serviceDoctor(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	ok := true
	check := func(name string, err error) {
		if err != nil {
			ok = false
			fmt.Printf("%s: FAIL: %v\n", name, err)
			return
		}
		fmt.Printf("%s: ok\n", name)
	}
	warn := func(name string, err error) {
		if err != nil {
			fmt.Printf("%s: WARN: %v\n", name, err)
			return
		}
		fmt.Printf("%s: ok\n", name)
	}
	checkManifest := func(name, path, binaryPath, configFile string) {
		err := requireManifestReferences(path, binaryPath, configFile)
		if os.IsNotExist(err) {
			warn(name, fmt.Errorf("manifest is not installed yet: %s", path))
			return
		}
		check(name, err)
	}
	check("root directory", requireDirectory(spec.Paths.Root))
	check("config file", requireReadableFile(spec.Paths.ConfigFile))
	check("config validation", validateConfigFile(spec.Paths.ConfigFile))
	check("token file", requirePrivateReadableFile(spec.Paths.TokenFile))
	check("trusted projects store", requireOptionalParent(spec.Paths.TrustStore))
	check("state directory", requireDirectory(spec.Paths.StateDir))
	check("logs directory", requireDirectory(spec.Paths.LogsDir))
	check("install binary", requireExecutableFile(spec.Process.Executable))
	check("installed binary version", requireInstalledBinaryVersion(spec.Process.Executable))
	switch runtime.GOOS {
	case "darwin":
		check("launchctl", requireExecutableOnPath("launchctl"))
		check("LaunchAgent manifest", requireOptionalParent(spec.Platforms["darwin"].ManifestPath))
		checkManifest("LaunchAgent manifest content", spec.Platforms["darwin"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile)
	case "linux":
		check("systemctl", requireExecutableOnPath("systemctl"))
		check("systemd user unit", requireOptionalParent(spec.Platforms["linux"].ManifestPath))
		checkManifest("systemd user unit content", spec.Platforms["linux"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile)
	default:
		fmt.Printf("service backend: WARN: unsupported platform %s\n", runtime.GOOS)
	}
	if !ok {
		return fmt.Errorf("service doctor found problems")
	}
	return nil
}

func requireDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return nil
}

func requireReadableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory: %s", path)
	}
	f, err := os.Open(path) //nolint:gosec // service doctor checks explicit local service paths.
	if err != nil {
		return err
	}
	return f.Close()
}

func requirePrivateReadableFile(path string) error {
	if err := requireReadableFile(path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by group or others (%s)", path, info.Mode().Perm())
	}
	return nil
}

func requireOptionalParent(path string) error {
	return requireDirectory(filepath.Dir(path))
}

func requireExecutableFile(path string) error {
	if err := requireReadableFile(path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("not executable: %s", path)
	}
	return nil
}

func requireExecutableOnPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func validateConfigFile(path string) error {
	_, err := config.Load(path)
	return err
}

func requireInstalledBinaryVersion(path string) error {
	cmd := exec.CommandContext(context.Background(), path, "version") //nolint:gosec // service doctor runs the configured user-local personal-mcp-server binary.
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	got := strings.TrimSpace(string(out))
	want := "personal-mcp-server " + version
	firstLine, _, _ := strings.Cut(got, "\n")
	if firstLine != want {
		return fmt.Errorf("installed binary reports first line %q, want %q", firstLine, want)
	}
	return nil
}

func requireManifestReferences(path, binary, configPath string) error {
	body, err := os.ReadFile(path) //nolint:gosec // service doctor checks the explicit local service manifest path.
	if err != nil {
		return err
	}
	text := string(body)
	if !manifestContainsPath(text, binary) {
		return fmt.Errorf("manifest does not reference expected binary %s", binary)
	}
	if !manifestContainsPath(text, configPath) {
		return fmt.Errorf("manifest does not reference expected config %s", configPath)
	}
	return nil
}

func manifestContainsPath(text, path string) bool {
	return strings.Contains(text, path) || strings.Contains(text, html.EscapeString(path)) || strings.Contains(text, systemdManifestQuote(path))
}

func systemdManifestQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}

func serviceStatus(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	printServiceStatusHeader(spec)
	switch runtime.GOOS {
	case "darwin":
		return printLaunchAgentStatus(spec)
	case "linux":
		return printSystemdUserStatus(spec)
	default:
		return fmt.Errorf("service status is only supported on macOS and Linux")
	}
}

func printServiceStatusHeader(spec serviceres.Spec) {
	manifestPath := ""
	backend := "unsupported"
	if platform, ok := spec.Platforms[runtime.GOOS]; ok {
		manifestPath = platform.ManifestPath
		backend = platform.Backend
	}
	fmt.Printf("service:  %s\n", spec.Service.Label)
	fmt.Printf("backend:  %s\n", backend)
	fmt.Printf("manifest: %s\n", manifestPath)
	fmt.Printf("binary:   %s\n", spec.Process.Executable)
	fmt.Printf("config:   %s\n", spec.Paths.ConfigFile)
	fmt.Printf("token:    %s\n", spec.Paths.TokenFile)
	fmt.Printf("stdout:   %s\n", spec.Paths.StdoutLog)
	fmt.Printf("stderr:   %s\n", spec.Paths.StderrLog)
}

func printLaunchAgentStatus(spec serviceres.Spec) error {
	fmt.Printf("target:   %s\n", launchAgentServiceTarget())
	if _, err := exec.LookPath("launchctl"); err != nil {
		fmt.Printf("manager:  missing launchctl: %v\n", err)
		return nil
	}
	out, err := captureServiceCommand("launchctl", "print", launchAgentServiceTarget())
	if err != nil {
		fmt.Printf("loaded:   false\n")
		fmt.Printf("manager:  launchctl print failed: %v\n", err)
		return nil
	}
	status := parseLaunchctlPrint(out)
	fmt.Printf("loaded:   true\n")
	fmt.Printf("running:  %s\n", boolStatus(status.PID != ""))
	if status.PID != "" {
		fmt.Printf("pid:      %s\n", status.PID)
	}
	if status.LastExitCode != "" {
		fmt.Printf("last_exit_code: %s\n", status.LastExitCode)
	}
	if err := requireManifestReferences(spec.Platforms["darwin"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile); err != nil {
		fmt.Printf("manifest_check: WARN: %v\n", err)
	} else {
		fmt.Printf("manifest_check: ok\n")
	}
	return nil
}

func printSystemdUserStatus(spec serviceres.Spec) error {
	fmt.Printf("unit:     %s\n", systemdServiceName)
	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Printf("manager:  missing systemctl: %v\n", err)
		return nil
	}
	out, err := captureServiceCommand("systemctl", "--user", "show", systemdServiceName, "--property=LoadState,ActiveState,SubState,MainPID,ExecMainStatus")
	if err != nil {
		fmt.Printf("manager:  systemctl show failed: %v\n", err)
		return nil
	}
	status := parseSystemctlShow(out)
	fmt.Printf("load_state: %s\n", valueOrUnknown(status["LoadState"]))
	fmt.Printf("active_state: %s\n", valueOrUnknown(status["ActiveState"]))
	fmt.Printf("sub_state: %s\n", valueOrUnknown(status["SubState"]))
	if pid := status["MainPID"]; pid != "" && pid != "0" {
		fmt.Printf("pid:      %s\n", pid)
	}
	if exitStatus := status["ExecMainStatus"]; exitStatus != "" {
		fmt.Printf("exec_main_status: %s\n", exitStatus)
	}
	if err := requireManifestReferences(spec.Platforms["linux"].ManifestPath, spec.Process.Executable, spec.Paths.ConfigFile); err != nil {
		fmt.Printf("manifest_check: WARN: %v\n", err)
	} else {
		fmt.Printf("manifest_check: ok\n")
	}
	return nil
}

type launchctlStatus struct {
	PID          string
	LastExitCode string
}

func parseLaunchctlPrint(out string) launchctlStatus {
	status := launchctlStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "pid = "):
			status.PID = strings.TrimSpace(strings.TrimPrefix(line, "pid = "))
		case strings.HasPrefix(line, "last exit code = "):
			status.LastExitCode = strings.TrimSpace(strings.TrimPrefix(line, "last exit code = "))
		}
	}
	return status
}

func parseSystemctlShow(out string) map[string]string {
	status := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		status[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return status
}

func captureServiceCommand(name string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed service-management commands, not user-controlled shell.
	body, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(body)), err
}

func boolStatus(ok bool) string {
	if ok {
		return "true"
	}
	return "false"
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func installLaunchAgent(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	if err := prepareServiceInstall(spec); err != nil {
		return err
	}
	plistPath := spec.Platforms["darwin"].ManifestPath
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o750); err != nil {
		return err
	}
	if err := atomicfile.WriteFile(plistPath, []byte(serviceres.LaunchAgentPlist(spec)), 0o600); err != nil {
		return err
	}
	fmt.Printf("installed LaunchAgent: %s\n", plistPath)
	if err := bootoutLaunchAgentIfLoaded(); err != nil {
		fmt.Fprintf(os.Stderr, "launchctl bootout warning: %v\n", err)
	}
	return startLaunchAgent()
}

func uninstallLaunchAgent() error {
	plistPath, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := bootoutLaunchAgentIfLoaded(); err != nil {
		fmt.Fprintf(os.Stderr, "launchctl bootout warning: %v\n", err)
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("removed LaunchAgent: %s\n", plistPath)
	return nil
}

func installSystemdUserUnit(binary, configPath string) error {
	spec, err := loadServiceSpec(binary, configPath)
	if err != nil {
		return err
	}
	if err := prepareServiceInstall(spec); err != nil {
		return err
	}
	unitPath := spec.Platforms["linux"].ManifestPath
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o750); err != nil {
		return err
	}
	if err := atomicfile.WriteFile(unitPath, []byte(serviceres.SystemdUserUnit(spec)), 0o600); err != nil {
		return err
	}
	fmt.Printf("installed systemd user unit: %s\n", unitPath)
	if err := runServiceCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return runServiceCommand("systemctl", "--user", "enable", "--now", systemdServiceName)
}

func uninstallSystemdUserUnit() error {
	unitPath, err := systemdUserUnitPath()
	if err != nil {
		return err
	}
	if err := runServiceCommand("systemctl", "--user", "disable", "--now", systemdServiceName); err != nil {
		fmt.Fprintf(os.Stderr, "systemctl disable warning: %v\n", err)
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("removed systemd user unit: %s\n", unitPath)
	return runServiceCommand("systemctl", "--user", "daemon-reload")
}

func startLaunchAgent() error {
	plistPath, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := runServiceCommand("launchctl", "bootstrap", launchAgentDomain(), plistPath); err != nil {
		return err
	}
	return runServiceCommand("launchctl", "kickstart", "-k", launchAgentServiceTarget())
}

func stopLaunchAgent() error {
	return runServiceCommand("launchctl", "bootout", launchAgentServiceTarget())
}

func bootoutLaunchAgentIfLoaded() error {
	out, err := captureServiceCommand("launchctl", "bootout", launchAgentServiceTarget())
	if err == nil {
		return nil
	}
	if isLaunchctlNoSuchProcess(out, err) {
		return nil
	}
	if out != "" {
		fmt.Fprintln(os.Stderr, out)
	}
	return err
}

func isLaunchctlNoSuchProcess(out string, err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(out, "Boot-out failed: 3: No such process") ||
		strings.Contains(out, "No such process") ||
		strings.Contains(out, "Could not find service")
}

func prepareServiceInstall(spec serviceres.Spec) error {
	if err := requireConfigFileExists(spec.Paths.ConfigFile); err != nil {
		return err
	}
	for _, dir := range []string{spec.Paths.Root, spec.Paths.ConfigDir, spec.Paths.StateDir, spec.Paths.LogsDir, filepath.Dir(spec.Process.Executable)} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return installCurrentBinary(spec.Process.Executable)
}

func requireConfigFileExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config file does not exist: %s; run personal-mcp-server init --generate-token first", path)
		}
		return err
	}
	return nil
}

func installCurrentBinary(target string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	source = expandUserPath(source)
	target = expandUserPath(target)
	same, err := samePath(source, target)
	if err != nil {
		return err
	}
	if same {
		return nil
	}
	if err := copyExecutable(source, target); err != nil {
		return err
	}
	fmt.Printf("installed binary: %s\n", target)
	return nil
}

func samePath(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	realA, errA := filepath.EvalSymlinks(absA)
	realB, errB := filepath.EvalSymlinks(absB)
	if errA == nil {
		absA = realA
	}
	if errB == nil {
		absB = realB
	}
	return absA == absB, nil
}

func copyExecutable(source, target string) error {
	in, err := os.Open(source) //nolint:gosec // service install copies the current executable path reported by the OS.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // target is the validated user-local service binary path.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, 0o755) //nolint:gosec // installed service binary must be executable by the current user.
}

type executableReplacement struct {
	target    string
	backup    string
	hadBackup bool
}

func replaceExecutableWithRollback(source, target string) (executableReplacement, error) {
	replacement := executableReplacement{target: target}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return replacement, err
	}
	if _, err := os.Stat(target); err == nil {
		replacement.hadBackup = true
		replacement.backup = target + ".backup-" + time.Now().UTC().Format("20060102T150405Z")
		if err := copyExecutable(target, replacement.backup); err != nil {
			return replacement, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return replacement, err
	}
	tmpTarget := target + ".tmp-" + time.Now().UTC().Format("20060102T150405Z")
	if err := copyExecutable(source, tmpTarget); err != nil {
		return replacement, err
	}
	if err := os.Rename(tmpTarget, target); err != nil {
		_ = os.Remove(tmpTarget)
		replacement.cleanup()
		return replacement, err
	}
	return replacement, nil
}

func (r executableReplacement) restore() {
	if !r.hadBackup {
		if err := os.Remove(r.target); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "rollback warning: failed to remove new binary: %v\n", err)
			return
		}
		fmt.Fprintf(os.Stderr, "removed new binary after failed upgrade: %s\n", r.target)
		return
	}
	if err := copyExecutable(r.backup, r.target); err != nil {
		fmt.Fprintf(os.Stderr, "rollback warning: failed to restore previous binary: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "restored previous binary: %s\n", r.target)
}

func (r executableReplacement) cleanup() {
	if r.hadBackup {
		_ = os.Remove(r.backup)
	}
}

func launchAgentPath() (string, error) {
	spec, err := loadServiceSpec(defaultBinaryPath(), defaultConfigPath())
	if err != nil {
		return "", err
	}
	return spec.Platforms["darwin"].ManifestPath, nil
}

func systemdUserUnitPath() (string, error) {
	spec, err := loadServiceSpec(defaultBinaryPath(), defaultConfigPath())
	if err != nil {
		return "", err
	}
	return spec.Platforms["linux"].ManifestPath, nil
}

func launchAgentDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func launchAgentServiceTarget() string {
	return launchAgentDomain() + "/" + serviceLabel
}

func runServiceCommand(name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed service-management commands, not user-controlled shell.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func loadServiceSpec(binary, configPath string) (serviceres.Spec, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return serviceres.Spec{}, err
	}
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		configDir = filepath.Join(home, ".config")
	}
	return serviceres.LoadDefaultSpec(serviceres.Vars{
		"app_root":        defaultAppRoot(),
		"install_bin":     expandUserPath(binary),
		"config_file":     expandUserPath(configPath),
		"home":            home,
		"user_config_dir": configDir,
	})
}

func printServicePaths(spec serviceres.Spec) {
	fmt.Printf("root:        %s\n", spec.Paths.Root)
	fmt.Printf("binary:      %s\n", spec.Process.Executable)
	fmt.Printf("config:      %s\n", spec.Paths.ConfigFile)
	fmt.Printf("token:       %s\n", spec.Paths.TokenFile)
	fmt.Printf("trust:       %s\n", spec.Paths.TrustStore)
	fmt.Printf("state:       %s\n", spec.Paths.StateDir)
	fmt.Printf("logs:        %s\n", spec.Paths.LogsDir)
	fmt.Printf("macos plist: %s\n", spec.Platforms["darwin"].ManifestPath)
	fmt.Printf("linux unit:  %s\n", spec.Platforms["linux"].ManifestPath)
}

type upgradeOptions struct {
	ArtifactPath   string
	SHAPath        string
	BinaryPath     string
	RestartService bool
	DryRun         bool
}

func upgradeCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("upgrade")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("upgrade " + args[0]) {
			printCommandHelp("upgrade")
		}
		return
	}
	if args[0] != "local" {
		usage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet("upgrade local", flag.ExitOnError)
	shaPath := fs.String("sha256", "", "optional sha256sum file; defaults to ARTIFACT.tar.gz.sha256 when present")
	binary := fs.String("binary", defaultBinaryPath(), "installed personal-mcp-server binary path")
	dryRun := fs.Bool("dry-run", false, "verify, inspect, and build the local artifact without replacing the installed binary")
	noRestart := fs.Bool("no-restart-service", false, "do not restart an installed user service after replacing the binary")
	_ = fs.Parse(args[1:])
	remaining := fs.Args()
	if len(remaining) != 1 {
		log.Fatal("upgrade local requires exactly one artifact tarball")
	}
	opts := upgradeOptions{
		ArtifactPath:   remaining[0],
		SHAPath:        *shaPath,
		BinaryPath:     *binary,
		RestartService: !*noRestart,
		DryRun:         *dryRun,
	}
	if err := upgradeLocal(opts); err != nil {
		log.Fatal(err)
	}
}

func upgradeLocal(opts upgradeOptions) error {
	artifactPath := expandUserPath(opts.ArtifactPath)
	binaryPath := expandUserPath(opts.BinaryPath)
	if err := verifyArtifactSHA256(artifactPath, opts.SHAPath); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "personal-mcp-server-upgrade-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if err := extractTarGz(artifactPath, tmpDir); err != nil {
		return err
	}
	srcDir, err := findExtractedModuleRoot(tmpDir)
	if err != nil {
		return err
	}
	if err := requireUpgradeModule(srcDir); err != nil {
		return err
	}
	artifactVersion, err := upgradeArtifactVersion(srcDir)
	if err != nil {
		return err
	}
	serviceWasInstalled := userServiceManifestExists()
	fmt.Printf("artifact: %s\n", artifactPath)
	fmt.Printf("artifact version: %s\n", artifactVersion)
	fmt.Println("module: github.com/noumena-labs-llc/personal-mcp-server")
	fmt.Printf("target binary: %s\n", binaryPath)
	fmt.Printf("service installed: %t\n", serviceWasInstalled)
	fmt.Printf("restart service: %t\n", opts.RestartService && serviceWasInstalled)

	builtBinary := filepath.Join(tmpDir, "personal-mcp-server-built")
	if err := runUpgradeCommand(srcDir, "go", "build", "-o", builtBinary, "./cmd/personal-mcp-server"); err != nil {
		return err
	}
	if opts.DryRun {
		fmt.Println("dry run: built artifact successfully; installed binary was not changed")
		return nil
	}
	if opts.RestartService && serviceWasInstalled {
		if err := serviceStop(); err != nil {
			fmt.Fprintf(os.Stderr, "service stop warning: %v\n", err)
		}
	}
	replacement, err := replaceExecutableWithRollback(builtBinary, binaryPath)
	if err != nil {
		return err
	}
	fmt.Printf("upgraded binary: %s\n", binaryPath)
	if err := runInstalledVersion(binaryPath); err != nil {
		replacement.restore()
		return err
	}
	if opts.RestartService && serviceWasInstalled {
		if err := serviceStart(); err != nil {
			replacement.restore()
			if restoreErr := serviceStart(); restoreErr != nil {
				return fmt.Errorf("service restart failed after upgrade (%w); restored previous binary but restart also failed: %w", err, restoreErr)
			}
			return fmt.Errorf("service restart failed after upgrade (%w); restored previous binary and restarted service", err)
		}
	}
	replacement.cleanup()
	return nil
}

func verifyArtifactSHA256(artifactPath, shaPath string) error {
	if shaPath == "" {
		candidate := artifactPath + ".sha256"
		if _, err := os.Stat(candidate); err == nil {
			shaPath = candidate
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if shaPath == "" {
		fmt.Fprintln(os.Stderr, "sha256 warning: no .sha256 file found; continuing without checksum verification")
		return nil
	}
	shaPath = expandUserPath(shaPath)
	expected, err := readSHA256SumFile(shaPath)
	if err != nil {
		return err
	}
	actual, err := fileSHA256Hex(artifactPath)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("sha256 mismatch for %s: got %s, want %s", artifactPath, actual, expected)
	}
	fmt.Printf("verified sha256: %s\n", shaPath)
	return nil
}

func readSHA256SumFile(path string) (string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // user-supplied local checksum path for explicit local upgrade.
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty sha256 file: %s", path)
	}
	digest := strings.ToLower(fields[0])
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("invalid sha256 digest in %s", path)
	}
	for _, ch := range digest {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return "", fmt.Errorf("invalid sha256 digest in %s", path)
		}
	}
	return digest, nil
}

func fileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // user-supplied local artifact path for explicit local upgrade.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func extractTarGz(artifactPath, destDir string) error {
	f, err := os.Open(artifactPath) //nolint:gosec // user-supplied local artifact path for explicit local upgrade.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := extractTarEntry(tr, header, destDir); err != nil {
			return err
		}
	}
}

func extractTarEntry(r io.Reader, header *tar.Header, destDir string) error {
	cleanName := filepath.Clean(header.Name)
	if cleanName == "." || filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || cleanName == ".." {
		return fmt.Errorf("unsafe tar entry path: %s", header.Name)
	}
	target := filepath.Join(destDir, cleanName)
	if !strings.HasPrefix(target, filepath.Clean(destDir)+string(filepath.Separator)) {
		return fmt.Errorf("unsafe tar entry target: %s", header.Name)
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o750)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, header.FileInfo().Mode().Perm()) //nolint:gosec // target path is constrained under the temp extraction directory.
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, r); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	case tar.TypeSymlink, tar.TypeLink:
		return fmt.Errorf("refusing link entry in upgrade artifact: %s", header.Name)
	default:
		return nil
	}
}

func findExtractedModuleRoot(tmpDir string) (string, error) {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(tmpDir, entry.Name())
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("upgrade artifact does not contain a top-level Go module")
}

func requireUpgradeModule(srcDir string) error {
	body, err := os.ReadFile(filepath.Join(srcDir, "go.mod")) //nolint:gosec // srcDir is a safely extracted local artifact directory.
	if err != nil {
		return err
	}
	firstLine := strings.TrimSpace(strings.SplitN(string(body), "\n", 2)[0])
	if firstLine != "module github.com/noumena-labs-llc/personal-mcp-server" {
		return fmt.Errorf("upgrade artifact module = %q, want github.com/noumena-labs-llc/personal-mcp-server", firstLine)
	}
	return nil
}

func upgradeArtifactVersion(srcDir string) (string, error) {
	versionBody, err := os.ReadFile(filepath.Join(srcDir, "VERSION")) //nolint:gosec // srcDir is a safely extracted local artifact directory.
	if err != nil {
		return "", err
	}
	artifactVersion := strings.TrimSpace(string(versionBody))
	if artifactVersion == "" {
		return "", fmt.Errorf("upgrade artifact VERSION is empty")
	}
	mainBody, err := os.ReadFile(filepath.Join(srcDir, "cmd", "personal-mcp-server", "main.go")) //nolint:gosec // srcDir is a safely extracted local artifact directory.
	if err != nil {
		return "", err
	}
	constLine := `const version = "` + artifactVersion + `"`
	if !strings.Contains(string(mainBody), constLine) {
		return "", fmt.Errorf("upgrade artifact version mismatch: VERSION=%s but main.go does not contain %s", artifactVersion, constLine)
	}
	return artifactVersion, nil
}

func runUpgradeCommand(dir, name string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // fixed upgrade build command, args are constants.
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runInstalledVersion(binaryPath string) error {
	cmd := exec.CommandContext(context.Background(), binaryPath, "version") //nolint:gosec // binary path is explicit local upgrade destination.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func userServiceManifestExists() bool {
	switch runtime.GOOS {
	case "darwin":
		path, err := launchAgentPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	case "linux":
		path, err := systemdUserUnitPath()
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	default:
		return false
	}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
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

func printVersion() {
	fmt.Println("personal-mcp-server " + version)
	fmt.Println("go: " + runtime.Version())
	fmt.Println("mcp-go-sdk: " + mcpSDKVersion())
}

func mcpSDKVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range bi.Deps {
			if dep.Path == "github.com/modelcontextprotocol/go-sdk" {
				return dep.Version
			}
		}
	}
	return "unknown"
}

func serverInfo(cfg *config.Config) map[string]any {
	largeFileGuidance := []string{
		"Do not request whole_file=true unless the user explicitly needs the entire file.",
		"Use fs_search_text to locate relevant sections before reading.",
		"Use fs_read_file with start_line and max_lines for large files.",
		"Use fs_get_file_info to check size before reading unfamiliar files.",
		"If a file exceeds max_read_bytes, avoid whole-file retries; raise limits.max_read_bytes only in the global config and verify with policy_describe.",
	}
	if cfg.Tools.Find.Enabled {
		largeFileGuidance[1] = "Use fs_find and fs_search_text to locate relevant sections before reading."
	}
	return map[string]any{
		"name":           "personal-mcp-server",
		"version":        version,
		"module":         "github.com/noumena-labs-llc/personal-mcp-server",
		"transport":      "streamable_http",
		"protocol":       "mcp",
		"go_version":     runtime.Version(),
		"mcp_go_sdk":     mcpSDKVersion(),
		"config_version": cfg.ConfigVersion,
		"features": map[string]bool{
			"hot_reload":                true,
			"localhost_only":            true,
			"allow_everything_defaults": cfg.Defaults.AllowEverything,
			"resources":                 true,
			"tool_resource_mirror":      true,
			"file_policy":               true,
			"command_policy":            true,
			"approvals":                 cfg.Approval.Enabled,
			"per_call_cwd":              true,
			"unified_patch":             cfg.Tools.ApplyUnifiedPatch.Enabled,
			"regex_replace":             cfg.Tools.ReplaceRegex.Enabled,
			"native_find":               cfg.Tools.Find.Enabled,
			"guide_tools":               true,
			"git_status":                cfg.Tools.GitStatus.Enabled || cfg.Tools.GitDiff.Enabled,
			"bounded_reads_default":     true,
			"project_configs":           cfg.ProjectConfigs.Enabled,
			"background_jobs":           cfg.Tools.RunCommand.Enabled,
			"server_logging":            true,
			"tool_latency_logging":      true,
			"progressive_catalog":       true,
			"json_navigation":           cfg.Tools.JSONOutline.Enabled || cfg.Tools.JSONGet.Enabled || cfg.Tools.JSONKeys.Enabled || cfg.Tools.JSONSlice.Enabled || cfg.Tools.JSONSearch.Enabled,
			"jsonl_navigation":          cfg.Tools.JSONLInfo.Enabled || cfg.Tools.JSONLRead.Enabled || cfg.Tools.JSONLTail.Enabled || cfg.Tools.JSONLFilter.Enabled,
			"feedback_collection":       cfg.Feedback.Enabled && cfg.Tools.FeedbackSubmit.Enabled,
		},
		"large_file_guidance": largeFileGuidance,
		"tool_latency": map[string]any{
			"slow_ms":      cfg.ServerLogging.ToolSlowMS,
			"very_slow_ms": cfg.ServerLogging.ToolVerySlowMS,
		},
	}
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

func auditCommand(args []string) {
	if len(args) == 0 || isHelpArg(args[0]) {
		printCommandHelp("audit")
		return
	}
	if len(args) > 1 && isHelpArg(args[1]) {
		if !printCommandHelp("audit " + args[0]) {
			printCommandHelp("audit")
		}
		return
	}
	switch args[0] {
	case "show":
		auditShow(args[1:])
	case "tail":
		auditTail(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

type auditFilter struct {
	Tool     string
	Decision string
	Contains string
	Format   string
}

func auditShow(args []string) {
	fs := flag.NewFlagSet("audit show", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	last := fs.Int("last", 50, "number of matching audit log lines to show")
	filter := addAuditFilterFlags(fs)
	_ = fs.Parse(args)
	path := auditPathFromConfig(*configPath)
	lines, err := lastLines(path, *last, *filter)
	if err != nil {
		log.Fatal(err)
	}
	printAuditLines(lines, *filter)
}

func auditTail(args []string) {
	fs := flag.NewFlagSet("audit tail", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to TOML config")
	last := fs.Int("last", 50, "number of matching audit log lines to show before following")
	follow := fs.Bool("follow", false, "continue printing new matching audit log lines")
	interval := fs.Duration("interval", time.Second, "poll interval when --follow is set")
	filter := addAuditFilterFlags(fs)
	_ = fs.Parse(args)
	path := auditPathFromConfig(*configPath)
	lines, err := lastLines(path, *last, *filter)
	if err != nil {
		log.Fatal(err)
	}
	printAuditLines(lines, *filter)
	if !*follow {
		fmt.Fprintf(os.Stderr, "audit tail is a snapshot; add --follow to keep watching: %s\n", path)
		return
	}
	if err := followAudit(path, *filter, *interval); err != nil {
		log.Fatal(err)
	}
}

func addAuditFilterFlags(fs *flag.FlagSet) *auditFilter {
	filter := &auditFilter{}
	fs.StringVar(&filter.Tool, "tool", "", "only show JSON audit events with this tool value")
	fs.StringVar(&filter.Decision, "decision", "", "only show JSON audit events with this decision/action value")
	fs.StringVar(&filter.Contains, "contains", "", "only show lines containing this text")
	fs.StringVar(&filter.Format, "format", "raw", "output format: raw or pretty")
	return filter
}

func auditPathFromConfig(configPath string) string {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	if strings.TrimSpace(cfg.Audit.Path) == "" {
		log.Fatal("audit.path is not set in config; use --audit-log when serving or set [audit].path")
	}
	return cfg.Audit.Path
}

func lastLines(path string, n int, filter auditFilter) ([]string, error) {
	if n <= 0 {
		n = 50
	}
	b, err := os.ReadFile(path) //nolint:gosec // audit path is local user config.
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return nil, nil
	}
	all := strings.Split(text, "\n")
	lines := make([]string, 0, min(n, len(all)))
	for _, line := range all {
		if auditLineMatches(line, filter) {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func followAudit(path string, filter auditFilter, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	var offset int64
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	}
	for {
		time.Sleep(interval)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() < offset {
			offset = 0
		}
		if info.Size() == offset {
			continue
		}
		chunk, nextOffset, err := readAuditChunk(path, offset)
		if err != nil {
			return err
		}
		offset = nextOffset
		for _, line := range strings.Split(strings.TrimRight(chunk, "\n"), "\n") {
			if line != "" && auditLineMatches(line, filter) {
				printAuditLines([]string{line}, filter)
			}
		}
	}
}

func readAuditChunk(path string, offset int64) (chunk string, nextOffset int64, err error) {
	f, err := os.Open(path) //nolint:gosec // audit path is local user config.
	if err != nil {
		return "", offset, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", offset, err
	}
	return string(b), offset + int64(len(b)), nil
}

func auditLineMatches(line string, filter auditFilter) bool {
	if filter.Contains != "" && !strings.Contains(line, filter.Contains) {
		return false
	}
	if filter.Tool == "" && filter.Decision == "" {
		return true
	}
	fields := map[string]any{}
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return false
	}
	if filter.Tool != "" && stringField(fields, "tool") != filter.Tool {
		return false
	}
	if filter.Decision != "" && stringField(fields, "decision") != filter.Decision && stringField(fields, "action") != filter.Decision {
		return false
	}
	return true
}

func printAuditLines(lines []string, filter auditFilter) {
	for _, line := range lines {
		fmt.Println(formatAuditLine(line, filter.Format))
	}
}

func formatAuditLine(line, format string) string {
	switch format {
	case "", "raw":
		return line
	case "pretty":
		fields := map[string]any{}
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			return line
		}
		parts := []string{}
		for _, key := range []string{"ts", "tool", "decision", "action", "path", "name", "error"} {
			if value := stringField(fields, key); value != "" {
				parts = append(parts, fmt.Sprintf("%s=%s", key, value))
			}
		}
		if len(parts) == 0 {
			return line
		}
		return strings.Join(parts, " ")
	default:
		return line
	}
}

func stringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	s, ok := value.(string)
	if ok {
		return s
	}
	return fmt.Sprint(value)
}
