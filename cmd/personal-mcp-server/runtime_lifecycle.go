package main

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/audit"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/mcphttp"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/project"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/shell"
)

type runtimeState struct {
	mu sync.RWMutex

	cfg       *config.Config
	handler   http.Handler
	audit     *audit.Logger
	fileTools *fsx.Tools
	projects  *project.Manager
	runner    *shell.Runner
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
	return &runtimeState{cfg: cfg, handler: srv.Handler(), audit: aud, fileTools: fileTools, projects: projects, runner: runner}, nil
}

func (r *runtimeState) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runner != nil {
		r.runner.ClosePersistentShells()
	}
	if r.audit != nil {
		_ = r.audit.Close()
	}
}

func (r *runtimeState) ReloadConfig(next *config.Config) error {
	if r == nil || next == nil {
		return fmt.Errorf("runtime is not available")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if next.ListenAddr() != r.cfg.ListenAddr() {
		return fmt.Errorf("server host or port changed: old=%s new=%s", r.cfg.ListenAddr(), next.ListenAddr())
	}
	if next.Server.Endpoint != r.cfg.Server.Endpoint {
		return fmt.Errorf("server endpoint changed: old=%s new=%s", r.cfg.Server.Endpoint, next.Server.Endpoint)
	}
	if !reflect.DeepEqual(next.Tools, r.cfg.Tools) {
		return fmt.Errorf("tool enablement/description changes require restart")
	}
	if !reflect.DeepEqual(next.Audit, r.cfg.Audit) {
		return fmt.Errorf("audit log configuration changes require restart")
	}

	newSandbox := fsx.NewSandbox(next)
	newProjects, err := project.NewManager(next, newSandbox)
	if err != nil {
		return fmt.Errorf("project config manager: %w", err)
	}
	newProjects.Global = r.cfg
	newProjects.Sandbox = newSandbox

	*r.cfg = *next
	r.fileTools.Cfg = r.cfg
	r.fileTools.Sandbox = newSandbox
	r.fileTools.ProjectPolicy = newProjects
	r.projects = newProjects
	r.runner.Cfg = r.cfg
	r.runner.Sandbox = newSandbox
	r.runner.Projects = newProjects
	r.runner.Specs = commandSpecsByName(r.cfg.Commands)
	r.runner.ClosePersistentShells()
	return nil
}

func commandSpecsByName(commands []config.CommandSpec) map[string]config.CommandSpec {
	specs := map[string]config.CommandSpec{}
	for i := range commands {
		cmd := &commands[i]
		specs[cmd.Name] = *cmd
	}
	return specs
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
	state.mu.RLock()
	defer state.mu.RUnlock()
	state.handler.ServeHTTP(w, r)
}

func (l *liveHandler) Current() *runtimeState {
	state, _ := l.runtime.Load().(*runtimeState)
	return state
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

func watchConfig(configPath, _ string, _ string, interval time.Duration, live *liveHandler) {
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
		nextCfg, loadErr := config.Load(configPath)
		if loadErr != nil {
			if !haveRejected || nextHash != lastRejected {
				slog.Warn("config reload rejected; keeping previous valid config", "err", loadErr)
				lastRejected = nextHash
				haveRejected = true
			}
			continue
		}
		state := live.Current()
		if state == nil {
			if !haveRejected || nextHash != lastRejected {
				slog.Warn("config reload rejected; runtime unavailable")
				lastRejected = nextHash
				haveRejected = true
			}
			continue
		}
		if err := state.ReloadConfig(nextCfg); err != nil {
			if !haveRejected || nextHash != lastRejected {
				slog.Warn("config reload rejected; keeping previous valid config", "err", err)
				lastRejected = nextHash
				haveRejected = true
			}
			continue
		}
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
