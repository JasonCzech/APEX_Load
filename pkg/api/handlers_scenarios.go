package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultScheduleHistoryListLimit = 100
	maxScheduleHistoryListLimit     = 250
)

func (s *Server) handleCreateScenario(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	scenario, err := s.runner.StoreScenario(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "scenario.create", "success", "scenario", scenario.ID, scenario.ProjectID, map[string]any{"name": scenario.Name, "duration": scenario.Duration})
	writeJSON(w, http.StatusCreated, maskStoredScenario(scenario))
}

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID != "" {
		scenarios := s.runner.ListScenariosFiltered(projectID)
		writeJSON(w, http.StatusOK, map[string]any{"scenarios": maskStoredScenarios(scenarios), "count": len(scenarios)})
		return
	}
	scenarios := s.runner.ListScenarios()
	writeJSON(w, http.StatusOK, map[string]any{"scenarios": maskStoredScenarios(scenarios), "count": len(scenarios)})
}

func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	scenario, ok := s.runner.GetScenario(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	writeJSON(w, http.StatusOK, maskStoredScenario(scenario))
}

func (s *Server) handleUpdateScenario(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	existing, ok := s.runner.GetScenario(r.PathValue("id"))
	if !ok || existing == nil {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	requestProjectID, err := s.scenarioRequestProjectID(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if requestProjectID != "" && normalizeProjectID(requestProjectID) != normalizeProjectID(existing.ProjectID) {
		writeError(w, http.StatusBadRequest, "scenario scripts must remain in the scenario project")
		return
	}
	scenario, ok, err := s.runner.UpdateScenario(r.PathValue("id"), req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errScenarioConcurrentChange) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	s.recordAuditEvent(r, "scenario.update", "success", "scenario", scenario.ID, scenario.ProjectID, map[string]any{"name": scenario.Name, "duration": scenario.Duration})
	writeJSON(w, http.StatusOK, maskStoredScenario(scenario))
}

func (s *Server) scenarioRequestProjectID(req CreateScenarioRequest) (string, error) {
	projectID := ""
	for _, group := range req.Groups {
		script, ok := s.runner.GetScript(strings.TrimSpace(group.ScriptID))
		if !ok || script == nil {
			return "", fmt.Errorf("script not found: %s", group.ScriptID)
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID != "" && candidate != projectID {
			return "", fmt.Errorf("scenario scripts span multiple projects")
		}
		projectID = candidate
	}
	return projectID, nil
}

func (s *Server) handleValidateScenarioLoadModel(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	report, err := s.runner.ValidateScenarioLoadModel(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleSizeScenarioFromGoal(w http.ResponseWriter, r *http.Request) {
	var req GoalSizingRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.BaselineTestID) == "" {
		writeError(w, http.StatusBadRequest, "baseline_test_id is required")
		return
	}
	if _, ok := s.runner.GetTest(req.BaselineTestID); !ok {
		writeError(w, http.StatusNotFound, "baseline test not found")
		return
	}
	record, ok := s.runner.GetRunArchiveContext(r.Context(), req.BaselineTestID)
	if !ok {
		writeError(w, http.StatusNotFound, "baseline run archive not found")
		return
	}
	lease, err := acquireArchiveMaterialized(r.Context(), record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize baseline archive: "+err.Error())
		return
	}
	defer lease.Release()
	report, err := cachedRunArchiveReport(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build baseline report: "+err.Error())
		return
	}
	sizing, err := buildGoalSizingReport(report, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Attach load-model validation best-effort: a validation error (e.g. a
	// baseline script no longer in the library) should not hide the sizing math.
	if validation, verr := s.runner.ValidateScenarioLoadModel(sizing.ScenarioDraft); verr == nil {
		sizing.LoadModelValidation = validation
	}
	writeJSON(w, http.StatusOK, sizing)
}

func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	projectID := ""
	if scenario, ok := s.runner.GetScenario(id); ok {
		projectID = scenario.ProjectID
	}
	deleted, err := s.runner.DeleteScenarioDurable(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete scenario: "+err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusBadRequest, "scenario not found or has results")
		return
	}
	s.recordAuditEvent(r, "scenario.delete", "success", "scenario", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleScenarioResults(w http.ResponseWriter, r *http.Request) {
	scenarioID := r.PathValue("id")
	tests := s.runner.ListTestsByContext(r.URL.Query().Get("application_id"), r.URL.Query().Get("release_id"), scenarioID)
	writeJSON(w, http.StatusOK, map[string]any{"tests": maskManagedTests(tests), "count": len(tests)})
}

func (s *Server) handleScenarioSchedulePreview(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	count := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("count")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			count = n
		}
	}
	preview, err := s.runner.PreviewScenarioScheduleRuns(r.PathValue("id"), req, count)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "scenario not found: "+r.PathValue("id") {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleCreateScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	_, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.OwnerSubject = actorSubject
	req.OwnerDisplayName = actorDisplayName
	req.RBACReviewSource = "api"
	schedule, err := s.runner.CreateScenarioSchedule(r.PathValue("id"), req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "scenario not found: "+r.PathValue("id") {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	projectID := ""
	if scenario, ok := s.runner.GetScenario(r.PathValue("id")); ok {
		projectID = scenario.ProjectID
	}
	s.recordAuditEvent(r, "scenario.schedule.create", "success", "scenario_schedule", schedule.ID, projectID, scenarioScheduleAuditDetails(schedule))
	writeJSON(w, http.StatusCreated, schedule)
}

func (s *Server) handleListScenarioSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, ok := s.runner.ListScenarioSchedules(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": schedules, "count": len(schedules)})
}

func (s *Server) handleUpdateScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	_, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	req.OwnerSubject = actorSubject
	req.OwnerDisplayName = actorDisplayName
	req.RBACReviewSource = "api"
	schedule, ok, err := s.runner.UpdateScenarioSchedule(r.PathValue("id"), r.PathValue("schedule_id"), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	projectID := ""
	if scenario, ok := s.runner.GetScenario(r.PathValue("id")); ok {
		projectID = scenario.ProjectID
	}
	s.recordAuditEvent(r, "scenario.schedule.update", "success", "scenario_schedule", schedule.ID, projectID, scenarioScheduleAuditDetails(schedule))
	writeJSON(w, http.StatusOK, schedule)
}

func (s *Server) handleDeleteScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	projectID := ""
	if scenario, ok := s.runner.GetScenario(r.PathValue("id")); ok {
		projectID = scenario.ProjectID
	}
	deleted, err := s.runner.DeleteScenarioSchedule(r.PathValue("id"), r.PathValue("schedule_id"))
	if err != nil {
		if errors.Is(err, errScheduleHasActiveOccurrence) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete scenario schedule")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	s.recordAuditEvent(r, "scenario.schedule.delete", "success", "scenario_schedule", r.PathValue("schedule_id"), projectID, map[string]any{"scenario_id": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("schedule_id")})
}

func (s *Server) handleListScenarioScheduleOccurrences(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultScheduleHistoryListLimit)
	if limit > maxScheduleHistoryListLimit {
		limit = maxScheduleHistoryListLimit
	}
	occurrences, ok := s.runner.ListScenarioScheduleOccurrencesPage(r.PathValue("id"), r.PathValue("schedule_id"), limit, offset)
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"occurrences": occurrences, "count": len(occurrences), "limit": limit, "offset": offset})
}

func (s *Server) handleListScenarioScheduleApprovals(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultScheduleHistoryListLimit)
	if limit > maxScheduleHistoryListLimit {
		limit = maxScheduleHistoryListLimit
	}
	approvals, ok := s.runner.ListScenarioScheduleApprovalsPage(r.PathValue("id"), r.PathValue("schedule_id"), limit, offset)
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals, "count": len(approvals), "limit": limit, "offset": offset})
}

func (s *Server) handleDeleteScenarioScheduleOccurrence(w http.ResponseWriter, r *http.Request) {
	projectID := ""
	if scenario, ok := s.runner.GetScenario(r.PathValue("id")); ok {
		projectID = scenario.ProjectID
	}
	deleted, err := s.runner.DeleteScenarioScheduleOccurrence(r.PathValue("id"), r.PathValue("schedule_id"), r.PathValue("occurrence_id"))
	if err != nil {
		if errors.Is(err, errScheduleHasActiveOccurrence) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete scenario schedule occurrence")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "scenario schedule occurrence not found")
		return
	}
	s.recordAuditEvent(r, "scenario.schedule.occurrence.delete", "success", "scenario_schedule_occurrence", r.PathValue("occurrence_id"), projectID, map[string]any{"scenario_id": r.PathValue("id"), "schedule_id": r.PathValue("schedule_id")})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("occurrence_id")})
}

func (s *Server) handleScenarioScheduleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.runner.ScenarioScheduleHealth(time.Now().UTC()))
}

func (s *Server) handleApproveScenarioScheduleApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveScenarioScheduleApproval(w, r, true)
}

func (s *Server) handleRejectScenarioScheduleApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveScenarioScheduleApproval(w, r, false)
}

func (s *Server) handleResolveScenarioScheduleApproval(w http.ResponseWriter, r *http.Request, approved bool) {
	var req struct {
		Reason string `json:"reason,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	approval, schedule, ok, err := s.runner.ResolveScenarioScheduleApproval(r.PathValue("id"), r.PathValue("schedule_id"), r.PathValue("approval_id"), approved, actorSubject, req.Reason)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule approval not found")
		return
	}
	status := "rejected"
	action := "scenario.schedule.approval.reject"
	if approved {
		status = "approved"
		action = "scenario.schedule.approval.approve"
	}
	projectID := ""
	if schedule != nil {
		projectID = schedule.ProjectID
	}
	s.recordAuditEvent(r, action, "success", "scenario_schedule_approval", approval.ID, projectID, map[string]any{"scenario_id": r.PathValue("id"), "schedule_id": r.PathValue("schedule_id"), "decision": status, "reason": req.Reason})
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "schedule": schedule})
}

func scenarioScheduleAuditDetails(schedule *storage.ScenarioSchedule) map[string]any {
	if schedule == nil {
		return nil
	}
	details := map[string]any{
		"scenario_id": schedule.ScenarioID,
		"name":        schedule.Name,
		"cron_expr":   schedule.CronExpr,
		"enabled":     schedule.Enabled,
		"owner": map[string]string{
			"subject":      schedule.OwnerSubject,
			"display_name": schedule.OwnerDisplayName,
		},
	}
	if schedule.Governance != nil {
		details["governance"] = schedule.Governance
	}
	if len(schedule.BlackoutWindows) > 0 {
		details["blackout_windows"] = schedule.BlackoutWindows
	}
	if len(schedule.MaintenanceWindows) > 0 {
		details["maintenance_windows"] = schedule.MaintenanceWindows
	}
	if len(schedule.AvailabilityWindows) > 0 {
		details["availability_windows"] = schedule.AvailabilityWindows
	}
	return details
}
