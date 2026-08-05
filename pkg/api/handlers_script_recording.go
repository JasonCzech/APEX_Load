package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/storage"
)

// handleGetScriptRecordingStatus reports whether a recorded baseline exists for
// a script, so the debugger can show the auto-diagnose affordance.
func (s *Server) handleGetScriptRecordingStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	id = stored.ID
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists":                         false,
			"correlation_usable":             false,
			"correlation_unavailable_reason": "baseline_not_found",
		})
		return
	}
	record, err := s.store.GetScriptRecordingStatus(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
		return
	}
	if record == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists":                         false,
			"correlation_usable":             false,
			"correlation_unavailable_reason": "baseline_not_found",
		})
		return
	}
	// The runner lookup used for authorization and the storage lookup are
	// separate snapshots. If a script ID is deleted and recreated in another
	// project between them, never expose the replacement recording's metadata to
	// a caller authorized only for the stale snapshot.
	if strings.TrimSpace(record.ScriptID) != id ||
		normalizeProjectID(record.ProjectID) != normalizeProjectID(stored.ProjectID) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	status := map[string]any{
		"exists":        true,
		"request_count": record.RequestCount,
		"source":        record.Source,
		"sensitivity":   record.Sensitivity,
		"captured_at":   record.UpdatedAt,
	}
	actionGraphErr := validateRecordingActionGraph(&storage.ScriptRecordingRecord{
		ScriptID:          record.ScriptID,
		ProjectID:         record.ProjectID,
		ScriptIncarnation: record.ScriptIncarnation,
		ActionGraphDigest: record.ActionGraphDigest,
	}, stored.Script)
	retainedCount, baselineErr := baselineSidecarStatus(record)
	totalCount := record.RequestCount
	if totalCount < retainedCount {
		totalCount = retainedCount
	}
	status["request_count"] = totalCount
	status["correlation_request_count"] = retainedCount
	status["correlation_truncated"] = totalCount > retainedCount
	status["correlation_usable"] = actionGraphErr == nil && baselineErr == nil && retainedCount > 0
	if errors.Is(actionGraphErr, errRecordingActionGraphDigestMissing) {
		status["correlation_unavailable_reason"] = "action_graph_unbound"
	} else if actionGraphErr != nil {
		status["correlation_unavailable_reason"] = "action_graph_changed"
	} else if errors.Is(baselineErr, errRecordingBaselineCiphertextUnbound) {
		status["correlation_unavailable_reason"] = "legacy_encryption_recapture_required"
	} else if baselineErr != nil {
		status["correlation_unavailable_reason"] = "raw_baseline_unavailable"
	} else if retainedCount == 0 {
		status["correlation_unavailable_reason"] = "baseline_empty"
	}
	writeJSON(w, http.StatusOK, status)
}

// baselineSidecarStatus classifies a stored recorded baseline for the polled
// status endpoint without decrypting it. A full baselineObservations call
// AES-GCM-decrypts and JSON-decodes a sidecar bounded by the exact ciphertext
// ceiling and maxRecordedBaselinePlaintextSize (12MiB) — still far too
// expensive for a viewer-accessible poll that only needs counts and usability
// flags.
//
// The storage projection carries a revision-bound structural count and
// validated envelope version. A nil error means the sidecar is a v3 ciphertext
// and the encryption key is configured; actual decryption (and its AAD binding
// check) still fails closed on the diagnose path, so a rotated or wrong key
// surfaces there rather than here.
func baselineSidecarStatus(record *storage.ScriptRecordingStatusRecord) (int, error) {
	if record == nil {
		return 0, nil
	}
	if !record.CorrelationMetadataCurrent {
		return 0, errRecordingBaselineUnauthenticated
	}
	retainedCount := record.CorrelationObservationCount
	switch {
	case record.CorrelationSidecarVersion == storage.ScriptRecordingSidecarNone:
		return 0, errRecordingBaselineUnauthenticated
	case record.CorrelationSidecarVersion == storage.ScriptRecordingSidecarV1,
		record.CorrelationSidecarVersion == storage.ScriptRecordingSidecarV2:
		return retainedCount, errRecordingBaselineCiphertextUnbound
	case strings.TrimSpace(record.SchemaVersion) != recordingBaselineSchemaVersion:
		return 0, fmt.Errorf("recording baseline schema version is unsupported")
	case record.CorrelationSidecarVersion != storage.ScriptRecordingSidecarV3:
		return 0, fmt.Errorf("recording baseline ciphertext metadata is malformed")
	case !recordingBaselineEncryptionAvailable():
		return retainedCount, fmt.Errorf("recording baseline encryption key (%s) is not configured", recordingBaselineKeyEnvVar)
	}
	return retainedCount, nil
}

type scriptReplayDiagnosisRequest struct {
	Recorded        *[]correlationStep `json:"recorded,omitempty"`
	Replay          []correlationStep  `json:"replay"`
	ReplaySessionID string             `json:"replay_session_id,omitempty"`
}

// handleDiagnoseScriptReplay diffs a script's stored or request-supplied
// recorded baseline against replay evidence and returns ranked correlation
// repair suggestions. replay_session_id selects bounded raw evidence retained
// privately by that script's debug session; the generic correlation endpoint is
// intentionally not wired to this capability.
func (s *Server) handleDiagnoseScriptReplay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	id = stored.ID
	var req scriptReplayDiagnosisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	replaySessionID := strings.TrimSpace(req.ReplaySessionID)
	// Keep both sides of a diagnosis in the same trust domain. Mixing a hidden
	// stored baseline with caller-chosen replay data (or an uploaded baseline
	// with private replay data) turns disclosure-safe offsets into a comparison
	// oracle over secret values. Uploaded HARs must use their uploaded/client
	// replay projection; encrypted stored baselines require a server-held replay
	// session from the same script.
	if req.Recorded != nil && replaySessionID != "" {
		writeError(w, http.StatusBadRequest, "uploaded recorded steps cannot be combined with server-held replay evidence")
		return
	}
	if req.Recorded == nil && replaySessionID == "" {
		writeError(w, http.StatusBadRequest, "a completed replay session is required for the stored correlation baseline")
		return
	}
	var (
		baseline             []correlate.Observation
		totalRecordedSteps   int
		recordedWasTruncated bool
		actionGraphDigest    string
		err                  error
	)
	if req.Recorded != nil {
		if len(*req.Recorded) == 0 {
			writeError(w, http.StatusBadRequest, "recorded steps are required when recorded is provided")
			return
		}
		if duplicateID, duplicated := duplicateCorrelationStepID(*req.Recorded); duplicated {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("recorded steps contain duplicate id %d", duplicateID))
			return
		}
		if nestedID, nested := nestedCorrelationStepID(*req.Recorded); nested {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("recorded step %d is a nested replay leaf and cannot be mapped to a top-level action", nestedID))
			return
		}
		baseline = toObservations(*req.Recorded)
		if !correlationObservationsWithinDiagnosisBounds(baseline) {
			writeError(w, http.StatusRequestEntityTooLarge, "recorded correlation baseline exceeds diagnosis limits")
			return
		}
		totalRecordedSteps = len(baseline)
	} else {
		if s.store == nil {
			writeError(w, http.StatusNotFound, "no recorded baseline for this script")
			return
		}
		record, loadErr := s.store.GetScriptRecording(id)
		if loadErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load recorded baseline")
			return
		}
		if record == nil {
			writeError(w, http.StatusNotFound, "no recorded baseline for this script")
			return
		}
		if strings.TrimSpace(record.ScriptID) != id ||
			normalizeProjectID(record.ProjectID) != normalizeProjectID(stored.ProjectID) {
			writeError(w, http.StatusNotFound, "script not found")
			return
		}
		if graphErr := validateRecordingActionGraph(record, stored.Script); graphErr != nil {
			switch {
			case errors.Is(graphErr, errRecordingActionGraphDigestMissing):
				writeError(w, http.StatusConflict, "recorded correlation baseline is not bound to this script action graph; save and capture a new recording, then replay the script")
			case errors.Is(graphErr, errRecordingActionGraphDigestMismatch):
				writeError(w, http.StatusConflict, "script action graph changed after the recording was captured; save and capture a new recording, then replay the current script")
			default:
				writeError(w, http.StatusConflict, "script action graph cannot be verified; save and capture a new recording, then replay the current script")
			}
			return
		}
		actionGraphDigest, err = scriptCorrelationActionGraphDigest(stored.Script)
		if err != nil {
			writeError(w, http.StatusConflict, "script action graph cannot be verified; save and capture a new recording, then replay the current script")
			return
		}
		baseline, err = baselineObservations(record)
		if err != nil {
			if errors.Is(err, errRecordingBaselineCiphertextUnbound) {
				writeError(w, http.StatusConflict, "recorded correlation baseline uses legacy encryption that is not bound to this script; capture a new recording before diagnosis")
				return
			}
			writeError(w, http.StatusConflict, "recorded correlation baseline is unavailable; capture a new recording with baseline encryption configured")
			return
		}
		if len(baseline) == 0 {
			writeError(w, http.StatusNotFound, "no recorded baseline for this script")
			return
		}
		totalRecordedSteps = max(record.RequestCount, len(baseline))
		recordedWasTruncated = record.RequestCount > len(baseline)
	}

	var replay []correlate.Observation
	if replaySessionID != "" {
		// A session id opts into the server-held raw evidence path. Never mix it
		// with or validate the client replay array: the dashboard may still send
		// its scrubbed compatibility projection, including duplicate container
		// events, but it is not trusted as diagnosis evidence in this mode.
		replay, err = s.scriptDebug.CorrelationObservations(replaySessionID, stored.ProjectID, id, actionGraphDigest)
		if err != nil {
			switch {
			case errors.Is(err, errScriptDebugCorrelationSessionNotFound):
				writeError(w, http.StatusNotFound, "replay session not found")
			case errors.Is(err, errScriptDebugCorrelationScriptMismatch):
				writeError(w, http.StatusConflict, "replay session does not belong to this script")
			case errors.Is(err, errScriptDebugCorrelationGraphMismatch):
				writeError(w, http.StatusConflict, "script action graph changed after this replay started; save the current script, capture a new recording, and replay it again")
			case errors.Is(err, errScriptDebugCorrelationSessionActive):
				writeError(w, http.StatusConflict, "replay session has not completed")
			default:
				writeError(w, http.StatusConflict, "raw replay correlation evidence is unavailable")
			}
			return
		}
	} else {
		if duplicateID, duplicated := duplicateCorrelationStepID(req.Replay); duplicated {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("replay steps contain duplicate id %d", duplicateID))
			return
		}
		if nestedID, nested := nestedCorrelationStepID(req.Replay); nested {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("replay step %d is a nested replay leaf and cannot be mapped to a top-level action", nestedID))
			return
		}
		replay = toObservations(req.Replay)
		if !correlationObservationsWithinDiagnosisBounds(replay) {
			writeError(w, http.StatusRequestEntityTooLarge, "replay correlation evidence exceeds diagnosis limits")
			return
		}
	}

	suggestions, diagnosisErr := correlate.DiagnoseReplayChecked(
		baseline,
		replay,
	)
	if diagnosisErr != nil {
		if errors.Is(diagnosisErr, correlate.ErrCorrelationWorkLimit) {
			writeError(w, http.StatusUnprocessableEntity, "correlation diagnosis exceeded deterministic work limits")
			return
		}
		writeError(w, http.StatusInternalServerError, "correlation diagnosis failed")
		return
	}
	suggestions = scrubRepairSuggestions(suggestions)
	comparedRecordedSteps := matchedCorrelationObservationCount(baseline, replay)

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
		Suggestions:           suggestions,
		Blocking:              correlate.HasBlockingRepairs(suggestions),
		Counts:                counts,
		ComparedRecordedSteps: comparedRecordedSteps,
		TotalRecordedSteps:    totalRecordedSteps,
		Truncated:             recordedWasTruncated || comparedRecordedSteps < len(baseline),
	})
}

func correlationObservationsWithinDiagnosisBounds(observations []correlate.Observation) bool {
	if len(observations) > maxRecordedBaselineObservations {
		return false
	}
	estimatedBytes := 2 // JSON array brackets.
	for index, observation := range observations {
		if index > 0 {
			estimatedBytes++
		}
		estimatedBytes += estimatedRecordingObservationJSONSize(rawBaselineObservation(observation))
		if estimatedBytes > maxRecordedBaselinePlaintextSize {
			return false
		}
	}
	return true
}

// baselineObservations converts a stored recorded baseline into correlate
// observations (the recorded side of a record-vs-replay diagnosis).
//
// Only an authenticated encrypted sidecar can feed correlation automation.
// Observations is an ID-only disclosure-safe projection, and legacy plaintext
// fields are never a compatibility fallback. Missing, unbound, undecryptable,
// or malformed sidecars fail closed and require a fresh capture.
func baselineObservations(record *storage.ScriptRecordingRecord) ([]correlate.Observation, error) {
	if record == nil {
		return nil, nil
	}
	encrypted := record.EncryptedCorrelationObservations
	if strings.TrimSpace(encrypted) == "" {
		return nil, errRecordingBaselineUnauthenticated
	}
	if strings.TrimSpace(record.SchemaVersion) != recordingBaselineSchemaVersion &&
		!strings.HasPrefix(encrypted, recordingBaselineCipherPrefixV1) &&
		!strings.HasPrefix(encrypted, recordingBaselineCipherPrefixV2) {
		return nil, fmt.Errorf("recording baseline schema version is unsupported")
	}
	payload, err := decryptRecordingBaseline(encrypted, recordingBaselineBinding{
		ScriptID:          record.ScriptID,
		ProjectID:         record.ProjectID,
		ScriptIncarnation: record.ScriptIncarnation,
		ActionGraphDigest: record.ActionGraphDigest,
		SchemaVersion:     record.SchemaVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("decrypt recorded correlation baseline: %w", err)
	}
	stored, err := decodeRecordingBaselineObservations(payload)
	if err != nil {
		return nil, fmt.Errorf("decode recorded correlation baseline: %w", err)
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
	if !correlationObservationsWithinDiagnosisBounds(observations) {
		return nil, fmt.Errorf("decode recorded correlation baseline: retained evidence exceeds diagnosis limits")
	}
	return observations, nil
}

// decodeRecordingBaselineObservations streams the authenticated JSON array and
// rejects overflow instead of returning a retained prefix. A compact malicious
// payload can encode far more than 500 empty objects inside the 12 MiB byte
// ceiling; unmarshalling it directly into a slice would allocate in proportion
// to that untrusted element count.
func decodeRecordingBaselineObservations(payload []byte) ([]storage.ScriptRecordingObservation, error) {
	if len(payload) > maxRecordedBaselinePlaintextSize {
		return nil, fmt.Errorf("recording baseline plaintext exceeds %d bytes", maxRecordedBaselinePlaintextSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	open, ok := token.(json.Delim)
	if !ok || open != '[' {
		return nil, errors.New("recording baseline must be a JSON array")
	}
	stored := make([]storage.ScriptRecordingObservation, 0, min(maxRecordedBaselineObservations, 64))
	for decoder.More() {
		if len(stored) >= maxRecordedBaselineObservations {
			return nil, fmt.Errorf("recording baseline has more than %d observations", maxRecordedBaselineObservations)
		}
		var observation storage.ScriptRecordingObservation
		if err := decoder.Decode(&observation); err != nil {
			return nil, err
		}
		stored = append(stored, observation)
	}
	token, err = decoder.Token()
	if err != nil {
		return nil, err
	}
	close, ok := token.(json.Delim)
	if !ok || close != ']' {
		return nil, errors.New("recording baseline must end with a JSON array delimiter")
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("recording baseline contains trailing JSON")
		}
		return nil, err
	}
	return stored, nil
}
