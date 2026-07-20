package storage

import (
	"errors"
	"testing"
	"time"
)

func boundCapacityStartFixture(t *testing.T, suffix string) (*SQLiteStore, *TestRecord, string) {
	t.Helper()
	store := newAtomicBindingStore(t)
	now := time.Now().UTC()
	reservationID := "slot-start-" + suffix
	testID := "test-start-" + suffix
	seedAtomicBindingReservation(t, store, reservationID, now)
	record := atomicBindingTestRecord(testID, now)
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if result, err := store.BindCapacityReservationToTest(reservationID, testID, now); err != nil || result != CapacityBindingBound {
		t.Fatalf("BindCapacityReservationToTest = (%q, %v), want bound", result, err)
	}
	stale, err := store.GetTest(testID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	stale.Status = "INITIALIZING"
	return store, stale, reservationID
}

func TestCapacityReservationRejectBeforeStaleStartCannotBeUndone(t *testing.T) {
	store, staleStart, reservationID := boundCapacityStartFixture(t, "reject-wins")

	changed, err := store.UpdateCapacityReservationApprovalState(
		reservationID,
		[]string{"approved"},
		"rejected",
		time.Now().UTC(),
	)
	if err != nil || !changed {
		t.Fatalf("reject = (%v, %v), want committed rejection", changed, err)
	}
	// staleStart still carries the pre-reject timeslot_id. Before the atomic
	// launch fence this full-row upsert restored that pointer and launched.
	if err := store.SaveTestStartIfNoDeploymentLease(staleStart, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrCapacityReservationStartInvalid) {
		t.Fatalf("stale start error = %v, want ErrCapacityReservationStartInvalid", err)
	}

	storedTest, err := store.GetTest(staleStart.ID)
	if err != nil {
		t.Fatalf("GetTest after race: %v", err)
	}
	if storedTest.Status != "QUEUED" || storedTest.TimeslotID != nil {
		t.Fatalf("rejected-slot test = %+v, want QUEUED and unbound", storedTest)
	}
	reservation, err := store.GetCapacityReservation(reservationID)
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if reservation.ApprovalState != "rejected" || reservation.BoundTestID != nil {
		t.Fatalf("reservation = %+v, want rejected and unbound", reservation)
	}
}

func TestCapacityReservationStartBeforeRejectProtectsActiveBinding(t *testing.T) {
	store, start, reservationID := boundCapacityStartFixture(t, "start-wins")

	if err := store.SaveTestStartIfNoDeploymentLease(start, DefaultDeploymentLeaseName, "", ""); err != nil {
		t.Fatalf("atomic start: %v", err)
	}
	changed, err := store.UpdateCapacityReservationApprovalState(
		reservationID,
		[]string{"approved"},
		"rejected",
		time.Now().UTC(),
	)
	if changed || !errors.Is(err, ErrCapacityReservationBoundActiveTest) {
		t.Fatalf("reject active binding = (%v, %v), want protected active-test error", changed, err)
	}

	storedTest, err := store.GetTest(start.ID)
	if err != nil {
		t.Fatalf("GetTest after start: %v", err)
	}
	if storedTest.Status != "INITIALIZING" || storedTest.TimeslotID == nil || *storedTest.TimeslotID != reservationID {
		t.Fatalf("started test = %+v, want INITIALIZING with retained binding", storedTest)
	}
	reservation, err := store.GetCapacityReservation(reservationID)
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if reservation.ApprovalState != "approved" || reservation.BoundTestID == nil || *reservation.BoundTestID != start.ID {
		t.Fatalf("reservation = %+v, want approved and bound to active test", reservation)
	}
	if reservation.BoundTestStatus == nil || *reservation.BoundTestStatus != "INITIALIZING" {
		t.Fatalf("bound test status = %v, want INITIALIZING", reservation.BoundTestStatus)
	}
}

func TestCapacityReservationInvalidatingEditBeforeStaleStartCannotBeUndone(t *testing.T) {
	store, staleStart, reservationID := boundCapacityStartFixture(t, "edit-wins")
	edit, err := store.GetCapacityReservation(reservationID)
	if err != nil {
		t.Fatalf("GetCapacityReservation before edit: %v", err)
	}
	edit.EndAt = time.Now().UTC().Add(5 * time.Minute)
	edit.ApprovalState = "pending"
	edit.ApprovalID = nil
	edit.BoundTestID = nil
	edit.BoundTestStatus = nil
	if err := store.UpdateCapacityReservationDetails(edit, true); err != nil {
		t.Fatalf("invalidating edit: %v", err)
	}

	// staleStart still carries the pre-edit timeslot_id. The launch transaction
	// must observe the committed approval reset instead of restoring that pointer.
	if err := store.SaveTestStartIfNoDeploymentLease(staleStart, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrCapacityReservationStartInvalid) {
		t.Fatalf("stale start error = %v, want ErrCapacityReservationStartInvalid", err)
	}
	storedTest, err := store.GetTest(staleStart.ID)
	if err != nil {
		t.Fatalf("GetTest after edit: %v", err)
	}
	if storedTest.Status != "QUEUED" || storedTest.TimeslotID != nil {
		t.Fatalf("edited-slot test = %+v, want QUEUED and unbound", storedTest)
	}
	reservation, err := store.GetCapacityReservation(reservationID)
	if err != nil {
		t.Fatalf("GetCapacityReservation after edit: %v", err)
	}
	if reservation.ApprovalState != "pending" || reservation.BoundTestID != nil {
		t.Fatalf("reservation = %+v, want pending and unbound", reservation)
	}
}

func TestCapacityReservationStartBeforeInvalidatingEditProtectsActiveBinding(t *testing.T) {
	store, start, reservationID := boundCapacityStartFixture(t, "start-before-edit")
	staleEdit, err := store.GetCapacityReservation(reservationID)
	if err != nil {
		t.Fatalf("GetCapacityReservation before start: %v", err)
	}
	staleEdit.EndAt = time.Now().UTC().Add(5 * time.Minute)
	staleEdit.ApprovalState = "pending"
	staleEdit.ApprovalID = nil
	staleEdit.BoundTestID = nil
	staleEdit.BoundTestStatus = nil

	if err := store.SaveTestStartIfNoDeploymentLease(start, DefaultDeploymentLeaseName, "", ""); err != nil {
		t.Fatalf("atomic start: %v", err)
	}
	if err := store.UpdateCapacityReservationDetails(staleEdit, true); !errors.Is(err, ErrCapacityReservationBoundActiveTest) {
		t.Fatalf("invalidating edit error = %v, want ErrCapacityReservationBoundActiveTest", err)
	}

	storedTest, err := store.GetTest(start.ID)
	if err != nil {
		t.Fatalf("GetTest after protected edit: %v", err)
	}
	if storedTest.Status != "INITIALIZING" || storedTest.TimeslotID == nil || *storedTest.TimeslotID != reservationID {
		t.Fatalf("started test = %+v, want INITIALIZING with retained binding", storedTest)
	}
	reservation, err := store.GetCapacityReservation(reservationID)
	if err != nil {
		t.Fatalf("GetCapacityReservation after protected edit: %v", err)
	}
	if reservation.ApprovalState != "approved" || reservation.BoundTestID == nil || *reservation.BoundTestID != start.ID {
		t.Fatalf("reservation = %+v, want approved and bound to active test", reservation)
	}
}

func TestCapacityReservationStartUsesActualDatabaseTimeNotFutureSchedule(t *testing.T) {
	store := newAtomicBindingStore(t)
	futureStart := time.Now().UTC().Add(time.Hour)
	reservationID := "slot-future-start"
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: reservationID, ProjectID: DefaultProjectID,
		StartAt: futureStart, EndAt: futureStart.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	record := atomicBindingTestRecord("test-future-start", futureStart)
	record.Status = "SCHEDULED"
	record.ScheduledStartAt = &futureStart
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	windowEnd := futureStart.Add(10 * time.Minute)
	if result, err := store.BindCapacityReservationToTestWithWindow(
		reservationID, record.ID, time.Now().UTC(), futureStart, windowEnd,
	); err != nil || result != CapacityBindingBound {
		t.Fatalf("BindCapacityReservationToTestWithWindow = (%q, %v), want bound", result, err)
	}
	staleStart, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	staleStart.Status = "INITIALIZING"
	if err := store.SaveTestStartIfNoDeploymentLease(staleStart, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrCapacityReservationStartInvalid) {
		t.Fatalf("early start error = %v, want ErrCapacityReservationStartInvalid", err)
	}
	durable, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest after early start: %v", err)
	}
	if durable.Status != "SCHEDULED" {
		t.Fatalf("durable status = %q, want SCHEDULED", durable.Status)
	}
}
