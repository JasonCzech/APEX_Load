package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type jmeterImportRequest struct {
	Data      []byte
	ProjectID string
	Opts      jmeter.ConvertOptions
}

type enterpriseArchiveImportRequest struct {
	Data             []byte
	HARData          []byte
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
	Data      []byte
	ProjectID string
	Opts      har.ConvertOptions
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
	DetectedProtocol            *enterprisearchive.DetectedProtocol                 `json:"detected_protocol,omitempty"`
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
func (s *Server) handleImportEnterpriseArchive(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readEnterpriseArchiveImportRequest(w, r)
	if !ok {
		return
	}

	// Run the conversion
	result, err := enterprisearchive.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSalesforceReviewToEnterpriseArchiveResult(result, importReq)

	writeEnterpriseArchiveImportResponse(w, http.StatusOK, result, importReq.ReadinessPolicy)
}

// handleValidateEnterpriseArchiveImport handles POST /api/v1/scripts/import/enterprise-script-archive/validate.
// It parses and converts the ZIP payload without persisting any generated assets.
func (s *Server) handleValidateEnterpriseArchiveImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readEnterpriseArchiveImportRequest(w, r)
	if !ok {
		return
	}

	result, err := enterprisearchive.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSalesforceReviewToEnterpriseArchiveResult(result, importReq)

	writeEnterpriseArchiveImportResponse(w, http.StatusOK, result, importReq.ReadinessPolicy)
}

func attachSalesforceReviewToEnterpriseArchiveResult(result *enterprisearchive.ConvertResult, importReq *enterpriseArchiveImportRequest) {
	if result == nil || result.Script == nil || importReq == nil {
		return
	}
	if !importReq.SalesforceNative && len(importReq.HARData) == 0 {
		return
	}
	review, err := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{
		BuildNative: importReq.SalesforceNative,
		HARData:     importReq.HARData,
	})
	if err != nil {
		result.Warnings = append(result.Warnings, "Salesforce review failed: "+err.Error())
		return
	}
	if review == nil || (!review.Detected && !importReq.SalesforceNative) {
		return
	}
	preserved := ensureScriptImportPreserved(result.Script, "enterprise_script_archive")
	preserved["salesforce_review"] = review
}

func ensureScriptImportPreserved(script *dsl.Script, source string) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: source}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	return script.Metadata.Import.Preserved
}

// handleImportJMeter handles POST /api/v1/scripts/import/jmeter.
// Accepts a multipart form upload or raw body containing a JMeter .jmx test plan.
func (s *Server) handleImportJMeter(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readJMeterImportRequest(w, r)
	if !ok {
		return
	}

	result, err := jmeter.ConvertJMX(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	for index := range result.Scripts {
		result.Scripts[index].ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScripts(result.Scripts, "jmeter", result.Summary.FileName, result.SourceReview)

	writeJMeterImportResponse(w, http.StatusOK, result)
}

// handleValidateJMeterImport handles POST /api/v1/scripts/import/jmeter/validate.
// It parses and converts the JMX plan without persisting any generated assets.
func (s *Server) handleValidateJMeterImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readJMeterImportRequest(w, r)
	if !ok {
		return
	}

	importReq.Opts.ScenarioDraft = r.FormValue("scenario_draft") == "true"
	result, err := jmeter.ConvertJMX(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation failed: "+err.Error())
		return
	}
	for index := range result.Scripts {
		result.Scripts[index].ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScripts(result.Scripts, "jmeter", result.Summary.FileName, result.SourceReview)

	writeJMeterImportResponse(w, http.StatusOK, result)
}

func (s *Server) handleJMeterSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": jmeter.GetSupportMatrix(),
	})
}

func (s *Server) handleK6SupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": k6.GetSupportMatrix(),
		"notes":          k6.GetSupportMatrixNotes(),
	})
}

func (s *Server) handleEnterpriseArchiveSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix":            enterprisearchive.GetRTSSupportMatrix(),
		"notes":                     enterprisearchive.GetRTSSupportMatrixNotes(),
		"tested_examples":           enterprisearchive.GetRTSSupportMatrixExamples(),
		"trueclient_script_types":   trueclient.ScriptTypeScopes(),
		"trueclient_step_matrix":    trueclient.StepScopes(),
		"web2_profile_capabilities": trueclient.Web2ProfileCapabilityScopes(),
		"web2_fixture_contract":     trueclient.Web2FixtureContracts(),
		"protocol_support_matrix":   protocolBundleSupportMatrix(),
	})
}

func (s *Server) handleAuthoringCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authoringCapabilityResponse())
}

// handleImportDevWeb handles POST /api/v1/scripts/import/devweb.
// Accepts JSON, raw .js, raw .zip, or multipart uploads and returns a generated script preview.
func (s *Server) handleImportDevWeb(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readDevWebImportRequest(w, r)
	if !ok {
		return
	}

	var (
		result *devweb.ConvertResult
		err    error
	)
	if importReq.IsZip {
		result, err = devweb.ConvertZip(importReq.Data, importReq.Opts)
	} else {
		result, err = devweb.Convert(devweb.ImportRequest{
			Name:      importReq.Name,
			ProjectID: importReq.ProjectID,
			Content:   string(importReq.Data),
			Options:   importReq.Opts,
		})
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "devweb", importReq.Name, result.SourceReview)
	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	protocolCompat := buildDevWebProtocolCompatReport(result)
	persistProtocolCompatReport(result.Script, "devweb", protocolCompat)

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		SourceReview:        result.SourceReview,
		ProtocolCompat:      protocolCompat,
	}))
}

// handleImportHAR handles POST /api/v1/scripts/import/har.
// Accepts JSON, raw HAR, or multipart uploads and returns a generated HTTP script preview.
func (s *Server) handleImportHAR(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readHARImportRequest(w, r)
	if !ok {
		return
	}

	result, err := har.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "har", importReq.Opts.ScriptName, result.SourceReview)
	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Correlations:        result.Correlations,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		SourceReview:        result.SourceReview,
	}))
}

// handleImportK6 handles POST /api/v1/scripts/import/k6.
// It statically converts the supported HTTP subset and preserves unsupported
// JavaScript/browser constructs as review diagnostics.
func (s *Server) handleImportK6(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readK6ImportRequest(w, r)
	if !ok {
		return
	}
	result, err := k6.Convert(k6.ImportRequest{
		Name:      importReq.Name,
		ProjectID: importReq.ProjectID,
		Content:   string(importReq.Data),
		Options:   importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "k6", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Assumptions:         result.Assumptions,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		SourceReview:        result.SourceReview,
		Summary: map[string]any{
			"import_mode":    "constrained_http_subset",
			"execution_mode": "apexload_http",
		},
	}))
}

// handleExportK6 handles POST /api/v1/scripts/export/k6.
func (s *Server) handleExportK6(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req k6.ExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	result, err := k6.Export(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "export failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func readK6ImportRequest(w http.ResponseWriter, r *http.Request) (*k6ImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, k6ImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req k6.ImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &k6ImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(k6ImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		return &k6ImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
			Opts: k6.ConvertOptions{
				BaseURL:    r.FormValue("base_url"),
				ScriptName: r.FormValue("script_name"),
			},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &k6ImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts: k6.ConvertOptions{
			BaseURL:    r.URL.Query().Get("base_url"),
			ScriptName: r.URL.Query().Get("script_name"),
		},
	}, true
}

func readHARImportRequest(w http.ResponseWriter, r *http.Request) (*harImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, harImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req struct {
			Name      string             `json:"name,omitempty"`
			ProjectID string             `json:"project_id,omitempty"`
			Content   string             `json:"content"`
			Options   har.ConvertOptions `json:"options,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		return &harImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(harImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		return &harImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Opts: har.ConvertOptions{
				ScriptName:          firstNonEmpty(r.FormValue("name"), strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))),
				BaseURL:             r.FormValue("base_url"),
				IncludeStaticAssets: parseOptionalBool(r.FormValue("include_static_assets")),
			},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &harImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Opts: har.ConvertOptions{
			ScriptName:          strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
			BaseURL:             r.URL.Query().Get("base_url"),
			IncludeStaticAssets: parseOptionalBool(r.URL.Query().Get("include_static_assets")),
		},
	}, true
}

func readDevWebImportRequest(w http.ResponseWriter, r *http.Request) (*devWebImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req devweb.ImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &devWebImportRequest{
			Data:      []byte(req.Content),
			ProjectID: req.ProjectID,
			Name:      req.Name,
			Opts:      req.Options,
		}, true
	}

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(webAPIImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		opts := devweb.ConvertOptions{
			BaseURL: r.FormValue("base_url"),
		}
		return &devWebImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
			IsZip:     strings.EqualFold(filepath.Ext(header.Filename), ".zip"),
			Opts:      opts,
		}, true
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &devWebImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		IsZip:     strings.EqualFold(filepath.Ext(filename), ".zip") || strings.HasPrefix(contentType, "application/zip"),
		Opts: devweb.ConvertOptions{
			BaseURL: r.URL.Query().Get("base_url"),
		},
	}, true
}

func readJMeterImportRequest(w http.ResponseWriter, r *http.Request) (*jmeterImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, jmeterImportMaxBodyBytes)

	contentType := r.Header.Get("Content-Type")
	var data []byte
	fileName := r.URL.Query().Get("filename")
	sidecarFiles := map[string][]byte{}

	if strings.HasPrefix(contentType, "application/xml") || strings.HasPrefix(contentType, "text/xml") || strings.HasPrefix(contentType, "application/octet-stream") || strings.HasPrefix(contentType, "application/zip") || strings.HasPrefix(contentType, "application/x-zip-compressed") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		data = body
	} else {
		if err := r.ParseMultipartForm(jmeterImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form (max 50MB): "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' field in multipart form: "+err.Error())
			return nil, false
		}
		defer file.Close()
		if header != nil {
			fileName = header.Filename
		}
		body, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		data = body
		sidecarFiles = readJMeterSidecarFiles(r, "file", fileName)
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}
	if isJMeterZipUpload(fileName, contentType) {
		jmxData, jmxFileName, zipSidecars, err := readJMeterZipImport(data)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read JMeter ZIP: "+err.Error())
			return nil, false
		}
		data = jmxData
		fileName = jmxFileName
		sidecarFiles = mergeJMeterSidecarFiles(sidecarFiles, zipSidecars)
	}

	return &jmeterImportRequest{
		Data:      data,
		ProjectID: firstNonEmpty(r.FormValue("project_id"), r.URL.Query().Get("project_id")),
		Opts: jmeter.ConvertOptions{
			ScriptName:        r.FormValue("name"),
			BaseURL:           r.FormValue("base_url"),
			IncludeAssertions: r.FormValue("include_assertions") == "true",
			ScenarioDraft:     r.FormValue("scenario_draft") == "true",
			FileName:          fileName,
			SidecarFiles:      sidecarFiles,
		},
	}, true
}

func isJMeterZipUpload(fileName string, contentType string) bool {
	return strings.EqualFold(filepath.Ext(fileName), ".zip") ||
		strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-zip-compressed")
}

func readJMeterZipImport(data []byte) ([]byte, string, map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", nil, err
	}
	jmxFiles := map[string][]byte{}
	sidecars := map[string][]byte{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			continue
		}
		name := filepath.ToSlash(entry.Name)
		switch strings.ToLower(filepath.Ext(name)) {
		case ".jmx":
			jmxFiles[name] = content
		case ".csv":
			sidecars[name] = content
			sidecars[filepath.Base(name)] = content
		}
	}
	if len(jmxFiles) == 0 {
		return nil, "", nil, fmt.Errorf("no .jmx file found")
	}
	names := make([]string, 0, len(jmxFiles))
	for name := range jmxFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	selected := names[0]
	return jmxFiles[selected], selected, sidecars, nil
}

func mergeJMeterSidecarFiles(first map[string][]byte, second map[string][]byte) map[string][]byte {
	if len(first) == 0 {
		if len(second) == 0 {
			return nil
		}
		out := make(map[string][]byte, len(second))
		for key, value := range second {
			out[key] = value
		}
		return out
	}
	out := make(map[string][]byte, len(first)+len(second))
	for key, value := range first {
		out[key] = value
	}
	for key, value := range second {
		out[key] = value
	}
	return out
}

func readJMeterSidecarFiles(r *http.Request, primaryField string, primaryFileName string) map[string][]byte {
	if r == nil || r.MultipartForm == nil || len(r.MultipartForm.File) == 0 {
		return nil
	}
	files := map[string][]byte{}
	for field, headers := range r.MultipartForm.File {
		if field == primaryField {
			continue
		}
		for _, header := range headers {
			if header == nil || strings.TrimSpace(header.Filename) == "" {
				continue
			}
			file, err := header.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil || len(data) == 0 {
				continue
			}
			name := strings.TrimSpace(header.Filename)
			if name == primaryFileName {
				continue
			}
			files[name] = data
			files[filepath.Base(name)] = data
		}
	}
	if len(files) == 0 {
		return nil
	}
	return files
}

func readEnterpriseArchiveImportRequest(w http.ResponseWriter, r *http.Request) (*enterpriseArchiveImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, enterpriseArchiveImportMaxBodyBytes)

	contentType := r.Header.Get("Content-Type")
	var data []byte
	var harData []byte

	if strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-zip-compressed") ||
		strings.HasPrefix(contentType, "application/octet-stream") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		data = body
	} else {
		if err := r.ParseMultipartForm(enterpriseArchiveImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form (max 100MB): "+err.Error())
			return nil, false
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' field in multipart form: "+err.Error())
			return nil, false
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		data = body
		if harFile, _, err := r.FormFile("har_file"); err == nil {
			defer harFile.Close()
			harBody, readErr := io.ReadAll(harFile)
			if readErr != nil {
				writeError(w, http.StatusBadRequest, "failed to read uploaded HAR file: "+readErr.Error())
				return nil, false
			}
			harData = harBody
		}
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}

	return &enterpriseArchiveImportRequest{
		Data:             data,
		HARData:          harData,
		ProjectID:        firstNonEmpty(r.FormValue("project_id"), r.URL.Query().Get("project_id")),
		SalesforceNative: parseBoolDefault(firstNonEmpty(r.FormValue("salesforce_native"), r.URL.Query().Get("salesforce_native")), false),
		Opts: enterprisearchive.ConvertOptions{
			ScriptName:           r.FormValue("name"),
			BaseURL:              r.FormValue("base_url"),
			IncludeAssertions:    r.FormValue("include_assertions") == "true",
			PreserveTransactions: parseBoolDefault(r.FormValue("preserve_transactions"), true),
		},
		ReadinessPolicy: enterprisearchive.ParseRuntimeSettingMigrationPolicy(r.FormValue("migration_block_statuses")),
	}, true
}

func parseBoolDefault(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func parseOptionalBool(value string) *bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := parseBoolDefault(value, false)
	return &parsed
}

func writeEnterpriseArchiveImportResponse(w http.ResponseWriter, status int, result *enterprisearchive.ConvertResult, readinessPolicy enterprisearchive.RuntimeSettingMigrationPolicy) {
	if result == nil {
		writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{}))
		return
	}
	unsupportedElements := uniqueSortedStrings(result.Stats.UnsupportedFuncs)
	warnings := enterpriseArchiveImportWarnings(result)
	summary := buildEnterpriseArchiveImportSummary(result, unsupportedElements, readinessPolicy)
	persistEnterpriseArchiveImportSummary(result, summary)
	reviewSections := buildEnterpriseArchiveImportReviewSections(warnings, result, unsupportedElements, summary)
	repairSuggestions := buildEnterpriseArchiveImportRepairSuggestions(result, unsupportedElements, summary)
	protocolCompat := buildEnterpriseArchiveProtocolCompatReport(result, unsupportedElements, summary)
	persistProtocolCompatReport(result.Script, "enterprise_script_archive", protocolCompat)

	writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            warnings,
		Stats:               result.Stats,
		UnsupportedElements: unsupportedElements,
		ReviewSections:      reviewSections,
		RepairSuggestions:   repairSuggestions,
		Summary:             summary,
		DetectedProtocol:    result.DetectedProtocol,
		ReviewArtifact:      result.ReviewArtifact,
		SourceReview:        summary.SourceReview,
		TrueClient2Compat:   summary.TrueClient2CompatReport,
		ProtocolCompat:      protocolCompat,
	}))
}

func enterpriseArchiveImportWarnings(result *enterprisearchive.ConvertResult) []string {
	if result == nil {
		return nil
	}
	warnings := append([]string(nil), result.Warnings...)
	return uniqueSortedStrings(warnings)
}

func buildEnterpriseArchiveImportRepairSuggestions(result *enterprisearchive.ConvertResult, unsupportedElements []string, summary enterpriseArchiveImportSummary) []scriptImportRepairSuggestion {
	suggestions := make([]scriptImportRepairSuggestion, 0)
	if result == nil {
		return suggestions
	}

	for _, element := range unsupportedElements {
		normalized := strings.ToLower(element)
		description := "Review the preserved source construct and replace it with equivalent APEX Load DSL behavior before relying on this script."
		action := "Open the generated script in the visual editor, inspect preserved metadata, and either remove the unsupported construct or model it with supported actions."
		if strings.Contains(normalized, "evaluate javascript") {
			description = "This source JavaScript statement could not be decomposed into a supported browser action."
			action = "Replace the JavaScript with browser_click, browser_fill, browser_select, browser_wait, browser_swipe, or a supported assertion, then validate with browser replay."
			if enterpriseArchiveWarningsContain(result, "window.scrollto(") {
				description = "This source JavaScript scroll statement could not be decomposed safely into a supported browser action."
				action = "Replace the JavaScript with browser_swipe and any required browser_wait actions, then validate the resulting scroll behavior with browser replay."
			}
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "unsupported:" + stableReviewKey(element),
			Category:    "unsupported",
			Severity:    "blocker",
			Title:       "Replace unsupported " + element,
			Description: description,
			Action:      action,
		})
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		suggestions = append(suggestions, browserImportRepairSuggestions(result.Script)...)
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		suggestions = append(suggestions, trueClientWeb2RepairSuggestions(summary)...)
	}

	if result.DetectedProtocol != nil && result.DetectedProtocol.ImportMode != enterprisearchive.ImportModeExecutable {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "protocol:review-artifact",
			Category:    "protocol",
			Severity:    "blocker",
			Title:       "Resolve protocol import gate",
			Description: fmt.Sprintf("%s was classified as %s and is not ready to save as an executable APEX Load script.", firstNonEmpty(result.DetectedProtocol.DisplayName, result.DetectedProtocol.CanonicalSourceProtocol), result.DetectedProtocol.ImportMode),
			Action:      "Save the review artifact, collect the required protocol evidence, and rerun import after the protocol is confirmed or a runner bridge is available.",
		})
	}

	if len(summary.RuntimeFiles) > 0 || summary.MappedRuntimeFile != "" {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:review-preserved-settings",
			Category:    "runtime",
			Severity:    "info",
			Title:       "Review preserved runtime settings",
			Description: "The source archive included source runtime metadata that may not all execute natively in APEX Load yet.",
			Action:      "Compare the preserved runtime file and project settings against the generated APEX Load config before promoting the script.",
		})
	}

	if summary.MigrationReadiness != nil && summary.MigrationReadiness.Blocked {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:migration-readiness",
			Category:    "runtime",
			Severity:    "blocker",
			Title:       "Resolve blocking runtime settings before promotion",
			Description: "The selected migration block policy matched imported runtime settings that are not fully executable in APEX Load yet.",
			Action:      "Review the blocking runtime settings, adjust the generated APEX Load config where possible, or relax the migration block policy before saving the script.",
		})
	}

	if summary.SalesforceReview != nil && len(summary.SalesforceReview.Blockers) > 0 {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "salesforce:native-preview-blockers",
			Category:    "salesforce",
			Severity:    "blocker",
			Title:       "Resolve Salesforce native preview blockers",
			Description: "The exact replay script was imported, but the Salesforce native preview needs additional evidence or correlation review before promotion.",
			Action:      "Attach a HAR captured from the same source recording, review Salesforce transaction grouping and correlations, then rerun the native preview.",
		})
	}

	return suggestions
}

func trueClientWeb2RepairSuggestions(summary enterpriseArchiveImportSummary) []scriptImportRepairSuggestion {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	suggestions := []scriptImportRepairSuggestion{
		{
			ID:          "web2:replay-evidence",
			Category:    "replay",
			Severity:    "warning",
			Title:       "Attach browser replay evidence",
			Description: "Web 2.0 browser-profile archives are review-only until selector, wait, runtime, and screenshot evidence is captured.",
			Action:      "Run browser replay from the visual editor and attach the replay report before approving the imported draft.",
		},
		{
			ID:          "web2:object-identification",
			Category:    "selector",
			Severity:    "warning",
			Title:       "Review Web 2.0 object identification",
			Description: fmt.Sprintf("%d object repository file(s), %d descriptor(s), and %d replay diagnostic(s) were preserved for selector repair.", len(inspection.ObjectRepositories), len(inspection.ObjectDescriptors), trueClientReplayDiagnosticCount(inspection)),
			Action:      "Open the object-identification review section, compare ranked selector candidates with replay evidence, and stabilize brittle selectors before promotion.",
		},
		{
			ID:          "web2:http-conversion",
			Category:    "conversion",
			Severity:    "info",
			Title:       "Gate browser-to-HTTP conversion",
			Description: "Web 2.0 browser drafts should not be promoted to high-density HTTP load until network capture and correlation findings are reviewed.",
			Action:      "Capture replay traffic, review conversion blockers and correlation candidates, then generate an HTTP draft only for eligible flows.",
		},
	}
	if len(inspection.ParameterFiles) > 0 {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "web2:parameter-fit",
			Category:    "data",
			Severity:    "info",
			Title:       "Verify Web 2.0 parameter data fit",
			Description: fmt.Sprintf("%d parameter file(s) were discovered for the imported Web 2.0 flow.", len(inspection.ParameterFiles)),
			Action:      "Open parameter data in the script editor, confirm row strategy and exhaustion behavior, and replay with representative data.",
		})
	}
	if len(inspection.UnsupportedFragments) > 0 {
		severity := "warning"
		for _, fragment := range inspection.UnsupportedFragments {
			if fragment.Status == trueclient.ScopeOutOfScope {
				severity = "blocker"
				break
			}
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "web2:source-fragments",
			Category:    "unsupported",
			Severity:    severity,
			Title:       "Resolve Web 2.0 source fragments",
			Description: fmt.Sprintf("%d review-only or unsupported source fragment(s) were preserved from the source archive.", len(inspection.UnsupportedFragments)),
			Action:      "Use the Web 2.0 source-fragment review section to replace unsupported JavaScript/API behavior with supported browser actions or documented manual steps.",
		})
	}
	return suggestions
}

func enterpriseArchiveWarningsContain(result *enterprisearchive.ConvertResult, needle string) bool {
	if result == nil {
		return false
	}
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), needle) {
			return true
		}
	}
	return false
}

func trueClientWeb2ReviewSections(summary enterpriseArchiveImportSummary) []scriptImportReviewSection {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	sourceType := firstNonEmpty(summary.SourceScriptType, inspection.ScriptType)
	sourceProfile := summary.SourceProfile
	if sourceProfile == "" && inspection.ScriptTypeScope != nil {
		sourceProfile = inspection.ScriptTypeScope.APEXLoadShape
	}
	items := []string{
		fmt.Sprintf("Source script type: %s", firstNonEmpty(sourceType, "browser-profile archive")),
		fmt.Sprintf("Source profile: %s", firstNonEmpty(sourceProfile, "browser profile")),
		fmt.Sprintf("Executable steps inspected: %d", inspection.StepInventory.Total),
		fmt.Sprintf("Supported step families: %d", len(inspection.StepInventory.Supported)),
	}
	if len(inspection.StepInventory.ReviewOnly) > 0 {
		items = append(items, fmt.Sprintf("Review-only step families: %d", len(inspection.StepInventory.ReviewOnly)))
	}
	if len(inspection.StepInventory.Unsupported) > 0 {
		items = append(items, fmt.Sprintf("Unsupported step families: %d", len(inspection.StepInventory.Unsupported)))
	}
	sections := []scriptImportReviewSection{{
		Key:      "web2_profile_summary",
		Title:    "Web 2.0 Browser Profile Summary",
		Severity: "warning",
		Summary:  "This Web 2.0 browser-profile archive was converted into a review-only browser draft.",
		Items:    items,
	}}

	if len(inspection.UnsupportedFragments) > 0 {
		fragmentItems := make([]string, 0, len(inspection.UnsupportedFragments))
		severity := "warning"
		for _, fragment := range inspection.UnsupportedFragments {
			if fragment.Status == trueclient.ScopeOutOfScope {
				severity = "blocker"
			}
			label := fragment.Action
			if fragment.Index != "" {
				label += " #" + fragment.Index
			}
			fragmentItems = append(fragmentItems, fmt.Sprintf("%s: %s", label, fragment.Reason))
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_unsupported_source_fragments",
			Title:    "Web 2.0 Source Fragments",
			Severity: severity,
			Summary:  "Review-only or unsupported TruClient fragments were preserved for repair before approval.",
			Items:    fragmentItems,
		})
	}

	if summary.TrueClient2CompatReport != nil && summary.TrueClient2CompatReport.JavaScriptReview != nil && summary.TrueClient2CompatReport.JavaScriptReview.ApprovalRequired {
		jsReview := summary.TrueClient2CompatReport.JavaScriptReview
		severity := "warning"
		if jsReview.Status == "rejected" {
			severity = "blocker"
		} else if jsReview.Status == "approved" {
			severity = "info"
		}
		jsItems := []string{
			"Policy: " + jsReview.Policy,
			fmt.Sprintf("Actions requiring approval: %d", jsReview.RequiredActions),
			fmt.Sprintf("Approved: %d; pending: %d; rejected: %d", jsReview.ApprovedActions, jsReview.PendingActions, jsReview.RejectedActions),
		}
		for _, review := range jsReview.ActionReviews {
			label := firstNonEmpty(review.Name, fmt.Sprintf("action_%d", review.Index))
			jsItems = append(jsItems, fmt.Sprintf("%s: %s (%s)", label, review.Status, firstNonEmpty(review.Reason, review.Gate, "JavaScript review required")))
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_javascript_review",
			Title:    "JavaScript Review Gate",
			Severity: severity,
			Summary:  "Review-gated JavaScript requires explicit approval metadata before production promotion.",
			Items:    jsItems,
		})
	}

	objectItems := make([]string, 0, len(inspection.ObjectRepositories)+1)
	for _, file := range inspection.ObjectRepositories {
		objectItems = append(objectItems, "Object repository: "+file.Path)
	}
	if len(inspection.ObjectDescriptors) > 0 {
		objectItems = append(objectItems, fmt.Sprintf("Object descriptors parsed: %d", len(inspection.ObjectDescriptors)))
	}
	if candidateCount := trueClientSelectorCandidateCount(inspection); candidateCount > 0 {
		objectItems = append(objectItems, fmt.Sprintf("Selector candidates extracted: %d", candidateCount))
	}
	if diagnosticCount := trueClientReplayDiagnosticCount(inspection); diagnosticCount > 0 {
		objectItems = append(objectItems, fmt.Sprintf("Replay diagnostics prepared: %d", diagnosticCount))
	}
	objectItems = append(objectItems, "Imported selectors are ranked from step arguments and object repositories; replay remains required before promotion.")
	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_object_identification",
		Title:    "Object Identification",
		Severity: "warning",
		Summary:  fmt.Sprintf("%d object repository file(s) were preserved for selector review.", len(inspection.ObjectRepositories)),
		Items:    objectItems,
	})

	if len(inspection.ParameterFiles) > 0 {
		parameterItems := make([]string, 0, len(inspection.ParameterFiles))
		for _, file := range inspection.ParameterFiles {
			parameterItems = append(parameterItems, "Parameter data: "+file.Path)
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_parameter_mapping",
			Title:    "Parameter Mapping",
			Severity: "info",
			Summary:  "Parameter files were discovered and preserved for data-source fit review.",
			Items:    parameterItems,
		})
	}

	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_replay_readiness",
		Title:    "Replay Readiness",
		Severity: "warning",
		Summary:  "Browser replay evidence is required before this review-only Web 2.0 draft is approved.",
		Items: []string{
			"Run browser replay against the target application.",
			"Review screenshots, selector failures, wait behavior, and runtime settings parity.",
			"Do not promote to a reusable production asset until replay evidence is attached.",
		},
	})
	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_http_conversion_eligibility",
		Title:    "HTTP Conversion Eligibility",
		Severity: "info",
		Summary:  "Browser-to-HTTP conversion remains a governed follow-up flow for Web 2.0 imports.",
		Items: []string{
			"Capture network requests during replay before generating an HTTP draft.",
			"Review correlation candidates and conversion blockers before using HTTP for density load.",
		},
	})
	return sections
}

func isWeb2TrueClientInspection(inspection *trueclient.ArchiveInspection) bool {
	if inspection == nil || inspection.ScriptTypeScope == nil {
		return false
	}
	return inspection.ScriptTypeScope.CanonicalName == "truclient2_web" || inspection.ScriptTypeScope.CanonicalName == "truclient_web"
}

func trueClientSelectorCandidateCount(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	count := 0
	for _, step := range inspection.Steps {
		count += len(step.SelectorCandidates)
	}
	return count
}

func trueClientReplayDiagnosticCount(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	count := 0
	for _, step := range inspection.Steps {
		count += len(step.ReplayDiagnostics)
	}
	return count
}

func buildWeb2MigrationReadinessReport(result *enterprisearchive.ConvertResult, summary enterpriseArchiveImportSummary) *web2MigrationReadinessReport {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	converted := 0
	skipped := 0
	if result != nil {
		converted = result.Stats.ConvertedActions
		skipped = result.Stats.Skipped
	}
	reviewSteps := skipped + len(inspection.UnsupportedFragments)
	for _, count := range inspection.StepInventory.ReviewOnly {
		reviewSteps += count
	}
	for _, count := range inspection.StepInventory.Unsupported {
		reviewSteps += count
	}
	runtimeStatus := "ready"
	if summary.MigrationReadiness != nil {
		if summary.MigrationReadiness.Blocked {
			runtimeStatus = "blocked"
		} else if summary.MigrationReadiness.ScorePct < 100 {
			runtimeStatus = "needs_review"
		}
	} else if len(summary.RuntimeSettingsParity) > 0 {
		runtimeStatus = "needs_review"
	}

	report := &web2MigrationReadinessReport{
		ImportedSteps:             inspection.StepInventory.Total,
		ConvertedSteps:            converted,
		SkippedReviewSteps:        reviewSteps,
		SelectorConfidencePct:     averageTrueClientSelectorConfidence(inspection),
		ReplayStatus:              "required",
		RuntimeParityStatus:       runtimeStatus,
		ConversionToHTTPReadiness: "needs_replay_network",
		Findings: []string{
			fmt.Sprintf("%d imported Web 2.0 browser-profile step(s), %d converted browser action(s), %d skipped or review step(s).", inspection.StepInventory.Total, converted, reviewSteps),
			"Browser replay evidence is required before approval.",
			"Browser-to-HTTP conversion requires replay network capture and generated HTTP validation.",
		},
		NextActions: []string{
			"Run browser replay and review screenshots, DOM, console, network, and Core Web Vitals evidence.",
			"Open object descriptors and repair low-confidence selectors before promotion.",
			"Use /api/v1/scripts/browser-to-http/preview after replay network evidence is available.",
		},
	}
	if runtimeStatus == "blocked" {
		report.Findings = append(report.Findings, "Runtime parity blockers must be resolved or the import policy relaxed before saving.")
	}
	if report.SelectorConfidencePct == 0 {
		report.Findings = append(report.Findings, "No selector confidence evidence was available in the imported object descriptors.")
	} else if report.SelectorConfidencePct < 70 {
		report.Findings = append(report.Findings, "Average selector confidence is below the recommended migration threshold.")
	}
	return report
}

func buildTrueClient2CompatReport(result *enterprisearchive.ConvertResult, summary enterpriseArchiveImportSummary) *trueClient2CompatReport {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	converted := 0
	skipped := 0
	importMode := summary.ImportMode
	if result != nil {
		converted = result.Stats.ConvertedActions
		skipped = result.Stats.Skipped
		if result.DetectedProtocol != nil && importMode == "" {
			importMode = string(result.DetectedProtocol.ImportMode)
		}
	}
	reviewSteps := skipped + len(inspection.UnsupportedFragments)
	for _, count := range inspection.StepInventory.ReviewOnly {
		reviewSteps += count
	}
	for _, count := range inspection.StepInventory.Unsupported {
		reviewSteps += count
	}
	frameWindowBlockers := trueClientFrameWindowBlockerCount(inspection)
	replayStatus := trueClientReplayStatus(result)
	runtimeStatus := trueClientRuntimeParityStatus(summary)
	jsReview := buildTrueClientJavaScriptReviewPolicy(result, inspection, replayStatus)
	jsGate := "clear"
	if jsReview != nil {
		jsGate = jsReview.Status
	}
	httpReadiness := "needs_replay_network"
	if replayStatus == "passed" && (jsGate == "clear" || jsGate == "approved") {
		httpReadiness = "ready_for_preview"
	}
	promotionAllowed := importMode == string(enterprisearchive.ImportModeExecutable) &&
		replayStatus == "passed" &&
		(jsGate == "clear" || jsGate == "approved") &&
		frameWindowBlockers == 0 &&
		runtimeStatus != "blocked" &&
		len(inspection.StepInventory.Unsupported) == 0
	promotionStatus := "blocked_review_only"
	if promotionAllowed {
		promotionStatus = "promotion_ready"
	} else if replayStatus == "passed" {
		promotionStatus = "needs_final_review"
	} else if frameWindowBlockers > 0 || jsGate != "clear" || runtimeStatus == "blocked" || len(inspection.StepInventory.Unsupported) > 0 {
		promotionStatus = "blocked"
	}

	report := &trueClient2CompatReport{
		SourceScriptType:        firstNonEmpty(summary.SourceScriptType, inspection.ScriptType),
		SourceProfile:           summary.SourceProfile,
		ImportMode:              importMode,
		PromotionStatus:         promotionStatus,
		PromotionAllowed:        promotionAllowed,
		ImportedSteps:           inspection.StepInventory.Total,
		ConvertedActions:        converted,
		SkippedReviewSteps:      reviewSteps,
		SupportedStepFamilies:   len(inspection.StepInventory.Supported),
		ReviewOnlyStepFamilies:  len(inspection.StepInventory.ReviewOnly),
		UnsupportedStepFamilies: len(inspection.StepInventory.Unsupported),
		SelectorConfidencePct:   averageTrueClientSelectorConfidence(inspection),
		SelectorCandidateCount:  trueClientSelectorCandidateCount(inspection),
		RuntimeParityStatus:     runtimeStatus,
		ReplayStatus:            replayStatus,
		JavaScriptReviewGate:    jsGate,
		JavaScriptReview:        jsReview,
		FrameWindowBlockers:     frameWindowBlockers,
		HTTPConversionReadiness: httpReadiness,
		FixtureContractStatus:   trueClientFixtureContractStatus(),
		Findings: []string{
			fmt.Sprintf("%d imported step(s) produced %d browser action(s).", inspection.StepInventory.Total, converted),
		},
		NextActions: []string{
			"Run browser replay and attach screenshot, DOM, console, and network evidence.",
			"Review selector candidates and repair low-confidence locators before approval.",
		},
	}
	if jsGate != "clear" {
		switch jsGate {
		case "approved":
			report.Findings = append(report.Findings, "Review-gated JavaScript has approval metadata; replay evidence is still required before promotion.")
		case "rejected":
			report.Findings = append(report.Findings, "At least one review-gated JavaScript action was rejected and must be replaced before promotion.")
		default:
			report.Findings = append(report.Findings, "Review-gated JavaScript is present and must be approved or replaced before promotion.")
			report.NextActions = append(report.NextActions, "Approve browser_evaluate_js only after code review and replay evidence, or replace it with native browser actions.")
		}
	}
	if frameWindowBlockers > 0 {
		report.Findings = append(report.Findings, fmt.Sprintf("%d iframe/window context blocker(s) require frame or popup replay evidence.", frameWindowBlockers))
		report.NextActions = append(report.NextActions, "Replay frame and popup actions and verify active page/frame targeting.")
	}
	if runtimeStatus == "blocked" {
		report.Findings = append(report.Findings, "Runtime parity policy reports blocking settings.")
	}
	if report.SelectorConfidencePct == 0 {
		report.Findings = append(report.Findings, "No selector confidence evidence was available.")
	} else if report.SelectorConfidencePct < 70 {
		report.Findings = append(report.Findings, "Average selector confidence is below the recommended migration threshold.")
	}
	if !promotionAllowed {
		report.NextActions = append(report.NextActions, "Keep this draft review-only until blockers are resolved and replay evidence passes.")
	}
	return report
}

func trueClientRuntimeParityStatus(summary enterpriseArchiveImportSummary) string {
	if summary.MigrationReadiness != nil {
		if summary.MigrationReadiness.Blocked {
			return "blocked"
		}
		if summary.MigrationReadiness.ScorePct < 100 {
			return "needs_review"
		}
		return "ready"
	}
	if len(summary.RuntimeSettingsParity) > 0 {
		return "needs_review"
	}
	return "not_imported"
}

func trueClientFrameWindowBlockerCount(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	count := 0
	for _, step := range inspection.Steps {
		for _, diagnostic := range step.ReplayDiagnostics {
			if diagnostic.Code == "frame_mismatch_risk" || diagnostic.Code == "window_context_risk" {
				count++
			}
		}
	}
	return count
}

func buildTrueClientJavaScriptReviewPolicy(result *enterprisearchive.ConvertResult, inspection *trueclient.ArchiveInspection, replayStatus string) *trueClientJavaScriptReviewPolicyReport {
	report := &trueClientJavaScriptReviewPolicyReport{
		Status:                 "clear",
		Policy:                 "explicit_approval_required",
		ReplayEvidenceRequired: true,
		NextActions:            []string{"No review-gated JavaScript actions were imported."},
	}
	if inspection != nil {
		report.SourceOnlyFragments = inspection.StepInventory.ReviewOnly["evaluate_javascript"]
	}
	if result != nil && result.Script != nil {
		actions := append(append([]dsl.Action{}, result.Script.Init...), append(result.Script.Actions, result.Script.Teardown...)...)
		for index, action := range actions {
			if !trueClientActionRequiresJavaScriptReview(action) {
				continue
			}
			review := trueClientJavaScriptActionReviewFor(index+1, action)
			report.ActionReviews = append(report.ActionReviews, review)
			report.RequiredActions++
			switch review.Status {
			case "approved":
				report.ApprovedActions++
			case "rejected":
				report.RejectedActions++
			default:
				report.PendingActions++
			}
		}
	}
	if report.RequiredActions == 0 && report.SourceOnlyFragments == 0 {
		report.ApprovalRequired = false
		report.ReplayEvidenceRequired = false
		report.Findings = []string{"No browser_evaluate_js or source-only Evaluate JavaScript steps require approval."}
		return report
	}
	report.ApprovalRequired = true
	report.NextActions = []string{
		"Review each browser_evaluate_js action against source JavaScript code.",
		"Attach reviewer, approval timestamp, and replay evidence before production promotion.",
		"Prefer replacing JavaScript with native browser actions when possible.",
	}
	switch {
	case report.RejectedActions > 0:
		report.Status = "rejected"
		report.Findings = []string{fmt.Sprintf("%d review-gated JavaScript action(s) were rejected.", report.RejectedActions)}
	case report.PendingActions > 0 || report.SourceOnlyFragments > 0:
		report.Status = "required"
		report.Findings = []string{fmt.Sprintf("%d JavaScript action(s) require explicit approval; %d are pending.", report.RequiredActions, report.PendingActions)}
	case replayStatus != "passed":
		report.Status = "approved_pending_replay"
		report.Findings = []string{"All imported JavaScript actions carry approval metadata, but replay evidence has not passed."}
	default:
		report.Status = "approved"
		report.Findings = []string{"All imported JavaScript actions carry approval metadata and replay evidence has passed."}
	}
	if report.SourceOnlyFragments > 0 {
		report.Findings = append(report.Findings, fmt.Sprintf("%d source Evaluate JavaScript fragment(s) remain review-only in the archive inspection.", report.SourceOnlyFragments))
	}
	return report
}

func trueClientActionRequiresJavaScriptReview(action dsl.Action) bool {
	if action.Type == "browser_evaluate_js" {
		return true
	}
	if strings.EqualFold(stringMapAnyValue(action.Metadata, "review_gate"), "javascript") {
		return true
	}
	if strings.EqualFold(stringMapAnyValue(action.Metadata, "compatibility_gate"), "trueclient2_javascript_review") {
		return true
	}
	if strings.Contains(strings.ToLower(stringMapAnyValue(action.Metadata, "source")), "evaluate_javascript") {
		return true
	}
	return boolMapValue(action.Metadata, "promotion_blocker") && strings.Contains(strings.ToLower(stringMapAnyValue(action.Metadata, "review_reason")), "javascript")
}

func trueClientJavaScriptActionReviewFor(index int, action dsl.Action) trueClientJavaScriptActionReview {
	status := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		stringMapAnyValue(action.Metadata, "javascript_review_status"),
		stringMapAnyValue(action.Metadata, "js_review_status"),
		stringMapAnyValue(action.Metadata, "review_status"),
	)))
	if status == "" {
		if boolMapValue(action.Metadata, "javascript_review_approved") || boolMapValue(action.Metadata, "js_review_approved") {
			status = "approved"
		} else {
			status = "pending"
		}
	}
	switch status {
	case "approved", "approve", "accepted":
		status = "approved"
	case "rejected", "reject", "blocked", "denied":
		status = "rejected"
	default:
		status = "pending"
	}
	reviewer := firstNonEmpty(
		stringMapAnyValue(action.Metadata, "javascript_review_reviewer"),
		stringMapAnyValue(action.Metadata, "js_review_reviewer"),
		stringMapAnyValue(action.Metadata, "approved_by"),
	)
	if status == "approved" && reviewer == "" {
		status = "pending"
	}
	return trueClientJavaScriptActionReview{
		Index:           index,
		Name:            action.Name,
		Type:            action.Type,
		Status:          status,
		Gate:            firstNonEmpty(stringMapAnyValue(action.Metadata, "compatibility_gate"), stringMapAnyValue(action.Metadata, "review_gate")),
		Reason:          stringMapAnyValue(action.Metadata, "review_reason"),
		Reviewer:        reviewer,
		ApprovedAt:      firstNonEmpty(stringMapAnyValue(action.Metadata, "javascript_review_approved_at"), stringMapAnyValue(action.Metadata, "js_review_approved_at"), stringMapAnyValue(action.Metadata, "approved_at")),
		Source:          stringMapAnyValue(action.Metadata, "source"),
		SourceStepID:    stringMapAnyValue(action.Metadata, "source_step_id"),
		SourceStepIndex: stringMapAnyValue(action.Metadata, "source_step_index"),
		ScriptPreview:   truncateReviewText(firstNonEmpty(action.Target, action.Value, action.Body), 160),
	}
}

func trueClientReplayStatus(result *enterprisearchive.ConvertResult) string {
	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return "required"
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved == nil {
		return "required"
	}
	for _, key := range []string{"browser_replay_status", "replay_status"} {
		switch strings.ToLower(strings.TrimSpace(stringValue(preserved[key]))) {
		case "passed", "pass", "success", "successful":
			return "passed"
		case "failed", "failure":
			return "failed"
		}
	}
	if !isNilImportValue(preserved["browser_replay_evidence"]) || !isNilImportValue(preserved["replay_evidence"]) {
		return "evidence_attached"
	}
	return "required"
}

func trueClientFixtureContractStatus() map[string]string {
	status := map[string]string{}
	for _, fixture := range trueclient.Web2FixtureContracts() {
		status[fixture.ID] = string(fixture.Status)
	}
	return status
}

func boolMapValue(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	switch value := values[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func stringMapAnyValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func truncateReviewText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return strings.TrimSpace(value[:limit-3]) + "..."
}

func averageTrueClientSelectorConfidence(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	total := 0
	count := 0
	for _, step := range inspection.Steps {
		for _, candidate := range step.SelectorCandidates {
			total += candidate.Confidence
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return int(float64(total)/float64(count) + 0.5)
}

func browserImportRepairSuggestions(script *dsl.Script) []scriptImportRepairSuggestion {
	suggestions := []scriptImportRepairSuggestion{
		{
			ID:          "browser:replay-validation",
			Category:    "replay",
			Severity:    "warning",
			Title:       "Run browser replay before saving as production-ready",
			Description: "Imported browser scripts depend on source selectors, waits, and device assumptions that should be verified against a live page.",
			Action:      "Open the imported draft in the visual editor and run browser replay. Update selectors or waits for any failed action.",
		},
	}

	for index, action := range script.Actions {
		actionID := firstNonEmpty(action.Name, fmt.Sprintf("action_%d", index+1))
		switch action.Type {
		case "browser_click", "browser_fill", "browser_select", "browser_tap", "browser_long_press", "browser_swipe":
			selector := firstNonEmpty(action.Selector, action.Target)
			if selector != "" {
				score, signals := browserSelectorStabilityScore(selector)
				suggestions = append(suggestions, scriptImportRepairSuggestion{
					ID:                     fmt.Sprintf("selector:%d:%s", index+1, stableReviewKey(actionID)),
					Category:               "selector",
					Severity:               "warning",
					Title:                  "Validate selector for " + actionID,
					Description:            fmt.Sprintf("Imported selector stability score is %d/100. Recorder object metadata and decomposed JavaScript selectors should be checked before promotion.", score),
					Action:                 browserSelectorRepairAction(score),
					TargetAction:           actionID,
					SelectorStabilityScore: score,
					SelectorSignals:        signals,
				})
			}
			if action.WaitFor == nil {
				suggestions = append(suggestions, scriptImportRepairSuggestion{
					ID:           fmt.Sprintf("wait-after:%d:%s", index+1, stableReviewKey(actionID)),
					Category:     "wait",
					Severity:     "info",
					Title:        "Add readiness wait after " + actionID,
					Description:  "Browser interaction steps imported without a readiness condition can race dynamic pages during replay.",
					Action:       "Attach a wait_for visible/text/url condition to the action or insert a browser_wait step using the page-ready signal seen in replay.",
					TargetAction: actionID,
				})
			}
		case "browser_wait":
			waitType := ""
			if action.WaitFor != nil {
				waitType = action.WaitFor.Type
			}
			suggestions = append(suggestions, scriptImportRepairSuggestion{
				ID:           fmt.Sprintf("wait:%d:%s", index+1, stableReviewKey(actionID)),
				Category:     "wait",
				Severity:     "info",
				Title:        "Review wait condition for " + actionID,
				Description:  "Imported waits may represent timing intent rather than the most stable page-ready condition.",
				Action:       "Prefer visible selector or text waits over fixed durations when the application exposes a reliable readiness signal.",
				TargetAction: firstNonEmpty(actionID, waitType),
			})
		}
	}

	return suggestions
}

func browserSelectorRepairAction(score int) string {
	if score >= 80 {
		return "Keep the selector if replay is stable; prefer data-testid or aria attributes for future recorder output."
	}
	if score >= 60 {
		return "Prefer stable attributes such as data-testid, aria labels, or unique form names; confirm the selector with browser replay."
	}
	return "Replace this selector with a stable data-testid, aria label, role/name, or unique form-name selector before approving the migrated script."
}

func browserSelectorStabilityScore(selector string) (int, []string) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return 0, []string{"empty selector"}
	}
	lower := strings.ToLower(selector)
	score := 45
	signals := make([]string, 0, 5)

	if strings.Contains(lower, "data-testid") || strings.Contains(lower, "data-test") || strings.Contains(lower, "data-qa") {
		score += 35
		signals = append(signals, "stable test attribute")
	}
	if strings.Contains(lower, "aria-label") || strings.Contains(lower, "role=") {
		score += 25
		signals = append(signals, "accessible selector")
	}
	if strings.Contains(lower, "[name=") || strings.Contains(lower, "[name'") || strings.Contains(lower, "[name\"") {
		score += 20
		signals = append(signals, "named form field")
	}
	if strings.HasPrefix(selector, "#") && !looksGeneratedSelectorToken(selector) {
		score += 20
		signals = append(signals, "simple id selector")
	}
	if strings.Contains(lower, ":nth-child") || strings.Contains(lower, ":nth-of-type") {
		score -= 30
		signals = append(signals, "positional selector")
	}
	if strings.HasPrefix(lower, "/html") || strings.HasPrefix(lower, "html > body") || strings.HasPrefix(lower, "body >") {
		score -= 25
		signals = append(signals, "absolute DOM path")
	}
	if looksGeneratedSelectorToken(selector) {
		score -= 20
		signals = append(signals, "generated-looking token")
	}
	if strings.Contains(selector, " ") || strings.Contains(selector, ">") {
		score -= 5
		signals = append(signals, "composite selector")
	}
	if len(signals) == 0 {
		signals = append(signals, "generic selector")
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, signals
}

func looksGeneratedSelectorToken(selector string) bool {
	token := strings.ToLower(selector)
	digitCount := 0
	separatorCount := 0
	for _, r := range token {
		switch {
		case r >= '0' && r <= '9':
			digitCount++
		case r == '-' || r == '_' || r == ':':
			separatorCount++
		}
	}
	if digitCount >= 4 && separatorCount >= 1 {
		return true
	}
	return strings.Contains(token, "ember") || strings.Contains(token, "react-select") || strings.Contains(token, "generated")
}

func stableReviewKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func buildEnterpriseArchiveImportReviewSections(warnings []string, result *enterprisearchive.ConvertResult, unsupportedElements []string, summary enterpriseArchiveImportSummary) []scriptImportReviewSection {
	sections := make([]scriptImportReviewSection, 0, 4)
	if result == nil {
		return sections
	}

	if result.DetectedProtocol != nil {
		items := []string{
			"Source protocol: " + firstNonEmpty(result.DetectedProtocol.DisplayName, result.DetectedProtocol.CanonicalSourceProtocol),
			"Import mode: " + string(result.DetectedProtocol.ImportMode),
			"Confidence: " + string(result.DetectedProtocol.Confidence),
		}
		if result.DetectedProtocol.APEXLoadProtocol != "" {
			items = append(items, "APEX Load protocol: "+result.DetectedProtocol.APEXLoadProtocol)
		}
		if result.DetectedProtocol.Bundle != "" {
			items = append(items, "Bundle: "+result.DetectedProtocol.Bundle)
		}
		for _, signal := range result.DetectedProtocol.Signals {
			items = append(items, "Signal: "+signal)
		}
		for _, conflict := range result.DetectedProtocol.Conflicts {
			items = append(items, "Conflict: "+conflict)
		}
		severity := "info"
		if result.DetectedProtocol.ImportMode != enterprisearchive.ImportModeExecutable || len(result.DetectedProtocol.Conflicts) > 0 {
			severity = "warning"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "detected_protocol",
			Title:    "Detected Source Protocol",
			Severity: severity,
			Summary:  "Protocol-aware routing classified this archive before conversion.",
			Items:    items,
		})
	}

	if result.ReviewArtifact != nil {
		items := []string{
			"Artifact ID: " + result.ReviewArtifact.ID,
			"Status: " + result.ReviewArtifact.Status,
		}
		for _, evidence := range result.ReviewArtifact.RequiredEvidence {
			items = append(items, "Required evidence: "+evidence)
		}
		for _, action := range result.ReviewArtifact.NextActions {
			items = append(items, "Next action: "+action)
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "review_artifact",
			Title:    "Protocol Review Artifact",
			Severity: "warning",
			Summary:  result.ReviewArtifact.Summary,
			Items:    items,
		})
	}

	if len(warnings) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "warnings",
			Title:    "Conversion Warnings",
			Severity: "warning",
			Summary:  "Review converter warnings before saving or replaying the imported draft.",
			Items:    warnings,
		})
	}

	if len(unsupportedElements) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "unsupported_elements",
			Title:    "Unsupported Source Elements",
			Severity: "blocker",
			Summary:  "These source constructs were preserved for review but were not converted into executable APEX Load actions.",
			Items:    unsupportedElements,
		})
	}

	preservedItems := make([]string, 0)
	if summary.ProjectFile != "" {
		preservedItems = append(preservedItems, "Project file: "+summary.ProjectFile)
	}
	if summary.MappedRuntimeFile != "" {
		preservedItems = append(preservedItems, "Mapped runtime file: "+summary.MappedRuntimeFile)
	}
	for _, parameterFile := range summary.ParameterFiles {
		preservedItems = append(preservedItems, "Parameter file: "+parameterFile)
	}
	for _, runtimeFile := range summary.RuntimeFiles {
		preservedItems = append(preservedItems, "Runtime file: "+runtimeFile)
	}
	for _, projectSection := range summary.ProjectSections {
		preservedItems = append(preservedItems, "Project section: "+projectSection)
	}
	if len(preservedItems) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "preserved_metadata",
			Title:    "Preserved Source Metadata",
			Severity: "info",
			Summary:  "Source project metadata was saved with the generated script for audit and repair workflows.",
			Items:    preservedItems,
		})
	}

	if len(summary.RuntimeSettingsParity) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_parity",
			Title:    "Runtime Settings Parity",
			Severity: runtimeSettingsParitySeverity(summary.RuntimeSettingsParity),
			Summary:  runtimeSettingsParitySummary(summary.RuntimeSettingsStatusCounts),
			Items:    runtimeSettingsParityItems(summary.RuntimeSettingsParity),
		})
	}

	if summary.MigrationReadiness != nil {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_migration_readiness",
			Title:    "Migration Readiness",
			Severity: migrationReadinessSeverity(summary.MigrationReadiness),
			Summary:  summary.MigrationReadiness.Summary,
			Items:    migrationReadinessItems(summary.MigrationReadiness),
		})
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		sections = append(sections, trueClientWeb2ReviewSections(summary)...)
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "browser_import_assumptions",
			Title:    "Browser Import Assumptions",
			Severity: "info",
			Summary:  "Mobile browser-profile archives map supported steps into browser DSL and require replay validation for selectors, waits, and device behavior; Web and Web 2.0 browser profiles are tracked as separate review profiles.",
			Items: []string{
				"Legacy desktop browser-profile archives remain out of scope; Web and Web 2.0 source profiles require guided review.",
				"Simple JavaScript fill/click statements are decomposed when possible; other JavaScript is preserved as review guidance.",
				"Selector fidelity should be checked with browser replay before production use.",
			},
		})
	}

	if result.DetectedProtocol != nil && (result.DetectedProtocol.CanonicalSourceProtocol == "oracle_web" || result.DetectedProtocol.CanonicalSourceProtocol == "siebel_web") {
		sections = append(sections, scriptImportReviewSection{
			Key:      "oracle_ebusiness_review",
			Title:    "Oracle E-Business Review Pack",
			Severity: "warning",
			Summary:  "Oracle-Web and Siebel-Web imports execute through HTTP but require enterprise correlation and session-state review before promotion.",
			Items: []string{
				"Review ICX/OA Framework or Siebel session identifiers and correlation candidates.",
				"Validate login, navigation, and form-post replay evidence against the target environment.",
				"Confirm Oracle E-Business runtime settings, data fit, and environment-specific tokens before saving reusable assets.",
			},
		})
	}

	if summary.SalesforceReview != nil {
		severity := "info"
		if len(summary.SalesforceReview.Blockers) > 0 {
			severity = "blocker"
		} else if summary.SalesforceReview.HARMatchCoverage.HARProvided && summary.SalesforceReview.HARMatchCoverage.CoveragePct < 80 {
			severity = "warning"
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "salesforce_review",
			Title:    "Salesforce Native Review",
			Severity: severity,
			Summary:  salesforceReviewSummary(summary.SalesforceReview),
			Items:    salesforceReviewItems(summary.SalesforceReview),
		})
	}

	return sections
}

func salesforceReviewSummary(review *salesforce.Review) string {
	if review == nil {
		return ""
	}
	if !review.Detected {
		return "Salesforce native review ran but did not detect Salesforce traffic."
	}
	coverage := review.HARMatchCoverage
	return fmt.Sprintf("Detected Salesforce traffic across %d domain(s); HAR matched %d of %d imported request(s) (%.1f%%).", len(review.Domains), coverage.MatchedRequests, coverage.ImportedRequests, coverage.CoveragePct)
}

func salesforceReviewItems(review *salesforce.Review) []string {
	if review == nil {
		return nil
	}
	items := make([]string, 0)
	for _, domain := range review.Domains {
		items = append(items, fmt.Sprintf("Domain: %s (%s, %d request(s))", domain.Host, domain.Kind, domain.Count))
	}
	if len(review.AuraEndpoints) > 0 {
		items = append(items, fmt.Sprintf("Aura endpoints detected: %d", len(review.AuraEndpoints)))
	}
	if len(review.Transactions) > 0 {
		items = append(items, fmt.Sprintf("Transaction candidates: %d", len(review.Transactions)))
		flowKinds := map[string]bool{}
		for _, transaction := range review.Transactions {
			for _, flowKind := range transaction.FlowKinds {
				if strings.TrimSpace(flowKind) != "" {
					flowKinds[flowKind] = true
				}
			}
		}
		if len(flowKinds) > 0 {
			ordered := make([]string, 0, len(flowKinds))
			for flowKind := range flowKinds {
				ordered = append(ordered, flowKind)
			}
			sort.Strings(ordered)
			items = append(items, "Salesforce flow kinds: "+strings.Join(ordered, ", "))
		}
	}
	if review.CorrelationConfidence != "" {
		items = append(items, "Correlation confidence: "+review.CorrelationConfidence)
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	return items
}

func buildEnterpriseArchiveImportSummary(result *enterprisearchive.ConvertResult, unsupportedElements []string, readinessPolicy enterprisearchive.RuntimeSettingMigrationPolicy) enterpriseArchiveImportSummary {
	summary := enterpriseArchiveImportSummary{
		UnsupportedFunctionCount: len(unsupportedElements),
	}
	if result != nil && result.DetectedProtocol != nil {
		summary.DetectedProtocol = result.DetectedProtocol
		summary.ImportMode = string(result.DetectedProtocol.ImportMode)
	}
	if result != nil && result.ReviewArtifact != nil {
		summary.ReviewArtifact = result.ReviewArtifact
	}
	if result != nil && result.SourceReview != nil {
		summary.SourceReview = result.SourceReview
	}

	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return summary
	}

	preserved := result.Script.Metadata.Import.Preserved
	if preserved == nil {
		return summary
	}

	summary.ProjectFile = stringValue(preserved["project_file"])
	summary.MappedRuntimeFile = stringValue(preserved["mapped_runtime_file"])
	summary.ProjectSections = sortedRecordKeys(preserved["project_settings"])
	summary.RuntimeFiles = sortedRecordKeys(preserved["runtime_files"])
	summary.ParameterFiles = stringSliceValue(preserved["parameter_files"])
	summary.RuntimeSettingsParity = runtimeSettingsParityValue(preserved["runtime_settings_parity"])
	summary.RuntimeSettingsStatusCounts = intMapValue(preserved["runtime_settings_status_counts"])
	summary.MigrationReadiness = enterpriseArchiveMigrationReadinessValue(preserved["runtime_settings_migration_readiness"])
	summary.TrueClientInspection = trueClientArchiveInspectionValue(preserved["trueclient_archive_inspection"])
	summary.SourceScriptType = stringValue(preserved["source_script_type"])
	summary.SourceProfile = stringValue(preserved["source_profile"])
	if summary.SourceReview == nil {
		summary.SourceReview = sourceReviewValue(preserved["source_review"])
	}
	if summary.MigrationReadiness == nil && len(summary.RuntimeSettingsParity) > 0 {
		summary.MigrationReadiness = enterprisearchive.BuildRTSMigrationReadiness(summary.RuntimeSettingsParity, readinessPolicy)
	}
	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		summary.Web2MigrationReadiness = buildWeb2MigrationReadinessReport(result, summary)
		summary.TrueClient2CompatReport = buildTrueClient2CompatReport(result, summary)
	}
	summary.SalesforceReview = salesforceReviewValue(preserved["salesforce_review"])

	return summary
}

func persistEnterpriseArchiveImportSummary(result *enterprisearchive.ConvertResult, summary enterpriseArchiveImportSummary) {
	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return
	}
	if result.Script.Metadata.Import.Preserved == nil {
		result.Script.Metadata.Import.Preserved = make(map[string]any)
	}
	if summary.MigrationReadiness != nil {
		result.Script.Metadata.Import.Preserved["runtime_settings_migration_readiness"] = summary.MigrationReadiness
	}
	if summary.Web2MigrationReadiness != nil {
		result.Script.Metadata.Import.Preserved["web2_migration_readiness"] = summary.Web2MigrationReadiness
	}
	if summary.TrueClient2CompatReport != nil {
		result.Script.Metadata.Import.Preserved["trueclient2_compat_report"] = summary.TrueClient2CompatReport
	}
	if summary.SalesforceReview != nil {
		result.Script.Metadata.Import.Preserved["salesforce_review"] = summary.SalesforceReview
	}
	if summary.SourceReview != nil {
		result.Script.Metadata.Import.Preserved["source_review"] = summary.SourceReview
	}
}

func salesforceReviewValue(value any) *salesforce.Review {
	if value == nil {
		return nil
	}
	if review, ok := value.(*salesforce.Review); ok {
		return review
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var review salesforce.Review
	if err := json.Unmarshal(data, &review); err != nil {
		return nil
	}
	return &review
}

func runtimeSettingsParityValue(value any) []enterprisearchive.RuntimeSettingParityEntry {
	switch typed := value.(type) {
	case []enterprisearchive.RuntimeSettingParityEntry:
		return append([]enterprisearchive.RuntimeSettingParityEntry(nil), typed...)
	case []any:
		entries := make([]enterprisearchive.RuntimeSettingParityEntry, 0, len(typed))
		for _, item := range typed {
			mapping, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entry := enterprisearchive.RuntimeSettingParityEntry{
				ID:           stringValue(mapping["id"]),
				Area:         stringValue(mapping["area"]),
				Concept:      stringValue(mapping["concept"]),
				Fields:       stringSliceValue(mapping["fields"]),
				Status:       enterprisearchive.RuntimeSettingStatus(stringValue(mapping["status"])),
				Notes:        stringValue(mapping["notes"]),
				RuntimeFiles: stringSliceValue(mapping["runtime_files"]),
			}
			if entry.ID == "" {
				continue
			}
			entries = append(entries, entry)
		}
		return entries
	default:
		return nil
	}
}

func enterpriseArchiveMigrationReadinessValue(value any) *enterprisearchive.RuntimeSettingMigrationReadiness {
	switch typed := value.(type) {
	case *enterprisearchive.RuntimeSettingMigrationReadiness:
		return typed
	case enterprisearchive.RuntimeSettingMigrationReadiness:
		clone := typed
		return &clone
	case map[string]any:
		readiness := &enterprisearchive.RuntimeSettingMigrationReadiness{
			ScorePct:         intValue(typed["score_pct"]),
			Blocked:          boolValue(typed["blocked"]),
			Summary:          stringValue(typed["summary"]),
			BlockingStatuses: runtimeSettingStatusSliceValue(typed["blocking_statuses"]),
			BlockingSettings: runtimeSettingsParityValue(typed["blocking_settings"]),
			Policy: enterprisearchive.RuntimeSettingMigrationPolicy{
				BlockStatuses: runtimeSettingPolicyStatuses(typed["policy"]),
			},
		}
		if readiness.ScorePct == 0 && !readiness.Blocked && readiness.Summary == "" && len(readiness.BlockingStatuses) == 0 && len(readiness.BlockingSettings) == 0 && len(readiness.Policy.BlockStatuses) == 0 {
			return nil
		}
		return readiness
	default:
		return nil
	}
}

func trueClientArchiveInspectionValue(value any) *trueclient.ArchiveInspection {
	switch typed := value.(type) {
	case *trueclient.ArchiveInspection:
		return typed
	case trueclient.ArchiveInspection:
		return &typed
	default:
		return nil
	}
}

func sourceReviewValue(value any) *enterprisearchive.SourceReview {
	switch typed := value.(type) {
	case *enterprisearchive.SourceReview:
		return typed
	case enterprisearchive.SourceReview:
		clone := typed
		return &clone
	case map[string]any:
		review := &enterprisearchive.SourceReview{
			SourceFiles: sourceReviewFilesValue(typed["source_files"]),
			Mappings:    sourceReviewMappingsValue(typed["mappings"]),
		}
		if len(review.SourceFiles) == 0 && len(review.Mappings) == 0 {
			return nil
		}
		return review
	default:
		return nil
	}
}

func sourceReviewFilesValue(value any) []enterprisearchive.SourceReviewFile {
	switch typed := value.(type) {
	case []enterprisearchive.SourceReviewFile:
		return append([]enterprisearchive.SourceReviewFile(nil), typed...)
	case []any:
		files := make([]enterprisearchive.SourceReviewFile, 0, len(typed))
		for _, item := range typed {
			mapping, ok := item.(map[string]any)
			if !ok {
				continue
			}
			file := enterprisearchive.SourceReviewFile{
				Path:          stringValue(mapping["path"]),
				Language:      stringValue(mapping["language"]),
				Content:       stringValue(mapping["content"]),
				OriginalBytes: intValue(mapping["original_bytes"]),
				Truncated:     boolValue(mapping["truncated"]),
				Redacted:      boolValue(mapping["redacted"]),
			}
			if file.Path == "" {
				continue
			}
			files = append(files, file)
		}
		return files
	default:
		return nil
	}
}

func sourceReviewMappingsValue(value any) []enterprisearchive.SourceReviewMapping {
	switch typed := value.(type) {
	case []enterprisearchive.SourceReviewMapping:
		return append([]enterprisearchive.SourceReviewMapping(nil), typed...)
	case []any:
		mappings := make([]enterprisearchive.SourceReviewMapping, 0, len(typed))
		for _, item := range typed {
			raw, ok := item.(map[string]any)
			if !ok {
				continue
			}
			mapping := enterprisearchive.SourceReviewMapping{
				ID:          stringValue(raw["id"]),
				SourceFile:  stringValue(raw["source_file"]),
				StartLine:   intValue(raw["start_line"]),
				EndLine:     intValue(raw["end_line"]),
				Function:    stringValue(raw["function"]),
				TargetPath:  stringValue(raw["target_path"]),
				TargetName:  stringValue(raw["target_name"]),
				TargetKind:  stringValue(raw["target_kind"]),
				Status:      stringValue(raw["status"]),
				SupportMode: stringValue(raw["support_mode"]),
				Notes:       stringValue(raw["notes"]),
			}
			if mapping.ID == "" && mapping.SourceFile == "" && mapping.TargetPath == "" {
				continue
			}
			mappings = append(mappings, mapping)
		}
		return mappings
	default:
		return nil
	}
}

func intMapValue(value any) map[string]int {
	switch typed := value.(type) {
	case map[string]int:
		if len(typed) == 0 {
			return nil
		}
		clone := make(map[string]int, len(typed))
		for key, count := range typed {
			clone[key] = count
		}
		return clone
	case map[string]any:
		if len(typed) == 0 {
			return nil
		}
		clone := make(map[string]int, len(typed))
		for key, raw := range typed {
			switch count := raw.(type) {
			case int:
				clone[key] = count
			case float64:
				clone[key] = int(count)
			}
		}
		if len(clone) == 0 {
			return nil
		}
		return clone
	default:
		return nil
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return parseBoolDefault(typed, false)
	default:
		return false
	}
}

func runtimeSettingStatusSliceValue(value any) []enterprisearchive.RuntimeSettingStatus {
	switch typed := value.(type) {
	case []enterprisearchive.RuntimeSettingStatus:
		return append([]enterprisearchive.RuntimeSettingStatus(nil), typed...)
	case []any:
		statuses := make([]enterprisearchive.RuntimeSettingStatus, 0, len(typed))
		for _, item := range typed {
			status := enterprisearchive.RuntimeSettingStatus(stringValue(item))
			if status == "" {
				continue
			}
			statuses = append(statuses, status)
		}
		return statuses
	default:
		return nil
	}
}

func runtimeSettingPolicyStatuses(value any) []enterprisearchive.RuntimeSettingStatus {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return runtimeSettingStatusSliceValue(mapping["block_statuses"])
}

func runtimeSettingsParitySeverity(entries []enterprisearchive.RuntimeSettingParityEntry) string {
	severity := "info"
	for _, entry := range entries {
		switch entry.Status {
		case enterprisearchive.RuntimeSettingStatusBlocked:
			return "blocker"
		case enterprisearchive.RuntimeSettingStatusPartial, enterprisearchive.RuntimeSettingStatusCompatibilityOnly:
			severity = "warning"
		}
	}
	return severity
}

func runtimeSettingsParitySummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "Imported runtime settings were classified against APEX Load's code-owned runtime compatibility matrix."
	}
	parts := make([]string, 0, 5)
	for _, status := range []string{
		string(enterprisearchive.RuntimeSettingStatusExecuted),
		string(enterprisearchive.RuntimeSettingStatusPartial),
		string(enterprisearchive.RuntimeSettingStatusCompatibilityOnly),
		string(enterprisearchive.RuntimeSettingStatusBlocked),
		string(enterprisearchive.RuntimeSettingStatusNonApplicable),
	} {
		if counts[status] == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[status], strings.ReplaceAll(status, "_", "-")))
	}
	if len(parts) == 0 {
		return "Imported runtime settings were classified against APEX Load's code-owned runtime compatibility matrix."
	}
	return "Imported runtime settings were classified against APEX Load's code-owned runtime compatibility matrix: " + strings.Join(parts, ", ") + "."
}

func runtimeSettingsParityItems(entries []enterprisearchive.RuntimeSettingParityEntry) []string {
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		item := fmt.Sprintf("%s: %s / %s", runtimeSettingStatusLabel(entry.Status), entry.Area, entry.Concept)
		if len(entry.RuntimeFiles) > 0 {
			item += " [" + strings.Join(entry.RuntimeFiles, ", ") + "]"
		}
		items = append(items, item)
	}
	return items
}

func migrationReadinessSeverity(readiness *enterprisearchive.RuntimeSettingMigrationReadiness) string {
	if readiness == nil {
		return "info"
	}
	if readiness.Blocked {
		return "blocker"
	}
	if readiness.ScorePct < 85 {
		return "warning"
	}
	return "info"
}

func migrationReadinessItems(readiness *enterprisearchive.RuntimeSettingMigrationReadiness) []string {
	if readiness == nil {
		return nil
	}
	items := []string{
		fmt.Sprintf("Score: %d%%", readiness.ScorePct),
		"Block policy: " + runtimeSettingStatusLabels(readiness.Policy.BlockStatuses),
	}
	if readiness.Blocked {
		items = append(items, "Matched blocking statuses: "+runtimeSettingStatusLabels(readiness.BlockingStatuses))
		for _, entry := range readiness.BlockingSettings {
			items = append(items, fmt.Sprintf("Blocking setting: %s / %s", entry.Area, entry.Concept))
		}
	} else {
		items = append(items, "No imported runtime settings matched the selected block statuses.")
	}
	return items
}

func runtimeSettingStatusLabels(statuses []enterprisearchive.RuntimeSettingStatus) string {
	if len(statuses) == 0 {
		return "None"
	}
	labels := make([]string, 0, len(statuses))
	for _, status := range statuses {
		labels = append(labels, runtimeSettingStatusLabel(status))
	}
	return strings.Join(labels, ", ")
}

func runtimeSettingStatusLabel(status enterprisearchive.RuntimeSettingStatus) string {
	switch status {
	case enterprisearchive.RuntimeSettingStatusExecuted:
		return "Executed"
	case enterprisearchive.RuntimeSettingStatusPartial:
		return "Partial"
	case enterprisearchive.RuntimeSettingStatusCompatibilityOnly:
		return "Compatibility-only"
	case enterprisearchive.RuntimeSettingStatusBlocked:
		return "Blocked"
	case enterprisearchive.RuntimeSettingStatusNonApplicable:
		return "Non-applicable"
	default:
		label := strings.ReplaceAll(string(status), "_", " ")
		if label == "" {
			return "Unknown"
		}
		return strings.ToUpper(label[:1]) + label[1:]
	}
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return uniqueSortedStrings(typed)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return uniqueSortedStrings(result)
	default:
		return nil
	}
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedRecordKeys(value any) []string {
	v := reflect.ValueOf(value)
	if !v.IsValid() || v.Kind() != reflect.Map || v.Type().Key().Kind() != reflect.String {
		return nil
	}

	keys := make([]string, 0, v.Len())
	for _, key := range v.MapKeys() {
		keys = append(keys, key.String())
	}
	sort.Strings(keys)
	return keys
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func attachSourceReviewToScripts(scripts []*dsl.Script, source string, sourceScriptName string, review any) {
	if isNilImportValue(review) {
		return
	}
	for _, script := range scripts {
		attachSourceReviewToScript(script, source, sourceScriptName, review)
	}
}

func attachSourceReviewToScript(script *dsl.Script, source string, sourceScriptName string, review any) {
	if script == nil || isNilImportValue(review) {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Source == "" {
		script.Metadata.Import.Source = source
	}
	if script.Metadata.Import.SourceScriptName == "" {
		script.Metadata.Import.SourceScriptName = sourceScriptName
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved["source_review"] = review
}

func persistProtocolCompatReport(script *dsl.Script, source string, report *protocolCompatReport) {
	if script == nil || report == nil {
		return
	}
	preserved := ensureScriptImportPreserved(script, source)
	preserved["protocol_compat_report"] = report
}

func buildDevWebProtocolCompatReport(result *devweb.ConvertResult) *protocolCompatReport {
	if result == nil {
		return nil
	}
	unsupported := result.Unsupported.Strings()
	status := protocolPromotionStatus("executable", len(unsupported), len(result.Warnings))
	return &protocolCompatReport{
		SourceFormat:         "DevWeb",
		CanonicalProtocol:    "devweb",
		ImportMode:           "executable",
		MappedSteps:          result.Stats.ConvertedActions,
		ReviewGatedSteps:     len(unsupported),
		UnsupportedFragments: uniqueSortedStrings(append(append([]string{}, result.Stats.UnsupportedCalls...), unsupported...)),
		RuntimeParity:        protocolRuntimeParityFromScript(result.Script),
		DataSources:          result.Stats.DataSources,
		CorrelationCoverage:  protocolCorrelationCoverage(result.Stats.Extractors, result.Stats.HTTPRequests),
		PromotionStatus:      status,
		NextActions:          protocolNextActions(status, "Validate DevWeb replay against the target environment."),
	}
}

func buildJMeterProtocolCompatReport(result *jmeter.ConvertResult, unsupported []string) *protocolCompatReport {
	if result == nil {
		return nil
	}
	reviewDiagnostics := countJMeterReviewDiagnostics(result)
	reviewGated := len(unsupported) + reviewDiagnostics
	status := protocolPromotionStatus("executable", reviewGated, len(result.Warnings))
	return &protocolCompatReport{
		SourceFormat:         "JMeter JMX",
		CanonicalProtocol:    "jmeter",
		ImportMode:           "executable",
		MappedSteps:          result.Stats.ConvertedActions,
		ReviewGatedSteps:     reviewGated,
		UnsupportedFragments: uniqueSortedStrings(append(append([]string(nil), unsupported...), jmeterReviewDiagnosticFragments(result)...)),
		RuntimeParity:        protocolRuntimeParityFromScript(result.Script),
		DataSources:          result.Stats.CSVSources,
		CorrelationCoverage:  protocolCorrelationCoverage(countJMeterExtractors(result), result.Stats.Samplers),
		PromotionStatus:      status,
		NextActions:          protocolNextActions(status, "Validate converted JMeter thread groups and scenario draft before promotion."),
	}
}

func countJMeterReviewDiagnostics(result *jmeter.ConvertResult) int {
	if result == nil {
		return 0
	}
	count := 0
	for _, item := range result.Unsupported {
		switch strings.ToLower(strings.TrimSpace(string(item.Severity))) {
		case "warning", "blocker":
			count++
		}
	}
	return count
}

func jmeterReviewDiagnosticFragments(result *jmeter.ConvertResult) []string {
	if result == nil {
		return nil
	}
	fragments := make([]string, 0)
	for _, item := range result.Unsupported {
		switch strings.ToLower(strings.TrimSpace(string(item.Severity))) {
		case "warning", "blocker":
			fragments = append(fragments, item.String())
		}
	}
	return fragments
}

func buildEnterpriseArchiveProtocolCompatReport(result *enterprisearchive.ConvertResult, unsupported []string, summary enterpriseArchiveImportSummary) *protocolCompatReport {
	if result == nil {
		return nil
	}
	importMode := "executable"
	canonical := "web_http_html"
	if result.DetectedProtocol != nil {
		importMode = string(result.DetectedProtocol.ImportMode)
		canonical = result.DetectedProtocol.CanonicalSourceProtocol
	}
	status := protocolPromotionStatus(importMode, len(unsupported), len(result.Warnings))
	nextActions := protocolNextActions(status, enterpriseArchiveProtocolValidationAction(canonical))
	if result.ReviewArtifact != nil && len(result.ReviewArtifact.NextActions) > 0 {
		nextActions = result.ReviewArtifact.NextActions
	}
	runtimeParity := summary.RuntimeSettingsStatusCounts
	if len(runtimeParity) == 0 {
		runtimeParity = protocolRuntimeParityFromScript(result.Script)
	}
	unsupportedFragments := enterpriseArchiveUnsupportedFragments(result, unsupported)
	return &protocolCompatReport{
		SourceFormat:         enterpriseArchiveProtocolSourceFormat(canonical),
		CanonicalProtocol:    canonical,
		ImportMode:           importMode,
		MappedSteps:          result.Stats.ConvertedActions,
		ReviewGatedSteps:     len(unsupported) + result.Stats.ReviewOnlyFunctions + result.Stats.CustomCallFunctions + result.Stats.ExternalRequiredFunctions,
		UnsupportedFragments: unsupportedFragments,
		RuntimeParity:        runtimeParity,
		DataSources:          protocolDataSourceCount(result.Script),
		CorrelationCoverage:  protocolCorrelationCoverage(result.Stats.Extractors, result.Stats.RawRequestActions),
		PromotionStatus:      status,
		NextActions:          nextActions,
	}
}

func enterpriseArchiveUnsupportedFragments(result *enterprisearchive.ConvertResult, unsupported []string) []string {
	fragments := append([]string(nil), unsupported...)
	if result != nil && result.ReviewArtifact != nil && len(result.ReviewArtifact.PreservedFunctions) > 0 {
		fragments = append(fragments, result.ReviewArtifact.PreservedFunctions...)
	}
	return uniqueSortedStrings(fragments)
}

func enterpriseArchiveProtocolSourceFormat(canonical string) string {
	switch canonical {
	case "devweb":
		return "DevWeb"
	case "jmeter":
		return "JMeter JMX"
	default:
		return "Enterprise Script Archive"
	}
}

func enterpriseArchiveProtocolValidationAction(canonical string) string {
	switch canonical {
	case "devweb":
		return "Validate converted DevWeb replay and review JavaScript/helper-module blockers before promotion."
	case "jmeter":
		return "Validate converted JMeter thread groups and scenario draft before promotion."
	default:
		return "Validate converted enterprise script archive replay and correlation behavior before promotion."
	}
}

func protocolPromotionStatus(importMode string, reviewCount int, warningCount int) string {
	switch importMode {
	case string(enterprisearchive.ImportModeExternalRunnerRequired):
		return "external_runner_required"
	case string(enterprisearchive.ImportModeUnsupported):
		return "unsupported"
	case string(enterprisearchive.ImportModeReviewOnly):
		return "review_required"
	}
	if reviewCount > 0 {
		return "review_required"
	}
	if warningCount > 0 {
		return "ready_with_warnings"
	}
	return "ready_for_validation"
}

func protocolNextActions(status string, validationAction string) []string {
	switch status {
	case "external_runner_required":
		return []string{"Preserve source artifact and attach approved external runner evidence before promotion."}
	case "unsupported":
		return []string{"Confirm protocol support strategy or defer this import as unsupported."}
	case "review_required":
		return []string{"Resolve review-gated fragments and unsupported constructs.", validationAction}
	case "ready_with_warnings":
		return []string{"Review converter warnings.", validationAction}
	default:
		return []string{validationAction}
	}
}

func protocolCorrelationCoverage(extractors int, requests int) string {
	if requests <= 0 {
		return "not_applicable"
	}
	if extractors <= 0 {
		return "none_detected"
	}
	return fmt.Sprintf("%d extractor(s) across %d request action(s)", extractors, requests)
}

func protocolRuntimeParityFromScript(script *dsl.Script) map[string]int {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	switch typed := script.Metadata.Import.Preserved["runtime_parity"].(type) {
	case map[string]string:
		counts := map[string]int{}
		for _, status := range typed {
			counts[status]++
		}
		return counts
	case map[string]any:
		counts := map[string]int{}
		for _, status := range typed {
			if text, ok := status.(string); ok {
				counts[text]++
			}
		}
		return counts
	default:
		return nil
	}
}

func protocolDataSourceCount(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return len(script.DataSources)
}

func countJMeterExtractors(result *jmeter.ConvertResult) int {
	if result == nil {
		return 0
	}
	total := 0
	var walk func(actions []dsl.Action)
	walk = func(actions []dsl.Action) {
		for _, action := range actions {
			total += len(action.Extractors)
			if action.Control != nil {
				walk(action.Control.Steps)
				walk(action.Control.Else)
			}
		}
	}
	for _, script := range result.Scripts {
		walk(script.Actions)
	}
	return total
}

func writeJMeterImportResponse(w http.ResponseWriter, status int, result *jmeter.ConvertResult) {
	unsupportedElements := uniqueSortedStrings(append(append([]string{}, result.UnsupportedElements...), result.Unsupported.Strings()...))
	protocolCompat := buildJMeterProtocolCompatReport(result, unsupportedElements)
	for _, script := range result.Scripts {
		persistProtocolCompatReport(script, "jmeter", protocolCompat)
	}
	writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Scripts:             result.Scripts,
		Script:              result.Script,
		ScenarioDraft:       result.ScenarioDraft,
		Warnings:            result.Warnings,
		UnsupportedElements: unsupportedElements,
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		Assumptions:         result.Assumptions,
		Stats:               result.Stats,
		Summary:             result.Summary,
		SourceReview:        result.SourceReview,
		ProtocolCompat:      protocolCompat,
	}))
}

func (s *Server) handleSaveJMeterImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scripts       []dsl.Script          `json:"scripts"`
		ScenarioDraft *jmeter.ScenarioDraft `json:"scenario_draft,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Scripts) == 0 {
		writeError(w, http.StatusBadRequest, "at least one script is required")
		return
	}

	storedScripts := make([]*StoredScript, 0, len(req.Scripts))
	scriptIDsByName := map[string]string{}
	for i := range req.Scripts {
		script := req.Scripts[i]
		if issues := validateScript(&script); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
			return
		}
		stored := s.runner.StoreScript(&script)
		storedScripts = append(storedScripts, stored)
		scriptIDsByName[script.Name] = stored.ID
	}

	var storedScenario *StoredScenario
	if req.ScenarioDraft != nil && len(req.ScenarioDraft.Groups) > 0 {
		groups := make([]CreateGroupEntry, 0, len(req.ScenarioDraft.Groups))
		for _, group := range req.ScenarioDraft.Groups {
			scriptID := scriptIDsByName[group.ScriptName]
			if scriptID == "" {
				continue
			}
			groups = append(groups, CreateGroupEntry{
				Name:         group.Name,
				ScriptID:     scriptID,
				VUsers:       group.VUsers,
				RampUp:       group.RampUp,
				InitStrategy: firstNonEmpty(group.Initialization, "at_runtime"),
			})
		}
		if len(groups) > 0 {
			scenario, err := s.runner.StoreScenario(CreateScenarioRequest{
				Name:         firstNonEmpty(req.ScenarioDraft.Name, "Imported JMeter Scenario"),
				Duration:     firstNonEmpty(req.ScenarioDraft.Duration, "5m"),
				ScenarioMode: firstNonEmpty(req.ScenarioDraft.ScenarioMode, "vuser_driven"),
				Groups:       groups,
			})
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to save scenario draft: "+err.Error())
				return
			}
			storedScenario = scenario
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"scripts":  storedScripts,
		"scenario": storedScenario,
		"count":    len(storedScripts),
	})
}

// handleImportSample handles POST /api/v1/scripts/import/sample.
// Accepts a JSON payload containing a supported sample format and returns a generated script preview.
func (s *Server) handleImportSample(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

	var req sample.ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	result, err := sample.Convert(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "sample conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(req.ProjectID)
	}
	attachSourceReviewToScript(result.Script, req.Format, req.Name, result.SourceReview)

	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Correlations:        result.Correlations,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		SourceReview:        result.SourceReview,
	}))
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

	// Optional: specify output directory via request body
	var req struct {
		OutputDir string `json:"output_dir,omitempty"`
		Filename  string `json:"filename,omitempty"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	filename := req.Filename
	if strings.TrimSpace(filename) == "" {
		filename = stored.Name + ".json"
	}

	// Confine the export to APEXLOAD_EXPORT_DIR (default ./exports). The
	// caller-supplied output directory and filename are untrusted: the filename
	// is always sanitized and the directory is rooted so "../" cannot escape.
	fullPath, err := resolveConfinedExportPath(exportBaseDir(), req.OutputDir, filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid export path")
		return
	}

	// Wrap in the standard script envelope
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

	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
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

// handleDownloadScript handles GET /api/v1/scripts/{id}/download
// Returns the script JSON as a downloadable file.
func (s *Server) handleDownloadScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	envelope := map[string]any{
		"script": stored.Script,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal script: "+err.Error())
		return
	}

	filename := sanitizeFilename(stored.Name) + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleUpdateScript handles PUT /api/v1/scripts/{id}
// Updates an existing script in-place.
func (s *Server) handleUpdateScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var wrapper struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	script := &wrapper.Script

	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}

	updated, ok := s.runner.UpdateScript(id, script)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	s.recordAuditEvent(r, "script.update", "success", "script", updated.ID, updated.ProjectID, map[string]any{"name": updated.Name, "folder_id": updated.FolderID})

	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteScript handles DELETE /api/v1/scripts/{id}
func (s *Server) handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !s.runner.DeleteScript(id) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	s.recordAuditEvent(r, "script.delete", "success", "script", id, stored.ProjectID, map[string]any{"name": stored.Name, "folder_id": stored.FolderID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// sanitizeFilename makes a string safe for use as a filename.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	result := replacer.Replace(name)
	if result == "" {
		result = "script"
	}
	return result
}
