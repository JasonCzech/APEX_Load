package api

import (
	"log/slog"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

// redactBaselineObservation returns the scrubbed persistence view of a recorded
// observation. Raw values are kept separately for server-side correlation.
func redactBaselineObservation(obs correlate.Observation) storage.ScriptRecordingObservation {
	return storage.ScriptRecordingObservation{
		ID:              obs.ID,
		RequestURL:      scrub.ValueURL(obs.RequestURL, scrub.Policy{}),
		RequestBody:     scrub.Value(obs.RequestBody, scrub.Policy{}),
		RequestHeaders:  scrub.Headers(obs.RequestHeaders, scrub.Policy{}),
		ResponseBody:    scrub.Value(obs.ResponseBody, scrub.Policy{}),
		ResponseHeaders: scrub.Headers(obs.ResponseHeaders, scrub.Policy{}),
	}
}

func rawBaselineObservation(obs correlate.Observation) storage.ScriptRecordingObservation {
	return storage.ScriptRecordingObservation{
		ID:              obs.ID,
		RequestURL:      obs.RequestURL,
		RequestBody:     obs.RequestBody,
		RequestHeaders:  obs.RequestHeaders,
		ResponseBody:    obs.ResponseBody,
		ResponseHeaders: obs.ResponseHeaders,
	}
}

// scrubRepairSuggestions redacts the literal recorded/replay values carried on
// correlation repair suggestions before they leave the server. Detection runs
// on the raw baseline sidecar for accuracy, but a suggestion's RecordedValue /
// ReplayValue can be a live credential (a password or bearer token is exactly
// the kind of dynamic value the detector flags), and the diagnostic only needs
// to name the field and extractor — not echo the secret to the client.
// scrub.Value leaves non-secret values (numeric IDs, CSRF-style correlation
// keys) intact, so the diagnostic stays useful.
func scrubRepairSuggestions(suggestions []correlate.RepairSuggestion) []correlate.RepairSuggestion {
	for i := range suggestions {
		suggestions[i].RecordedValue = scrub.Value(suggestions[i].RecordedValue, scrub.Policy{})
		suggestions[i].ReplayValue = scrub.Value(suggestions[i].ReplayValue, scrub.Policy{})
	}
	return suggestions
}

// persistRecordedBaseline stores a scrubbed recorded baseline view plus a raw
// correlation sidecar for a script. It is best-effort: a nil store, empty
// baseline, or save error must not fail the surrounding import.
func (s *Server) persistRecordedBaseline(scriptID, source string, observations []correlate.Observation) error {
	if s.store == nil || scriptID == "" || len(observations) == 0 {
		return nil
	}
	redacted := make([]storage.ScriptRecordingObservation, 0, len(observations))
	raw := make([]storage.ScriptRecordingObservation, 0, len(observations))
	for _, obs := range observations {
		redacted = append(redacted, redactBaselineObservation(obs))
		raw = append(raw, rawBaselineObservation(obs))
	}
	return s.store.SaveScriptRecording(&storage.ScriptRecordingRecord{
		ScriptID:                scriptID,
		Source:                  source,
		Sensitivity:             scrub.SensitivityRaw,
		Observations:            redacted,
		CorrelationObservations: raw,
	})
}

// captureRecordingBaseline persists the recorded baseline for a newly-created
// script from a still-live recording session. Best-effort: capture failures
// never block script creation.
func (s *Server) captureRecordingBaseline(scriptID, sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || s.recorder == nil {
		return
	}
	session, ok := s.recorder.GetSession(sessionID)
	if !ok {
		return
	}
	if err := s.persistRecordedBaseline(scriptID, "browser_recording", recorder.SessionBaseline(session)); err != nil {
		slog.Warn("failed to persist recorded baseline", "script_id", scriptID, "error", err)
	}
}
