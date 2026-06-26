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
	"encoding/json"
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

	// Mutable fields only; identity, ownership, and creation time are preserved.
	// Approval transitions otherwise go through the dedicated project-admin
	// approve/reject endpoints, not a general editor update.
	existing.StartAt = req.StartAt
	existing.EndAt = req.EndAt
	existing.Timezone = req.Timezone
	existing.ScenarioID = req.ScenarioID
	existing.RequestedCapacity = req.RequestedCapacity
	if termsChanged && existing.ApprovalState == "approved" {
		existing.ApprovalState = "pending"
		existing.BoundTestID = nil
		existing.BoundTestStatus = nil
	}
	_, existing.ConflictSummary = s.summarizeReservationConflicts(existing.ProjectID, existing.ID, existing.StartAt, existing.EndAt)

	if err := s.store.SaveCapacityReservation(existing); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
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

// decodeReservationApprovalRequest reads the optional approve-body override flag.
// The approve endpoint historically took no body, so a missing/empty/invalid body
// degrades to the zero value (no override).
func decodeReservationApprovalRequest(r *http.Request) reservationApprovalRequest {
	var req reservationApprovalRequest
	if r.Body == nil {
		return req
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	return req
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
	details := map[string]any{"approval_state": state}
	// Guard approval against overbooking: approving a reservation whose reserved
	// generators, combined with the already-approved reservations overlapping its
	// window, exceed the configured pool would let two bound timeslots claim
	// capacity that does not exist. An admin can override with an explicit reason.
	if state == "approved" {
		approval := decodeReservationApprovalRequest(r)
		if approval.ConflictOverride {
			if strings.TrimSpace(approval.Reason) == "" {
				writeError(w, http.StatusBadRequest, "conflict_override requires a reason")
				return
			}
			details["conflict_override"] = true
			details["override_reason"] = strings.TrimSpace(approval.Reason)
		} else if msg := s.reservationOverbookingConflict(reservation); msg != "" {
			writeError(w, http.StatusConflict, msg+"; pass conflict_override with a reason to approve anyway")
			return
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
	updated, err := s.store.UpdateCapacityReservationApprovalState(reservation.ID, fromStates, state, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "timeslot approval state changed concurrently; reload and retry")
		return
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
	var pool int
	if poolID != "" {
		pool = s.runner.PoolGeneratorEnvelope(poolID)
	} else {
		pool = resolveCapacityAvailabilityAt(settings, reservation.StartAt.UTC()).EffectiveAvailableGenerators
	}
	if pool <= 0 {
		// No envelope to enforce against: no configured global pool, or an empty/
		// unknown fleet pool (the run-time admission gate re-validates either way).
		return ""
	}
	overlaps, err := s.store.ListCapacityReservationsByTimeRange("", reservation.StartAt, reservation.EndAt)
	if err != nil {
		return "reservation capacity state is temporarily unavailable; try again"
	}
	total := required
	for _, o := range overlaps {
		if o == nil || o.ID == reservation.ID || o.ApprovalState != "approved" {
			continue
		}
		if timeslotPoolID(o) != poolID {
			continue // different capacity envelope — don't cross-count
		}
		total += timeslotReservedGenerators(settings, o)
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
	reservation, ok := s.loadCapacityReservation(w, r)
	if !ok {
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
	// Fast, clearly-messaged rejection for the common case; the binding update
	// re-checks the approved state atomically to close the TOCTOU window.
	if reservation.ApprovalState != "approved" {
		writeError(w, http.StatusConflict, "timeslot must be approved before it can be bound")
		return
	}
	test, err := s.store.GetTest(req.TestID)
	if err != nil || test == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if normalizeProjectID(test.ProjectID) != normalizeProjectID(reservation.ProjectID) {
		writeError(w, http.StatusBadRequest, "test belongs to a different project than the timeslot")
		return
	}
	// Enforce the same window-fit and VUser-capacity invariants the create-time
	// bind applies, so a test cannot be bound to a timeslot it overflows or runs
	// outside of.
	start := time.Now()
	if test.ScheduledStartAt != nil && !test.ScheduledStartAt.IsZero() {
		start = *test.ScheduledStartAt
	}
	end := start.Add(time.Duration(test.Duration))
	if err := reservationFitError(reservation, start, end, test.TotalVUsers); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := test.Status
	bound, err := s.store.UpdateCapacityReservationBinding(reservation.ID, &req.TestID, &status, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !bound {
		// The atomic update binds only an approved, not-yet-bound reservation, so a
		// miss here means it was concurrently bound or its approval was revoked.
		writeError(w, http.StatusConflict, "timeslot is no longer bindable (already bound or not approved)")
		return
	}
	s.recordAuditEvent(r, "timeslot.bind", "success", "timeslot", reservation.ID, reservation.ProjectID, map[string]any{
		"test_id": req.TestID,
	})
	updated, err := s.store.GetCapacityReservation(reservation.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
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
