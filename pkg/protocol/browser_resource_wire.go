package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"time"
)

// A resource record contains at most five phase pairs and bounded opaque IDs.
// This bound applies before JSON materialization at every retained wire reader.
const MaxBrowserResourceEvidenceBytes = 16 << 10

var browserResourceWireUTC = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)

// DecodeBrowserResourceEvidence preserves required observed zero/false values
// without admitting omitted fields as Go zero values. It returns an owned copy
// and fixed errors, never source text. Explicit null phases is a valid nil Go
// collection; bytes and the required scalar fields must be present and typed.
func DecodeBrowserResourceEvidence(raw []byte) (*BrowserResourceEvidence, error) {
	invalid := errors.New("browser resource evidence is malformed or incomplete")
	if !browserDocumentWirePresence(raw, reflect.TypeOf(BrowserResourceEvidence{}), MaxBrowserResourceEvidenceBytes) {
		return nil, invalid
	}
	var timestamp struct {
		ObservedAt string `json:"observed_at"`
	}
	if json.Unmarshal(raw, &timestamp) != nil || !browserResourceWireUTC.MatchString(timestamp.ObservedAt) {
		return nil, invalid
	}
	// time.Time's JSON decoder accepts fractional precision beyond nanoseconds by
	// truncating it. Refuse that lossy shape before it can masquerade as fidelity.
	if at, err := time.Parse(time.RFC3339Nano, timestamp.ObservedAt); err != nil || at.IsZero() {
		return nil, invalid
	}
	var evidence BrowserResourceEvidence
	if json.Unmarshal(raw, &evidence) != nil {
		return nil, invalid
	}
	owned := evidence.Clone()
	if owned == nil {
		return nil, invalid
	}
	return owned, nil
}
