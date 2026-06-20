package api

import (
	"encoding/json"
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

// correlationStep is one request/response exchange in a recorded or replayed
// flow. Recorded and replayed steps are matched by ID.
type correlationStep struct {
	ID              int               `json:"id"`
	RequestURL      string            `json:"request_url,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseStatus  int               `json:"response_status,omitempty"`
}

type correlationDiagnoseRequest struct {
	Recorded []correlationStep `json:"recorded"`
	Replay   []correlationStep `json:"replay"`
}

type correlationDiagnoseResponse struct {
	Suggestions []correlate.RepairSuggestion `json:"suggestions"`
	Blocking    bool                         `json:"blocking"`
	Counts      correlationDiagnoseCounts    `json:"counts"`
}

type correlationDiagnoseCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// handleListCorrelationRules exposes the detection rule catalog so the dashboard
// can show what the correlation engine detects and keep claims honest.
func (s *Server) handleListCorrelationRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"rules": correlate.RuleCatalog()})
}

// handleDiagnoseCorrelations runs replay-failure detection: it compares a
// recording against a replay of the same flow and returns ranked repair
// suggestions (which step breaks, why, and the extractor to add).
func (s *Server) handleDiagnoseCorrelations(w http.ResponseWriter, r *http.Request) {
	var req correlationDiagnoseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Recorded) == 0 {
		writeError(w, http.StatusBadRequest, "recorded steps are required")
		return
	}

	suggestions := correlate.DiagnoseReplay(
		toObservations(req.Recorded),
		toObservations(req.Replay),
	)

	counts := correlationDiagnoseCounts{}
	for _, s := range suggestions {
		switch s.Severity {
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

func toObservations(steps []correlationStep) []correlate.Observation {
	observations := make([]correlate.Observation, 0, len(steps))
	for _, step := range steps {
		observations = append(observations, correlate.Observation{
			ID:              step.ID,
			RequestURL:      step.RequestURL,
			RequestBody:     step.RequestBody,
			RequestHeaders:  step.RequestHeaders,
			ResponseBody:    step.ResponseBody,
			ResponseHeaders: step.ResponseHeaders,
			ResponseStatus:  step.ResponseStatus,
		})
	}
	return observations
}
