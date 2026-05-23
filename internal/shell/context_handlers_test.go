package shell

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/noumena-labs-llc/personal-mcp-server/internal/approval"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/config"
	"github.com/noumena-labs-llc/personal-mcp-server/internal/fsx"
)

func commandApprovalTestRunner(t *testing.T) (*Runner, *approval.Manager) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("printf command unavailable on Windows by default")
	}
	root := t.TempDir()
	cfg := shellTestConfig(root)
	cfg.Approval.Enabled = true
	cfg.CommandPolicy = config.CommandPolicyConfig{Default: "prompt"}
	approver := approval.NewManager(time.Minute)
	r := NewRunner(cfg, fsx.NewSandbox(cfg), approver, nil)
	return r, approver
}

func TestRunArgvContextApprovalCancelledRequestRemovesPending(t *testing.T) {
	r, approver := commandApprovalTestRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.RunArgvContext(ctx, json.RawMessage(`{"exec":"printf","args":["approved"],"cwd":"."}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if pending := approver.List(); len(pending) != 0 {
		t.Fatalf("expected cancelled approval to be removed, got %#v", pending)
	}
}

func TestRunArgvContextApprovalApproveStillRunsCommand(t *testing.T) {
	r, approver := commandApprovalTestRunner(t)
	resultCh := make(chan struct {
		out any
		err error
	}, 1)
	go func() {
		out, err := r.RunArgvContext(context.Background(), json.RawMessage(`{"exec":"printf","args":["approved"],"cwd":"."}`))
		resultCh <- struct {
			out any
			err error
		}{out: out, err: err}
	}()

	req := waitForApprovalRequest(t, approver)
	if !approver.Decide(req.ID, approval.DecisionApprove) {
		t.Fatalf("failed to approve %s", req.ID)
	}
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("approved command failed: %v", result.err)
		}
		stdout := shellResultStdout(t, shellResultMap(t, result.out))
		if stdout != "appr" {
			t.Fatalf("expected capped approved output, got %q", stdout)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approved command did not finish")
	}
	if pending := approver.List(); len(pending) != 0 {
		t.Fatalf("expected approved request to be removed, got %#v", pending)
	}
}

func TestRunArgvContextApprovalDenyStillFailsCommand(t *testing.T) {
	r, approver := commandApprovalTestRunner(t)
	resultCh := make(chan error, 1)
	go func() {
		_, err := r.RunArgvContext(context.Background(), json.RawMessage(`{"exec":"printf","args":["denied"],"cwd":"."}`))
		resultCh <- err
	}()

	req := waitForApprovalRequest(t, approver)
	if !approver.Decide(req.ID, approval.DecisionDeny) {
		t.Fatalf("failed to deny %s", req.ID)
	}
	select {
	case err := <-resultCh:
		if err == nil || !strings.Contains(err.Error(), "approval denied") {
			t.Fatalf("expected approval denial, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("denied command did not finish")
	}
	if pending := approver.List(); len(pending) != 0 {
		t.Fatalf("expected denied request to be removed, got %#v", pending)
	}
}

func waitForApprovalRequest(t *testing.T, approver *approval.Manager) approval.Request {
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
