package api

import (
	"encoding/json"
	"net/http"
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
	writeJSON(w, http.StatusOK, scenario)
}

func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runner.DeleteScenario(id) {
		writeError(w, http.StatusBadRequest, "scenario not found or has results")
		return
	}
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
	schedule, err := s.runner.CreateScenarioSchedule(r.PathValue("id"), req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "scenario not found: "+r.PathValue("id") {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
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
	schedule, ok, err := s.runner.UpdateScenarioSchedule(r.PathValue("id"), r.PathValue("schedule_id"), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (s *Server) handleDeleteScenarioSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.runner.DeleteScenarioSchedule(r.PathValue("id"), r.PathValue("schedule_id")) {
		writeError(w, http.StatusNotFound, "scenario schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("schedule_id")})
}
