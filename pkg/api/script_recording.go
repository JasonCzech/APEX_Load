package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxRecordedBaselineObservations  = 500
	maxRecordedBaselinePlaintextSize = 12 << 20
)

var (
	errRecordingActionGraphDigestMissing  = errors.New("recorded baseline has no action-graph digest")
	errRecordingActionGraphDigestMismatch = errors.New("recorded baseline belongs to a different action graph")
	errRecordingScriptBindingMismatch     = errors.New("recorded baseline belongs to a different script incarnation")
	errRecordingBaselineUnauthenticated   = errors.New("recorded correlation baseline has no authenticated encrypted sidecar")
	errRecordingCaptureSourceNotFound     = errors.New("source recording session was not found")
	errRecordingCaptureUnattested         = errors.New("script is not an attested conversion of the source recording")
)

type attestedRecordingBaseline struct {
	source       string
	observations []correlate.Observation
}

type correlationActionGraph struct {
	Protocol string           `json:"protocol"`
	Config   dsl.ScriptConfig `json:"config"`
	Init     []dsl.Action     `json:"init"`
	Actions  []dsl.Action     `json:"actions"`
	Teardown []dsl.Action     `json:"teardown"`
}

// scriptCorrelationActionGraphDigest returns a stable identity for the exact
// executable action topology and request surfaces to which correlation
// observation IDs refer. The full runtime config is included conservatively:
// base URLs, protocol defaults, authentication, and other config can change
// emitted traffic without changing an action. Script metadata, timestamps, and
// display-only fields are deliberately excluded. encoding/json sorts
// string-keyed maps, and the non-nil slice projection makes nil and empty
// action sections equivalent.
func scriptCorrelationActionGraphDigest(script *dsl.Script) (string, error) {
	if script == nil {
		return "", fmt.Errorf("script is required")
	}
	actions := func(input []dsl.Action) []dsl.Action {
		if len(input) == 0 {
			return []dsl.Action{}
		}
		return input
	}
	payload, err := json.Marshal(correlationActionGraph{
		Protocol: strings.ToLower(strings.TrimSpace(script.Protocol)),
		Config:   script.Config,
		Init:     actions(script.Init),
		Actions:  actions(script.Actions),
		Teardown: actions(script.Teardown),
	})
	if err != nil {
		return "", fmt.Errorf("marshal script action graph: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateRecordingActionGraph(record *storage.ScriptRecordingRecord, script *dsl.Script) error {
	if record == nil {
		return errRecordingActionGraphDigestMissing
	}
	expected := strings.TrimSpace(record.ActionGraphDigest)
	if expected == "" {
		return errRecordingActionGraphDigestMissing
	}
	actual, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		return err
	}
	if expected != actual {
		return errRecordingActionGraphDigestMismatch
	}
	if script == nil || normalizeProjectID(record.ProjectID) != normalizeProjectID(script.ProjectID) || strings.TrimSpace(record.ScriptIncarnation) == "" || record.ScriptIncarnation != storage.ScriptIncarnation(script.ID, script.ProjectID, script.RefKey, script.CreatedAt) {
		return errRecordingScriptBindingMismatch
	}
	return nil
}

// redactBaselineObservation returns the disclosure-safe persistence view of a
// recorded observation. Heuristic redaction is insufficient here because
// arbitrary URLs, headers, and bodies can contain PII or business identifiers
// that do not look like credentials. Keep only the action index; RequestCount
// on the containing record provides the count projection. Full values live
// exclusively in the encrypted correlation sidecar.
func redactBaselineObservation(obs correlate.Observation) storage.ScriptRecordingObservation {
	return storage.ScriptRecordingObservation{
		ID: obs.ID,
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

// scrubRepairSuggestions removes the literal recorded/replay values carried on
// correlation repair suggestions before they leave the server. Detection runs
// on the raw baseline sidecar for accuracy, and every dynamic literal must be
// treated as secret regardless of length, entropy, or whether scrub.Value's
// credential heuristics happen to recognize it. Structural repair locations
// remain available so an authorized dashboard can locate the stale text in the
// replay data it already owns without the server echoing baseline material. A
// legacy suggestion without locations is downgraded to manual replacement
// because its now-withheld literal cannot be applied safely.
func scrubRepairSuggestions(suggestions []correlate.RepairSuggestion) []correlate.RepairSuggestion {
	for i := range suggestions {
		hadRepairLiteral := suggestions[i].RecordedValue != "" || suggestions[i].ReplayValue != ""
		suggestions[i].RecordedValue = ""
		suggestions[i].ReplayValue = ""
		if hadRepairLiteral && len(suggestions[i].RepairLocations) == 0 {
			suggestions[i].ReviewAction = "manual_value_replacement_required"
		}
	}
	return suggestions
}

// persistRecordedBaseline stores a structural recorded baseline view plus an
// encrypted raw correlation sidecar for a script. It is best-effort: a nil
// store, empty baseline, or save error must not fail the surrounding import.
func (s *Server) persistRecordedBaseline(scriptID, source string, script *dsl.Script, observations []correlate.Observation) error {
	scriptID = strings.TrimSpace(scriptID)
	if s.store == nil || scriptID == "" || len(observations) == 0 {
		return nil
	}
	actionGraphDigest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		return fmt.Errorf("bind recorded baseline to script: %w", err)
	}
	parent, err := s.store.GetScript(scriptID)
	if err != nil || parent == nil {
		if err == nil {
			err = storage.ErrScriptRecordingParentChanged
		}
		return fmt.Errorf("bind recorded baseline to live script: %w", err)
	}
	parentActionGraphDigest, err := scriptCorrelationActionGraphDigest(parent)
	if err != nil || parentActionGraphDigest != actionGraphDigest {
		return fmt.Errorf("bind recorded baseline to live script: %w", storage.ErrScriptRecordingParentChanged)
	}
	capacity := len(observations)
	if capacity > maxRecordedBaselineObservations {
		capacity = maxRecordedBaselineObservations
	}
	redacted := make([]storage.ScriptRecordingObservation, 0, capacity)
	raw := make([]storage.ScriptRecordingObservation, 0, capacity)
	estimatedBytes := 2 // JSON array brackets.
	for _, obs := range observations {
		if len(raw) >= maxRecordedBaselineObservations {
			break
		}
		rawObservation := rawBaselineObservation(obs)
		nextSize := estimatedBytes + estimatedRecordingObservationJSONSize(rawObservation)
		if len(raw) > 0 {
			nextSize++ // comma between array elements
		}
		if nextSize > maxRecordedBaselinePlaintextSize {
			break
		}
		estimatedBytes = nextSize
		raw = append(raw, rawObservation)
		redacted = append(redacted, redactBaselineObservation(obs))
	}
	record := &storage.ScriptRecordingRecord{
		ScriptID:          scriptID,
		ProjectID:         normalizeProjectID(parent.ProjectID),
		ScriptIncarnation: storage.ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt),
		ScriptUpdatedAt:   parent.UpdatedAt,
		SchemaVersion:     recordingBaselineSchemaVersion,
		ActionGraphDigest: actionGraphDigest,
		Source:            source,
		Sensitivity:       scrub.SensitivityScrubbed,
		// RequestCount describes the original recording. The retained baseline
		// can be smaller because of the observation-count or plaintext-size cap.
		RequestCount: len(observations),
		Observations: redacted,
	}
	// Even when the first raw exchange alone exceeds the authenticated
	// plaintext budget, persist an empty structural projection with the
	// original RequestCount. That row is the authoritative witness that a
	// capture occurred and was fully truncated; silently omitting it would make
	// status and recapture guidance indistinguishable from "never recorded".
	if len(raw) > 0 && recordingBaselineEncryptionAvailable() {
		payload, err := json.Marshal(raw)
		if err == nil {
			// The estimator is deliberately conservative, but enforce the exact
			// plaintext bound before encryption as a final defense against a future
			// observation-field change that the estimator does not yet know about.
			for len(payload) > maxRecordedBaselinePlaintextSize && len(raw) > 0 {
				raw = raw[:len(raw)-1]
				redacted = redacted[:len(redacted)-1]
				payload, err = json.Marshal(raw)
				if err != nil {
					break
				}
			}
			if err == nil && len(raw) > 0 {
				record.Observations = redacted
				if encrypted, err := encryptRecordingBaseline(payload, recordingBaselineBinding{
					ScriptID:          record.ScriptID,
					ProjectID:         record.ProjectID,
					ScriptIncarnation: record.ScriptIncarnation,
					ActionGraphDigest: record.ActionGraphDigest,
					SchemaVersion:     record.SchemaVersion,
				}); err == nil {
					record.EncryptedCorrelationObservations = encrypted
					record.Sensitivity = "encrypted_contains_secrets"
				}
			}
		}
	}
	return s.store.SaveScriptRecording(record)
}

func estimatedRecordingObservationJSONSize(observation storage.ScriptRecordingObservation) int {
	// encoding/json can expand one input byte to at most six bytes (for control,
	// invalid UTF-8, and HTML-escaped characters). Include generous structural
	// overhead for field names, delimiters, the integer ID, and header entries.
	size := 256
	add := func(value string) { size += 6 * len(value) }
	add(observation.RequestURL)
	add(observation.RequestBody)
	add(observation.ResponseBody)
	for key, value := range observation.RequestHeaders {
		size += 16
		add(key)
		add(value)
	}
	for key, value := range observation.ResponseHeaders {
		size += 16
		add(key)
		add(value)
	}
	return size
}

// attestRecordingBaselineCapture resolves one immutable recording snapshot and
// verifies that the submitted script has the exact executable action graph the
// server's converter derives from that snapshot. A caller-controlled session ID
// is therefore not enough to bind an arbitrary script to another recording's
// encrypted correlation evidence. The returned observations come from the same
// snapshot used for verification so a live session cannot change between the
// provenance check and persistence.
func (s *Server) attestRecordingBaselineCapture(scriptProjectID string, script *dsl.Script, sessionID string) (*attestedRecordingBaseline, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil
	}
	// A source id can be supplied by either recording surface. Resolve the
	// headed-browser namespace first so an unlikely id collision can never
	// attach an unrelated proxy baseline to a browser-generated script.
	if s.browserRecording != nil {
		if recording, ok := s.browserRecording.Get(sessionID); ok {
			if normalizeProjectID(recording.ProjectID) != normalizeProjectID(scriptProjectID) {
				return nil, errRecordingCaptureSourceNotFound
			}
			return nil, fmt.Errorf("%w: headed-browser drafts must be saved through the server-side browser conversion endpoint", errRecordingCaptureUnattested)
		}
	}
	if s.recorder == nil {
		return nil, errRecordingCaptureSourceNotFound
	}
	session, ok := s.recorder.GetSession(sessionID)
	if !ok {
		return nil, errRecordingCaptureSourceNotFound
	}
	if normalizeProjectID(session.ProjectID) != normalizeProjectID(scriptProjectID) {
		return nil, errRecordingCaptureSourceNotFound
	}
	submittedDigest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errRecordingCaptureUnattested, err)
	}
	for _, parameterize := range []bool{false, true} {
		converted := recorder.ConvertSessionWithOptions(session, script.Name, recorder.ConvertOptions{Parameterize: parameterize})
		if converted == nil || converted.Script == nil {
			continue
		}
		candidate, _, bindingErr := applySuggestedSecretBindings(converted.Script, 0)
		if bindingErr != nil || candidate == nil {
			continue
		}
		candidateDigest, digestErr := scriptCorrelationActionGraphDigest(candidate)
		if digestErr == nil && candidateDigest == submittedDigest {
			return &attestedRecordingBaseline{
				source:       "proxy_recording",
				observations: recorder.SessionBaseline(session),
			}, nil
		}
	}
	return nil, errRecordingCaptureUnattested
}
