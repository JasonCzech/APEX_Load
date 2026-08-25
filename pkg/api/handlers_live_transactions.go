package api

import (
	"net/http"
	"strings"
)

func (s *Server) handleGetLiveTransactionDetail(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	eventID := strings.TrimSpace(r.PathValue("event_id"))
	if eventID == "" {
		writeError(w, http.StatusBadRequest, "event_id is required")
		return
	}
	evidence, testExists, found := s.runner.GetLiveTransactionDetail(testID, eventID)
	if !testExists {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "live transaction detail not found")
		return
	}
	writeJSON(w, http.StatusOK, evidence)
}
