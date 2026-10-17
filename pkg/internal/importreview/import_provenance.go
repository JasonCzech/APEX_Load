package importreview

import (
	"strings"
)

var (
	ImportProvenanceIdentifierKeys = map[string]struct{}{
		"source_path":    {},
		"source_step_id": {},
		"source_profile": {},
		"source_uid":     {},
	}
	ImportProvenanceEvidenceKeys = map[string]struct{}{
		"original_url":       {},
		"source_transaction": {},
	}
	// These names are non-wire evidence only on recognized HTTP actions.
	// Messaging protocols may send identically named metadata as properties.
	HttpImportProvenanceEvidenceKeys = map[string]struct{}{
		"source_subtransaction":  {},
		"source_action_name":     {},
		"source_file":            {},
		"source_function":        {},
		"phase":                  {},
		"source_secret_function": {},
		"source_secret_ref":      {},
		"body_gated_by_secret":   {},
		"masked_body_raw":        {},
		"masked_body_function":   {},
	}
)

var ImportProvenanceEvidenceKeyPrefixes = []string{"asyncapi_"}

func NormalizeImportProvenanceKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func IsPortableImportProvenanceIdentifierKey(key string) bool {
	_, ok := ImportProvenanceIdentifierKeys[NormalizeImportProvenanceKey(key)]
	return ok
}

func IsImportProvenanceMetadataKey(key string) bool {
	normalized := NormalizeImportProvenanceKey(key)
	if _, ok := ImportProvenanceIdentifierKeys[normalized]; ok {
		return true
	}
	if _, ok := ImportProvenanceEvidenceKeys[normalized]; ok {
		return true
	}
	for _, prefix := range ImportProvenanceEvidenceKeyPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
