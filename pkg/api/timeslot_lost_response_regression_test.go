package api

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type capacityBindLostResponseStore struct {
	SQLiteStore            *storage.SQLiteStore
	armVerificationFailure atomic.Bool
	failNextVerification   atomic.Bool
}

var errCapacityBindVerificationLost = errors.New("simulated lost post-commit verification response")

func (s *capacityBindLostResponseStore) BindCapacityReservationToTestWithWindow(reservationID, testID string, boundAt, windowStartAt, windowEndAt time.Time) (storage.CapacityBindingResult, error) {
	result, err := s.SQLiteStore.BindCapacityReservationToTestWithWindow(reservationID, testID, boundAt, windowStartAt, windowEndAt)
	if err == nil && result == storage.CapacityBindingBound && s.armVerificationFailure.CompareAndSwap(true, false) {
		s.failNextVerification.Store(true)
	}
	return result, err
}

func (s *capacityBindLostResponseStore) GetCapacityReservation(id string) (*storage.CapacityReservation, error) {
	if s.failNextVerification.CompareAndSwap(true, false) {
		return nil, errCapacityBindVerificationLost
	}
	return s.SQLiteStore.GetCapacityReservation(id)
}

func TestStandaloneTimeslotBindRetryAdoptsExactCommittedPair(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &capacityBindLostResponseStore{SQLiteStore: base}
	now := time.Now().UTC()
	if err := store.SQLiteStore.SaveProject(&storage.Project{
		ID: "lost-bind-project", Name: "Lost bind project", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SQLiteStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "lost-bind-slot", ProjectID: "lost-bind-project", ApprovalState: "approved",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := store.SQLiteStore.SaveTest(&storage.TestRecord{
		ID: "lost-bind-test", RefKey: "RUN-LOST-BIND", Name: "Lost bind test", Status: "QUEUED",
		ProjectID: "lost-bind-project", StartTime: now, Duration: int64(10 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	runner := NewTestRunner(10, NewHub(), sqliteOverlay(store))
	store.armVerificationFailure.Store(true)

	reservation, result, err := runner.BindCapacityTimeslot("lost-bind-slot", "lost-bind-test")
	if !errors.Is(err, errCapacityBindVerificationLost) {
		t.Fatalf("first BindCapacityTimeslot = (%#v, %q, %v), want committed bind with lost verification response", reservation, result, err)
	}
	if result != storage.CapacityBindingBound {
		t.Fatalf("first bind result = %q, want %q", result, storage.CapacityBindingBound)
	}

	reservation, result, err = runner.BindCapacityTimeslot("lost-bind-slot", "lost-bind-test")
	if err != nil || result != storage.CapacityBindingBound || reservation == nil ||
		reservation.BoundTestID == nil || *reservation.BoundTestID != "lost-bind-test" {
		t.Fatalf("retry BindCapacityTimeslot = (%#v, %q, %v), want exact committed pair", reservation, result, err)
	}
	durable, err := store.SQLiteStore.GetTest("lost-bind-test")
	if err != nil || durable == nil || durable.TimeslotID == nil || *durable.TimeslotID != "lost-bind-slot" {
		t.Fatalf("durable test after retry = (%#v, %v), want reciprocal timeslot", durable, err)
	}
	if durable.CapacityWindowStartAt == nil || durable.CapacityWindowEndAt == nil {
		t.Fatalf("durable test is missing its committed launch window: %#v", durable)
	}
	shiftedStart := durable.CapacityWindowStartAt.Add(time.Second)
	shiftedEnd := shiftedStart.Add(durable.CapacityWindowEndAt.Sub(*durable.CapacityWindowStartAt))
	if retry, err := store.BindCapacityReservationToTestWithWindow(
		"lost-bind-slot", "lost-bind-test", shiftedStart, shiftedStart, shiftedEnd,
	); err != nil || retry != storage.CapacityBindingBound {
		t.Fatalf("same-span lost-response retry = (%q, %v), want bound", retry, err)
	}
	if conflict, err := store.BindCapacityReservationToTestWithWindow(
		"lost-bind-slot", "lost-bind-test", shiftedStart, shiftedStart, shiftedEnd.Add(time.Second),
	); err != nil || conflict != storage.CapacityBindingWindowMismatch {
		t.Fatalf("changed-span reciprocal retry = (%q, %v), want window mismatch", conflict, err)
	}

	// Idempotency is pair-specific. Reusing either bound row with a different
	// peer must retain the ordinary conflict result.
	if err := store.SQLiteStore.SaveTest(&storage.TestRecord{
		ID: "lost-bind-other-test", Name: "Other test", Status: "QUEUED", ProjectID: "lost-bind-project",
		StartTime: now, Duration: int64(10 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest(other): %v", err)
	}
	if err := store.SQLiteStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "lost-bind-other-slot", ProjectID: "lost-bind-project", ApprovalState: "approved",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveCapacityReservation(other): %v", err)
	}
	if conflict, err := store.BindCapacityReservationToTestWithWindow(
		"lost-bind-slot", "lost-bind-other-test", now, now, now.Add(10*time.Minute),
	); err != nil || conflict != storage.CapacityBindingReservationBound {
		t.Fatalf("bound reservation with different test = (%q, %v), want reservation conflict", conflict, err)
	}
	if conflict, err := store.BindCapacityReservationToTestWithWindow(
		"lost-bind-other-slot", "lost-bind-test", now, now, now.Add(10*time.Minute),
	); err != nil || conflict != storage.CapacityBindingTestBound {
		t.Fatalf("bound test with different reservation = (%q, %v), want test conflict", conflict, err)
	}
}
