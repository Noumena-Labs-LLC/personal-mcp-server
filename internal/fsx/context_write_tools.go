package fsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

func (t *Tools) enforceFilePolicyContext(ctx context.Context, operation, requestedPath, resolvedPath string, details map[string]any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	decision, err := t.DecideFilePolicy(operation, requestedPath, resolvedPath)
	if err != nil {
		return err
	}
	switch decision.Action {
	case policy.ActionAllow:
		return nil
	case policy.ActionDeny:
		return fmt.Errorf("file policy denied %s for %q: %s", operation, requestedPath, decision.Rule)
	case policy.ActionPrompt:
		if !t.Cfg.Approval.Enabled || t.Approver == nil {
			return fmt.Errorf("file policy requires approval for %s %q, but approval is disabled", operation, requestedPath)
		}
		if details == nil {
			details = map[string]any{}
		}
		details["path"] = requestedPath
		details["resolved_path"] = resolvedPath
		details["operation"] = operation
		_, err := t.Approver.Request(ctx, approval.Request{
			Kind:    "file",
			Action:  operation,
			Rule:    decision.Rule,
			Summary: fmt.Sprintf("%s %s", operation, requestedPath),
			Details: details,
		})
		return err
	default:
		return fmt.Errorf("file policy returned unknown action %q", decision.Action)
	}
}

func (t *Tools) allowPolicyCopy() *Tools {
	cfg := *t.Cfg
	cfg.Approval.Enabled = false
	cfg.FilePolicy = config.FilePolicyConfig{
		ReadDefault:         policy.ActionAllow,
		WriteDefault:        policy.ActionAllow,
		CreateDefault:       policy.ActionAllow,
		PatchDefault:        policy.ActionAllow,
		UnifiedPatchDefault: policy.ActionAllow,
	}
	return &Tools{Sandbox: t.Sandbox, Cfg: &cfg, Approver: t.Approver}
}

func (t *Tools) CreateFileContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a CreateFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, errors.New("path is required")
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicyContext(ctx, "create", displayPath, p, map[string]any{"bytes": len(a.Content), "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().CreateFile(raw)
}

func (t *Tools) CreateDirContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a CreateDirArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, errors.New("path is required")
	}
	parents := true
	if a.Parents != nil {
		parents = *a.Parents
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicyContext(ctx, "create", displayPath, p, map[string]any{"tool": "fs_create_dir", "cwd": a.Cwd, "parents": parents, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().CreateDir(raw)
}

func (t *Tools) ReplaceFileContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a ReplaceFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, errors.New("path is required")
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicyContext(ctx, "patch", displayPath, p, map[string]any{"tool": "fs_replace_file", "bytes": len(a.Content), "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().ReplaceFile(raw)
}

func (t *Tools) AppendFileContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a AppendFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, errors.New("path is required")
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(p)
	missing := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !missing {
		return nil, statErr
	}
	operation := "patch"
	if missing {
		operation = "create"
	}
	if err := t.enforceFilePolicyContext(ctx, operation, displayPath, p, map[string]any{"tool": "fs_append_file", "bytes": len(a.Content), "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().AppendFile(raw)
}

func (t *Tools) ApplyPatchContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a ApplyPatchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Edits) == 0 && (a.Old != "" || a.New != "") {
		a.Edits = []PatchEdit{{Old: a.Old, New: a.New, ExpectedReplacements: a.ExpectedReplacements}}
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicyContext(ctx, "patch", displayPath, p, map[string]any{"dry_run": a.DryRun, "edits": len(a.Edits), "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().ApplyPatch(raw)
}

func (t *Tools) ReplaceRegexContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a ReplaceRegexArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicyContext(ctx, "patch", displayPath, p, map[string]any{"tool": "fs_replace_regex", "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().ReplaceRegex(raw)
}
