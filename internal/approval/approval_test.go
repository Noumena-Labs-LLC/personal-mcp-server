package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagerApproveRequest(t *testing.T) {
	mgr := NewManager(2 * time.Second)
	resultCh := make(chan struct {
		decision string
		err      error
	}, 1)

	go func() {
		decision, err := mgr.Request(context.Background(), Request{Kind: "file", Action: "patch", Summary: "edit file"})
		resultCh <- struct {
			decision string
			err      error
		}{decision: decision, err: err}
	}()

	req := waitForPending(t, mgr)
	if req.ID == "" || req.Kind != "file" || req.Action != "patch" {
		t.Fatalf("unexpected pending request: %#v", req)
	}
	if !mgr.Decide(req.ID, DecisionApprove) {
		t.Fatalf("expected decide to accept known approval ID")
	}

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("expected approval success, got %v", got.err)
		}
		if got.decision != DecisionApprove {
			t.Fatalf("decision = %q, want %q", got.decision, DecisionApprove)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval result")
	}
	if got := mgr.List(); len(got) != 0 {
		t.Fatalf("expected request to be removed after decision, got %#v", got)
	}
}

func TestManagerDenyAndTimeoutDefaults(t *testing.T) {
	mgr := NewManagerWithDefault(time.Millisecond, DecisionApprove)
	decision, err := mgr.Request(context.Background(), Request{Summary: "timeout defaults to approve"})
	if err != nil {
		t.Fatalf("expected approve-on-timeout success, got %v", err)
	}
	if decision != DecisionApprove {
		t.Fatalf("decision = %q, want %q", decision, DecisionApprove)
	}

	denyMgr := NewManager(2 * time.Second)
	resultCh := make(chan error, 1)
	go func() {
		_, requestErr := denyMgr.Request(context.Background(), Request{Summary: "deny me"})
		resultCh <- requestErr
	}()
	req := waitForPending(t, denyMgr)
	if !denyMgr.Decide(req.ID, DecisionDeny) {
		t.Fatalf("expected deny decision to be delivered")
	}
	select {
	case requestErr := <-resultCh:
		if requestErr == nil || !strings.Contains(requestErr.Error(), "approval denied") {
			t.Fatalf("expected approval denied error, got %v", requestErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for denied approval")
	}
	if denyMgr.Decide("missing", DecisionApprove) {
		t.Fatalf("expected unknown approval ID to return false")
	}
}

func TestManagerContextCancellation(t *testing.T) {
	mgr := NewManager(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err := mgr.Request(ctx, Request{Summary: "cancelled"})
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if decision != DecisionDeny {
		t.Fatalf("decision = %q, want %q", decision, DecisionDeny)
	}
}

func TestManagerTimeoutDoesNotWaitForLogging(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mgr := NewManagerWithDefault(5*time.Millisecond, DecisionDeny)
	mgr.printf = func(_ string, _ ...any) (int, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return 0, nil
	}
	start := time.Now()
	decision, err := mgr.Request(context.Background(), Request{Summary: "blocked logging"})
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got decision=%q err=%v", decision, err)
	}
	if decision != DecisionDeny {
		t.Fatalf("decision = %q, want %q", decision, DecisionDeny)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("timeout path took too long: %s", elapsed)
	}

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("approval logging hook did not start")
	}
	close(release)
}

func TestApprovalHandler(t *testing.T) {
	mgr := NewManager(2 * time.Second)
	server := httptest.NewServer(mgr.Handler())
	defer server.Close()

	resultCh := make(chan error, 1)
	go func() {
		_, err := mgr.Request(context.Background(), Request{Kind: "cmd", Action: "run", Summary: "run command"})
		resultCh <- err
	}()
	req := waitForPending(t, mgr)

	resp, err := http.Get(server.URL + "/approvals") //nolint:gosec,noctx // test server URL is local and request is bounded by the test.
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var listed struct {
		Pending []Request `json:"pending"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Pending) != 1 || listed.Pending[0].ID != req.ID {
		t.Fatalf("unexpected pending list: %#v", listed.Pending)
	}

	postResp, err := http.Post(server.URL+"/approvals/"+req.ID+"/approve", "application/json", http.NoBody) //nolint:gosec,noctx // test server URL is local and request is bounded by the test.
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, postResp)
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d, want %d", postResp.StatusCode, http.StatusOK)
	}
	select {
	case requestErr := <-resultCh:
		if requestErr != nil {
			t.Fatalf("approval request returned error: %v", requestErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approved request")
	}

	assertStatus(t, mgr.Handler(), http.MethodPost, "/approvals/missing/approve", http.StatusNotFound)
	assertStatus(t, mgr.Handler(), http.MethodPost, "/approvals/missing/maybe", http.StatusBadRequest)
	assertStatus(t, mgr.Handler(), http.MethodGet, "/approvals/missing/approve", http.StatusMethodNotAllowed)
	assertStatus(t, mgr.Handler(), http.MethodPost, "/approvals/badpath", http.StatusBadRequest)
	assertStatus(t, mgr.Handler(), http.MethodPost, "/approvals", http.StatusMethodNotAllowed)
}

func waitForPending(t *testing.T, mgr *Manager) Request {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending := mgr.List()
		if len(pending) == 1 {
			return pending[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for pending approval")
	return Request{}
}

func assertStatus(t *testing.T, handler http.Handler, method, path string, want int) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s status = %d, want %d", method, path, rec.Code, want)
	}
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
}
