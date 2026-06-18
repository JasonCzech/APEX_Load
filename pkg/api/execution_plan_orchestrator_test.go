package api

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBuildPlanEntryCreateRequest(t *testing.T) {
	entryTS := "slot-entry"
	planTS := "slot-plan"

	// Entry-level timeslot wins; data pools + runtime overrides pass through.
	req := buildPlanEntryCreateRequest("Nightly", "proj-a", &planTS, storage.ExecutionPlanEntry{
		Order:            0,
		ScenarioID:       "scn-1",
		ScenarioName:     "Checkout",
		TimeslotID:       &entryTS,
		RuntimeOverrides: json.RawMessage(`{"think_time_multiplier":2}`),
		DataPools: []storage.ScenarioDataPoolBinding{
			{ID: "b1", PoolID: "pool-9", Alias: "users"},
		},
	})
	if req.ScenarioID != "scn-1" || req.ProjectID != "proj-a" {
		t.Fatalf("scenario/project not mapped: %+v", req)
	}
	if req.Name != "Nightly / Checkout" {
		t.Fatalf("name = %q, want %q", req.Name, "Nightly / Checkout")
	}
	if req.TimeslotID == nil || *req.TimeslotID != "slot-entry" {
		t.Fatalf("entry timeslot should win: %+v", req.TimeslotID)
	}
	if len(req.DataPools) != 1 || req.DataPools[0].PoolID != "pool-9" {
		t.Fatalf("data pools not mapped: %+v", req.DataPools)
	}
	if req.RuntimeSettings == nil {
		t.Fatalf("runtime overrides not parsed")
	}

	// Falls back to the plan-level timeslot when the entry has none.
	req = buildPlanEntryCreateRequest("Nightly", "proj-a", &planTS, storage.ExecutionPlanEntry{ScenarioID: "scn-2"})
	if req.TimeslotID == nil || *req.TimeslotID != "slot-plan" {
		t.Fatalf("plan timeslot fallback failed: %+v", req.TimeslotID)
	}

	// No timeslot anywhere → nil.
	req = buildPlanEntryCreateRequest("Nightly", "proj-a", nil, storage.ExecutionPlanEntry{ScenarioID: "scn-3"})
	if req.TimeslotID != nil {
		t.Fatalf("expected nil timeslot, got %+v", req.TimeslotID)
	}
}

// newPlanTestRunner builds a runner backed by a real SQLite store (so plan-run
// progress is queryable) with a controllable execution backend that never
// auto-completes, plus one scenario per requested label. Returns the runner, the
// backend spy, and the created scenario IDs.
func newPlanTestRunner(t *testing.T, scenarioLabels ...string) (*TestRunner, *executionBackendSpy, []string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	runner := NewTestRunner(10, NewHub(), store)
	spy := &executionBackendSpy{called: make(chan string, 8)}
	runner.setExecutionBackend(spy)

	ids := make([]string, 0, len(scenarioLabels))
	for _, label := range scenarioLabels {
		scriptID := "script-" + label
		runner.StoreScript(&dsl.Script{
			ID: scriptID, Name: label, Protocol: "http", Version: "1.0.0",
			Config:  dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
		})
		scn, err := runner.StoreScenario(CreateScenarioRequest{
			Name: label, Duration: "30s",
			Groups: []CreateGroupEntry{{Name: "G", ScriptID: scriptID, VUsers: 1, RampUp: "1s"}},
		})
		if err != nil {
			t.Fatalf("StoreScenario(%s): %v", label, err)
		}
		ids = append(ids, scn.ID)
	}
	return runner, spy, ids
}

func waitForStart(t *testing.T, spy *executionBackendSpy, d time.Duration) (string, bool) {
	t.Helper()
	select {
	case id := <-spy.called:
		return id, true
	case <-time.After(d):
		return "", false
	}
}

func completeManagedTest(t *testing.T, runner *TestRunner, id, status string) {
	t.Helper()
	runner.mu.RLock()
	test := runner.tests[id]
	runner.mu.RUnlock()
	if test == nil {
		t.Fatalf("managed test %q not found", id)
	}
	runner.completeTestRun(test, status, time.Now().UTC(), "", nil)
}

func TestSequentialPlanRunGatesOnCompletion(t *testing.T) {
	runner, spy, ids := newPlanTestRunner(t, "first", "second")
	plan := &storage.ExecutionPlan{
		ID: "exec-plan-seq", ProjectID: "proj-a", Name: "Gated", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{
			{Order: 0, ScenarioID: ids[0]},
			{Order: 1, ScenarioID: ids[1]},
		},
	}
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	runID := run.ID

	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 0 did not start")
	}
	// Entry 1 must NOT start until entry 0 completes.
	if id, started := waitForStart(t, spy, 250*time.Millisecond); started {
		t.Fatalf("entry 1 (%s) started before entry 0 completed", id)
	}

	completeManagedTest(t, runner, first, "COMPLETE")

	second, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start after entry 0 completed")
	}
	if second == first {
		t.Fatal("entry 1 reused entry 0's test")
	}
	completeManagedTest(t, runner, second, "COMPLETE")

	got, err := runner.execPlanRunStore.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("run status = %q, want completed", got.Status)
	}
	if got.Entries[0].Status != "complete" || got.Entries[1].Status != "complete" {
		t.Fatalf("entry statuses = %q/%q, want complete/complete", got.Entries[0].Status, got.Entries[1].Status)
	}
	if got.Entries[0].TestID != first || got.Entries[1].TestID != second {
		t.Fatalf("entry test ids not recorded: %+v", got.Entries)
	}
	if got.CompletedAt == nil {
		t.Fatal("completed run should have CompletedAt")
	}
}

func TestSequentialPlanRunHaltsOnFailure(t *testing.T) {
	runner, spy, ids := newPlanTestRunner(t, "first", "second")
	plan := &storage.ExecutionPlan{
		ID: "exec-plan-halt", ProjectID: "proj-a", Name: "Halts", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{
			{Order: 0, ScenarioID: ids[0]}, // default failure policy = halt
			{Order: 1, ScenarioID: ids[1]},
		},
	}
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}

	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 0 did not start")
	}
	completeManagedTest(t, runner, first, "FAILED")

	// Entry 1 must never start (halted).
	if id, started := waitForStart(t, spy, 300*time.Millisecond); started {
		t.Fatalf("entry 1 (%s) started despite halt-on-failure", id)
	}

	got, err := runner.execPlanRunStore.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if got.Status != "halted" {
		t.Fatalf("run status = %q, want halted", got.Status)
	}
	if got.Entries[0].Status != "failed" {
		t.Fatalf("entry 0 status = %q, want failed", got.Entries[0].Status)
	}
	if got.Entries[1].Status != "skipped" {
		t.Fatalf("entry 1 status = %q, want skipped", got.Entries[1].Status)
	}
}

func TestSequentialPlanRunSkipsFailedEntry(t *testing.T) {
	runner, spy, ids := newPlanTestRunner(t, "first", "second")
	plan := &storage.ExecutionPlan{
		ID: "exec-plan-skip", ProjectID: "proj-a", Name: "Skips", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{
			{Order: 0, ScenarioID: ids[0], FailurePolicy: "skip"},
			{Order: 1, ScenarioID: ids[1]},
		},
	}
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}

	first, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 0 did not start")
	}
	completeManagedTest(t, runner, first, "FAILED")

	// skip policy → entry 1 still runs.
	second, ok := waitForStart(t, spy, 2*time.Second)
	if !ok {
		t.Fatal("entry 1 did not start after a skip-policy failure")
	}
	completeManagedTest(t, runner, second, "COMPLETE")

	got, err := runner.execPlanRunStore.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("run status = %q, want completed", got.Status)
	}
	if got.Entries[0].Status != "failed" || got.Entries[1].Status != "complete" {
		t.Fatalf("entry statuses = %q/%q, want failed/complete", got.Entries[0].Status, got.Entries[1].Status)
	}
}

func TestAdvanceExecutionPlanIgnoresNonPlanTest(t *testing.T) {
	runner, _, _ := newPlanTestRunner(t)
	// Should be a no-op and not panic for a test that is not part of any plan run.
	runner.advanceExecutionPlanForTest(&ManagedTest{ID: "orphan-test"}, "COMPLETE")
}
