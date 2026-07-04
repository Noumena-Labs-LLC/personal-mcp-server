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

func TestLiveHandlerReleasesInFlightOnPanic(t *testing.T) {
	var called atomic.Bool
	state := &runtimeState{
		handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called.Store(true)
			panic("boom")
		}),
		idle: make(chan struct{}),
	}

	live := newLiveHandler(state)
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)

	done := make(chan struct{})
	panicCh := make(chan any, 1)
	go func() {
		defer close(done)
		defer func() { panicCh <- recover() }()
		live.ServeHTTP(rr, req)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ServeHTTP did not return after a handler panic")
	}

	if got := <-panicCh; got == nil {
		t.Fatal("expected handler panic to propagate")
	}
	if !called.Load() {
		t.Fatal("panic handler was not invoked")
	}
	if got := state.inFlight.Load(); got != 0 {
		t.Fatalf("inFlight = %d, want 0", got)
	}

	closeDone := make(chan struct{})
	go func() {
		state.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("runtimeState.Close hung after panic")
	}
}
