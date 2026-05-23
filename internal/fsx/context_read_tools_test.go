package fsx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestContextReadWrappersMatchBaseHandlers(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)

	if err := os.WriteFile(filepath.Join(root, "search.txt"), []byte("alpha\nbeta match\ngamma\n"), 0o600); err != nil {
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
