package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/agentforce"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/mulesoft"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type jmeterImportRequest struct {
	Data      []byte
	Documents []jmeterImportDocument
	ProjectID string
	Opts      jmeter.ConvertOptions
}

type jmeterImportDocument struct {
	Data     []byte
	FileName string
}

type enterpriseArchiveImportRequest struct {
	Data                 []byte
	ArchiveMember        string
	ArchiveMemberSize    int
	HARData              []byte
	SupplementalData     [][]byte
	SupplementalName     []string
	SupplementalManifest *enterpriseSupplementalArchiveManifest
	ProjectID            string
	Opts                 enterprisearchive.ConvertOptions
	ReadinessPolicy      enterprisearchive.RuntimeSettingMigrationPolicy
	SalesforceNative     bool
	ExpireDraft          bool
}

type devWebImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	IsZip     bool
	Opts      devweb.ConvertOptions
}

type harImportRequest struct {
	Data          []byte
	SecondaryData []byte
	ProjectID     string
	Opts          har.ConvertOptions
	// RecordingProfileID names a project recording profile the import
	// applies.
	RecordingProfileID string
	// Salvage describes the truncated capture whose complete entries Data
	// holds, when the import opted in to salvage_truncated.
	Salvage *har.TruncationSalvage
}

// applyClientGeneratedUUIDs gives UUIDs the recorded client made up (no
// response contains them) fresh values per replayed iteration; see package
// clientids. It records what changed, or why detection was skipped.
var applyClientGeneratedUUIDs = importreview.ApplyClientGeneratedUUIDs

// applyClientTimestamps gives epoch timestamps the recorded client made up
// (cache-busters, poll times) a fresh value at replay; see package
// clienttimestamps.
var applyClientTimestamps = importreview.ApplyClientTimestamps

// noteSalvage records on a converted import that its HAR was truncated.
func (req *harImportRequest) noteSalvage(result *har.ConvertResult) {
	if req.Salvage == nil || result == nil {
		return
	}
	warning := req.Salvage.Warning()
	result.Warnings = append([]string{warning}, result.Warnings...)
	result.Unsupported = append(result.Unsupported, diag.Diagnostic{Code: "har_truncated_salvaged", Element: "HAR truncation", Location: "HAR file", Detail: warning, Severity: diag.Warning})
}

type k6ImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      k6.ConvertOptions
}

type enterpriseArchiveImportSummary struct {
	ProjectFile                 string                                              `json:"project_file,omitempty"`
	MappedRuntimeFile           string                                              `json:"mapped_runtime_file,omitempty"`
	ProjectSections             []string                                            `json:"project_sections,omitempty"`
	RuntimeFiles                []string                                            `json:"runtime_files,omitempty"`
	ParameterFiles              []string                                            `json:"parameter_files,omitempty"`
	RuntimeSettingsParity       []enterprisearchive.RuntimeSettingParityEntry       `json:"runtime_settings_parity,omitempty"`
	RuntimeSettingsStatusCounts map[string]int                                      `json:"runtime_settings_status_counts,omitempty"`
	MigrationReadiness          *enterprisearchive.RuntimeSettingMigrationReadiness `json:"migration_readiness,omitempty"`
	TrueClientInspection        *trueclient.ArchiveInspection                       `json:"trueclient_archive_inspection,omitempty"`
	SourceScriptType            string                                              `json:"source_script_type,omitempty"`
	SourceProfile               string                                              `json:"source_profile,omitempty"`
	Web2MigrationReadiness      *web2MigrationReadinessReport                       `json:"web2_migration_readiness,omitempty"`
	TrueClient2CompatReport     *trueClient2CompatReport                            `json:"trueclient2_compat_report,omitempty"`
	SalesforceReview            *salesforce.Review                                  `json:"salesforce_review,omitempty"`
	AgentforceReview            *agentforce.Review                                  `json:"agentforce_review,omitempty"`
	MuleSoftReview              *mulesoft.Review                                    `json:"mulesoft_review,omitempty"`
	DetectedProtocol            *enterprisearchive.DetectedProtocol                 `json:"detected_protocol,omitempty"`
	FidelityReport              *enterprisearchive.ImportFidelityReport             `json:"fidelity_report,omitempty"`
	ReviewArtifact              *enterprisearchive.ReviewArtifact                   `json:"review_artifact,omitempty"`
	SourceReview                *enterprisearchive.SourceReview                     `json:"source_review,omitempty"`
	ImportMode                  string                                              `json:"import_mode,omitempty"`
	UnsupportedFunctionCount    int                                                 `json:"unsupported_function_count"`
}

type web2MigrationReadinessReport struct {
	ImportedSteps             int      `json:"imported_steps"`
	ConvertedSteps            int      `json:"converted_steps"`
	SkippedReviewSteps        int      `json:"skipped_review_steps"`
	SelectorConfidencePct     int      `json:"selector_confidence_pct"`
	ReplayStatus              string   `json:"replay_status"`
	RuntimeParityStatus       string   `json:"runtime_parity_status"`
	ConversionToHTTPReadiness string   `json:"conversion_to_http_readiness"`
	Findings                  []string `json:"findings,omitempty"`
	NextActions               []string `json:"next_actions,omitempty"`
}

type trueClient2CompatReport = importreview.TrueClient2CompatReport

type trueClientJavaScriptReviewPolicyReport = importreview.TrueClientJavaScriptReviewPolicyReport

type trueClientJavaScriptActionReview = importreview.TrueClientJavaScriptActionReview

type protocolCompatReport = importreview.ProtocolCompatReport

// handleImportEnterpriseArchive handles POST /api/v1/scripts/import/enterprise-script-archive.
// Accepts a multipart form upload of a ZIP file containing enterprise C/VUser source files.

// handleValidateEnterpriseArchiveImport handles POST /api/v1/scripts/import/enterprise-script-archive/validate.
// It parses and converts the ZIP payload without persisting any generated assets.

// handleImportJMeter handles POST /api/v1/scripts/import/jmeter.
// Accepts a multipart form upload or raw body containing a JMeter .jmx test plan.

// handleValidateJMeterImport handles POST /api/v1/scripts/import/jmeter/validate.
// It parses and converts the JMX plan without persisting any generated assets.

// handleImportDevWeb handles POST /api/v1/scripts/import/devweb.
// Accepts JSON, raw .js, raw .zip, or multipart uploads and returns a generated script preview.

// handleImportHAR handles POST /api/v1/scripts/import/har.
// Accepts JSON, raw HAR, or multipart uploads and returns a generated HTTP script preview.

// handleImportK6 handles POST /api/v1/scripts/import/k6.
// It statically converts the supported HTTP subset and preserves unsupported
// JavaScript/browser constructs as review diagnostics.

// handleExportK6 handles POST /api/v1/scripts/export/k6.

// handleImportSample handles POST /api/v1/scripts/import/sample.
// Accepts a JSON payload containing a supported sample format and returns a generated script preview.
func (s *Server) handleImportSample(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

	var req sample.ImportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	result, err := sample.Convert(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "sample conversion failed: "+err.Error())
		return
	}
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	draft := importDraft{
		Source:           req.Format,
		SourceScriptName: req.Name,
		ProjectID:        req.ProjectID,
		Script:           result.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.Augment(r)
	result.Warnings = augmented.Warnings

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                result,
		Script:                result.Script,
		Warnings:              result.Warnings,
		Stats:                 result.Stats,
		Correlations:          result.Correlations,
		Diagnostics:           result.Unsupported,
		UnsupportedElements:   result.Unsupported.Strings(),
		ReviewSections:        augmented.Sections,
		SourceReview:          result.SourceReview,
		Parameterization:      buildImportParameterization(result.Script, sampleCorrelationsToPreApplied(result.Correlations)),
		Preflight:             augmented.Preflight,
		ProtectedRequirements: protectedRequirements,
	})
}

type scriptExportRequest struct {
	OutputDir string `json:"output_dir,omitempty"`
	Filename  string `json:"filename,omitempty"`
}

// handleExportScript handles POST /api/v1/scripts/{id}/export
// Exports a script as a JSON DSL file to disk.
func (s *Server) handleExportScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}

	// Optional: specify output directory via request body
	var req scriptExportRequest
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}

	filename := req.Filename
	if strings.TrimSpace(filename) == "" {
		filename = disclosureSafeScriptName(stored.Name, stored.Script) + ".json"
	}

	// Confine the export to APEXLOAD_EXPORT_DIR (default ./exports). The
	// caller-supplied output directory and filename are untrusted: the filename
	// is always sanitized and the directory is rooted so "../" cannot escape.
	fullPath, err := resolveConfinedExportPath(exportBaseDir(), req.OutputDir, filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid export path")
		return
	}

	// Wrap in the standard script envelope. An export is the runnable artifact:
	// re-importing this file must reproduce a script that executes, so the
	// executable definition is written verbatim. Only the filename stays a
	// disclosure-safe label.
	envelope := map[string]any{
		"script": stored.Script,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal script: "+err.Error())
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create output directory")
		return
	}

	if err := writePrivateScriptExport(fullPath, data); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write script file")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"exported": true,
		"path":     fullPath,
		"filename": filepath.Base(fullPath),
		"size":     len(data),
	})
}

func writePrivateScriptExport(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	// OpenFile does not tighten an existing file's mode. Do so before writing,
	// keeping a prior permissive export from remaining world-readable.
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// handleDownloadScript handles GET /api/v1/scripts/{id}/download
// Returns the script JSON as a downloadable file.
func (s *Server) handleDownloadScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}

	// A download is an export over HTTP and carries the same contract: the file
	// must be re-importable and runnable, so it holds the executable definition.
	// The attachment filename remains a disclosure-safe label.
	envelope := map[string]any{
		"script": stored.Script,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal script: "+err.Error())
		return
	}

	filename := sanitizeFilename(disclosureSafeScriptName(stored.Name, stored.Script)) + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleUpdateScript handles PUT /api/v1/scripts/{id}
// Updates an existing script in-place.
func (s *Server) handleUpdateScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, supported, admitted := s.loadScriptAuthoringRequest(w, r, id)
	if !admitted {
		return
	}
	id = stored.ID
	expected, mode, admitted := s.admitScriptSavePrecondition(w, r, stored, supported)
	if !admitted {
		return
	}

	var wrapper struct {
		Script dsl.Script `json:"script"`
	}
	if !decodeJSONBody(w, r, &wrapper) {
		return
	}
	script := &wrapper.Script
	// Approval provenance is server-owned. Never allow a PUT payload to supply
	// or replace it; an unchanged, already-approved Java script may have its
	// verified durable evidence restored below.
	clearJavaVuserAuthoringApproval(script)
	if err := bindStoredScriptProject(script, stored.ProjectID, ""); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateScriptFolderProject(s.store, script); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateJavaVMParameterSafety(
		script,
		isCredentialPreservingImport(script) || isCredentialPreservingImport(stored.Script),
	); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if isCredentialPreservingImport(script) || isCredentialPreservingImport(stored.Script) {
		var err error
		script, err = preparePersistentLibraryScript(script, stored.Script)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		// Updates must pass the same disclosure-safe binding gate as creates. In
		// particular, a PUT must not become a back door for introducing a new raw
		// password, token, or executable [REDACTED] sentinel into durable storage.
		projected := maskScriptSecrets(script)
		if projected == nil {
			writeError(w, http.StatusBadRequest, "failed to project script for secret binding")
			return
		}
		if unresolved := discoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
			writeSecretBindingsRequired(w, unresolved)
			return
		}
		// Keep the caller's executable object. Non-executable import/review
		// metadata is scrubbed by sanitizePersistentImportReviewSurfaces inside
		// the durable write, so an update never has to trade runnability for it.
		if err := reconcileMaskedScriptSecrets(script, stored.Script); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if issues := validateStoredExecutionScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}
	if err := s.runner.restoreJavaVuserAuthoringApprovalForUpdate(script, stored.Script); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	details := map[string]any{"name": disclosureSafeScriptName(script.Name, script), "folder_id": script.FolderID, "save_mode": mode}
	var operationID string
	var err error
	if mode == "legacy_unconditioned" && s.store != nil {
		operationID, err = s.beginRequiredAuditOperation(r, "script.update", "script", stored.ID, stored.ProjectID, details)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "script compatibility audit unavailable; no save was attempted")
			return
		}
	}
	var updated *StoredScript
	var ok bool
	if expected != nil {
		updated, ok, err = s.runner.UpdateScriptAuthoringDurable(id, script, *expected)
	} else {
		updated, ok, err = s.runner.UpdateScriptDurableInProject(id, script, authorizedProjectOr(r, stored.ProjectID))
	}
	if operationID != "" {
		status, outcome := "failure", "unknown"
		if err == nil && ok {
			status, outcome = "success", "committed"
		} else if err == nil || errors.Is(err, storage.ErrScriptAuthoringConflict) || errors.Is(err, storage.ErrScriptAuthoringPreconditionRequired) || errors.Is(err, storage.ErrScriptAuthoringNotFound) || errors.Is(err, storage.ErrScriptAuthoringInvalidRevision) {
			outcome = "not_committed"
		}
		if auditErr := s.completeRequiredAuditOperation(r, operationID, "script.update", status, outcome, "script", stored.ID, stored.ProjectID, details); auditErr != nil {
			writeScriptAuthoringSaveOutcomeUnavailable(w, false)
			return
		}
		if outcome == "unknown" {
			writeScriptAuthoringSaveOutcomeUnavailable(w, true)
			return
		}
	}
	if err != nil {
		writeScriptAuthoringMutationError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if operationID == "" {
		s.recordAuditEvent(r, "script.update", "success", "script", updated.ID, updated.ProjectID, details)
	}
	w.Header().Set("X-APEX-Script-Save-Mode", mode)

	if !s.scriptLibraryAssignments(w, []*StoredScript{updated}) {
		return
	}
	writeJSON(w, http.StatusOK, viewStoredScript(updated))
}

// handleDeleteScript handles DELETE /api/v1/scripts/{id}
func (s *Server) handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	// Tombstone first: a delete with an unknown outcome then refuses retries of
	// the create that made this script, but can never let one recreate it.
	// Every definite refusal below withdraws the tombstone.
	restoreReceipts, tombstoneErr := s.tombstoneAuthoringReceipts("script", stored.ID)
	if tombstoneErr != nil {
		writeError(w, http.StatusServiceUnavailable, "script delete unavailable; no deletion was attempted")
		return
	}
	outcomeUnknown := false
	deleteCommitted := false
	defer func() {
		if !deleteCommitted && !outcomeUnknown {
			restoreReceipts()
		}
	}()
	var importSourceArtifacts []storage.ImportSourceArtifact
	if importSourceStore, ok := s.store.(storage.ImportSourceStore); ok {
		revisions, listErr := importSourceStore.ListImportSourceRevisions(id, stored.ProjectID)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to prepare retained import source cleanup")
			return
		}
		seen := make(map[string]struct{})
		for _, revision := range revisions {
			for _, artifactID := range []string{revision.RawArtifactID, revision.SanitizedArtifactID} {
				artifactID = strings.TrimSpace(artifactID)
				if artifactID == "" {
					continue
				}
				if _, exists := seen[artifactID]; exists {
					continue
				}
				artifact, getErr := importSourceStore.GetImportSourceArtifact(artifactID, stored.ProjectID)
				if getErr != nil {
					writeError(w, http.StatusInternalServerError, "failed to prepare retained import source cleanup")
					return
				}
				importSourceArtifacts = append(importSourceArtifacts, *artifact)
				seen[artifactID] = struct{}{}
			}
		}
	}
	deleted, err := s.runner.DeleteScriptDurableInProject(id, authorizedProjectOr(r, stored.ProjectID))
	if err != nil {
		if errors.Is(err, errScriptConcurrentChange) || storage.IsGitHubScriptSyncBusy(err) || errors.Is(err, storage.ErrScriptInUse) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// Only a failed commit leaves the outcome unknown; any other error is
		// a definite refusal whose tombstone the deferred restore withdraws.
		if errors.Is(err, storage.ErrScriptAuthoringOutcomeUnknown) {
			outcomeUnknown = true
			// The delete may have committed: keep its tombstone current.
			s.retombstoneAuthoringReceipts("script", stored.ID)
		}
		writeError(w, http.StatusInternalServerError, "failed to delete script: "+err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	deleteCommitted = true
	s.retombstoneAuthoringReceipts("script", stored.ID)
	s.cleanupUnreferencedBrunoArtifacts(importSourceArtifacts)
	s.recordAuditEvent(r, "script.delete", "success", "script", id, stored.ProjectID, map[string]any{"name": disclosureSafeScriptName(stored.Name, stored.Script), "folder_id": stored.FolderID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// sanitizeFilename makes a string safe for use as a filename.
