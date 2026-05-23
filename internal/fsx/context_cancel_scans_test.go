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
