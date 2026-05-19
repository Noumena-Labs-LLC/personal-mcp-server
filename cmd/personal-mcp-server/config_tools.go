package main

import (
	"encoding/json"
	"strings"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
)

func configValidateTool() func(json.RawMessage) (any, error) {
	return func(raw json.RawMessage) (any, error) {
		var a struct {
			Path string `json:"path"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(a.Path) == "" {
			a.Path = defaultConfigPath()
		}
		loaded, validationErrors := loadConfigForValidation(a.Path)
		if len(validationErrors) > 0 {
			return configValidationFailure(a.Path, validationErrors), nil
		}
		warnings := []string{}
		if len(loaded.Roots) == 0 {
			warnings = append(warnings, "no roots configured")
		}
		if loaded.Defaults.AllowEverything {
			warnings = append(warnings, "allow_everything is enabled; explicit denies and hard safety boundaries still apply")
		}
		return map[string]any{"ok": true, "path": a.Path, "errors": []string{}, "warnings": warnings, "canonical_suggestions": []string{"Prefer snake_case TOML keys even though safe aliases are accepted."}}, nil
	}
}

func loadConfigForValidation(path string) (loaded *config.Config, validationErrors []string) {
	loaded, err := config.Load(path)
	if err != nil {
		return nil, []string{err.Error()}
	}
	return loaded, nil
}

func configValidationFailure(path string, validationErrors []string) map[string]any {
	return map[string]any{"ok": false, "path": path, "errors": validationErrors, "warnings": []string{}, "canonical_suggestions": []string{"Use canonical snake_case TOML keys in generated configs."}}
}

func configExplainTool(cfg *config.Config) func(json.RawMessage) (any, error) {
	return func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"ok":                     true,
			"config_version":         cfg.ConfigVersion,
			"allow_everything":       cfg.Defaults.AllowEverything,
			"roots":                  cfg.Roots,
			"limits":                 cfg.Limits,
			"server_logging":         cfg.ServerLogging,
			"approval_enabled":       cfg.Approval.Enabled,
			"file_policy":            cfg.FilePolicy,
			"command_policy_default": cfg.CommandPolicy.Default,
			"feedback_enabled":       cfg.Feedback.Enabled,
			"notes":                  []string{"Explicit config entries win over defaults.", "Roots, limits, secret deny rules, and tool-specific guards remain active."},
		}, nil
	}
}
