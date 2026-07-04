package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	DecisionApprove = "approve"
	DecisionDeny    = "deny"
)

type Request struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Action    string         `json:"action"`
	Rule      string         `json:"rule,omitempty"`
	Summary   string         `json:"summary"`
	Details   map[string]any `json:"details,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type pending struct {
	Request
	ch chan string
}

type Manager struct {
	mu               sync.Mutex
	next             int64
	pending          map[string]*pending
	timeout          time.Duration
	defaultOnTimeout string
	printf           func(string, ...any) (int, error)
}

func NewManager(timeout time.Duration) *Manager {
	return NewManagerWithDefault(timeout, DecisionDeny)
}

func NewManagerWithDefault(timeout time.Duration, defaultOnTimeout string) *Manager {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if defaultOnTimeout != DecisionApprove {
		defaultOnTimeout = DecisionDeny
	}
	return &Manager{pending: map[string]*pending{}, timeout: timeout, defaultOnTimeout: defaultOnTimeout, printf: fmt.Printf}
}

func (m *Manager) Request(ctx context.Context, req Request) (string, error) {
	m.mu.Lock()
	m.next++
	req.ID = fmt.Sprintf("approval-%d", m.next)
	req.CreatedAt = time.Now().UTC()
	p := &pending{Request: req, ch: make(chan string, 1)}
	m.pending[req.ID] = p
	m.mu.Unlock()
	defer m.remove(req.ID)

	timer := time.NewTimer(m.timeout)
	defer timer.Stop()
	if m.printf == nil {
		m.printf = fmt.Printf
	}
	go func(printf func(string, ...any) (int, error)) {
		_, _ = printf("approval required: id=%s kind=%s action=%s summary=%s\n", req.ID, req.Kind, req.Action, req.Summary)
	}(m.printf)
	select {
	case decision := <-p.ch:
		if decision == DecisionApprove {
			return decision, nil
		}
		return decision, fmt.Errorf("approval denied: %s", req.ID)
	case <-ctx.Done():
		return DecisionDeny, ctx.Err()
	case <-timer.C:
		if m.defaultOnTimeout == DecisionApprove {
			return DecisionApprove, nil
		}
		return DecisionDeny, fmt.Errorf("approval timed out: %s", req.ID)
	}
}

func (m *Manager) List() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, 0, len(m.pending))
	for _, p := range m.pending {
		out = append(out, p.Request)
	}
	return out
}

func (m *Manager) Decide(id, decision string) bool {
	m.mu.Lock()
	p := m.pending[id]
	m.mu.Unlock()
	if p == nil {
		return false
	}
	select {
	case p.ch <- decision:
		return true
	default:
		return false
	}
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.pending, id)
	m.mu.Unlock()
}

func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/approvals", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"pending": m.List()})
	})
	mux.HandleFunc("/approvals/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/approvals/"), "/")
		if len(parts) != 2 || parts[0] == "" {
			http.Error(w, "bad approval path", http.StatusBadRequest)
			return
		}
		decision := ""
		switch parts[1] {
		case "approve":
			decision = DecisionApprove
		case "deny":
			decision = DecisionDeny
		default:
			http.Error(w, "bad approval decision", http.StatusBadRequest)
			return
		}
		if !m.Decide(parts[0], decision) {
			http.Error(w, "approval not found", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "id": parts[0], "decision": decision})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
