package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/agentforce"
	"github.com/jasonczech/apexload/pkg/convert/devweb"
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
	Data             []byte
	HARData          []byte
	SupplementalData [][]byte
	SupplementalName []string
	ProjectID        string
	Opts             enterprisearchive.ConvertOptions
	ReadinessPolicy  enterprisearchive.RuntimeSettingMigrationPolicy
	SalesforceNative bool
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

type trueClient2CompatReport struct {
	SourceScriptType        string                                  `json:"source_script_type,omitempty"`
	SourceProfile           string                                  `json:"source_profile,omitempty"`
	ImportMode              string                                  `json:"import_mode,omitempty"`
	PromotionStatus         string                                  `json:"promotion_status"`
	PromotionAllowed        bool                                    `json:"promotion_allowed"`
	ImportedSteps           int                                     `json:"imported_steps"`
	ConvertedActions        int                                     `json:"converted_actions"`
	SkippedReviewSteps      int                                     `json:"skipped_review_steps"`
	SupportedStepFamilies   int                                     `json:"supported_step_families"`
	ReviewOnlyStepFamilies  int                                     `json:"review_only_step_families"`
	UnsupportedStepFamilies int                                     `json:"unsupported_step_families"`
	SelectorConfidencePct   int                                     `json:"selector_confidence_pct"`
	SelectorCandidateCount  int                                     `json:"selector_candidate_count"`
	RuntimeParityStatus     string                                  `json:"runtime_parity_status"`
	ReplayStatus            string                                  `json:"replay_status"`
	JavaScriptReviewGate    string                                  `json:"javascript_review_gate"`
	JavaScriptReview        *trueClientJavaScriptReviewPolicyReport `json:"javascript_review,omitempty"`
	FrameWindowBlockers     int                                     `json:"frame_window_blockers"`
	HTTPConversionReadiness string                                  `json:"http_conversion_readiness"`
	FixtureContractStatus   map[string]string                       `json:"fixture_contract_status,omitempty"`
	Findings                []string                                `json:"findings,omitempty"`
	NextActions             []string                                `json:"next_actions,omitempty"`
}

type trueClientJavaScriptReviewPolicyReport struct {
	Status                 string                             `json:"status"`
	Policy                 string                             `json:"policy"`
	ApprovalRequired       bool                               `json:"approval_required"`
	ReplayEvidenceRequired bool                               `json:"replay_evidence_required"`
	RequiredActions        int                                `json:"required_actions"`
	ApprovedActions        int                                `json:"approved_actions"`
	PendingActions         int                                `json:"pending_actions"`
	RejectedActions        int                                `json:"rejected_actions"`
	SourceOnlyFragments    int                                `json:"source_only_fragments,omitempty"`
	ActionReviews          []trueClientJavaScriptActionReview `json:"action_reviews,omitempty"`
	Findings               []string                           `json:"findings,omitempty"`
	NextActions            []string                           `json:"next_actions,omitempty"`
}

type trueClientJavaScriptActionReview struct {
	Index           int    `json:"index"`
	Name            string `json:"name,omitempty"`
	Type            string `json:"type,omitempty"`
	Status          string `json:"status"`
	Gate            string `json:"gate,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Reviewer        string `json:"reviewer,omitempty"`
	ApprovedAt      string `json:"approved_at,omitempty"`
	Source          string `json:"source,omitempty"`
	SourceStepID    string `json:"source_step_id,omitempty"`
	SourceStepIndex string `json:"source_step_index,omitempty"`
	ScriptPreview   string `json:"script_preview,omitempty"`
}

type protocolCompatReport struct {
	SourceFormat         string         `json:"source_format"`
	CanonicalProtocol    string         `json:"canonical_protocol"`
	ImportMode           string         `json:"import_mode"`
	MappedSteps          int            `json:"mapped_steps"`
	ReviewGatedSteps     int            `json:"review_gated_steps"`
	UnsupportedFragments []string       `json:"unsupported_fragments,omitempty"`
	RuntimeParity        map[string]int `json:"runtime_parity,omitempty"`
	DataSources          int            `json:"data_sources"`
	CorrelationCoverage  string         `json:"correlation_coverage"`
	PromotionStatus      string         `json:"promotion_status"`
	NextActions          []string       `json:"next_actions,omitempty"`
}

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
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(req.ProjectID)
	}
	attachSourceReviewToScript(result.Script, req.Format, req.Name, result.SourceReview)

	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	preflight := buildReplayPreflight(result.Script, nil, issues, result.Warnings)
	attachImportReplayPreflight(result.Script, req.Format, preflight)

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                result,
		Script:                result.Script,
		Warnings:              result.Warnings,
		Stats:                 result.Stats,
		Correlations:          result.Correlations,
		UnsupportedElements:   result.Unsupported.Strings(),
		ReviewSections:        importDiagnosticReviewSections(result.Unsupported),
		SourceReview:          result.SourceReview,
		Parameterization:      buildImportParameterization(result.Script, sampleCorrelationsToPreApplied(result.Correlations)),
		Preflight:             preflight,
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
	stored, ok := s.runner.GetScript(id)
	if !ok || stored == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
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

	updated, ok, err := s.runner.UpdateScriptDurableInProject(id, script, authorizedProjectOr(r, stored.ProjectID))
	if err != nil {
		if errors.Is(err, errScriptConcurrentChange) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update script: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	s.recordAuditEvent(r, "script.update", "success", "script", updated.ID, updated.ProjectID, map[string]any{"name": disclosureSafeScriptName(updated.Name, updated.Script), "folder_id": updated.FolderID})

	writeJSON(w, http.StatusOK, maskStoredScript(updated))
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
	var importSourceArtifacts []storage.BrunoSourceArtifact
	if importSourceStore, ok := s.store.(storage.BrunoImportStore); ok {
		revisions, listErr := importSourceStore.ListBrunoSourceRevisions(id, stored.ProjectID)
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
				artifact, getErr := importSourceStore.GetBrunoSourceArtifact(artifactID, stored.ProjectID)
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
		if errors.Is(err, errScriptConcurrentChange) || storage.IsGitHubScriptSyncBusy(err) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete script: "+err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	s.cleanupUnreferencedBrunoArtifacts(importSourceArtifacts)
	s.recordAuditEvent(r, "script.delete", "success", "script", id, stored.ProjectID, map[string]any{"name": disclosureSafeScriptName(stored.Name, stored.Script), "folder_id": stored.FolderID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// sanitizeFilename makes a string safe for use as a filename.
