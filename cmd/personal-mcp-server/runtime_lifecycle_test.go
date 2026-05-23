package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveHandlerReturnsServiceUnavailableWhenRuntimeIsDraining(t *testing.T) {
	var called atomic.Bool
	state := &runtimeState{
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called.Store(true)
		}),
		idle: make(chan struct{}),
	}
	state.drain()

	live := newLiveHandler(state)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)

	done := make(chan struct{})
	go func() {
		live.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ServeHTTP did not return for a draining runtime")
	}

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}
	if called.Load() {
		t.Fatal("draining runtime handler should not be invoked")
	}
}
