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
	writeJSON(w, http.StatusOK, reservation)
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
	// Mutable fields only; identity, ownership, binding, creation time, and the
	// approval state are preserved. Approval transitions go through the dedicated
	// project-admin approve/reject endpoints, not a general editor update.
	existing.StartAt = req.StartAt
	existing.EndAt = req.EndAt
	existing.Timezone = req.Timezone
	existing.ScenarioID = req.ScenarioID
	existing.RequestedCapacity = req.RequestedCapacity
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

func (s *Server) transitionReservationApproval(w http.ResponseWriter, r *http.Request, state, action string) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	reservation, ok := s.loadCapacityReservation(w, r)
	if !ok {
		return
	}
	reservation.ApprovalState = state
	if err := s.store.SaveCapacityReservation(reservation); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, action, "success", "timeslot", reservation.ID, reservation.ProjectID, map[string]any{
		"approval_state": state,
	})
	writeJSON(w, http.StatusOK, reservation)
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
