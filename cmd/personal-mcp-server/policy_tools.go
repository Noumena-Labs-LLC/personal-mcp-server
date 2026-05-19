package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

type commandExplainArgs struct {
	Exec string   `json:"exec"`
	Args []string `json:"args"`
}

type fileExplainArgs struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Cwd       string `json:"cwd"`
}

func commandExplainTool(cfg *config.Config) func(json.RawMessage) (any, error) {
	return func(raw json.RawMessage) (any, error) {
		var a commandExplainArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Exec) == "" {
			return nil, fmt.Errorf("exec is required")
		}
		decision, err := policy.DecideCommand(cfg.CommandPolicy, a.Exec, a.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"exec": a.Exec, "args": a.Args, "decision": decision}, nil
	}
}

func fileExplainTool(ft *fsx.Tools) func(json.RawMessage) (any, error) {
	return func(raw json.RawMessage) (any, error) {
		var a fileExplainArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Operation) == "" {
			return nil, fmt.Errorf("operation is required")
		}
		if strings.TrimSpace(a.Path) == "" {
			return nil, fmt.Errorf("path is required")
		}
		resolved, display, err := ft.ResolveForTool(a.Path, a.Cwd)
		if err != nil {
			return nil, err
		}
		explanation, err := ft.ExplainFilePolicy(a.Operation, display, resolved)
		if err != nil {
			return nil, err
		}
		return map[string]any{"operation": a.Operation, "path": display, "resolved_path": resolved, "cwd": a.Cwd, "decision": explanation["effective"], "policy": explanation}, nil
	}
}

func pathModeSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"auto", "absolute", "root_relative", "cwd_relative"}, "description": "Optional path interpretation mode. Defaults to auto."}
}
