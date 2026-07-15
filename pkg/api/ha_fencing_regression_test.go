package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestControllerInstanceIDIsUniquePerProcessInstance(t *testing.T) {
	first := controllerInstanceID()
	second := controllerInstanceID()
	if strings.TrimSpace(first) == "" || strings.TrimSpace(second) == "" {
		t.Fatalf("controller instance IDs must be non-empty: %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("controller instance IDs collided: %q", first)
	}
	if !strings.Contains(first, "/") || !strings.Contains(second, "/") {
		t.Fatalf("controller instance IDs must include a readable prefix and UUID: %q, %q", first, second)
	}
}

func TestExecutionLeaseTokenRotatesOnReacquisition(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	leadership := newExecutionLeadership(store, "same-process")
	leadership.tick()
	first := leadership.executionFenceToken()
	if first == "" {
		t.Fatal("first acquisition did not produce a token")
	}
	leadership.relinquish()
	leadership.tick()
	defer leadership.relinquish()
	second := leadership.executionFenceToken()
	if second == "" {
		t.Fatal("reacquisition did not produce a token")
	}
	if first == second {
		t.Fatalf("execution epoch token was reused across reacquisition: %q", first)
	}
}

func TestBuildManagedTestRecordPreservesTerminalFields(t *testing.T) {
	endedAt := time.Now().UTC()
	test := &ManagedTest{
		ID:         "terminal-record",
		Name:       "Terminal record",
		Status:     "FAILED",
		StartDelay: "30s",
		StartedAt:  &endedAt,
		EndedAt:    &endedAt,
		Error:      "generator disconnected",
	}
	record := buildManagedTestRecord(test)
	if record.EndTime == nil || !record.EndTime.Equal(endedAt) {
		t.Fatalf("EndTime = %v, want %s", record.EndTime, endedAt)
	}
	if record.Error != test.Error {
		t.Fatalf("Error = %q, want %q", record.Error, test.Error)
	}
	if record.StartDelay != test.StartDelay {
		t.Fatalf("StartDelay = %q, want %q", record.StartDelay, test.StartDelay)
	}
}

func TestStopActiveTestsHonorsDeadlineWhileRunnerLockIsBusy(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- runner.stopActiveTests(ctx) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			runner.mu.Unlock()
			t.Fatalf("stopActiveTests error = %v, want deadline exceeded", err)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			runner.mu.Unlock()
			t.Fatalf("stopActiveTests waited past its bounded deadline: %s", elapsed)
		}
		runner.mu.Unlock()
	case <-time.After(time.Second):
		runner.mu.Unlock()
		t.Fatal("stopActiveTests remained blocked behind runner.mu")
	}
}

type blockingProjectReadStore struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingProjectReadStore) GetProject(id string) (*storage.Project, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return &storage.Project{ID: id, Name: id}, nil
}

func TestScheduleCompilationReleasesRunnerLockDuringStorage(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	script := runner.StoreScript(&dsl.Script{
		ID:        "scheduler-unlocked-script",
		Name:      "Scheduler unlocked",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Scheduler unlocked",
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	blocking := &blockingProjectReadStore{started: make(chan struct{}), release: make(chan struct{})}
	runner.projectStore = blocking

	buildDone := make(chan error, 1)
	go func() {
		runner.mu.Lock()
		_, err := runner.buildManagedTestForScheduleSweepLocked(CreateTestRequest{ScenarioID: scenario.ID}, "snapshot-build", "DRAFT")
		runner.mu.Unlock()
		buildDone <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("schedule compilation never reached blocking storage")
	}

	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		runner.mu.Unlock()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
		// The storage call is still blocked, proving it does not own tr.mu.
	case <-time.After(500 * time.Millisecond):
		close(blocking.release)
		t.Fatal("scheduler storage call retained runner.mu")
	}
	close(blocking.release)
	if err := <-buildDone; err != nil {
		t.Fatalf("scheduled snapshot build: %v", err)
	}
}

func TestStaleExecutionEpochCannotCommitTerminalSideEffects(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	oldLeadership := newExecutionLeadership(store, "replica-old")
	oldLeadership.tick()
	if !oldLeadership.IsAdmitted() {
		t.Fatal("old replica did not acquire execution leadership")
	}
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(oldLeadership))
	oldToken := oldLeadership.executionFenceToken()
	if oldToken == "" {
		t.Fatal("old leadership did not expose an execution epoch token")
	}
	test := &ManagedTest{
		ID:             "stale-terminal-callback",
		Name:           "Stale completion",
		Status:         "QUEUED",
		ProjectID:      storage.DefaultProjectID,
		CreatedAt:      time.Now().UTC(),
		executionFence: oldToken,
	}
	if err := runner.saveManagedTestRecord(test); err != nil {
		t.Fatalf("seed fenced test: %v", err)
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	// Simulate a database-visible takeover before the old process notices its
	// loss. This is the exact window where an asynchronous completion callback
	// must be stopped by the durable epoch predicate, not an in-memory flag.
	if err := store.ReleaseSchedulerLease(executionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(store, "replica-new")
	newLeadership.tick()
	if !newLeadership.IsAdmitted() {
		t.Fatal("successor replica did not acquire execution leadership")
	}
	defer newLeadership.relinquish()

	completedAt := time.Now().UTC()
	archive := &storage.RunArchiveRecord{TestID: test.ID, CompletedAt: completedAt}
	if err := runner.completeTestRun(test, "COMPLETE", completedAt, "", archive); err != nil {
		t.Fatalf("stale completeTestRun should be cancelled without retry: %v", err)
	}
	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if durable.Status != "QUEUED" || durable.EndTime != nil {
		t.Fatalf("stale epoch terminalized durable row: %#v", durable)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if test.Status != "QUEUED" || test.EndedAt != nil || test.Archive != nil {
		t.Fatalf("stale epoch published in-memory terminal side effects: %#v", test)
	}
	if _, ok := runner.archives[test.ID]; ok {
		t.Fatal("stale epoch attached an archive")
	}
	if _, ok := runner.terminalRetrying[test.ID]; ok {
		t.Fatal("stale epoch enqueued an unbounded terminal retry")
	}
}

func TestEmptyExecutionFenceCannotAdoptSuccessorLease(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	leadership := newExecutionLeadership(store, "replica-successor")
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("successor did not acquire leadership")
	}
	defer leadership.relinquish()
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(leadership))
	token := leadership.executionFenceToken()
	seed := &ManagedTest{
		ID:             "empty-fence-clone",
		Name:           "Empty fence clone",
		Status:         "QUEUED",
		ProjectID:      storage.DefaultProjectID,
		CreatedAt:      time.Now().UTC(),
		executionFence: token,
	}
	if err := runner.saveManagedTestRecord(seed); err != nil {
		t.Fatalf("seed fenced row: %v", err)
	}

	staleClone := *seed
	staleClone.executionFence = ""
	staleClone.Status = "ABORTED"
	if err := runner.saveManagedTestRecord(&staleClone); !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("empty-fence save error = %v, want errNotExecutionLeader", err)
	}
	durable, err := store.GetTest(seed.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if durable.Status != "QUEUED" {
		t.Fatalf("empty-fence clone adopted successor lease and wrote %q", durable.Status)
	}
}

func TestHAStandbyBackfillsLegacyTestRefKeyWithoutExecutionEpoch(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	startedAt := time.Now().UTC().Add(-time.Minute)
	if err := store.SaveTest(&storage.TestRecord{
		ID:        "legacy-empty-ref-key",
		Name:      "Legacy active run",
		Status:    "RUNNING",
		ProjectID: storage.DefaultProjectID,
		StartTime: startedAt,
		Groups:    []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	// Construct the HA runner before ticking leadership, matching standby boot.
	// Reference metadata may be repaired, but active lifecycle columns remain
	// owned by whichever replica holds the execution epoch.
	leadership := newExecutionLeadership(store, "standby-refkey")
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(leadership))
	if leadership.IsLeader() {
		t.Fatal("standby unexpectedly acquired leadership during construction")
	}
	durable, err := store.GetTest("legacy-empty-ref-key")
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if strings.TrimSpace(durable.RefKey) == "" {
		t.Fatal("standby did not backfill the legacy reference key")
	}
	if durable.Status != "RUNNING" || !durable.StartTime.Equal(startedAt) || durable.EndTime != nil || durable.Error != "" {
		t.Fatalf("standby reference-key repair changed lifecycle fields: %#v", durable)
	}
	runner.mu.RLock()
	managed := runner.tests[durable.ID]
	runner.mu.RUnlock()
	if managed == nil || managed.RefKey != durable.RefKey {
		t.Fatalf("in-memory ref key = %#v, durable ref key = %q", managed, durable.RefKey)
	}
}

func TestSchedulerWritesUseDatabaseExecutionEpochFence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	scenarioID := "scheduler-epoch-scenario"
	if err := store.SaveScenario(&storage.Scenario{
		ID:        scenarioID,
		Name:      "Scheduler epoch",
		Duration:  "1m",
		CreatedAt: now,
		UpdatedAt: now,
	}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	plannedAt := now.Add(time.Hour)
	scheduleID := "scheduler-epoch-schedule"
	if err := store.SaveScenarioSchedule(&storage.ScenarioSchedule{
		ID:         scheduleID,
		ScenarioID: scenarioID,
		ProjectID:  storage.DefaultProjectID,
		Name:       "Scheduler epoch",
		CronExpr:   "0 * * * *",
		Timezone:   "UTC",
		Enabled:    true,
		NextRunAt:  &plannedAt,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}

	oldLeadership := newExecutionLeadership(store, "scheduler-old")
	oldLeadership.tick()
	if !oldLeadership.IsAdmitted() {
		t.Fatal("old scheduler did not acquire execution leadership")
	}
	defer oldLeadership.relinquish()
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(oldLeadership))

	runner.mu.Lock()
	schedule := cloneScenarioSchedule(runner.scenarios[scenarioID].Schedules[0])
	schedule.LastError = "old epoch committed"
	schedule.UpdatedAt = now.Add(time.Second)
	if err := runner.saveScenarioScheduleForSweepLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("old epoch schedule update: %v", err)
	}
	occurrence := &storage.ScenarioScheduleOccurrence{
		ID:               "scheduler-epoch-occurrence",
		ScheduleID:       scheduleID,
		ScenarioID:       scenarioID,
		ProjectID:        storage.DefaultProjectID,
		ScheduleRevision: schedule.Revision,
		PlannedAt:        plannedAt,
		Status:           "materializing",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := runner.saveScenarioScheduleOccurrenceForSweepLocked(occurrence); err != nil {
		runner.mu.Unlock()
		t.Fatalf("old epoch occurrence write: %v", err)
	}
	runner.mu.Unlock()

	oldToken := oldLeadership.executionFenceToken()
	if err := store.ReleaseSchedulerLease(executionLeaseName, oldToken); err != nil {
		t.Fatalf("release old database lease: %v", err)
	}
	newLeadership := newExecutionLeadership(store, "scheduler-new")
	newLeadership.tick()
	if !newLeadership.IsAdmitted() {
		t.Fatal("successor scheduler did not acquire execution leadership")
	}
	defer newLeadership.relinquish()

	// oldLeadership intentionally remains locally admitted. Only the database
	// predicate can close this takeover window.
	schedule.LastError = "stale epoch overwrite"
	schedule.UpdatedAt = now.Add(2 * time.Second)
	runner.mu.Lock()
	err = runner.saveScenarioScheduleForSweepLocked(schedule)
	runner.mu.Unlock()
	if !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("stale schedule write error = %v, want errNotExecutionLeader", err)
	}
	occurrence.Status = "failed"
	occurrence.ReasonCode = "stale_callback"
	runner.mu.Lock()
	err = runner.saveScenarioScheduleOccurrenceForSweepLocked(occurrence)
	runner.mu.Unlock()
	if !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("stale occurrence write error = %v, want errNotExecutionLeader", err)
	}

	durableSchedule, err := store.GetScenarioSchedule(scheduleID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if durableSchedule.LastError != "old epoch committed" || durableSchedule.Revision != 2 {
		t.Fatalf("stale epoch changed durable schedule: %#v", durableSchedule)
	}
	durableOccurrence, err := store.GetScenarioScheduleOccurrence(occurrence.ID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrence: %v", err)
	}
	if durableOccurrence.Status != "materializing" || durableOccurrence.ReasonCode != "" {
		t.Fatalf("stale epoch changed durable occurrence: %#v", durableOccurrence)
	}
}
