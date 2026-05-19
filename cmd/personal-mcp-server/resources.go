package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/mcphttp"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

func registerResources(s *mcphttp.Server, cfg *config.Config, ft *fsx.Tools) {
	jsonResource := func(_ string, v any) (string, string, error) {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", "", err
		}
		return string(b), "application/json", nil
	}
	registerTextResource := func(r guideResource) {
		s.RegisterResource(mcphttp.Resource{
			URI: r.URI, Name: r.Name, Title: r.Title, Description: r.Description, MIMEType: "text/markdown",
			Handler: func(string) (string, string, error) {
				content, err := readGuideFile(r.File)
				if err != nil {
					return "", "", err
				}
				return content, "text/markdown", nil
			},
		})
	}
	s.RegisterResource(mcphttp.Resource{
		URI: "personal-mcp://server", Name: "server", Title: "Server information", Description: "personal MCP server server version, module, transport, and feature summary.", MIMEType: "application/json",
		Handler: func(uri string) (string, string, error) {
			return jsonResource(uri, serverInfo(cfg))
		},
	})
	s.RegisterResource(mcphttp.Resource{
		URI: "personal-mcp://roots", Name: "roots", Title: "Configured roots", Description: "Configured filesystem roots available to personal MCP server.", MIMEType: "application/json",
		Handler: func(uri string) (string, string, error) {
			return jsonResource(uri, map[string]any{"roots": ft.Sandbox.Roots})
		},
	})
	s.RegisterResource(mcphttp.Resource{
		URI: "personal-mcp://policy", Name: "policy", Title: "Effective policy", Description: "Effective tool, filesystem, command, and approval policy.", MIMEType: "application/json",
		Handler: func(uri string) (string, string, error) {
			return jsonResource(uri, policy.Describe(cfg, ft.Sandbox.Roots, version))
		},
	})
	for _, r := range guideResources() {
		registerTextResource(r)
	}
	for _, r := range docResources() {
		registerTextResource(r)
	}
	s.RegisterResourceTemplate(mcphttp.ResourceTemplate{
		URITemplate: "personal-mcp://file/{path}", Name: "file", Title: "Read file", Description: "Read a bounded text file inside configured roots.", MIMEType: "text/plain",
		Handler: func(uri string) (string, string, error) {
			path, err := resourcePath(uri, "file")
			if err != nil {
				return "", "", err
			}
			out, err := ft.ReadFile(mustJSON(map[string]any{"path": path, "start_line": 1, "max_lines": 300}))
			if err != nil {
				return "", "", err
			}
			m, ok := out.(map[string]any)
			if !ok {
				return "", "", fmt.Errorf("unexpected read_file result")
			}
			content, _ := m["content"].(string)
			return content, "text/plain", nil
		},
	})
	s.RegisterResourceTemplate(mcphttp.ResourceTemplate{
		URITemplate: "personal-mcp://tree/{path}", Name: "tree", Title: "List directory", Description: "List a directory inside configured roots.", MIMEType: "application/json",
		Handler: func(uri string) (string, string, error) {
			path, err := resourcePath(uri, "tree")
			if err != nil {
				return "", "", err
			}
			out, err := ft.ListDir(mustJSON(map[string]any{"path": path, "max_entries": 200}))
			if err != nil {
				return "", "", err
			}
			return jsonResource(uri, out)
		},
	})
	s.RegisterResourceTemplate(mcphttp.ResourceTemplate{
		URITemplate: "personal-mcp://info/{path}", Name: "info", Title: "File info", Description: "Read file or directory metadata inside configured roots.", MIMEType: "application/json",
		Handler: func(uri string) (string, string, error) {
			path, err := resourcePath(uri, "info")
			if err != nil {
				return "", "", err
			}
			out, err := ft.GetFileInfo(mustJSON(map[string]any{"path": path}))
			if err != nil {
				return "", "", err
			}
			return jsonResource(uri, out)
		},
	})
}

func resourcePath(rawURI, host string) (string, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return "", err
	}
	if u.Scheme != "personal-mcp" || u.Host != host {
		return "", fmt.Errorf("unexpected resource URI %q", rawURI)
	}
	p, err := url.PathUnescape(strings.TrimPrefix(u.Path, "/"))
	if err != nil {
		return "", err
	}
	if p == "" {
		p = "."
	}
	return p, nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type resourceReadArgs struct {
	URI string `json:"uri"`
	Cwd string `json:"cwd"`
}

type guideReadArgs struct {
	Name    string `json:"name"`
	Section string `json:"section"`
}

func guideCatalog() []map[string]any {
	guides := make([]map[string]any, 0, len(guideResources())+len(docResources()))
	appendGuide := func(r guideResource) {
		entry := map[string]any{
			"name":        r.Name,
			"uri":         r.URI,
			"title":       r.Title,
			"description": r.Description,
		}
		if content, err := readGuideFile(r.File); err == nil {
			entry["sections"] = fsx.ParseMarkdownSections(content)
		}
		guides = append(guides, entry)
	}
	for _, r := range guideResources() {
		appendGuide(r)
	}
	for _, r := range docResources() {
		appendGuide(r)
	}
	return guides
}

func guideByName(name string) (guideResource, bool) {
	normalized := strings.TrimSpace(strings.ToLower(name))
	normalized = strings.TrimPrefix(normalized, "personal-mcp://")
	normalized = strings.TrimPrefix(normalized, "guide/")
	normalized = strings.TrimPrefix(normalized, "docs/")
	normalized = strings.ReplaceAll(normalized, "_", "-")
	aliases := map[string]string{
		"project-config":  "project-config-guide",
		"setup":           "setup-guide",
		"setup-macos":     "macos-setup-guide",
		"setup-linux":     "linux-setup-guide",
		"claude-desktop":  "claude-desktop-guide",
		"services":        "service-guide",
		"logs":            "logs-guide",
		"troubleshooting": "troubleshooting-guide",
		"readme":          "readme",
		"tools":           "tool-guide",
		"quality":         "quality-docs",
		"security":        "security-docs",
		"threat-model":    "threat-model-docs",
	}
	if alias, ok := aliases[normalized]; ok {
		normalized = alias
	}
	for _, r := range guideResources() {
		if normalized == r.Name || normalized == strings.TrimPrefix(r.URI, "personal-mcp://") || normalized == strings.TrimPrefix(r.URI, "personal-mcp://guide/") {
			return r, true
		}
	}
	for _, r := range docResources() {
		if normalized == r.Name || normalized == strings.TrimPrefix(r.URI, "personal-mcp://") || normalized == strings.TrimPrefix(r.URI, "personal-mcp://docs/") {
			return r, true
		}
	}
	return guideResource{}, false
}

func guideReadTool(raw json.RawMessage) (any, error) {
	var a guideReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Name) == "" {
		return nil, fmt.Errorf("name is required; call guide_list to discover available guides")
	}
	r, ok := guideByName(a.Name)
	if !ok {
		return nil, fmt.Errorf("unknown guide %q; call guide_list to discover available guides", a.Name)
	}
	content, err := readGuideFile(r.File)
	if err != nil {
		return nil, err
	}
	sections := fsx.ParseMarkdownSections(content)
	result := map[string]any{"name": r.Name, "uri": r.URI, "title": r.Title, "mime_type": "text/markdown", "sections": sections}
	if strings.TrimSpace(a.Section) == "" {
		result["content"] = content
		return result, nil
	}
	section, err := fsx.FindMarkdownSection(sections, a.Section)
	if err != nil {
		return nil, err
	}
	result["section"] = section
	result["content"] = fsx.MarkdownSectionContent(content, section)
	return result, nil
}

func resourceCatalog() []map[string]string {
	resources := []map[string]string{
		{"uri": "personal-mcp://server", "name": "server", "mime_type": "application/json", "description": "Server version, module, transport, features, and large-file guidance."},
		{"uri": "personal-mcp://roots", "name": "roots", "mime_type": "application/json", "description": "Configured filesystem roots."},
		{"uri": "personal-mcp://policy", "name": "policy", "mime_type": "application/json", "description": "Effective tool, file, command, and approval policy."},
	}
	appendText := func(r guideResource) {
		resources = append(resources, map[string]string{"uri": r.URI, "name": r.Name, "mime_type": "text/markdown", "description": r.Description})
	}
	for _, r := range guideResources() {
		appendText(r)
	}
	for _, r := range docResources() {
		appendText(r)
	}
	resources = append(resources,
		map[string]string{"uri": "personal-mcp://file/{path}", "name": "file", "mime_type": "text/plain", "description": "Read a bounded file. Example: personal-mcp://file/project/README.md"},
		map[string]string{"uri": "personal-mcp://tree/{path}", "name": "tree", "mime_type": "application/json", "description": "List a directory. Example: personal-mcp://tree/project"},
		map[string]string{"uri": "personal-mcp://info/{path}", "name": "info", "mime_type": "application/json", "description": "Read file or directory metadata. Example: personal-mcp://info/project/go.mod"},
	)
	return resources
}

func resourceReadTool(ft *fsx.Tools, cfg *config.Config) func(json.RawMessage) (any, error) {
	return func(raw json.RawMessage) (any, error) {
		var a resourceReadArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.URI) == "" {
			return nil, fmt.Errorf("uri is required; call resource_list to discover supported personal-mcp:// resources")
		}
		u, err := url.Parse(a.URI)
		if err != nil {
			return nil, err
		}
		if u.Scheme != "personal-mcp" {
			return nil, fmt.Errorf("unsupported resource URI scheme %q", u.Scheme)
		}
		switch u.Host {
		case "server":
			return map[string]any{"uri": a.URI, "mime_type": "application/json", "content": serverInfo(cfg)}, nil
		case "roots":
			return map[string]any{"uri": a.URI, "mime_type": "application/json", "content": map[string]any{"roots": ft.Sandbox.Roots}}, nil
		case "policy":
			return map[string]any{"uri": a.URI, "mime_type": "application/json", "content": policy.Describe(cfg, ft.Sandbox.Roots, version)}, nil
		case "guide", "docs":
			path := strings.TrimPrefix(u.Path, "/")
			if _, ok := guideResourceByPath(u.Host, path); !ok {
				return nil, fmt.Errorf("unknown %s resource %q", u.Host, a.URI)
			}
			file, _ := guideByURI(a.URI)
			content, err := readGuideFile(file)
			if err != nil {
				return nil, err
			}
			return map[string]any{"uri": a.URI, "mime_type": "text/markdown", "content": content}, nil
		case "file":
			path, err := resourcePath(a.URI, "file")
			if err != nil {
				return nil, err
			}
			out, err := ft.ReadFile(mustJSON(map[string]any{"path": path, "cwd": a.Cwd, "start_line": 1, "max_lines": 300}))
			if err != nil {
				return nil, err
			}
			return map[string]any{"uri": a.URI, "mime_type": "text/plain", "content": out}, nil
		case "tree":
			path, err := resourcePath(a.URI, "tree")
			if err != nil {
				return nil, err
			}
			out, err := ft.ListDir(mustJSON(map[string]any{"path": path, "cwd": a.Cwd, "max_entries": 200}))
			if err != nil {
				return nil, err
			}
			return map[string]any{"uri": a.URI, "mime_type": "application/json", "content": out}, nil
		case "info":
			path, err := resourcePath(a.URI, "info")
			if err != nil {
				return nil, err
			}
			out, err := ft.GetFileInfo(mustJSON(map[string]any{"path": path, "cwd": a.Cwd}))
			if err != nil {
				return nil, err
			}
			return map[string]any{"uri": a.URI, "mime_type": "application/json", "content": out}, nil
		default:
			return nil, fmt.Errorf("unknown personal MCP resource %q", a.URI)
		}
	}
}

type guideResource struct {
	URI         string
	Name        string
	Title       string
	Description string
	File        string
}

func guideResources() []guideResource {
	return []guideResource{
		{URI: "personal-mcp://guide/index", Name: "guide-index", Title: "Guide index", Description: "Index of LLM-readable personal MCP server setup, tool, and project guidance.", File: "index.md"},
		{URI: "personal-mcp://guide/tools", Name: "tool-guide", Title: "Tool guide", Description: "Guide for using personal MCP server tools and resources safely.", File: "tools.md"},
		{URI: "personal-mcp://guide/project-config", Name: "project-config-guide", Title: "Project config guide", Description: "Guide for creating and understanding .personal-mcp-server.toml project configs.", File: "project_config.md"},
		{URI: "personal-mcp://guide/setup", Name: "setup-guide", Title: "Setup guide", Description: "General setup workflow for personal MCP server.", File: "setup.md"},
		{URI: "personal-mcp://guide/setup-macos", Name: "macos-setup-guide", Title: "macOS setup guide", Description: "macOS local setup and user LaunchAgent guidance.", File: "setup_macos.md"},
		{URI: "personal-mcp://guide/setup-linux", Name: "linux-setup-guide", Title: "Linux setup guide", Description: "Linux local setup and systemd user service guidance.", File: "setup_linux.md"},
		{URI: "personal-mcp://guide/claude-desktop", Name: "claude-desktop-guide", Title: "Claude Desktop guide", Description: "Guide for connecting Claude Desktop through mcp-remote.", File: "claude_desktop.md"},
		{URI: "personal-mcp://guide/services", Name: "service-guide", Title: "Service guide", Description: "User-level macOS LaunchAgent and Linux systemd service guidance.", File: "services.md"},
		{URI: "personal-mcp://guide/logs", Name: "logs-guide", Title: "Logs and rotation guide", Description: "Audit log rotation and service stdout/stderr guidance.", File: "logs.md"},
		{URI: "personal-mcp://guide/troubleshooting", Name: "troubleshooting-guide", Title: "Troubleshooting guide", Description: "Common local setup and runtime troubleshooting steps.", File: "troubleshooting.md"},
	}
}

func docResources() []guideResource {
	return []guideResource{
		{URI: "personal-mcp://docs/readme", Name: "readme", Title: "README summary", Description: "LLM-readable README summary.", File: "readme.md"},
		{URI: "personal-mcp://docs/project-configs", Name: "project-configs-docs", Title: "Project config docs summary", Description: "LLM-readable project config documentation summary.", File: "docs_project_configs.md"},
		{URI: "personal-mcp://docs/tools", Name: "tools-docs", Title: "Tool docs summary", Description: "LLM-readable tool documentation summary.", File: "docs_tools.md"},
		{URI: "personal-mcp://docs/security", Name: "security-docs", Title: "Security docs summary", Description: "LLM-readable security documentation summary.", File: "docs_security.md"},
		{URI: "personal-mcp://docs/threat-model", Name: "threat-model-docs", Title: "Threat model summary", Description: "LLM-readable threat model summary.", File: "docs_threat_model.md"},
		{URI: "personal-mcp://docs/quality", Name: "quality-docs", Title: "Quality docs summary", Description: "LLM-readable quality and test documentation summary.", File: "docs_quality.md"},
		{URI: "personal-mcp://docs/release", Name: "release-docs", Title: "Release docs summary", Description: "LLM-readable release packaging and distribution summary.", File: "docs_release.md"},
		{URI: "personal-mcp://docs/audit", Name: "audit-docs", Title: "Audit docs summary", Description: "LLM-readable code quality, security, documentation, and release-readiness audit summary.", File: "docs_audit.md"},
	}
}

func readGuideFile(file string) (string, error) {
	b, err := guideFS.ReadFile("guides/" + file)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func guideByURI(uri string) (string, bool) {
	for _, r := range guideResources() {
		if r.URI == uri {
			return r.File, true
		}
	}
	for _, r := range docResources() {
		if r.URI == uri {
			return r.File, true
		}
	}
	return "", false
}

func guideResourceByPath(host, path string) (guideResource, bool) {
	uri := "personal-mcp://" + host + "/" + path
	for _, r := range guideResources() {
		if r.URI == uri {
			return r, true
		}
	}
	for _, r := range docResources() {
		if r.URI == uri {
			return r, true
		}
	}
	return guideResource{}, false
}
