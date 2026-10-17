package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

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
var importProvenanceEvidenceKeys = importreview.ImportProvenanceEvidenceKeys
var httpImportProvenanceEvidenceKeys = importreview.HttpImportProvenanceEvidenceKeys
var importProvenanceIdentifierKeys = importreview.ImportProvenanceIdentifierKeys

// importProvenanceEvidenceKeyPrefixes marks whole families of provenance
// evidence. AsyncAPI records channel/operation provenance under generated
// asyncapi_* keys rather than a fixed list.
var importProvenanceEvidenceKeyPrefixes = importreview.ImportProvenanceEvidenceKeyPrefixes

var normalizeImportProvenanceKey = importreview.NormalizeImportProvenanceKey

// isPortableImportProvenanceIdentifierKey reports whether key names a portable
// source identifier that survives script projection.
var isPortableImportProvenanceIdentifierKey = importreview.IsPortableImportProvenanceIdentifierKey

// isImportProvenanceMetadataKey reports whether key is import provenance of
// either class, and therefore never demands a credential binding.
var isImportProvenanceMetadataKey = importreview.IsImportProvenanceMetadataKey
