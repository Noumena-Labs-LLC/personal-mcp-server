package mcphttp

import (
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func TestRegisterModule(t *testing.T) {
	s := New(&config.Config{}, nil, nil, "test-version")
	called := false
	s.RegisterModule(ModuleFunc(func(got *Server) {
		if got != s {
			t.Fatalf("module received unexpected server")
		}
		called = true
	}))
	if !called {
		t.Fatalf("expected module function to be called")
	}

	// Nil modules are intentionally ignored so optional feature bundles can be wired conditionally.
	s.RegisterModule(nil)
}
