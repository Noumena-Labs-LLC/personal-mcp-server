package fsx

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/policy"
)

type staticProjectPolicy struct {
	decision policy.Decision
	details  map[string]any
	matched  bool
	err      error
}

func (p *staticProjectPolicy) DecideProjectFile(_, _, _ string) (decision policy.Decision, details map[string]any, matched bool, err error) {
	return p.decision, p.details, p.matched, p.err
}

func mustAnySlice(t *testing.T, v any, key string) []any {
	t.Helper()
	items, ok := v.([]any)
	if !ok {
		t.Fatalf("expected %s to be []any, got %T", key, v)
	}
	return items
}

func TestMarkdownAndNumberHelperBranches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseMarkdownSectionsContext(ctx, "# one\n"); err == nil {
		t.Fatal("expected cancelled markdown parse to fail")
	}

	content := "# One\nbody\n```go\n# ignored\n```\n## Two\ntext\n# One\n"
	sections, err := ParseMarkdownSectionsContext(context.Background(), content)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %#v", sections)
	}
	if sections[0].ID != "one" || sections[2].ID != "one-2" {
		t.Fatalf("unexpected section ids: %#v", sections)
	}
	if _, err := FindMarkdownSection(sections, "One"); err == nil {
		t.Fatal("expected ambiguous section lookup to fail")
	}
	sec, err := FindMarkdownSection(sections, "one-2")
	if err != nil {
		t.Fatal(err)
	}
	if got := MarkdownSectionContent(content, sec); !strings.Contains(got, "# One") {
		t.Fatalf("MarkdownSectionContent = %q", got)
	}
	if got := joinLines([]string{"a\n", "b\n", "c\n"}, 2, 3); got != "b\nc\n" {
		t.Fatalf("joinLines = %q", got)
	}
	if got := joinLines([]string{"a\n", "b\n"}, 4, 5); got != "" {
		t.Fatalf("joinLines out of range = %q", got)
	}
	if got, err := markdownHeadingLine(0, "Title"); err != nil || got != "## Title\n" {
		t.Fatalf("markdownHeadingLine default = %q, %v", got, err)
	}
	if _, err := markdownHeadingLine(7, "Title"); err == nil {
		t.Fatal("expected invalid heading level to fail")
	}
	if _, err := markdownHeadingLine(1, ""); err == nil {
		t.Fatal("expected empty heading title to fail")
	}
	if got := normalizeMarkdownBlock("alpha"); got != "alpha\n" {
		t.Fatalf("normalizeMarkdownBlock = %q", got)
	}
	if got := normalizeMarkdownBlock(""); got != "" {
		t.Fatalf("normalizeMarkdownBlock empty = %q", got)
	}
	if got := truncateDiff("abcdef", 3); !strings.Contains(got, "diff truncated") {
		t.Fatalf("truncateDiff = %q", got)
	}
	if got := truncateDiff("abcdef", 0); got != "abcdef" {
		t.Fatalf("truncateDiff no limit = %q", got)
	}
	cases := []struct {
		name  string
		value any
		ok    bool
		want  float64
	}{
		{name: "float64", value: float64(1.5), ok: true, want: 1.5},
		{name: "int", value: 2, ok: true, want: 2},
		{name: "int64", value: int64(3), ok: true, want: 3},
		{name: "json.Number", value: json.Number("4.25"), ok: true, want: 4.25},
		{name: "string", value: "5.5", ok: true, want: 5.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := jsonNumberAsFloat(tc.value, tc.ok)
			if !ok {
				t.Fatal("expected conversion to succeed")
			}
			if got != tc.want {
				t.Fatalf("jsonNumberAsFloat = %v, want %v", got, tc.want)
			}
		})
	}
	if _, ok := jsonNumberAsFloat(nil, false); ok {
		t.Fatal("expected missing value to fail conversion")
	}
}

func TestJSONLAndPolicyHelperBranches(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := root
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = canonical
	}
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1024
	cfg.Limits.MaxSearchFileBytes = 1024
	cfg.Limits.MaxSearchResults = 10
	tools := NewTools(cfg, nil)

	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "file.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"id":1,"name":"alpha","nested":{"score":1},"ts":"2026-05-23T12:00:00Z"}` + "\n" +
		"\n" +
		"not-json\n" +
		`{"id":2,"name":"beta","nested":{"score":2},"ts":"2026-05-23T12:05:00Z"}` + "\n" +
		`{"id":3,"name":"gamma","nested":{"score":3},"ts":"2026-05-23T12:10:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "records.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	readOut, err := tools.JSONLRead(json.RawMessage(`{"path":"records.jsonl","offset":1,"limit":0}`))
	if err != nil {
		t.Fatal(err)
	}
	readMap := resultMap(t, readOut)
	if got := resultInt(t, readMap, "returned"); got != 2 {
		t.Fatalf("JSONLRead returned = %d, want 2", got)
	}
	records := mustAnySlice(t, readMap["records"], "records")
	if len(records) != 2 {
		t.Fatalf("JSONLRead records = %#v", records)
	}

	tailOut, err := tools.JSONLTail(json.RawMessage(`{"path":"records.jsonl","records":0}`))
	if err != nil {
		t.Fatal(err)
	}
	tailMap := resultMap(t, tailOut)
	if got := resultInt(t, tailMap, "records_returned"); got != 3 {
		t.Fatalf("JSONLTail records_returned = %d, want 3", got)
	}
	tailRecords := mustAnySlice(t, tailMap["records"], "records")
	if len(tailRecords) != 3 {
		t.Fatalf("JSONLTail records = %#v", tailRecords)
	}

	filtered, err := tools.JSONLFilter(json.RawMessage(`{"path":"records.jsonl","where":{"name":"beta"},"contains":{"name":"bet"},"exists":["nested.score"],"missing":["absent"],"numeric_gte":{"nested.score":2},"numeric_lte":{"nested.score":2},"ts_gte":"2026-05-23T12:00:00Z","ts_lte":"2026-05-23T12:10:00Z","limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	filteredMap := resultMap(t, filtered)
	if got := resultInt(t, filteredMap, "returned"); got != 1 {
		t.Fatalf("JSONLFilter returned = %d, want 1", got)
	}

	reversed, err := tools.JSONLFilter(json.RawMessage(`{"path":"records.jsonl","contains":{"name":"a"},"reverse":true,"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	reversedMap := resultMap(t, reversed)
	reversedRecords := mustAnySlice(t, reversedMap["records"], "records")
	if got := resultInt(t, reversedMap, "returned"); got != 2 || len(reversedRecords) != 2 {
		t.Fatalf("JSONLFilter reverse = %#v", reversedMap)
	}
	first, ok := reversedRecords[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first record to be map[string]any, got %T", reversedRecords[0])
	}
	if first["name"] != "gamma" {
		t.Fatalf("expected reverse ordering to keep newest match first, got %#v", reversedRecords)
	}

	if _, err := tools.JSONLFilter(json.RawMessage(`{"path":"records.jsonl","ts_gte":"bad-time"}`)); err == nil {
		t.Fatal("expected invalid timestamp to fail")
	}

	validated, err := tools.JSONLValidate(json.RawMessage(`{"path":"records.jsonl","limit_errors":1}`))
	if err != nil {
		t.Fatal(err)
	}
	validatedMap := resultMap(t, validated)
	if got := resultInt(t, validatedMap, "malformed_lines"); got != 1 {
		t.Fatalf("JSONLValidate malformed_lines = %d, want 1", got)
	}
	if samples, ok := validatedMap["error_samples"].([]map[string]any); !ok || len(samples) != 1 {
		t.Fatalf("JSONLValidate error_samples = %#v", samples)
	}

	if got, err := tools.ExplainFilePolicy("read", "records.jsonl", filepath.Join(root, "records.jsonl")); err != nil {
		t.Fatal(err)
	} else if _, ok := got["project"]; ok {
		t.Fatalf("did not expect project policy metadata without a project policy: %#v", got)
	}

	tools.ProjectPolicy = &staticProjectPolicy{
		decision: policy.Decision{Action: policy.ActionDeny, Rule: "project-rule"},
		details:  map[string]any{"project": true},
		matched:  true,
	}
	explained, err := tools.ExplainFilePolicy("read", "records.jsonl", filepath.Join(root, "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	effective, ok := explained["effective"].(policy.Decision)
	if !ok {
		t.Fatalf("expected effective policy decision, got %T", explained["effective"])
	}
	if effective.Action != policy.ActionDeny {
		t.Fatalf("expected project policy to win, got %#v", effective)
	}
	projectMeta, ok := explained["project"].(map[string]any)
	if !ok {
		t.Fatalf("expected project metadata map, got %T", explained["project"])
	}
	if projectMeta["matched"] != true {
		t.Fatalf("expected project policy metadata, got %#v", projectMeta)
	}

	tools.ProjectPolicy = &staticProjectPolicy{
		decision: policy.Decision{Action: policy.ActionAllow, Rule: "project-allow"},
		matched:  false,
	}
	if decision, err := tools.DecideFilePolicy("read", "records.jsonl", filepath.Join(root, "records.jsonl")); err != nil {
		t.Fatal(err)
	} else if decision.Action != policy.ActionAllow {
		t.Fatalf("expected global allow when project does not match, got %#v", decision)
	}

	if out, err := tools.ListRoots(nil); err != nil {
		t.Fatal(err)
	} else if roots, ok := resultMap(t, out)["roots"].([]string); !ok || len(roots) != 1 || roots[0] != resolvedRoot {
		t.Fatalf("ListRoots = %#v", out)
	}

	if resolved, display, err := tools.ResolveForTool("file.txt", "nested"); err != nil {
		t.Fatal(err)
	} else if resolved != filepath.Join(resolvedRoot, "nested", "file.txt") || display != filepath.Join("nested", "file.txt") {
		t.Fatalf("ResolveForTool = %q %q", resolved, display)
	}
	if got, display, err := tools.ResolveForTool("records.jsonl", ""); err != nil {
		t.Fatal(err)
	} else if gotPath, want := got, filepath.Join(resolvedRoot, "records.jsonl"); gotPath != want || display != "records.jsonl" {
		t.Fatalf("ResolveForTool = %q %q, want %q records.jsonl", gotPath, display, want)
	}

	sandbox := NewSandbox(cfg)
	if _, err := sandbox.ResolveWithMode("records.jsonl", "", "absolute"); err == nil {
		t.Fatal("expected absolute mode to require an absolute path")
	}
	if _, err := sandbox.ResolveWithMode("records.jsonl", "", "cwd_relative"); err == nil {
		t.Fatal("expected cwd_relative mode to require cwd")
	}
	if _, err := sandbox.ResolveWithMode(filepath.Join(root, "records.jsonl"), "", "root_relative"); err == nil {
		t.Fatal("expected root_relative mode to reject absolute path")
	}
	if _, err := sandbox.ResolveWithMode("records.jsonl", "", "invalid-mode"); err == nil {
		t.Fatal("expected invalid path mode to fail")
	}

	hintErr := pathStatError("same/path", "same", "same", os.ErrNotExist)
	if !strings.Contains(hintErr.Error(), "use path=\".\"") {
		t.Fatalf("pathStatError hint missing: %v", hintErr)
	}
	if got := pathStatError("other/path", "file", "cwd", os.ErrNotExist); !strings.Contains(got.Error(), "stat other/path") {
		t.Fatalf("pathStatError generic form missing: %v", got)
	}
}

func TestGitDiffAndTreeHelpers(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxCommandOutputBytes = 64 * 1024
	tools := NewTools(cfg, nil)

	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skip.txt"), []byte("skip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hidden"), []byte("hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "nested", "child.txt"), []byte("child\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	diffOut, err := tools.GitDiff(json.RawMessage(`{"path":"repo/tracked.txt","max_bytes":65536}`))
	if err != nil {
		t.Fatal(err)
	}
	diffMap := resultMap(t, diffOut)
	if got, ok := diffMap["diff"].(string); !ok || !strings.Contains(got, "+two") {
		t.Fatalf("GitDiff = %q", got)
	}

	treeOut, err := tools.Tree(json.RawMessage(`{"path":"repo","max_depth":2,"include_hidden":false,"exclude_globs":["skip.txt"]}`))
	if err != nil {
		t.Fatal(err)
	}
	treeMap := resultMap(t, treeOut)
	tree, ok := treeMap["tree"].(string)
	if !ok {
		t.Fatalf("expected tree output to be string, got %T", treeMap["tree"])
	}
	if !strings.Contains(tree, "tracked.txt") || !strings.Contains(tree, "nested/") {
		t.Fatalf("Tree = %q", tree)
	}
	if strings.Contains(tree, ".hidden") || strings.Contains(tree, "skip.txt") {
		t.Fatalf("Tree should omit hidden and excluded files: %q", tree)
	}
}
