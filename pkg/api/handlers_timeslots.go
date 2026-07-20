package api

// Capacity reservations (timeslots): forward-booking of a future capacity
// window — the LoadRunner Enterprise "timeslot" analog. Reservations carry an
// approval state (pending → approved → rejected) and may be bound to exactly one
// test run. Approval is a project-admin action via the dedicated approve/reject
// endpoints — it cannot be self-granted by an editor through create or update —
// and binding is enforced atomically against the approved state. Admission-control
// enforcement (making the scheduler reservation-aware) is a separate,
// concurrency-sensitive change tracked under the capacity_reservation parity
// dimension.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultTimeslotListLimit = 100

func validateReservationTimezone(tz string) error {
	if strings.TrimSpace(tz) == "" {
		return nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return nil
}

// validateReservationWindow enforces a non-empty, well-ordered booking window.
func validateReservationWindow(start, end time.Time) error {
	if start.IsZero() || end.IsZero() {
		return errors.New("start_at and end_at are required")
	}
	if !start.Before(end) {
		return errors.New("start_at must be before end_at")
	}
	return nil
}

func validateReservationCapacity(capacity *storage.CapacityReservationRequest) error {
	if capacity == nil {
		return nil
	}
	if capacity.Generators != nil && *capacity.Generators <= 0 {
		return errors.New("requested_capacity.generators must be greater than zero")
	}
	if capacity.Generators != nil && *capacity.Generators > maxCapacityUnit {
		return fmt.Errorf("requested_capacity.generators must not exceed %d", maxCapacityUnit)
	}
	if capacity.VUsers != nil && *capacity.VUsers <= 0 {
		return errors.New("requested_capacity.vusers must be greater than zero")
	}
	if capacity.VUsers != nil && *capacity.VUsers > maxManagedTestVUsers {
		return fmt.Errorf("requested_capacity.vusers must not exceed %d", maxManagedTestVUsers)
	}
	return nil
}

// reservationCapacityEqual reports whether two requested-capacity envelopes
// reserve the same generators/VUsers from the same pool (the admission-relevant
// terms). The PoolID is included because it selects which capacity envelope the
// reservation draws from, so repointing the pool changes what is protected and
// must re-trigger approval. Protocol and load-zone labels do not affect reserved
// capacity and are ignored.
func reservationCapacityEqual(a, b *storage.CapacityReservationRequest) bool {
	if a == nil || b == nil {
		return a == b
	}
	return intPtrEqual(a.Generators, b.Generators) && intPtrEqual(a.VUsers, b.VUsers) &&
		strings.TrimSpace(a.PoolID) == strings.TrimSpace(b.PoolID)
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// summarizeReservationConflicts counts active (non-rejected) reservations that
// overlap the window, excluding the reservation being edited. A storage error is
// logged and treated as "no known conflict" so an advisory check never blocks a
// create/update, but the failure is not silently swallowed.
func (s *Server) summarizeReservationConflicts(projectID, excludeID string, start, end time.Time) ([]*storage.CapacityReservation, string) {
	if s.store == nil {
		return nil, ""
	}
	overlaps, err := s.store.ListCapacityReservationsByTimeRange(projectID, start, end)
	if err != nil {
		slog.Warn("failed to check capacity reservation conflicts", "project_id", projectID, "err", err)
		return nil, ""
	}
	active := make([]*storage.CapacityReservation, 0, len(overlaps))
	for _, o := range overlaps {
		if o.ID == excludeID || o.ApprovalState == "rejected" {
			continue
		}
		active = append(active, o)
	}
	if len(active) == 0 {
		return active, ""
	}
	return active, fmt.Sprintf("overlaps %d active reservation(s) in this window", len(active))
}

func (s *Server) handleCreateCapacityReservation(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.CapacityReservation
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	req.ProjectID = normalizeProjectID(req.ProjectID)
	if err := validateReservationWindow(req.StartAt, req.EndAt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateReservationTimezone(req.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateReservationCapacity(req.RequestedCapacity); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A pool-scoped reservation must reference a real fleet pool, otherwise it is
	// silently inert: excluded from the global reservation math yet bounded by an
	// empty/unknown pool envelope at admission, so it protects no capacity.
	if req.RequestedCapacity != nil {
		if poolID := strings.TrimSpace(req.RequestedCapacity.PoolID); poolID != "" && !s.runner.FleetPoolExists(poolID) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("requested_capacity.pool_id references unknown fleet pool %q", poolID))
			return
		}
	}
	// Reservations always start pending; approval is a project-admin action via the
	// dedicated approve endpoint, so approved state cannot be self-granted at create.
	req.ApprovalState = "pending"
	req.ID = uuid.New().String()
	// Binding is established only via the bind endpoint.
	req.BoundTestID = nil
	req.BoundTestStatus = nil
	now := time.Now()
	req.CreatedAt = now
	req.UpdatedAt = now
	_, req.ConflictSummary = s.summarizeReservationConflicts(req.ProjectID, req.ID, req.StartAt, req.EndAt)

	if err := s.store.SaveCapacityReservation(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "timeslot.create", "success", "timeslot", req.ID, req.ProjectID, map[string]any{
		"start_at": req.StartAt, "end_at": req.EndAt,
	})
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleListCapacityReservations(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	limit, offset := parseLimitOffsetQuery(r, defaultTimeslotListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"timeslots": []*storage.CapacityReservation{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	reservations, err := s.store.ListCapacityReservations(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if reservations == nil {
		reservations = []*storage.CapacityReservation{}
	}
	for _, res := range reservations {
		s.refreshReservationBindingStatus(res)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"timeslots": reservations,
		"count":     len(reservations),
		"limit":     limit,
		"offset":    offset,
	})
}

func (s *Server) handleGetCapacityReservation(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	reservation, ok := s.loadCapacityReservation(w, r)
	if !ok {
		return
	}
	s.refreshReservationBindingStatus(reservation)
	writeJSON(w, http.StatusOK, reservation)
}

// refreshReservationBindingStatus updates a reservation's surfaced
// bound_test_status to the bound test's current status. The value is captured
// once at bind time and never re-persisted, so without this a finished test
// would be reported as QUEUED/RUNNING forever; if the bound test was deleted the
// status is reported as DELETED so a dangling binding is visible. This is a
// display-only refresh on a per-request copy — the stored row is unchanged and
// the intentional whole-window capacity hold (see timeslot_reservation.go) is
// unaffected.
func (s *Server) refreshReservationBindingStatus(res *storage.CapacityReservation) {
	if res == nil || res.BoundTestID == nil || strings.TrimSpace(*res.BoundTestID) == "" {
		return
	}
	test, err := s.store.GetTest(*res.BoundTestID)
	if err != nil || test == nil {
		deleted := "DELETED"
		res.BoundTestStatus = &deleted
		return
	}
	status := test.Status
	res.BoundTestStatus = &status
}

func (s *Server) handleUpdateCapacityReservation(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	existing, ok := s.loadCapacityReservation(w, r)
	if !ok {
		return
	}
	var req storage.CapacityReservation
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := validateReservationWindow(req.StartAt, req.EndAt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateReservationTimezone(req.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateReservationCapacity(req.RequestedCapacity); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Same guard as create: an edit must not repoint the reservation at a
	// non-existent fleet pool (which would silently protect no capacity).
	if req.RequestedCapacity != nil {
		if poolID := strings.TrimSpace(req.RequestedCapacity.PoolID); poolID != "" && !s.runner.FleetPoolExists(poolID) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("requested_capacity.pool_id references unknown fleet pool %q", poolID))
			return
		}
	}
	// The window and reserved capacity are the load-bearing, admin-approved
	// terms: an approved reservation's generators block other tenants. If an
	// editor changes any of them, the reservation must be re-approved — otherwise
	// an editor could enlarge an admin-approved slot (e.g. 2 gen/1h → 40 gen/12h)
	// and keep it approved+bound. Cosmetic edits (timezone/scenario) do not retrigger approval.
	termsChanged := !existing.StartAt.Equal(req.StartAt) ||
		!existing.EndAt.Equal(req.EndAt) ||
		!reservationCapacityEqual(existing.RequestedCapacity, req.RequestedCapacity)
	previousBoundTestID := strings.TrimSpace(trimPtr(existing.BoundTestID))

	// Mutable fields only; identity, ownership, and creation time are preserved.
	// Approval transitions otherwise go through the dedicated project-admin
	// approve/reject endpoints, not a general editor update.
	existing.StartAt = req.StartAt
	existing.EndAt = req.EndAt
	existing.Timezone = req.Timezone
	existing.ScenarioID = req.ScenarioID
	existing.RequestedCapacity = req.RequestedCapacity
	resetApproval := termsChanged && existing.ApprovalState == "approved"
	if termsChanged && existing.ApprovalState == "approved" {
		existing.ApprovalState = "pending"
		existing.ApprovalID = nil
		existing.BoundTestID = nil
		existing.BoundTestStatus = nil
	}
	_, existing.ConflictSummary = s.summarizeReservationConflicts(existing.ProjectID, existing.ID, existing.StartAt, existing.EndAt)

	if err := s.store.UpdateCapacityReservationDetails(existing, resetApproval); err != nil {
		if errors.Is(err, storage.ErrCapacityReservationBoundActiveTest) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if resetApproval && previousBoundTestID != "" {
		s.runner.clearLiveCapacityReservationBinding(previousBoundTestID, existing.ID)
	}
	if fresh, err := s.store.GetCapacityReservation(existing.ID); err == nil && fresh != nil {
		existing = fresh
	}
	s.recordAuditEvent(r, "timeslot.update", "success", "timeslot", existing.ID, existing.ProjectID, nil)
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteCapacityReservation(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	if reservation, err := s.store.GetCapacityReservation(id); err == nil && reservation != nil {
		projectID = reservation.ProjectID
	}
	if err := s.store.DeleteCapacityReservation(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "timeslot.delete", "success", "timeslot", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// handleApproveCapacityReservation and handleRejectCapacityReservation gate the
// approval transition behind project-admin access (enforced by the route catalog),
// mirroring the scenario-schedule approval workflow.
func (s *Server) handleApproveCapacityReservation(w http.ResponseWriter, r *http.Request) {
	s.transitionReservationApproval(w, r, "approved", "timeslot.approve")
}

func (s *Server) handleRejectCapacityReservation(w http.ResponseWriter, r *http.Request) {
	s.transitionReservationApproval(w, r, "rejected", "timeslot.reject")
}

type reservationApprovalRequest struct {
	ConflictOverride bool   `json:"conflict_override"`
	Reason           string `json:"reason"`
}

type atomicCapacityApprovalStore interface {
	ApproveCapacityReservationWithinLimit(
		id string,
		updatedAt time.Time,
		capacity func(*storage.CapacityReservation) (envelope string, maxUnits int),
		units func(*storage.CapacityReservation) int,
	) (changed bool, overbooked bool, err error)
}

type capacityFencedApprovalStore interface {
	ApproveCapacityReservationWithCapacityFence(
		id string,
		updatedAt time.Time,
		settingKey string,
		evaluate func(target *storage.CapacityReservation, approved []*storage.CapacityReservation, settingValue string) (bool, error),
	) (changed bool, overbooked bool, err error)
}

func (s *Server) transitionReservationApproval(w http.ResponseWriter, r *http.Request, state, action string) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	reservation, ok := s.loadCapacityReservation(w, r)
	if !ok {
		return
	}
	previousBoundTestID := strings.TrimSpace(trimPtr(reservation.BoundTestID))
	details := map[string]any{"approval_state": state}
	approval := reservationApprovalRequest{}
	// Guard approval against overbooking: approving a reservation whose reserved
	// generators, combined with the already-approved reservations overlapping its
	// window, exceed the configured pool would let two bound timeslots claim
	// capacity that does not exist. An admin can override with an explicit reason.
	if state == "approved" {
		if !decodeOptionalJSONBody(w, r, &approval) {
			return
		}
		if approval.ConflictOverride {
			if strings.TrimSpace(approval.Reason) == "" {
				writeError(w, http.StatusBadRequest, "conflict_override requires a reason")
				return
			}
			details["conflict_override"] = true
			details["override_reason"] = strings.TrimSpace(approval.Reason)
		} else {
			_, atomic := s.store.(atomicCapacityApprovalStore)
			_, fenced := s.store.(capacityFencedApprovalStore)
			if !atomic && !fenced {
				if msg := s.reservationOverbookingConflict(reservation); msg != "" {
					writeError(w, http.StatusConflict, msg+"; pass conflict_override with a reason to approve anyway")
					return
				}
			}
		}
	}
	// Atomic compare-and-set instead of read-modify-write so two concurrent
	// approve/reject calls (or an approve landing after a reject) cannot clobber
	// each other. Approve only fires on a still-pending slot; reject can also
	// revoke an already-approved slot.
	fromStates := []string{"pending", "approved"}
	if state == "approved" {
		fromStates = []string{"pending"}
	}
	now := time.Now().UTC()
	var (
		updated bool
		err     error
	)
	if state == "approved" && !approval.ConflictOverride {
		// Approval is a settings-dependent mutation. Bypass the normal HA read
		// throttle before taking a snapshot; the capacity-fenced store path below
		// then re-reads the same durable value while holding the cross-write lock.
		s.runner.refreshSystemSettingsFromStore(true)
		if _, fenced := s.store.(capacityFencedApprovalStore); fenced {
			settings := s.runner.GetSystemSettings()
			poolEnvelopes, snapshotErr := s.runner.PoolGeneratorEnvelopeSnapshot()
			if snapshotErr != nil {
				writeError(w, http.StatusServiceUnavailable, "fleet capacity is temporarily unavailable")
				return
			}
			fencedStore := s.store.(capacityFencedApprovalStore)
			var overbooked bool
			updated, overbooked, err = fencedStore.ApproveCapacityReservationWithCapacityFence(
				reservation.ID,
				now,
				systemSettingsStoreKey,
				func(target *storage.CapacityReservation, approved []*storage.CapacityReservation, settingValue string) (bool, error) {
					durableSettings, err := systemSettingsFromStoredValue(settingValue, settings)
					if err != nil {
						return false, err
					}
					after := make([]*storage.CapacityReservation, 0, len(approved)+1)
					after = append(after, approved...)
					after = append(after, target)
					return capacityChangeOverbookingConflict(durableSettings, durableSettings, approved, after, poolEnvelopes) != "", nil
				},
			)
			if err == nil && overbooked {
				writeError(w, http.StatusConflict, "approving this timeslot would overbook its capacity envelope; pass conflict_override with a reason to approve anyway")
				return
			}
		} else if atomicStore, ok := s.store.(atomicCapacityApprovalStore); ok {
			settings := s.runner.GetSystemSettings()
			poolEnvelopes, snapshotErr := s.runner.PoolGeneratorEnvelopeSnapshot()
			if snapshotErr != nil {
				writeError(w, http.StatusServiceUnavailable, "fleet capacity is temporarily unavailable")
				return
			}
			var overbooked bool
			updated, overbooked, err = atomicStore.ApproveCapacityReservationWithinLimit(
				reservation.ID,
				now,
				func(candidate *storage.CapacityReservation) (string, int) {
					poolID := timeslotPoolID(candidate)
					if poolID != "" {
						if limit, known := poolEnvelopes[poolID]; known {
							return poolID, limit
						}
						return poolID, -1
					}
					return "", resolveCapacityAvailabilityAt(settings, candidate.StartAt.UTC()).EffectiveAvailableGenerators
				},
				func(candidate *storage.CapacityReservation) int {
					units := timeslotReservedGenerators(settings, candidate)
					// The storage transaction sums approved timeslots. Add the
					// settings-backed reservations once, on the target row, so the
					// same atomic comparison also protects capacity already promised
					// outside the timeslot table.
					if candidate != nil && candidate.ID == reservation.ID {
						units = saturatingAddNonNegative(units, settingsReservedGeneratorsOverlapping(
							settings,
							timeslotPoolID(candidate),
							candidate.StartAt,
							candidate.EndAt,
						))
					}
					return units
				},
			)
			if err == nil && overbooked {
				writeError(w, http.StatusConflict, "approving this timeslot would overbook its capacity envelope; pass conflict_override with a reason to approve anyway")
				return
			}
		} else {
			updated, err = s.store.UpdateCapacityReservationApprovalState(reservation.ID, fromStates, state, now)
		}
	} else {
		updated, err = s.store.UpdateCapacityReservationApprovalState(reservation.ID, fromStates, state, now)
	}
	if err != nil {
		if errors.Is(err, storage.ErrCapacityReservationBoundActiveTest) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "timeslot approval state changed concurrently; reload and retry")
		return
	}
	if state != "approved" && previousBoundTestID != "" {
		s.runner.clearLiveCapacityReservationBinding(previousBoundTestID, reservation.ID)
	}
	if fresh, err := s.store.GetCapacityReservation(reservation.ID); err == nil && fresh != nil {
		reservation = fresh
	} else {
		reservation.ApprovalState = state
		reservation.UpdatedAt = now
	}
	s.recordAuditEvent(r, action, "success", "timeslot", reservation.ID, reservation.ProjectID, details)
	writeJSON(w, http.StatusOK, reservation)
}

// settingsReservedGeneratorsOverlapping returns the settings-backed generator
// reservations that draw from poolID and overlap [start, end). Settings
// reservations and persisted timeslots protect the same finite envelope, so an
// approval must account for both. Capacity envelopes are intentionally isolated:
// global reservations do not consume a fleet pool and reservations for one fleet
// pool do not consume another.
func settingsReservedGeneratorsOverlapping(settings SystemSettings, poolID string, start, end time.Time) int {
	poolID = strings.TrimSpace(poolID)
	total := 0
	for _, reservation := range normalizeCapacityReservations(settings.CapacityPlanning.Reservations) {
		if reservation.ReservedGenerators <= 0 || strings.TrimSpace(reservation.PoolID) != poolID {
			continue
		}
		// Half-open interval overlap. Nil bounds are unbounded.
		if reservation.EndsAt != nil && !reservation.EndsAt.After(start) {
			continue
		}
		if reservation.StartsAt != nil && !reservation.StartsAt.Before(end) {
			continue
		}
		total = saturatingAddNonNegative(total, reservation.ReservedGenerators)
	}
	return total
}

// reservationOverbookingConflict returns a non-empty message when approving the
// reservation would over-commit the configured generator pool across the
// approved reservations overlapping its window. A storage failure fails closed so
// an approval is never granted against unknown reservation state.
func (s *Server) reservationOverbookingConflict(reservation *storage.CapacityReservation) string {
	settings := s.runner.GetSystemSettings()
	required := timeslotReservedGenerators(settings, reservation)
	if required <= 0 {
		return ""
	}
	// A pool-scoped reservation draws from (and is compared only against) its fleet
	// pool's member envelope; a global reservation draws from the configured global
	// pool and ignores pool-scoped overlaps entirely. This mirrors the admission
	// accounting, which keeps the two envelopes separate — without it a pool slot is
	// checked against the wrong pool and cross-counts unrelated envelopes.
	poolID := timeslotPoolID(reservation)
	var (
		pool  int
		known bool
	)
	if poolID != "" {
		poolEnvelopes, err := s.runner.PoolGeneratorEnvelopeSnapshot()
		if err != nil {
			return "reservation capacity state is temporarily unavailable; try again"
		}
		pool, known = poolEnvelopes[poolID]
	} else {
		pool = resolveCapacityAvailabilityAt(settings, reservation.StartAt.UTC()).EffectiveAvailableGenerators
		known = true
	}
	if !known {
		// A custom/no-fleet compatibility store cannot size this envelope. The
		// run-time assignment path will still reject an unusable pool.
		return ""
	}
	overlaps, err := s.store.ListCapacityReservationsByTimeRange("", reservation.StartAt, reservation.EndAt)
	if err != nil {
		return "reservation capacity state is temporarily unavailable; try again"
	}
	total := saturatingAddNonNegative(required, settingsReservedGeneratorsOverlapping(settings, poolID, reservation.StartAt, reservation.EndAt))
	for _, o := range overlaps {
		if o == nil || o.ID == reservation.ID || o.ApprovalState != "approved" {
			continue
		}
		if timeslotPoolID(o) != poolID {
			continue // different capacity envelope — don't cross-count
		}
		total = saturatingAddNonNegative(total, timeslotReservedGenerators(settings, o))
	}
	if total > pool {
		if poolID != "" {
			return fmt.Sprintf("approving this timeslot would reserve %d generators in fleet pool %q during an overlapping window but the pool has only %d", total, poolID, pool)
		}
		return fmt.Sprintf("approving this timeslot would reserve %d generators in an overlapping window but only %d are available", total, pool)
	}
	return ""
}

func (s *Server) handleCheckCapacityAvailability(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req struct {
		ProjectID string    `json:"project_id"`
		StartAt   time.Time `json:"start_at"`
		EndAt     time.Time `json:"end_at"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	req.ProjectID = normalizeProjectID(req.ProjectID)
	if err := validateReservationWindow(req.StartAt, req.EndAt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	conflicts, summary := s.summarizeReservationConflicts(req.ProjectID, "", req.StartAt, req.EndAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"available": len(conflicts) == 0,
		"conflicts": conflicts,
		"count":     len(conflicts),
		"summary":   summary,
	})
}

func (s *Server) handleBindCapacityTimeslot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	if !s.requireExecutionLeader(w) {
		return
	}
	var req struct {
		TestID string `json:"test_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.TestID = strings.TrimSpace(req.TestID)
	if req.TestID == "" {
		writeError(w, http.StatusBadRequest, "test_id is required")
		return
	}
	reservation, result, err := s.runner.BindCapacityTimeslot(r.PathValue("id"), req.TestID)
	if err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, "this controller is not the execution leader")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result != storage.CapacityBindingBound {
		status := http.StatusConflict
		switch result {
		case storage.CapacityBindingReservationMissing, storage.CapacityBindingTestMissing:
			status = http.StatusNotFound
		case storage.CapacityBindingProjectMismatch, storage.CapacityBindingWindowMismatch,
			storage.CapacityBindingCapacityExceeded, storage.CapacityBindingTestNotBindable:
			status = http.StatusBadRequest
		case storage.CapacityBindingFenced:
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, capacityBindingResultError(result).Error())
		return
	}
	s.recordAuditEvent(r, "timeslot.bind", "success", "timeslot", reservation.ID, reservation.ProjectID, map[string]any{
		"test_id": req.TestID,
	})
	writeJSON(w, http.StatusOK, reservation)
}

// loadCapacityReservation fetches the {id} reservation, writing the appropriate
// error response and returning ok=false when it cannot be loaded.
func (s *Server) loadCapacityReservation(w http.ResponseWriter, r *http.Request) (*storage.CapacityReservation, bool) {
	reservation, err := s.store.GetCapacityReservation(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		msg := "failed to load timeslot"
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
			msg = "timeslot not found"
		}
		writeError(w, status, msg)
		return nil, false
	}
	return reservation, true
}
