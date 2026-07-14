package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type commitThenErrorExecutionPlanRunStore struct {
	base      *storage.SQLiteStore
	mu        sync.Mutex
	casCalls  int
	failOnCAS int
}

type errorBeforeCommitExecutionPlanRunStore struct {
	base      *storage.SQLiteStore
	mu        sync.Mutex
	casCalls  int
	failOnCAS int
}

func (s *errorBeforeCommitExecutionPlanRunStore) SaveExecutionPlanRun(run *storage.ExecutionPlanRun) error {
	return s.base.SaveExecutionPlanRun(run)
}

func (s *errorBeforeCommitExecutionPlanRunStore) GetExecutionPlanRun(id string) (*storage.ExecutionPlanRun, error) {
	return s.base.GetExecutionPlanRun(id)
}

func (s *errorBeforeCommitExecutionPlanRunStore) ListExecutionPlanRuns(planID string, limit, offset int) ([]*storage.ExecutionPlanRun, error) {
	return s.base.ListExecutionPlanRuns(planID, limit, offset)
}

func (s *errorBeforeCommitExecutionPlanRunStore) CreateExecutionPlanRunIfAbsent(run *storage.ExecutionPlanRun) (bool, error) {
	return s.base.CreateExecutionPlanRunIfAbsent(run)
}

func (s *errorBeforeCommitExecutionPlanRunStore) CompareAndSwapExecutionPlanRun(run *storage.ExecutionPlanRun, expectedRevision int64) (bool, error) {
	s.mu.Lock()
	s.casCalls++
	call := s.casCalls
	s.mu.Unlock()
	if call == s.failOnCAS {
		return false, errors.New("simulated ambiguous CAS response before local commit")
	}
	return s.base.CompareAndSwapExecutionPlanRun(run, expectedRevision)
}

func (s *commitThenErrorExecutionPlanRunStore) SaveExecutionPlanRun(run *storage.ExecutionPlanRun) error {
	return s.base.SaveExecutionPlanRun(run)
}

func (s *commitThenErrorExecutionPlanRunStore) GetExecutionPlanRun(id string) (*storage.ExecutionPlanRun, error) {
	return s.base.GetExecutionPlanRun(id)
}

func (s *commitThenErrorExecutionPlanRunStore) ListExecutionPlanRuns(planID string, limit, offset int) ([]*storage.ExecutionPlanRun, error) {
	return s.base.ListExecutionPlanRuns(planID, limit, offset)
}

func (s *commitThenErrorExecutionPlanRunStore) CreateExecutionPlanRunIfAbsent(run *storage.ExecutionPlanRun) (bool, error) {
	return s.base.CreateExecutionPlanRunIfAbsent(run)
}

func (s *commitThenErrorExecutionPlanRunStore) CompareAndSwapExecutionPlanRun(run *storage.ExecutionPlanRun, expectedRevision int64) (bool, error) {
	updated, err := s.base.CompareAndSwapExecutionPlanRun(run, expectedRevision)
	s.mu.Lock()
	s.casCalls++
	call := s.casCalls
	s.mu.Unlock()
	if err == nil && updated && call == s.failOnCAS {
		return false, errors.New("simulated lost commit response")
	}
	return updated, err
}

func TestBuildPlanEntryCreateRequest(t *testing.T) {
	entryTS := "slot-entry"
	planTS := "slot-plan"

	// Entry-level timeslot wins; data pools + runtime overrides pass through.
	req, err := buildPlanEntryCreateRequest("Nightly", "proj-a", &planTS, storage.ExecutionPlanEntry{
		Order:            0,
		ScenarioID:       "scn-1",
		ScenarioName:     "Checkout",
		TimeslotID:       &entryTS,
		RuntimeOverrides: json.RawMessage(`{"think_time_multiplier":2}`),
		DataPools: []storage.ScenarioDataPoolBinding{
			{ID: "b1", PoolID: "pool-9", Alias: "users"},
		},
	})
	if err != nil {
		t.Fatalf("buildPlanEntryCreateRequest: %v", err)
	}
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
	req, err = buildPlanEntryCreateRequest("Nightly", "proj-a", &planTS, storage.ExecutionPlanEntry{ScenarioID: "scn-2"})
	if err != nil {
		t.Fatalf("buildPlanEntryCreateRequest fallback: %v", err)
	}
	if req.TimeslotID == nil || *req.TimeslotID != "slot-plan" {
		t.Fatalf("plan timeslot fallback failed: %+v", req.TimeslotID)
	}

	// No timeslot anywhere → nil.
	req, err = buildPlanEntryCreateRequest("Nightly", "proj-a", nil, storage.ExecutionPlanEntry{ScenarioID: "scn-3"})
	if err != nil {
		t.Fatalf("buildPlanEntryCreateRequest no timeslot: %v", err)
	}
	if req.TimeslotID != nil {
		t.Fatalf("expected nil timeslot, got %+v", req.TimeslotID)
	}

	if _, err := buildPlanEntryCreateRequest("Nightly", "proj-a", nil, storage.ExecutionPlanEntry{ScenarioID: "scn-bad", RuntimeOverrides: json.RawMessage(`{"iterations":`)}); err == nil {
		t.Fatal("malformed runtime overrides must fail")
	}
}

func TestSequentialPlanReconcilesLostCASResponseWithoutDeletingCommittedTest(t *testing.T) {
	runner, backend, scenarioIDs := newPlanTestRunner(t, "lost-response")
	base, ok := runner.execPlanRunStore.(*storage.SQLiteStore)
	if !ok {
		t.Fatalf("plan store type = %T, want *storage.SQLiteStore", runner.execPlanRunStore)
	}
	// CAS 1 claims the entry; CAS 2 attaches its queued test. Simulate the
	// database committing CAS 2 while its success response is lost.
	runner.execPlanRunStore = &commitThenErrorExecutionPlanRunStore{base: base, failOnCAS: 2}
	plan := &storage.ExecutionPlan{
		ID: "plan-lost-sequential", ProjectID: "project-a", Name: "Lost response", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenarioIDs[0]}},
	}
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	startedID, started := waitForStart(t, backend, time.Second)
	if !started {
		t.Fatal("committed entry was not launched after CAS reconciliation")
	}
	if len(run.Entries) != 1 || run.Entries[0].TestID != startedID {
		t.Fatalf("run entries = %+v, started test = %s", run.Entries, startedID)
	}
	if _, ok := runner.GetTest(startedID); !ok {
		t.Fatalf("committed test %s was deleted after a lost response", startedID)
	}
}

func TestParallelPlanReconcilesLostFinalCASResponse(t *testing.T) {
	runner, _, scenarioIDs := newPlanTestRunner(t, "parallel-lost-response")
	base, ok := runner.execPlanRunStore.(*storage.SQLiteStore)
	if !ok {
		t.Fatalf("plan store type = %T, want *storage.SQLiteStore", runner.execPlanRunStore)
	}
	runner.execPlanRunStore = &commitThenErrorExecutionPlanRunStore{base: base, failOnCAS: 1}
	plan := &storage.ExecutionPlan{
		ID: "plan-lost-parallel", ProjectID: "project-a", Name: "Lost response", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenarioIDs[0]}},
	}
	result, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil {
		t.Fatalf("MaterializeAndRunExecutionPlan: %v", err)
	}
	if result.Created != 1 || len(result.Results) != 1 || result.Results[0].TestID == "" {
		t.Fatalf("materialization result = %+v", result)
	}
	if _, ok := runner.GetTest(result.Results[0].TestID); !ok {
		t.Fatalf("committed test %s was deleted after a lost response", result.Results[0].TestID)
	}
}

func TestSequentialPlanRetriesAmbiguousEntryClaimBeforeMaterialization(t *testing.T) {
	runner, backend, scenarioIDs := newPlanTestRunner(t, "ambiguous-claim")
	base := runner.execPlanRunStore.(*storage.SQLiteStore)
	runner.execPlanRunStore = &errorBeforeCommitExecutionPlanRunStore{base: base, failOnCAS: 1}
	plan := &storage.ExecutionPlan{
		ID: "plan-ambiguous-claim", ProjectID: "project-a", Name: "Ambiguous claim", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenarioIDs[0]}},
	}

	if _, err := runner.StartExecutionPlanRun(plan, "", ""); !errors.Is(err, errExecutionPlanRunCommitUnknown) {
		t.Fatalf("StartExecutionPlanRun error = %v, want commit-unknown", err)
	}
	startedID, started := waitForStart(t, backend, 3*time.Second)
	if !started {
		t.Fatal("ambiguous claim retry did not eventually materialize the entry")
	}
	runs, err := base.ListExecutionPlanRuns(plan.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListExecutionPlanRuns: %v", err)
	}
	var fresh *storage.ExecutionPlanRun
	if len(runs) == 1 {
		fresh = runs[0]
	}
	if fresh == nil || len(fresh.Entries) != 1 || fresh.Entries[0].TestID != startedID {
		t.Fatalf("durable run = %+v, want started test %s", fresh, startedID)
	}
}

func TestParallelPlanRetainsTestsWhileAmbiguousFinalCASRetries(t *testing.T) {
	runner, _, scenarioIDs := newPlanTestRunner(t, "ambiguous-parallel")
	base := runner.execPlanRunStore.(*storage.SQLiteStore)
	runner.execPlanRunStore = &errorBeforeCommitExecutionPlanRunStore{base: base, failOnCAS: 1}
	plan := &storage.ExecutionPlan{
		ID: "plan-ambiguous-parallel", ProjectID: "project-a", Name: "Ambiguous parallel", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenarioIDs[0]}},
	}

	if _, err := runner.MaterializeAndRunExecutionPlan(plan, "", ""); !errors.Is(err, errExecutionPlanRunCommitUnknown) {
		t.Fatalf("MaterializeAndRunExecutionPlan error = %v, want commit-unknown", err)
	}
	tests := runner.ListTests()
	if len(tests) != 1 {
		t.Fatalf("staged tests = %d, want retained test while commit is unknown", len(tests))
	}
	testID := tests[0].ID
	deadline := time.Now().Add(3 * time.Second)
	for {
		runs, err := base.ListExecutionPlanRuns(plan.ID, 0, 0)
		if err != nil {
			t.Fatalf("ListExecutionPlanRuns: %v", err)
		}
		var fresh *storage.ExecutionPlanRun
		if len(runs) == 1 {
			fresh = runs[0]
		}
		if fresh != nil && fresh.Status == "completed" && len(fresh.Entries) == 1 && fresh.Entries[0].TestID == testID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ambiguous final CAS was not reconciled; run=%+v", fresh)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := runner.GetTest(testID); !ok {
		t.Fatalf("staged test %s was deleted during ambiguous commit reconciliation", testID)
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

	// The plan fixtures below intentionally exercise two distinct project IDs.
	// Seed both ownership rows so the tests reach orchestration behavior instead
	// of failing test persistence at the projects foreign-key boundary.
	now := time.Now().UTC()
	for _, projectID := range []string{"proj-a", "project-a"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}

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

// syncFailBackend fails the test at the very top of Run — synchronously, before
// any await — the way the distributed backend does on "no generator targets
// configured". This exercises the orchestrator's registration race: the terminal
// hook can fire before launchNextPlanEntry registers the test→run mapping.
type syncFailBackend struct{ msg string }

func (b syncFailBackend) Run(tr *TestRunner, test *ManagedTest) {
	tr.failTestBeforeRun(test, b.msg)
}

func TestSequentialPlanRunHaltsWhenEntryFailsSynchronously(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "Project A", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	runner := NewTestRunner(10, NewHub(), store)
	runner.setExecutionBackend(syncFailBackend{msg: "no generator targets configured"})

	scriptID := "script-fail"
	runner.StoreScript(&dsl.Script{
		ID: scriptID, Name: "X", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})
	scn, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "X", Duration: "30s",
		Groups: []CreateGroupEntry{{Name: "G", ScriptID: scriptID, VUsers: 1, RampUp: "1s"}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	plan := &storage.ExecutionPlan{
		ID: "exec-plan-fail", ProjectID: "proj-a", Name: "Fails", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{
			{Order: 0, ScenarioID: scn.ID},
			{Order: 1, ScenarioID: scn.ID},
		},
	}
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}

	// The run must reach a terminal state, not stall in "running". With the race,
	// the terminal hook missed the seqPlanByTest mapping and the run never finished.
	deadline := time.Now().Add(3 * time.Second)
	for {
		runner.seqMu.Lock()
		_, stillTracked := runner.seqPlanRuns[run.ID]
		runner.seqMu.Unlock()
		if !stillTracked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sequential plan run stalled in 'running' after a synchronous entry failure (registration race)")
		}
		time.Sleep(5 * time.Millisecond)
	}

	persisted, err := store.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if persisted.Status != "halted" {
		t.Fatalf("plan run status = %q, want halted", persisted.Status)
	}
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
