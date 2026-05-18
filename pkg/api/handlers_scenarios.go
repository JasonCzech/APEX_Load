package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func (s *Server) handleCreateScenario(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	scenario, err := s.runner.StoreScenario(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "scenario.create", "success", "scenario", scenario.ID, scenario.ProjectID, map[string]any{"name": scenario.Name, "duration": scenario.Duration})
	writeJSON(w, http.StatusCreated, scenario)
}

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID != "" {
		scenarios := s.runner.ListScenariosFiltered(projectID)
		writeJSON(w, http.StatusOK, map[string]any{"scenarios": scenarios, "count": len(scenarios)})
		return
	}
	scenarios := s.runner.ListScenarios()
	writeJSON(w, http.StatusOK, map[string]any{"scenarios": scenarios, "count": len(scenarios)})
}

func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	scenario, ok := s.runner.GetScenario(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	writeJSON(w, http.StatusOK, scenario)
}

func (s *Server) handleUpdateScenario(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	scenario, ok, err := s.runner.UpdateScenario(r.PathValue("id"), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	s.recordAuditEvent(r, "scenario.update", "success", "scenario", scenario.ID, scenario.ProjectID, map[string]any{"name": scenario.Name, "duration": scenario.Duration})
	writeJSON(w, http.StatusOK, scenario)
}

func (s *Server) handleValidateScenarioLoadModel(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	report, err := s.runner.ValidateScenarioLoadModel(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	projectID := ""
	if scenario, ok := s.runner.GetScenario(id); ok {
		projectID = scenario.ProjectID
	}
	if !s.runner.DeleteScenario(id) {
		writeError(w, http.StatusBadRequest, "scenario not found or has results")
		return
	}
	s.recordAuditEvent(r, "scenario.delete", "success", "scenario", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleScenarioResults(w http.ResponseWriter, r *http.Request) {
	scenarioID := r.PathValue("id")
	tests := s.runner.ListTestsByContext(r.URL.Query().Get("application_id"), r.URL.Query().Get("release_id"), scenarioID)
	writeJSON(w, http.StatusOK, map[string]any{"tests": tests, "count": len(tests)})
}

func (s *Server) handleCreateScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	var req CreateScenarioScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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
	if !s.runner.DeleteScenarioSchedule(r.PathValue("id"), r.PathValue("schedule_id")) {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	s.recordAuditEvent(r, "scenario.schedule.delete", "success", "scenario_schedule", r.PathValue("schedule_id"), projectID, map[string]any{"scenario_id": r.PathValue("id")})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("schedule_id")})
}

func (s *Server) handleListScenarioScheduleOccurrences(w http.ResponseWriter, r *http.Request) {
	occurrences, ok := s.runner.ListScenarioScheduleOccurrences(r.PathValue("id"), r.PathValue("schedule_id"))
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"occurrences": occurrences, "count": len(occurrences)})
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
