package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

type correlationDiffRequest struct {
	RecordingA []correlationStep `json:"recording_a"`
	RecordingB []correlationStep `json:"recording_b"`
}

type correlationDiffResponse struct {
	Results []correlationDiffResult `json:"results"`
	Counts  correlationDiffCounts   `json:"counts"`
}

// correlationDiffResult is the disclosure-safe transport projection of a
// correlate.DiffResult. DiffRecordings must inspect the literal values from
// both recordings to classify a candidate, but those values can be live
// credentials or session tokens. Only extractor structure and classification
// metadata are allowed to cross the HTTP boundary.
type correlationDiffResult struct {
	Name        string              `json:"name"`
	Source      string              `json:"source"`
	Expression  string              `json:"expression"`
	ExtractType string              `json:"extract_type"`
	Convert     string              `json:"convert,omitempty"`
	SaveOffset  int                 `json:"save_offset,omitempty"`
	SaveLength  int                 `json:"save_length,omitempty"`
	FoundInID   int                 `json:"found_in_id"`
	UsedInIDs   []int               `json:"used_in_ids,omitempty"`
	RuleID      string              `json:"rule_id,omitempty"`
	Confidence  float64             `json:"confidence"`
	Reasons     []string            `json:"reasons,omitempty"`
	Class       correlate.DiffClass `json:"class"`
	DiffReason  string              `json:"diff_reason"`
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
	if duplicateID, duplicated := duplicateCorrelationStepID(req.RecordingA); duplicated {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("recording_a contains duplicate id %d", duplicateID))
		return
	}
	if duplicateID, duplicated := duplicateCorrelationStepID(req.RecordingB); duplicated {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("recording_b contains duplicate id %d", duplicateID))
		return
	}
	recordingA := toObservations(req.RecordingA)
	recordingB := toObservations(req.RecordingB)
	if !correlationObservationsWithinDiagnosisBounds(recordingA) || !correlationObservationsWithinDiagnosisBounds(recordingB) {
		writeError(w, http.StatusRequestEntityTooLarge, "correlation diff exceeds observation or evidence limits")
		return
	}

	results, err := correlate.DiffRecordingsChecked(
		recordingA,
		recordingB,
	)
	if err != nil {
		if errors.Is(err, correlate.ErrCorrelationWorkLimit) {
			writeError(w, http.StatusUnprocessableEntity, "correlation diff exceeded deterministic work limits")
			return
		}
		writeError(w, http.StatusInternalServerError, "correlation diff failed")
		return
	}
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
	safeResults := make([]correlationDiffResult, 0, len(results))
	for _, result := range results {
		safeResults = append(safeResults, correlationDiffResult{
			Name:        result.Name,
			Source:      result.Source,
			Expression:  result.Expression,
			ExtractType: result.ExtractType,
			Convert:     result.Convert,
			SaveOffset:  result.SaveOffset,
			SaveLength:  result.SaveLength,
			FoundInID:   result.FoundInID,
			UsedInIDs:   result.UsedInIDs,
			RuleID:      result.RuleID,
			Confidence:  result.Confidence,
			Reasons:     result.Reasons,
			Class:       result.Class,
			DiffReason:  result.DiffReason,
		})
	}
	writeJSON(w, http.StatusOK, correlationDiffResponse{Results: safeResults, Counts: counts})
}
