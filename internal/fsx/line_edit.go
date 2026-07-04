package fsx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errBinaryFile = errors.New("refusing binary file")

type EditLinesArgs struct {
	Path           string `json:"path"`
	Cwd            string `json:"cwd"`
	PathMode       string `json:"path_mode"`
	Operation      string `json:"operation"`
	Line           int    `json:"line"`
	EndLine        int    `json:"end_line"`
	Content        string `json:"content"`
	LineStartsWith string `json:"line_starts_with"`
	DryRun         bool   `json:"dry_run"`
	CreateBackup   bool   `json:"create_backup"`
}

type lineBuffer struct {
	max   int
	lines []string
}

func (b *lineBuffer) add(line string) {
	if b.max <= 0 {
		return
	}
	if len(b.lines) == b.max {
		copy(b.lines, b.lines[1:])
		b.lines = b.lines[:b.max-1]
	}
	b.lines = append(b.lines, line)
}

func (b *lineBuffer) slice() []string {
	return append([]string(nil), b.lines...)
}

func (t *Tools) EditLines(raw json.RawMessage) (any, error) {
	var a EditLinesArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, errors.New("path is required")
	}
	if a.Line <= 0 {
		return nil, errors.New("line must be >= 1")
	}
	switch a.Operation {
	case "replace", "insert_before", "insert_after", "delete":
	default:
		return nil, fmt.Errorf("unsupported operation %q", a.Operation)
	}
	if strings.Contains(a.Content, "\x00") {
		return nil, errBinaryFile
	}
	switch a.Operation {
	case "insert_before", "insert_after":
		if a.EndLine != 0 {
			return nil, errors.New("end_line is only allowed for replace and delete")
		}
	case "replace", "delete":
		if a.EndLine == 0 {
			a.EndLine = a.Line
		}
		if a.EndLine < a.Line {
			return nil, errors.New("end_line must be >= line")
		}
	}

	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"tool": "fs_edit_lines", "cwd": a.Cwd, "operation": a.Operation, "line": a.Line, "end_line": a.EndLine, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot edit a directory")
	}

	editResult, err := applyLineEditToFile(p, info.Mode(), a, t.Cfg.Limits.DiffContextLines, !a.DryRun)
	if err != nil {
		return nil, err
	}
	diff, diffTruncated := compactLineEditDiff(displayPath, editResult.original.before, editResult.original.changed, editResult.original.after, editResult.updated.before, editResult.updated.changed, editResult.updated.after, t.Cfg.Limits.DiffContextLines, t.Cfg.Limits.MaxDiffBytes, editResult.oldBaseLine, editResult.newBaseLine)
	if a.DryRun {
		return map[string]any{
			"dry_run":        true,
			"path":           displayPath,
			"cwd":            a.Cwd,
			"operation":      a.Operation,
			"line":           a.Line,
			"end_line":       editResult.lineRangeEnd,
			"line_count":     editResult.original.totalLines,
			"new_line_count": editResult.updated.totalLines,
			"changed":        editResult.changed,
			"diff":           diff,
			"diff_truncated": diffTruncated,
		}, nil
	}
	if editResult.changed && a.CreateBackup {
		if err := copyFile(p, p+".bak", 0o600); err != nil { //nolint:gosec // backup path is derived from a sandbox-resolved path.
			return nil, err
		}
	}
	if editResult.changed && editResult.original.stagedPath != "" {
		if err := os.Rename(editResult.original.stagedPath, p); err != nil {
			_ = os.Remove(editResult.original.stagedPath)
			return nil, err
		}
		if d, err := os.Open(filepath.Dir(p)); err == nil { //nolint:gosec // dir is derived from a sandbox-resolved target path.
			_ = d.Sync()
			_ = d.Close()
		}
	}
	return map[string]any{
		"dry_run":        false,
		"path":           displayPath,
		"cwd":            a.Cwd,
		"operation":      a.Operation,
		"line":           a.Line,
		"end_line":       editResult.lineRangeEnd,
		"line_count":     editResult.original.totalLines,
		"new_line_count": editResult.updated.totalLines,
		"changed":        editResult.changed,
		"diff":           diff,
		"diff_truncated": diffTruncated,
	}, nil
}

type lineEditSnapshot struct {
	before     []string
	changed    []string
	after      []string
	totalLines int
	stagedPath string
}

type lineEditResult struct {
	original     lineEditSnapshot
	updated      lineEditSnapshot
	changed      bool
	oldBaseLine  int
	newBaseLine  int
	lineRangeEnd int
}

func applyLineEditToFile(path string, mode os.FileMode, a EditLinesArgs, contextLines int, stageWrite bool) (lineEditResult, error) {
	src, err := os.Open(path) //nolint:gosec // path was resolved through the sandbox by the caller.
	if err != nil {
		return lineEditResult{}, err
	}
	defer func() { _ = src.Close() }()

	var tmp *os.File
	if stageWrite {
		dir := filepath.Dir(path)
		tmp, err = os.CreateTemp(dir, ".personal-mcp-server-line-edit-*")
		if err != nil {
			return lineEditResult{}, err
		}
		if err := tmp.Chmod(mode.Perm()); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			return lineEditResult{}, err
		}
	}
	cleanupTmp := func() {
		if tmp != nil {
			name := tmp.Name()
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}

	reader := bufio.NewReader(src)
	var writer *bufio.Writer
	if tmp != nil {
		writer = bufio.NewWriter(tmp)
	}

	writeString := func(s string) error {
		if writer == nil || s == "" {
			return nil
		}
		_, err := writer.WriteString(s)
		return err
	}

	before := lineBuffer{max: maxInt(0, contextLines)}
	after := make([]string, 0, maxInt(0, contextLines))
	oldChanged := make([]string, 0, 8)
	newChanged := splitLines(a.Content)
	lineNo := 0
	changeApplied := false
	collectingRange := false
	oldBaseLine := 1
	newBaseLine := 1

	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			cleanupTmp()
			return lineEditResult{}, readErr
		}
		if line == "" && errors.Is(readErr, io.EOF) {
			break
		}
		if strings.ContainsRune(line, '\x00') {
			cleanupTmp()
			return lineEditResult{}, errBinaryFile
		}
		lineNo++
		plain := strings.TrimRight(line, "\r\n")

		if !changeApplied {
			if lineNo < a.Line {
				if err := writeString(line); err != nil {
					cleanupTmp()
					return lineEditResult{}, err
				}
				before.add(line)
				continue
			}
			if lineNo == a.Line {
				if a.LineStartsWith != "" && !strings.HasPrefix(plain, a.LineStartsWith) {
					cleanupTmp()
					return lineEditResult{}, fmt.Errorf("line %d does not start with %q", a.Line, a.LineStartsWith)
				}
				switch a.Operation {
				case "insert_before":
					oldBaseLine = a.Line - len(before.slice())
					newBaseLine = oldBaseLine
					if err := writeString(a.Content); err != nil {
						cleanupTmp()
						return lineEditResult{}, err
					}
					if err := writeString(line); err != nil {
						cleanupTmp()
						return lineEditResult{}, err
					}
					if len(after) < contextLines {
						after = append(after, line)
					}
					changeApplied = true
					continue
				case "insert_after":
					if err := writeString(line); err != nil {
						cleanupTmp()
						return lineEditResult{}, err
					}
					before.add(line)
					oldBaseLine = a.Line - len(before.slice()) + 1
					newBaseLine = oldBaseLine
					if err := writeString(a.Content); err != nil {
						cleanupTmp()
						return lineEditResult{}, err
					}
					changeApplied = true
					continue
				case "replace", "delete":
					oldBaseLine = a.Line - len(before.slice())
					newBaseLine = oldBaseLine
					oldChanged = append(oldChanged, line)
					collectingRange = true
					if a.EndLine == a.Line {
						collectingRange = false
						if a.Operation == "replace" {
							if err := writeString(a.Content); err != nil {
								cleanupTmp()
								return lineEditResult{}, err
							}
						}
						changeApplied = true
					}
					continue
				}
			}
		}

		if collectingRange {
			oldChanged = append(oldChanged, line)
			if lineNo == a.EndLine {
				collectingRange = false
				if a.Operation == "replace" {
					if err := writeString(a.Content); err != nil {
						cleanupTmp()
						return lineEditResult{}, err
					}
				}
				changeApplied = true
			}
			continue
		}

		if err := writeString(line); err != nil {
			cleanupTmp()
			return lineEditResult{}, err
		}
		if changeApplied && len(after) < contextLines {
			after = append(after, line)
		}
	}

	if collectingRange {
		cleanupTmp()
		return lineEditResult{}, fmt.Errorf("end_line %d is beyond end of file", a.EndLine)
	}
	if !changeApplied {
		cleanupTmp()
		return lineEditResult{}, fmt.Errorf("line %d is beyond end of file", a.Line)
	}

	if writer != nil {
		if err := writer.Flush(); err != nil {
			cleanupTmp()
			return lineEditResult{}, err
		}
		if err := tmp.Sync(); err != nil {
			cleanupTmp()
			return lineEditResult{}, err
		}
		if err := tmp.Close(); err != nil {
			cleanupTmp()
			return lineEditResult{}, err
		}
	}

	oldBefore := before.slice()
	changed := strings.Join(oldChanged, "") != a.Content
	original := lineEditSnapshot{
		before:     oldBefore,
		changed:    oldChanged,
		after:      append([]string(nil), after...),
		totalLines: lineNo,
	}
	updated := lineEditSnapshot{
		before:     append([]string(nil), oldBefore...),
		changed:    newChanged,
		after:      append([]string(nil), after...),
		totalLines: lineNo - len(oldChanged) + len(newChanged),
	}
	if stageWrite && tmp != nil {
		if changed {
			original.stagedPath = tmp.Name()
		} else {
			_ = os.Remove(tmp.Name())
		}
	}
	lineRangeEnd := a.Line
	if a.Operation == "replace" || a.Operation == "delete" {
		lineRangeEnd = a.EndLine
	}
	return lineEditResult{
		original:     original,
		updated:      updated,
		changed:      changed,
		oldBaseLine:  oldBaseLine,
		newBaseLine:  newBaseLine,
		lineRangeEnd: lineRangeEnd,
	}, nil
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath) //nolint:gosec // path was resolved through the sandbox by the caller.
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(dstPath) //nolint:gosec // backup path is derived from a sandbox-resolved path.
	if err != nil {
		return err
	}
	if err := dst.Chmod(mode); err != nil {
		_ = dst.Close()
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

func compactLineEditDiff(path string, oldBefore, oldChanged, oldAfter, newBefore, newChanged, newAfter []string, contextLines int, maxBytes int64, oldBaseLine, newBaseLine int) (string, bool) {
	oldLines := append(append(append([]string{}, oldBefore...), oldChanged...), oldAfter...)
	newLines := append(append(append([]string{}, newBefore...), newChanged...), newAfter...)
	return formatSingleRegionUnifiedDiffAt(path, oldLines, newLines, contextLines, maxBytes, oldBaseLine, newBaseLine)
}

func exactTextLineCount(path string) (int64, error) {
	f, err := os.Open(path) //nolint:gosec // path was resolved through the sandbox by the caller.
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 64*1024)
	var total int64
	var lastByte byte
	var sawBytes bool
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if bytes.IndexByte(chunk, 0) >= 0 {
				return 0, errBinaryFile
			}
			total += int64(bytes.Count(chunk, []byte{'\n'}))
			lastByte = chunk[len(chunk)-1]
			sawBytes = true
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if sawBytes && lastByte != '\n' {
		total++
	}
	return total, nil
}
