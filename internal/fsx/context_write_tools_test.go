package fsx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
)

func fileApprovalTestTools(t *testing.T) (*Tools, *approval.Manager, string) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Approval.Enabled = true
	cfg.FilePolicy.CreateDefault = "prompt"
	cfg.FilePolicy.PatchDefault = "prompt"
	approver := approval.NewManager(time.Minute)
	return NewTools(cfg, approver), approver, root
}

func TestCreateFileContextApprovalCancelledRequestRemovesPending(t *testing.T) {
	tools, approver, _ := fileApprovalTestTools(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.CreateFileContext(ctx, json.RawMessage(`{"path":"new.txt","content":"hello"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if pending := approver.List(); len(pending) != 0 {
		t.Fatalf("expected cancelled approval to be removed, got %#v", pending)
	}
}

func TestCreateFileContextApprovalApproveCreatesFile(t *testing.T) {
	tools, approver, root := fileApprovalTestTools(t)
	resultCh := make(chan struct {
		out any
		err error
	}, 1)
	go func() {
		out, err := tools.CreateFileContext(context.Background(), json.RawMessage(`{"path":"new.txt","content":"hello"}`))
		resultCh <- struct {
			out any
			err error
		}{out: out, err: err}
	}()

	req := waitForFileApprovalRequest(t, approver)
	if req.Kind != "file" || req.Action != "create" {
		t.Fatalf("unexpected approval request: %#v", req)
	}
	if !approver.Decide(req.ID, approval.DecisionApprove) {
		t.Fatalf("failed to approve %s", req.ID)
	}
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("approved create failed: %v", result.err)
		}
		m := resultMap(t, result.out)
		if !resultBool(t, m, "created") {
			t.Fatalf("expected created result, got %#v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approved create did not finish")
	}
	b, err := os.ReadFile(filepath.Join(root, "new.txt")) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("unexpected created content %q", string(b))
	}
	if pending := approver.List(); len(pending) != 0 {
		t.Fatalf("expected approved request to be removed, got %#v", pending)
	}
}

func TestCreateFileContextApprovalDenyDoesNotCreateFile(t *testing.T) {
	tools, approver, root := fileApprovalTestTools(t)
	resultCh := make(chan error, 1)
	go func() {
		_, err := tools.CreateFileContext(context.Background(), json.RawMessage(`{"path":"new.txt","content":"hello"}`))
		resultCh <- err
	}()

	req := waitForFileApprovalRequest(t, approver)
	if !approver.Decide(req.ID, approval.DecisionDeny) {
		t.Fatalf("failed to deny %s", req.ID)
	}
	select {
	case err := <-resultCh:
		if err == nil || !strings.Contains(err.Error(), "approval denied") {
			t.Fatalf("expected approval denial, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("denied create did not finish")
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected denied create to leave file absent, got %v", err)
	}
}

func TestReplaceFileContextApprovalApproveWritesFile(t *testing.T) {
	tools, approver, root := fileApprovalTestTools(t)
	path := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan error, 1)
	go func() {
		_, err := tools.ReplaceFileContext(context.Background(), json.RawMessage(`{"path":"existing.txt","content":"new"}`))
		resultCh <- err
	}()

	req := waitForFileApprovalRequest(t, approver)
	if req.Kind != "file" || req.Action != "patch" {
		t.Fatalf("unexpected approval request: %#v", req)
	}
	if !approver.Decide(req.ID, approval.DecisionApprove) {
		t.Fatalf("failed to approve %s", req.ID)
	}
	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("approved replace failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approved replace did not finish")
	}
	b, err := os.ReadFile(path) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Fatalf("unexpected replaced content %q", string(b))
	}
}

func waitForFileApprovalRequest(t *testing.T, approver *approval.Manager) approval.Request {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending := approver.List()
		if len(pending) == 1 {
			return pending[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("approval request did not appear")
	return approval.Request{}
}
