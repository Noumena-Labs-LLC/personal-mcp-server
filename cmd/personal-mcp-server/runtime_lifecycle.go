package main

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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
	cfg     *config.Config
	handler http.Handler
	audit   *audit.Logger
	runner  *shell.Runner

	inFlight   atomic.Int64
	draining   atomic.Bool
	idleClosed atomic.Bool
	closed     atomic.Bool
	idle       chan struct{}
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
	return &runtimeState{cfg: cfg, handler: srv.Handler(), audit: aud, runner: runner, idle: make(chan struct{})}, nil
}

func (r *runtimeState) acquire() bool {
	if r == nil {
		return false
	}
	r.inFlight.Add(1)
	if r.draining.Load() {
		r.release()
		return false
	}
	return true
}

func (r *runtimeState) release() {
	if r == nil {
		return
	}
	if r.inFlight.Add(-1) == 0 && r.draining.Load() {
		r.closeIdle()
	}
}

func (r *runtimeState) drain() {
	if r == nil {
		return
	}
	r.draining.Store(true)
	if r.inFlight.Load() == 0 {
		r.closeIdle()
	}
}

func (r *runtimeState) closeIdle() {
	if r.idleClosed.CompareAndSwap(false, true) {
		close(r.idle)
	}
}

func (r *runtimeState) Close() {
	if r == nil {
		return
	}
	r.drain()
	<-r.idle
	if !r.closed.CompareAndSwap(false, true) {
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
	state := l.Current()
	if state == nil || state.handler == nil {
		http.Error(w, "handler unavailable", http.StatusServiceUnavailable)
		return
	}
	if !state.acquire() {
		http.Error(w, "handler draining", http.StatusServiceUnavailable)
		return
	}
	defer state.release()
	state.handler.ServeHTTP(w, r)
}

func (l *liveHandler) Current() *runtimeState {
	if l == nil {
		return nil
	}
	state, _ := l.runtime.Load().(*runtimeState)
	return state
}

func (l *liveHandler) Swap(state *runtimeState) *runtimeState {
	previous, _ := l.runtime.Swap(state).(*runtimeState)
	if previous != nil {
		previous.drain()
	}
	return previous
}

func (l *liveHandler) Close() {
	state := l.Current()
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
		if previous != nil {
			go previous.Close()
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
