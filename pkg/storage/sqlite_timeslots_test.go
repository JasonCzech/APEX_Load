package storage

import (
	"fmt"
	"path/filepath"
	"sync"
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

func TestCapacityReservationRetentionPreservesNonterminalBindings(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-7 * 24 * time.Hour)
	if err := store.SaveProject(&Project{ID: "retention-project", Name: "Retention Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	for _, fixture := range []struct {
		testID        string
		reservationID string
		status        string
	}{
		{testID: "retention-running", reservationID: "retention-ended-active", status: "RUNNING"},
		{testID: "retention-queued", reservationID: "retention-rejected-queued", status: "QUEUED"},
		{testID: "retention-complete", reservationID: "retention-ended-terminal", status: "COMPLETE"},
	} {
		reservation := &CapacityReservation{
			ID: fixture.reservationID, ProjectID: "retention-project",
			StartAt: now.Add(-30 * 24 * time.Hour), EndAt: now.Add(-29 * 24 * time.Hour),
			ApprovalState: "approved", UpdatedAt: now.Add(-29 * 24 * time.Hour),
		}
		if err := store.SaveCapacityReservation(reservation); err != nil {
			t.Fatalf("SaveCapacityReservation(%s): %v", fixture.reservationID, err)
		}
		if err := store.SaveTest(&TestRecord{
			ID: fixture.testID, Name: fixture.testID, Status: "QUEUED", ProjectID: "retention-project",
			StartTime: now.Add(-30 * 24 * time.Hour), Duration: int64(60 * time.Minute), Groups: []any{},
		}); err != nil {
			t.Fatalf("SaveTest(%s): %v", fixture.testID, err)
		}
		boundAt := now.Add(-30*24*time.Hour + 5*time.Minute)
		if bound, err := store.UpdateCapacityReservationBinding(fixture.reservationID, &fixture.testID, nil, boundAt); err != nil || !bound {
			t.Fatalf("UpdateCapacityReservationBinding(%s) = %v, %v", fixture.reservationID, bound, err)
		}
		if _, err := store.db.Exec(`UPDATE tests SET status = ? WHERE id = ?`, fixture.status, fixture.testID); err != nil {
			t.Fatalf("seed test status %s: %v", fixture.status, err)
		}
		if fixture.testID == "retention-queued" {
			if _, err := store.db.Exec(`UPDATE capacity_reservations SET approval_state = 'rejected', updated_at = ? WHERE id = ?`, now.Add(-30*24*time.Hour), fixture.reservationID); err != nil {
				t.Fatalf("seed rejected binding: %v", err)
			}
		}
	}

	if pruned, err := store.DeleteCapacityReservationsEndedBefore(cutoff); err != nil || pruned != 1 {
		t.Fatalf("DeleteCapacityReservationsEndedBefore = %d, %v, want one terminal binding", pruned, err)
	}
	if pruned, err := store.DeleteRejectedCapacityReservationsBefore(cutoff); err != nil || pruned != 0 {
		t.Fatalf("DeleteRejectedCapacityReservationsBefore = %d, %v, want no nonterminal binding", pruned, err)
	}
	for _, id := range []string{"retention-ended-active", "retention-rejected-queued"} {
		reservation, err := store.GetCapacityReservation(id)
		if err != nil || reservation == nil || reservation.BoundTestID == nil {
			t.Fatalf("nonterminal reservation %s was pruned or unbound: %+v, %v", id, reservation, err)
		}
	}
	if reservation, _ := store.GetCapacityReservation("retention-ended-terminal"); reservation != nil {
		t.Fatalf("terminal reservation survived retention: %+v", reservation)
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

func TestUpdateCapacityReservationDetailsDoesNotRewriteApprovalState(t *testing.T) {
	store := openTimeslotStore(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID:            "ts-race",
		ProjectID:     "proj-a",
		StartAt:       start,
		EndAt:         start.Add(time.Hour),
		Timezone:      "UTC",
		ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("save approved reservation: %v", err)
	}
	stale, err := store.GetCapacityReservation("ts-race")
	if err != nil {
		t.Fatalf("get stale copy: %v", err)
	}
	if ok, err := store.UpdateCapacityReservationApprovalState("ts-race", []string{"approved"}, "rejected", time.Now()); err != nil || !ok {
		t.Fatalf("reject approved reservation = (%v, %v), want (true, nil)", ok, err)
	}
	stale.Timezone = "America/New_York"
	if err := store.UpdateCapacityReservationDetails(stale, false); err == nil {
		t.Fatal("cosmetic update from stale copy succeeded; want revision conflict")
	}
	got, err := store.GetCapacityReservation("ts-race")
	if err != nil {
		t.Fatalf("get updated reservation: %v", err)
	}
	if got.ApprovalState != "rejected" {
		t.Fatalf("approval_state = %q, want concurrently rejected state preserved", got.ApprovalState)
	}
	if got.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want stale edit rejected", got.Timezone)
	}
}

func TestCapacityReservationOverlappingApprovalsAreSerialized(t *testing.T) {
	store := openTimeslotStore(t)
	base := time.Now().UTC().Add(time.Hour)
	units := 2
	for _, id := range []string{"approval-a", "approval-b"} {
		if err := store.SaveCapacityReservation(&CapacityReservation{
			ID: id, ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "pending",
			RequestedCapacity: &CapacityReservationRequest{Generators: &units},
		}); err != nil {
			t.Fatalf("SaveCapacityReservation(%s): %v", id, err)
		}
	}
	type result struct {
		changed    bool
		overbooked bool
		err        error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"approval-a", "approval-b"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			changed, overbooked, err := store.ApproveCapacityReservationWithinLimit(id, time.Now().UTC(), func(*CapacityReservation) (string, int) {
				return "", 2
			}, func(reservation *CapacityReservation) int {
				if reservation.RequestedCapacity == nil || reservation.RequestedCapacity.Generators == nil {
					return 0
				}
				return *reservation.RequestedCapacity.Generators
			})
			results <- result{changed: changed, overbooked: overbooked, err: err}
		}()
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("approval: %v", result.err)
		}
		if result.changed {
			winners++
		}
		if result.overbooked {
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d, want 1 and 1", winners, conflicts)
	}
}

func TestCapacityApprovalFailsClosedOnUnknownInvalidAndOverflowingUnits(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name          string
		maxUnits      int
		targetUnits   int
		approvedUnits int
		wantConflict  bool
		wantError     bool
	}{
		{name: "unknown envelope", maxUnits: -1, targetUnits: 1, wantConflict: true},
		{name: "negative callback units", maxUnits: 10, targetUnits: -1, wantError: true},
		{name: "aggregate overflow", maxUnits: maxInt, targetUnits: maxInt, approvedUnits: 1, wantConflict: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTimeslotStore(t)
			base := time.Now().UTC().Add(time.Hour)
			for _, reservation := range []*CapacityReservation{
				{ID: "approved", ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "approved"},
				{ID: "target", ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "pending"},
			} {
				if err := store.SaveCapacityReservation(reservation); err != nil {
					t.Fatalf("SaveCapacityReservation(%s): %v", reservation.ID, err)
				}
			}

			changed, conflict, err := store.ApproveCapacityReservationWithinLimit(
				"target",
				time.Now().UTC(),
				func(*CapacityReservation) (string, int) { return "", test.maxUnits },
				func(reservation *CapacityReservation) int {
					if reservation.ID == "target" {
						return test.targetUnits
					}
					return test.approvedUnits
				},
			)
			if changed || conflict != test.wantConflict || (err != nil) != test.wantError {
				t.Fatalf("approval = changed:%v conflict:%v err:%v, want false/%v/error:%v", changed, conflict, err, test.wantConflict, test.wantError)
			}
			stored, loadErr := store.GetCapacityReservation("target")
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if stored.ApprovalState != "pending" {
				t.Fatalf("target state = %q, want pending", stored.ApprovalState)
			}
		})
	}
}

func TestCapacityFencedApprovalUsesDurableSettingAndApprovedSnapshot(t *testing.T) {
	store := openTimeslotStore(t)
	if err := store.SaveSetting("capacity-settings", "durable-new"); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}
	base := time.Now().UTC().Add(time.Hour)
	units := 2
	for _, reservation := range []*CapacityReservation{
		{ID: "already-approved", ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "approved", RequestedCapacity: &CapacityReservationRequest{Generators: &units}},
		{ID: "pending-target", ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "pending", RequestedCapacity: &CapacityReservationRequest{Generators: &units}},
	} {
		if err := store.SaveCapacityReservation(reservation); err != nil {
			t.Fatalf("SaveCapacityReservation(%s): %v", reservation.ID, err)
		}
	}
	changed, overbooked, err := store.ApproveCapacityReservationWithCapacityFence(
		"pending-target",
		time.Now().UTC(),
		"capacity-settings",
		func(target *CapacityReservation, approved []*CapacityReservation, settingValue string) (bool, error) {
			if target.ID != "pending-target" || settingValue != "durable-new" {
				t.Fatalf("callback target=%q setting=%q", target.ID, settingValue)
			}
			if len(approved) != 1 || approved[0].ID != "already-approved" {
				t.Fatalf("approved snapshot = %+v", approved)
			}
			return true, nil
		},
	)
	if err != nil || changed || !overbooked {
		t.Fatalf("approval = changed:%v overbooked:%v err:%v, want false/true/nil", changed, overbooked, err)
	}
	stored, err := store.GetCapacityReservation("pending-target")
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	if stored.ApprovalState != "pending" {
		t.Fatalf("rejected target state = %q, want pending", stored.ApprovalState)
	}
}

func TestCapacityFencedSettingCASRollsBackValidationFailure(t *testing.T) {
	store := openTimeslotStore(t)
	if err := store.SaveSetting("capacity-settings", "old"); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}
	base := time.Now().UTC().Add(time.Hour)
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: "approved-for-settings", ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	wantErr := fmt.Errorf("capacity conflict")
	updated, err := store.CompareAndSwapSettingWithCapacityFence(
		"capacity-settings", "old", "new", time.Now().UTC(),
		func(current string, approved []*CapacityReservation) error {
			if current != "old" || len(approved) != 1 || approved[0].ID != "approved-for-settings" {
				t.Fatalf("validator current=%q approved=%+v", current, approved)
			}
			return wantErr
		},
	)
	if updated || err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("setting CAS = updated:%v err:%v, want false/%v", updated, err, wantErr)
	}
	if raw, err := store.GetSetting("capacity-settings"); err != nil || raw != "old" {
		t.Fatalf("setting after rollback = %q, %v; want old, nil", raw, err)
	}
}

func TestCapacityApprovalDerivesEnvelopeFromTransactionalTarget(t *testing.T) {
	store := openTimeslotStore(t)
	base := time.Now().UTC().Add(time.Hour)
	units := 2
	approved := &CapacityReservation{
		ID: "approved-new-pool", ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "approved",
		RequestedCapacity: &CapacityReservationRequest{PoolID: "new-pool", Generators: &units},
	}
	target := &CapacityReservation{
		ID: "pending-moved-pool", ProjectID: "proj-a", StartAt: base, EndAt: base.Add(time.Hour), ApprovalState: "pending",
		RequestedCapacity: &CapacityReservationRequest{PoolID: "old-pool", Generators: &units},
	}
	for _, reservation := range []*CapacityReservation{approved, target} {
		if err := store.SaveCapacityReservation(reservation); err != nil {
			t.Fatalf("save %s: %v", reservation.ID, err)
		}
	}
	current, err := store.GetCapacityReservation(target.ID)
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	current.RequestedCapacity.PoolID = "new-pool"
	if err := store.UpdateCapacityReservationDetails(current, false); err != nil {
		t.Fatalf("move target to new pool: %v", err)
	}

	changed, overbooked, err := store.ApproveCapacityReservationWithinLimit(
		target.ID,
		time.Now().UTC(),
		func(reservation *CapacityReservation) (string, int) {
			return reservation.RequestedCapacity.PoolID, 2
		},
		func(reservation *CapacityReservation) int {
			return *reservation.RequestedCapacity.Generators
		},
	)
	if err != nil {
		t.Fatalf("approve moved target: %v", err)
	}
	if changed || !overbooked {
		t.Fatalf("changed=%v overbooked=%v, want false/true using target's current pool", changed, overbooked)
	}
}

func TestCapacityReservationRangeQueriesNormalizeNonUTCInstants(t *testing.T) {
	store := openTimeslotStore(t)
	local := time.FixedZone("UTC+2", 2*60*60)
	start := time.Date(2026, 7, 10, 9, 0, 0, 0, local)
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID:            "ts-offset",
		ProjectID:     "proj-a",
		StartAt:       start,
		EndAt:         start.Add(8 * time.Hour),
		ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("save offset reservation: %v", err)
	}
	overlaps, err := store.ListCapacityReservationsByTimeRange("proj-a",
		time.Date(2026, 7, 10, 7, 30, 0, 0, time.UTC),
		time.Date(2026, 7, 10, 8, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("range query: %v", err)
	}
	if len(overlaps) != 1 || overlaps[0].ID != "ts-offset" {
		t.Fatalf("overlaps = %+v, want non-UTC reservation found by UTC instant", overlaps)
	}
	if !overlaps[0].StartAt.Equal(time.Date(2026, 7, 10, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("stored start = %v, want normalized UTC instant", overlaps[0].StartAt)
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

	// Atomic reciprocal check-and-set: the first bind updates both rows and an
	// exact retry adopts that already-committed reciprocal pair.
	testID, status := "test-1", "QUEUED"
	bindAt := base.Add(5 * time.Minute)
	if err := store.SaveProject(&Project{ID: "proj-a", Name: "Project A", CreatedAt: bindAt, UpdatedAt: bindAt}); err != nil {
		t.Fatalf("save bind project: %v", err)
	}
	if err := store.SaveTest(&TestRecord{
		ID: testID, Name: testID, Status: status, ProjectID: "proj-a",
		StartTime: bindAt, Duration: int64(time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("save bind test: %v", err)
	}
	bound, err := store.UpdateCapacityReservationBinding("ts-0", &testID, &status, bindAt)
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if !bound {
		t.Fatal("first bind should succeed")
	}
	bound2, err := store.UpdateCapacityReservationBinding("ts-0", &testID, &status, bindAt)
	if err != nil {
		t.Fatalf("second bind: %v", err)
	}
	if !bound2 {
		t.Fatal("exact second bind must adopt the already-committed reciprocal pair")
	}
	rebound, err := store.GetCapacityReservation("ts-0")
	if err != nil {
		t.Fatalf("get after bind: %v", err)
	}
	if rebound.BoundTestID == nil || *rebound.BoundTestID != "test-1" {
		t.Fatalf("bound_test_id = %v, want test-1", rebound.BoundTestID)
	}
	boundTest, err := store.GetTest(testID)
	if err != nil || boundTest.TimeslotID == nil || *boundTest.TimeslotID != "ts-0" {
		t.Fatalf("test-side binding = (%#v, %v), want ts-0", boundTest, err)
	}

	if err := store.DeleteCapacityReservation("ts-0"); err == nil {
		t.Fatal("delete bound approved reservation succeeded")
	}
	if cleared, err := store.ClearCapacityReservationBinding("ts-0", testID, time.Now()); err != nil || !cleared {
		t.Fatalf("clear binding = %v, %v", cleared, err)
	}
	if changed, err := store.UpdateCapacityReservationApprovalState("ts-0", []string{"approved"}, "rejected", time.Now()); err != nil || !changed {
		t.Fatalf("reject before delete = %v, %v", changed, err)
	}
	if err := store.DeleteCapacityReservation("ts-0"); err != nil {
		t.Fatalf("delete rejected unbound reservation: %v", err)
	}
	if _, err := store.GetCapacityReservation("ts-0"); err == nil {
		t.Fatal("expected error fetching a deleted reservation")
	}
}
