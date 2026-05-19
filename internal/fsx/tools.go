package fsx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

type ProjectFilePolicy interface {
	DecideProjectFile(operation, requestedPath, resolvedPath string) (policy.Decision, map[string]any, bool, error)
}

type Tools struct {
	Sandbox       *Sandbox
	Cfg           *config.Config
	Approver      *approval.Manager
	ProjectPolicy ProjectFilePolicy
}

func NewTools(c *config.Config, approver *approval.Manager) *Tools {
	return &Tools{Sandbox: NewSandbox(c), Cfg: c, Approver: approver}
}

func (t *Tools) DecideFilePolicy(operation, requestedPath, resolvedPath string) (policy.Decision, error) {
	globalDecision, err := policy.DecideFile(t.Cfg.FilePolicy, operation, requestedPath, resolvedPath)
	if err != nil {
		return policy.Decision{}, err
	}
	if globalDecision.Action == policy.ActionDeny && globalDecision.Rule != "default" {
		return globalDecision, nil
	}
	if t.ProjectPolicy == nil {
		return globalDecision, nil
	}
	projectDecision, _, matched, err := t.ProjectPolicy.DecideProjectFile(operation, requestedPath, resolvedPath)
	if err != nil {
		return policy.Decision{}, err
	}
	if !matched {
		return globalDecision, nil
	}
	switch projectDecision.Action {
	case policy.ActionDeny, policy.ActionPrompt:
		return projectDecision, nil
	case policy.ActionAllow:
		if globalDecision.Action == policy.ActionPrompt {
			return globalDecision, nil
		}
		return projectDecision, nil
	default:
		return policy.Decision{}, fmt.Errorf("project file policy returned unknown action %q", projectDecision.Action)
	}
}

func (t *Tools) ExplainFilePolicy(operation, requestedPath, resolvedPath string) (map[string]any, error) {
	globalDecision, err := policy.DecideFile(t.Cfg.FilePolicy, operation, requestedPath, resolvedPath)
	if err != nil {
		return nil, err
	}
	effectiveDecision, err := t.DecideFilePolicy(operation, requestedPath, resolvedPath)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"effective": effectiveDecision, "global": globalDecision}
	if t.ProjectPolicy != nil {
		projectDecision, projectDetails, matched, err := t.ProjectPolicy.DecideProjectFile(operation, requestedPath, resolvedPath)
		if err != nil {
			return nil, err
		}
		out["project"] = map[string]any{"matched": matched, "decision": projectDecision, "details": projectDetails}
	}
	return out, nil
}

func (t *Tools) enforceFilePolicy(operation, requestedPath, resolvedPath string, details map[string]any) error {
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
		_, err := t.Approver.Request(context.Background(), approval.Request{
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

func (t *Tools) ListRoots(json.RawMessage) (any, error) {
	return map[string]any{"roots": t.Sandbox.Roots}, nil
}

func (t *Tools) resolvePath(userPath, cwd, pathMode string) (resolvedPath, displayPath string, err error) {
	resolvedPath, err = t.Sandbox.ResolveWithMode(userPath, cwd, pathMode)
	if err != nil {
		return "", "", err
	}
	return resolvedPath, DisplayPathWithCwd(userPath, cwd), nil
}

func (t *Tools) ResolveForTool(userPath, cwd string) (resolvedPath, displayPath string, err error) {
	return t.resolvePath(userPath, cwd, "auto")
}

func pathStatError(resolvedPath, userPath, cwd string, err error) error {
	if strings.TrimSpace(cwd) != "" && strings.TrimSpace(userPath) != "" && filepath.Base(filepath.Clean(cwd)) == filepath.Base(filepath.Clean(userPath)) {
		return fmt.Errorf("%w. Hint: cwd=%q and path=%q repeat the same final path segment; use path=\".\" when cwd already points at the project", err, cwd, userPath)
	}
	return fmt.Errorf("stat %s: %w", resolvedPath, err)
}

type ListDirArgs struct {
	Path          string `json:"path"`
	Cwd           string `json:"cwd"`
	PathMode      string `json:"path_mode"`
	Recursive     bool   `json:"recursive"`
	IncludeHidden bool   `json:"include_hidden"`
	MaxEntries    int    `json:"max_entries"`
}

func (t *Tools) ListDir(raw json.RawMessage) (any, error) {
	var a ListDirArgs
	_ = json.Unmarshal(raw, &a)
	if a.MaxEntries <= 0 || a.MaxEntries > 1000 {
		a.MaxEntries = 200
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("list", displayPath, p, map[string]any{"recursive": a.Recursive, "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, pathStatError(p, a.Path, a.Cwd, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", a.Path)
	}
	type entry struct {
		Path    string `json:"path"`
		Type    string `json:"type"`
		Size    int64  `json:"size"`
		ModTime string `json:"modified"`
	}
	entries := []entry{}
	rootForRel := p
	walk := func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == p {
			return nil
		}
		if len(entries) >= a.MaxEntries {
			return filepath.SkipAll
		}
		name := d.Name()
		if !a.IncludeHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr == nil && !t.Sandbox.InRoot(resolved) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if t.Sandbox.IsDenied(path) {
			return nil
		}
		inf, err := d.Info()
		if err != nil {
			return err
		}
		typ := "file"
		if d.IsDir() {
			typ = "dir"
		} else if d.Type()&os.ModeSymlink != 0 {
			typ = "symlink"
		}
		rel, _ := filepath.Rel(rootForRel, path)
		entries = append(entries, entry{Path: rel, Type: typ, Size: inf.Size(), ModTime: inf.ModTime().UTC().Format(time.RFC3339)})
		if !a.Recursive && d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if err := filepath.WalkDir(p, walk); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return map[string]any{"entries": entries, "path": displayPath, "cwd": a.Cwd, "truncated": len(entries) >= a.MaxEntries}, nil
}

type SearchArgs struct {
	Path              string   `json:"path"`
	Cwd               string   `json:"cwd"`
	PathMode          string   `json:"path_mode"`
	Query             string   `json:"query"`
	Regex             bool     `json:"regex"`
	CaseSensitive     bool     `json:"case_sensitive"`
	MaxResults        int      `json:"max_results"`
	IncludeGlobs      []string `json:"include_globs"`
	ExcludeGlobs      []string `json:"exclude_globs"`
	ContextBefore     int      `json:"context_before"`
	ContextAfter      int      `json:"context_after"`
	MaxMatchesPerFile int      `json:"max_matches_per_file"`
	Offset            int      `json:"offset"`
}

func (t *Tools) SearchText(raw json.RawMessage) (any, error) {
	var a SearchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Query == "" {
		return nil, errors.New("query is required")
	}
	if a.MaxResults <= 0 || a.MaxResults > t.Cfg.Limits.MaxSearchResults {
		a.MaxResults = t.Cfg.Limits.MaxSearchResults
	}
	if a.Offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	base, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("search", displayPath, base, map[string]any{"query": a.Query, "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	var re *regexp.Regexp
	needle := a.Query
	if a.Regex {
		pattern := a.Query
		if !a.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
	} else if !a.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	type match struct {
		Path          string   `json:"path"`
		Line          int      `json:"line"`
		Text          string   `json:"text"`
		ContextBefore []string `json:"context_before,omitempty"`
		ContextAfter  []string `json:"context_after,omitempty"`
	}
	matches := []match{}
	seenMatches := 0
	skippedLarge := []map[string]any{}
	skippedLargeCount := 0
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if len(matches) >= a.MaxResults {
			return filepath.SkipAll
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != base {
				return filepath.SkipDir
			}
			return nil
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr == nil && !t.Sandbox.InRoot(resolved) {
			return nil
		}
		if t.Sandbox.IsDenied(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > t.Cfg.Limits.MaxSearchFileBytes {
			skippedLargeCount++
			if len(skippedLarge) < 20 {
				rel, _ := filepath.Rel(base, path)
				skippedLarge = append(skippedLarge, map[string]any{"path": rel, "bytes": info.Size(), "limit": t.Cfg.Limits.MaxSearchFileBytes})
			}
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		if !matchGlobSet(rel, a.IncludeGlobs, true) || matchGlobSet(rel, a.ExcludeGlobs, false) {
			return nil
		}
		f, err := os.Open(path) //nolint:gosec // path was resolved through the sandbox before walking.
		if err != nil {
			return err
		}
		reader := bufio.NewReader(f)
		lineNo := 0
		stopWalk := false
		before := make([]string, 0, maxInt(a.ContextBefore, 0))
		var pending []int
		matchesInFile := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				break
			}
			if line != "" {
				if strings.ContainsRune(line, '\x00') {
					break
				}
				lineNo++
				text := strings.TrimRight(line, "\r\n")
				for _, idx := range pending {
					if len(matches[idx].ContextAfter) < a.ContextAfter {
						matches[idx].ContextAfter = append(matches[idx].ContextAfter, text)
					}
				}
				filtered := pending[:0]
				for _, idx := range pending {
					if len(matches[idx].ContextAfter) < a.ContextAfter {
						filtered = append(filtered, idx)
					}
				}
				pending = filtered
				check := line
				if !a.CaseSensitive && !a.Regex {
					check = strings.ToLower(check)
				}
				ok := false
				if re != nil {
					ok = re.MatchString(line)
				} else {
					ok = strings.Contains(check, needle)
				}
				if ok && (a.MaxMatchesPerFile <= 0 || matchesInFile < a.MaxMatchesPerFile) {
					matchesInFile++
					seenMatches++
					if seenMatches <= a.Offset {
						if len(matches) >= a.MaxResults {
							stopWalk = true
							break
						}
					} else {
						ctxBefore := append([]string(nil), before...)
						m := match{Path: rel, Line: lineNo, Text: text, ContextBefore: ctxBefore}
						matches = append(matches, m)
						if a.ContextAfter > 0 {
							pending = append(pending, len(matches)-1)
						}
						if len(matches) >= a.MaxResults {
							stopWalk = true
							break
						}
					}
				}
				if a.ContextBefore > 0 {
					before = append(before, text)
					if len(before) > a.ContextBefore {
						before = before[1:]
					}
				}
			}
			if err == io.EOF {
				break
			}
		}
		if closeErr := f.Close(); closeErr != nil {
			return closeErr
		}
		if stopWalk {
			return filepath.SkipAll
		}
		return nil
	})
	truncated := len(matches) >= a.MaxResults
	nextOffset := 0
	if truncated {
		nextOffset = a.Offset + len(matches)
	}
	return map[string]any{"matches": matches, "path": displayPath, "cwd": a.Cwd, "offset": a.Offset, "next_offset": nextOffset, "truncated": truncated, "skipped_too_large_count": skippedLargeCount, "skipped_too_large_samples": skippedLarge}, err
}

type PatchEdit struct {
	Old                  string `json:"old"`
	New                  string `json:"new"`
	ExpectedReplacements int    `json:"expected_replacements"`
}

type ApplyPatchArgs struct {
	Path                 string      `json:"path"`
	Cwd                  string      `json:"cwd"`
	PathMode             string      `json:"path_mode"`
	Old                  string      `json:"old"`
	New                  string      `json:"new"`
	ExpectedReplacements int         `json:"expected_replacements"`
	Edits                []PatchEdit `json:"edits"`
	DryRun               bool        `json:"dry_run"`
	CreateBackup         bool        `json:"create_backup"`
}

func (t *Tools) ApplyPatch(raw json.RawMessage) (any, error) {
	var a ApplyPatchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Edits) == 0 {
		if a.Old == "" && a.New == "" {
			return nil, errors.New("fs_apply_patch requires either top-level old/new or edits=[{old,new,expected_replacements?}]; expected_replacements is optional and defaults to 1")
		}
		a.Edits = []PatchEdit{{Old: a.Old, New: a.New, ExpectedReplacements: a.ExpectedReplacements}}
	}
	if len(a.Edits) == 0 {
		return nil, errors.New("at least one edit is required")
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"dry_run": a.DryRun, "edits": len(a.Edits), "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot patch a directory")
	}
	if info.Size() > t.Cfg.Limits.MaxReadBytes {
		return nil, fmt.Errorf("file too large: %d bytes", info.Size())
	}
	b, err := os.ReadFile(p) //nolint:gosec // path was resolved through the sandbox.
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return nil, errors.New("refusing binary file")
	}
	original := string(b)
	updated := original
	total := 0
	warnings := []string{}
	for i, edit := range a.Edits {
		if edit.Old == "" {
			return nil, fmt.Errorf("edit %d: old text is required", i)
		}
		if edit.ExpectedReplacements == 0 {
			edit.ExpectedReplacements = 1
		} else if edit.ExpectedReplacements < 0 {
			return nil, fmt.Errorf("edit %d: expected_replacements must be positive when provided", i)
		}
		count := strings.Count(updated, edit.Old)
		if count == 0 {
			return nil, fmt.Errorf("edit %d: old text not found; re-read the target range before retrying", i)
		}
		replacements := edit.ExpectedReplacements
		if count < replacements {
			replacements = count
			warnings = append(warnings, fmt.Sprintf("edit %d requested %d replacements but only %d were found", i, edit.ExpectedReplacements, count))
		} else if count > replacements {
			warnings = append(warnings, fmt.Sprintf("edit %d found %d matches and replaced the first %d; additional matches remain", i, count, replacements))
		}
		updated = strings.Replace(updated, edit.Old, edit.New, replacements)
		total += replacements
	}
	diff, diffTruncated := compactUnifiedDiff(displayPath, original, updated, t.Cfg.Limits.DiffContextLines, t.Cfg.Limits.MaxDiffBytes)
	if a.DryRun {
		result := map[string]any{"dry_run": true, "path": displayPath, "cwd": a.Cwd, "replacements": total, "diff": diff, "diff_truncated": diffTruncated}
		if len(warnings) > 0 {
			result["warnings"] = warnings
		}
		return result, nil
	}
	if a.CreateBackup {
		if err := os.WriteFile(p+".bak", b, 0o600); err != nil { //nolint:gosec // backup path is derived from a sandbox-resolved file path.
			return nil, err
		}
	}
	if err := atomicWrite(p, []byte(updated), info.Mode()); err != nil {
		return nil, err
	}
	result := map[string]any{"dry_run": false, "path": displayPath, "cwd": a.Cwd, "replacements": total, "diff": diff, "diff_truncated": diffTruncated}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".personal-mcp-server-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { //nolint:gosec // dir is derived from a sandbox-resolved target path.
		_ = d.Sync()
		_ = d.Close()
	}
	ok = true
	return nil
}

func matchGlobSet(rel string, globs []string, defaultWhenEmpty bool) bool {
	if len(globs) == 0 {
		return defaultWhenEmpty
	}
	rel = filepath.ToSlash(rel)
	for _, glob := range globs {
		if globMatches(glob, rel) {
			return true
		}
	}
	return false
}

func globMatches(glob, rel string) bool {
	glob = filepath.ToSlash(glob)
	if ok, _ := filepath.Match(glob, rel); ok {
		return true
	}
	if strings.HasPrefix(glob, "**/") {
		if ok, _ := filepath.Match(strings.TrimPrefix(glob, "**/"), filepath.Base(rel)); ok {
			return true
		}
	}
	if strings.HasSuffix(glob, "/**") {
		prefix := strings.TrimSuffix(glob, "/**")
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}
	if strings.Contains(glob, "**") {
		pattern := regexp.QuoteMeta(glob)
		pattern = strings.ReplaceAll(pattern, `\*\*`, `.*`)
		pattern = strings.ReplaceAll(pattern, `\*`, `[^/]*`)
		matched, _ := regexp.MatchString("^"+pattern+"$", rel)
		return matched
	}
	return false
}

type FindArgs struct {
	Path         string   `json:"path"`
	Cwd          string   `json:"cwd"`
	PathMode     string   `json:"path_mode"`
	Type         string   `json:"type"`
	NameGlobs    []string `json:"name_globs"`
	PathGlobs    []string `json:"path_globs"`
	ExcludeGlobs []string `json:"exclude_globs"`
	MaxResults   int      `json:"max_results"`
	MaxDepth     int      `json:"max_depth"`
	MinSizeBytes int64    `json:"min_size_bytes"`
	MaxSizeBytes int64    `json:"max_size_bytes"`
	Offset       int      `json:"offset"`
}

func (t *Tools) Find(raw json.RawMessage) (any, error) {
	var a FindArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.MaxResults <= 0 || a.MaxResults > 1000 {
		a.MaxResults = 200
	}
	if a.Offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	if a.Type == "" {
		a.Type = "any"
	}
	base, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("list", displayPath, base, map[string]any{"tool": "fs_find", "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	type result struct {
		Path    string `json:"path"`
		Type    string `json:"type"`
		Size    int64  `json:"size"`
		ModTime string `json:"modified"`
	}
	results := []result{}
	seenResults := 0
	baseDepth := strings.Count(filepath.ToSlash(filepath.Clean(base)), "/")
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == base {
			return nil
		}
		if len(results) >= a.MaxResults {
			return filepath.SkipAll
		}
		depth := strings.Count(filepath.ToSlash(filepath.Clean(path)), "/") - baseDepth
		if a.MaxDepth > 0 && depth > a.MaxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr == nil && !t.Sandbox.InRoot(resolved) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if t.Sandbox.IsDenied(path) {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		relSlash := filepath.ToSlash(rel)
		if matchGlobSet(relSlash, a.ExcludeGlobs, false) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(a.PathGlobs) > 0 && !matchGlobSet(relSlash, a.PathGlobs, false) {
			return nil
		}
		if len(a.NameGlobs) > 0 && !matchGlobSet(d.Name(), a.NameGlobs, false) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		typ := "file"
		if d.IsDir() {
			typ = "dir"
		} else if d.Type()&os.ModeSymlink != 0 {
			typ = "symlink"
		}
		if a.Type != "any" && a.Type != typ {
			return nil
		}
		if a.MinSizeBytes > 0 && info.Size() < a.MinSizeBytes {
			return nil
		}
		if a.MaxSizeBytes > 0 && info.Size() > a.MaxSizeBytes {
			return nil
		}
		seenResults++
		if seenResults <= a.Offset {
			return nil
		}
		results = append(results, result{Path: relSlash, Type: typ, Size: info.Size(), ModTime: info.ModTime().UTC().Format(time.RFC3339)})
		return nil
	})
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	truncated := len(results) >= a.MaxResults
	nextOffset := 0
	if truncated {
		nextOffset = a.Offset + len(results)
	}
	return map[string]any{"results": results, "path": displayPath, "cwd": a.Cwd, "offset": a.Offset, "next_offset": nextOffset, "truncated": truncated}, err
}

type ReplaceRegexArgs struct {
	Path            string `json:"path"`
	Cwd             string `json:"cwd"`
	PathMode        string `json:"path_mode"`
	Pattern         string `json:"pattern"`
	Replacement     string `json:"replacement"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	MaxReplacements int    `json:"max_replacements"`
	AllowUnlimited  bool   `json:"allow_unlimited"`
	DryRun          bool   `json:"dry_run"`
	CreateBackup    bool   `json:"create_backup"`
}

func (t *Tools) ReplaceRegex(raw json.RawMessage) (any, error) {
	var a ReplaceRegexArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Pattern == "" {
		return nil, errors.New("pattern is required")
	}
	if a.MaxReplacements == 0 && !a.AllowUnlimited {
		a.MaxReplacements = 1
	}
	if a.MaxReplacements < 0 {
		return nil, errors.New("max_replacements cannot be negative")
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"tool": "fs_replace_regex", "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot replace in a directory")
	}
	if info.Size() > t.Cfg.Limits.MaxReadBytes {
		return nil, fmt.Errorf("file too large: %d bytes", info.Size())
	}
	b, err := os.ReadFile(p) //nolint:gosec // path was resolved through the sandbox.
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return nil, errors.New("refusing binary file")
	}
	original := string(b)
	lines := strings.SplitAfter(original, "\n")
	start := 1
	if a.StartLine > 0 {
		start = a.StartLine
	}
	end := len(lines)
	if a.EndLine > 0 && a.EndLine < end {
		end = a.EndLine
	}
	if start < 1 || start > len(lines)+1 || end < start-1 {
		return nil, errors.New("invalid line range")
	}
	prefix := strings.Join(lines[:start-1], "")
	target := strings.Join(lines[start-1:end], "")
	suffix := strings.Join(lines[end:], "")
	matches := re.FindAllStringIndex(target, -1)
	if len(matches) == 0 {
		return map[string]any{"changed": false, "replacements": 0, "path": displayPath, "cwd": a.Cwd, "dry_run": a.DryRun}, nil
	}
	limit := len(matches)
	if !a.AllowUnlimited && a.MaxReplacements > 0 && limit > a.MaxReplacements {
		limit = a.MaxReplacements
	}
	var out strings.Builder
	last := 0
	for i := 0; i < limit; i++ {
		loc := matches[i]
		out.WriteString(target[last:loc[0]])
		out.WriteString(re.ReplaceAllString(target[loc[0]:loc[1]], a.Replacement))
		last = loc[1]
	}
	out.WriteString(target[last:])
	updated := prefix + out.String() + suffix
	diff, diffTruncated := compactUnifiedDiff(displayPath, original, updated, t.Cfg.Limits.DiffContextLines, t.Cfg.Limits.MaxDiffBytes)
	if a.DryRun {
		return map[string]any{"changed": updated != original, "dry_run": true, "path": displayPath, "cwd": a.Cwd, "replacements": limit, "diff": diff, "diff_truncated": diffTruncated}, nil
	}
	if a.CreateBackup {
		if err := os.WriteFile(p+".bak", b, 0o600); err != nil { //nolint:gosec // backup path is derived from a sandbox-resolved path.
			return nil, err
		}
	}
	if err := atomicWrite(p, []byte(updated), info.Mode()); err != nil {
		return nil, err
	}
	return map[string]any{"changed": updated != original, "dry_run": false, "path": displayPath, "cwd": a.Cwd, "replacements": limit, "diff": diff, "diff_truncated": diffTruncated}, nil
}

type CreateFileArgs struct {
	Path         string `json:"path"`
	Cwd          string `json:"cwd"`
	PathMode     string `json:"path_mode"`
	Content      string `json:"content"`
	FailIfExists bool   `json:"fail_if_exists"`
	CreateDirs   bool   `json:"create_dirs"`
}

func (t *Tools) CreateFile(raw json.RawMessage) (any, error) {
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
	if err := t.enforceFilePolicy("create", displayPath, p, map[string]any{"bytes": len(a.Content), "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	if _, err := os.Stat(p); err == nil {
		return nil, fmt.Errorf("file already exists: %s", a.Path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if a.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return nil, err
		}
	}
	if err := atomicWrite(p, []byte(a.Content), 0o600); err != nil {
		return nil, err
	}
	return map[string]any{"created": true, "path": displayPath, "cwd": a.Cwd, "bytes": len(a.Content)}, nil
}

type CreateDirArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Parents  *bool  `json:"parents"`
	DryRun   bool   `json:"dry_run"`
}

func (t *Tools) CreateDir(raw json.RawMessage) (any, error) {
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
	if err := t.enforceFilePolicy("create", displayPath, p, map[string]any{"tool": "fs_create_dir", "cwd": a.Cwd, "parents": parents, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	if info, err := os.Stat(p); err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("path already exists and is not a directory: %s", a.Path)
		}
		return map[string]any{"created": false, "dry_run": a.DryRun, "path": displayPath, "cwd": a.Cwd, "parents": parents, "already_exists": true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !parents {
		if _, err := os.Stat(filepath.Dir(p)); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"created": !a.DryRun, "dry_run": a.DryRun, "path": displayPath, "cwd": a.Cwd, "parents": parents, "already_exists": false}
	if a.DryRun {
		return result, nil
	}
	if parents {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, err
		}
	} else if err := os.Mkdir(p, 0o700); err != nil {
		return nil, err
	}
	return result, nil
}

type ReplaceFileArgs struct {
	Path         string `json:"path"`
	Cwd          string `json:"cwd"`
	PathMode     string `json:"path_mode"`
	Content      string `json:"content"`
	CreateBackup bool   `json:"create_backup"`
}

func (t *Tools) ReplaceFile(raw json.RawMessage) (any, error) {
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
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"tool": "fs_replace_file", "bytes": len(a.Content), "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot replace a directory")
	}
	oldBytes, err := os.ReadFile(p) //nolint:gosec // path was resolved through the sandbox.
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(oldBytes, 0) >= 0 || strings.Contains(a.Content, "\x00") {
		return nil, errors.New("refusing binary file")
	}
	newBytes := []byte(a.Content)
	diff, diffTruncated := compactUnifiedDiff(displayPath, string(oldBytes), a.Content, t.Cfg.Limits.DiffContextLines, t.Cfg.Limits.MaxDiffBytes)
	if a.CreateBackup {
		if err := os.WriteFile(p+".bak", oldBytes, 0o600); err != nil { //nolint:gosec // backup path is derived from a sandbox-resolved path.
			return nil, err
		}
	}
	if err := atomicWrite(p, newBytes, info.Mode()); err != nil {
		return nil, err
	}
	return map[string]any{"path": displayPath, "cwd": a.Cwd, "old_bytes": len(oldBytes), "new_bytes": len(newBytes), "changed": !bytes.Equal(oldBytes, newBytes), "diff": diff, "diff_truncated": diffTruncated}, nil
}

type MoveFileArgs struct {
	SourcePath string `json:"source_path"`
	DestPath   string `json:"dest_path"`
	Cwd        string `json:"cwd"`
	PathMode   string `json:"path_mode"`
	Overwrite  bool   `json:"overwrite"`
}

func (t *Tools) MoveFile(raw json.RawMessage) (any, error) {
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
	if err := t.enforceFilePolicy("delete", displaySrc, src, map[string]any{"tool": "fs_move_file", "cwd": a.Cwd, "destination": displayDst}); err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("create", displayDst, dst, map[string]any{"tool": "fs_move_file", "cwd": a.Cwd, "source": displaySrc}); err != nil {
		return nil, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("cannot move a directory")
	}
	if _, err := os.Stat(dst); err == nil && !a.Overwrite {
		return nil, fmt.Errorf("destination file already exists: %s", a.DestPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(filepath.Dir(dst)); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}
	return map[string]any{"source_path": displaySrc, "dest_path": displayDst, "cwd": a.Cwd, "bytes": info.Size(), "moved": true, "overwrite": a.Overwrite}, nil
}

type GitStatusArgs struct {
	Path             string `json:"path"`
	Cwd              string `json:"cwd"`
	PathMode         string `json:"path_mode"`
	IncludeUntracked bool   `json:"include_untracked"`
	MaxEntries       int    `json:"max_entries"`
}

func (t *Tools) GitStatus(raw json.RawMessage) (any, error) {
	var a GitStatusArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("read", displayPath, p, map[string]any{"tool": "git_status", "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	repoRoot, err := findGitRepo(p, t.Sandbox)
	if err != nil {
		return nil, err
	}
	maxEntries := a.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 200
	}
	if maxEntries > 1000 {
		maxEntries = 1000
	}
	args := []string{"status", "--porcelain=v1", "-z"}
	if !a.IncludeUntracked {
		args = append(args, "--untracked-files=no")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(t.Cfg.Limits.CommandTimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	cmd.Env = []string{"PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOME="}
	limit := t.Cfg.Limits.MaxCommandOutputBytes
	var out cappedWriter
	out.limit = limit
	var stderr cappedWriter
	stderr.limit = 64 * 1024
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	start := time.Now()
	runErr := cmd.Run()
	timedOut := ctx.Err() == context.DeadlineExceeded
	entries := parseGitStatusPorcelain(out.String(), maxEntries)
	res := map[string]any{
		"path":              displayPath,
		"cwd":               a.Cwd,
		"repo_root":         repoRoot,
		"entries":           entries,
		"entry_count":       len(entries),
		"include_untracked": a.IncludeUntracked,
		"truncated":         out.truncated || len(entries) >= maxEntries,
		"stderr":            stderr.String(),
		"timed_out":         timedOut,
		"duration_ms":       time.Since(start).Milliseconds(),
	}
	if runErr != nil && !timedOut {
		res["error"] = runErr.Error()
	}
	return res, nil
}

func parseGitStatusPorcelain(raw string, maxEntries int) []map[string]any {
	if maxEntries <= 0 {
		maxEntries = 200
	}
	parts := strings.Split(raw, "\x00")
	entries := make([]map[string]any, 0, minInt(len(parts), maxEntries))
	for i := 0; i < len(parts) && len(entries) < maxEntries; i++ {
		part := parts[i]
		if part == "" {
			continue
		}
		if len(part) < 4 {
			entries = append(entries, map[string]any{"raw": part})
			continue
		}
		x := string(part[0])
		y := string(part[1])
		path := strings.TrimSpace(part[3:])
		entry := map[string]any{"index": x, "worktree": y, "path": path, "status": strings.TrimSpace(part[:2])}
		if part[0] == 'R' || part[0] == 'C' {
			if i+1 < len(parts) {
				entry["previous_path"] = parts[i+1]
				i++
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

type GitDiffArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Staged   bool   `json:"staged"`
	MaxBytes int64  `json:"max_bytes"`
}

func (t *Tools) GitDiff(raw json.RawMessage) (any, error) {
	var a GitDiffArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("read", displayPath, p, map[string]any{"tool": "git_diff", "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	repoRoot, err := findGitRepo(p, t.Sandbox)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(repoRoot, p)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		rel = "."
	}
	args := []string{"diff"}
	if a.Staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", rel)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(t.Cfg.Limits.CommandTimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	cmd.Env = []string{"PATH=/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOME="}
	limit := t.Cfg.Limits.MaxCommandOutputBytes
	if a.MaxBytes > 0 && a.MaxBytes < limit {
		limit = a.MaxBytes
	}
	var out cappedWriter
	out.limit = limit
	var stderr cappedWriter
	stderr.limit = 64 * 1024
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	start := time.Now()
	err = cmd.Run()
	timedOut := ctx.Err() == context.DeadlineExceeded
	if err != nil && !timedOut {
		return map[string]any{"diff": out.String(), "stderr": stderr.String(), "path": displayPath, "cwd": a.Cwd, "repo_root": repoRoot, "truncated": out.truncated, "duration_ms": time.Since(start).Milliseconds()}, nil
	}
	return map[string]any{"diff": out.String(), "stderr": stderr.String(), "path": displayPath, "cwd": a.Cwd, "repo_root": repoRoot, "truncated": out.truncated, "timed_out": timedOut, "duration_ms": time.Since(start).Milliseconds()}, nil
}

func findGitRepo(path string, sandbox *Sandbox) (string, error) {
	start := path
	if info, err := os.Stat(start); err == nil && !info.IsDir() {
		start = filepath.Dir(start)
	}
	start = filepath.Clean(start)
	for dir := start; ; dir = filepath.Dir(dir) {
		if !sandbox.InRoot(dir) {
			break
		}
		if isGitDir(filepath.Join(dir, ".git")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}

	candidates, err := findNestedGitRepos(start, sandbox, 4, 10)
	if err != nil {
		return "", err
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no git repository found at or above %q inside allowed roots; pass cwd or path inside a repository", path)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple git repositories found under %q; pass cwd or path inside one repository: %s", path, strings.Join(candidates, ", "))
	}
}

func isGitDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func findNestedGitRepos(start string, sandbox *Sandbox, maxDepth, maxResults int) ([]string, error) {
	info, err := os.Stat(start)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	var repos []string
	baseDepth := strings.Count(filepath.Clean(start), string(os.PathSeparator))
	err = filepath.WalkDir(start, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if !sandbox.InRoot(path) {
			return filepath.SkipDir
		}
		name := d.Name()
		if path != start && (name == "vendor" || name == "node_modules" || name == ".tools") {
			return filepath.SkipDir
		}
		depth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - baseDepth
		if depth > maxDepth {
			return filepath.SkipDir
		}
		if isGitDir(filepath.Join(path, ".git")) {
			repos = append(repos, path)
			if len(repos) >= maxResults {
				return filepath.SkipAll
			}
			return filepath.SkipDir
		}
		return nil
	})
	return repos, err
}

type cappedWriter struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - int64(w.buf.Len())
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		w.truncated = true
		_, _ = w.buf.Write(p[:remaining])
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *cappedWriter) String() string { return w.buf.String() }

type TreeArgs struct {
	Path          string   `json:"path"`
	Cwd           string   `json:"cwd"`
	PathMode      string   `json:"path_mode"`
	MaxDepth      int      `json:"max_depth"`
	MaxEntries    int      `json:"max_entries"`
	IncludeHidden bool     `json:"include_hidden"`
	ExcludeGlobs  []string `json:"exclude_globs"`
}

func (t *Tools) Tree(raw json.RawMessage) (any, error) {
	var a TreeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	if a.MaxDepth <= 0 || a.MaxDepth > 10 {
		a.MaxDepth = 3
	}
	if a.MaxEntries <= 0 || a.MaxEntries > 2000 {
		a.MaxEntries = 300
	}
	base, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("list", displayPath, base, map[string]any{"tool": "fs_tree", "cwd": a.Cwd}); err != nil {
		return nil, err
	}
	info, err := os.Stat(base)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path %q is not a directory", displayPath)
	}
	var lines []string
	lines = append(lines, ".")
	entries := 0
	truncated := false
	var walk func(string, string, int) error
	walk = func(dir, prefix string, depth int) error {
		if depth >= a.MaxDepth || entries >= a.MaxEntries {
			if entries >= a.MaxEntries {
				truncated = true
			}
			return nil
		}
		children, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(children, func(i, j int) bool {
			if children[i].IsDir() != children[j].IsDir() {
				return children[i].IsDir()
			}
			return children[i].Name() < children[j].Name()
		})
		visible := make([]os.DirEntry, 0, len(children))
		for _, child := range children {
			if !a.IncludeHidden && strings.HasPrefix(child.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, child.Name())
			if t.Sandbox.IsDenied(path) {
				continue
			}
			if resolved, err := filepath.EvalSymlinks(path); err == nil && !t.Sandbox.InRoot(resolved) {
				continue
			}
			rel, _ := filepath.Rel(base, path)
			if matchGlobSet(filepath.ToSlash(rel), a.ExcludeGlobs, false) {
				continue
			}
			visible = append(visible, child)
		}
		for i, child := range visible {
			if entries >= a.MaxEntries {
				truncated = true
				return nil
			}
			connector := "├── "
			nextPrefix := prefix + "│   "
			if i == len(visible)-1 {
				connector = "└── "
				nextPrefix = prefix + "    "
			}
			name := child.Name()
			if child.IsDir() {
				name += "/"
			}
			lines = append(lines, prefix+connector+name)
			entries++
			if child.IsDir() {
				if err := walk(filepath.Join(dir, child.Name()), nextPrefix, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(base, "", 0); err != nil {
		return nil, err
	}
	return map[string]any{"path": displayPath, "cwd": a.Cwd, "tree": strings.Join(lines, "\n"), "entries": entries, "truncated": truncated, "max_depth": a.MaxDepth}, nil
}

type AppendFileArgs struct {
	Path            string `json:"path"`
	Cwd             string `json:"cwd"`
	PathMode        string `json:"path_mode"`
	Content         string `json:"content"`
	CreateIfMissing bool   `json:"create_if_missing"`
	EnsureNewline   bool   `json:"ensure_newline"`
	DryRun          bool   `json:"dry_run"`
	CreateBackup    bool   `json:"create_backup"`
}

func (t *Tools) AppendFile(raw json.RawMessage) (any, error) {
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
	info, statErr := os.Stat(p)
	missing := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !missing {
		return nil, statErr
	}
	operation := "patch"
	if missing {
		operation = "create"
	}
	if err := t.enforceFilePolicy(operation, displayPath, p, map[string]any{"tool": "fs_append_file", "bytes": len(a.Content), "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	if missing && !a.CreateIfMissing {
		return nil, fmt.Errorf("file does not exist: %s", a.Path)
	}
	original := ""
	mode := os.FileMode(0o600)
	if !missing {
		if info.IsDir() {
			return nil, errors.New("cannot append to a directory")
		}
		if info.Size() > t.Cfg.Limits.MaxReadBytes {
			return nil, fmt.Errorf("file too large: %d bytes", info.Size())
		}
		b, err := os.ReadFile(p) //nolint:gosec // path was resolved through the sandbox.
		if err != nil {
			return nil, err
		}
		if bytes.IndexByte(b, 0) >= 0 {
			return nil, errors.New("refusing binary file")
		}
		original = string(b)
		mode = info.Mode()
	}
	appendText := a.Content
	if a.EnsureNewline && appendText != "" && !strings.HasSuffix(appendText, "\n") {
		appendText += "\n"
	}
	updated := original + appendText
	diff, diffTruncated := compactUnifiedDiff(displayPath, original, updated, t.Cfg.Limits.DiffContextLines, t.Cfg.Limits.MaxDiffBytes)
	if a.DryRun {
		return map[string]any{"dry_run": true, "path": displayPath, "cwd": a.Cwd, "created": missing, "bytes_appended": len(appendText), "diff": diff, "diff_truncated": diffTruncated}, nil
	}
	if a.CreateBackup && !missing {
		if err := os.WriteFile(p+".bak", []byte(original), 0o600); err != nil { //nolint:gosec // backup path is derived from a sandbox-resolved path.
			return nil, err
		}
	}
	if missing {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return nil, err
		}
	}
	if err := atomicWrite(p, []byte(updated), mode); err != nil {
		return nil, err
	}
	return map[string]any{"dry_run": false, "path": displayPath, "cwd": a.Cwd, "created": missing, "bytes_appended": len(appendText), "diff": diff, "diff_truncated": diffTruncated}, nil
}

type MarkdownOutlineArgs struct {
	Path        string `json:"path"`
	Cwd         string `json:"cwd"`
	PathMode    string `json:"path_mode"`
	MaxSections int    `json:"max_sections"`
}

func (t *Tools) MarkdownOutline(raw json.RawMessage) (any, error) {
	var a MarkdownOutlineArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	content, displayPath, sections, err := t.readMarkdownForTool(a.Path, a.Cwd, a.PathMode, "md_outline")
	if err != nil {
		return nil, err
	}
	count := len(sections)
	if a.MaxSections <= 0 || a.MaxSections > 1000 {
		a.MaxSections = 200
	}
	truncated := count > a.MaxSections
	if truncated {
		sections = sections[:a.MaxSections]
	}
	return map[string]any{"path": displayPath, "cwd": a.Cwd, "sections": sections, "section_count": len(markdownSections(content)), "truncated": truncated}, nil
}

type MarkdownReadSectionArgs struct {
	Path     string `json:"path"`
	Cwd      string `json:"cwd"`
	PathMode string `json:"path_mode"`
	Section  string `json:"section"`
}

func (t *Tools) MarkdownReadSection(raw json.RawMessage) (any, error) {
	var a MarkdownReadSectionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	content, displayPath, sections, err := t.readMarkdownForTool(a.Path, a.Cwd, a.PathMode, "md_read_section")
	if err != nil {
		return nil, err
	}
	section, err := findMarkdownSection(sections, a.Section)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": displayPath, "cwd": a.Cwd, "section": section, "content": joinLines(splitLinesKeepEnd(content), section.LineStart, section.LineEnd)}, nil
}

type MarkdownReplaceSectionArgs struct {
	Path           string `json:"path"`
	Cwd            string `json:"cwd"`
	PathMode       string `json:"path_mode"`
	Section        string `json:"section"`
	Content        string `json:"content"`
	IncludeHeading bool   `json:"include_heading"`
	DryRun         bool   `json:"dry_run"`
	CreateBackup   bool   `json:"create_backup"`
}

func (t *Tools) MarkdownReplaceSection(raw json.RawMessage) (any, error) {
	var a MarkdownReplaceSectionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"tool": "md_replace_section", "section": a.Section, "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	original, info, sections, err := t.readMarkdownFile(p)
	if err != nil {
		return nil, err
	}
	section, err := findMarkdownSection(sections, a.Section)
	if err != nil {
		return nil, err
	}
	lines := splitLinesKeepEnd(original)
	start := section.LineStart
	if !a.IncludeHeading {
		start++
	}
	updatedLines := append([]string{}, lines[:start-1]...)
	updatedLines = append(updatedLines, normalizeMarkdownBlock(a.Content))
	updatedLines = append(updatedLines, lines[section.LineEnd:]...)
	return t.writeMarkdownUpdate(p, displayPath, a.Cwd, original, strings.Join(updatedLines, ""), info.Mode(), a.DryRun, a.CreateBackup, map[string]any{"section": section})
}

type MarkdownReplaceSectionHeadingArgs struct {
	Path         string `json:"path"`
	Cwd          string `json:"cwd"`
	PathMode     string `json:"path_mode"`
	Section      string `json:"section"`
	Title        string `json:"title"`
	Level        int    `json:"level"`
	DryRun       bool   `json:"dry_run"`
	CreateBackup bool   `json:"create_backup"`
}

func (t *Tools) MarkdownReplaceSectionHeading(raw json.RawMessage) (any, error) {
	var a MarkdownReplaceSectionHeadingArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"tool": "md_replace_section_heading", "section": a.Section, "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	original, info, sections, err := t.readMarkdownFile(p)
	if err != nil {
		return nil, err
	}
	section, err := findMarkdownSection(sections, a.Section)
	if err != nil {
		return nil, err
	}
	level := a.Level
	if level == 0 {
		level = section.Level
	}
	heading, err := markdownHeadingLine(level, a.Title)
	if err != nil {
		return nil, err
	}
	lines := splitLinesKeepEnd(original)
	updatedLines := append([]string{}, lines[:section.LineStart-1]...)
	updatedLines = append(updatedLines, heading)
	updatedLines = append(updatedLines, lines[section.LineStart:]...)
	extra := map[string]any{"section": section, "old_title": section.Title, "new_title": strings.TrimSpace(a.Title), "old_level": section.Level, "new_level": level}
	return t.writeMarkdownUpdate(p, displayPath, a.Cwd, original, strings.Join(updatedLines, ""), info.Mode(), a.DryRun, a.CreateBackup, extra)
}

type MarkdownInsertSectionArgs struct {
	Path          string `json:"path"`
	Cwd           string `json:"cwd"`
	PathMode      string `json:"path_mode"`
	AfterSection  string `json:"after_section"`
	BeforeSection string `json:"before_section"`
	Title         string `json:"title"`
	Level         int    `json:"level"`
	Content       string `json:"content"`
	DryRun        bool   `json:"dry_run"`
	CreateBackup  bool   `json:"create_backup"`
}

func (t *Tools) MarkdownInsertSection(raw json.RawMessage) (any, error) {
	var a MarkdownInsertSectionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.AfterSection == "" && a.BeforeSection == "" {
		return nil, errors.New("after_section or before_section is required")
	}
	if a.AfterSection != "" && a.BeforeSection != "" {
		return nil, errors.New("use only one of after_section or before_section")
	}
	return t.markdownInsertSection(a)
}

type MarkdownAppendSectionArgs struct {
	Path            string `json:"path"`
	Cwd             string `json:"cwd"`
	PathMode        string `json:"path_mode"`
	Title           string `json:"title"`
	Level           int    `json:"level"`
	Content         string `json:"content"`
	DryRun          bool   `json:"dry_run"`
	CreateBackup    bool   `json:"create_backup"`
	CreateIfMissing bool   `json:"create_if_missing"`
}

func (t *Tools) MarkdownAppendSection(raw json.RawMessage) (any, error) {
	var a MarkdownAppendSectionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	heading, err := markdownHeadingLine(a.Level, a.Title)
	if err != nil {
		return nil, err
	}
	appendArgs := AppendFileArgs{Path: a.Path, Cwd: a.Cwd, PathMode: a.PathMode, Content: "\n" + heading + normalizeMarkdownBlock(a.Content), CreateIfMissing: a.CreateIfMissing, EnsureNewline: true, DryRun: a.DryRun, CreateBackup: a.CreateBackup}
	b, err := json.Marshal(appendArgs)
	if err != nil {
		return nil, err
	}
	return t.AppendFile(b)
}

type MarkdownAppendSubsectionArgs struct {
	Path          string `json:"path"`
	Cwd           string `json:"cwd"`
	PathMode      string `json:"path_mode"`
	ParentSection string `json:"parent_section"`
	Title         string `json:"title"`
	Level         int    `json:"level"`
	Content       string `json:"content"`
	DryRun        bool   `json:"dry_run"`
	CreateBackup  bool   `json:"create_backup"`
}

func (t *Tools) MarkdownAppendSubsection(raw json.RawMessage) (any, error) {
	var a MarkdownAppendSubsectionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"tool": "md_append_subsection", "parent_section": a.ParentSection, "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	original, info, sections, err := t.readMarkdownFile(p)
	if err != nil {
		return nil, err
	}
	parent, err := findMarkdownSection(sections, a.ParentSection)
	if err != nil {
		return nil, err
	}
	level := a.Level
	if level == 0 {
		level = parent.Level + 1
	}
	if level <= parent.Level {
		return nil, fmt.Errorf("subsection level must be greater than parent level %d", parent.Level)
	}
	heading, err := markdownHeadingLine(level, a.Title)
	if err != nil {
		return nil, err
	}
	lines := splitLinesKeepEnd(original)
	insertAt := parent.LineEnd
	block := markdownSectionInsertBlock(lines, insertAt, heading+normalizeMarkdownBlock(a.Content))
	updatedLines := append([]string{}, lines[:insertAt]...)
	updatedLines = append(updatedLines, block)
	updatedLines = append(updatedLines, lines[insertAt:]...)
	extra := map[string]any{"parent_section": parent, "inserted_title": a.Title, "inserted_level": level}
	return t.writeMarkdownUpdate(p, displayPath, a.Cwd, original, strings.Join(updatedLines, ""), info.Mode(), a.DryRun, a.CreateBackup, extra)
}

func markdownSectionInsertBlock(lines []string, insertAt int, block string) string {
	if block == "" {
		return block
	}
	before := strings.Join(lines[:insertAt], "")
	after := strings.Join(lines[insertAt:], "")
	var prefix string
	if before != "" && !strings.HasSuffix(before, "\n\n") {
		if strings.HasSuffix(before, "\n") {
			prefix = "\n"
		} else {
			prefix = "\n\n"
		}
	}
	var suffix string
	if after != "" && !strings.HasPrefix(after, "\n") && !strings.HasSuffix(block, "\n\n") {
		suffix = "\n"
	}
	return prefix + block + suffix
}

func (t *Tools) markdownInsertSection(a MarkdownInsertSectionArgs) (any, error) {
	heading, err := markdownHeadingLine(a.Level, a.Title)
	if err != nil {
		return nil, err
	}
	p, displayPath, err := t.resolvePath(a.Path, a.Cwd, a.PathMode)
	if err != nil {
		return nil, err
	}
	selector := a.AfterSection
	if selector == "" {
		selector = a.BeforeSection
	}
	if err := t.enforceFilePolicy("patch", displayPath, p, map[string]any{"tool": "md_insert_section", "section": selector, "cwd": a.Cwd, "dry_run": a.DryRun}); err != nil {
		return nil, err
	}
	original, info, sections, err := t.readMarkdownFile(p)
	if err != nil {
		return nil, err
	}
	section, err := findMarkdownSection(sections, selector)
	if err != nil {
		return nil, err
	}
	lines := splitLinesKeepEnd(original)
	insertAt := section.LineEnd
	if a.BeforeSection != "" {
		insertAt = section.LineStart - 1
	}
	updatedLines := append([]string{}, lines[:insertAt]...)
	updatedLines = append(updatedLines, "\n"+heading+normalizeMarkdownBlock(a.Content))
	updatedLines = append(updatedLines, lines[insertAt:]...)
	return t.writeMarkdownUpdate(p, displayPath, a.Cwd, original, strings.Join(updatedLines, ""), info.Mode(), a.DryRun, a.CreateBackup, map[string]any{"section": section, "inserted_title": a.Title})
}

func (t *Tools) readMarkdownForTool(path, cwd, pathMode, tool string) (content, displayPath string, sections []MarkdownSection, err error) {
	p, displayPath, err := t.resolvePath(path, cwd, pathMode)
	if err != nil {
		return "", "", nil, err
	}
	if err := t.enforceFilePolicy("read", displayPath, p, map[string]any{"tool": tool, "cwd": cwd}); err != nil {
		return "", "", nil, err
	}
	content, _, sections, err = t.readMarkdownFile(p)
	if err != nil {
		return "", "", nil, err
	}
	return content, displayPath, sections, nil
}

func (t *Tools) readMarkdownFile(path string) (content string, info os.FileInfo, sections []MarkdownSection, err error) {
	info, err = os.Stat(path)
	if err != nil {
		return "", nil, nil, err
	}
	if info.IsDir() {
		return "", nil, nil, errors.New("cannot parse a directory as Markdown")
	}
	if info.Size() > t.Cfg.Limits.MaxReadBytes {
		return "", nil, nil, fmt.Errorf("file too large: %d bytes", info.Size())
	}
	b, err := os.ReadFile(path) //nolint:gosec // path was resolved through the sandbox by the caller.
	if err != nil {
		return "", nil, nil, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return "", nil, nil, errors.New("refusing binary file")
	}
	content = string(b)
	return content, info, markdownSections(content), nil
}

func (t *Tools) writeMarkdownUpdate(path, displayPath, cwd, original, updated string, mode os.FileMode, dryRun, createBackup bool, extra map[string]any) (any, error) {
	diff, diffTruncated := compactUnifiedDiff(displayPath, original, updated, t.Cfg.Limits.DiffContextLines, t.Cfg.Limits.MaxDiffBytes)
	res := map[string]any{"dry_run": dryRun, "path": displayPath, "cwd": cwd, "changed": updated != original, "diff": diff, "diff_truncated": diffTruncated}
	for k, v := range extra {
		res[k] = v
	}
	if dryRun {
		return res, nil
	}
	if createBackup {
		if err := os.WriteFile(path+".bak", []byte(original), 0o600); err != nil { //nolint:gosec // backup path is derived from a sandbox-resolved path.
			return nil, err
		}
	}
	if err := atomicWrite(path, []byte(updated), mode); err != nil {
		return nil, err
	}
	return res, nil
}
