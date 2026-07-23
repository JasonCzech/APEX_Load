package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/storage"
)

const correlationRulesetPreviewTokenVersion = "apexload.correlation-ruleset-preview.v1"

// correlationRulesetPreviewBinding is deliberately never serialized to the
// client. Its HMAC is an opaque optimistic-concurrency token proving that an
// apply request still describes the exact ruleset, script, recording, matched
// selection, and diff that the server previewed.
type correlationRulesetPreviewBinding struct {
	Version                    string `json:"version"`
	RulesetID                  string `json:"ruleset_id"`
	RulesetUpdatedAt           string `json:"ruleset_updated_at"`
	ScriptID                   string `json:"script_id"`
	ScriptUpdatedAt            string `json:"script_updated_at"`
	ActionGraphDigest          string `json:"action_graph_digest"`
	RecordingProjectID         string `json:"recording_project_id"`
	RecordingScriptIncarnation string `json:"recording_script_incarnation"`
	RecordingUpdatedAt         string `json:"recording_updated_at"`
	RecordingActionGraphDigest string `json:"recording_action_graph_digest"`
	RecordingCiphertextSHA256  string `json:"recording_ciphertext_sha256"`
	MatchedSelectionSHA256     string `json:"matched_selection_sha256"`
	PreviewDiffSHA256          string `json:"preview_diff_sha256"`
}

func canonicalPreviewTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func previewPayloadSHA256(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// correlationRulesetPreviewSigningKey is derived from the recording-baseline
// key because every ruleset preview necessarily decrypts that baseline first.
// The operator-provided key is shared across HA replicas, while domain
// separation prevents reuse of the AES material as raw HMAC key bytes.
func correlationRulesetPreviewSigningKey() ([]byte, error) {
	configured := strings.TrimSpace(os.Getenv(recordingBaselineKeyEnvVar))
	if configured == "" {
		return nil, fmt.Errorf("correlation ruleset preview signing is unavailable because %s is not configured", recordingBaselineKeyEnvVar)
	}
	digest := sha256.Sum256([]byte("apexload.correlation-ruleset-preview.hmac.v1\x00" + configured))
	return digest[:], nil
}

func correlationRulesetPreviewToken(
	ruleset *storage.CorrelationRuleset,
	stored *StoredScript,
	record *storage.ScriptRecordingRecord,
	actionGraphDigest string,
	matched []paramize.Suggestion,
	diffs []paramize.Diff,
) (string, error) {
	if ruleset == nil || stored == nil || stored.Script == nil || record == nil {
		return "", fmt.Errorf("correlation ruleset preview state is incomplete")
	}
	matchedDigest, err := previewPayloadSHA256(matched)
	if err != nil {
		return "", fmt.Errorf("hash matched correlation selection: %w", err)
	}
	diffDigest, err := previewPayloadSHA256(diffs)
	if err != nil {
		return "", fmt.Errorf("hash correlation preview diff: %w", err)
	}
	ciphertextDigest := sha256.Sum256([]byte(record.EncryptedCorrelationObservations))
	binding := correlationRulesetPreviewBinding{
		Version:                    correlationRulesetPreviewTokenVersion,
		RulesetID:                  strings.TrimSpace(ruleset.ID),
		RulesetUpdatedAt:           canonicalPreviewTime(ruleset.UpdatedAt),
		ScriptID:                   strings.TrimSpace(stored.ID),
		ScriptUpdatedAt:            canonicalPreviewTime(stored.Script.UpdatedAt),
		ActionGraphDigest:          strings.TrimSpace(actionGraphDigest),
		RecordingProjectID:         strings.TrimSpace(record.ProjectID),
		RecordingScriptIncarnation: strings.TrimSpace(record.ScriptIncarnation),
		RecordingUpdatedAt:         canonicalPreviewTime(record.UpdatedAt),
		RecordingActionGraphDigest: strings.TrimSpace(record.ActionGraphDigest),
		RecordingCiphertextSHA256:  hex.EncodeToString(ciphertextDigest[:]),
		MatchedSelectionSHA256:     matchedDigest,
		PreviewDiffSHA256:          diffDigest,
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		return "", fmt.Errorf("encode correlation preview binding: %w", err)
	}
	key, err := correlationRulesetPreviewSigningKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return "crp1_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func correlationRulesetPreviewTokenMatches(actual, expected string) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	return actual != "" && expected != "" && hmac.Equal([]byte(actual), []byte(expected))
}
