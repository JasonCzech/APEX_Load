package api

import (
	"errors"
	"net/http"
)

func (s *Server) handleCreateExecutionPlanSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateExecutionPlanScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	planID := r.PathValue("id")
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	schedule, err := s.runner.CreateExecutionPlanSchedule(planID, req, testCreationActor{
		Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errNotExecutionLeader) {
			status = http.StatusServiceUnavailable
		} else if errors.Is(err, errExecutionPlanNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, errExecutionPlanSchedulePersistence) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	s.recordAuditEvent(r, "execution_plan.schedule.create", "success", "execution_plan_schedule", schedule.ID, schedule.ProjectID, map[string]any{"plan_id": planID, "cron_expr": schedule.CronExpr})
	writeJSON(w, http.StatusCreated, schedule)
}

func (s *Server) handleListExecutionPlanSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := s.runner.ListExecutionPlanSchedulesForPlan(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": schedules, "count": len(schedules)})
}

func (s *Server) handleUpdateExecutionPlanSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateExecutionPlanScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	expectedRevision, preconditionErr := scheduleExpectedRevision(r, req.ExpectedRevision)
	if preconditionErr != nil {
		status := http.StatusBadRequest
		if errors.Is(preconditionErr, errSchedulePreconditionRequired) {
			status = http.StatusPreconditionRequired
		}
		writeError(w, status, preconditionErr.Error())
		return
	}
	req.ExpectedRevision = expectedRevision
	planID := r.PathValue("id")
	scheduleID := r.PathValue("schedule_id")
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	schedule, ok, err := s.runner.UpdateExecutionPlanSchedule(planID, scheduleID, req, testCreationActor{
		Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errNotExecutionLeader) {
			status = http.StatusServiceUnavailable
		} else if errors.Is(err, errScheduleConcurrentChange) {
			status = http.StatusConflict
		} else if errors.Is(err, errScheduleHasActiveOccurrence) {
			status = http.StatusConflict
		} else if errors.Is(err, errExecutionPlanSchedulePersistence) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "execution plan schedule not found")
		return
	}
	s.recordAuditEvent(r, "execution_plan.schedule.update", "success", "execution_plan_schedule", schedule.ID, schedule.ProjectID, map[string]any{"plan_id": planID})
	writeJSON(w, http.StatusOK, schedule)
}

func (s *Server) handleDeleteExecutionPlanSchedule(w http.ResponseWriter, r *http.Request) {
	planID := r.PathValue("id")
	scheduleID := r.PathValue("schedule_id")
	ok, err := s.runner.DeleteExecutionPlanScheduleForPlan(planID, scheduleID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errNotExecutionLeader) {
			status = http.StatusServiceUnavailable
		} else if errors.Is(err, errScheduleHasActiveOccurrence) {
			status = http.StatusConflict
		} else if errors.Is(err, errExecutionPlanSchedulePersistence) {
			status = http.StatusInternalServerError
		}
		writeError(w, status, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "execution plan schedule not found")
		return
	}
	s.recordAuditEvent(r, "execution_plan.schedule.delete", "success", "execution_plan_schedule", scheduleID, "", map[string]any{"plan_id": planID})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": scheduleID})
}

func (s *Server) handleListExecutionPlanScheduleOccurrences(w http.ResponseWriter, r *http.Request) {
	planID := r.PathValue("id")
	scheduleID := r.PathValue("schedule_id")
	limit, offset := parseLimitOffsetQuery(r, defaultScheduleHistoryListLimit)
	if limit > maxScheduleHistoryListLimit {
		limit = maxScheduleHistoryListLimit
	}
	occurrences, ok, err := s.runner.ListExecutionPlanScheduleOccurrencesPageForPlan(planID, scheduleID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "execution plan schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"occurrences": occurrences, "count": len(occurrences), "limit": limit, "offset": offset})
}
