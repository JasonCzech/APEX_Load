package api

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLockRWMutexWithContextRegistersWriterIntent(t *testing.T) {
	var mu sync.RWMutex
	mu.RLock()

	locked := make(chan error, 1)
	go func() {
		locked <- lockRWMutexWithContext(context.Background(), &mu)
	}()
	// Keep read pressure active while releasing the original reader. A polling
	// TryLock implementation can starve here; a real queued RWMutex writer must
	// eventually register intent, drain the readers, and acquire.
	stopReaders := make(chan struct{})
	var readers sync.WaitGroup
	for range 16 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				mu.RLock()
				mu.RUnlock() //nolint:staticcheck // the readers only contend for the lock
			}
		}()
	}
	mu.RUnlock()
	select {
	case err := <-locked:
		if err != nil {
			t.Fatalf("lockRWMutexWithContext: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued writer starved behind continuous readers")
	}
	if mu.TryRLock() {
		mu.RUnlock()
		t.Fatal("reader acquired while caller owned write lock")
	}
	close(stopReaders)
	mu.Unlock()
	readers.Wait()
}

func TestLockRWMutexWithContextReleasesLateAcquisition(t *testing.T) {
	var mu sync.RWMutex
	mu.RLock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := lockRWMutexWithContext(ctx, &mu); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	mu.RUnlock()

	deadline := time.Now().Add(time.Second)
	for !mu.TryLock() {
		if time.Now().After(deadline) {
			t.Fatal("late write-lock ownership was leaked")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Unlock()
}

func TestLockRWMutexWithContextDoesNotQueueAlreadyCancelledWriter(t *testing.T) {
	var mu sync.RWMutex
	mu.RLock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lockRWMutexWithContext(ctx, &mu); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}

	readerAcquired := make(chan struct{})
	go func() {
		mu.RLock()
		close(readerAcquired)
		mu.RUnlock()
	}()
	select {
	case <-readerAcquired:
	case <-time.After(time.Second):
		t.Fatal("an already-cancelled writer was queued behind the existing reader")
	}
	mu.RUnlock()
}

func TestLeadershipLossTeardownStopsRuntimeWithoutLifecyclePersistence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	oldLeadership := newExecutionLeadership(store, "old-controller")
	oldLeadership.tick()
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(oldLeadership))
	oldToken := oldLeadership.executionFenceToken()
	runCtx, runCancel := context.WithCancel(context.Background())
	client := &startupLifecycleFakeClient{accept: true}
	test := &ManagedTest{
		ID:                    "leadership-loss-runtime",
		Name:                  "Leadership loss runtime",
		Status:                "RUNNING",
		ProjectID:             storage.DefaultProjectID,
		CreatedAt:             time.Now().UTC(),
		executionCtx:          runCtx,
		cancel:                runCancel,
		executionFence:        oldToken,
		remoteClient:          client,
		remoteClientsByTarget: map[string]generatorClient{"generator-a": client},
		jointHeldFromStart:    true,
		// Joint batches held for the next point (B5).
		pendingJointPublications: []*storage.NativePublicationEvidence{{SourceID: "generator-a", Sequence: 4}},
		pendingJointBytes:        512,
	}
	if err := runner.saveManagedTestRecord(test); err != nil {
		t.Fatalf("seed active test: %v", err)
	}
	// A finished run whose archive publication awaits its retry still holds
	// joint batches; this controller will not commit them after losing the
	// lease (B5).
	finished := &ManagedTest{ID: "leadership-loss-finished", Status: "COMPLETE", ProjectID: storage.DefaultProjectID,
		pendingJointPublications: []*storage.NativePublicationEvidence{{SourceID: "generator-a", Sequence: 9}}, pendingJointBytes: 256}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.tests[finished.ID] = finished
	runner.mu.Unlock()

	// The durable lease is already owned by a successor. A normal StopTest would
	// fail its STOPPING save under the old token; lease-loss teardown must still
	// cancel and stop the runtime without touching the durable lifecycle row.
	if err := store.ReleaseSchedulerLease(executionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(store, "new-controller")
	newLeadership.tick()
	defer newLeadership.relinquish()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.teardownActiveTestsAfterLeadershipLoss(ctx); err != nil {
		t.Fatalf("teardownActiveTestsAfterLeadershipLoss: %v", err)
	}
	select {
	case <-runCtx.Done():
	default:
		t.Fatal("leadership-loss teardown did not cancel the local run context")
	}
	client.mu.Lock()
	stopped, closed := client.stopped, client.closed
	client.mu.Unlock()
	if stopped != 1 || closed != 1 {
		t.Fatalf("remote cleanup calls = stop:%d close:%d, want 1/1", stopped, closed)
	}
	if test.cancel != nil || test.executionCtx != nil || test.remoteClient != nil || test.remoteClientsByTarget != nil {
		t.Fatalf("stale runtime handles remained attached: %#v", test)
	}
	// The successor holds the run's batches from now on (B5).
	if test.jointHeldFromStart {
		t.Fatal("a run torn down after leadership loss is still held from its start")
	}
	if test.pendingJointPublications != nil || test.pendingJointBytes != 0 || test.jointPublicationsDropped != 1 {
		t.Fatalf("held joint batches after leadership loss: %d held, %d bytes, %d dropped", len(test.pendingJointPublications), test.pendingJointBytes, test.jointPublicationsDropped)
	}
	if finished.pendingJointPublications != nil || finished.jointPublicationsDropped != 1 {
		t.Fatalf("a finished run kept its held joint batches after leadership loss: %d held, %d dropped", len(finished.pendingJointPublications), finished.jointPublicationsDropped)
	}
	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if durable.Status != "RUNNING" || durable.EndTime != nil {
		t.Fatalf("lease-loss teardown rewrote successor-owned lifecycle state: %#v", durable)
	}
}

func TestReloadStoredTestsReplacesCanceledPriorEpochRuntime(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	leadership := newExecutionLeadership(store, "same-controller")
	leadership.tick()
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(leadership))
	oldToken := leadership.executionFenceToken()
	runCtx, runCancel := context.WithCancel(context.Background())
	stale := &ManagedTest{
		ID:             "canceled-prior-epoch",
		Name:           "Canceled prior epoch",
		Status:         "RUNNING",
		ProjectID:      storage.DefaultProjectID,
		CreatedAt:      time.Now().UTC(),
		executionCtx:   runCtx,
		cancel:         runCancel,
		executionFence: oldToken,
	}
	if err := runner.saveManagedTestRecord(stale); err != nil {
		t.Fatalf("seed active test: %v", err)
	}
	runner.mu.Lock()
	runner.tests[stale.ID] = stale
	runner.mu.Unlock()
	runCancel()
	leadership.relinquish()
	leadership.tick()
	defer leadership.relinquish()
	if next := leadership.executionFenceToken(); next == "" || next == oldToken {
		t.Fatalf("reacquired execution token = %q, old %q", next, oldToken)
	}
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reloadStoredTests: %v", err)
	}
	runner.mu.RLock()
	reloaded := runner.tests[stale.ID]
	runner.mu.RUnlock()
	if reloaded == stale {
		t.Fatal("reload treated a canceled prior-epoch handle as live")
	}
	if reloaded == nil || reloaded.Status != "FAILED" {
		t.Fatalf("reconciled test = %#v, want FAILED replacement", reloaded)
	}
}
