package storage

import (
	"path/filepath"
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
