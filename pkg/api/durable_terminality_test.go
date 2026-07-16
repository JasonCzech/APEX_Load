package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scriptedTestPersistence struct {
	testPersistence
	mu             sync.Mutex
	saveCalls      int
	updateCalls    int
	failSaveCalls  map[int]bool
	failUpdateCall map[int]bool
	err            error
}

func (s *scriptedTestPersistence) SaveTest(test *storage.TestRecord) error {
	s.mu.Lock()
	s.saveCalls++
	call := s.saveCalls
	fail := s.failSaveCalls[call]
	s.mu.Unlock()
	if fail {
		return s.err
	}
	if s.testPersistence == nil {
		return nil
	}
	return s.testPersistence.SaveTest(test)
}

func (s *scriptedTestPersistence) SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, _ string, _ string, _ string) error {
	return s.SaveTest(test)
}

func (s *scriptedTestPersistence) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, _ string, _ string, _ string, _ string, _ string) (bool, error) {
	err := s.SaveTest(test)
	return err == nil, err
}

func (s *scriptedTestPersistence) UpdateTestStatus(id, status string, endedAt *time.Time, detail string) error {
	s.mu.Lock()
	s.updateCalls++
	call := s.updateCalls
	fail := s.failUpdateCall[call]
	s.mu.Unlock()
	if fail {
		return s.err
	}
	if s.testPersistence == nil {
		return nil
	}
	return s.testPersistence.UpdateTestStatus(id, status, endedAt, detail)
}

type reservationReleaseSpy struct {
	mu          sync.Mutex
	clearCalls  int
	clearedSlot []string
	clearedTest []string
	failCalls   map[int]bool
	err         error
	reservation *storage.CapacityReservation
}

func (s *reservationReleaseSpy) GetCapacityReservation(string) (*storage.CapacityReservation, error) {
	return s.reservation, nil
}
func (s *reservationReleaseSpy) UpdateCapacityReservationBinding(string, *string, *string, time.Time) (bool, error) {
	return true, nil
}
func (s *reservationReleaseSpy) ClearCapacityReservationBinding(slotID, testID string, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCalls++
	s.clearedSlot = append(s.clearedSlot, slotID)
	s.clearedTest = append(s.clearedTest, testID)
	if s.failCalls[s.clearCalls] {
		return false, s.err
	}
	return true, nil
}
func (s *reservationReleaseSpy) ListCapacityReservationsByTimeRange(string, time.Time, time.Time) ([]*storage.CapacityReservation, error) {
	return nil, nil
}

type scriptedExecutionPlanRunStore struct {
	mu        sync.Mutex
	saveCalls int
	failCalls map[int]bool
	err       error
	runs      map[string]*storage.ExecutionPlanRun
}

func (s *scriptedExecutionPlanRunStore) SaveExecutionPlanRun(run *storage.ExecutionPlanRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.failCalls[s.saveCalls] {
		return s.err
	}
	if s.runs == nil {
		s.runs = make(map[string]*storage.ExecutionPlanRun)
	}
	s.runs[run.ID] = cloneExecutionPlanRunForTest(run)
	return nil
}

func (s *scriptedExecutionPlanRunStore) GetExecutionPlanRun(id string) (*storage.ExecutionPlanRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneExecutionPlanRunForTest(s.runs[id]), nil
}

func (s *scriptedExecutionPlanRunStore) ListExecutionPlanRuns(planID string, _, _ int) ([]*storage.ExecutionPlanRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*storage.ExecutionPlanRun, 0)
	for _, run := range s.runs {
		if run.PlanID == planID {
			out = append(out, cloneExecutionPlanRunForTest(run))
		}
	}
	return out, nil
}

func cloneExecutionPlanRunForTest(run *storage.ExecutionPlanRun) *storage.ExecutionPlanRun {
	if run == nil {
		return nil
	}
	clone := *run
	clone.Entries = append([]storage.ExecutionPlanRunEntry(nil), run.Entries...)
	clone.CompletedAt = cloneTimePtr(run.CompletedAt)
	return &clone
}

func failCalls(from, count int) map[int]bool {
	out := make(map[int]bool, count)
	for i := 0; i < count; i++ {
		out[from+i] = true
	}
	return out
}

func hubEventTypes(hub *Hub) []string {
	out := make([]string, 0)
	for {
		select {
		case msg := <-hub.broadcast:
			var envelope struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(msg.payload, &envelope)
			out = append(out, envelope.Type)
		default:
			return out
		}
	}
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func TestCompleteTestRunPersistenceFailureDefersAllTerminalSideEffects(t *testing.T) {
	wantErr := errors.New("database unavailable")
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	test := &ManagedTest{ID: "active", Name: "active", Status: "RUNNING", ProjectID: "project-a", TimeslotID: ptrString("slot-a")}
	pending := &ManagedTest{ID: "pending", Name: "pending", Status: "PENDING", ProjectID: "project-a"}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.tests[pending.ID] = pending
	runner.mu.Unlock()
	runner.testStore = &scriptedTestPersistence{err: wantErr, failUpdateCall: failCalls(1, 1000)}
	defer runner.beginShutdown()
	reservations := &reservationReleaseSpy{}
	runner.reservationStore = reservations
	runner.seqPlanRuns["run-a"] = &seqPlanRunState{
		runID: "run-a", planID: "plan-a", status: "running", cursor: 0,
		entries:  []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: "scenario-a"}},
		statuses: []storage.ExecutionPlanRunEntry{{Order: 0, ScenarioID: "scenario-a", TestID: test.ID, Status: "running"}},
	}
	runner.seqPlanByTest[test.ID] = "run-a"

	err := runner.completeTestRun(test, "COMPLETE", time.Now().UTC(), "", &storage.RunArchiveRecord{TestID: test.ID})
	if !errors.Is(err, wantErr) {
		t.Fatalf("completeTestRun error = %v, want database error", err)
	}
	if test.Status != "RUNNING" || test.EndedAt != nil || test.Archive != nil {
		t.Fatalf("failed durable completion mutated active test: %+v", test)
	}
	if pending.Status != "PENDING" {
		t.Fatalf("pending test was promoted after failed completion: %s", pending.Status)
	}
	if reservations.clearCalls != 0 {
		t.Fatalf("reservation clear calls = %d, want 0", reservations.clearCalls)
	}
	if _, ok := runner.seqPlanByTest[test.ID]; !ok {
		t.Fatal("execution plan advanced after failed terminal persistence")
	}
	if events := hubEventTypes(hub); len(events) != 0 {
		t.Fatalf("terminal events after failed persistence = %v, want none", events)
	}
}

func TestCompleteTestRunRetriesThenCommitsTerminalSideEffects(t *testing.T) {
	wantErr := errors.New("brief database outage")
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	test := &ManagedTest{ID: "active-retry", Name: "active", Status: "RUNNING", ProjectID: "project-a", TimeslotID: ptrString("slot-a")}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	testStore := &scriptedTestPersistence{err: wantErr, failUpdateCall: failCalls(1, durableTransitionAttempts-1)}
	runner.testStore = testStore
	reservations := &reservationReleaseSpy{}
	runner.reservationStore = reservations
	planStore := &scriptedExecutionPlanRunStore{}
	runner.execPlanRunStore = planStore
	runner.seqPlanRuns["run-retry"] = &seqPlanRunState{
		runID: "run-retry", planID: "plan-retry", status: "running", cursor: 0, createdAt: time.Now().UTC(),
		entries:  []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: "scenario-a"}},
		statuses: []storage.ExecutionPlanRunEntry{{Order: 0, ScenarioID: "scenario-a", TestID: test.ID, Status: "running"}},
	}
	runner.seqPlanByTest[test.ID] = "run-retry"

	archive := &storage.RunArchiveRecord{TestID: test.ID}
	if err := runner.completeTestRun(test, "COMPLETE", time.Now().UTC(), "", archive); err != nil {
		t.Fatalf("completeTestRun after transient failures: %v", err)
	}
	if testStore.updateCalls != durableTransitionAttempts {
		t.Fatalf("terminal persistence attempts = %d, want %d", testStore.updateCalls, durableTransitionAttempts)
	}
	if test.Status != "COMPLETE" || test.Archive != archive {
		t.Fatalf("terminal completion did not commit: %+v", test)
	}
	if reservations.clearCalls != 1 {
		t.Fatalf("reservation clear calls = %d, want 1", reservations.clearCalls)
	}
	if _, ok := runner.seqPlanByTest[test.ID]; ok {
		t.Fatal("execution-plan mapping remained after recovered completion")
	}
	if _, ok := runner.seqPlanRuns["run-retry"]; ok {
		t.Fatal("single-entry execution plan did not finish after recovered completion")
	}
	events := hubEventTypes(hub)
	if !containsEvent(events, "test_complete") || !containsEvent(events, "execution_plan_run_finished") {
		t.Fatalf("recovered terminal events = %v", events)
	}
}

func TestCompleteTestRunReconcilesAutomaticallyAfterRetryBound(t *testing.T) {
	wantErr := errors.New("database outage exceeded inline retry bound")
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	test := &ManagedTest{ID: "active-background-retry", Name: "active", Status: "RUNNING", ProjectID: "project-a", TimeslotID: ptrString("slot-a")}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	testStore := &scriptedTestPersistence{err: wantErr, failUpdateCall: failCalls(1, durableTransitionAttempts)}
	runner.testStore = testStore
	reservations := &reservationReleaseSpy{}
	runner.reservationStore = reservations

	archive := &storage.RunArchiveRecord{TestID: test.ID}
	if err := runner.completeTestRun(test, "COMPLETE", time.Now().UTC(), "", archive); !errors.Is(err, wantErr) {
		t.Fatalf("initial completeTestRun error = %v, want exhausted persistence error", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	events := make([]string, 0)
	for {
		runner.mu.RLock()
		status := test.Status
		runner.mu.RUnlock()
		events = append(events, hubEventTypes(hub)...)
		if status == "COMPLETE" && containsEvent(events, "test_complete") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background terminal reconciliation did not complete; status=%s", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if testStore.updateCalls <= durableTransitionAttempts {
		t.Fatalf("terminal persistence attempts = %d, want a background retry after %d inline attempts", testStore.updateCalls, durableTransitionAttempts)
	}
	if reservations.clearCalls != 1 {
		t.Fatalf("reservation clear calls = %d, want 1", reservations.clearCalls)
	}
	if !containsEvent(events, "test_complete") {
		t.Fatalf("background reconciliation events = %v, want test_complete", events)
	}
}

func TestTerminalCapacityReleaseRetriesWithoutDuplicateCompletion(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	test := &ManagedTest{ID: "capacity-release-retry", Name: "active", Status: "RUNNING", ProjectID: "project-a", TimeslotID: ptrString("slot-a")}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	runner.testStore = &scriptedTestPersistence{}
	wantErr := errors.New("reservation store unavailable")
	reservations := &reservationReleaseSpy{err: wantErr, failCalls: map[int]bool{1: true}}
	runner.reservationStore = reservations

	if err := runner.completeTestRun(test, "COMPLETE", time.Now().UTC(), "", nil); !errors.Is(err, wantErr) {
		t.Fatalf("completeTestRun error = %v, want reservation release error", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		reservations.mu.Lock()
		calls := reservations.clearCalls
		reservations.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capacity release retry calls = %d, want at least 2", calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
	events := hubEventTypes(hub)
	completeEvents := 0
	for _, event := range events {
		if event == "test_complete" {
			completeEvents++
		}
	}
	if completeEvents != 1 {
		t.Fatalf("test_complete events = %d (%v), want exactly 1", completeEvents, events)
	}
}

func TestStartupActiveTestReconciliationReleasesUnmappedTimeslotBinding(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	defer runner.beginShutdown()
	testStore := &scriptedTestPersistence{}
	runner.testStore = testStore
	reservations := &reservationReleaseSpy{}
	runner.reservationStore = reservations
	test := &ManagedTest{
		ID: "startup-active", Name: "startup active", Status: "RUNNING",
		ProjectID: "project-a", TimeslotID: ptrString("startup-slot"),
	}

	// loadStoredTests/reloadStoredTests reconcile before placing the hydrated row
	// in tr.tests. Exercise that ordering explicitly.
	runner.reconcileStoredActiveTest(test)
	if test.Status != "FAILED" || test.EndedAt == nil {
		t.Fatalf("reconciled test = %+v, want durable FAILED", test)
	}
	runner.mu.RLock()
	_, mapped := runner.tests[test.ID]
	runner.mu.RUnlock()
	if mapped {
		t.Fatal("fixture unexpectedly inserted test before capacity release")
	}
	reservations.mu.Lock()
	calls := reservations.clearCalls
	slots := append([]string(nil), reservations.clearedSlot...)
	tests := append([]string(nil), reservations.clearedTest...)
	reservations.mu.Unlock()
	if calls != 1 || len(slots) != 1 || slots[0] != "startup-slot" || len(tests) != 1 || tests[0] != test.ID {
		t.Fatalf("capacity releases = calls %d slots %v tests %v", calls, slots, tests)
	}
	if testStore.updateCalls != 1 {
		t.Fatalf("terminal status persistence calls = %d, want 1", testStore.updateCalls)
	}
}

func TestStartupActiveTestReconciliationRetriesUnmappedTimeslotRelease(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	defer runner.beginShutdown()
	runner.testStore = &scriptedTestPersistence{}
	reservations := &reservationReleaseSpy{
		err: errors.New("temporary reservation outage"), failCalls: map[int]bool{1: true},
	}
	runner.reservationStore = reservations
	test := &ManagedTest{
		ID: "startup-active-retry", Name: "startup active retry", Status: "INITIALIZING",
		ProjectID: "project-a", TimeslotID: ptrString("startup-retry-slot"),
	}

	runner.reconcileStoredActiveTest(test)
	if test.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED despite release error", test.Status)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		reservations.mu.Lock()
		calls := reservations.clearCalls
		slots := append([]string(nil), reservations.clearedSlot...)
		tests := append([]string(nil), reservations.clearedTest...)
		reservations.mu.Unlock()
		if calls >= 2 {
			if slots[0] != "startup-retry-slot" || slots[1] != "startup-retry-slot" || tests[0] != test.ID || tests[1] != test.ID {
				t.Fatalf("retry lost explicit binding: slots=%v tests=%v", slots, tests)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capacity release retry calls = %d, want at least 2", calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestScenarioSchedulerTerminalPersistenceFailureReconcilesPreparedRun(t *testing.T) {
	store := &metricsStoreSpy{}
	hub := NewHub()
	runner := NewTestRunner(2, hub, store)
	runner.StoreScript(&dsl.Script{
		ID: "script-scheduler-durable", Name: "Scheduler", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduler", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-scheduler-durable", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	wantErr := errors.New("test row unavailable")
	testStore := &scriptedTestPersistence{
		testPersistence: store, err: wantErr,
		failSaveCalls: failCalls(3, durableTransitionAttempts),
	}
	runner.testStore = testStore
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	schedule := &storage.ScenarioSchedule{
		ID: "schedule-durable-failure", ScenarioID: scenario.ID, Name: "Durable failure",
		CronExpr: "not a cron expression", Enabled: true, NextRunAt: &now, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}

	runner.mu.Lock()
	_, err = runner.runScenarioScheduleLocked(schedule, now)
	runner.mu.Unlock()
	if !errors.Is(err, wantErr) {
		t.Fatalf("runScenarioScheduleLocked error = %v, want persistence error", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var materialized *ManagedTest
	for {
		runner.mu.RLock()
		for _, candidate := range runner.tests {
			materialized = candidate
		}
		status := ""
		if materialized != nil {
			status = materialized.Status
		}
		runner.mu.RUnlock()
		if status == "FAILED" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduled terminal reconciliation did not complete: %+v", materialized)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if materialized.executionCtx == nil || materialized.executionCtx.Err() == nil {
		t.Fatal("reconciled scheduled failure retained a live execution context")
	}
	if events := hubEventTypes(hub); !containsEvent(events, "test_failed") {
		t.Fatalf("scheduler recovery events = %v, want durable test_failed", events)
	}
}

func TestScenarioSchedulerRejectsSplitTimeslotPersistence(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID: "script-scheduled-release", Name: "Scheduler", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Scheduler release", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-scheduled-release", VUsers: 1}}})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	wantErr := errors.New("test row unavailable")
	runner.testStore = &scriptedTestPersistence{testPersistence: store, err: wantErr, failSaveCalls: failCalls(1, durableTransitionAttempts)}
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	reservationNow := time.Now().UTC()
	slotID := "scheduled-slot"
	release := &reservationReleaseSpy{
		failCalls: map[int]bool{1: true}, err: errors.New("reservation store unavailable"),
		reservation: &storage.CapacityReservation{
			ID: slotID, ProjectID: storage.DefaultProjectID, StartAt: reservationNow.Add(-time.Hour), EndAt: reservationNow.Add(time.Hour), ApprovalState: "approved",
			RequestedCapacity: &storage.CapacityReservationRequest{VUsers: reservedInt(10)},
		},
	}
	runner.reservationStore = release
	schedule := &storage.ScenarioSchedule{
		ID: "schedule-release-retry", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID, Name: "Release retry", TimeslotID: slotID,
		CronExpr: "*/5 * * * *", Enabled: true, NextRunAt: &now, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	runner.mu.Lock()
	_, err = runner.runScenarioScheduleLocked(schedule, now)
	runner.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "does not support atomic test creation") {
		t.Fatalf("runScenarioScheduleLocked error = %v, want atomic-persistence rejection", err)
	}
	release.mu.Lock()
	clearCalls := release.clearCalls
	release.mu.Unlock()
	if clearCalls != 0 {
		t.Fatalf("split-write compensation ran %d times; atomic create must leave nothing to release", clearCalls)
	}
}

func TestSequentialPlanTransitionPersistenceFailurePreventsEntryLaunch(t *testing.T) {
	runner, backend, scenarioIDs := newPlanTestRunnerForProject(t, "project-a", "first")
	wantErr := errors.New("plan store unavailable")
	planStore := &scriptedExecutionPlanRunStore{err: wantErr, failCalls: failCalls(2, durableTransitionAttempts)}
	runner.execPlanRunStore = planStore
	plan := &storage.ExecutionPlan{
		ID: "plan-transition-failure", ProjectID: "project-a", Name: "Durable", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenarioIDs[0]}},
	}

	if _, err := runner.StartExecutionPlanRun(plan, "", ""); !errors.Is(err, errExecutionPlanRunPersistence) {
		t.Fatalf("StartExecutionPlanRun error = %v, want plan persistence error", err)
	}
	if testID, started := waitForStart(t, backend, 100*time.Millisecond); started {
		t.Fatalf("entry %s launched despite transition persistence failure", testID)
	}
	runner.seqMu.Lock()
	stateCount := len(runner.seqPlanRuns)
	runner.seqMu.Unlock()
	if stateCount != 0 {
		t.Fatalf("sequential in-memory runs after failed transition = %d, want terminalized", stateCount)
	}
	planStore.mu.Lock()
	var persisted *storage.ExecutionPlanRun
	for _, candidate := range planStore.runs {
		persisted = cloneExecutionPlanRunForTest(candidate)
	}
	planStore.mu.Unlock()
	if persisted == nil || persisted.Status != "failed" || persisted.Entries[0].Status != "skipped" {
		t.Fatalf("persisted failed run = %+v", persisted)
	}
	if events := hubEventTypes(runner.hub); containsEvent(events, "execution_plan_entry_started") || containsEvent(events, "test_created") {
		t.Fatalf("materialization success broadcast after failed transition: %v", events)
	}
}

func TestSequentialPlanAdvancePersistenceFailureRetriesAutomatically(t *testing.T) {
	runner, backend, scenarioIDs := newPlanTestRunnerForProject(t, "project-a", "first", "second")
	wantErr := errors.New("plan store unavailable")
	// Save 1 anchors the run, save 2 claims the first entry before creating its
	// test, and save 3 attaches the queued test. Fail the subsequent terminal
	// transition so the retry path is exercised after a successful launch.
	planStore := &scriptedExecutionPlanRunStore{err: wantErr, failCalls: failCalls(4, durableTransitionAttempts)}
	runner.execPlanRunStore = planStore
	plan := &storage.ExecutionPlan{
		ID: "plan-advance-failure", ProjectID: "project-a", Name: "Durable", RunMode: "sequential",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenarioIDs[0]}, {Order: 1, ScenarioID: scenarioIDs[1]}},
	}
	run, err := runner.StartExecutionPlanRun(plan, "", "")
	if err != nil {
		t.Fatalf("StartExecutionPlanRun: %v", err)
	}
	firstID, started := waitForStart(t, backend, time.Second)
	if !started {
		t.Fatal("first entry did not launch")
	}
	runner.mu.RLock()
	first := runner.tests[firstID]
	runner.mu.RUnlock()
	if err := runner.completeTestRun(first, "COMPLETE", time.Now().UTC(), "", nil); !errors.Is(err, errExecutionPlanRunPersistence) {
		t.Fatalf("completeTestRun error = %v, want plan persistence error", err)
	}
	if testID, started := waitForStart(t, backend, 10*time.Millisecond); started {
		t.Fatalf("second entry %s launched despite failed advance persistence", testID)
	}
	if _, started := waitForStart(t, backend, 2*time.Second); !started {
		t.Fatal("second entry did not launch after automatic durable retry recovered")
	}
	runner.seqMu.Lock()
	state := runner.seqPlanRuns[run.ID]
	_, mappingRetained := runner.seqPlanByTest[firstID]
	runner.seqMu.Unlock()
	if state == nil || state.statuses[0].Status != "complete" || mappingRetained {
		t.Fatalf("automatic advance did not commit recovered state: state=%+v mapping=%v", state, mappingRetained)
	}
}

func TestParallelPlanPersistenceFailureReturns503BeforeMaterialization(t *testing.T) {
	server, store := newTimeslotServer(t)
	plan := &storage.ExecutionPlan{
		ID: "plan-parallel-durable", ProjectID: "project-a", Name: "Parallel", RunMode: "parallel",
		Entries:   []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: "missing"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	server.runner.execPlanRunStore = &scriptedExecutionPlanRunStore{
		err: errors.New("plan store unavailable"), failCalls: failCalls(1, durableTransitionAttempts),
	}

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/"+plan.ID+"/runs", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("materialize status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	server.runner.mu.RLock()
	testCount := len(server.runner.tests)
	server.runner.mu.RUnlock()
	if testCount != 0 {
		t.Fatalf("parallel persistence failure created %d tests, want 0", testCount)
	}
}

func TestParallelPlanFinalPersistenceFailureCleansUpSilentMaterialization(t *testing.T) {
	runner, _, scenarioIDs := newPlanTestRunnerForProject(t, "project-a", "parallel")
	_ = hubEventTypes(runner.hub)
	planStore := &scriptedExecutionPlanRunStore{
		err: errors.New("plan store unavailable"), failCalls: failCalls(2, durableTransitionAttempts),
	}
	runner.execPlanRunStore = planStore
	plan := &storage.ExecutionPlan{
		ID: "plan-parallel-final-failure", ProjectID: "project-a", Name: "Parallel", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenarioIDs[0]}},
	}

	if _, err := runner.MaterializeAndRunExecutionPlan(plan, "", ""); !errors.Is(err, errExecutionPlanRunPersistence) {
		t.Fatalf("MaterializeAndRunExecutionPlan error = %v, want persistence error", err)
	}
	runner.mu.RLock()
	testCount := len(runner.tests)
	var staged *ManagedTest
	for _, candidate := range runner.tests {
		staged = candidate
	}
	runner.mu.RUnlock()
	if testCount != 1 || staged == nil || staged.Status != "FAILED" || staged.EndedAt == nil {
		t.Fatalf("failed final plan persistence staged test = %+v (count %d), want one terminal FAILED row", staged, testCount)
	}
	if events := hubEventTypes(runner.hub); containsEvent(events, "test_created") {
		t.Fatalf("silent materialization broadcast before final plan persistence: %v", events)
	}
	var persisted *storage.ExecutionPlanRun
	planStore.mu.Lock()
	for _, candidate := range planStore.runs {
		persisted = cloneExecutionPlanRunForTest(candidate)
	}
	planStore.mu.Unlock()
	if persisted == nil || persisted.Status != "failed" {
		t.Fatalf("parallel anchor was not terminalized after rollback: %+v", persisted)
	}
}

type failingOccurrenceScheduleStore struct {
	executionPlanSchedulePersistence
	mu        sync.Mutex
	saveCalls int
	failCalls map[int]bool
	err       error
}

func (s *failingOccurrenceScheduleStore) SaveExecutionPlanScheduleOccurrence(occurrence *storage.ExecutionPlanScheduleOccurrence) error {
	s.mu.Lock()
	s.saveCalls++
	call := s.saveCalls
	fail := s.failCalls[call]
	s.mu.Unlock()
	if fail {
		return s.err
	}
	return s.executionPlanSchedulePersistence.SaveExecutionPlanScheduleOccurrence(occurrence)
}

func (s *failingOccurrenceScheduleStore) CompareAndSwapExecutionPlanScheduleOccurrence(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedRevision int64) (bool, error) {
	s.mu.Lock()
	s.saveCalls++
	call := s.saveCalls
	fail := s.failCalls[call]
	s.mu.Unlock()
	if fail {
		return false, s.err
	}
	store, ok := s.executionPlanSchedulePersistence.(executionPlanScheduleOccurrenceCASPersistence)
	if !ok {
		return false, errors.New("embedded occurrence store does not support revision CAS")
	}
	return store.CompareAndSwapExecutionPlanScheduleOccurrence(occurrence, expectedRevision)
}

func TestExecutionPlanScheduleOccurrenceFinalizeRetriesBeforeAdvancing(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	plan := &storage.ExecutionPlan{ID: "plan-scheduled-durable", ProjectID: "project-a", Name: "Scheduled", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	next := now.Add(-time.Minute)
	schedule := &storage.ExecutionPlanSchedule{
		ID: "schedule-finalize-failure", PlanID: plan.ID, ProjectID: plan.ProjectID, Name: "Due",
		CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &next, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	wantErr := errors.New("occurrence store unavailable")
	scheduleStore := &failingOccurrenceScheduleStore{
		executionPlanSchedulePersistence: store, err: wantErr,
		failCalls: failCalls(1, durableTransitionAttempts),
	}
	runner := NewTestRunner(1, NewHub(), store)
	runner.execPlanScheduleStore = scheduleStore

	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); !errors.Is(err, wantErr) {
		t.Fatalf("runDueExecutionPlanScheduleOnce error = %v, want finalize persistence error", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		persisted, loadErr := store.GetExecutionPlanSchedule(schedule.ID)
		if loadErr != nil {
			t.Fatalf("GetExecutionPlanSchedule: %v", loadErr)
		}
		occurrences, listErr := store.ListExecutionPlanScheduleOccurrences(schedule.ID)
		if listErr != nil {
			t.Fatalf("ListExecutionPlanScheduleOccurrences: %v", listErr)
		}
		if len(occurrences) == 1 && occurrences[0].Status == "materialized" && persisted.NextRunAt != nil && persisted.NextRunAt.After(next) {
			if persisted.LastRunRunID == "" {
				t.Fatal("recovered schedule advance did not retain the materialized run id")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("occurrence/schedule did not reconcile: occurrence=%+v schedule=%+v", occurrences, persisted)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
