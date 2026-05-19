package fsx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type UnifiedPatchArgs struct {
	Patch        string `json:"patch"`
	Cwd          string `json:"cwd"`
	PathMode     string `json:"path_mode"`
	DryRun       bool   `json:"dry_run"`
	Strip        int    `json:"strip"`
	CreateBackup bool   `json:"create_backup"`
}

type patchFile struct {
	OldPath string
	NewPath string
	Hunks   []patchHunk
}

type patchHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []patchLine
}

type patchLine struct {
	Kind byte
	Text string
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func (t *Tools) ApplyUnifiedPatch(raw json.RawMessage) (any, error) {
	var a UnifiedPatchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Patch) == "" {
		return nil, errors.New("patch is required")
	}
	if int64(len(a.Patch)) > t.Cfg.Limits.MaxPatchBytes {
		return nil, errors.New("patch exceeds max_patch_bytes")
	}
	files, err := parseUnifiedPatch(a.Patch, a.Strip)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("patch does not contain any files")
	}
	if len(files) > 50 {
		return nil, errors.New("patch changes too many files")
	}
	type plannedChange struct {
		requestedPath string
		resolvedPath  string
		oldContent    string
		newContent    string
		mode          os.FileMode
		create        bool
	}
	changes := make([]plannedChange, 0, len(files))
	for _, filePatch := range files {
		if filePatch.NewPath == "/dev/null" {
			return nil, fmt.Errorf("deleting files is not supported: %s", filePatch.OldPath)
		}
		requestedPath := filePatch.NewPath
		if requestedPath == "" || requestedPath == "/dev/null" {
			requestedPath = filePatch.OldPath
		}
		if requestedPath == "" || requestedPath == "/dev/null" {
			return nil, errors.New("patch file has no usable target path")
		}
		resolvedPath, displayPath, err := t.resolvePath(requestedPath, a.Cwd, a.PathMode)
		if err != nil {
			return nil, err
		}
		operation := "unified_patch"
		oldContent := ""
		mode := os.FileMode(0o600)
		create := filePatch.OldPath == "/dev/null"
		info, statErr := os.Stat(resolvedPath)
		switch {
		case statErr == nil:
			if info.IsDir() {
				return nil, fmt.Errorf("cannot patch directory: %s", requestedPath)
			}
			if info.Size() > t.Cfg.Limits.MaxReadBytes {
				return nil, fmt.Errorf("file too large: %d bytes", info.Size())
			}
			b, err := os.ReadFile(resolvedPath) //nolint:gosec // path was resolved through the sandbox.
			if err != nil {
				return nil, err
			}
			if bytes.IndexByte(b, 0) >= 0 {
				return nil, errors.New("refusing binary file")
			}
			oldContent = string(b)
			mode = info.Mode()
		case errors.Is(statErr, os.ErrNotExist):
			create = true
			operation = "create"
		default:
			return nil, statErr
		}
		if err := t.enforceFilePolicy(operation, displayPath, resolvedPath, map[string]any{"dry_run": a.DryRun, "tool": "fs_apply_unified_patch", "cwd": a.Cwd}); err != nil {
			return nil, err
		}
		newContent, err := applyFilePatch(oldContent, filePatch)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", requestedPath, err)
		}
		changes = append(changes, plannedChange{requestedPath: displayPath, resolvedPath: resolvedPath, oldContent: oldContent, newContent: newContent, mode: mode, create: create})
	}
	var diff strings.Builder
	truncated := false
	for _, change := range changes {
		fileDiff, fileTruncated := compactUnifiedDiff(change.requestedPath, change.oldContent, change.newContent, t.Cfg.Limits.DiffContextLines, t.Cfg.Limits.MaxDiffBytes)
		if fileTruncated {
			truncated = true
		}
		diff.WriteString(fileDiff)
		if t.Cfg.Limits.MaxDiffBytes > 0 && int64(diff.Len()) > t.Cfg.Limits.MaxDiffBytes {
			truncated = true
			break
		}
	}
	outDiff := diff.String()
	if truncated && t.Cfg.Limits.MaxDiffBytes > 0 && int64(len(outDiff)) > t.Cfg.Limits.MaxDiffBytes {
		outDiff = outDiff[:t.Cfg.Limits.MaxDiffBytes] + "\n... diff truncated ...\n"
	}
	if !a.DryRun {
		for _, change := range changes {
			if change.create {
				if _, err := os.Stat(change.resolvedPath); err == nil {
					return nil, fmt.Errorf("file already exists: %s", change.requestedPath)
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
			}
			if err := os.MkdirAll(filepath.Dir(change.resolvedPath), 0o700); err != nil {
				return nil, err
			}
			if a.CreateBackup && !change.create {
				if err := os.WriteFile(change.resolvedPath+".bak", []byte(change.oldContent), 0o600); err != nil { //nolint:gosec // backup path is derived from a sandbox-resolved file path.
					return nil, err
				}
			}
			if err := atomicWrite(change.resolvedPath, []byte(change.newContent), change.mode); err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{"dry_run": a.DryRun, "cwd": a.Cwd, "files_changed": len(changes), "diff": outDiff, "diff_truncated": truncated}, nil
}

func parseUnifiedPatch(patch string, strip int) ([]patchFile, error) {
	lines := splitLines(patch)
	files := []patchFile{}
	for i := 0; i < len(lines); {
		line := strings.TrimRight(lines[i], "\r\n")
		if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "new file mode ") || strings.HasPrefix(line, "deleted file mode ") || strings.HasPrefix(line, "similarity index ") || strings.HasPrefix(line, "rename ") {
			i++
			continue
		}
		if !strings.HasPrefix(line, "--- ") {
			i++
			continue
		}
		oldPath := cleanPatchPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")), strip)
		i++
		if i >= len(lines) {
			return nil, errors.New("patch missing +++ line")
		}
		line = strings.TrimRight(lines[i], "\r\n")
		if !strings.HasPrefix(line, "+++ ") {
			return nil, errors.New("patch missing +++ line")
		}
		newPath := cleanPatchPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")), strip)
		i++
		pf := patchFile{OldPath: oldPath, NewPath: newPath}
		for i < len(lines) {
			line = strings.TrimRight(lines[i], "\r\n")
			if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "diff --git ") {
				break
			}
			if !strings.HasPrefix(line, "@@ ") {
				i++
				continue
			}
			hunk, next, err := parseHunk(lines, i)
			if err != nil {
				return nil, err
			}
			pf.Hunks = append(pf.Hunks, hunk)
			i = next
		}
		files = append(files, pf)
	}
	return files, nil
}

func parseHunk(lines []string, start int) (patchHunk, int, error) {
	header := strings.TrimRight(lines[start], "\r\n")
	matches := hunkHeaderRE.FindStringSubmatch(header)
	if matches == nil {
		return patchHunk{}, start, fmt.Errorf("invalid hunk header: %s", header)
	}
	oldStart, _ := strconv.Atoi(matches[1])
	oldCount := 1
	if matches[2] != "" {
		oldCount, _ = strconv.Atoi(matches[2])
	}
	newStart, _ := strconv.Atoi(matches[3])
	newCount := 1
	if matches[4] != "" {
		newCount, _ = strconv.Atoi(matches[4])
	}
	h := patchHunk{OldStart: oldStart, OldCount: oldCount, NewStart: newStart, NewCount: newCount}
	i := start + 1
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "@@ ") || strings.HasPrefix(trimmed, "--- ") || strings.HasPrefix(trimmed, "diff --git ") {
			break
		}
		if strings.HasPrefix(trimmed, `\ No newline at end of file`) {
			i++
			continue
		}
		if line == "" {
			break
		}
		kind := line[0]
		if kind != ' ' && kind != '+' && kind != '-' {
			break
		}
		h.Lines = append(h.Lines, patchLine{Kind: kind, Text: line[1:]})
		i++
	}
	return h, i, nil
}

func cleanPatchPath(path string, strip int) string {
	path = strings.Trim(path, "\"")
	if idx := strings.IndexAny(path, "\t "); idx >= 0 {
		path = path[:idx]
	}
	if path == "/dev/null" {
		return path
	}
	path = filepath.ToSlash(path)
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	for ; strip > 0; strip-- {
		idx := strings.IndexByte(path, '/')
		if idx < 0 {
			return "."
		}
		path = path[idx+1:]
	}
	return path
}

func applyFilePatch(original string, filePatch patchFile) (string, error) {
	oldLines := splitLines(original)
	out := make([]string, 0, len(oldLines))
	pos := 0
	for _, hunk := range filePatch.Hunks {
		target := hunk.OldStart - 1
		if hunk.OldStart == 0 {
			target = 0
		}
		if target < pos || target > len(oldLines) {
			return "", fmt.Errorf("hunk starts outside file at old line %d", hunk.OldStart)
		}
		out = append(out, oldLines[pos:target]...)
		pos = target
		for _, line := range hunk.Lines {
			switch line.Kind {
			case ' ':
				if pos >= len(oldLines) || oldLines[pos] != line.Text {
					return "", fmt.Errorf("context mismatch near old line %d", pos+1)
				}
				out = append(out, oldLines[pos])
				pos++
			case '-':
				if pos >= len(oldLines) || oldLines[pos] != line.Text {
					return "", fmt.Errorf("delete mismatch near old line %d", pos+1)
				}
				pos++
			case '+':
				out = append(out, line.Text)
			}
		}
	}
	out = append(out, oldLines[pos:]...)
	return strings.Join(out, ""), nil
}
