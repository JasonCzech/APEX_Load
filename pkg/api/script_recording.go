package api

import (
	"log/slog"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

// redactBaselineObservation applies selective credential redaction to the
// request side of a recorded observation. Request bodies preserve dynamic
// correlation candidates (session ids, CSRF, view-state), while request headers
// are scrubbed with the stricter artifact policy used for persisted baselines.
func redactBaselineObservation(obs correlate.Observation) storage.ScriptRecordingObservation {
	return storage.ScriptRecordingObservation{
		ID:              obs.ID,
		RequestURL:      obs.RequestURL,
		RequestBody:     scrub.RedactCredentialBody(obs.RequestBody),
		RequestHeaders:  scrub.Headers(obs.RequestHeaders, scrub.Policy{}),
		ResponseBody:    obs.ResponseBody,
		ResponseHeaders: obs.ResponseHeaders,
	}
}

// persistRecordedBaseline stores a selectively-redacted recorded baseline for a
// script. It is best-effort: a nil store, empty baseline, or save error must not
// fail the surrounding import.
func (s *Server) persistRecordedBaseline(scriptID, source string, observations []correlate.Observation) error {
	if s.store == nil || scriptID == "" || len(observations) == 0 {
		return nil
	}
	redacted := make([]storage.ScriptRecordingObservation, 0, len(observations))
	for _, obs := range observations {
		redacted = append(redacted, redactBaselineObservation(obs))
	}
	return s.store.SaveScriptRecording(&storage.ScriptRecordingRecord{
		ScriptID:     scriptID,
		Source:       source,
		Sensitivity:  scrub.SensitivityScrubbed,
		Observations: redacted,
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
