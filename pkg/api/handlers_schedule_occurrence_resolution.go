package api

import (
	"errors"
	"net/http"
)

func (s *Server) handleResolveScenarioScheduleOccurrence(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req ScheduleOccurrenceResolutionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	occurrence, schedule, found, err := s.runner.ResolveScenarioScheduleOccurrence(
		r.PathValue("id"), r.PathValue("schedule_id"), r.PathValue("occurrence_id"), req,
	)
	if err != nil {
		writeScheduleOccurrenceResolutionError(w, err, found)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "scenario schedule occurrence not found")
		return
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	projectID := ""
	if schedule != nil {
		projectID = schedule.ProjectID
	}
	s.recordAuditEvent(r, "scenario.schedule.occurrence.resolve", "success", "scenario_schedule_occurrence", occurrence.ID, projectID, map[string]any{
		"scenario_id": r.PathValue("id"), "schedule_id": r.PathValue("schedule_id"), "action": req.Action,
		"reason": req.Reason, "resolved_by": actorSubject,
	})
	writeJSON(w, http.StatusOK, map[string]any{"occurrence": occurrence, "schedule": schedule})
}

func (s *Server) handleResolveExecutionPlanScheduleOccurrence(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req ScheduleOccurrenceResolutionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	occurrence, schedule, found, err := s.runner.ResolveExecutionPlanScheduleOccurrence(
		r.PathValue("id"), r.PathValue("schedule_id"), r.PathValue("occurrence_id"), req,
	)
	if err != nil {
		writeScheduleOccurrenceResolutionError(w, err, found)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "execution-plan schedule occurrence not found")
		return
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	projectID := ""
	if schedule != nil {
		projectID = schedule.ProjectID
	}
	s.recordAuditEvent(r, "execution_plan.schedule.occurrence.resolve", "success", "execution_plan_schedule_occurrence", occurrence.ID, projectID, map[string]any{
		"plan_id": r.PathValue("id"), "schedule_id": r.PathValue("schedule_id"), "action": req.Action,
		"reason": req.Reason, "resolved_by": actorSubject,
	})
	writeJSON(w, http.StatusOK, map[string]any{"occurrence": occurrence, "schedule": schedule})
}

func writeScheduleOccurrenceResolutionError(w http.ResponseWriter, err error, found bool) {
	switch {
	case errors.Is(err, errNotExecutionLeader):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, errScheduleConcurrentChange), errors.Is(err, errScheduleOccurrenceNotResolvable):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errScheduleOccurrenceResolutionInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case !found:
		writeError(w, http.StatusNotFound, "schedule occurrence not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
