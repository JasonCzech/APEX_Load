package storage

import (
	"path/filepath"
	"testing"
	"time"
)

const testExecutionLeaseName = "controller-execution-leader"

func newHAFencingStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteStorePersistsTestStartDelay(t *testing.T) {
	store := newHAFencingStore(t)
	record := &TestRecord{
		ID:         "test-start-delay",
		Name:       "Delayed start",
		Status:     "QUEUED",
		StartDelay: "45s",
		StartTime:  time.Now().UTC(),
		Groups:     []any{},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	got, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if got.StartDelay != record.StartDelay {
		t.Fatalf("StartDelay = %q, want %q", got.StartDelay, record.StartDelay)
	}
}

func TestSQLiteStoreEnsureTestRefKeyDoesNotOverwriteLifecycle(t *testing.T) {
	store := newHAFencingStore(t)
	now := time.Now().UTC()
	record := &TestRecord{
		ID:        "test-ref-key-backfill",
		Name:      "Legacy run",
		Status:    "RUNNING",
		StartTime: now,
		Groups:    []any{},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	durableRefKey, err := store.EnsureTestRefKey(record.ID, "RUN-LEGACY-000001")
	if err != nil {
		t.Fatalf("EnsureTestRefKey: %v", err)
	}
	if durableRefKey != "RUN-LEGACY-000001" {
		t.Fatalf("durable ref key = %q, want RUN-LEGACY-000001", durableRefKey)
	}

	// A concurrent replica that proposed another key must adopt the durable
	// winner instead of replacing it.
	durableRefKey, err = store.EnsureTestRefKey(record.ID, "RUN-LEGACY-000002")
	if err != nil {
		t.Fatalf("second EnsureTestRefKey: %v", err)
	}
	if durableRefKey != "RUN-LEGACY-000001" {
		t.Fatalf("second durable ref key = %q, want first writer's key", durableRefKey)
	}
	got, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if got.Status != "RUNNING" || !got.StartTime.Equal(now) || got.EndTime != nil || got.Error != "" {
		t.Fatalf("reference-key backfill overwrote lifecycle fields: %#v", got)
	}
}

func TestSQLiteStoreMigratesLegacyTestsStartDelayColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if _, err := store.db.Exec(`ALTER TABLE tests DROP COLUMN start_delay`); err != nil {
		_ = store.Close()
		t.Fatalf("remove start_delay to simulate legacy schema: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer reopened.Close()
	record := &TestRecord{
		ID:         "test-migrated-start-delay",
		Name:       "Migrated delay",
		Status:     "QUEUED",
		StartDelay: "750ms",
		StartTime:  time.Now().UTC(),
		Groups:     []any{},
	}
	if err := reopened.SaveTest(record); err != nil {
		t.Fatalf("SaveTest after migration: %v", err)
	}
	got, err := reopened.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest after migration: %v", err)
	}
	if got.StartDelay != record.StartDelay {
		t.Fatalf("migrated StartDelay = %q, want %q", got.StartDelay, record.StartDelay)
	}
}

func TestSQLiteStoreFencesLifecycleAndFullSaveByLeaseEpoch(t *testing.T) {
	store := newHAFencingStore(t)
	oldToken := "controller-a/epoch-1"
	newToken := "controller-b/epoch-1"
	acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, oldToken, time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire old epoch = (%v, %v), want (true, nil)", acquired, err)
	}
	record := &TestRecord{
		ID:        "test-fenced-lifecycle",
		Name:      "Fenced lifecycle",
		Status:    "RUNNING",
		StartTime: time.Now().UTC(),
		Groups:    []any{},
	}
	saved, err := store.SaveTestIfLeaseHeld(record, testExecutionLeaseName, oldToken)
	if err != nil || !saved {
		t.Fatalf("SaveTestIfLeaseHeld old epoch = (%v, %v), want (true, nil)", saved, err)
	}
	if err := store.ReleaseSchedulerLease(testExecutionLeaseName, oldToken); err != nil {
		t.Fatalf("release old epoch: %v", err)
	}
	acquired, err = store.TryAcquireSchedulerLease(testExecutionLeaseName, newToken, time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire successor epoch = (%v, %v), want (true, nil)", acquired, err)
	}

	record.Status = "ABORTED"
	saved, err = store.SaveTestIfLeaseHeld(record, testExecutionLeaseName, oldToken)
	if err != nil {
		t.Fatalf("stale SaveTestIfLeaseHeld: %v", err)
	}
	if saved {
		t.Fatal("stale full-row save succeeded after lease takeover")
	}
	endedAt := time.Now().UTC()
	result, err := store.UpdateTestStatusIfActiveAndLeaseHeld(record.ID, "FAILED", &endedAt, "late callback", testExecutionLeaseName, oldToken)
	if err != nil {
		t.Fatalf("stale lifecycle transition: %v", err)
	}
	if result != TestStatusTransitionFenced {
		t.Fatalf("stale lifecycle result = %q, want %q", result, TestStatusTransitionFenced)
	}
	got, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest after stale writes: %v", err)
	}
	if got.Status != "RUNNING" || got.EndTime != nil || got.Error != "" {
		t.Fatalf("stale epoch changed durable row: %#v", got)
	}

	result, err = store.UpdateTestStatusIfActiveAndLeaseHeld(record.ID, "COMPLETE", &endedAt, "", testExecutionLeaseName, newToken)
	if err != nil || result != TestStatusTransitionUpdated {
		t.Fatalf("successor lifecycle transition = (%q, %v), want (%q, nil)", result, err, TestStatusTransitionUpdated)
	}
	result, err = store.UpdateTestStatusIfActiveAndLeaseHeld("missing-test", "FAILED", &endedAt, "", testExecutionLeaseName, newToken)
	if err != nil || result != TestStatusTransitionMissing {
		t.Fatalf("missing lifecycle transition = (%q, %v), want (%q, nil)", result, err, TestStatusTransitionMissing)
	}
}

func TestSQLiteStoreFencesScheduleAndOccurrenceWritesByLeaseEpoch(t *testing.T) {
	store := newHAFencingStore(t)
	now := time.Now().UTC()
	if err := store.SaveScenario(&Scenario{
		ID:        "scenario-fenced-schedule",
		Name:      "Fenced schedule",
		Duration:  "1m",
		CreatedAt: now,
		UpdatedAt: now,
	}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}

	oldToken := "controller-a/schedule-epoch"
	newToken := "controller-b/schedule-epoch"
	acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, oldToken, now, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire old epoch = (%v, %v), want (true, nil)", acquired, err)
	}
	nextRun := now.Add(time.Hour)
	schedule := &ScenarioSchedule{
		ID:         "schedule-fenced-epoch",
		ScenarioID: "scenario-fenced-schedule",
		ProjectID:  DefaultProjectID,
		Name:       "Fenced schedule",
		CronExpr:   "0 * * * *",
		Timezone:   "UTC",
		Enabled:    true,
		NextRunAt:  &nextRun,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	created, err := store.CreateScenarioScheduleIfAbsentAndLeaseHeld(schedule, testExecutionLeaseName, oldToken)
	if err != nil || !created {
		t.Fatalf("create fenced schedule = (%v, %v), want (true, nil)", created, err)
	}
	occurrence := &ScenarioScheduleOccurrence{
		ID:               "occurrence-fenced-epoch",
		ScheduleID:       schedule.ID,
		ScenarioID:       schedule.ScenarioID,
		ProjectID:        DefaultProjectID,
		ScheduleRevision: schedule.Revision,
		PlannedAt:        nextRun,
		Status:           "materializing",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	saved, err := store.SaveScenarioScheduleOccurrenceIfLeaseHeld(occurrence, testExecutionLeaseName, oldToken)
	if err != nil || !saved {
		t.Fatalf("save fenced occurrence = (%v, %v), want (true, nil)", saved, err)
	}

	if err := store.ReleaseSchedulerLease(testExecutionLeaseName, oldToken); err != nil {
		t.Fatalf("release old epoch: %v", err)
	}
	acquired, err = store.TryAcquireSchedulerLease(testExecutionLeaseName, newToken, now, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire successor epoch = (%v, %v), want (true, nil)", acquired, err)
	}

	schedule.LastError = "stale scheduler callback"
	updated, err := store.UpdateScenarioScheduleIfRevisionAndLeaseHeld(schedule, 1, testExecutionLeaseName, oldToken)
	if err != nil {
		t.Fatalf("stale schedule update: %v", err)
	}
	if updated {
		t.Fatal("stale epoch advanced scenario schedule")
	}
	occurrence.Status = "failed"
	occurrence.ReasonCode = "stale_callback"
	saved, err = store.SaveScenarioScheduleOccurrenceIfLeaseHeld(occurrence, testExecutionLeaseName, oldToken)
	if err != nil {
		t.Fatalf("stale occurrence update: %v", err)
	}
	if saved {
		t.Fatal("stale epoch changed scenario schedule occurrence")
	}

	durableSchedule, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if durableSchedule.Revision != 1 || durableSchedule.LastError != "" {
		t.Fatalf("stale epoch changed durable schedule: %#v", durableSchedule)
	}
	durableOccurrence, err := store.GetScenarioScheduleOccurrence(occurrence.ID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleOccurrence: %v", err)
	}
	if durableOccurrence.Status != "materializing" || durableOccurrence.ReasonCode != "" {
		t.Fatalf("stale epoch changed durable occurrence: %#v", durableOccurrence)
	}

	schedule.LastError = "successor update"
	updated, err = store.UpdateScenarioScheduleIfRevisionAndLeaseHeld(schedule, 1, testExecutionLeaseName, newToken)
	if err != nil || !updated {
		t.Fatalf("successor schedule update = (%v, %v), want (true, nil)", updated, err)
	}
	occurrence.Status = "materialized"
	occurrence.ReasonCode = "started"
	saved, err = store.SaveScenarioScheduleOccurrenceIfLeaseHeld(occurrence, testExecutionLeaseName, newToken)
	if err != nil || !saved {
		t.Fatalf("successor occurrence update = (%v, %v), want (true, nil)", saved, err)
	}
}

func TestSQLiteStoreRejectsSubsecondExpiredExecutionLease(t *testing.T) {
	store := newHAFencingStore(t)
	token := "controller-a/subsecond"
	acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, token, time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire lease = (%v, %v), want (true, nil)", acquired, err)
	}
	record := &TestRecord{
		ID:        "test-subsecond-expiry",
		Name:      "Subsecond expiry",
		Status:    "RUNNING",
		StartTime: time.Now().UTC(),
		Groups:    []any{},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest seed: %v", err)
	}

	// Place expiry earlier in the current second. A whole-epoch-seconds clock
	// would still consider this lease live until the next second boundary.
	for {
		fraction := time.Now().Nanosecond()
		if fraction >= int(400*time.Millisecond) && fraction <= int(700*time.Millisecond) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	expiredAt := time.Now().UTC().Truncate(time.Second).Add(200 * time.Millisecond)
	if _, err := store.db.Exec(`UPDATE scheduler_leases SET expires_at = ? WHERE name = ?`, expiredAt, testExecutionLeaseName); err != nil {
		t.Fatalf("expire lease within current second: %v", err)
	}

	record.Status = "ABORTED"
	saved, err := store.SaveTestIfLeaseHeld(record, testExecutionLeaseName, token)
	if err != nil {
		t.Fatalf("SaveTestIfLeaseHeld: %v", err)
	}
	if saved {
		t.Fatal("full-row save succeeded after subsecond lease expiry")
	}
	endedAt := time.Now().UTC()
	result, err := store.UpdateTestStatusIfActiveAndLeaseHeld(record.ID, "FAILED", &endedAt, "late", testExecutionLeaseName, token)
	if err != nil {
		t.Fatalf("UpdateTestStatusIfActiveAndLeaseHeld: %v", err)
	}
	if result != TestStatusTransitionFenced {
		t.Fatalf("status result = %q, want %q", result, TestStatusTransitionFenced)
	}
	got, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if got.Status != "RUNNING" {
		t.Fatalf("expired epoch changed status to %q", got.Status)
	}
}

func TestSQLiteStoreSchedulerLeaseAcquisitionPreservesSubsecondTTL(t *testing.T) {
	store := newHAFencingStore(t)
	// Acquire away from a whole-second boundary so the former unixepoch-based
	// implementation deterministically exposed its truncation.
	for {
		fraction := time.Now().Nanosecond()
		if fraction >= int(400*time.Millisecond) && fraction <= int(650*time.Millisecond) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	ttl := 1500 * time.Millisecond
	beforeAcquire := time.Now().UTC()
	acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, "controller/subsecond-acquire", beforeAcquire, ttl)
	if err != nil || !acquired {
		t.Fatalf("TryAcquireSchedulerLease = (%v, %v), want (true, nil)", acquired, err)
	}
	lease, err := store.GetSchedulerLease(testExecutionLeaseName)
	if err != nil {
		t.Fatalf("GetSchedulerLease: %v", err)
	}
	if lease.AcquiredAt.Before(beforeAcquire.Add(-100 * time.Millisecond)) {
		t.Fatalf("database acquisition time was truncated: acquired_at=%s before=%s", lease.AcquiredAt, beforeAcquire)
	}
	if lease.AcquiredAt.Nanosecond() == 0 {
		t.Fatalf("database acquisition time lost subsecond precision: %s", lease.AcquiredAt)
	}
	if got := lease.ExpiresAt.Sub(lease.AcquiredAt); got != ttl {
		t.Fatalf("stored lease TTL = %s, want %s", got, ttl)
	}
}

func TestSQLiteStoreFencesCapacityBindingByLeaseEpoch(t *testing.T) {
	store := newHAFencingStore(t)
	oldToken := "controller-a/epoch-1"
	newToken := "controller-b/epoch-1"
	acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, newToken, time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire successor epoch = (%v, %v), want (true, nil)", acquired, err)
	}
	now := time.Now().UTC()
	reservation := &CapacityReservation{
		ID:            "reservation-fenced-bind",
		ProjectID:     DefaultProjectID,
		StartAt:       now.Add(time.Hour),
		EndAt:         now.Add(2 * time.Hour),
		ApprovalState: "approved",
	}
	if err := store.SaveCapacityReservation(reservation); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	testID, status := "test-bound", "QUEUED"
	scheduledStart := now.Add(70 * time.Minute)
	if err := store.SaveTest(&TestRecord{
		ID: testID, Name: testID, Status: status, ProjectID: DefaultProjectID,
		StartTime: now, ScheduledStartAt: &scheduledStart, Duration: int64(5 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	bound, err := store.UpdateCapacityReservationBindingIfLeaseHeld(reservation.ID, &testID, &status, now, testExecutionLeaseName, oldToken)
	if err != nil {
		t.Fatalf("stale fenced bind: %v", err)
	}
	if bound {
		t.Fatal("stale epoch bound a capacity reservation")
	}
	got, err := store.GetCapacityReservation(reservation.ID)
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if got.BoundTestID != nil {
		t.Fatalf("stale bind persisted bound_test_id = %q", *got.BoundTestID)
	}
	bound, err = store.UpdateCapacityReservationBindingIfLeaseHeld(reservation.ID, &testID, &status, now, testExecutionLeaseName, newToken)
	if err != nil || !bound {
		t.Fatalf("successor fenced bind = (%v, %v), want (true, nil)", bound, err)
	}
	boundTest, err := store.GetTest(testID)
	if err != nil || boundTest.TimeslotID == nil || *boundTest.TimeslotID != reservation.ID {
		t.Fatalf("successor test-side binding = (%#v, %v)", boundTest, err)
	}
}

func TestSQLiteStoreLeaseFencedTestDeleteRejectsTakeoverAndActiveRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shared-delete-test.db")
	oldStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(old): %v", err)
	}
	defer oldStore.Close()
	newStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(new): %v", err)
	}
	defer newStore.Close()

	now := time.Now().UTC()
	record := &TestRecord{
		ID:        "lease-fenced-delete-test",
		Name:      "Lease fenced delete",
		Status:    "COMPLETE",
		ProjectID: DefaultProjectID,
		StartTime: now,
		Groups:    []any{},
	}
	if err := oldStore.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	oldToken := "delete-test-old/1"
	if acquired, err := oldStore.TryAcquireSchedulerLease(testExecutionLeaseName, oldToken, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire old lease = (%v, %v), want (true, nil)", acquired, err)
	}
	// Model the old controller's replica-local precheck before it is descheduled.
	if got, err := oldStore.GetTest(record.ID); err != nil || got.Status != "COMPLETE" {
		t.Fatalf("old precheck = (%#v, %v), want terminal test", got, err)
	}

	if err := oldStore.ReleaseSchedulerLease(testExecutionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newToken := "delete-test-new/1"
	if acquired, err := newStore.TryAcquireSchedulerLease(testExecutionLeaseName, newToken, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire successor lease = (%v, %v), want (true, nil)", acquired, err)
	}
	record.Status = "RUNNING"
	if saved, err := newStore.SaveTestIfLeaseHeld(record, testExecutionLeaseName, newToken); err != nil || !saved {
		t.Fatalf("successor start = (%v, %v), want (true, nil)", saved, err)
	}

	result, err := oldStore.DeleteTestIfLeaseHeld(record.ID, testExecutionLeaseName, oldToken)
	if err != nil || result != LeaseFencedDeleteFenced {
		t.Fatalf("stale delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteFenced)
	}
	if got, err := newStore.GetTest(record.ID); err != nil || got.Status != "RUNNING" {
		t.Fatalf("stale delete changed successor run = (%#v, %v)", got, err)
	}
	result, err = newStore.DeleteTestIfLeaseHeld(record.ID, testExecutionLeaseName, newToken)
	if err != nil || result != LeaseFencedDeleteActive {
		t.Fatalf("active delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteActive)
	}

	record.Status = "COMPLETE"
	if saved, err := newStore.SaveTestIfLeaseHeld(record, testExecutionLeaseName, newToken); err != nil || !saved {
		t.Fatalf("successor complete = (%v, %v), want (true, nil)", saved, err)
	}
	result, err = newStore.DeleteTestIfLeaseHeld(record.ID, testExecutionLeaseName, newToken)
	if err != nil || result != LeaseFencedDeleteDeleted {
		t.Fatalf("terminal delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteDeleted)
	}
	result, err = newStore.DeleteTestIfLeaseHeld(record.ID, testExecutionLeaseName, newToken)
	if err != nil || result != LeaseFencedDeleteMissing {
		t.Fatalf("missing delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteMissing)
	}
}

func TestSQLiteStoreLeaseFencedProjectDeleteRejectsTakeoverAndActiveRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shared-delete-project.db")
	oldStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(old): %v", err)
	}
	defer oldStore.Close()
	newStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(new): %v", err)
	}
	defer newStore.Close()

	now := time.Now().UTC()
	projectID := "lease-fenced-delete-project"
	if err := oldStore.SaveProject(&Project{ID: projectID, Name: "Lease fenced project", CreatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	record := &TestRecord{
		ID:        "lease-fenced-project-test",
		Name:      "Project run",
		Status:    "COMPLETE",
		ProjectID: projectID,
		StartTime: now,
		Groups:    []any{},
	}
	if err := oldStore.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	oldToken := "delete-project-old/1"
	if acquired, err := oldStore.TryAcquireSchedulerLease(testExecutionLeaseName, oldToken, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire old lease = (%v, %v), want (true, nil)", acquired, err)
	}
	// Model the old handler's project/test enumeration before the takeover.
	if _, err := oldStore.GetProject(projectID); err != nil {
		t.Fatalf("old project precheck: %v", err)
	}
	if tests, err := oldStore.ListTests(projectID, nil); err != nil || len(tests) != 1 || tests[0].Status != "COMPLETE" {
		t.Fatalf("old test precheck = (%#v, %v), want one terminal test", tests, err)
	}

	if err := oldStore.ReleaseSchedulerLease(testExecutionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newToken := "delete-project-new/1"
	if acquired, err := newStore.TryAcquireSchedulerLease(testExecutionLeaseName, newToken, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire successor lease = (%v, %v), want (true, nil)", acquired, err)
	}
	record.Status = "INITIALIZING"
	if saved, err := newStore.SaveTestIfLeaseHeld(record, testExecutionLeaseName, newToken); err != nil || !saved {
		t.Fatalf("successor start = (%v, %v), want (true, nil)", saved, err)
	}

	result, err := oldStore.DeleteProjectIfLeaseHeld(projectID, testExecutionLeaseName, oldToken)
	if err != nil || result != LeaseFencedDeleteFenced {
		t.Fatalf("stale project delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteFenced)
	}
	if _, err := newStore.GetProject(projectID); err != nil {
		t.Fatalf("stale delete removed project: %v", err)
	}
	if got, err := newStore.GetTest(record.ID); err != nil || got.Status != "INITIALIZING" {
		t.Fatalf("stale delete changed active test = (%#v, %v)", got, err)
	}
	result, err = newStore.DeleteProjectIfLeaseHeld(projectID, testExecutionLeaseName, newToken)
	if err != nil || result != LeaseFencedDeleteActive {
		t.Fatalf("active project delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteActive)
	}

	record.Status = "FAILED"
	if saved, err := newStore.SaveTestIfLeaseHeld(record, testExecutionLeaseName, newToken); err != nil || !saved {
		t.Fatalf("successor terminal transition = (%v, %v), want (true, nil)", saved, err)
	}
	result, err = newStore.DeleteProjectIfLeaseHeld(projectID, testExecutionLeaseName, newToken)
	if err != nil || result != LeaseFencedDeleteDeleted {
		t.Fatalf("terminal project delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteDeleted)
	}
	result, err = newStore.DeleteProjectIfLeaseHeld(projectID, testExecutionLeaseName, newToken)
	if err != nil || result != LeaseFencedDeleteMissing {
		t.Fatalf("missing project delete = (%q, %v), want (%q, nil)", result, err, LeaseFencedDeleteMissing)
	}
}

func TestSQLiteStoreRejectsCapacityBindingAfterSubsecondLeaseExpiry(t *testing.T) {
	store := newHAFencingStore(t)
	token := "controller-a/subsecond-binding"
	acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, token, time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire lease = (%v, %v), want (true, nil)", acquired, err)
	}
	now := time.Now().UTC()
	reservation := &CapacityReservation{
		ID:            "reservation-subsecond-expiry",
		ProjectID:     DefaultProjectID,
		StartAt:       now.Add(time.Hour),
		EndAt:         now.Add(2 * time.Hour),
		ApprovalState: "approved",
	}
	if err := store.SaveCapacityReservation(reservation); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	for {
		fraction := time.Now().Nanosecond()
		if fraction >= int(400*time.Millisecond) && fraction <= int(700*time.Millisecond) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	expiredAt := time.Now().UTC().Truncate(time.Second).Add(200 * time.Millisecond)
	if _, err := store.db.Exec(`UPDATE scheduler_leases SET expires_at = ? WHERE name = ?`, expiredAt, testExecutionLeaseName); err != nil {
		t.Fatalf("expire lease within current second: %v", err)
	}
	testID, status := "test-too-late", "QUEUED"
	bound, err := store.UpdateCapacityReservationBindingIfLeaseHeld(reservation.ID, &testID, &status, time.Now(), testExecutionLeaseName, token)
	if err != nil {
		t.Fatalf("UpdateCapacityReservationBindingIfLeaseHeld: %v", err)
	}
	if bound {
		t.Fatal("capacity reservation bound after subsecond lease expiry")
	}
	got, err := store.GetCapacityReservation(reservation.ID)
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if got.BoundTestID != nil {
		t.Fatalf("expired epoch persisted bound_test_id = %q", *got.BoundTestID)
	}
}

func TestSQLiteStoreComparisonBaselineIsAtomicAndProjectScoped(t *testing.T) {
	store := newHAFencingStore(t)
	now := time.Now().UTC()
	for _, projectID := range []string{"project-baseline-a", "project-baseline-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	pinnedAt := now.Add(-time.Hour)
	records := []*TestRecord{
		{ID: "baseline-a-1", ProjectID: "project-baseline-a", Name: "Checkout", Status: "COMPLETE", StartTime: now, Groups: []any{}},
		{ID: "baseline-a-2", ProjectID: "project-baseline-a", Name: "Checkout", Status: "COMPLETE", StartTime: now, Groups: []any{}},
		{ID: "baseline-b-1", ProjectID: "project-baseline-b", Name: "Checkout", Status: "COMPLETE", StartTime: now, Groups: []any{}, BaselinePinned: true, BaselinePinnedAt: &pinnedAt},
	}
	for _, record := range records {
		if err := store.SaveTest(record); err != nil {
			t.Fatalf("SaveTest(%s): %v", record.ID, err)
		}
	}
	firstPin := now.Add(-time.Minute)
	if _, err := store.SetTestComparisonBaseline("baseline-a-1", &firstPin); err != nil {
		t.Fatalf("pin first baseline: %v", err)
	}
	secondPin := now
	if _, err := store.SetTestComparisonBaseline("baseline-a-2", &secondPin); err != nil {
		t.Fatalf("replace comparison baseline: %v", err)
	}

	wants := map[string]bool{
		"baseline-a-1": false,
		"baseline-a-2": true,
		"baseline-b-1": true,
	}
	for id, wantPinned := range wants {
		got, err := store.GetTest(id)
		if err != nil {
			t.Fatalf("GetTest(%s): %v", id, err)
		}
		if got.BaselinePinned != wantPinned {
			t.Fatalf("%s BaselinePinned = %v, want %v", id, got.BaselinePinned, wantPinned)
		}
	}
}
