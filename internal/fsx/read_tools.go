package fsx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type PathArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
}

func (t *Tools) GetFileInfo(raw json.RawMessage) (any, error) {
	var a PathArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("info", displayPath, p, map[string]any{"cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	typ := "file"
	if info.IsDir() {
		typ = "dir"
	} else if info.Mode()&os.ModeSymlink != 0 {
		typ = "symlink"
	}
	out := map[string]any{
		"path":          displayPath,
		"cwd":           a.Cwd,
		"resolved_path": p,
		"type":          typ,
		"size":          info.Size(),
		"size_bytes":    info.Size(),
		"modified":      info.ModTime().UTC().Format(time.RFC3339),
	}
	if typ == "file" {
		isText, lineEstimate, sniffedBytes, sniffErr := sniffTextFileInfo(p, info.Size(), t.Cfg.Limits.MaxReadBytes)
		out["is_text"] = isText
		out["line_count_estimate"] = lineEstimate
		out["sniffed_bytes"] = sniffedBytes
		out["truncated_recommended"] = info.Size() > t.Cfg.Limits.MaxReadBytes/2
		out["suggested_tools"] = largeFileSuggestedTools()
		if sniffErr != nil {
			out["sniff_error"] = sniffErr.Error()
		}
	}
	return out, nil
}

func sniffTextFileInfo(path string, size, maxReadBytes int64) (isText bool, lineEstimate, sniffedBytes int64, err error) {
	if size == 0 {
		return true, 0, 0, nil
	}
	limit := int64(64 * 1024)
	if maxReadBytes > 0 && maxReadBytes < limit {
		limit = maxReadBytes
	}
	if size < limit {
		limit = size
	}
	f, err := os.Open(path) //nolint:gosec // path was resolved through the sandbox by the caller.
	if err != nil {
		return false, 0, 0, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	buf := make([]byte, limit)
	n, readErr := io.ReadFull(f, buf)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return false, 0, int64(n), readErr
	}
	buf = buf[:n]
	if bytes.Contains(buf, []byte{0}) {
		return false, 0, int64(n), nil
	}
	lines := int64(bytes.Count(buf, []byte{'\n'}))
	if int64(n) == size {
		if n > 0 && buf[n-1] != '\n' {
			lines++
		}
		return true, lines, int64(n), nil
	}
	if n > 0 {
		lineEstimate = (lines * size) / int64(n)
		if lineEstimate == 0 {
			lineEstimate = 1
		}
	}
	return true, lineEstimate, int64(n), nil
}

func largeFileSuggestedTools() []string {
	return []string{"fs_get_file_info", "fs_tail_file", "fs_search_text", "fs_read_file with start_line/max_lines", "jsonl_tail", "jsonl_filter", "json_outline"}
}

type ReadFileArgs struct {
	Path      string `json:"path"`
	Cwd       string `json:"cwd"`
	PathMode  string `json:"path_mode"`
	StartLine int    `json:"start_line"`
	MaxLines  int    `json:"max_lines"`
	WholeFile bool   `json:"whole_file"`
}

const highOffsetReadLineThreshold = 5000

func (t *Tools) ReadFile(raw json.RawMessage) (any, error) {
	var a ReadFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.StartLine <= 0 {
		a.StartLine = 1
	}
	if a.WholeFile {
		a.MaxLines = 1000000000
	} else if a.MaxLines <= 0 || a.MaxLines > 1000 {
		a.MaxLines = 200
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("read", displayPath, p, map[string]any{"start_line": a.StartLine, "max_lines": a.MaxLines, "whole_file": a.WholeFile, "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot read a directory")
	}
	if info.Size() > t.Cfg.Limits.MaxReadBytes && a.WholeFile {
		return map[string]any{
			"ok":              false,
			"error":           "file_too_large_for_full_read",
			"path":            displayPath,
			"cwd":             a.Cwd,
			"size_bytes":      info.Size(),
			"max_read_bytes":  t.Cfg.Limits.MaxReadBytes,
			"suggested_tools": largeFileSuggestedTools(),
		}, nil
	}
	if highOffsetReadWouldScanTooMuch(info.Size(), t.Cfg.Limits.MaxReadBytes, a.StartLine, a.WholeFile) {
		return map[string]any{
			"ok":              false,
			"error":           "high_start_line_requires_linear_scan",
			"path":            displayPath,
			"cwd":             a.Cwd,
			"start_line":      a.StartLine,
			"max_lines":       a.MaxLines,
			"size_bytes":      info.Size(),
			"max_read_bytes":  t.Cfg.Limits.MaxReadBytes,
			"line_threshold":  highOffsetReadLineThreshold,
			"message":         "fs_read_file reaches start_line by scanning from the beginning; use fs_tail_file for recent content or fs_search_text to find nearby line ranges before retrying a bounded read.",
			"suggested_tools": []string{"fs_tail_file", "fs_search_text", "fs_get_file_info"},
			"whole_file":      a.WholeFile,
			"truncated":       true,
		}, nil
	}
	content, endLine, truncated, bytesRead, err := readTextWindow(p, a.StartLine, a.MaxLines, t.Cfg.Limits.MaxReadBytes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": content, "path": displayPath, "cwd": a.Cwd, "start_line": a.StartLine, "end_line": endLine, "truncated": truncated, "whole_file": a.WholeFile, "bytes_read": bytesRead}, nil
}

func highOffsetReadWouldScanTooMuch(size, maxReadBytes int64, startLine int, wholeFile bool) bool {
	if wholeFile || startLine <= highOffsetReadLineThreshold || size <= 0 || maxReadBytes <= 0 {
		return false
	}
	return size > maxReadBytes
}

type TailFileArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Lines    int    `json:"lines"`
}

func (t *Tools) TailFile(raw json.RawMessage) (any, error) {
	var a TailFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Lines <= 0 {
		a.Lines = 200
	}
	if a.Lines > 1000 {
		a.Lines = 1000
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("read", displayPath, p, map[string]any{"lines": a.Lines, "tail": true, "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot tail a directory")
	}
	content, returnedLines, bytesRead, truncated, err := tailTextFile(p, a.Lines, t.Cfg.Limits.MaxReadBytes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": content, "path": displayPath, "cwd": a.Cwd, "lines_requested": a.Lines, "lines_returned": returnedLines, "truncated": truncated, "bytes_read": bytesRead, "size_bytes": info.Size()}, nil
}

func tailTextFile(path string, lines int, maxBytes int64) (content string, returnedLines int, bytesRead int64, truncated bool, err error) {
	f, err := os.Open(path) //nolint:gosec // path was resolved through the sandbox by the caller.
	if err != nil {
		return "", 0, 0, false, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return "", 0, 0, false, err
	}
	if info.Size() == 0 {
		return "", 0, 0, false, nil
	}
	chunkSize := int64(32 * 1024)
	var buf []byte
	for offset := info.Size(); offset > 0; {
		readSize := chunkSize
		if offset < readSize {
			readSize = offset
		}
		if maxBytes > 0 && bytesRead+readSize > maxBytes {
			readSize = maxBytes - bytesRead
			if readSize <= 0 {
				truncated = true
				break
			}
		}
		offset -= readSize
		chunk := make([]byte, readSize)
		if _, err := f.ReadAt(chunk, offset); err != nil && !errors.Is(err, io.EOF) {
			return "", 0, bytesRead, false, err
		}
		if bytes.Contains(chunk, []byte{0}) {
			return "", 0, bytesRead + readSize, false, errors.New("refusing binary file")
		}
		buf = append(chunk, buf...)
		bytesRead += readSize
		if countLinesInBytes(buf) > lines {
			break
		}
		if maxBytes > 0 && bytesRead >= maxBytes {
			truncated = offset > 0
			break
		}
	}
	parts := bytes.Split(buf, []byte{'\n'})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
		truncated = true
	}
	returnedLines = len(parts)
	if returnedLines == 0 {
		return "", 0, bytesRead, truncated, nil
	}
	return string(bytes.Join(parts, []byte{'\n'})) + "\n", returnedLines, bytesRead, truncated, nil
}

func countLinesInBytes(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}
	n := bytes.Count(buf, []byte{'\n'})
	if buf[len(buf)-1] != '\n' {
		n++
	}
	return n
}

func readTextWindow(path string, startLine, maxLines int, maxBytes int64) (content string, endLine int, truncated bool, bytesRead int64, err error) {
	f, err := os.Open(path) //nolint:gosec // path was resolved through the sandbox by the caller.
	if err != nil {
		return "", 0, false, 0, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	reader := bufio.NewReader(f)
	var out strings.Builder
	lineNo := 0
	collected := 0
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			bytesRead += int64(len(line))
			if maxBytes > 0 && bytesRead > maxBytes {
				return "", 0, false, bytesRead, fmt.Errorf("read exceeded max_read_bytes: %d", maxBytes)
			}
			if strings.ContainsRune(line, '\x00') {
				return "", 0, false, bytesRead, errors.New("refusing binary file")
			}
			lineNo++
			if lineNo >= startLine && collected < maxLines {
				out.WriteString(line)
				collected++
				endLine = lineNo
			} else if collected >= maxLines {
				truncated = true
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", 0, false, bytesRead, readErr
		}
	}
	if endLine == 0 {
		endLine = startLine - 1
	}
	return out.String(), endLine, truncated, bytesRead, nil
}
