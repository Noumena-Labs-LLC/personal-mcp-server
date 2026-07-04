package mcphttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/audit"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func newCoverageServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	aud, err := audit.New("", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:           "127.0.0.1",
			Port:           3929,
			Endpoint:       "/mcp",
			AuthTokenEnv:   "PERSONAL_MCP_TOKEN",
			ValidateOrigin: true,
			AllowedOrigins: []string{"http://127.0.0.1"},
		},
	}
	s := New(cfg, aud, approval.NewManager(120*time.Second), "test-version")
	return s
}

func doMCPReq(s *Server, method, path, body string, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:3929"
	if auth {
		req.Header.Set("Authorization", "Bearer token")
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func TestResourcePromptAndMiddlewareCoverage(t *testing.T) {
	s := newCoverageServer(t)

	s.RegisterResource(Resource{
		URI:         "personal-mcp://demo",
		Name:        "demo",
		Title:       "demo",
		Description: "demo",
		MIMEType:    "text/plain",
		Handler: func(uri string) (string, string, error) {
			return "payload:" + uri, "", nil
		},
	})
	s.RegisterResourceTemplate(ResourceTemplate{
		URITemplate: "personal-mcp://demo/{path}",
		Name:        "demo-template",
		Title:       "demo-template",
		Description: "demo-template",
		MIMEType:    "text/plain",
		Handler: func(uri string) (string, string, error) {
			return "templated:" + uri, "text/markdown", nil
		},
	})

	rr := doMCPReq(s, http.MethodGet, "/healthz", "", false)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("healthz response = %d %s", rr.Code, rr.Body.String())
	}

	rr = doMCPReq(s, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"personal-mcp://demo"}}`, true)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "payload:personal-mcp://demo") {
		t.Fatalf("resource read response = %d %s", rr.Code, rr.Body.String())
	}
	rr = doMCPReq(s, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"personal-mcp://demo/sample.txt"}}`, true)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "templated:personal-mcp://demo/sample.txt") {
		t.Fatalf("resource template response = %d %s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Host = "evil.test"
	req.Header.Set("Authorization", "Bearer token")
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected bad host rejection, got %d", rr.Code)
	}

	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3929"
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Origin", "http://evil.test")
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected bad origin rejection, got %d", rr.Code)
	}

	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:3929"
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "text/plain")
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected content-type rejection, got %d", rr.Code)
	}

	if got := callToolError("boom"); !got.IsError || len(got.Content) == 0 {
		t.Fatalf("callToolError = %#v", got)
	}

	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	var logBuf strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	s.logSlowToolCall(slog.LevelWarn, "tool_call_slow", "demo", 25*time.Millisecond, 10*time.Millisecond, errors.New("boom"), 11, 12, map[string]any{"handler_ms": int64(1), "audit_ms": int64(2)})
	if !strings.Contains(logBuf.String(), "tool_call_slow") || !strings.Contains(logBuf.String(), "boom") {
		t.Fatalf("slow log output = %s", logBuf.String())
	}

	s.Register(Tool{
		Name:        "echo",
		Description: "echo",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(_ json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})
	rr = doMCPReq(s, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}`, true)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "ok") {
		t.Fatalf("tool call response = %d %s", rr.Code, rr.Body.String())
	}
}
