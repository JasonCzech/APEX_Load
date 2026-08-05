package api

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scenarioRecoveryFaultStore struct {
	*metricsStoreSpy
	mu                     sync.Mutex
	failNextOccurrenceSave bool
	failNextScheduleSave   bool
	err                    error
}

// scheduleQuotaRaceStore consumes the last concurrency slot after the
// scheduler's calendar preflight but before start admission. It deterministically
// exercises the prepare-to-PENDING path without relying on goroutine timing.
type scheduleQuotaRaceStore struct {
	*metricsStoreSpy
	runner *TestRunner
	once   sync.Once
}

func (s *scheduleQuotaRaceStore) SaveTest(test *storage.TestRecord) error {
	if err := s.metricsStoreSpy.SaveTest(test); err != nil {
		return err
	}
	if test != nil && test.Status == "QUEUED" && s.runner != nil {
		s.once.Do(func() {
			s.runner.mu.Lock()
			s.runner.tests["quota-race-winner"] = &ManagedTest{
				ID: "quota-race-winner", Name: "Concurrent quota winner", Status: "RUNNING", TotalVUsers: 1,
			}
			s.runner.mu.Unlock()
		})
	}
	return nil
}

func (s *scenarioRecoveryFaultStore) consumeOccurrenceFailure() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := s.failNextOccurrenceSave
	s.failNextOccurrenceSave = false
	return fail
}

func (s *scenarioRecoveryFaultStore) SaveScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence) error {
	if s.consumeOccurrenceFailure() {
		return s.err
	}
	return s.metricsStoreSpy.SaveScenarioScheduleOccurrence(occurrence)
}

func (s *scenarioRecoveryFaultStore) CreateScenarioScheduleOccurrenceIfAbsent(occurrence *storage.ScenarioScheduleOccurrence) (bool, error) {
	if s.consumeOccurrenceFailure() {
		return false, s.err
	}
	return s.metricsStoreSpy.CreateScenarioScheduleOccurrenceIfAbsent(occurrence)
}

func (s *scenarioRecoveryFaultStore) CompareAndSwapScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence, expectedRevision int64) (bool, error) {
	if s.consumeOccurrenceFailure() {
		return false, s.err
	}
	return s.metricsStoreSpy.CompareAndSwapScenarioScheduleOccurrence(occurrence, expectedRevision)
}

func (s *scenarioRecoveryFaultStore) SaveScenarioSchedule(schedule *storage.ScenarioSchedule) error {
	s.mu.Lock()
	fail := s.failNextScheduleSave
	s.failNextScheduleSave = false
	s.mu.Unlock()
	if fail {
		return s.err
	}
	return s.metricsStoreSpy.SaveScenarioSchedule(schedule)
}

func TestScenarioSchedulerRecordsQuotaPendingOccurrenceAsQueued(t *testing.T) {
	store := &scheduleQuotaRaceStore{metricsStoreSpy: &metricsStoreSpy{}}
	runner := NewTestRunner(1, NewHub(), store, withUnfencedDeploymentLeaseStartForFaultInjection())
	store.runner = runner
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "script-quota-pending-schedule", Name: "Quota pending schedule", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Quota pending schedule", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: "script-quota-pending-schedule", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.July, 19, 16, 0, 0, 0, time.UTC)
	schedule := &storage.ScenarioSchedule{
		ID: "schedule-quota-pending", ScenarioID: scenario.ID, Revision: 1,
		Name: "Quota pending", CronExpr: "*/5 * * * *", Enabled: true, NextRunAt: &now,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	select {
	case testID := <-backend.started:
		t.Fatalf("quota-pending scheduled test %q was dispatched", testID)
	default:
	}

	var pending *ManagedTest
	runner.mu.RLock()
	for id, test := range runner.tests {
		if id != "quota-race-winner" {
			pending = test
			break
		}
	}
	runner.mu.RUnlock()
	if pending == nil || pending.Status != "PENDING" || !strings.Contains(pending.PendingReason, "global test slot") {
		t.Fatalf("scheduled run = %#v, want quota PENDING", pending)
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences: %v", err)
	}
	if len(occurrences) != 1 {
		t.Fatalf("occurrences = %#v, want one", occurrences)
	}
	occurrence := occurrences[0]
	if occurrence.Status != "queued" || occurrence.ReasonCode != "quota_pending" || occurrence.TestID != pending.ID || occurrence.MaterializedAt != nil {
		t.Fatalf("occurrence = %#v, want queued/quota_pending without materialized_at", occurrence)
	}
	if !strings.Contains(occurrence.ConflictSummary, "global test slot") {
		t.Fatalf("occurrence conflict summary = %q, want quota reason", occurrence.ConflictSummary)
	}
	runner.mu.RLock()
	currentSchedule := cloneScenarioSchedule(runner.scenarios[scenario.ID].Schedules[0])
	runner.mu.RUnlock()
	if currentSchedule.LastRunAt != nil {
		t.Fatalf("schedule LastRunAt = %v, want nil before actual start", currentSchedule.LastRunAt)
	}
	if currentSchedule.LastRunTestID != pending.ID || currentSchedule.LastOccurrence == nil || currentSchedule.LastOccurrence.Status != "queued" {
		t.Fatalf("schedule audit state = %#v, want queued test reference", currentSchedule)
	}
	if currentSchedule.NextRunAt == nil || !currentSchedule.NextRunAt.After(now) {
		t.Fatalf("schedule NextRunAt = %v, want advanced recurrence", currentSchedule.NextRunAt)
	}
}

func TestScenarioSchedulerFailsPreparedTestWhenNextRunCalculationFails(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store, withUnfencedDeploymentLeaseStartForFaultInjection())
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID:       "script-invalid-next-run",
		Name:     "Invalid Next Run Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name:     "Invalid Next Run Scenario",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-invalid-next-run",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	now := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	schedule := &storage.ScenarioSchedule{
		ID:         "schedule-invalid-next-run",
		ScenarioID: scenario.ID,
		Name:       "Invalid Next Run",
		CronExpr:   "not a valid cron expression",
		Enabled:    true,
		NextRunAt:  &now,
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
	}
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("saveScenarioScheduleLocked returned error: %v", err)
	}
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules returned error: %v", err)
	}

	select {
	case testID := <-backend.started:
		t.Fatalf("test %q started despite next-run calculation failure", testID)
	default:
	}

	runner.mu.RLock()
	if len(runner.tests) != 1 {
		runner.mu.RUnlock()
		t.Fatalf("runner.tests length = %d, want 1 failed run", len(runner.tests))
	}
	var materialized *ManagedTest
	for _, test := range runner.tests {
		materialized = test
	}
	if materialized.Status != "FAILED" {
		runner.mu.RUnlock()
		t.Fatalf("materialized test status = %q, want FAILED", materialized.Status)
	}
	executionCtx := materialized.executionCtx
	runner.mu.RUnlock()
	if executionCtx == nil || executionCtx.Err() == nil {
		t.Fatal("failed scheduled run retained a live execution context")
	}

	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences returned error: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "failed" || occurrences[0].ReasonCode != "schedule_calculation_failed" {
		t.Fatalf("occurrences = %#v, want one schedule_calculation_failed occurrence", occurrences)
	}
}

func TestScenarioSchedulerRecoversMaterializingOccurrenceByPreassignedTestID(t *testing.T) {
	setup := func(t *testing.T, suffix string) (*metricsStoreSpy, *TestRunner, *scheduleExecutionBackend, *storage.ScenarioSchedule, time.Time) {
		t.Helper()
		store := &metricsStoreSpy{}
		runner := NewTestRunner(2, NewHub(), store, withUnfencedDeploymentLeaseStartForFaultInjection())
		backend := &scheduleExecutionBackend{started: make(chan string, 1)}
		runner.setExecutionBackend(backend)
		scriptID := "script-materializing-" + suffix
		runner.StoreScript(&dsl.Script{ID: scriptID, Name: "Scheduled", Protocol: "http", Version: "1.0.0", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Home", Type: "request", Target: "/"}}})
		scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Recovery " + suffix, Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}}})
		if err != nil {
			t.Fatalf("StoreScenario: %v", err)
		}
		now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
		due := now.Add(-time.Minute)
		schedule := &storage.ScenarioSchedule{ID: "schedule-materializing-" + suffix, ScenarioID: scenario.ID, Revision: 1, Name: "Recovery", CronExpr: "*/5 * * * *", Enabled: true, NextRunAt: &due, OwnerSubject: "scheduler-owner", OwnerDisplayName: "Scheduler Owner", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
		runner.mu.Lock()
		if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
			runner.mu.Unlock()
			t.Fatalf("saveScenarioScheduleLocked: %v", err)
		}
		runner.mu.Unlock()
		return store, runner, backend, schedule, now
	}

	t.Run("resume before test persistence", func(t *testing.T) {
		store, runner, backend, schedule, now := setup(t, "resume")
		reservedTestID := "preassigned-scheduled-test"
		if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
			ID: "occurrence-materializing-resume", ScheduleID: schedule.ID, ScenarioID: schedule.ScenarioID,
			ScheduleRevision: schedule.Revision, PlannedAt: schedule.NextRunAt.UTC(), Status: "materializing", ReasonCode: "due", TestID: reservedTestID,
			CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
		}
		scheduler := newScenarioScheduler(runner)
		scheduler.now = func() time.Time { return now }
		if err := scheduler.RunDueSchedules(); err != nil {
			t.Fatalf("RunDueSchedules: %v", err)
		}
		select {
		case startedID := <-backend.started:
			if startedID != reservedTestID {
				t.Fatalf("started test ID = %q, want preassigned %q", startedID, reservedTestID)
			}
		case <-time.After(time.Second):
			t.Fatal("resumed scheduled test did not start")
		}
		materialized, ok := runner.GetTest(reservedTestID)
		if !ok {
			t.Fatalf("materialized test %q not found", reservedTestID)
		}
		if materialized.CreatedBySubject != "scheduler-owner" || materialized.CreatedByDisplayName != "Scheduler Owner" || materialized.StartedBySubject != "scheduler-owner" || materialized.StartedByDisplayName != "Scheduler Owner" {
			t.Fatalf("scheduled attribution = created %q/%q started %q/%q, want schedule owner throughout start", materialized.CreatedBySubject, materialized.CreatedByDisplayName, materialized.StartedBySubject, materialized.StartedByDisplayName)
		}
		occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(occurrences) != 1 || occurrences[0].ID != "occurrence-materializing-resume" || occurrences[0].TestID != reservedTestID || occurrences[0].Status != "materialized" {
			t.Fatalf("resumed occurrence = %#v", occurrences)
		}
	})

	t.Run("resume after test persistence", func(t *testing.T) {
		store, runner, backend, schedule, now := setup(t, "persisted")
		reservedTestID := "already-persisted-scheduled-test"
		if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
			ID: "occurrence-materializing-persisted", ScheduleID: schedule.ID, ScenarioID: schedule.ScenarioID,
			ScheduleRevision: schedule.Revision, PlannedAt: schedule.NextRunAt.UTC(), Status: "materializing", ReasonCode: "due", TestID: reservedTestID,
			CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
		}
		recoveredRequest := CreateTestRequest{Name: "Recovered", ScenarioID: schedule.ScenarioID}
		recoveredRequest.projectQuotaEvidence = runner.loadCreateTestProjectQuotaEvidence(recoveredRequest)
		runner.mu.Lock()
		recovered, err := runner.buildManagedTestLocked(recoveredRequest, reservedTestID, "QUEUED")
		if err != nil {
			runner.mu.Unlock()
			t.Fatalf("buildManagedTestLocked: %v", err)
		}
		runner.tests[reservedTestID] = recovered
		runner.mu.Unlock()
		scheduler := newScenarioScheduler(runner)
		scheduler.now = func() time.Time { return now }
		if err := scheduler.RunDueSchedules(); err != nil {
			t.Fatalf("RunDueSchedules: %v", err)
		}
		select {
		case startedID := <-backend.started:
			if startedID != reservedTestID {
				t.Fatalf("recovery started test %q, want correlated %q", startedID, reservedTestID)
			}
		case <-time.After(time.Second):
			t.Fatal("correlated persisted test was not resumed")
		}
		occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(occurrences) != 1 || occurrences[0].TestID != reservedTestID || occurrences[0].Status != "materialized" || occurrences[0].ReasonCode != "recovered" {
			t.Fatalf("recovered occurrence = %#v", occurrences)
		}
		storedSchedules, ok := runner.ListScenarioSchedules(schedule.ScenarioID)
		if !ok || len(storedSchedules) != 1 || storedSchedules[0].NextRunAt == nil || !storedSchedules[0].NextRunAt.After(now) {
			t.Fatalf("stored schedule after recovery = %#v, want advanced next run", storedSchedules)
		}
	})
}

func TestScenarioSchedulerDoesNotPromoteMaterializedOccurrenceWhoseTestFailedBeforeLaunch(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(2, NewHub(), store, withUnfencedDeploymentLeaseStartForFaultInjection())
	backend := &scheduleExecutionBackend{started: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "script-materialized-failed", Name: "Scheduled", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Home", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Materialized crash", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-materialized-failed", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "schedule-materialized-failed", ScenarioID: scenario.ID, Revision: 1,
		Name: "Materialized crash", CronExpr: "*/5 * * * *", Enabled: true, NextRunAt: &due,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	testID := "test-materialized-failed"
	materializedAt := now.Add(-30 * time.Second)
	occurrence := &storage.ScenarioScheduleOccurrence{
		ID: "occurrence-materialized-failed", ScheduleID: schedule.ID, ScenarioID: scenario.ID,
		ScheduleRevision: schedule.Revision, PlannedAt: due, Status: "materialized", ReasonCode: "started",
		TestID: testID, MaterializedAt: &materializedAt, CreatedAt: due, UpdatedAt: materializedAt,
	}
	failedRequest := CreateTestRequest{Name: "Interrupted", ScenarioID: scenario.ID}
	failedRequest.projectQuotaEvidence = runner.loadCreateTestProjectQuotaEvidence(failedRequest)
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("save schedule: %v", err)
	}
	failed, err := runner.buildManagedTestLocked(failedRequest, testID, "FAILED")
	if err != nil {
		runner.mu.Unlock()
		t.Fatalf("build failed correlated test: %v", err)
	}
	failed.Error = "controller restarted before scheduled launch"
	runner.tests[testID] = failed
	runner.mu.Unlock()
	if err := store.SaveScenarioScheduleOccurrence(occurrence); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
	}

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	select {
	case started := <-backend.started:
		t.Fatalf("failed correlated test was launched: %s", started)
	default:
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences: %v", err)
	}
	if len(occurrences) != 1 || occurrences[0].Status != "failed" || occurrences[0].ReasonCode != "recovery_interrupted" || occurrences[0].MaterializedAt != nil {
		t.Fatalf("recovered occurrence = %+v, want failed/recovery_interrupted", occurrences)
	}
	stored, ok := runner.ListScenarioSchedules(scenario.ID)
	if !ok || len(stored) != 1 || stored[0].NextRequiredAction != "operator_decision_required" || stored[0].LastError == "" {
		if len(stored) == 1 {
			t.Fatalf("recovered schedule = %+v, want operator-visible failure", *stored[0])
		}
		t.Fatalf("recovered schedules = %+v, want one operator-visible failure", stored)
	}
}

func TestScenarioSchedulerRecoveryCatchUpAllRetainsBacklogAnchor(t *testing.T) {
	store := &metricsStoreSpy{}
	runner := NewTestRunner(20, NewHub(), store, withUnfencedDeploymentLeaseStartForFaultInjection())
	backend := &scheduleExecutionBackend{started: make(chan string, 16)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "script-recovered-catch-up-all", Name: "Catch up", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Home", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Recovered catch up", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-recovered-catch-up-all", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	plannedAt := now.Add(-20 * time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "schedule-recovered-catch-up-all", ScenarioID: scenario.ID, Revision: 1,
		Name: "Recovered catch up", CronExpr: "*/5 * * * *", Enabled: true, MissedRunPolicy: "catch_up_all",
		NextRunAt: &plannedAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	testID := "test-recovered-catch-up-all"
	materializedAt := plannedAt.Add(time.Minute)
	recoveredRequest := CreateTestRequest{Name: "Recovered", ScenarioID: scenario.ID}
	recoveredRequest.projectQuotaEvidence = runner.loadCreateTestProjectQuotaEvidence(recoveredRequest)
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
		runner.mu.Unlock()
		t.Fatalf("save schedule: %v", err)
	}
	recovered, err := runner.buildManagedTestLocked(recoveredRequest, testID, "RUNNING")
	if err != nil {
		runner.mu.Unlock()
		t.Fatalf("build recovered test: %v", err)
	}
	runner.tests[testID] = recovered
	runner.mu.Unlock()
	if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
		ID: "occurrence-recovered-catch-up-all", ScheduleID: schedule.ID, ScenarioID: scenario.ID,
		ScheduleRevision: schedule.Revision, PlannedAt: plannedAt, Status: "materialized", ReasonCode: "started",
		TestID: testID, MaterializedAt: &materializedAt, CreatedAt: plannedAt, UpdatedAt: materializedAt,
	}); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
	}

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil {
		t.Fatalf("ListScenarioScheduleOccurrences: %v", err)
	}
	if len(occurrences) < 2 {
		t.Fatalf("occurrences = %+v, want recovered row plus retained backlog materialization", occurrences)
	}
	stored, ok := runner.ListScenarioSchedules(scenario.ID)
	if !ok || len(stored) != 1 || stored[0].NextRunAt == nil || !stored[0].NextRunAt.After(now) {
		t.Fatalf("schedule = %+v, want catch_up_all to drain backlog through now", stored)
	}
}

func TestScenarioSchedulerRecoveryFailsPreparedTestAcrossPersistenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reasonCode string
		arm        func(*scenarioRecoveryFaultStore, *storage.ScenarioSchedule)
	}{
		{
			name:       "occurrence persistence",
			reasonCode: "recovery_occurrence_persist_failed",
			arm: func(store *scenarioRecoveryFaultStore, _ *storage.ScenarioSchedule) {
				store.failNextOccurrenceSave = true
			},
		},
		{
			name:       "next run calculation",
			reasonCode: "recovery_schedule_calculation_failed",
			arm: func(_ *scenarioRecoveryFaultStore, schedule *storage.ScenarioSchedule) {
				schedule.CronExpr = "not a cron expression"
			},
		},
		{
			name:       "schedule persistence",
			reasonCode: "recovery_schedule_persist_failed",
			arm: func(store *scenarioRecoveryFaultStore, _ *storage.ScenarioSchedule) {
				store.failNextScheduleSave = true
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &metricsStoreSpy{}
			store := &scenarioRecoveryFaultStore{metricsStoreSpy: base, err: errors.New("injected schedule store failure")}
			runner := NewTestRunner(2, NewHub(), store, withUnfencedDeploymentLeaseStartForFaultInjection())
			defer runner.beginShutdown()
			backend := &scheduleExecutionBackend{started: make(chan string, 1)}
			runner.setExecutionBackend(backend)
			scriptID := "script-recovery-boundary-" + tc.reasonCode
			runner.StoreScript(&dsl.Script{
				ID: scriptID, Name: "Recovery boundary", Protocol: "http", Version: "1.0.0",
				Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
				Actions: []dsl.Action{{Name: "Home", Type: "request", Target: "/"}},
			})
			scenario, err := runner.StoreScenario(CreateScenarioRequest{
				Name: "Recovery boundary", Duration: "1m",
				Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}},
			})
			if err != nil {
				t.Fatalf("StoreScenario: %v", err)
			}
			now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
			due := now.Add(-time.Minute)
			schedule := &storage.ScenarioSchedule{
				ID: "schedule-" + tc.reasonCode, ScenarioID: scenario.ID, Revision: 1,
				Name: "Recovery boundary", CronExpr: "*/5 * * * *", Enabled: true,
				NextRunAt: &due, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
			}
			testID := "test-" + tc.reasonCode
			occurrence := &storage.ScenarioScheduleOccurrence{
				ID: "occurrence-" + tc.reasonCode, ScheduleID: schedule.ID, ScenarioID: scenario.ID,
				ScheduleRevision: schedule.Revision, PlannedAt: due, Status: "materializing",
				ReasonCode: "due", TestID: testID, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
			}
			recoveredRequest := CreateTestRequest{Name: "Recovered", ScenarioID: scenario.ID}
			recoveredRequest.projectQuotaEvidence = runner.loadCreateTestProjectQuotaEvidence(recoveredRequest)
			runner.mu.Lock()
			if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
				runner.mu.Unlock()
				t.Fatalf("save schedule: %v", err)
			}
			recovered, err := runner.buildManagedTestLocked(recoveredRequest, testID, "QUEUED")
			if err != nil {
				runner.mu.Unlock()
				t.Fatalf("build recovered test: %v", err)
			}
			runner.tests[testID] = recovered
			if err := runner.saveManagedTestRecord(recovered); err != nil {
				runner.mu.Unlock()
				t.Fatalf("save recovered test: %v", err)
			}
			runner.mu.Unlock()
			if err := store.SaveScenarioScheduleOccurrence(occurrence); err != nil {
				t.Fatalf("seed occurrence: %v", err)
			}

			tc.arm(store, schedule)
			if tc.reasonCode == "recovery_schedule_calculation_failed" {
				runner.mu.Lock()
				if err := runner.saveScenarioScheduleLocked(schedule); err != nil {
					runner.mu.Unlock()
					t.Fatalf("save invalid schedule: %v", err)
				}
				runner.mu.Unlock()
			}
			scheduler := newScenarioScheduler(runner)
			scheduler.now = func() time.Time { return now }
			if err := scheduler.RunDueSchedules(); err != nil {
				t.Fatalf("RunDueSchedules: %v", err)
			}
			select {
			case started := <-backend.started:
				t.Fatalf("prepared test %q launched after recovery failure", started)
			default:
			}

			runner.mu.RLock()
			gotTest := runner.tests[testID]
			runner.mu.RUnlock()
			if gotTest == nil || gotTest.Status != "FAILED" {
				t.Fatalf("recovered test = %#v, want FAILED", gotTest)
			}
			if gotTest.executionCtx == nil || gotTest.executionCtx.Err() == nil {
				t.Fatal("failed recovered test retained a live execution context")
			}
			occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
			if err != nil {
				t.Fatalf("ListScenarioScheduleOccurrences: %v", err)
			}
			if len(occurrences) != 1 || occurrences[0].Status != "failed" || occurrences[0].ReasonCode != tc.reasonCode {
				t.Fatalf("occurrences = %#v, want one failed %s occurrence", occurrences, tc.reasonCode)
			}
			storedSchedules, ok := runner.ListScenarioSchedules(scenario.ID)
			if !ok || len(storedSchedules) != 1 || storedSchedules[0].NextRequiredAction != "operator_decision_required" {
				t.Fatalf("schedule after failed recovery = %#v, want operator decision", storedSchedules)
			}

			// A later sweep sees the terminal test/occurrence pair and must neither
			// resurrect it nor create another occurrence.
			if err := scheduler.RunDueSchedules(); err != nil {
				t.Fatalf("second RunDueSchedules: %v", err)
			}
			select {
			case started := <-backend.started:
				t.Fatalf("second recovery sweep launched %q", started)
			default:
			}
			occurrences, err = store.ListScenarioScheduleOccurrences(schedule.ID)
			if err != nil {
				t.Fatalf("second ListScenarioScheduleOccurrences: %v", err)
			}
			if len(occurrences) != 1 || occurrences[0].Status != "failed" {
				t.Fatalf("second-sweep occurrences = %#v", occurrences)
			}
		})
	}
}
