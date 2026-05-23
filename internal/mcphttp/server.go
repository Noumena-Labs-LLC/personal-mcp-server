// Package mcphttp wires personal-mcp-server's tools into the official MCP Go SDK
// and exposes the SDK's Streamable HTTP handler behind local security middleware.
package mcphttp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/audit"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

type ToolFunc func(json.RawMessage) (any, error)
type ToolContextFunc func(context.Context, json.RawMessage) (any, error)

type Tool struct {
	Name           string
	Description    string
	InputSchema     any
	Handler         ToolFunc
	ContextHandler  ToolContextFunc
}

type Prompt struct {
	Name        string
	Description string
	Template    string
}

type Resource struct {
	URI         string
	Name        string
	Title       string
	Description string
	MIMEType    string
	Handler     func(string) (string, string, error)
}

type ResourceTemplate struct {
	URITemplate string
	Name        string
	Title       string
	Description string
	MIMEType    string
	Handler     func(string) (string, string, error)
}

type Server struct {
	Cfg       *config.Config
	Audit     *audit.Logger
	Approvals *approval.Manager
	MCP       *mcp.Server
}

func New(c *config.Config, a *audit.Logger, approvals *approval.Manager, serverVersion string) *Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "personal-mcp-server", Version: serverVersion}, &mcp.ServerOptions{
		Instructions: defaultInstructions(),
		Capabilities: &mcp.ServerCapabilities{}, // disable default logging; tools/prompts are inferred as registered.
	})
	return &Server{Cfg: c, Audit: a, Approvals: approvals, MCP: server}
}

func (t Tool) call(ctx context.Context, args json.RawMessage) (any, error) {
	if t.ContextHandler != nil {
		return t.ContextHandler(ctx, args)
	}
	if t.Handler == nil {
		return nil, fmt.Errorf("tool %q has no handler", t.Name)
	}
	return t.Handler(args)
}

func (s *Server) Register(t Tool) { //nolint:nilerr // MCP tool errors are returned as CallToolResult.IsError, per protocol.
	s.MCP.AddTool(&mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.InputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		started := time.Now()
		args := json.RawMessage(`{}`)
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			args = req.Params.Arguments
		}

		handlerStarted := time.Now()
		out, err := t.call(ctx, args)
		handlerDuration := time.Since(handlerStarted)

		fields := map[string]any{
			"ok":         err == nil,
			"handler_ms": handlerDuration.Milliseconds(),
		}
		if err != nil {
			fields["error"] = err.Error()
			duration := time.Since(started)
			fields["duration_ms"] = duration.Milliseconds()
			auditStarted := time.Now()
			s.Audit.Event(t.Name, fields)
			fields["audit_ms"] = time.Since(auditStarted).Milliseconds()
			s.logToolLatency(t.Name, duration, err, len(args), len(err.Error()), fields)
			return callToolError(err.Error()), nil //nolint:nilerr // MCP tool failures are encoded in CallToolResult.IsError.
		}

		marshalStarted := time.Now()
		b, marshalErr := json.MarshalIndent(out, "", "  ")
		marshalDuration := time.Since(marshalStarted)
		duration := time.Since(started)
		fields["marshal_ms"] = marshalDuration.Milliseconds()
		fields["duration_ms"] = duration.Milliseconds()
		if marshalErr != nil {
			fields["ok"] = false
			fields["error"] = marshalErr.Error()
			auditStarted := time.Now()
			s.Audit.Event(t.Name, fields)
			fields["audit_ms"] = time.Since(auditStarted).Milliseconds()
			s.logToolLatency(t.Name, duration, marshalErr, len(args), len(marshalErr.Error()), fields)
			return callToolError(marshalErr.Error()), nil //nolint:nilerr // MCP tool failures are encoded in CallToolResult.IsError.
		}

		auditStarted := time.Now()
		s.Audit.Event(t.Name, fields)
		fields["audit_ms"] = time.Since(auditStarted).Milliseconds()
		s.logToolLatency(t.Name, duration, nil, len(args), len(b), fields)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
	})
}

func (s *Server) logToolLatency(toolName string, duration time.Duration, err error, requestBytes, responseBytes int, fields map[string]any) {
	if s == nil || s.Cfg == nil {
		return
	}
	slow := time.Duration(s.Cfg.ServerLogging.ToolSlowMS) * time.Millisecond
	verySlow := time.Duration(s.Cfg.ServerLogging.ToolVerySlowMS) * time.Millisecond
	if verySlow > 0 && duration >= verySlow {
		s.logSlowToolCall(slog.LevelError, "tool_call_very_slow", toolName, duration, verySlow, err, requestBytes, responseBytes, fields)
		return
	}
	if slow > 0 && duration >= slow {
		s.logSlowToolCall(slog.LevelWarn, "tool_call_slow", toolName, duration, slow, err, requestBytes, responseBytes, fields)
	}
}

func (s *Server) logSlowToolCall(level slog.Level, event, toolName string, duration, threshold time.Duration, err error, requestBytes, responseBytes int, fields map[string]any) {
	attrs := []slog.Attr{
		slog.String("event", event),
		slog.String("tool", toolName),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.Int64("threshold_ms", threshold.Milliseconds()),
		slog.Bool("ok", err == nil),
		slog.Int("request_bytes", requestBytes),
		slog.Int("response_bytes", responseBytes),
	}
	for _, key := range []string{"handler_ms", "marshal_ms", "audit_ms"} {
		if value, ok := fields[key]; ok {
			if n, ok := value.(int64); ok {
				attrs = append(attrs, slog.Int64(key, n))
			}
		}
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	slog.LogAttrs(context.Background(), level, "tool call exceeded latency threshold", attrs...)
}

func callToolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: message},
		},
	}
}

func (s *Server) RegisterResource(r Resource) {
	s.MCP.AddResource(&mcp.Resource{
		URI:         r.URI,
		Name:        r.Name,
		Title:       r.Title,
		Description: r.Description,
		MIMEType:    r.MIMEType,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		text, mimeType, err := r.Handler(req.Params.URI)
		if err != nil {
			return nil, err
		}
		if mimeType == "" {
			mimeType = r.MIMEType
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: mimeType, Text: text}}}, nil
	})
}

func (s *Server) RegisterResourceTemplate(t ResourceTemplate) {
	s.MCP.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: t.URITemplate,
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		MIMEType:    t.MIMEType,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		text, mimeType, err := t.Handler(req.Params.URI)
		if err != nil {
			return nil, err
		}
		if mimeType == "" {
			mimeType = t.MIMEType
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: mimeType, Text: text}}}, nil
	})
}

func (s *Server) RegisterPrompt(p Prompt) {
	s.MCP.AddPrompt(&mcp.Prompt{Name: p.Name, Description: p.Description}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: p.Description,
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.Template}}},
		}, nil
	})
}

func (s *Server) Handler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return s.MCP
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle(s.Cfg.Server.Endpoint, mcpHandler)
	if s.Approvals != nil {
		mux.Handle("/approvals", s.Approvals.Handler())
		mux.Handle("/approvals/", s.Approvals.Handler())
	}
	return s.securityMiddleware(mux)
}

func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedA := fmt.Sprintf("127.0.0.1:%d", s.Cfg.Server.Port)
		expectedB := fmt.Sprintf("localhost:%d", s.Cfg.Server.Port)
		if r.Host != expectedA && r.Host != expectedB {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		if s.Cfg.Server.ValidateOrigin {
			origin := r.Header.Get("Origin")
			if origin != "" && !contains(s.Cfg.Server.AllowedOrigins, origin) {
				http.Error(w, "bad origin", http.StatusForbidden)
				return
			}
		}
		if r.URL.Path != "/healthz" {
			got := r.Header.Get("Authorization")
			want := "Bearer " + s.Cfg.AuthToken()
			if got != want {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if accept := r.Header.Get("Accept"); accept == "" || accept == "*/*" {
				// The SDK's Streamable HTTP transport expects clients to advertise JSON or SSE.
				r.Header.Set("Accept", "application/json, text/event-stream")
			}
			if r.Method == http.MethodPost {
				ct := r.Header.Get("Content-Type")
				if ct == "" {
					r.Header.Set("Content-Type", "application/json")
				} else if !strings.HasPrefix(ct, "application/json") {
					http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func defaultInstructions() string {
	return `personal-mcp-server is a localhost-only, config-scoped MCP server. Start by calling server_info and policy_describe or reading personal-mcp://server, personal-mcp://policy, personal-mcp://guide/index, and personal-mcp://guide/tools. Work only inside configured roots. Prefer resources for read-only context and tools for actions. Search before reading broadly, use fs_get_file_info for unfamiliar files, read bounded line ranges, avoid whole_file=true on large files unless explicitly needed, and never request denied secret files. For edits, use fs_apply_patch or fs_apply_unified_patch for scoped changes; review returned diffs and warnings when useful, then verify the result. Use fs_create_file only for new files, fs_create_dir for directory creation, and fs_replace_file for intentional whole-file overwrites. Use project_info, workflow_list, and personal-mcp://guide/project-config for project workflow discovery. Use git_diff plus cmd_run_named or cmd_run_argv for verification when configured. If a policy returns prompt, explain why the operation is needed and ask the local user to approve through the approval CLI or local endpoint; no native OS dialog is shown. The server hot-reloads valid TOML changes and keeps the previous config if reload fails validation. The server enforces roots, deny rules, file policy, command policy, size limits, and no shell strings.`
}
