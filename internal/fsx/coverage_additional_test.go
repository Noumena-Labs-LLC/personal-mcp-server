package fsx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestContextHelpers(t *testing.T) {
	if got := normalizeContext(context.TODO()); got == nil {
		t.Fatal("normalizeContext(nil) returned nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := contextErr(ctx); err == nil {
		t.Fatal("contextErr should report canceled context")
	}
	if err := contextErr(context.TODO()); err != nil {
		t.Fatalf("contextErr(context.TODO()) = %v", err)
	}
}

func TestStructuredAndWriteContextWrappers(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)

	if err := os.WriteFile(filepath.Join(root, "obj.json"), []byte(`{"name":"alpha","items":[1,2,3],"nested":{"k":"v"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lines.jsonl"), []byte("{\"name\":\"alpha\"}\n{\"name\":\"beta\"}\ninvalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("# One\n\n## Two\ncontent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "append.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "replace.txt"), []byte("alpha beta"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("json", func(t *testing.T) {
		out, err := tools.JSONKeysContext(context.Background(), rawJSON(t, JSONKeysArgs{Path: "obj.json", Limit: 2}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.TrimSpace(toJSONString(t, out)), "keys") {
			t.Fatalf("JSONKeysContext output: %#v", out)
		}
		if _, err := tools.JSONGetContext(context.Background(), rawJSON(t, jsonPathArgs{Path: "obj.json", Pointer: "/nested/k"})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.JSONSliceContext(context.Background(), rawJSON(t, JSONSliceArgs{Path: "obj.json", Pointer: "/items", Limit: 2})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.JSONValidateContext(context.Background(), rawJSON(t, jsonPathArgs{Path: "obj.json"})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.JSONOutlineContext(context.Background(), rawJSON(t, JSONOutlineArgs{Path: "obj.json", MaxDepth: 2, MaxChildren: 2})); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("jsonl", func(t *testing.T) {
		if _, err := tools.JSONLReadContext(context.Background(), rawJSON(t, JSONLReadArgs{Path: "lines.jsonl", Limit: 2})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.JSONLTailContext(context.Background(), rawJSON(t, JSONLTailArgs{Path: "lines.jsonl", Records: 2})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.JSONLValidateContext(context.Background(), rawJSON(t, JSONLValidateArgs{Path: "lines.jsonl", LimitErrors: 2})); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("markdown", func(t *testing.T) {
		if _, err := tools.MarkdownReadSectionContext(context.Background(), rawJSON(t, MarkdownReadSectionArgs{Path: "doc.md", Section: "two"})); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("writes", func(t *testing.T) {
		if _, err := tools.CreateDirContext(context.Background(), rawJSON(t, CreateDirArgs{Path: "newdir", Parents: boolPtr(true)})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.AppendFileContext(context.Background(), rawJSON(t, AppendFileArgs{Path: "append.txt", Content: " world", CreateIfMissing: true, EnsureNewline: false})); err != nil {
			t.Fatal(err)
		}
		if body, err := os.ReadFile(filepath.Join(root, "append.txt")); err != nil || !strings.Contains(string(body), "world") {
			t.Fatalf("append file content = %q, %v", string(body), err)
		}
		if _, err := tools.AppendFileContext(context.Background(), rawJSON(t, AppendFileArgs{Path: "missing.txt", Content: "new", CreateIfMissing: true})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.ApplyPatchContext(context.Background(), rawJSON(t, ApplyPatchArgs{Path: "replace.txt", Old: "alpha", New: "omega"})); err != nil {
			t.Fatal(err)
		}
		if body, err := os.ReadFile(filepath.Join(root, "replace.txt")); err != nil || !strings.Contains(string(body), "omega") {
			t.Fatalf("patch file content = %q, %v", string(body), err)
		}
		if _, err := tools.ReplaceRegexContext(context.Background(), rawJSON(t, ReplaceRegexArgs{Path: "replace.txt", Pattern: "omega", Replacement: "zeta"})); err != nil {
			t.Fatal(err)
		}
		if body, err := os.ReadFile(filepath.Join(root, "replace.txt")); err != nil || !strings.Contains(string(body), "zeta") {
			t.Fatalf("regex file content = %q, %v", string(body), err)
		}
		if _, err := tools.DeleteFilesContext(context.Background(), rawJSON(t, DeleteFilesArgs{Paths: []string{"missing.txt", "append.txt", "append.txt"}, MaxFiles: 10})); err != nil {
			t.Fatal(err)
		}
		if _, err := tools.DeleteFileContext(context.Background(), rawJSON(t, DeleteFileArgs{Path: "replace.txt"})); err != nil {
			t.Fatal(err)
		}
	})
}

func boolPtr(v bool) *bool { return &v }

func toJSONString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
