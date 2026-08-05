package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newAtomicBindingStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "capacity-bindings.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedAtomicBindingReservation(t *testing.T, store *SQLiteStore, id string, now time.Time) {
	t.Helper()
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: id, ProjectID: DefaultProjectID,
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		ApprovalState: "approved",
		RequestedCapacity: &CapacityReservationRequest{VUsers: func() *int {
			value := 100
			return &value
		}()},
	}); err != nil {
		t.Fatalf("SaveCapacityReservation(%s): %v", id, err)
	}
}

func atomicBindingTestRecord(id string, now time.Time) *TestRecord {
	return &TestRecord{
		ID: id, Name: id, Status: "QUEUED", ProjectID: DefaultProjectID,
		TotalVUsers: 10, Duration: int64(10 * time.Minute), StartTime: now, Groups: []any{},
	}
}

func TestStandaloneCapacityBindEnforcesAndPersistsFullLaunchWindow(t *testing.T) {
	store := newAtomicBindingStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: "slot-full-window", ProjectID: DefaultProjectID,
		StartAt: now.Add(-time.Minute), EndAt: now.Add(15 * time.Minute), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	record := atomicBindingTestRecord("test-full-window", now)
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	// The bare 10-minute duration fits, but the authoritative launch window
	// (for example duration + start delay/baselines/ramp-down) does not.
	result, err := store.BindCapacityReservationToTestWithWindow(
		"slot-full-window", record.ID, now, now, now.Add(20*time.Minute),
	)
	if err != nil || result != CapacityBindingWindowMismatch {
		t.Fatalf("oversized full-window bind = (%q, %v), want window mismatch", result, err)
	}
	stored, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest after rejected bind: %v", err)
	}
	if stored.TimeslotID != nil || stored.CapacityWindowStartAt != nil || stored.CapacityWindowEndAt != nil {
		t.Fatalf("rejected bind persisted partial window: %+v", stored)
	}

	windowEnd := now.Add(14 * time.Minute)
	result, err = store.BindCapacityReservationToTestWithWindow("slot-full-window", record.ID, now, now, windowEnd)
	if err != nil || result != CapacityBindingBound {
		t.Fatalf("fitting full-window bind = (%q, %v), want bound", result, err)
	}
	stored, err = store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest after bind: %v", err)
	}
	if stored.CapacityWindowStartAt == nil || stored.CapacityWindowEndAt == nil ||
		!stored.CapacityWindowStartAt.Equal(now) || !stored.CapacityWindowEndAt.Equal(windowEnd) {
		t.Fatalf("persisted capacity window = (%v, %v), want (%v, %v)", stored.CapacityWindowStartAt, stored.CapacityWindowEndAt, now, windowEnd)
	}
}

func TestCreateTestAndBindCapacityReservationRollsBackOnBindingFailpoint(t *testing.T) {
	store := newAtomicBindingStore(t)
	now := time.Now().UTC()
	seedAtomicBindingReservation(t, store, "slot-failpoint", now)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_capacity_binding
		BEFORE UPDATE OF bound_test_id ON capacity_reservations
		BEGIN SELECT RAISE(ABORT, 'capacity binding failpoint'); END`); err != nil {
		t.Fatalf("create failpoint trigger: %v", err)
	}

	record := atomicBindingTestRecord("test-failpoint", now)
	result, err := store.CreateTestAndBindCapacityReservation(record, "slot-failpoint", now)
	if err == nil || result != CapacityBindingFenced {
		t.Fatalf("atomic create failpoint = (%q, %v), want storage error", result, err)
	}
	if _, err := store.GetTest("test-failpoint"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("test insert survived rolled-back binding = %v", err)
	}
	reservation, err := store.GetCapacityReservation("slot-failpoint")
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if reservation.BoundTestID != nil {
		t.Fatalf("reservation bind survived rollback: %v", reservation.BoundTestID)
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_capacity_binding`); err != nil {
		t.Fatalf("drop failpoint trigger: %v", err)
	}
	result, err = store.CreateTestAndBindCapacityReservation(record, "slot-failpoint", now)
	if err != nil || result != CapacityBindingBound {
		t.Fatalf("atomic create retry = (%q, %v), want bound", result, err)
	}
	result, err = store.CreateTestAndBindCapacityReservation(record, "slot-failpoint", now)
	if err != nil || result != CapacityBindingBound {
		t.Fatalf("idempotent atomic create retry = (%q, %v), want bound", result, err)
	}
}

func TestCapacityReservationBindingIsFencedAcrossTwoStores(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shared-capacity-bindings.db")
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
	seedAtomicBindingReservation(t, oldStore, "slot-takeover", now)
	if err := oldStore.SaveTest(atomicBindingTestRecord("test-takeover", now)); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	oldToken := "controller-old/capacity-1"
	newToken := "controller-new/capacity-1"
	if acquired, err := oldStore.TryAcquireSchedulerLease(testExecutionLeaseName, oldToken, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire old lease = (%v, %v)", acquired, err)
	}
	if err := oldStore.ReleaseSchedulerLease(testExecutionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	if acquired, err := newStore.TryAcquireSchedulerLease(testExecutionLeaseName, newToken, now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire new lease = (%v, %v)", acquired, err)
	}

	result, err := oldStore.BindCapacityReservationToTestIfLeaseHeld("slot-takeover", "test-takeover", now, testExecutionLeaseName, oldToken)
	if err != nil || result != CapacityBindingFenced {
		t.Fatalf("stale bind = (%q, %v), want fenced", result, err)
	}
	result, err = newStore.BindCapacityReservationToTestIfLeaseHeld("slot-takeover", "test-takeover", now, testExecutionLeaseName, newToken)
	if err != nil || result != CapacityBindingBound {
		t.Fatalf("successor bind = (%q, %v), want bound", result, err)
	}
	assertReciprocalCapacityBinding(t, newStore, "slot-takeover", "test-takeover")

	seedAtomicBindingReservation(t, oldStore, "slot-create-takeover", now)
	createRecord := atomicBindingTestRecord("test-create-takeover", now)
	result, err = oldStore.CreateTestAndBindCapacityReservationIfLeaseHeld(createRecord, "slot-create-takeover", now, testExecutionLeaseName, oldToken)
	if err != nil || result != CapacityBindingFenced {
		t.Fatalf("stale atomic create = (%q, %v), want fenced", result, err)
	}
	if _, err := newStore.GetTest(createRecord.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale atomic create inserted test: %v", err)
	}
	result, err = newStore.CreateTestAndBindCapacityReservationIfLeaseHeld(createRecord, "slot-create-takeover", now, testExecutionLeaseName, newToken)
	if err != nil || result != CapacityBindingBound {
		t.Fatalf("successor atomic create = (%q, %v), want bound", result, err)
	}
	assertReciprocalCapacityBinding(t, newStore, "slot-create-takeover", createRecord.ID)
}

func TestConcurrentCapacityBindsProduceOneReciprocalWinner(t *testing.T) {
	store := newAtomicBindingStore(t)
	now := time.Now().UTC()
	seedAtomicBindingReservation(t, store, "slot-concurrent", now)
	for _, testID := range []string{"test-concurrent-a", "test-concurrent-b"} {
		if err := store.SaveTest(atomicBindingTestRecord(testID, now)); err != nil {
			t.Fatalf("SaveTest(%s): %v", testID, err)
		}
	}
	start := make(chan struct{})
	type outcome struct {
		testID string
		result CapacityBindingResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, testID := range []string{"test-concurrent-a", "test-concurrent-b"} {
		wg.Add(1)
		go func(testID string) {
			defer wg.Done()
			<-start
			result, err := store.BindCapacityReservationToTest("slot-concurrent", testID, now)
			outcomes <- outcome{testID: testID, result: result, err: err}
		}(testID)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	winner := ""
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent bind %s: %v", outcome.testID, outcome.err)
		}
		if outcome.result == CapacityBindingBound {
			if winner != "" {
				t.Fatalf("multiple bind winners: %s and %s", winner, outcome.testID)
			}
			winner = outcome.testID
		}
	}
	if winner == "" {
		t.Fatal("concurrent binds produced no winner")
	}
	assertReciprocalCapacityBinding(t, store, "slot-concurrent", winner)
	loser := "test-concurrent-a"
	if winner == loser {
		loser = "test-concurrent-b"
	}
	if test, err := store.GetTest(loser); err != nil || test.TimeslotID != nil {
		t.Fatalf("loser retained test-side binding: (%#v, %v)", test, err)
	}
}

func TestConcurrentCapacityBindAndTestDeleteCannotLeaveDanglingBinding(t *testing.T) {
	store := newAtomicBindingStore(t)
	now := time.Now().UTC()
	seedAtomicBindingReservation(t, store, "slot-delete-race", now)
	if err := store.SaveTest(atomicBindingTestRecord("test-delete-race", now)); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	start := make(chan struct{})
	var bindErr, deleteErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, bindErr = store.BindCapacityReservationToTest("slot-delete-race", "test-delete-race", now)
	}()
	go func() {
		defer wg.Done()
		<-start
		deleteErr = store.DeleteTest("test-delete-race")
	}()
	close(start)
	wg.Wait()
	if bindErr != nil {
		t.Fatalf("BindCapacityReservationToTest: %v", bindErr)
	}
	if deleteErr != nil {
		t.Fatalf("DeleteTest: %v", deleteErr)
	}
	if _, err := store.GetTest("test-delete-race"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("test still exists after delete race: %v", err)
	}
	reservation, err := store.GetCapacityReservation("slot-delete-race")
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if reservation.BoundTestID != nil {
		t.Fatalf("delete race left dangling reservation binding: %v", reservation.BoundTestID)
	}
}

func TestRepairCapacityReservationBindingsAndTerminalRelease(t *testing.T) {
	store := newAtomicBindingStore(t)
	now := time.Now().UTC()
	legacyTestID := "test-legacy-one-sided"
	if err := store.SaveTest(atomicBindingTestRecord(legacyTestID, now)); err != nil {
		t.Fatalf("SaveTest(legacy): %v", err)
	}
	status := "QUEUED"
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: "slot-legacy-one-sided", ProjectID: DefaultProjectID,
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
		BoundTestID: &legacyTestID, BoundTestStatus: &status,
	}); err != nil {
		t.Fatalf("SaveCapacityReservation(legacy): %v", err)
	}
	terminalSlotID := "slot-terminal-one-sided"
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: terminalSlotID, ProjectID: DefaultProjectID,
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "rejected",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation(terminal): %v", err)
	}
	terminal := atomicBindingTestRecord("test-terminal-one-sided", now)
	terminal.Status = "COMPLETE"
	terminal.TimeslotID = &terminalSlotID
	if err := store.SaveTest(terminal); err != nil {
		t.Fatalf("SaveTest(terminal): %v", err)
	}

	changed, err := store.RepairCapacityReservationBindings()
	if err != nil || changed < 2 {
		t.Fatalf("RepairCapacityReservationBindings = (%d, %v), want at least two changes", changed, err)
	}
	assertReciprocalCapacityBinding(t, store, "slot-legacy-one-sided", legacyTestID)
	if got, err := store.GetTest(terminal.ID); err != nil || got.TimeslotID != nil {
		t.Fatalf("terminal legacy pointer was not released: (%#v, %v)", got, err)
	}

	if released, err := store.ClearCapacityReservationBinding("slot-legacy-one-sided", legacyTestID, now); err != nil || !released {
		t.Fatalf("ClearCapacityReservationBinding = (%v, %v)", released, err)
	}
	reservation, err := store.GetCapacityReservation("slot-legacy-one-sided")
	if err != nil || reservation.BoundTestID != nil || reservation.BoundTestStatus != nil {
		t.Fatalf("reservation side not released: (%#v, %v)", reservation, err)
	}
	if got, err := store.GetTest(legacyTestID); err != nil || got.TimeslotID != nil {
		t.Fatalf("test side not released: (%#v, %v)", got, err)
	}
}

func TestRepairCapacityReservationBindingsUsesBoundedBatches(t *testing.T) {
	store := newAtomicBindingStore(t)
	now := time.Now().UTC()
	const count = 205 // crosses the production repair batch boundary (200)
	for index := 0; index < count; index++ {
		testID := fmt.Sprintf("test-batch-%03d", index)
		slotID := fmt.Sprintf("slot-batch-%03d", index)
		if err := store.SaveTest(atomicBindingTestRecord(testID, now)); err != nil {
			t.Fatalf("SaveTest(%d): %v", index, err)
		}
		status := "QUEUED"
		if err := store.SaveCapacityReservation(&CapacityReservation{
			ID: slotID, ProjectID: DefaultProjectID,
			StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
			BoundTestID: &testID, BoundTestStatus: &status,
		}); err != nil {
			t.Fatalf("SaveCapacityReservation(%d): %v", index, err)
		}
	}
	changed, err := store.RepairCapacityReservationBindings()
	if err != nil || changed != count {
		t.Fatalf("RepairCapacityReservationBindings = (%d, %v), want (%d, nil)", changed, err, count)
	}
	assertReciprocalCapacityBinding(t, store, "slot-batch-000", "test-batch-000")
	assertReciprocalCapacityBinding(t, store, "slot-batch-204", "test-batch-204")
}

func TestCapacityBindingQueriesRemainPostgresLockAndPlaceholderSafe(t *testing.T) {
	placeholders := capacityBindingPlaceholders(2)
	raw := capacityReservationSelect + ` WHERE bound_test_id IN (` + placeholders + `)
		OR id IN (SELECT timeslot_id FROM tests WHERE id IN (` + placeholders + `) AND timeslot_id IS NOT NULL)
		ORDER BY id FOR UPDATE`
	rewritten := rewriteSQL(raw, storeDialectPostgres)
	for _, placeholder := range []string{"$1", "$2", "$3", "$4"} {
		if !strings.Contains(rewritten, placeholder) {
			t.Fatalf("Postgres binding query missing %s: %s", placeholder, rewritten)
		}
	}
	if strings.Contains(rewritten, "?") {
		t.Fatalf("Postgres binding query retained SQLite placeholder: %s", rewritten)
	}
	if !strings.HasSuffix(strings.TrimSpace(rewritten), "FOR UPDATE") {
		t.Fatalf("Postgres binding query lost row lock: %s", rewritten)
	}
}

func TestCapacityBindingWindowSpanComparisonUsesBackendPrecision(t *testing.T) {
	startAt := time.Unix(1_000, 1_500).UTC()
	endAt := startAt.Add(10*time.Minute + 900*time.Nanosecond)
	retryStartAt := startAt.Add(17 * time.Second)

	if capacityBindingWindowSpansMatch(storeDialectSQLite, startAt, endAt, retryStartAt, retryStartAt.Add(10*time.Minute)) {
		t.Fatal("SQLite comparison ignored a representable 900ns duration change")
	}
	if !capacityBindingWindowSpansMatch(storeDialectPostgres, startAt, endAt, retryStartAt, retryStartAt.Add(10*time.Minute)) {
		t.Fatal("PostgreSQL comparison rejected an unrepresentable sub-microsecond duration change")
	}
	if capacityBindingWindowSpansMatch(storeDialectPostgres, startAt, endAt, retryStartAt, retryStartAt.Add(10*time.Minute+2*time.Microsecond)) {
		t.Fatal("PostgreSQL comparison ignored a representable duration change")
	}
}

func assertReciprocalCapacityBinding(t *testing.T, store *SQLiteStore, slotID, testID string) {
	t.Helper()
	reservation, err := store.GetCapacityReservation(slotID)
	if err != nil {
		t.Fatalf("GetCapacityReservation(%s): %v", slotID, err)
	}
	if reservation.BoundTestID == nil || *reservation.BoundTestID != testID {
		t.Fatalf("reservation %s bound test = %v, want %s", slotID, reservation.BoundTestID, testID)
	}
	test, err := store.GetTest(testID)
	if err != nil {
		t.Fatalf("GetTest(%s): %v", testID, err)
	}
	if test.TimeslotID == nil || *test.TimeslotID != slotID {
		t.Fatalf("test %s timeslot = %v, want %s", testID, test.TimeslotID, slotID)
	}
}
