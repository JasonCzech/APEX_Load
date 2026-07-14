package main

import (
	"sync"
	"testing"
	"time"
)

type blockingGRPCStopper struct {
	stopOnce sync.Once
	stopCh   chan struct{}
	stopped  chan struct{}
}

func (s *blockingGRPCStopper) GracefulStop() {
	<-s.stopCh
	close(s.stopped)
}

func (s *blockingGRPCStopper) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func TestGracefulStopWithTimeoutForcesStop(t *testing.T) {
	server := &blockingGRPCStopper{stopCh: make(chan struct{}), stopped: make(chan struct{})}
	started := time.Now()
	if graceful := gracefulStopWithTimeout(server, 10*time.Millisecond); graceful {
		t.Fatal("expected the blocked graceful stop to time out")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("forced stop took too long: %v", elapsed)
	}
	select {
	case <-server.stopped:
	default:
		t.Fatal("GracefulStop goroutine was not released by Stop")
	}
}

type immediateGRPCStopper struct {
	forced bool
}

func (*immediateGRPCStopper) GracefulStop() {}
func (s *immediateGRPCStopper) Stop()       { s.forced = true }

func TestGracefulStopWithTimeoutCompletesGracefully(t *testing.T) {
	server := &immediateGRPCStopper{}
	if graceful := gracefulStopWithTimeout(server, time.Second); !graceful {
		t.Fatal("expected graceful stop to complete")
	}
	if server.forced {
		t.Fatal("Stop was called after GracefulStop completed")
	}
}
