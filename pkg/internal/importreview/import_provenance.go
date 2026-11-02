package importreview

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	ImportProvenanceIdentifierKeys = map[string]struct{}{
		"source_path":                  {},
		"source_step_id":               {},
		"source_profile":               {},
		"source_uid":                   {},
		dsl.ImportActionUIDMetadataKey: {},
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
		"salesforce":             {},
		"mulesoft":               {},
		"agentforce":             {},
		"import_review":          {},
		"source_refs":            {},
		"source_mappings":        {},
		"source_ref":             {},
	}
)

var ImportProvenanceEvidenceKeyPrefixes = []string{"asyncapi_"}

var HttpImportProvenanceEvidenceKeyPrefixes = []string{"salesforce_"}

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

func IsHTTPImportProvenanceMetadataKey(key string) bool {
	if IsImportProvenanceMetadataKey(key) {
		return true
	}
	normalized := NormalizeImportProvenanceKey(key)
	if _, ok := HttpImportProvenanceEvidenceKeys[normalized]; ok {
		return true
	}
	for _, prefix := range HttpImportProvenanceEvidenceKeyPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
