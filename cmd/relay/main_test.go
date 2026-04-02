package main

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type fakeGRPCStopper struct {
	mu               sync.Mutex
	gracefulCalled   bool
	stopCalled       bool
	gracefulStopFunc func()
	stopFunc         func()
}

func (f *fakeGRPCStopper) GracefulStop() {
	f.mu.Lock()
	f.gracefulCalled = true
	fn := f.gracefulStopFunc
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakeGRPCStopper) Stop() {
	f.mu.Lock()
	f.stopCalled = true
	fn := f.stopFunc
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakeGRPCStopper) snapshot() (gracefulCalled bool, stopCalled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gracefulCalled, f.stopCalled
}

func TestStopGRPCServerGraceful(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := &fakeGRPCStopper{}

	stopGRPCServer(logger, server, 20*time.Millisecond)

	gracefulCalled, stopCalled := server.snapshot()
	if !gracefulCalled {
		t.Fatalf("expected GracefulStop to be called")
	}
	if stopCalled {
		t.Fatalf("expected Stop not to be called when graceful shutdown succeeds")
	}
}

func TestStopGRPCServerFallsBackToForceStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	releaseGraceful := make(chan struct{})
	server := &fakeGRPCStopper{
		gracefulStopFunc: func() {
			<-releaseGraceful
		},
		stopFunc: func() {
			close(releaseGraceful)
		},
	}

	stopGRPCServer(logger, server, 20*time.Millisecond)

	gracefulCalled, stopCalled := server.snapshot()
	if !gracefulCalled {
		t.Fatalf("expected GracefulStop to be called")
	}
	if !stopCalled {
		t.Fatalf("expected Stop to be called after graceful shutdown timeout")
	}
}
