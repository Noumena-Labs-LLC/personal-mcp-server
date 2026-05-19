package mcphttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/audit"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("PERSONAL_MCP_TOKEN", "token")
	aud, err := audit.New("", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 3929, Endpoint: "/mcp", AuthTokenEnv: "PERSONAL_MCP_TOKEN", ValidateOrigin: true, AllowedOrigins: []string{"http://127.0.0.1"}}} //nolint:gosec // test token env var name is not a credential.
	s := New(cfg, aud, approval.NewManager(120*time.Second), "test-version")
	s.Register(Tool{Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"}, Handler: func(_ json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil }})
	s.RegisterPrompt(Prompt{Name: "guide", Description: "guide", Template: "do things safely"})
	return s
}

func doReq(s *Server, method, path, body string, auth bool) *httptest.ResponseRecorder {
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

func TestSecurityMiddlewareRequiresAuthAndHost(t *testing.T) {
	s := testServer(t)
	rr := doReq(s, http.MethodPost, "/mcp", `{}`, false)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Host = "evil.test"
	req.Header.Set("Authorization", "Bearer token")
	rr = httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestInitializeToolsAndPrompts(t *testing.T) {
	s := testServer(t)
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"prompts/list","params":{}}`,
		`{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"guide"}}`,
	} {
		rr := doReq(s, http.MethodPost, "/mcp", body, true)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d: %s", body, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"jsonrpc":"2.0"`) {
			t.Fatalf("missing jsonrpc response: %s", rr.Body.String())
		}
	}
}

func TestNotificationIsAcceptedBySDKHandler(t *testing.T) {
	s := testServer(t)
	rr := doReq(s, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, true)
	if rr.Code < 200 || rr.Code >= 300 {
		t.Fatalf("expected 2xx, got %d: %s", rr.Code, rr.Body.String())
	}
}
