package correlate

import (
	"fmt"
	"sort"
	"strings"
)

// RepairSeverity ranks how likely a finding is to break the replay.
type RepairSeverity string

const (
	// SeverityError means the replay almost certainly fails as-is (a stale,
	// rotated value is being replayed verbatim).
	SeverityError RepairSeverity = "error"
	// SeverityWarning means a correlation source the recording relied on was not
	// found on replay (boundary/shape drift) and should be reviewed.
	SeverityWarning RepairSeverity = "warning"
)

// SuggestedExtractor is the concrete correlation fix the engine proposes — the
// same shape the authoring/DSL layer needs to insert an extractor.
type SuggestedExtractor struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	ExtractType string `json:"extract_type"`
	Expression  string `json:"expression"`
	Convert     string `json:"convert,omitempty"`
}

// RepairSuggestion is a single ranked, explainable replay-correlation finding
// with a proposed fix. This is what turns APEX from "a converter" into a
// migration assistant: it tells the user *which* step will break, *why*, and the
// exact extractor to add.
type RepairSuggestion struct {
	RuleID         string              `json:"rule_id"`
	SourceFamily   ResponseFamily      `json:"source_family,omitempty"`
	Severity       RepairSeverity      `json:"severity"`
	Title          string              `json:"title"`
	Detail         string              `json:"detail"`
	SourceStepID   int                 `json:"source_step_id"`
	FailingStepID  int                 `json:"failing_step_id,omitempty"`
	UsedInIDs      []int               `json:"used_in_ids,omitempty"`
	UsageCount     int                 `json:"usage_count,omitempty"`
	AffectedFields []string            `json:"affected_fields,omitempty"`
	RecordedValue  string              `json:"recorded_value,omitempty"`
	ReplayValue    string              `json:"replay_value,omitempty"`
	Suggestion     string              `json:"suggestion"`
	Extractor      *SuggestedExtractor `json:"extractor,omitempty"`
	Confidence     float64             `json:"confidence"`
	ConfidenceBand string              `json:"confidence_band"`
	ReviewAction   string              `json:"review_action"`
}

// DiagnoseReplay compares a recording against a replay of the same flow (steps
// matched by Observation.ID) and reports correlation failures with repair
// suggestions. It reuses the rules engine: a value that was dynamic in the
// recording, rotated on replay, and is still being sent verbatim by a later step
// is a missing correlation; a correlation source that vanished on replay is a
// boundary drift.
func DiagnoseReplay(recorded, replay []Observation) []RepairSuggestion {
	correlations := Detect(recorded)
	replayByID := indexObservations(replay)
	suggestions := make([]RepairSuggestion, 0)

	for _, c := range correlations {
		rep, ok := replayByID[c.FoundInID]
		if !ok {
			continue
		}
		replayValue := candidateValueByName(rep, c.Name)

		if replayValue == "" {
			// The recording extracted this value here, but the replay response no
			// longer exposes it — the boundary or JSON path likely drifted.
			suggestions = append(suggestions, RepairSuggestion{
				RuleID:         c.RuleID,
				SourceFamily:   FamilyForRule(c.RuleID),
				Severity:       SeverityWarning,
				Title:          "Correlation source missing on replay",
				Detail:         fmt.Sprintf("Step %d no longer exposes %q in its response; the recorded boundary may have changed.", c.FoundInID, c.Name),
				SourceStepID:   c.FoundInID,
				UsedInIDs:      append([]int(nil), c.UsedInIDs...),
				UsageCount:     len(c.UsedInIDs),
				Suggestion:     fmt.Sprintf("Re-check the extractor for %s (expression %q) against the current response.", c.Name, c.Expression),
				Extractor:      extractorFor(c),
				Confidence:     c.Confidence,
				ConfidenceBand: confidenceBand(c.Confidence),
				ReviewAction:   reviewActionFor(SeverityWarning, c.Confidence),
			})
			continue
		}

		if replayValue == c.Value {
			// Value did not rotate between record and replay; treating it as a hard
			// dynamic value would be a false positive, so skip.
			continue
		}

		// The value rotated on replay. Any later step still sending the *recorded*
		// literal (and not the fresh replay value) will be rejected by the server.
		for _, usedID := range c.UsedInIDs {
			ru, ok := replayByID[usedID]
			if !ok {
				continue
			}
			affectedFields := ObservationUsageFields(ru, c.Value)
			if len(affectedFields) > 0 && !ObservationContains(ru, replayValue) {
				suggestions = append(suggestions, RepairSuggestion{
					RuleID:         c.RuleID,
					SourceFamily:   FamilyForRule(c.RuleID),
					Severity:       SeverityError,
					Title:          "Stale value replayed (missing correlation)",
					Detail:         fmt.Sprintf("Step %d replays the recorded value for %s, but step %d now returns a different value — the server will reject the stale value.", usedID, c.Name, c.FoundInID),
					SourceStepID:   c.FoundInID,
					FailingStepID:  usedID,
					UsedInIDs:      append([]int(nil), c.UsedInIDs...),
					UsageCount:     len(c.UsedInIDs),
					AffectedFields: affectedFields,
					RecordedValue:  Truncate(c.Value, 80),
					ReplayValue:    Truncate(replayValue, 80),
					Suggestion:     fmt.Sprintf("Capture %s from step %d and substitute {{var.%s}} in step %d.", c.Name, c.FoundInID, c.Name, usedID),
					Extractor:      extractorFor(c),
					Confidence:     c.Confidence,
					ConfidenceBand: confidenceBand(c.Confidence),
					ReviewAction:   reviewActionFor(SeverityError, c.Confidence),
				})
			}
		}
	}

	// A replay step can be rejected (HTTP >=400) without any dynamic value
	// visibly rotating — e.g. a stale CSRF/nonce the server echoes back unchanged,
	// or a token the rule catalog does not recognize. The value-diff loop above
	// cannot see those; this pass flags rejected steps that no suggestion already
	// explains so they are not silently missed.
	suggestions = append(suggestions, diagnoseUnexplainedRejections(replay, suggestions)...)

	sort.SliceStable(suggestions, func(i, j int) bool {
		if suggestions[i].Severity != suggestions[j].Severity {
			// Errors before warnings.
			return suggestions[i].Severity == SeverityError
		}
		if suggestions[i].Confidence != suggestions[j].Confidence {
			return suggestions[i].Confidence > suggestions[j].Confidence
		}
		return suggestions[i].FailingStepID < suggestions[j].FailingStepID
	})
	return suggestions
}

// diagnoseUnexplainedRejections flags replay steps the server rejected (HTTP
// >=400) that no value-rotation suggestion already explains. It is additive and
// only fires when callers supply Observation.ResponseStatus (default 0 → no-op),
// so it never changes behavior for status-less inputs.
func diagnoseUnexplainedRejections(replay []Observation, existing []RepairSuggestion) []RepairSuggestion {
	explained := make(map[int]struct{}, len(existing))
	for _, s := range existing {
		if s.FailingStepID != 0 {
			explained[s.FailingStepID] = struct{}{}
		}
	}
	out := make([]RepairSuggestion, 0)
	for _, o := range replay {
		if o.ResponseStatus < 400 {
			continue
		}
		if _, ok := explained[o.ID]; ok {
			continue
		}
		out = append(out, RepairSuggestion{
			RuleID:         "replay_rejected_unexplained",
			Severity:       SeverityWarning,
			Title:          "Replay step rejected without a detected correlation cause",
			Detail:         fmt.Sprintf("Step %d failed on replay (HTTP %d) but no rotated dynamic value was detected — the rejection may stem from a token the rule engine does not recognize (e.g. a custom CSRF/anti-replay header re-sent verbatim).", o.ID, o.ResponseStatus),
			FailingStepID:  o.ID,
			Suggestion:     "Inspect this step's request for an auth/CSRF/nonce value that must be re-extracted from an earlier response, even if its value did not visibly change between record and replay.",
			Confidence:     0.4,
			ConfidenceBand: confidenceBand(0.4),
			ReviewAction:   "manual_review_required",
		})
	}
	return out
}

func ObservationUsageFields(observation Observation, value string) []string {
	if value == "" {
		return nil
	}
	fields := make([]string, 0, 4)
	if strings.Contains(observation.RequestURL, value) {
		fields = append(fields, "request_url")
	}
	if strings.Contains(observation.RequestBody, value) {
		fields = append(fields, "request_body")
	}
	keys := make([]string, 0, len(observation.RequestHeaders))
	for key := range observation.RequestHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.Contains(observation.RequestHeaders[key], value) {
			fields = append(fields, "request_headers."+key)
		}
	}
	return fields
}

// HasBlockingRepairs reports whether any suggestion is severe enough to fail a
// replay gate (used by the replay/authoring surfaces to block save/approve).
func HasBlockingRepairs(suggestions []RepairSuggestion) bool {
	for _, s := range suggestions {
		if s.Severity == SeverityError {
			return true
		}
	}
	return false
}

func indexObservations(observations []Observation) map[int]Observation {
	index := make(map[int]Observation, len(observations))
	for _, o := range observations {
		index[o.ID] = o
	}
	return index
}

// candidateValueByName re-runs the rule extractors over a replay response and
// returns the value of the same-named candidate, i.e. the fresh value the server
// produced this time.
func candidateValueByName(observation Observation, name string) string {
	for _, candidate := range Candidates(observation.ResponseBody, observation.ResponseHeaders) {
		if candidate.Name == name {
			return candidate.Value
		}
	}
	return ""
}

func extractorFor(c Correlation) *SuggestedExtractor {
	return &SuggestedExtractor{
		Name:        c.Name,
		Source:      c.Source,
		ExtractType: c.ExtractType,
		Expression:  c.Expression,
		Convert:     c.Convert,
	}
}

func confidenceBand(confidence float64) string {
	switch {
	case confidence >= 0.8:
		return "high"
	case confidence >= 0.6:
		return "medium"
	default:
		return "low"
	}
}

func reviewActionFor(severity RepairSeverity, confidence float64) string {
	if severity == SeverityError && confidence >= 0.8 {
		return "auto_apply_available"
	}
	if severity == SeverityError {
		return "manual_review_required"
	}
	return "review_extractor_boundary"
}
