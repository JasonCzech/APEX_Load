package api

import (
	"encoding/json"
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

type correlationDiffRequest struct {
	RecordingA []correlationStep `json:"recording_a"`
	RecordingB []correlationStep `json:"recording_b"`
}

type correlationDiffResponse struct {
	Results []correlate.DiffResult `json:"results"`
	Counts  correlationDiffCounts  `json:"counts"`
}

type correlationDiffCounts struct {
	Dynamic int `json:"dynamic"`
	Static  int `json:"static"`
	Unknown int `json:"unknown"`
}

// handleDiffCorrelations runs two-recording classification: correlations whose
// values rotate between two recordings of the same flow are truly dynamic,
// identical values are static fixtures that do not need correlation, and
// values missing from the second recording need boundary review. This is the
// strongest deterministic false-positive filter before script generation.
func (s *Server) handleDiffCorrelations(w http.ResponseWriter, r *http.Request) {
	var req correlationDiffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.RecordingA) == 0 || len(req.RecordingB) == 0 {
		writeError(w, http.StatusBadRequest, "recording_a and recording_b are both required")
		return
	}

	results := correlate.DiffRecordings(
		toObservations(req.RecordingA),
		toObservations(req.RecordingB),
	)
	counts := correlationDiffCounts{}
	for _, result := range results {
		switch result.Class {
		case correlate.DiffDynamic:
			counts.Dynamic++
		case correlate.DiffStatic:
			counts.Static++
		default:
			counts.Unknown++
		}
	}
	if results == nil {
		results = []correlate.DiffResult{}
	}
	writeJSON(w, http.StatusOK, correlationDiffResponse{Results: results, Counts: counts})
}
