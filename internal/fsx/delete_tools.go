package fsx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type DeleteFileArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
}

func (t *Tools) DeleteFile(raw json.RawMessage) (any, error) {
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
	if err := t.enforceFilePolicy("delete", displayPath, p, map[string]any{"tool": "fs_delete_file", "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot delete a directory")
	}
	if err := os.Remove(p); err != nil {
		return nil, err
	}
	return map[string]any{"path": displayPath, "cwd": a.Cwd, "bytes": info.Size(), "deleted": true}, nil
}

type DeleteFilesArgs struct {
	Paths        []string `json:"paths"`
	Cwd          string   `json:"cwd"`
	PathMode     string   `json:"path_mode"`
	AllowMissing bool     `json:"allow_missing"`
	MaxFiles     int      `json:"max_files"`
}

func (t *Tools) DeleteFiles(raw json.RawMessage) (any, error) {
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
	type deleteResult struct {
		Path     string `json:"path"`
		Resolved string `json:"resolved_path,omitempty"`
		Size     int64  `json:"size_bytes,omitempty"`
		Status   string `json:"status"`
		Error    string `json:"error,omitempty"`
	}
	files := []deleteResult{}
	missing := []string{}
	refused := []map[string]string{}
	seen := map[string]bool{}
	var total int64
	for _, userPath := range a.Paths {
		if strings.TrimSpace(userPath) == "" {
			refused = append(refused, map[string]string{"path": userPath, "error": "empty path"})
			continue
		}
		p, displayPath, err := t.resolvePath(userPath, a.Cwd, a.PathMode)
		if err != nil {
			refused = append(refused, map[string]string{"path": userPath, "error": err.Error()})
			continue
		}
		if seen[p] {
			refused = append(refused, map[string]string{"path": displayPath, "error": "duplicate resolved path"})
			continue
		}
		seen[p] = true
		if err := t.enforceFilePolicy("delete", displayPath, p, map[string]any{"tool": "fs_delete_files", "cwd": a.Cwd}); err != nil {
			refused = append(refused, map[string]string{"path": displayPath, "error": err.Error()})
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && a.AllowMissing {
				missing = append(missing, displayPath)
				files = append(files, deleteResult{Path: displayPath, Resolved: p, Status: "missing"})
				continue
			}
			refused = append(refused, map[string]string{"path": displayPath, "error": err.Error()})
			continue
		}
		if info.IsDir() {
			refused = append(refused, map[string]string{"path": displayPath, "error": "cannot delete a directory"})
			continue
		}
		total += info.Size()
		files = append(files, deleteResult{Path: displayPath, Resolved: p, Size: info.Size(), Status: "pending"})
	}
	if len(refused) > 0 {
		return map[string]any{"ok": false, "files": files, "file_count": len(files), "total_size_bytes": total, "missing": missing, "refused": refused, "deleted": false}, nil
	}
	for i := range files {
		if files[i].Status != "pending" {
			continue
		}
		if err := os.Remove(files[i].Resolved); err != nil {
			files[i].Status = "error"
			files[i].Error = err.Error()
			refused = append(refused, map[string]string{"path": files[i].Path, "error": err.Error()})
			continue
		}
		files[i].Status = "deleted"
	}
	return map[string]any{"ok": len(refused) == 0, "files": files, "file_count": len(files), "total_size_bytes": total, "missing": missing, "refused": refused, "deleted": len(refused) == 0}, nil
}
