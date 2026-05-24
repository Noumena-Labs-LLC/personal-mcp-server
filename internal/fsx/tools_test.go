package fsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func resultMap(t *testing.T, out any) map[string]any {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", out)
	}
	return m
}

func resultString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("expected %q to be string, got %T", key, m[key])
	}
	return v
}

func resultInt(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	v, ok := m[key].(int)
	if !ok {
		t.Fatalf("expected %q to be int, got %T", key, m[key])
	}
	return v
}

func resultBool(t *testing.T, m map[string]any, key string) bool {
	t.Helper()
	v, ok := m[key].(bool)
	if !ok {
		t.Fatalf("expected %q to be bool, got %T", key, m[key])
	}
	return v
}

func resultWarnings(t *testing.T, m map[string]any) []string {
	t.Helper()
	v, ok := m["warnings"]
	if !ok {
		return nil
	}
	warnings, ok := v.([]string)
	if !ok {
		t.Fatalf("expected warnings to be []string, got %T", v)
	}
	return warnings
}

func resultIntMap(t *testing.T, v any, key string) map[string]int {
	t.Helper()
	m, ok := v.(map[string]int)
	if !ok {
		t.Fatalf("expected %q to be map[string]int, got %T", key, v)
	}
	return m
}

func TestGetFileInfoIncludesLargeFileGuidance(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 8
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "big.log"), []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.GetFileInfo(json.RawMessage(`{"path":"big.log"}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if !resultBool(t, m, "is_text") {
		t.Fatalf("expected text file info, got %#v", m)
	}
	if !resultBool(t, m, "truncated_recommended") {
		t.Fatalf("expected truncation recommendation, got %#v", m)
	}
	if _, ok := m["line_count_estimate"].(int64); !ok {
		t.Fatalf("expected line_count_estimate int64, got %T", m["line_count_estimate"])
	}
}

func TestReadFileWholeFileLargeFastFailsWithGuidance(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 4
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ReadFile(json.RawMessage(`{"path":"big.txt","whole_file":true}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if resultBool(t, m, "ok") {
		t.Fatalf("expected ok=false, got %#v", m)
	}
	if got := resultString(t, m, "error"); got != "file_too_large_for_full_read" {
		t.Fatalf("unexpected error %q", got)
	}
}

func TestTailFileReadsLastLinesFromLargeFile(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1024
	tools := NewTools(cfg, nil)
	content := strings.Join([]string{"one", "two", "three", "four"}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.TailFile(json.RawMessage(`{"path":"app.log","lines":2}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if got := resultString(t, m, "content"); got != "three\nfour\n" {
		t.Fatalf("unexpected tail content %q", got)
	}
	if got := resultInt(t, m, "lines_returned"); got != 2 {
		t.Fatalf("expected two returned lines, got %d", got)
	}
}

func TestReadFileRejectsBinaryAndLargeFiles(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 4
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.ReadFile(json.RawMessage(`{"path":"big.txt"}`)); err == nil {
		t.Fatal("expected large file rejection")
	}
	cfg.Limits.MaxReadBytes = 100
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{'a', 0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.ReadFile(json.RawMessage(`{"path":"bin.dat"}`)); err == nil {
		t.Fatal("expected binary file rejection")
	}
}

func TestApplyPatchDryRunAndWrite(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ApplyPatch(json.RawMessage(`{"path":"main.go","old":"world","new":"gopher","expected_replacements":1,"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if !strings.Contains(resultString(t, m, "diff"), "+gopher") {
		t.Fatalf("expected diff to include replacement: %#v", m)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "gopher") {
		t.Fatal("dry run should not write")
	}
	_, err = tools.ApplyPatch(json.RawMessage(`{"path":"main.go","edits":[{"old":"world","new":"gopher","expected_replacements":1}],"dry_run":false}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "gopher") {
		t.Fatal("expected patch to write")
	}
}

func TestApplyPatchReplacementCountWarnings(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("foo\nfoo\nfoo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := tools.ApplyPatch(json.RawMessage(`{"path":"main.go","old":"foo","new":"bar","expected_replacements":1,"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if got := resultInt(t, m, "replacements"); got != 1 {
		t.Fatalf("expected one replacement, got %d", got)
	}
	if warnings := resultWarnings(t, m); len(warnings) != 1 || !strings.Contains(warnings[0], "additional matches remain") {
		t.Fatalf("expected additional-match warning, got %#v", warnings)
	}

	out, err = tools.ApplyPatch(json.RawMessage(`{"path":"main.go","old":"foo","new":"bar","expected_replacements":9,"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	m = resultMap(t, out)
	if got := resultInt(t, m, "replacements"); got != 3 {
		t.Fatalf("expected three replacements, got %d", got)
	}
	if warnings := resultWarnings(t, m); len(warnings) != 1 || !strings.Contains(warnings[0], "only 3 were found") {
		t.Fatalf("expected fewer-found warning, got %#v", warnings)
	}

	if _, err := tools.ApplyPatch(json.RawMessage(`{"path":"main.go","old":"missing","new":"bar","expected_replacements":2,"dry_run":true}`)); err == nil {
		t.Fatal("expected missing old text to fail")
	}
}

func TestSearchTextCapsResults(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxSearchResults = 1
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("needle\nneedle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.SearchText(json.RawMessage(`{"path":".","query":"needle","max_results":5}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	matches := m["matches"]
	if got := reflect.ValueOf(matches).Len(); got != 1 {
		t.Fatalf("expected 1 result, got %d", got)
	}
}

func TestSearchTextOffsetPagination(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxSearchResults = 10
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("needle one\nneedle two\nneedle three\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.SearchText(json.RawMessage(`{"path":".","query":"needle","max_results":1,"offset":1}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if got := resultInt(t, m, "offset"); got != 1 {
		t.Fatalf("expected offset 1, got %d", got)
	}
	if got := resultInt(t, m, "next_offset"); got != 2 {
		t.Fatalf("expected next_offset 2, got %d", got)
	}
	matches := reflect.ValueOf(m["matches"])
	if matches.Len() != 1 {
		t.Fatalf("expected 1 match, got %d", matches.Len())
	}
	text := matches.Index(0).FieldByName("Text").String()
	if text != "needle two" {
		t.Fatalf("expected second match, got %q", text)
	}
}

func TestSearchTextHonorsPerCallMaxFileSize(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxSearchResults = 10
	cfg.Limits.MaxSearchFileBytes = 1024
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte("needle in large file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := tools.SearchText(json.RawMessage(`{"path":".","query":"needle","max_file_size":6}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if got, ok := m["applied_max_file_size"].(int64); !ok || got != 6 {
		t.Fatalf("expected applied_max_file_size=6, got %#v", m["applied_max_file_size"])
	}
	if got := resultInt(t, m, "skipped_too_large_count"); got != 2 {
		t.Fatalf("expected both files to be skipped, got %d", got)
	}

	out, err = tools.SearchText(json.RawMessage(`{"path":".","query":"needle","max_file_size":64}`))
	if err != nil {
		t.Fatal(err)
	}
	m = resultMap(t, out)
	if got := resultInt(t, m, "skipped_too_large_count"); got != 0 {
		t.Fatalf("expected no skipped files, got %d", got)
	}
	matches := reflect.ValueOf(m["matches"])
	if matches.Len() != 2 {
		t.Fatalf("expected 2 matches, got %d", matches.Len())
	}
}

func TestSearchTextRejectsMaxFileSizeAboveConfiguredLimit(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxSearchFileBytes = 8
	tools := NewTools(cfg, nil)
	if _, err := tools.SearchText(json.RawMessage(`{"path":".","query":"needle","max_file_size":9}`)); err == nil {
		t.Fatal("expected max_file_size limit error")
	}
}

func TestFindOffsetPagination(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := tools.Find(json.RawMessage(`{"path":".","type":"file","name_globs":["*.go"],"max_results":1,"offset":1}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if got := resultInt(t, m, "offset"); got != 1 {
		t.Fatalf("expected offset 1, got %d", got)
	}
	if got := resultInt(t, m, "next_offset"); got != 2 {
		t.Fatalf("expected next_offset 2, got %d", got)
	}
	results := reflect.ValueOf(m["results"])
	if results.Len() != 1 {
		t.Fatalf("expected 1 result, got %d", results.Len())
	}
	path := results.Index(0).FieldByName("Path").String()
	if path != "b.go" {
		t.Fatalf("expected b.go, got %q", path)
	}
}

func TestCreateFileRefusesOverwriteAndCreatesNew(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	out, err := tools.CreateFile(json.RawMessage(`{"path":"new.txt","content":"hello","fail_if_exists":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resultBool(t, resultMap(t, out), "created") {
		t.Fatalf("expected created response, got %#v", out)
	}
	if _, err := tools.CreateFile(json.RawMessage(`{"path":"new.txt","content":"again","fail_if_exists":true}`)); err == nil {
		t.Fatal("expected overwrite refusal")
	}
}

func TestCreateFileRejectsSecretName(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	if _, err := tools.CreateFile(json.RawMessage(`{"path":".env","content":"SECRET=1"}`)); err == nil {
		t.Fatal("expected secret file rejection")
	}
}

func TestApplyPatchCompactDiffForSingleLineInsertion(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.DiffContextLines = 3
	cfg.Limits.MaxDiffBytes = 200000
	cfg.Limits.MaxReadBytes = 20000
	cfg.Limits.MaxWriteBytes = 20000
	tools := NewTools(cfg, nil)
	var original strings.Builder
	for i := 1; i <= 1200; i++ {
		fmt.Fprintf(&original, "line %04d\n", i)
	}
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte(original.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	old := "line 0600\n"
	newText := "line 0600\ninserted\n"
	req, err := json.Marshal(map[string]any{"path": "large.txt", "old": old, "new": newText, "expected_replacements": 1, "dry_run": true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tools.ApplyPatch(req)
	if err != nil {
		t.Fatal(err)
	}
	diff := resultString(t, resultMap(t, out), "diff")
	if !strings.Contains(diff, "+inserted") {
		t.Fatalf("expected insertion in diff: %s", diff)
	}
	if got := strings.Count(diff, "\n"); got > 30 {
		t.Fatalf("expected compact diff, got %d lines", got)
	}
}

func TestApplyUnifiedPatchDryRunAndWrite(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.DiffContextLines = 3
	cfg.Limits.MaxDiffBytes = 200000
	cfg.Limits.MaxPatchBytes = 200000
	tools := NewTools(cfg, nil)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := `--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
 
 func main() {
+	println("hi")
 }
`
	req, err := json.Marshal(map[string]any{"patch": patch, "dry_run": true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tools.ApplyUnifiedPatch(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultString(t, resultMap(t, out), "diff"), "+\tprintln(\"hi\")") {
		t.Fatalf("expected compact unified diff, got %#v", out)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "println") {
		t.Fatal("dry run should not write")
	}
	req, err = json.Marshal(map[string]any{"patch": patch})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.ApplyUnifiedPatch(req); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "println") {
		t.Fatal("expected unified patch to write")
	}
}

func TestApplyUnifiedPatchRejectsDelete(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxPatchBytes = 200000
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := `--- a/main.go
+++ /dev/null
@@ -1,1 +0,0 @@
-hello
`
	req, err := json.Marshal(map[string]any{"patch": patch})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tools.ApplyUnifiedPatch(req); err == nil {
		t.Fatal("expected delete patch rejection")
	}
}

func TestToolsUseCwdForRelativePaths(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("hello cwd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ReadFile(json.RawMessage(`{"cwd":"project","path":"README.md","max_lines":10}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if got := resultString(t, m, "content"); !strings.Contains(got, "hello cwd") {
		t.Fatalf("expected cwd-relative file content, got %q", got)
	}
}

func TestFindFiltersByGlobAndType(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.Find(json.RawMessage(`{"path":".","type":"file","name_globs":["*.go"],"max_results":10}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	results := reflect.ValueOf(m["results"])
	if results.Len() != 1 {
		t.Fatalf("expected one result, got %#v", m["results"])
	}
}

func TestListDirSurfacesIgnoredEntries(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, ".hidden.txt"), []byte("hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ListDir(json.RawMessage(`{"path":".","include_hidden":false}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if got := resultInt(t, m, "ignored_count"); got != 1 {
		t.Fatalf("expected ignored_count=1, got %d", got)
	}
	counts := resultIntMap(t, m["ignored_counts"], "ignored_counts")
	if counts["hidden"] != 1 {
		t.Fatalf("expected hidden ignored count, got %#v", counts)
	}
}

func TestSearchTextSurfacesIgnoredReasons(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxSearchFileBytes = 1024
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, ".hidden.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte("needle in large file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{'n', 'e', 0, 'd', 'l', 'e'}, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.SearchText(json.RawMessage(`{"path":".","query":"needle","max_file_size":8}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	counts := resultIntMap(t, m["ignored_counts"], "ignored_counts")
	if counts["too_large"] != 1 || counts["binary"] != 1 {
		t.Fatalf("unexpected ignored_counts %#v", counts)
	}
}

func TestFindSurfacesIgnoredReasons(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.Find(json.RawMessage(`{"path":".","type":"file","name_globs":["*.go"],"exclude_globs":["a.txt"],"max_results":10}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	counts := resultIntMap(t, m["ignored_counts"], "ignored_counts")
	if counts["exclude_glob"] != 1 {
		t.Fatalf("expected exclude_glob ignored count, got %#v", counts)
	}
}

func TestReplaceRegexDryRunAndWrite(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 2000
	cfg.Limits.MaxWriteBytes = 2000
	tools := NewTools(cfg, nil)
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ReplaceRegex(json.RawMessage(`{"path":"a.txt","pattern":"beta","replacement":"gamma","max_replacements":1,"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultString(t, resultMap(t, out), "diff"), "+gamma") {
		t.Fatalf("expected replacement diff, got %#v", out)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "gamma") {
		t.Fatal("dry run should not write")
	}
	_, err = tools.ReplaceRegex(json.RawMessage(`{"path":"a.txt","pattern":"beta","replacement":"gamma","max_replacements":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "gamma") != 1 {
		t.Fatalf("expected exactly one replacement, got %q", string(b))
	}
}

func TestMarkdownOutlineReadAndReplaceSection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("# Title\n\nIntro\n\n## Install\n\nold body\n\n### Notes\n\nchild\n\n## Usage\n\nrun\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := NewTools(testConfig(root), nil)

	outlineRaw, err := tools.MarkdownOutline(json.RawMessage(`{"path":"README.md"}`))
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	outline := resultMap(t, outlineRaw)
	sections, ok := outline["sections"].([]MarkdownSection)
	if !ok {
		t.Fatalf("expected markdown sections, got %T", outline["sections"])
	}
	if len(sections) != 4 {
		t.Fatalf("expected 4 sections, got %d", len(sections))
	}
	if sections[1].ID != "install" || sections[1].LineEnd != 12 {
		t.Fatalf("unexpected install section: %#v", sections[1])
	}

	readRaw, err := tools.MarkdownReadSection(json.RawMessage(`{"path":"README.md","section":"install"}`))
	if err != nil {
		t.Fatalf("read section: %v", err)
	}
	read := resultMap(t, readRaw)
	if !strings.Contains(resultString(t, read, "content"), "### Notes") {
		t.Fatalf("expected child section in content: %#v", read)
	}

	replaceRaw, err := tools.MarkdownReplaceSection(json.RawMessage(`{"path":"README.md","section":"install","content":"new body\n","dry_run":true}`))
	if err != nil {
		t.Fatalf("replace dry run: %v", err)
	}
	replace := resultMap(t, replaceRaw)
	if resultBool(t, replace, "changed") != true || !strings.Contains(resultString(t, replace, "diff"), "new body") {
		t.Fatalf("unexpected replace result: %#v", replace)
	}
}

func TestAppendFileAndMarkdownAppendSection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# Changelog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := NewTools(testConfig(root), nil)

	if _, err := tools.AppendFile(json.RawMessage(`{"path":"CHANGELOG.md","content":"\nentry","ensure_newline":true}`)); err != nil {
		t.Fatalf("append file: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatalf("read appended file: %v", err)
	}
	if !strings.HasSuffix(string(b), "entry\n") {
		t.Fatalf("expected newline-appended content, got %q", string(b))
	}

	if _, err := tools.MarkdownAppendSection(json.RawMessage(`{"path":"CHANGELOG.md","title":"Next","level":2,"content":"- item\n"}`)); err != nil {
		t.Fatalf("append markdown section: %v", err)
	}
	b, err = os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatalf("read markdown append: %v", err)
	}
	if !strings.Contains(string(b), "## Next\n- item") {
		t.Fatalf("expected appended section, got %q", string(b))
	}
}

func TestMarkdownReplaceSectionHeadingAndAppendSubsection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("# Title\n\n## Parent\nbody\n\n## Other\nother\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := NewTools(testConfig(root), nil)

	if _, err := tools.MarkdownReplaceSectionHeading(json.RawMessage(`{"path":"README.md","section":"parent","title":"Renamed Parent","dry_run":true}`)); err != nil {
		t.Fatalf("replace heading dry run: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Renamed Parent") {
		t.Fatalf("dry run should not rename heading: %q", string(b))
	}

	if _, err := tools.MarkdownReplaceSectionHeading(json.RawMessage(`{"path":"README.md","section":"parent","title":"Renamed Parent","level":2}`)); err != nil {
		t.Fatalf("replace heading: %v", err)
	}
	if _, err := tools.MarkdownAppendSubsection(json.RawMessage(`{"path":"README.md","parent_section":"renamed-parent","title":"Child","content":"child body\n"}`)); err != nil {
		t.Fatalf("append subsection: %v", err)
	}
	b, err = os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "## Renamed Parent\nbody\n\n### Child\nchild body") {
		t.Fatalf("expected renamed parent with child subsection, got %q", text)
	}
	if strings.Index(text, "### Child") > strings.Index(text, "## Other") {
		t.Fatalf("expected child subsection before next top-level sibling, got %q", text)
	}
}

func TestMarkdownParserDuplicateHeadingsAndFencedCode(t *testing.T) {
	content := "# Title\n\n```\n## Not a heading\n```\n\n## Repeat\nfirst\n\n## Repeat\nsecond\n"
	sections := ParseMarkdownSections(content)
	if len(sections) != 3 {
		t.Fatalf("expected 3 headings excluding fenced code, got %#v", sections)
	}
	if sections[1].ID != "repeat" || sections[2].ID != "repeat-2" {
		t.Fatalf("expected stable duplicate ids, got %#v", sections)
	}
	if _, err := FindMarkdownSection(sections, "Repeat"); err == nil {
		t.Fatal("expected title selector to be ambiguous for duplicate headings")
	}
	section, err := FindMarkdownSection(sections, "repeat-2")
	if err != nil {
		t.Fatalf("find duplicate section by id: %v", err)
	}
	if got := MarkdownSectionContent(content, section); !strings.Contains(got, "second") || strings.Contains(got, "first") {
		t.Fatalf("unexpected duplicate section content: %q", got)
	}
}

func TestMarkdownInsertSectionBeforeAndAfter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("# Title\n\n## First\nfirst\n\n## Last\nlast\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := NewTools(testConfig(root), nil)

	if _, err := tools.MarkdownInsertSection(json.RawMessage(`{"path":"README.md","after_section":"first","title":"Middle","level":2,"content":"middle\n"}`)); err != nil {
		t.Fatalf("insert after: %v", err)
	}
	if _, err := tools.MarkdownInsertSection(json.RawMessage(`{"path":"README.md","before_section":"last","title":"Before Last","level":3,"content":"before last\n","dry_run":true}`)); err != nil {
		t.Fatalf("insert before dry run: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "## Middle\nmiddle") {
		t.Fatalf("expected inserted middle section, got %q", text)
	}
	if strings.Contains(text, "Before Last") {
		t.Fatalf("dry run should not write before-last section: %q", text)
	}
}

func TestAppendFileCreateIfMissingDryRunAndPolicyDeny(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	if _, err := tools.AppendFile(json.RawMessage(`{"path":"new.txt","content":"hello","create_if_missing":true,"dry_run":true}`)); err != nil {
		t.Fatalf("append create dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("dry run should not create file, stat err=%v", err)
	}
	if _, err := tools.AppendFile(json.RawMessage(`{"path":"new.txt","content":"hello","create_if_missing":true}`)); err != nil {
		t.Fatalf("append create: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "new.txt")) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("unexpected created append content %q", string(b))
	}

	denyCfg := testConfig(root)
	denyCfg.FilePolicy.PatchDefault = "deny"
	denyTools := NewTools(denyCfg, nil)
	if _, err := denyTools.AppendFile(json.RawMessage(`{"path":"new.txt","content":"!"}`)); err == nil {
		t.Fatal("expected append to be denied by patch policy")
	}
}

func TestParseGitStatusPorcelainIncludesRenameAndLimit(t *testing.T) {
	raw := " M modified.txt\x00R  new.txt\x00old.txt\x00?? untracked.txt\x00"
	entries := parseGitStatusPorcelain(raw, 2)
	if len(entries) != 2 {
		t.Fatalf("expected two limited entries, got %#v", entries)
	}
	if entries[0]["path"] != "modified.txt" || entries[0]["worktree"] != "M" {
		t.Fatalf("unexpected modified entry: %#v", entries[0])
	}
	if entries[1]["path"] != "new.txt" || entries[1]["previous_path"] != "old.txt" {
		t.Fatalf("unexpected rename entry: %#v", entries[1])
	}
}

func TestGitStatusIncludesAndOmitsUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	cmd := exec.CommandContext(context.Background(), "git", "init") //nolint:gosec // test runs fixed git command in t.TempDir.
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	tracked := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(context.Background(), "git", "add", "tracked.txt") //nolint:gosec // test runs fixed git command in t.TempDir.
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, string(out))
	}
	cmd = exec.CommandContext(context.Background(), "git", "-c", "user.email=test@example.invalid", "-c", "user.name=Test", "commit", "-m", "init") //nolint:gosec // test runs fixed git command in t.TempDir.
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, string(out))
	}
	if err := os.WriteFile(tracked, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools := NewTools(testConfig(root), nil)
	withRaw, err := tools.GitStatus(json.RawMessage(`{"path":".","include_untracked":true}`))
	if err != nil {
		t.Fatalf("git status with untracked: %v", err)
	}
	withText := fmt.Sprintf("%#v", withRaw)
	if !strings.Contains(withText, "tracked.txt") || !strings.Contains(withText, "untracked.txt") {
		t.Fatalf("expected tracked and untracked files, got %s", withText)
	}
	withoutRaw, err := tools.GitStatus(json.RawMessage(`{"path":".","include_untracked":false}`))
	if err != nil {
		t.Fatalf("git status without untracked: %v", err)
	}
	withoutText := fmt.Sprintf("%#v", withoutRaw)
	if !strings.Contains(withoutText, "tracked.txt") {
		t.Fatalf("expected tracked file, got %s", withoutText)
	}
	if strings.Contains(withoutText, "untracked.txt") {
		t.Fatalf("did not expect untracked file, got %s", withoutText)
	}
}

func TestReplaceFileReplacesWithoutHashOrSizeGate(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxDiffBytes = 200000
	tools := NewTools(cfg, nil)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ReplaceFile(json.RawMessage(`{"path":"note.txt","content":"new\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if !resultBool(t, m, "changed") {
		t.Fatalf("expected changed response: %#v", m)
	}
	b, err := os.ReadFile(path) // #nosec G304 -- test path is created under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new\n" {
		t.Fatalf("expected replacement, got %q", string(b))
	}
}

func TestDeleteFileDeletesWithoutHashOrSizeGateAndRefusesDirectories(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.WriteFile(filepath.Join(root, "gone.txt"), []byte("bye"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.DeleteFile(json.RawMessage(`{"path":"."}`)); err == nil {
		t.Fatal("expected directory refusal")
	}
	out, err := tools.DeleteFile(json.RawMessage(`{"path":"gone.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resultBool(t, resultMap(t, out), "deleted") {
		t.Fatalf("expected deleted response: %#v", out)
	}
	if _, err := os.Stat(filepath.Join(root, "gone.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected removed file, got %v", err)
	}
}

func TestMoveFileMovesWithoutHashOrSizeGateAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.WriteFile(filepath.Join(root, "from.txt"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "to.txt"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.MoveFile(json.RawMessage(`{"source_path":"from.txt","dest_path":"to.txt"}`)); err == nil {
		t.Fatal("expected overwrite refusal")
	}
	out, err := tools.MoveFile(json.RawMessage(`{"source_path":"from.txt","dest_path":"new.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resultBool(t, resultMap(t, out), "moved") {
		t.Fatalf("expected moved response: %#v", out)
	}
	if _, err := os.Stat(filepath.Join(root, "from.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected source removed, got %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "new.txt")) // #nosec G304 -- test path is created under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "abc" {
		t.Fatalf("expected moved content, got %q", string(b))
	}
}

func TestCreateDirUsesParentsAndDryRun(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	out, err := tools.CreateDir(json.RawMessage(`{"path":"a/b/c","dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if !resultBool(t, m, "dry_run") || resultBool(t, m, "created") {
		t.Fatalf("unexpected dry-run response: %#v", m)
	}
	if _, err := os.Stat(filepath.Join(root, "a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created directory: %v", err)
	}
	out, err = tools.CreateDir(json.RawMessage(`{"path":"a/b/c"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resultBool(t, resultMap(t, out), "created") {
		t.Fatalf("expected created response: %#v", out)
	}
	info, err := os.Stat(filepath.Join(root, "a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
	out, err = tools.CreateDir(json.RawMessage(`{"path":"a/b/c"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resultBool(t, resultMap(t, out), "created") {
		t.Fatalf("existing directory should not be reported as created: %#v", out)
	}
}

func TestCreateDirRefusesFileConflictsAndMissingParentWhenParentsFalse(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.CreateDir(json.RawMessage(`{"path":"file.txt"}`)); err == nil {
		t.Fatal("expected file conflict")
	}
	if _, err := tools.CreateDir(json.RawMessage(`{"path":"missing/child","parents":false}`)); err == nil {
		t.Fatal("expected missing parent failure")
	}
}

func TestDeleteFilesDeletesWithoutDryRunOrPlanHash(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Tools.DeleteFiles.Enabled = true
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "a.tmp"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.DeleteFiles(json.RawMessage(`{"paths":["a.tmp"]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if !resultBool(t, m, "ok") || !resultBool(t, m, "deleted") {
		t.Fatalf("expected direct delete success, got %#v", m)
	}
	if _, err := os.Stat(filepath.Join(root, "a.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file to be deleted, err=%v", err)
	}
}

func TestDeleteFilesRefusesDirectories(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := tools.DeleteFiles(json.RawMessage(`{"paths":["dir"]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if resultBool(t, m, "ok") {
		t.Fatalf("expected ok=false for directory refusal: %#v", m)
	}
}

func TestDeleteFilesAllowMissing(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	if err := os.WriteFile(filepath.Join(root, "gone.tmp"), []byte("abcd"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tools.DeleteFiles(json.RawMessage(`{"paths":["gone.tmp","missing.tmp"],"allow_missing":true}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if !resultBool(t, m, "ok") || m["total_size_bytes"] != int64(4) {
		t.Fatalf("expected successful missing-aware delete, got %#v", m)
	}
	missing, ok := m["missing"].([]string)
	if !ok || len(missing) != 1 || missing[0] != "missing.tmp" {
		t.Fatalf("expected missing file in result, got %#v", m["missing"])
	}
	if _, err := os.Stat(filepath.Join(root, "gone.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected removed file, got %v", err)
	}
}

func TestDeleteFilesRootEscapeRefusal(t *testing.T) {
	root := t.TempDir()
	tools := NewTools(testConfig(root), nil)
	out, err := tools.DeleteFiles(json.RawMessage(`{"paths":["../outside.tmp"]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := resultMap(t, out)
	if resultBool(t, m, "ok") {
		t.Fatalf("expected root escape refusal, got %#v", m)
	}
}
