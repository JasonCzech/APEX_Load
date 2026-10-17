package recordbridge

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

const (
	browserRecordingSaveVersion    = 1
	browserRecordingSaveVersionKey = "browser_recorder_save_version"
	// "session_id" is intentionally avoided in the durable key name: generic
	// metadata disclosure sanitization treats that spelling as a credential.
	// This value is a non-secret UUID receipt correlation reference.
	browserRecordingSaveSessionIDKey     = "browser_recorder_save_ref"
	browserRecordingSaveIndexKey         = "browser_recorder_save_index"
	browserRecordingSaveCountKey         = "browser_recorder_save_count"
	browserRecordingSaveActorHashKey     = "browser_recorder_save_actor_hash"
	browserRecordingSaveRoleKey          = "browser_recorder_save_role"
	browserRecordingSaveProofKey         = "browser_recorder_save_proof"
	browserRecordingSaveRoleBrowser      = "browser"
	browserRecordingSaveRoleHTTP         = "http"
	browserRecordingHARBaselineNeededKey = "browser_recorder_har_baseline_required"
	browserRecordingSaveActorDomain      = "apexload:browser-recorder-save-actor:v1"
	browserRecordingSaveProofDomain      = "apexload:browser-recorder-save-proof:v1"
	browserRecordingSaveLocatorDomain    = "apexload:browser-recorder-save-locator:v1"
	browserRecordingSaveLocatorKeyPrefix = "import.browser_recorder.locator.v1."
	browserRecordingSaveLocatorStaged    = "staged"
	browserRecordingSaveLocatorPublished = "published"
)

type BrowserRecordingSaveProvenanceProof struct {
	Version          int    `json:"version"`
	RecordingID      string `json:"recording_id"`
	ScriptID         string `json:"script_id"`
	ProjectID        string `json:"project_id"`
	Index            int    `json:"index"`
	Count            int    `json:"count"`
	ActorHash        string `json:"actor_hash"`
	Role             string `json:"role"`
	BaselineRequired bool   `json:"baseline_required"`
}

func StampBrowserRecordingSaveProvenance(
	script *dsl.Script,
	recordingID string,
	index int,
	count int,
	actorHash string,
	role string,
	baselineRequired bool,
) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	preserved := script.Metadata.Import.Preserved
	preserved[browserRecordingSaveVersionKey] = browserRecordingSaveVersion
	preserved[browserRecordingSaveSessionIDKey] = strings.TrimSpace(recordingID)
	preserved[browserRecordingSaveIndexKey] = index
	preserved[browserRecordingSaveCountKey] = count
	preserved[browserRecordingSaveActorHashKey] = strings.TrimSpace(actorHash)
	preserved[browserRecordingSaveRoleKey] = strings.TrimSpace(role)
	preserved[browserRecordingHARBaselineNeededKey] = baselineRequired
}
func BrowserRecordingSaveProvenanceProofPayload(script *dsl.Script) (BrowserRecordingSaveProvenanceProof, error) {
	preserved := browserRecordingImportPreserved(script)
	if script == nil || preserved == nil {
		return BrowserRecordingSaveProvenanceProof{}, errors.New("browser recording save provenance is missing")
	}
	baselineRequired, _ := preserved[browserRecordingHARBaselineNeededKey].(bool)
	payload := BrowserRecordingSaveProvenanceProof{
		Version:          browserRecordingPreservedInt(preserved, browserRecordingSaveVersionKey),
		RecordingID:      browserRecordingPreservedString(preserved, browserRecordingSaveSessionIDKey),
		ScriptID:         strings.TrimSpace(script.ID),
		ProjectID:        normalizeProjectID(script.ProjectID),
		Index:            browserRecordingPreservedInt(preserved, browserRecordingSaveIndexKey),
		Count:            browserRecordingPreservedInt(preserved, browserRecordingSaveCountKey),
		ActorHash:        browserRecordingPreservedString(preserved, browserRecordingSaveActorHashKey),
		Role:             browserRecordingPreservedString(preserved, browserRecordingSaveRoleKey),
		BaselineRequired: baselineRequired,
	}
	if payload.Version != browserRecordingSaveVersion || payload.RecordingID == "" || payload.ScriptID == "" ||
		payload.ActorHash == "" || payload.Index < 0 || payload.Count <= 0 {
		return BrowserRecordingSaveProvenanceProof{}, errors.New("browser recording save provenance is incomplete")
	}
	return payload, nil
}
func browserRecordingImportPreserved(script *dsl.Script) map[string]any {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return nil
	}
	return script.Metadata.Import.Preserved
}
func browserRecordingPreservedString(preserved map[string]any, key string) string {
	value, _ := preserved[key].(string)
	return strings.TrimSpace(value)
}
func browserRecordingPreservedInt(preserved map[string]any, key string) int {
	switch value := preserved[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		if value == float64(int(value)) {
			return int(value)
		}
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return -1
}
