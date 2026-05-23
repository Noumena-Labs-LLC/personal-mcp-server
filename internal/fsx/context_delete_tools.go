package fsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (t *Tools) DeleteFileContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a DeleteFileArgs
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
	if err := t.enforceFilePolicyContext(ctx, "delete", displayPath, p, map[string]any{"tool": "fs_delete_file", "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().DeleteFile(raw)
}

func (t *Tools) DeleteFilesContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a DeleteFilesArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Paths) == 0 {
		return nil, errors.New("paths is required")
	}
	if a.MaxFiles <= 0 || a.MaxFiles > 100 {
		a.MaxFiles = 100
	}
	if len(a.Paths) > a.MaxFiles {
		return nil, fmt.Errorf("too many paths: %d > max_files %d", len(a.Paths), a.MaxFiles)
	}
	seen := map[string]bool{}
	for _, userPath := range a.Paths {
		if strings.TrimSpace(userPath) == "" {
			continue
		}
		p, displayPath, err := t.resolvePath(userPath, a.Cwd, a.PathMode)
		if err != nil {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if err := t.enforceFilePolicyContext(ctx, "delete", displayPath, p, map[string]any{"tool": "fs_delete_files", "cwd": a.Cwd}); err != nil {
			return nil, err
		}
	}
	return t.allowPolicyCopy().DeleteFiles(raw)
}

func (t *Tools) MoveFileContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var a MoveFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.SourcePath == "" || a.DestPath == "" {
		return nil, errors.New("source_path and dest_path are required")
	}
	src, displaySrc, err := t.resolvePath(a.SourcePath, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	dst, displayDst, err := t.resolvePath(a.DestPath, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicyContext(ctx, "delete", displaySrc, src, map[string]any{"tool": "fs_move_file", "cwd": a.Cwd, "destination": displayDst}); err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicyContext(ctx, "create", displayDst, dst, map[string]any{"tool": "fs_move_file", "cwd": a.Cwd, "source": displaySrc}); err != nil {
		return nil, err
	}
	return t.allowPolicyCopy().MoveFile(raw)
}
