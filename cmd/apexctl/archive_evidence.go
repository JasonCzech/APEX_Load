package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
)

const archiveEvidenceMaxResponse = 4 << 20

type archiveEvidenceExpectation struct {
	TestID          string    `json:"test_id"`
	ProjectID       string    `json:"project_id"`
	CreatedAt       time.Time `json:"created_at"`
	Status          string    `json:"status"`
	MetadataBackend string    `json:"metadata_backend"`
}

type archiveEvidenceRun struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"project_id"`
	CreatedAt time.Time  `json:"created_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Status    string     `json:"status"`
	Archive   *struct {
		TestID         string `json:"test_id"`
		ManifestSHA256 string `json:"manifest_sha256"`
	} `json:"archive"`
	ArchiveFailure *struct {
		Reason string `json:"reason"`
	} `json:"archive_failure"`
}

type archiveEvidenceManifest struct {
	SchemaVersion string    `json:"schema_version"`
	TestID        string    `json:"test_id"`
	Status        string    `json:"status"`
	CompletedAt   time.Time `json:"completed_at"`
	Provenance    struct {
		ProjectID   string    `json:"project_id"`
		RequestedAt time.Time `json:"requested_at"`
	} `json:"provenance"`
	SourceCoverage *api.RunArchiveSourceCoverage `json:"source_coverage"`
}

// This projection intentionally excludes raw errors, script/settings data,
// artifact names, source addresses and controller URLs/credentials.
type archiveEvidenceReport struct {
	SchemaVersion     string                     `json:"schema_version"`
	Expected          archiveEvidenceExpectation `json:"expected"`
	ObservedAt        time.Time                  `json:"observed_at"`
	Passed            bool                       `json:"passed"`
	Reason            string                     `json:"reason"`
	Stage             string                     `json:"stage"`
	HTTPStatus        int                        `json:"http_status,omitempty"`
	IdentityMatched   bool                       `json:"identity_matched"`
	ManifestSHA256    string                     `json:"manifest_sha256,omitempty"`
	IntegrityVerified bool                       `json:"integrity_verified"`
	IntegrityStatus   string                     `json:"integrity_status,omitempty"`
	VerifiedAt        *time.Time                 `json:"verified_at,omitempty"`
	VerifiedArtifacts int                        `json:"verified_artifacts"`
	SourceCoverage    string                     `json:"source_coverage"`
	Sources           int                        `json:"sources"`
	MissingSources    int                        `json:"missing_sources"`
	ArchiveFailure    string                     `json:"archive_failure,omitempty"`
}

func archiveEvidenceTimestamp(at time.Time, backend string) time.Time {
	if backend == "postgres" {
		return at.Truncate(time.Microsecond)
	}
	return at
}

func archiveEvidenceHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (c *ApiClient) archiveEvidenceRead(ctx context.Context, path string, destination any) (int, string) {
	reader := c
	if _, bounded := ctx.Deadline(); bounded {
		// Evidence has an explicit overall budget. Cold archive preparation can
		// exceed the ordinary status-read timeout; let that same caller context
		// bound headers and body without changing the shared client or transport.
		copy := *c
		httpClient := http.Client{}
		if c.Client != nil {
			httpClient = *c.Client
		}
		httpClient.Timeout = 0
		copy.Client = &httpClient
		reader = &copy
	}
	response, err := reader.doStatusReadContext(ctx, path)
	if err != nil {
		return 0, "api_unavailable"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, "api_unavailable"
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, archiveEvidenceMaxResponse+1))
	if err != nil {
		return response.StatusCode, "api_unavailable"
	}
	if len(body) > archiveEvidenceMaxResponse {
		return response.StatusCode, "response_too_large"
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return response.StatusCode, "invalid_response"
	}
	return response.StatusCode, ""
}

func archiveEvidenceRunMatches(run *archiveEvidenceRun, expected archiveEvidenceExpectation) bool {
	return run.ID == expected.TestID && run.ProjectID == expected.ProjectID && !run.CreatedAt.IsZero() &&
		archiveEvidenceTimestamp(run.CreatedAt, expected.MetadataBackend).Equal(archiveEvidenceTimestamp(expected.CreatedAt, expected.MetadataBackend))
}

func archiveEvidenceIntegrity(report *api.RunArchiveIntegrityReport, testID, manifestHash string) bool {
	if report.SchemaVersion != "apexload.archive_integrity.v1" || report.TestID != testID ||
		!report.Verified || report.Status != "verified" || report.Total < 2 || report.Total > 4097 ||
		report.Matched != report.Total || report.Mismatched != 0 || report.Missing != 0 || report.Unverifiable != 0 ||
		len(report.Artifacts) != report.Total || report.VerifiedAt.IsZero() {
		return false
	}
	seen := make(map[string]bool, report.Total)
	anchored := false
	for _, artifact := range report.Artifacts {
		if artifact.Name == "" || seen[artifact.Name] || artifact.Status != "match" || artifact.SizeBytes < 0 ||
			!archiveEvidenceHash(artifact.ExpectedSHA256) || !strings.EqualFold(artifact.ExpectedSHA256, artifact.ActualSHA256) {
			return false
		}
		seen[artifact.Name] = true
		if artifact.Name == "manifest.json" {
			anchored = strings.EqualFold(artifact.ExpectedSHA256, manifestHash)
		}
	}
	return anchored
}

func collectArchiveEvidence(ctx context.Context, client *ApiClient, expected archiveEvidenceExpectation) *archiveEvidenceReport {
	result := &archiveEvidenceReport{SchemaVersion: "apexload.archive_evidence.v1", Expected: expected,
		ObservedAt: time.Now().UTC(), Reason: "invalid_expectation", SourceCoverage: "unknown"}
	if expected.validate() != nil {
		return result
	}
	path := "/api/v1/tests/" + url.PathEscape(expected.TestID)
	read := func(stage, suffix string, destination any) bool {
		result.Stage = stage
		status, reason := client.archiveEvidenceRead(ctx, path+suffix, destination)
		result.HTTPStatus = status
		if reason != "" {
			result.Reason = reason
			return false
		}
		return true
	}
	var before archiveEvidenceRun
	if !read("test_before", "", &before) {
		return result
	}
	if !archiveEvidenceRunMatches(&before, expected) {
		result.Reason = "identity_mismatch"
		return result
	}
	if before.Status != expected.Status {
		result.Reason = "unexpected_run_status"
		return result
	}
	if before.EndedAt == nil || before.EndedAt.IsZero() || before.EndedAt.Before(before.CreatedAt) {
		result.Reason = "invalid_terminal_time"
		return result
	}
	if before.Archive == nil {
		result.Reason = "archive_missing"
		if before.ArchiveFailure != nil {
			switch before.ArchiveFailure.Reason {
			case "remote_collection_failed", "remote_collection_unavailable":
				result.ArchiveFailure = before.ArchiveFailure.Reason
				result.Reason = "archive_collection_failed"
			}
		}
		return result
	}
	if before.Archive.TestID != expected.TestID || !archiveEvidenceHash(before.Archive.ManifestSHA256) {
		result.Reason = "invalid_archive_identity"
		return result
	}
	result.ManifestSHA256 = strings.ToLower(before.Archive.ManifestSHA256)
	var manifest archiveEvidenceManifest
	if !read("manifest", "/archive/manifest", &manifest) {
		return result
	}
	if manifest.SchemaVersion != "apexload.archive.v4" {
		result.Reason = "unsupported_manifest_schema"
		return result
	}
	if manifest.TestID != expected.TestID || manifest.Provenance.ProjectID != expected.ProjectID ||
		manifest.Status != expected.Status || manifest.Provenance.RequestedAt.IsZero() ||
		!archiveEvidenceTimestamp(manifest.Provenance.RequestedAt, expected.MetadataBackend).Equal(archiveEvidenceTimestamp(expected.CreatedAt, expected.MetadataBackend)) ||
		!archiveEvidenceTimestamp(manifest.CompletedAt, expected.MetadataBackend).Equal(archiveEvidenceTimestamp(*before.EndedAt, expected.MetadataBackend)) {
		result.Reason = "manifest_identity_mismatch"
		return result
	}
	if coverage := manifest.SourceCoverage; coverage != nil {
		if (coverage.Mode != "required" && coverage.Mode != "best_effort") || len(coverage.Sources) == 0 || len(coverage.Sources) > 4096 {
			result.Reason = "invalid_source_coverage"
			return result
		}
		seen := map[string]bool{}
		for _, source := range coverage.Sources {
			if strings.TrimSpace(source.Target) == "" || seen[source.Target] {
				result.Reason = "invalid_source_coverage"
				return result
			}
			seen[source.Target] = true
			if !source.Collected {
				result.MissingSources++
			}
		}
		result.Sources = len(coverage.Sources)
		result.SourceCoverage = "complete_snapshot"
		if coverage.Mode == "best_effort" {
			result.SourceCoverage = "best_effort"
		}
		if result.MissingSources > 0 {
			result.SourceCoverage = "partial"
		}
	}
	var integrity api.RunArchiveIntegrityReport
	verificationStarted := time.Now()
	if !read("integrity", "/archive/verify", &integrity) {
		return result
	}
	// Verification is useful as post-fault evidence only when the controller's
	// observation belongs to this request window. Allow bounded host clock skew.
	if integrity.VerifiedAt.Before(verificationStarted.Add(-5*time.Second)) || integrity.VerifiedAt.After(time.Now().Add(5*time.Second)) {
		result.Reason = "stale_integrity_response"
		return result
	}
	switch integrity.Status {
	case "verified", "verified_partial", "tampered", "corrupt", "unverifiable":
		result.IntegrityStatus = integrity.Status
	default:
		result.Reason = "invalid_integrity_response"
		return result
	}
	result.IntegrityVerified = archiveEvidenceIntegrity(&integrity, expected.TestID, result.ManifestSHA256)
	if !integrity.VerifiedAt.IsZero() {
		result.VerifiedAt = &integrity.VerifiedAt
	}
	if result.IntegrityVerified {
		result.VerifiedArtifacts = integrity.Matched
	}
	var after archiveEvidenceRun
	if !read("test_after", "", &after) {
		return result
	}
	if !archiveEvidenceRunMatches(&after, expected) || after.Status != before.Status || after.EndedAt == nil ||
		!archiveEvidenceTimestamp(*after.EndedAt, expected.MetadataBackend).Equal(archiveEvidenceTimestamp(*before.EndedAt, expected.MetadataBackend)) ||
		after.Archive == nil || after.Archive.TestID != expected.TestID || !strings.EqualFold(after.Archive.ManifestSHA256, result.ManifestSHA256) {
		result.Reason = "run_changed_during_collection"
		return result
	}
	if ctx.Err() != nil {
		result.Reason = "deadline_exceeded"
		return result
	}
	result.IdentityMatched = true
	result.Reason = "integrity_not_verified"
	if !result.IntegrityVerified {
		return result
	}
	result.Reason = "source_coverage_not_complete"
	if result.SourceCoverage != "complete_snapshot" {
		return result
	}
	result.Passed, result.Reason = true, "verified"
	return result
}

func (expected archiveEvidenceExpectation) validate() error {
	if expected.TestID == "" || expected.ProjectID == "" || expected.CreatedAt.IsZero() {
		return fmt.Errorf("test, project and created-at are required")
	}
	if expected.MetadataBackend != "sqlite" && expected.MetadataBackend != "postgres" {
		return fmt.Errorf("metadata-backend must be sqlite or postgres")
	}
	if expected.Status != "COMPLETE" && expected.Status != "FAILED" && expected.Status != "ABORTED" {
		return fmt.Errorf("status must be COMPLETE, FAILED or ABORTED")
	}
	return nil
}
