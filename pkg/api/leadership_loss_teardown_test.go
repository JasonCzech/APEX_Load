package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

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
	}
	if err := runner.saveManagedTestRecord(test); err != nil {
		t.Fatalf("seed active test: %v", err)
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
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
