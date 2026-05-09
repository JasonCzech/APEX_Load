package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func (s *Server) handleCreateTest(w http.ResponseWriter, r *http.Request) {
	var req CreateTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	test, err := s.runner.CreateTest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, test)
}

func (s *Server) handleListTests(w http.ResponseWriter, r *http.Request) {
	tests := s.runner.ListTests()
	writeJSON(w, http.StatusOK, map[string]any{
		"tests": tests,
		"count": len(tests),
	})
}

func (s *Server) handleGetTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	writeJSON(w, http.StatusOK, test)
}

func (s *Server) handleStartTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.StartTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "test_id": id})
}

func (s *Server) handleStopTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.StopTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping", "test_id": id})
}

func (s *Server) handleAbortTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.runner.AbortTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "aborted", "test_id": id})
}

func (s *Server) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if test.LiveMetrics == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"test_id": id,
			"status":  test.Status,
			"message": "no metrics available yet",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"test_id": id,
		"status":  test.Status,
		"metrics": test.LiveMetrics,
		"history": test.History,
	})
}

func (s *Server) handleCreateScript(w http.ResponseWriter, r *http.Request) {
	var wrapper struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	script := &wrapper.Script

	if script.Name == "" {
		writeError(w, http.StatusBadRequest, "script name is required")
		return
	}
	if script.Protocol == "" {
		writeError(w, http.StatusBadRequest, "script protocol is required")
		return
	}
	if len(script.Actions) == 0 {
		writeError(w, http.StatusBadRequest, "script must have at least one action")
		return
	}

	stored := s.runner.StoreScript(script)
	writeJSON(w, http.StatusCreated, stored)
}

func (s *Server) handleListScripts(w http.ResponseWriter, r *http.Request) {
	scripts := s.runner.ListScripts()
	writeJSON(w, http.StatusOK, map[string]any{
		"scripts": scripts,
		"count":   len(scripts),
	})
}

func (s *Server) handleGetScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	script, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	writeJSON(w, http.StatusOK, script)
}

func (s *Server) handleValidateScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	script, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	issues := validateScript(script.Script)
	valid := len(issues) == 0

	writeJSON(w, http.StatusOK, map[string]any{
		"script_id": id,
		"valid":     valid,
		"issues":    issues,
	})
}

func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "healthy",
		"version":     "0.1.0",
		"ws_clients":  s.hub.ClientCount(),
		"test_count":  len(s.runner.tests),
	})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.hub.serveWS(w, r, "")
}

func (s *Server) handleTestStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	s.hub.serveWS(w, r, id)
}

func validateScript(script *dsl.Script) []string {
	var issues []string
	if script.Config.BaseURL == "" {
		issues = append(issues, "config.base_url is empty")
	}
	for i, a := range script.Actions {
		if a.Name == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		if a.Target == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing target", i, a.Name))
		}
	}
	return issues
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
