package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type GeneratorDrainRequest struct {
	Target     string `json:"target"`
	DrainState string `json:"drain_state"`
}

func (s *Server) handleSetGeneratorDrainState(w http.ResponseWriter, r *http.Request) {
	var req GeneratorDrainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	req.DrainState = strings.TrimSpace(req.DrainState)
	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	if req.DrainState == "" {
		req.DrainState = "active"
	}

	backend, ok := s.runner.getExecutionBackend().(drainableExecutionBackend)
	if !ok {
		writeError(w, http.StatusBadRequest, "generator drain control requires distributed execution")
		return
	}
	if err := backend.SetGeneratorDrainState(req.Target, req.DrainState); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.recordAuditEvent(r, "generator.drain_state.update", "success", "generator", req.Target, "", map[string]any{"drain_state": req.DrainState})
	writeJSON(w, http.StatusOK, map[string]string{
		"target":      req.Target,
		"drain_state": req.DrainState,
	})
}
