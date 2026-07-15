package api

import (
	"context"
	"sync"
	"testing"
	"time"
)

type blockingRenewalLeaseStore struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (s *blockingRenewalLeaseStore) TryAcquireSchedulerLease(string, string, time.Time, time.Duration) (bool, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		return true, nil
	}
	<-s.release
	return false, nil
}

func TestExecutionLeadershipWatchdogExpiresBlockedRenewal(t *testing.T) {
	store := &blockingRenewalLeaseStore{release: make(chan struct{})}
	leadership := newExecutionLeadership(store, "watchdog")
	leadership.ttl = 80 * time.Millisecond
	leadership.renew = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		leadership.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for !leadership.leader.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !leadership.leader.Load() {
		t.Fatal("leadership was never acquired")
	}
	time.Sleep(150 * time.Millisecond)
	if leadership.leader.Load() {
		t.Fatal("watchdog did not expire a blocked renewal")
	}
	close(store.release)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("leadership loop did not stop")
	}
}

func TestInitializingRunContextCancelsWithLeadershipEpoch(t *testing.T) {
	leadership := newExecutionLeadership(&fakeLeaseStore{dfltOK: true}, "run-context")
	leadership.tick()
	if !leadership.IsLeader() {
		t.Fatal("leadership was not acquired")
	}
	runner := NewTestRunner(1, NewHub(), nil, WithExecutionLeadership(leadership))
	testRun := &ManagedTest{ID: "run"}
	runner.mu.Lock()
	runner.markTestInitializingLocked(testRun)
	runner.mu.Unlock()
	if err := testRun.executionCtx.Err(); err != nil {
		t.Fatalf("initial execution context already canceled: %v", err)
	}
	leadership.relinquish()
	select {
	case <-testRun.executionCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("leadership loss did not cancel the initializing run")
	}
}
