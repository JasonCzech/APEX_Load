package librarycontract

import (
	"encoding/json"
	"time"
)

// LibraryBundle is a transport envelope. Items are complete opaque server
// snapshots: clients transfer their content without decoding executable scripts,
// controller runtime state, or arbitrary numeric metadata through float64.
type LibraryBundle struct {
	SchemaVersion string                     `json:"schema_version"`
	BundleID      string                     `json:"bundle_id"`
	CreatedAt     time.Time                  `json:"created_at"`
	Source        LibraryBundleSource        `json:"source"`
	Items         []json.RawMessage          `json:"items"`
	Provenance    map[string]json.RawMessage `json:"provenance,omitempty"`
}

type LibraryImportRequest struct {
	Bundle        LibraryBundle `json:"bundle"`
	ProjectID     string        `json:"project_id,omitempty"`
	ApplicationID string        `json:"application_id,omitempty"`
	ReleaseID     string        `json:"release_id,omitempty"`
	Mode          string        `json:"mode,omitempty"`
}
