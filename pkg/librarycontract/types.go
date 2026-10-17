// Package librarycontract contains shared client/server contracts without controller runtime dependencies.
package librarycontract

import (
	"time"
)

type LibraryBundleSource struct {
	InstanceID string `json:"instance_id"`
	ProjectID  string `json:"project_id,omitempty"`
}

type LibraryDependency struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type LibraryTransferRequest struct {
	Kind                string `json:"kind"`
	ID                  string `json:"id"`
	LocalID             string `json:"local_id,omitempty"`
	RemoteID            string `json:"remote_id,omitempty"`
	ProjectID           string `json:"project_id,omitempty"`        // local authorized project (pull: local destination)
	ApplicationID       string `json:"application_id,omitempty"`    // local destination application for result pulls
	ReleaseID           string `json:"release_id,omitempty"`        // local destination release for result pulls
	RemoteProjectID     string `json:"remote_project_id,omitempty"` // optional source/destination project on the remote controller
	IncludeDependencies bool   `json:"include_dependencies,omitempty"`
}

type LibraryImportResponse struct {
	SchemaVersion string                    `json:"schema_version"`
	ImportedAt    time.Time                 `json:"imported_at"`
	Items         []LibraryImportResultItem `json:"items"`
	Conflicts     []LibraryConflict         `json:"conflicts,omitempty"`
	Warnings      []string                  `json:"warnings,omitempty"`
}

type LibraryImportResultItem struct {
	Kind             string `json:"kind"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceItemID     string `json:"source_item_id"`
	LocalItemID      string `json:"local_item_id"`
	ContentHash      string `json:"content_hash"`
	Action           string `json:"action"`
}

type LibraryConflict struct {
	Kind             string `json:"kind"`
	SourceInstanceID string `json:"source_instance_id"`
	SourceItemID     string `json:"source_item_id"`
	LocalItemID      string `json:"local_item_id,omitempty"`
	Reason           string `json:"reason"`
	Code             string `json:"code,omitempty"`
	FeatureID        string `json:"feature_id,omitempty"`
	DomainID         string `json:"domain_id,omitempty"`
}
