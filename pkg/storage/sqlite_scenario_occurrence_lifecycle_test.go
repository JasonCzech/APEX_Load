package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScenarioScheduleOccurrenceTestIDIndexInstalled(t *testing.T) {
	const indexName = "idx_scenario_schedule_occurrences_test_id"
	t.Run("fresh database", func(t *testing.T) {
		store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "fresh.db"))
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		assertSQLiteIndexExists(t, store, indexName)
	})
	t.Run("existing database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "existing.db")
		store, err := NewSQLiteStore(path)
		if err != nil {
			t.Fatalf("NewSQLiteStore(initial): %v", err)
		}
		if _, err := store.db.Exec(`DROP INDEX ` + indexName); err != nil {
			t.Fatalf("drop lifecycle lookup index: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close initial store: %v", err)
		}
		store, err = NewSQLiteStore(path)
		if err != nil {
			t.Fatalf("NewSQLiteStore(reopen): %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		assertSQLiteIndexExists(t, store, indexName)
	})
}

func assertSQLiteIndexExists(t *testing.T, store *SQLiteStore, indexName string) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, indexName).Scan(&count); err != nil {
		t.Fatalf("query index %q: %v", indexName, err)
	}
	if count != 1 {
		t.Fatalf("index %q count = %d, want 1", indexName, count)
	}
}

func TestScenarioScheduleOccurrenceLookupRejectsAmbiguousTestIdentity(t *testing.T) {
	store := newHAFencingStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const (
		scenarioID = "scenario-ambiguous-occurrence-test"
		scheduleID = "schedule-ambiguous-occurrence-test"
		testID     = "test-ambiguous-occurrence-test"
	)
	if err := store.SaveScenario(&Scenario{ID: scenarioID, Name: "Ambiguous occurrence", Duration: "1m", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.SaveTest(&TestRecord{
		ID: testID, Name: "Ambiguous occurrence", Status: "PENDING", ScenarioID: scenarioID,
		ProjectID: DefaultProjectID, Duration: int64(time.Minute), StartTime: now, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if err := store.SaveScenarioSchedule(&ScenarioSchedule{
		ID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID, Name: "Ambiguous occurrence",
		CronExpr: "* * * * *", Timezone: "UTC", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	for index := range 2 {
		plannedAt := now.Add(time.Duration(index) * time.Minute)
		if err := store.SaveScenarioScheduleOccurrence(&ScenarioScheduleOccurrence{
			ID: scheduleID + "-occurrence-" + string(rune('a'+index)), ScheduleID: scheduleID,
			ScenarioID: scenarioID, ProjectID: DefaultProjectID, PlannedAt: plannedAt,
			Status: "queued", TestID: testID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("SaveScenarioScheduleOccurrence(%d): %v", index, err)
		}
	}
	if occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(testID); err == nil || occurrence != nil {
		t.Fatalf("ambiguous lookup = (%#v, %v), want nil occurrence and error", occurrence, err)
	}
}

func TestMaterializedScenarioOccurrenceDemotedOnlyForPrelaunchAbort(t *testing.T) {
	tests := []struct {
		name                  string
		priorTestStatus       string
		wantOccurrenceStatus  string
		wantOccurrenceReason  string
		wantMaterializedAt    bool
		wantScheduleLastRunAt bool
		wantScheduleLastError bool
	}{
		{
			name: "initializing run never crossed backend boundary", priorTestStatus: "INITIALIZING",
			wantOccurrenceStatus: "failed", wantOccurrenceReason: "prelaunch_aborted",
			wantMaterializedAt: false, wantScheduleLastRunAt: false, wantScheduleLastError: true,
		},
		{
			name: "running lifecycle remains a real materialization", priorTestStatus: "RUNNING",
			wantOccurrenceStatus: "materialized", wantOccurrenceReason: "started",
			wantMaterializedAt: true, wantScheduleLastRunAt: true, wantScheduleLastError: false,
		},
	}
	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := newHAFencingStore(t)
			now := time.Date(2026, 8, 5, 17, index, 0, 0, time.UTC)
			suffix := string(rune('a' + index))
			scenarioID := "scenario-prelaunch-abort-" + suffix
			scheduleID := "schedule-prelaunch-abort-" + suffix
			occurrenceID := "occurrence-prelaunch-abort-" + suffix
			testID := "test-prelaunch-abort-" + suffix
			if err := store.SaveScenario(&Scenario{
				ID: scenarioID, Name: "Prelaunch abort", Duration: "1m", CreatedAt: now, UpdatedAt: now,
			}, nil); err != nil {
				t.Fatalf("SaveScenario: %v", err)
			}
			record := &TestRecord{
				ID: testID, Name: "Prelaunch abort", Status: testCase.priorTestStatus,
				ScenarioID: scenarioID, ProjectID: DefaultProjectID, Duration: int64(time.Minute),
				StartTime: now, Groups: []any{},
			}
			if err := store.SaveTest(record); err != nil {
				t.Fatalf("SaveTest(initial): %v", err)
			}
			nextRunAt := now.Add(5 * time.Minute)
			if err := store.SaveScenarioSchedule(&ScenarioSchedule{
				ID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID, Revision: 1,
				Name: "Prelaunch abort", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true,
				LastRunTestID: testID, LastRunAt: &now, NextRunAt: &nextRunAt,
				CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			}); err != nil {
				t.Fatalf("SaveScenarioSchedule: %v", err)
			}
			if err := store.SaveScenarioScheduleOccurrence(&ScenarioScheduleOccurrence{
				ID: occurrenceID, ScheduleID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID,
				ScheduleRevision: 1, PlannedAt: now, MaterializedAt: &now, Status: "materialized",
				ReasonCode: "started", TestID: testID, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
			}

			endedAt := now.Add(time.Second)
			record.Status = "ABORTED"
			record.EndTime = &endedAt
			record.Error = "test cancelled before start"
			if err := store.SaveTest(record); err != nil {
				t.Fatalf("SaveTest(ABORTED): %v", err)
			}

			occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(testID)
			if err != nil || occurrence == nil {
				t.Fatalf("GetScenarioScheduleOccurrenceByTestID = (%#v, %v)", occurrence, err)
			}
			if occurrence.Status != testCase.wantOccurrenceStatus || occurrence.ReasonCode != testCase.wantOccurrenceReason ||
				(occurrence.MaterializedAt != nil) != testCase.wantMaterializedAt {
				t.Fatalf("occurrence after abort = %#v", occurrence)
			}
			schedule, err := store.GetScenarioSchedule(scheduleID)
			if err != nil {
				t.Fatalf("GetScenarioSchedule: %v", err)
			}
			if schedule.LastRunTestID != testID || (schedule.LastRunAt != nil) != testCase.wantScheduleLastRunAt ||
				(strings.TrimSpace(schedule.LastError) != "") != testCase.wantScheduleLastError {
				t.Fatalf("schedule after abort = %#v", schedule)
			}
		})
	}
}

func TestDeletingQueuedScheduledTestTerminalizesOccurrence(t *testing.T) {
	tests := []struct {
		name       string
		deleteTest func(t *testing.T, store *SQLiteStore, testID string)
	}{
		{
			name: "direct delete",
			deleteTest: func(t *testing.T, store *SQLiteStore, testID string) {
				t.Helper()
				if err := store.DeleteTest(testID); err != nil {
					t.Fatalf("DeleteTest: %v", err)
				}
			},
		},
		{
			name: "execution lease fenced delete",
			deleteTest: func(t *testing.T, store *SQLiteStore, testID string) {
				t.Helper()
				const owner = "controller/delete-scheduled-test"
				if acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, owner, time.Now().UTC(), time.Minute); err != nil || !acquired {
					t.Fatalf("TryAcquireSchedulerLease = (%v, %v), want (true, nil)", acquired, err)
				}
				result, err := store.DeleteTestIfLeaseHeld(testID, testExecutionLeaseName, owner)
				if err != nil || result != LeaseFencedDeleteDeleted {
					t.Fatalf("DeleteTestIfLeaseHeld = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteDeleted)
				}
			},
		},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := newHAFencingStore(t)
			now := time.Date(2026, 8, 5, 18, index, 0, 0, time.UTC)
			suffix := string(rune('a' + index))
			scenarioID := "scenario-delete-queued-" + suffix
			scheduleID := "schedule-delete-queued-" + suffix
			occurrenceID := "occurrence-delete-queued-" + suffix
			testID := "test-delete-queued-" + suffix

			if err := store.SaveScenario(&Scenario{
				ID: scenarioID, Name: "Delete queued occurrence", Duration: "1m", CreatedAt: now, UpdatedAt: now,
			}, nil); err != nil {
				t.Fatalf("SaveScenario: %v", err)
			}
			if err := store.SaveTest(&TestRecord{
				ID: testID, Name: "Delete queued occurrence", Status: "QUEUED", ScenarioID: scenarioID,
				ProjectID: DefaultProjectID, Duration: int64(time.Minute), StartTime: now, Groups: []any{},
			}); err != nil {
				t.Fatalf("SaveTest: %v", err)
			}
			nextRunAt := now.Add(5 * time.Minute)
			if err := store.SaveScenarioSchedule(&ScenarioSchedule{
				ID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID, Revision: 1,
				Name: "Delete queued occurrence", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true,
				LastRunTestID: testID, NextRunAt: &nextRunAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			}); err != nil {
				t.Fatalf("SaveScenarioSchedule: %v", err)
			}
			if err := store.SaveScenarioScheduleOccurrence(&ScenarioScheduleOccurrence{
				ID: occurrenceID, ScheduleID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID,
				ScheduleRevision: 1, PlannedAt: now, Status: "queued", ReasonCode: "start_retrying",
				TestID: testID, ConflictSummary: "provider temporarily unavailable", CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
			}

			testCase.deleteTest(t, store, testID)

			if _, err := store.GetTest(testID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("GetTest after delete error = %v, want sql.ErrNoRows", err)
			}
			occurrence, err := store.GetScenarioScheduleOccurrence(occurrenceID)
			if err != nil {
				t.Fatalf("GetScenarioScheduleOccurrence: %v", err)
			}
			if occurrence.Status != "failed" || occurrence.ReasonCode != scenarioScheduleOccurrenceTestDeletedReason ||
				occurrence.TestID != "" || occurrence.MaterializedAt != nil ||
				occurrence.ConflictSummary != scenarioScheduleOccurrenceTestDeletedError ||
				!strings.HasPrefix(occurrence.TransitionToken, "test-deleted:"+testID+":") {
				t.Fatalf("occurrence after test deletion = %#v", occurrence)
			}
			schedule, err := store.GetScenarioSchedule(scheduleID)
			if err != nil {
				t.Fatalf("GetScenarioSchedule: %v", err)
			}
			if schedule.LastRunTestID != "" || schedule.LastRunAt != nil || schedule.LastError != scenarioScheduleOccurrenceTestDeletedError ||
				schedule.NextRequiredAction != "operator_decision_required" || schedule.NextRunAt == nil || !schedule.NextRunAt.Equal(nextRunAt) {
				t.Fatalf("schedule after test deletion = %#v", schedule)
			}
		})
	}
}

func TestDeletingScheduledTestRejectsAmbiguousOccurrenceCorrelation(t *testing.T) {
	store := newHAFencingStore(t)
	now := time.Date(2026, 8, 5, 19, 0, 0, 0, time.UTC)
	const (
		scenarioID = "scenario-delete-ambiguous"
		scheduleID = "schedule-delete-ambiguous"
		testID     = "test-delete-ambiguous"
	)
	if err := store.SaveScenario(&Scenario{
		ID: scenarioID, Name: "Delete ambiguous occurrence", Duration: "1m", CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.SaveTest(&TestRecord{
		ID: testID, Name: "Delete ambiguous occurrence", Status: "QUEUED", ScenarioID: scenarioID,
		ProjectID: DefaultProjectID, Duration: int64(time.Minute), StartTime: now, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if err := store.SaveScenarioSchedule(&ScenarioSchedule{
		ID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID, Revision: 1,
		Name: "Delete ambiguous occurrence", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true,
		LastRunTestID: testID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	for index := range 2 {
		if err := store.SaveScenarioScheduleOccurrence(&ScenarioScheduleOccurrence{
			ID: scheduleID + "-occurrence-" + string(rune('a'+index)), ScheduleID: scheduleID,
			ScenarioID: scenarioID, ProjectID: DefaultProjectID, ScheduleRevision: 1,
			PlannedAt: now.Add(time.Duration(index) * time.Minute), Status: "queued", ReasonCode: "start_retrying",
			TestID: testID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("SaveScenarioScheduleOccurrence(%d): %v", index, err)
		}
	}

	err := store.DeleteTest(testID)
	if err == nil || !strings.Contains(err.Error(), "correlated to 2 scenario schedule occurrences") {
		t.Fatalf("DeleteTest error = %v, want ambiguous-correlation failure", err)
	}
	if test, err := store.GetTest(testID); err != nil || test.Status != "QUEUED" {
		t.Fatalf("test after rejected delete = (%#v, %v), want retained QUEUED test", test, err)
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(scheduleID)
	if err != nil || len(occurrences) != 2 {
		t.Fatalf("ListScenarioScheduleOccurrences = (%#v, %v), want two rows", occurrences, err)
	}
	for _, occurrence := range occurrences {
		if occurrence.TestID != testID || occurrence.Status != "queued" || occurrence.ReasonCode != "start_retrying" {
			t.Fatalf("ambiguous occurrence changed despite rollback: %#v", occurrence)
		}
	}
}

func TestQueuedScenarioOccurrencePromotionIsExecutionLeaseFenced(t *testing.T) {
	store := newHAFencingStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const (
		scenarioID   = "scenario-pending-promotion-fence"
		scheduleID   = "schedule-pending-promotion-fence"
		occurrenceID = "occurrence-pending-promotion-fence"
		testID       = "test-pending-promotion-fence"
		oldOwner     = "controller-old/pending-promotion"
		newOwner     = "controller-new/pending-promotion"
	)
	if err := store.SaveScenario(&Scenario{
		ID: scenarioID, Name: "Pending promotion fence", Duration: "2m", CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	plannedAt := now.Add(-time.Minute)
	nextRunAt := now.Add(time.Minute)
	record := &TestRecord{
		ID: testID, Name: "Pending promotion fence", Status: "PENDING",
		ScenarioID: scenarioID, ProjectID: DefaultProjectID, TotalVUsers: 1,
		Duration: int64(2 * time.Minute), StartTime: plannedAt, Groups: []any{},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if err := store.SaveScenarioSchedule(&ScenarioSchedule{
		ID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID, Revision: 1,
		Name: "Pending promotion fence", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true,
		LastRunTestID: testID, NextRunAt: &nextRunAt, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	if err := store.SaveScenarioScheduleOccurrence(&ScenarioScheduleOccurrence{
		ID: occurrenceID, ScheduleID: scheduleID, ScenarioID: scenarioID, ProjectID: DefaultProjectID,
		ScheduleRevision: 1, PlannedAt: plannedAt, Status: "queued", ReasonCode: "quota_pending",
		TestID: testID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
	}

	if acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, oldOwner, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire old execution lease = (%v, %v), want (true, nil)", acquired, err)
	}
	if err := store.ReleaseSchedulerLease(testExecutionLeaseName, oldOwner); err != nil {
		t.Fatalf("release old execution lease: %v", err)
	}
	if acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, newOwner, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire successor execution lease = (%v, %v), want (true, nil)", acquired, err)
	}

	start := *record
	start.Status = "INITIALIZING"
	saved, err := store.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(
		&start, testExecutionLeaseName, oldOwner, DefaultDeploymentLeaseName, "", "",
	)
	if err != nil {
		t.Fatalf("stale promotion: %v", err)
	}
	if saved {
		t.Fatal("stale execution leader promoted the pending test")
	}
	assertQueuedScenarioOccurrenceLifecycle(t, store, testID, scheduleID)

	saved, err = store.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(
		&start, testExecutionLeaseName, newOwner, DefaultDeploymentLeaseName, "", "",
	)
	if err != nil || !saved {
		t.Fatalf("successor promotion = (%v, %v), want (true, nil)", saved, err)
	}
	durableTest, err := store.GetTest(testID)
	if err != nil {
		t.Fatalf("GetTest after successor promotion: %v", err)
	}
	if durableTest.Status != "INITIALIZING" {
		t.Fatalf("successor test status = %q, want INITIALIZING", durableTest.Status)
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID after successor promotion: %v", err)
	}
	if occurrence == nil || occurrence.ID != occurrenceID || occurrence.Status != "materialized" || occurrence.ReasonCode != "started" || occurrence.MaterializedAt == nil {
		t.Fatalf("successor occurrence = %#v, want exact materialized/started occurrence", occurrence)
	}
	schedule, err := store.GetScenarioSchedule(scheduleID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule after successor promotion: %v", err)
	}
	if schedule.LastRunTestID != testID || schedule.LastRunAt == nil || !schedule.LastRunAt.Equal(*occurrence.MaterializedAt) {
		t.Fatalf("successor schedule lifecycle = test %q at %v, want %q at %v", schedule.LastRunTestID, schedule.LastRunAt, testID, occurrence.MaterializedAt)
	}
}

func assertQueuedScenarioOccurrenceLifecycle(t *testing.T, store *SQLiteStore, testID, scheduleID string) {
	t.Helper()
	durableTest, err := store.GetTest(testID)
	if err != nil {
		t.Fatalf("GetTest after stale promotion: %v", err)
	}
	if durableTest.Status != "PENDING" {
		t.Fatalf("stale promotion changed test status to %q, want PENDING", durableTest.Status)
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByTestID after stale promotion: %v", err)
	}
	if occurrence == nil || occurrence.Status != "queued" || occurrence.ReasonCode != "quota_pending" || occurrence.MaterializedAt != nil {
		t.Fatalf("stale promotion changed occurrence: %#v", occurrence)
	}
	schedule, err := store.GetScenarioSchedule(scheduleID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule after stale promotion: %v", err)
	}
	if schedule.LastRunTestID != testID || schedule.LastRunAt != nil {
		t.Fatalf("stale promotion changed schedule lifecycle = test %q at %v", schedule.LastRunTestID, schedule.LastRunAt)
	}
}
