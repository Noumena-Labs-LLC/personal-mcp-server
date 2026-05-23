package fsx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContextReadWrappersMatchBaseHandlers(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)

	if err := os.WriteFile(filepath.Join(root, "search.txt"), []byte("alpha\nbeta match\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("line\n", 200)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tail.txt"), []byte(strings.Repeat("tail\n", 200)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`{"items":[1,2,3],"meta":{"name":"example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "records.jsonl"), []byte("{\"kind\":\"keep\"}\n{\"kind\":\"skip\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Intro\n\n## Details\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(root, "dir", fmt.Sprintf("entry-%02d.txt", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		base func() (any, error)
		ctx  func() (any, error)
	}{
		{
			name: "search_text",
			base: func() (any, error) {
				return tools.SearchText(json.RawMessage(`{"path":".","query":"match","max_results":5}`))
			},
			ctx: func() (any, error) {
				return tools.SearchTextContext(context.Background(), json.RawMessage(`{"path":".","query":"match","max_results":5}`))
			},
		},
		{
			name: "find",
			base: func() (any, error) {
				return tools.Find(json.RawMessage(`{"path":".","name_globs":["*.json*"],"max_results":5}`))
			},
			ctx: func() (any, error) {
				return tools.FindContext(context.Background(), json.RawMessage(`{"path":".","name_globs":["*.json*"],"max_results":5}`))
			},
		},
		{
			name: "list_dir",
			base: func() (any, error) {
				return tools.ListDir(json.RawMessage(`{"path":"dir","max_entries":10}`))
			},
			ctx: func() (any, error) {
				return tools.ListDirContext(context.Background(), json.RawMessage(`{"path":"dir","max_entries":10}`))
			},
		},
		{
			name: "read_file",
			base: func() (any, error) {
				return tools.ReadFile(json.RawMessage(`{"path":"big.txt","start_line":1,"max_lines":5}`))
			},
			ctx: func() (any, error) {
				return tools.ReadFileContext(context.Background(), json.RawMessage(`{"path":"big.txt","start_line":1,"max_lines":5}`))
			},
		},
		{
			name: "tail_file",
			base: func() (any, error) {
				return tools.TailFile(json.RawMessage(`{"path":"tail.txt","lines":5}`))
			},
			ctx: func() (any, error) {
				return tools.TailFileContext(context.Background(), json.RawMessage(`{"path":"tail.txt","lines":5}`))
			},
		},
		{
			name: "json_outline",
			base: func() (any, error) {
				return tools.JSONOutline(json.RawMessage(`{"path":"data.json","pointer":"", "max_depth":2,"max_children":10}`))
			},
			ctx: func() (any, error) {
				return tools.JSONOutlineContext(context.Background(), json.RawMessage(`{"path":"data.json","pointer":"", "max_depth":2,"max_children":10}`))
			},
		},
		{
			name: "jsonl_info",
			base: func() (any, error) {
				return tools.JSONLInfo(json.RawMessage(`{"path":"records.jsonl","sample":10,"max_fields":10}`))
			},
			ctx: func() (any, error) {
				return tools.JSONLInfoContext(context.Background(), json.RawMessage(`{"path":"records.jsonl","sample":10,"max_fields":10}`))
			},
		},
		{
			name: "markdown_outline",
			base: func() (any, error) {
				return tools.MarkdownOutline(json.RawMessage(`{"path":"notes.md","max_sections":10}`))
			},
			ctx: func() (any, error) {
				return tools.MarkdownOutlineContext(context.Background(), json.RawMessage(`{"path":"notes.md","max_sections":10}`))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseOut, baseErr := tc.base()
			if baseErr != nil {
				t.Fatalf("base handler failed: %v", baseErr)
			}
			ctxOut, ctxErr := tc.ctx()
			if ctxErr != nil {
				t.Fatalf("context handler failed: %v", ctxErr)
			}
			if !reflect.DeepEqual(baseOut, ctxOut) {
				t.Fatalf("context handler output mismatch\nbase: %#v\nctx: %#v", baseOut, ctxOut)
			}
		})
	}
}
