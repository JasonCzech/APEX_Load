package api

import (
	"encoding/json"
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
		RequestURL:      scrub.Value(obs.RequestURL, scrub.Policy{}),
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
	record := &storage.ScriptRecordingRecord{
		ScriptID:     scriptID,
		Source:       source,
		Sensitivity:  scrub.SensitivityScrubbed,
		RequestCount: len(observations),
		Observations: redacted,
	}
	if payload, err := json.Marshal(raw); err == nil && recordingBaselineEncryptionAvailable() {
		if encrypted, err := encryptRecordingBaseline(payload); err == nil {
			record.EncryptedCorrelationObservations = encrypted
			record.Sensitivity = "encrypted_contains_secrets"
		}
	}
	return s.store.SaveScriptRecording(record)
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
