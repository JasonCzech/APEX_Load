package api

import "strings"

// Import provenance metadata records where an imported action came from. Two
// classes live under the same umbrella and they are deliberately NOT the same
// set — collapsing them is the failure mode this file exists to prevent.
//
//   - Identifier keys name a location in the source artifact. Their values are
//     opaque identifiers and archive-relative paths, so they are portable: safe
//     to project to a client and safe to carry through a masked
//     read-modify-write.
//
//   - Evidence keys record the SHAPE of the original request. They are exempt
//     from secret-binding discovery for the same reason (they are provenance,
//     not an executable field awaiting a credential), but their values can
//     embed authored credentials — original_url routinely carries query
//     credentials and userinfo. They must never be treated as portable.
//
// isImportProvenanceMetadataKey is the protocol-independent union, while
// isPortableImportProvenanceIdentifierKey is the identifier set alone.
// Additional HTTP evidence keys require action context because other protocols
// may send identically named metadata on the wire.
var (
	importProvenanceIdentifierKeys = map[string]struct{}{
		"source_path":    {},
		"source_step_id": {},
		"source_profile": {},
		"source_uid":     {},
	}
	importProvenanceEvidenceKeys = map[string]struct{}{
		"original_url":       {},
		"source_transaction": {},
	}
	// These names are non-wire evidence only on recognized HTTP actions.
	// Messaging protocols may send identically named metadata as properties.
	httpImportProvenanceEvidenceKeys = map[string]struct{}{
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

// importProvenanceEvidenceKeyPrefixes marks whole families of provenance
// evidence. AsyncAPI records channel/operation provenance under generated
// asyncapi_* keys rather than a fixed list.
var importProvenanceEvidenceKeyPrefixes = []string{"asyncapi_"}

func normalizeImportProvenanceKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// isPortableImportProvenanceIdentifierKey reports whether key names a portable
// source identifier that survives script projection.
func isPortableImportProvenanceIdentifierKey(key string) bool {
	_, ok := importProvenanceIdentifierKeys[normalizeImportProvenanceKey(key)]
	return ok
}

// isImportProvenanceMetadataKey reports whether key is import provenance of
// either class, and therefore never demands a credential binding.
func isImportProvenanceMetadataKey(key string) bool {
	normalized := normalizeImportProvenanceKey(key)
	if _, ok := importProvenanceIdentifierKeys[normalized]; ok {
		return true
	}
	if _, ok := importProvenanceEvidenceKeys[normalized]; ok {
		return true
	}
	for _, prefix := range importProvenanceEvidenceKeyPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}
