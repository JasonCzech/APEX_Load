package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type flakySequentialRecoveryStore struct {
	mu        sync.Mutex
	run       *storage.ExecutionPlanRun
	saveCalls int
	failUntil int
	err       error
}

func TestSequentialRecoveryKeepsMappingsWhileAdmissionIsClosed(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	plan := &storage.ExecutionPlan{ID: "plan-acquire", ProjectID: storage.DefaultProjectID, Name: "Acquire", RunMode: "sequential", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	run := &storage.ExecutionPlanRun{
		ID: "run-acquire", PlanID: plan.ID, ProjectID: storage.DefaultProjectID, RunMode: "sequential", Status: "running", Revision: 1, CreatedAt: now, UpdatedAt: now,
		Entries: []storage.ExecutionPlanRunEntry{{Order: 0, ScenarioID: "scenario", TestID: "test-acquire", Status: "running", RunName: "Acquire"}},
	}
	if err := store.SaveExecutionPlanRun(run); err != nil {
		t.Fatal(err)
	}
	leadership := newExecutionLeadership(&fakeLeaseStore{dfltOK: true}, "owner")
	leadership.leader.Store(true)
	leadership.reconciling.Store(true)
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(leadership))
	runner.tests["test-acquire"] = &ManagedTest{ID: "test-acquire", Status: "PENDING"}

	if err := runner.recoverSequentialExecutionPlanRuns(); err != nil {
		t.Fatalf("acquire-time recovery: %v", err)
	}
	runner.seqMu.Lock()
	mapped := runner.seqPlanByTest["test-acquire"]
	state := runner.seqPlanRuns[run.ID]
	runner.seqMu.Unlock()
	if mapped != run.ID || state == nil || state.recovering {
		t.Fatalf("mapping/state during closed admission = %q, %+v", mapped, state)
	}
}

func (s *flakySequentialRecoveryStore) SaveExecutionPlanRun(run *storage.ExecutionPlanRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.saveCalls <= s.failUntil {
		return s.err
	}
	s.run = cloneExecutionPlanRunForTest(run)
	return nil
}

func (s *flakySequentialRecoveryStore) GetExecutionPlanRun(id string) (*storage.ExecutionPlanRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil || s.run.ID != id {
		return nil, nil
	}
	return cloneExecutionPlanRunForTest(s.run), nil
}

func (s *flakySequentialRecoveryStore) ListExecutionPlanRuns(planID string, _, _ int) ([]*storage.ExecutionPlanRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil || (planID != "" && s.run.PlanID != planID) {
		return nil, nil
	}
	return []*storage.ExecutionPlanRun{cloneExecutionPlanRunForTest(s.run)}, nil
}

func newSequentialRecoveryFixture(t *testing.T, failurePolicy string) (*storage.SQLiteStore, *TestRunner, *executionBackendSpy, *storage.ExecutionPlan, string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(10, NewHub(), store)
	backend := &executionBackendSpy{called: make(chan string, 8)}
	runner.setExecutionBackend(backend)
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "proj-a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject(proj-a): %v", err)
	}

	scenarioIDs := make([]string, 0, 2)
	for i, label := range []string{"recover-first", "recover-second"} {
		scriptID := "script-" + label
		if _, err := runner.StoreScriptDurable(&dsl.Script{
			ID: scriptID, Name: label, Protocol: "http", Version: "1.0.0", ProjectID: "proj-a",
			Config:  dsl.ScriptConfig{BaseURL: "https://example.test", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
		}); err != nil {
			t.Fatalf("StoreScript(%d): %v", i, err)
		}
		scenario, err := runner.StoreScenario(CreateScenarioRequest{
			Name: label, Duration: "30s",
			Groups: []CreateGroupEntry{{Name: "G", ScriptID: scriptID, VUsers: 1, RampUp: "1s"}},
		})
		if err != nil {
			t.Fatalf("StoreScenario(%d): %v", i, err)
		}
		scenarioIDs = append(scenarioIDs, scenario.ID)
	}
	plan := &storage.ExecutionPlan{
		ID: "exec-plan-recovery", ProjectID: "proj-a", Name: "Original Recovery Name", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{
			{Order: 0, ScenarioID: scenarioIDs[0], ScenarioName: "First", FailurePolicy: failurePolicy, RuntimeOverrides: json.RawMessage(`{"think_time_multiplier":2}`)},
			{Order: 1, ScenarioID: scenarioIDs[1], ScenarioName: "Second"},
		},
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	_, err = runner.startExecutionPlanRunWithID(plan, "actor-1", "Recovery Operator", "Scheduled Snapshot Name", "exec-plan-run-recovery", nil)
	if err != nil {
		t.Fatalf("startExecutionPlanRunWithID: %v", err)
	}
	firstID, ok := waitForStart(t, backend, 2*time.Second)
	if !ok {
		t.Fatal("first entry did not start")
	}
	return store, runner, backend, plan, firstID
}

func acquireRecoveredSequentialRunner(t *testing.T, store storage.Store) (*TestRunner, *executionBackendSpy) {
	t.Helper()
	leadership := &executionLeadership{ttl: time.Hour, now: time.Now}
	runner := NewTestRunner(10, NewHub(), store, WithExecutionLeadership(leadership))
	backend := &executionBackendSpy{called: make(chan string, 8)}
	runner.setExecutionBackend(backend)
	leadership.mu.Lock()
	leadership.lastConfirmed = time.Now().UTC()
	leadership.leader.Store(true)
	leadership.mu.Unlock()
	runner.reloadAndReconcile()
	return runner, backend
}

func TestSequentialPlanRecoveryAdvancesPersistedTerminalTestExactlyOnce(t *testing.T) {
	store, _, _, _, firstID := newSequentialRecoveryFixture(t, "halt")
	endedAt := time.Now().UTC()
	if err := store.UpdateTestStatus(firstID, "COMPLETE", &endedAt, ""); err != nil {
		t.Fatalf("persist first completion: %v", err)
	}

	recovered, backend := acquireRecoveredSequentialRunner(t, store)
	secondID, ok := waitForStart(t, backend, 2*time.Second)
	if !ok {
		t.Fatal("recovery did not advance the persisted complete entry")
	}
	if secondID == firstID {
		t.Fatalf("recovery restarted completed test %s", firstID)
	}

	// Reconciliation is intentionally repeatable (startup plus leadership
	// callbacks can be coalesced). It must not materialize another entry.
	recovered.ReconcilePendingTests()
	recovered.ReconcilePendingTests()
	if duplicate, started := waitForStart(t, backend, 150*time.Millisecond); started {
		t.Fatalf("repeat recovery launched duplicate test %s", duplicate)
	}
	tests, err := store.ListTests("", nil)
	if err != nil {
		t.Fatalf("ListTests: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("persisted tests = %d, want exactly two", len(tests))
	}

	completeManagedTest(t, recovered, secondID, "COMPLETE")
	run, err := store.GetExecutionPlanRun("exec-plan-run-recovery")
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if run.Status != "completed" || run.Entries[0].Status != "complete" || run.Entries[1].Status != "complete" {
		t.Fatalf("recovered run = %+v, want completed entries", run)
	}
}

func TestSequentialPlanRecoveryMaterializesClaimedEntryWithoutTest(t *testing.T) {
	store, original, _, _, firstID := newSequentialRecoveryFixture(t, "halt")
	original.beginShutdown()

	run, err := store.GetExecutionPlanRun("exec-plan-run-recovery")
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	// Model a controller that committed the side-effect-free entry claim but
	// stopped before persisting/creating its queued test.
	run.Entries[0].Status = "running"
	run.Entries[0].TestID = ""
	if err := store.SaveExecutionPlanRun(run); err != nil {
		t.Fatalf("persist claimed entry without test: %v", err)
	}
	if err := store.DeleteTest(firstID); err != nil {
		t.Fatalf("remove test created by fixture: %v", err)
	}

	_, backend := acquireRecoveredSequentialRunner(t, store)
	recoveredID, started := waitForStart(t, backend, 2*time.Second)
	if !started {
		t.Fatal("recovery did not materialize the durably claimed entry")
	}
	// Test IDs are deterministic side-effect keys derived from the run and entry.
	// Reusing the deleted ID is required so repeated recovery cannot create a
	// second logical test for the same durably claimed entry.
	if recoveredID != firstID {
		t.Fatalf("recovery test id = %s, want deterministic id %s", recoveredID, firstID)
	}
	fresh, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("reload recovered run: %v", err)
	}
	if fresh.Entries[0].Status != "running" || fresh.Entries[0].TestID != recoveredID {
		t.Fatalf("recovered entry = %+v, want running test %s", fresh.Entries[0], recoveredID)
	}
}

func TestSequentialPlanRecoveryUsesPersistedSkipPolicyAndMetadata(t *testing.T) {
	store, _, _, plan, firstID := newSequentialRecoveryFixture(t, "skip")

	// Mutating and then deleting the source plan proves recovery uses the
	// materialized policy/config snapshot, not mutable plan state.
	plan.Name = "Changed Name"
	plan.Entries[0].FailurePolicy = "halt"
	plan.Entries[0].RuntimeOverrides = nil
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("mutate plan: %v", err)
	}
	if err := store.DeleteExecutionPlan(plan.ID); err != nil {
		t.Fatalf("delete plan: %v", err)
	}

	recovered, backend := acquireRecoveredSequentialRunner(t, store)
	secondID, ok := waitForStart(t, backend, 2*time.Second)
	if !ok {
		run, _ := store.GetExecutionPlanRun("exec-plan-run-recovery")
		t.Fatalf("skip-policy recovery did not launch entry two; run=%+v", run)
	}
	if secondID == firstID {
		t.Fatalf("recovery restarted interrupted test %s", firstID)
	}

	run, err := store.GetExecutionPlanRun("exec-plan-run-recovery")
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if run.Entries[0].Status != "failed" || run.Entries[0].FailurePolicy != "skip" {
		t.Fatalf("entry zero = %+v, want failed with snapshotted skip policy", run.Entries[0])
	}
	if run.Entries[0].RunName != "Scheduled Snapshot Name" || len(run.Entries[0].RuntimeOverrides) == 0 {
		t.Fatalf("entry snapshot was not preserved: %+v", run.Entries[0])
	}
	recovered.mu.RLock()
	second := recovered.tests[secondID]
	recovered.mu.RUnlock()
	if second == nil || second.Name != "Scheduled Snapshot Name / Second" {
		t.Fatalf("recovered test name = %#v, want snapshotted scheduled name", second)
	}
	if second.CreatedBySubject != "actor-1" || second.CreatedByDisplayName != "Recovery Operator" {
		t.Fatalf("recovered attribution = %q/%q", second.CreatedBySubject, second.CreatedByDisplayName)
	}
}

func TestSequentialPlanRecoveryHaltsInterruptedEntryByDefault(t *testing.T) {
	store, _, _, _, _ := newSequentialRecoveryFixture(t, "")
	_, backend := acquireRecoveredSequentialRunner(t, store)
	if next, started := waitForStart(t, backend, 200*time.Millisecond); started {
		t.Fatalf("halt-policy recovery unexpectedly launched %s", next)
	}
	run, err := store.GetExecutionPlanRun("exec-plan-run-recovery")
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if run.Status != "halted" || run.Entries[0].Status != "failed" || run.Entries[1].Status != "skipped" {
		t.Fatalf("halt recovery run = %+v", run)
	}
	if !strings.Contains(run.Entries[0].Detail, "controller restarted") {
		t.Fatalf("recovered failure detail = %q", run.Entries[0].Detail)
	}
}

func TestSequentialPlanPreassignedRunIDIsIdempotent(t *testing.T) {
	store, runner, backend, plan, firstID := newSequentialRecoveryFixture(t, "halt")
	again, err := runner.startExecutionPlanRunWithID(plan, "actor-2", "Other", "Other Name", "exec-plan-run-recovery", nil)
	if err != nil {
		t.Fatalf("idempotent start: %v", err)
	}
	if again.ID != "exec-plan-run-recovery" || again.Entries[0].TestID != firstID {
		t.Fatalf("idempotent result = %+v", again)
	}
	if duplicate, started := waitForStart(t, backend, 150*time.Millisecond); started {
		t.Fatalf("idempotent replay started duplicate %s", duplicate)
	}
	tests, err := store.ListTests("", nil)
	if err != nil {
		t.Fatalf("ListTests: %v", err)
	}
	if len(tests) != 1 {
		t.Fatalf("persisted tests = %d, want one", len(tests))
	}
}

func TestSequentialPlanRecoveryRetriesAfterProvisionalStatePersistenceFailure(t *testing.T) {
	wantErr := errors.New("temporary plan-run store outage")
	store := &flakySequentialRecoveryStore{
		failUntil: durableTransitionAttempts,
		err:       wantErr,
		run: &storage.ExecutionPlanRun{
			ID: "recover-retry", PlanID: "plan-retry", ProjectID: "proj-a", RunMode: "sequential", Status: "running",
			CreatedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now(),
			Entries: []storage.ExecutionPlanRunEntry{
				{Order: 0, ScenarioID: "missing-scenario", TestID: "missing-test", Status: "running", RunName: "Recovery Retry"},
				{Order: 1, ScenarioID: "never-started", Status: "pending", RunName: "Recovery Retry"},
			},
		},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.execPlanRunStore = store
	t.Cleanup(runner.beginShutdown)

	// The inline recovery exhausts its bounded writes, removes the provisional
	// in-memory state, and schedules a full rehydrate. The next attempt succeeds
	// and applies the default fail-closed halt policy.
	runner.ReconcilePendingTests()
	deadline := time.Now().Add(3 * time.Second)
	for {
		store.mu.Lock()
		run := cloneExecutionPlanRunForTest(store.run)
		calls := store.saveCalls
		store.mu.Unlock()
		runner.seqMu.Lock()
		_, runTracked := runner.seqPlanRuns["recover-retry"]
		_, testMapped := runner.seqPlanByTest["missing-test"]
		runner.seqMu.Unlock()
		if run != nil && run.Status == "halted" {
			if calls != durableTransitionAttempts+1 {
				t.Fatalf("SaveExecutionPlanRun calls = %d, want %d", calls, durableTransitionAttempts+1)
			}
			if run.Entries[0].Status != "failed" || run.Entries[1].Status != "skipped" {
				t.Fatalf("recovered retry run = %+v", run)
			}
			// Persistence becomes visible just before failRecoveredSequentialEntry
			// removes its test-to-run mapping. Wait for the whole recovery action,
			// not only its durable write, before asserting cleanup.
			if !runTracked && !testMapped {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery retry did not fully settle; last=%+v calls=%d run_tracked=%v test_mapped=%v", run, calls, runTracked, testMapped)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSequentialPlanRecoveryQuarantinesMalformedRunWithoutBlockingPendingPromotion(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(2, NewHub(), store)
	t.Cleanup(runner.beginShutdown)
	backend := &executionBackendSpy{called: make(chan string, 2)}
	runner.setExecutionBackend(backend)

	if _, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "unrelated-pending-script", Name: "Unrelated pending", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	}); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Unrelated pending", Duration: "30s", ScriptID: "unrelated-pending-script", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runner.mu.Lock()
	pendingRecord := runner.tests[pending.ID]
	pendingRecord.Status = "PENDING"
	pendingAt := time.Now().UTC().Add(-time.Minute)
	pendingRecord.PendingSince = &pendingAt
	pendingRecord.PendingReason = "awaiting recovery"
	if err := runner.saveManagedTestRecord(pendingRecord); err != nil {
		runner.mu.Unlock()
		t.Fatalf("save pending test: %v", err)
	}
	runner.mu.Unlock()

	now := time.Now().UTC().Add(-time.Minute)
	if err := store.SaveExecutionPlan(&storage.ExecutionPlan{
		ID: "malformed-plan", ProjectID: storage.DefaultProjectID, Name: "Malformed", RunMode: "sequential",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	if err := store.SaveExecutionPlanRun(&storage.ExecutionPlanRun{
		ID: "malformed-running-run", PlanID: "malformed-plan", ProjectID: storage.DefaultProjectID,
		RunMode: "sequential", Status: "running", TransitionToken: "previous-transition", CreatedAt: now, UpdatedAt: now,
		Entries: []storage.ExecutionPlanRunEntry{
			{Order: 0, ScenarioID: "scenario-a", TestID: "test-a", Status: "running", RunName: "Malformed"},
			{Order: 1, ScenarioID: "scenario-b", TestID: "test-b", Status: "running", RunName: "Malformed"},
			{Order: 2, ScenarioID: "scenario-c", Status: "pending", RunName: "Malformed"},
		},
	}); err != nil {
		t.Fatalf("SaveExecutionPlanRun: %v", err)
	}

	runner.ReconcilePendingTests()
	startedID, started := waitForStart(t, backend, 2*time.Second)
	if !started || startedID != pending.ID {
		t.Fatalf("unrelated pending promotion = %q/%v, want %q", startedID, started, pending.ID)
	}
	run, err := store.GetExecutionPlanRun("malformed-running-run")
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if run == nil || run.Status != "halted" || run.CompletedAt == nil {
		t.Fatalf("malformed run = %+v, want durably halted", run)
	}
	if run.TransitionToken == "" || run.TransitionToken == "previous-transition" {
		t.Fatalf("recovery transition token = %q, want a fresh token", run.TransitionToken)
	}
	if run.Entries[0].Status != "failed" || run.Entries[1].Status != "failed" || run.Entries[2].Status != "skipped" {
		t.Fatalf("quarantined entries = %+v", run.Entries)
	}
	if !strings.Contains(run.Entries[0].Detail, "operator review") || !strings.Contains(run.Entries[1].Detail, "multiple running entries") {
		t.Fatalf("operator-review evidence missing: %+v", run.Entries)
	}
	runner.seqMu.Lock()
	_, tracked := runner.seqPlanRuns[run.ID]
	_, mappedA := runner.seqPlanByTest["test-a"]
	_, mappedB := runner.seqPlanByTest["test-b"]
	runner.seqMu.Unlock()
	if tracked || mappedA || mappedB {
		t.Fatalf("quarantined run leaked live recovery state: tracked=%v mapped=%v/%v", tracked, mappedA, mappedB)
	}
}
