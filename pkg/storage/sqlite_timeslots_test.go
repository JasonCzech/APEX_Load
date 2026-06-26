package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTimeslotStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestCapacityReservationApprovalCASAndRetention(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-7 * 24 * time.Hour)

	// Three reservations: a fully-elapsed one, a long-rejected one, and a live one.
	ended := &CapacityReservation{ID: "ended", ProjectID: "p", StartAt: now.Add(-30 * 24 * time.Hour), EndAt: now.Add(-29 * 24 * time.Hour), ApprovalState: "approved", UpdatedAt: now.Add(-29 * 24 * time.Hour)}
	rejected := &CapacityReservation{ID: "rejected", ProjectID: "p", StartAt: now.Add(24 * time.Hour), EndAt: now.Add(25 * time.Hour), ApprovalState: "rejected", UpdatedAt: now.Add(-30 * 24 * time.Hour)}
	live := &CapacityReservation{ID: "live", ProjectID: "p", StartAt: now.Add(24 * time.Hour), EndAt: now.Add(25 * time.Hour), ApprovalState: "pending", UpdatedAt: now}
	for _, r := range []*CapacityReservation{ended, rejected, live} {
		if err := store.SaveCapacityReservation(r); err != nil {
			t.Fatalf("save %s: %v", r.ID, err)
		}
	}
	// SaveCapacityReservation overwrites updated_at with the real wall clock, so
	// drive the rejected row's updated_at through the CAS (which honors the passed
	// timestamp) to pin it to the test's logical past — otherwise the
	// rejected-prune assertion below depends on the real date.
	if _, err := store.UpdateCapacityReservationApprovalState("rejected", []string{"rejected"}, "rejected", now.Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("seed rejected updated_at: %v", err)
	}

	// Approval CAS: pending -> approved succeeds; a second flip from pending fails.
	ok, err := store.UpdateCapacityReservationApprovalState("live", []string{"pending"}, "approved", now)
	if err != nil || !ok {
		t.Fatalf("CAS pending->approved = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = store.UpdateCapacityReservationApprovalState("live", []string{"pending"}, "approved", now)
	if err != nil || ok {
		t.Fatalf("CAS approved-not-pending = (%v, %v), want (false, nil)", ok, err)
	}
	// Reject can still revoke an approved slot.
	ok, err = store.UpdateCapacityReservationApprovalState("live", []string{"pending", "approved"}, "rejected", now)
	if err != nil || !ok {
		t.Fatalf("CAS approved->rejected = (%v, %v), want (true, nil)", ok, err)
	}

	// Retention sweeps: ended-before removes the elapsed slot; rejected-before
	// removes the long-rejected one (but NOT 'live', just rejected at `now`).
	endedPruned, err := store.DeleteCapacityReservationsEndedBefore(cutoff)
	if err != nil || endedPruned != 1 {
		t.Fatalf("DeleteCapacityReservationsEndedBefore = (%d, %v), want (1, nil)", endedPruned, err)
	}
	rejectedPruned, err := store.DeleteRejectedCapacityReservationsBefore(cutoff)
	if err != nil || rejectedPruned != 1 {
		t.Fatalf("DeleteRejectedCapacityReservationsBefore = (%d, %v), want (1, nil)", rejectedPruned, err)
	}
	if got, _ := store.GetCapacityReservation("ended"); got != nil {
		t.Fatalf("ended reservation should have been pruned, got %+v", got)
	}
	if got, _ := store.GetCapacityReservation("rejected"); got != nil {
		t.Fatalf("rejected reservation should have been pruned, got %+v", got)
	}
	if got, _ := store.GetCapacityReservation("live"); got == nil {
		t.Fatal("live reservation should have survived retention")
	}
}

func TestCapacityReservationRoundTrip(t *testing.T) {
	store := openTimeslotStore(t)
	gens, vus := 4, 500
	scenario := "scenario-1"
	res := &CapacityReservation{
		ID:         "ts-1",
		ProjectID:  "proj-a",
		ScenarioID: &scenario,
		StartAt:    time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		EndAt:      time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC),
		Timezone:   "UTC",
		RequestedCapacity: &CapacityReservationRequest{
			Generators: &gens, VUsers: &vus, Protocol: "http", LoadZone: "east",
		},
		ApprovalState: "approved",
	}
	if err := store.SaveCapacityReservation(res); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetCapacityReservation("ts-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProjectID != "proj-a" || got.ApprovalState != "approved" || got.Timezone != "UTC" {
		t.Fatalf("scalar fields not preserved: %+v", got)
	}
	if got.ScenarioID == nil || *got.ScenarioID != "scenario-1" {
		t.Fatalf("scenario_id not preserved: %v", got.ScenarioID)
	}
	if got.RequestedCapacity == nil || got.RequestedCapacity.Generators == nil || *got.RequestedCapacity.Generators != 4 {
		t.Fatalf("requested_capacity not preserved: %+v", got.RequestedCapacity)
	}
	if !got.StartAt.Equal(res.StartAt) || !got.EndAt.Equal(res.EndAt) {
		t.Fatalf("window not preserved: %v - %v", got.StartAt, got.EndAt)
	}
	if got.BoundTestID != nil {
		t.Fatalf("expected unbound reservation, got bound_test_id=%v", *got.BoundTestID)
	}
}

func TestCapacityReservationListTimeRangeAndBinding(t *testing.T) {
	store := openTimeslotStore(t)
	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		res := &CapacityReservation{
			ID:            fmt.Sprintf("ts-%d", i),
			ProjectID:     "proj-a",
			StartAt:       base.Add(time.Duration(i) * time.Hour),
			EndAt:         base.Add(time.Duration(i)*time.Hour + 30*time.Minute),
			ApprovalState: "approved",
		}
		if err := store.SaveCapacityReservation(res); err != nil {
			t.Fatalf("save ts-%d: %v", i, err)
		}
	}
	// A reservation in a different project must never leak into proj-a queries.
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: "other", ProjectID: "proj-b", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("save other: %v", err)
	}

	all, err := store.ListCapacityReservations("proj-a", 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("list len = %d, want 3", len(all))
	}

	page, err := store.ListCapacityReservations("proj-a", 2, 0)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("page len = %d, want 2 (limit honored)", len(page))
	}

	// Window [base+15m, base+45m) overlaps only ts-0 [base, base+30m).
	overlaps, err := store.ListCapacityReservationsByTimeRange("proj-a", base.Add(15*time.Minute), base.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("time range: %v", err)
	}
	if len(overlaps) != 1 || overlaps[0].ID != "ts-0" {
		t.Fatalf("overlaps = %+v, want only ts-0", overlaps)
	}
	for _, o := range overlaps {
		if o.ProjectID != "proj-a" {
			t.Fatalf("project isolation breached: got %s", o.ProjectID)
		}
	}

	// Atomic check-and-set: the first bind wins, the second is rejected.
	testID, status := "test-1", "RUNNING"
	bound, err := store.UpdateCapacityReservationBinding("ts-0", &testID, &status, time.Now())
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if !bound {
		t.Fatal("first bind should succeed")
	}
	bound2, err := store.UpdateCapacityReservationBinding("ts-0", &testID, &status, time.Now())
	if err != nil {
		t.Fatalf("second bind: %v", err)
	}
	if bound2 {
		t.Fatal("second bind must fail because the reservation is already bound")
	}
	rebound, err := store.GetCapacityReservation("ts-0")
	if err != nil {
		t.Fatalf("get after bind: %v", err)
	}
	if rebound.BoundTestID == nil || *rebound.BoundTestID != "test-1" {
		t.Fatalf("bound_test_id = %v, want test-1", rebound.BoundTestID)
	}

	if err := store.DeleteCapacityReservation("ts-0"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetCapacityReservation("ts-0"); err == nil {
		t.Fatal("expected error fetching a deleted reservation")
	}
}
