package api

// Capacity timeslot binding + reservation-aware admission. An approved, bound
// timeslot is treated as a transient, time-windowed per-project generator
// reservation: its reserved generators are fed into the same admission math that
// already protects static (settings-based) reservations, so other tenants are
// blocked from the capacity it holds during its window. With no bound timeslots
// active, the contribution is zero and admission behaves exactly as before.

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// validateAndBindTimeslotLocked binds a test to an approved capacity timeslot as
// part of creation. It is strict: any validation failure (or a lost atomic bind)
// returns an error so the run is never created unprotected. Caller holds tr.mu.
func (tr *TestRunner) validateAndBindTimeslotLocked(timeslotID string, test *ManagedTest) error {
	if tr.reservationStore == nil {
		return fmt.Errorf("capacity timeslots are not available")
	}
	reservation, err := tr.reservationStore.GetCapacityReservation(timeslotID)
	if err != nil || reservation == nil {
		return fmt.Errorf("timeslot %s not found", timeslotID)
	}
	if reservation.ApprovalState != "approved" {
		return fmt.Errorf("timeslot must be approved before it can be bound")
	}
	if normalizeProjectID(reservation.ProjectID) != normalizeProjectID(test.ProjectID) {
		return fmt.Errorf("timeslot belongs to a different project than the test")
	}
	start := time.Now()
	if test.ScheduledStartAt != nil && !test.ScheduledStartAt.IsZero() {
		start = *test.ScheduledStartAt
	}
	end := start.Add(test.Duration)
	if err := reservationFitError(reservation, start, end, test.TotalVUsers); err != nil {
		return err
	}
	status := test.Status
	bound, err := tr.reservationStore.UpdateCapacityReservationBinding(reservation.ID, &test.ID, &status, time.Now())
	if err != nil {
		return fmt.Errorf("bind timeslot: %w", err)
	}
	if !bound {
		return fmt.Errorf("timeslot is no longer bindable (already bound or not approved)")
	}
	test.TimeslotID = &reservation.ID
	return nil
}

// launchTimeslotRevalidationErrorLocked re-checks a run's bound capacity timeslot
// at the actual launch instant. A timeslot is validated and bound at create time,
// but between then and launch it can be rejected, re-bound, resized, or the run's
// effective window can drift — so trusting the bind-time fit lets a run launch
// into capacity it no longer holds. Returns a non-nil error (blocking the start)
// when the bound slot is gone, no longer approved, no longer bound to this run, or
// no longer fits. Runs without a bound timeslot are unaffected.
func (tr *TestRunner) launchTimeslotRevalidationErrorLocked(test *ManagedTest, now time.Time) error {
	if test == nil || test.TimeslotID == nil || tr.reservationStore == nil {
		return nil
	}
	slotID := strings.TrimSpace(*test.TimeslotID)
	if slotID == "" {
		return nil
	}
	reservation, err := tr.reservationStore.GetCapacityReservation(slotID)
	if err != nil || reservation == nil {
		return fmt.Errorf("bound capacity timeslot %q is no longer available", slotID)
	}
	if reservation.ApprovalState != "approved" {
		return fmt.Errorf("bound capacity timeslot %q is no longer approved (state %q)", slotID, reservation.ApprovalState)
	}
	if reservation.BoundTestID == nil || *reservation.BoundTestID != test.ID {
		return fmt.Errorf("bound capacity timeslot %q is no longer bound to this run", slotID)
	}
	launchStart := now.UTC()
	if test.ScheduledStartAt != nil {
		launchStart = test.ScheduledStartAt.UTC()
	}
	launchEnd := launchStart.Add(test.Duration)
	if err := reservationFitError(reservation, launchStart, launchEnd, test.TotalVUsers); err != nil {
		return fmt.Errorf("bound capacity timeslot %q no longer fits this run: %w", slotID, err)
	}
	return nil
}

// reservationFitError reports why a run with the given window and VUser count does
// not fit a reservation (nil if it fits). Both bind paths — create-time
// (validateAndBindTimeslotLocked) and the standalone bind endpoint — call it so
// they enforce identical window-fit and capacity invariants.
func reservationFitError(reservation *storage.CapacityReservation, start, end time.Time, totalVUsers int) error {
	if start.Before(reservation.StartAt) || end.After(reservation.EndAt) {
		return fmt.Errorf("the run window does not fit within the reserved timeslot")
	}
	if reservation.RequestedCapacity != nil && reservation.RequestedCapacity.VUsers != nil &&
		totalVUsers > *reservation.RequestedCapacity.VUsers {
		return fmt.Errorf("the run requests %d VUsers but the timeslot reserves only %d", totalVUsers, *reservation.RequestedCapacity.VUsers)
	}
	return nil
}

// boundTimeslotsActiveAtLocked returns the approved, bound timeslots whose window
// contains the instant `at`. Caller holds tr.mu. The error is propagated (not
// swallowed) so admission can fail CLOSED: silently treating a lookup failure as
// "no reservations" would drop every timeslot's protection and let other tenants
// into reserved capacity.
func (tr *TestRunner) boundTimeslotsActiveAtLocked(at time.Time) ([]*storage.CapacityReservation, error) {
	if tr.reservationStore == nil {
		return nil, nil
	}
	// Half-open overlap with [at, at+1ns) selects reservations where
	// start_at <= at < end_at.
	all, err := tr.reservationStore.ListCapacityReservationsByTimeRange("", at, at.Add(time.Nanosecond))
	if err != nil {
		slog.Warn("capacity admission: bound-timeslot lookup failed; failing closed", "error", err, "at", at)
		return nil, err
	}
	out := make([]*storage.CapacityReservation, 0, len(all))
	for _, ts := range all {
		if ts == nil || ts.ApprovalState != "approved" || ts.BoundTestID == nil || strings.TrimSpace(*ts.BoundTestID) == "" {
			continue
		}
		out = append(out, ts)
	}
	return out, nil
}

// boundTimeslotBoundariesInWindowLocked returns the start/end instants of the
// approved, bound timeslots overlapping the launch window. They are fed into the
// launch-window sample set so the reservation-aware capacity check is evaluated
// at the exact instants a timeslot's capacity hold begins or ends, rather than
// only at fixed event/window boundaries. A lookup failure degrades to no extra
// boundaries — the per-sample reservation check still fails closed on its own.
func (tr *TestRunner) boundTimeslotBoundariesInWindowLocked(window LaunchWindow) []time.Time {
	if tr.reservationStore == nil {
		return nil
	}
	slots, err := tr.reservationStore.ListCapacityReservationsByTimeRange("", window.StartAt, window.EndAt)
	if err != nil {
		slog.Warn("launch window: bound-timeslot boundary lookup failed", "error", err)
		return nil
	}
	out := make([]time.Time, 0, len(slots)*2)
	for _, ts := range slots {
		if ts == nil || ts.ApprovalState != "approved" || ts.BoundTestID == nil || strings.TrimSpace(*ts.BoundTestID) == "" {
			continue
		}
		out = append(out, ts.StartAt.UTC(), ts.EndAt.UTC())
	}
	return out
}

// reservationTotalsWithTimeslots returns (totalReserved, projectReserved)
// generators at `at`, combining settings-based reservations with the supplied
// active bound timeslots.
func reservationTotalsWithTimeslots(settings SystemSettings, projectID string, at time.Time, activeSlots []*storage.CapacityReservation) (int, int) {
	totalReserved, projectReserved := capacityReservationTotalsAt(settings, projectID, at)
	for _, ts := range activeSlots {
		g := timeslotReservedGenerators(settings, ts)
		if g <= 0 {
			continue
		}
		totalReserved += g
		if normalizeProjectID(ts.ProjectID) == normalizeProjectID(projectID) {
			projectReserved += g
		}
	}
	return totalReserved, projectReserved
}

// timeslotReservedGenerators converts a timeslot's requested capacity into
// generators: an explicit generator count if given, else derived from the
// reserved VUsers via the configured per-generator density.
func timeslotReservedGenerators(settings SystemSettings, ts *storage.CapacityReservation) int {
	if ts == nil || ts.RequestedCapacity == nil {
		return 0
	}
	if g := ts.RequestedCapacity.Generators; g != nil && *g > 0 {
		return *g
	}
	if v := ts.RequestedCapacity.VUsers; v != nil && *v > 0 {
		perGen := settings.CapacityPlanning.Generator.MaxVUsers
		if perGen <= 0 {
			perGen = 10000
		}
		return (*v + perGen - 1) / perGen // ceil
	}
	return 0
}
