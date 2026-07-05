package api

import (
	"encoding/json"
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/storage"
)

// handleGetScriptRecordingStatus reports whether a recorded baseline exists for
// a script, so the debugger can show the auto-diagnose affordance.
func (s *Server) handleGetScriptRecordingStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	record, err := s.store.GetScriptRecording(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
		return
	}
	if record == nil {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exists":        true,
		"request_count": record.RequestCount,
		"source":        record.Source,
		"sensitivity":   record.Sensitivity,
		"captured_at":   record.UpdatedAt,
	})
}

// handleDiagnoseScriptReplay diffs a script's stored recorded baseline against a
// supplied replay and returns ranked correlation repair suggestions. The
// baseline never leaves the server.
func (s *Server) handleDiagnoseScriptReplay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Replay []correlationStep `json:"replay"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusNotFound, "no recorded baseline for this script")
		return
	}
	record, err := s.store.GetScriptRecording(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
		return
	}
	baseline := baselineObservations(record)
	if record == nil || len(baseline) == 0 {
		writeError(w, http.StatusNotFound, "no recorded baseline for this script")
		return
	}

	suggestions := correlate.DiagnoseReplay(
		baseline,
		toObservations(req.Replay),
	)

	counts := correlationDiagnoseCounts{}
	for _, suggestion := range suggestions {
		switch suggestion.Severity {
		case correlate.SeverityError:
			counts.Errors++
		case correlate.SeverityWarning:
			counts.Warnings++
		}
	}

	writeJSON(w, http.StatusOK, correlationDiagnoseResponse{
		Suggestions: suggestions,
		Blocking:    correlate.HasBlockingRepairs(suggestions),
		Counts:      counts,
	})
}

// baselineObservations converts a stored recorded baseline into correlate
// observations (the recorded side of a record-vs-replay diagnosis).
func baselineObservations(record *storage.ScriptRecordingRecord) []correlate.Observation {
	if record == nil {
		return nil
	}
	stored := record.CorrelationObservations
	if len(stored) == 0 {
		stored = record.Observations
	}
	observations := make([]correlate.Observation, 0, len(stored))
	for _, o := range stored {
		observations = append(observations, correlate.Observation{
			ID:              o.ID,
			RequestURL:      o.RequestURL,
			RequestBody:     o.RequestBody,
			RequestHeaders:  o.RequestHeaders,
			ResponseBody:    o.ResponseBody,
			ResponseHeaders: o.ResponseHeaders,
		})
	}
	return observations
}
