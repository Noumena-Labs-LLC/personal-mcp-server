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

func fileDeleteApprovalTestTools(t *testing.T) (*Tools, *approval.Manager, string) {
	t.Helper()
	tools, approver, root := fileApprovalTestTools(t)
	tools.Cfg.FilePolicy.WriteDefault = "prompt"
	return tools, approver, root
}

func TestDeleteFileContextApprovalCancelledRequestRemovesPending(t *testing.T) {
	tools, approver, root := fileDeleteApprovalTestTools(t)
	if err := os.WriteFile(filepath.Join(root, "victim.txt"), []byte("bye"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tools.DeleteFileContext(ctx, json.RawMessage(`{"path":"victim.txt"}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if pending := approver.List(); len(pending) != 0 {
		t.Fatalf("expected cancelled approval to be removed, got %#v", pending)
	}
	if _, err := os.Stat(filepath.Join(root, "victim.txt")); err != nil {
		t.Fatalf("cancelled delete should leave file present: %v", err)
	}
}

func TestDeleteFileContextApprovalApproveRemovesFile(t *testing.T) {
	tools, approver, root := fileDeleteApprovalTestTools(t)
	path := filepath.Join(root, "victim.txt")
	if err := os.WriteFile(path, []byte("bye"), 0o600); err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan error, 1)
	go func() {
		_, err := tools.DeleteFileContext(context.Background(), json.RawMessage(`{"path":"victim.txt"}`))
		resultCh <- err
	}()
	req := waitForFileApprovalRequest(t, approver)
	if req.Kind != "file" || req.Action != "delete" {
		t.Fatalf("unexpected approval request: %#v", req)
	}
	if !approver.Decide(req.ID, approval.DecisionApprove) {
		t.Fatalf("failed to approve %s", req.ID)
	}
	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("approved delete failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approved delete did not finish")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file to be removed, got %v", err)
	}
}

func TestMoveFileContextRequiresBothApprovals(t *testing.T) {
	tools, approver, root := fileDeleteApprovalTestTools(t)
	tools.Cfg.FilePolicy.CreateDefault = "prompt"
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("move me"), 0o600); err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan error, 1)
	go func() {
		_, err := tools.MoveFileContext(context.Background(), json.RawMessage(`{"source_path":"old.txt","dest_path":"new.txt"}`))
		resultCh <- err
	}()

	first := waitForFileApprovalRequest(t, approver)
	if first.Action != "delete" {
		t.Fatalf("expected delete approval first, got %#v", first)
	}
	if !approver.Decide(first.ID, approval.DecisionApprove) {
		t.Fatalf("failed to approve %s", first.ID)
	}
	second := waitForFileApprovalActionAfter(t, approver, "create", first.ID)
	if !approver.Decide(second.ID, approval.DecisionApprove) {
		t.Fatalf("failed to approve %s", second.ID)
	}

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("approved move failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approved move did not finish")
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old path to be removed, got %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "new.txt")) //nolint:gosec // test reads a file created in t.TempDir.
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if got := string(b); !strings.Contains(got, "move me") {
		t.Fatalf("unexpected moved content %q", got)
	}
}

func waitForFileApprovalActionAfter(t *testing.T, approver *approval.Manager, action, previousID string) approval.Request {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, req := range approver.List() {
			if req.ID != previousID && req.Action == action {
				return req
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("approval request with action %q did not appear", action)
	return approval.Request{}
}
