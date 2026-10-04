package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

// Correlation diagnosis is deliberately tighter than the ordinary JSON API
// backstop. Candidate extraction and cross-step reuse analysis are CPU-heavy,
// so large imports must go through the bounded recording/import workflows.
const maxCorrelationRequestBodyBytes int64 = 2 << 20

// correlationStep is one request/response exchange in a recorded or replayed
// flow. Recorded and replayed steps are matched by ID.
type correlationStep struct {
	ID                     int               `json:"id"`
	RequestURL             string            `json:"request_url,omitempty"`
	RequestBody            string            `json:"request_body,omitempty"`
	RequestBodyTransformed bool              `json:"request_body_transformed,omitempty"`
	RequestHeaders         map[string]string `json:"request_headers,omitempty"`
	ResponseBody           string            `json:"response_body,omitempty"`
	ResponseHeaders        map[string]string `json:"response_headers,omitempty"`
	ResponseStatus         int               `json:"response_status,omitempty"`
	ResponseSuccess        *bool             `json:"response_success,omitempty"`
	ParentActionName       string            `json:"parent_action_name,omitempty"`
	RequestIndex           *int              `json:"request_index,omitempty"`
	StepPath               []string          `json:"step_path,omitempty"`
}

type correlationDiagnoseRequest struct {
	Recorded []correlationStep `json:"recorded"`
	Replay   []correlationStep `json:"replay"`
}

type correlationDiagnoseResponse struct {
	Suggestions           []correlate.RepairSuggestion   `json:"suggestions"`
	Blocking              bool                           `json:"blocking"`
	Counts                correlationDiagnoseCounts      `json:"counts"`
	ComparedRecordedSteps int                            `json:"compared_recorded_steps"`
	TotalRecordedSteps    int                            `json:"total_recorded_steps"`
	Truncated             bool                           `json:"truncated,omitempty"`
	RelationshipCoverage  correlate.RelationshipCoverage `json:"relationship_coverage"`
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
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Recorded) == 0 {
		writeError(w, http.StatusBadRequest, "recorded steps are required")
		return
	}
	if duplicateID, duplicated := duplicateCorrelationStepID(req.Recorded); duplicated {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("recorded steps contain duplicate id %d", duplicateID))
		return
	}
	if nestedID, nested := nestedCorrelationStepID(req.Recorded); nested {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("recorded step %d is a nested replay leaf and cannot be mapped to a top-level action", nestedID))
		return
	}
	if duplicateID, duplicated := duplicateCorrelationStepID(req.Replay); duplicated {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("replay steps contain duplicate id %d", duplicateID))
		return
	}
	if nestedID, nested := nestedCorrelationStepID(req.Replay); nested {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("replay step %d is a nested replay leaf and cannot be mapped to a top-level action", nestedID))
		return
	}

	recorded := toObservations(req.Recorded)
	replay := toObservations(req.Replay)
	if !correlationObservationsWithinDiagnosisBounds(recorded) || !correlationObservationsWithinDiagnosisBounds(replay) {
		writeError(w, http.StatusRequestEntityTooLarge, "correlation diagnosis exceeds observation or evidence limits")
		return
	}
	diagnosis, err := correlate.DiagnoseReplayReviewChecked(recorded, replay)
	if err != nil {
		if errors.Is(err, correlate.ErrCorrelationWorkLimit) {
			writeError(w, http.StatusUnprocessableEntity, "correlation diagnosis exceeded deterministic work limits")
			return
		}
		writeError(w, http.StatusInternalServerError, "correlation diagnosis failed")
		return
	}
	suggestions := scrubRepairSuggestions(diagnosis.Suggestions)
	comparedRecordedSteps := matchedCorrelationObservationCount(recorded, replay)

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
		Suggestions:           suggestions,
		Blocking:              correlate.HasBlockingRepairs(suggestions),
		Counts:                counts,
		ComparedRecordedSteps: comparedRecordedSteps,
		TotalRecordedSteps:    len(req.Recorded),
		Truncated:             comparedRecordedSteps < len(req.Recorded),
		RelationshipCoverage:  diagnosis.RelationshipCoverage,
	})
}

func matchedCorrelationObservationCount(recorded, replay []correlate.Observation) int {
	if len(recorded) == 0 || len(replay) == 0 {
		return 0
	}
	replayIDs := make(map[int]struct{}, len(replay))
	for _, observation := range replay {
		replayIDs[observation.ID] = struct{}{}
	}
	matched := 0
	for _, observation := range recorded {
		if _, exists := replayIDs[observation.ID]; exists {
			matched++
		}
	}
	return matched
}

func duplicateCorrelationStepID(steps []correlationStep) (int, bool) {
	seen := make(map[int]struct{}, len(steps))
	for _, step := range steps {
		if _, exists := seen[step.ID]; exists {
			return step.ID, true
		}
		seen[step.ID] = struct{}{}
	}
	return 0, false
}

func nestedCorrelationStepID(steps []correlationStep) (int, bool) {
	for _, step := range steps {
		if strings.TrimSpace(step.ParentActionName) != "" || step.RequestIndex != nil || len(step.StepPath) > 0 {
			return step.ID, true
		}
	}
	return 0, false
}

func toObservations(steps []correlationStep) []correlate.Observation {
	observations := make([]correlate.Observation, 0, len(steps))
	for _, step := range steps {
		observations = append(observations, correlate.Observation{
			ID:                     step.ID,
			RequestURL:             step.RequestURL,
			RequestBody:            step.RequestBody,
			RequestBodyTransformed: step.RequestBodyTransformed,
			RequestHeaders:         step.RequestHeaders,
			ResponseBody:           step.ResponseBody,
			ResponseHeaders:        step.ResponseHeaders,
			ResponseStatus:         step.ResponseStatus,
			ResponseSuccess:        step.ResponseSuccess,
		})
	}
	return observations
}
