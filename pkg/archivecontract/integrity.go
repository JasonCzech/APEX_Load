// Package archivecontract contains shared client/server contracts without controller runtime dependencies.
package archivecontract

import (
	"time"
)

type RunArchiveIntegrityArtifact struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	ActualSHA256   string `json:"actual_sha256,omitempty"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

type RunArchiveIntegrityReport struct {
	SchemaVersion string                        `json:"schema_version"`
	TestID        string                        `json:"test_id"`
	Status        string                        `json:"status"`
	Verified      bool                          `json:"verified"`
	VerifiedAt    time.Time                     `json:"verified_at"`
	Summary       string                        `json:"summary"`
	Total         int                           `json:"total"`
	Matched       int                           `json:"matched"`
	Mismatched    int                           `json:"mismatched"`
	Missing       int                           `json:"missing"`
	Unverifiable  int                           `json:"unverifiable"`
	Artifacts     []RunArchiveIntegrityArtifact `json:"artifacts,omitempty"`
}
