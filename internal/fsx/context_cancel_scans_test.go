package fsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchTextContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1 << 20
	cfg.Limits.MaxSearchFileBytes = 1 << 20
	tools := NewTools(cfg, nil)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("file-%03d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.SearchTextContext(ctx, json.RawMessage(`{"path":".","query":"needle","max_results":10}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestFindContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1 << 20
	cfg.Limits.MaxSearchFileBytes = 1 << 20
	tools := NewTools(cfg, nil)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("dir-%03d", i)
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.FindContext(ctx, json.RawMessage(`{"path":".","type":"dir","max_results":10}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestJSONLFilterContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1 << 20
	cfg.Limits.MaxSearchFileBytes = 1 << 20
	tools := NewTools(cfg, nil)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, `{"ts":"2026-01-01T00:%02d:00Z","kind":"keep","n":%d}`+"\n", i%60, i)
	}
	if err := os.WriteFile(filepath.Join(root, "records.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.JSONLFilterContext(ctx, json.RawMessage(`{"path":"records.jsonl","limit":10}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestJSONSearchContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1 << 20
	cfg.Limits.MaxSearchFileBytes = 1 << 20
	tools := NewTools(cfg, nil)
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < 300; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"name":"item-%03d","value":"needle"}`, i)
	}
	b.WriteString("]}")
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.JSONSearchContext(ctx, json.RawMessage(`{"path":"data.json","query":"needle","limit":10}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestMarkdownOutlineContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1 << 20
	cfg.Limits.MaxSearchFileBytes = 1 << 20
	tools := NewTools(cfg, nil)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "# Heading %03d\n\nbody\n\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.MarkdownOutlineContext(ctx, json.RawMessage(`{"path":"notes.md"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestReadFileContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1 << 20
	tools := NewTools(cfg, nil)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "line %03d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.ReadFileContext(ctx, json.RawMessage(`{"path":"big.txt","start_line":1,"max_lines":50}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestTailFileContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Limits.MaxReadBytes = 1 << 20
	tools := NewTools(cfg, nil)
	if err := os.WriteFile(filepath.Join(root, "tail.txt"), []byte(strings.Repeat("tail\n", 500)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.TailFileContext(ctx, json.RawMessage(`{"path":"tail.txt","lines":50}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestListDirContextCancelledExits(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	tools := NewTools(cfg, nil)
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry-%03d.txt", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.ListDirContext(ctx, json.RawMessage(`{"path":"dir","recursive":true,"max_entries":100}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
