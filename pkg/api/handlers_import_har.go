package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// handlers_import_har — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"

	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *Server) handleImportHAR(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readHARImportRequest(w, r)
	if !ok {
		return
	}
	profile, err := s.projectRecordingProfile(normalizeProjectID(importReq.ProjectID), importReq.RecordingProfileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	importReq.Opts.Profile = profile
	importReq.Opts.RecordingProfileID = strings.TrimSpace(importReq.RecordingProfileID)
	importReq.Opts.EntryPoint = sample.EntryPointImportHAR

	result, err := har.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	importReq.noteSalvage(result)
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	if len(importReq.SecondaryData) > 0 {
		diff, err := har.Diff(importReq.Data, importReq.SecondaryData, importReq.Opts)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "HAR pair diff failed: "+err.Error())
			return
		}
		result.Diff = diff
	}
	if result.Script != nil {
		// Scope the HAR draft even when a native Salesforce rewrite replaces it
		// below: the raw conversion result still travels in the response.
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	salesforceNative := salesforceNativeRequested(r)
	agentforceTransform := agentforceTransformRequested(r)
	disableSalesforce := false
	if result.EffectiveSettings != nil {
		salesforceNative = result.EffectiveSettings.SalesforceNative
		agentforceTransform = result.EffectiveSettings.AgentforceTransform
		disableSalesforce = result.EffectiveSettings.DisableSalesforce
	}
	// A Salesforce HAR may ship a native rewrite instead of the raw HAR draft.
	// Everything downstream of that choice — which script, which source profile,
	// which sections — is the same for both outcomes.
	var salesforceAugmentation salesforceHARAugmentation
	if !disableSalesforce {
		salesforceAugmentation = importreview.AugmentHARImportWithSalesforceProfile(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "har", salesforceNative, agentforceTransform, true, profile)
	}
	script := result.Script
	source := "har"
	sections := harImportReviewSections(result)
	correlations := any(result.Correlations)
	summary := harImportSummary(result)
	preApplied := sampleCorrelationsToPreApplied(result.Correlations)
	if salesforceAugmentation.Detected {
		script = salesforceAugmentation.Script
		source = salesforceAugmentation.Source
		sections = salesforceAugmentation.ReviewSections
		correlations = salesforceAugmentation.Correlations
		summary = salesforceAugmentation.Summary
		if script != result.Script {
			preApplied = nil
		}
	}

	var preparedCredentialReview []importreview.SecretBindingRequirement
	var credentialReviewPrepared bool
	draft := importDraft{
		Source:           source,
		SourceScriptName: importReq.Opts.ScriptName,
		ProjectID:        importReq.ProjectID,
		Script:           script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         sections,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
		BeforeFinalize: func(s *dsl.Script, warnings *[]string, unsupported *diag.Diagnostics) error {
			applyClientGeneratedUUIDs(s, importReq.Data, result)
			applyClientTimestamps(s, importReq.Data, result)
			*warnings = result.Warnings
			*unsupported = result.Unsupported
			if result.EffectiveSettings != nil && result.EffectiveSettings.Parameterize && len(importReq.Opts.ParameterizationSelections) == 0 {
				result.Warnings = append(result.Warnings, importreview.ApplyAutoParameterizationToScript(s)...)
				*warnings = result.Warnings
			}
			if len(importReq.Opts.ParameterizationSelections) > 0 {
				if _, err := importreview.ApplyParameterizationSelectionsToScript(s, 0, importReq.Opts.ParameterizationSelections); err != nil {
					return err
				}
			}
			if len(importReq.SecretBindings) > 0 {
				var err error
				_, preparedCredentialReview, credentialReviewPrepared, err = importreview.ApplySecretBindingsToImportDraft(s, 0, importReq.SecretBindings, protectedRequirements)
				if err != nil {
					return err
				}
			}
			sample.AttachEffectiveSettings(s, result.EffectiveSettings)
			return nil
		},
	}
	augmented, err := draft.AugmentWithAgentforceTransformChecked(agentforceTransform)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result.Warnings = augmented.Warnings

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                   result,
		Script:                   script,
		Warnings:                 result.Warnings,
		Stats:                    result.Stats,
		Correlations:             correlations,
		Diagnostics:              result.Unsupported,
		UnsupportedElements:      result.Unsupported.Strings(),
		ReviewSections:           augmented.Sections,
		SourceReview:             result.SourceReview,
		Summary:                  summary,
		Parameterization:         buildImportParameterization(script, preApplied),
		Preflight:                augmented.Preflight,
		EffectiveSettings:        result.EffectiveSettings,
		ProtectedRequirements:    protectedRequirements,
		PreparedCredentialReview: preparedCredentialReview,
		CredentialReviewPrepared: credentialReviewPrepared,
	})
}

func salesforceNativeRequested(r *http.Request) bool {
	if r == nil {
		return true
	}
	return parseBoolDefault(r.URL.Query().Get("salesforce_native"), true)
}

var harImportSummary = importreview.HarImportSummary

// harGovernanceItems states the governance facts as sentences. The raw enum
// values stay available in the summary's governance block.
var harGovernanceItems = importreview.HarGovernanceItems

// harImportReviewSections is the HAR importer's own sections. Converter
// diagnostics are not included: the shared augmentation pass appends those for
// every format, and adding them here too would show each one twice.
var harImportReviewSections = importreview.HarImportReviewSections

func readHARImportRequest(w http.ResponseWriter, r *http.Request) (*harImportRequest, bool) {
	req, ok := decodeHARImportRequest(w, r)
	if !ok {
		return nil, false
	}
	// A capture may arrive zipped; everything after this sees the HAR itself.
	if !unwrapHARImportArchives(w, req) {
		return nil, false
	}
	// A truncated capture is imported only on explicit request; otherwise the
	// error says how many complete entries it holds and how to keep them.
	if salvaged, report, truncated := har.SalvageTruncated(req.Data); truncated {
		if req.Opts.SalvageTruncated == nil || !*req.Opts.SalvageTruncated {
			writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+report.Hint())
			return nil, false
		}
		req.Data = salvaged
		req.Salvage = &report
	}
	return req, true
}

func decodeHARImportRequest(w http.ResponseWriter, r *http.Request) (*harImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, harImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req struct {
			Name                       string                                `json:"name,omitempty"`
			ProjectID                  string                                `json:"project_id,omitempty"`
			Content                    string                                `json:"content"`
			SecondaryContent           string                                `json:"secondary_content,omitempty"`
			ComparisonContent          string                                `json:"comparison_content,omitempty"`
			ContentB                   string                                `json:"content_b,omitempty"`
			Options                    har.ConvertOptions                    `json:"options,omitempty"`
			RecordingProfileID         string                                `json:"recording_profile_id,omitempty"`
			ProfileID                  string                                `json:"profile_id,omitempty"`
			ConversionPolicy           string                                `json:"conversion_policy,omitempty"`
			CorrelationMode            string                                `json:"correlation_mode,omitempty"`
			ResourceMode               string                                `json:"resource_mode,omitempty"`
			PageTitleChecks            *bool                                 `json:"page_title_checks,omitempty"`
			IncludeAssertions          *bool                                 `json:"include_assertions,omitempty"`
			InferExtractors            *bool                                 `json:"infer_extractors,omitempty"`
			IncludeStaticAssets        *bool                                 `json:"include_static_assets,omitempty"`
			RedactSensitive            *bool                                 `json:"redact_sensitive,omitempty"`
			SalvageTruncated           *bool                                 `json:"salvage_truncated,omitempty"`
			AsyncScan                  *bool                                 `json:"async_scan,omitempty"`
			HTTPVersionMode            string                                `json:"http_version_mode,omitempty"`
			DisableSalesforce          *bool                                 `json:"disable_salesforce,omitempty"`
			SalesforceNative           *bool                                 `json:"salesforce_native,omitempty"`
			AgentforceTransform        *bool                                 `json:"agentforce_transform,omitempty"`
			Parameterize               *bool                                 `json:"parameterize,omitempty"`
			ParameterizationSelections []paramize.Selection                  `json:"parameterization_selections,omitempty"`
			SecretBindings             []importreview.SecretBindingSelection `json:"secret_bindings,omitempty"`
		}
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		profileID := strings.TrimSpace(firstNonEmpty(
			req.RecordingProfileID,
			req.ProfileID,
			req.Options.RecordingProfileID,
			upload.Value("recording_profile_id"),
			upload.Value("profile_id"),
		))
		req.Options.RecordingProfileID = profileID
		req.Options.ConversionPolicy = strings.TrimSpace(firstNonEmpty(req.ConversionPolicy, req.Options.ConversionPolicy, upload.Value("conversion_policy")))
		req.Options.CorrelationMode = strings.TrimSpace(firstNonEmpty(req.CorrelationMode, req.Options.CorrelationMode, upload.Value("correlation_mode")))
		req.Options.ResourceMode = strings.TrimSpace(firstNonEmpty(req.ResourceMode, req.Options.ResourceMode, upload.Value("resource_mode")))
		req.Options.HTTPVersionMode = strings.TrimSpace(firstNonEmpty(req.HTTPVersionMode, req.Options.HTTPVersionMode, upload.Value("http_version_mode")))
		req.Options.PageTitleChecks = firstNonNilBool(req.PageTitleChecks, req.Options.PageTitleChecks, parseOptionalBool(upload.Value("page_title_checks")))
		req.Options.IncludeAssertions = firstNonNilBool(req.IncludeAssertions, req.Options.IncludeAssertions, parseOptionalBool(upload.Value("include_assertions")))
		req.Options.InferExtractors = firstNonNilBool(req.InferExtractors, req.Options.InferExtractors, parseOptionalBool(upload.Value("infer_extractors")))
		req.Options.IncludeStaticAssets = firstNonNilBool(req.IncludeStaticAssets, req.Options.IncludeStaticAssets, parseOptionalBool(upload.Value("include_static_assets")))
		req.Options.RedactSensitive = firstNonNilBool(req.RedactSensitive, req.Options.RedactSensitive, parseOptionalBool(upload.Value("redact_sensitive")))
		req.Options.SalvageTruncated = firstNonNilBool(req.SalvageTruncated, req.Options.SalvageTruncated, parseOptionalBool(upload.Value("salvage_truncated")))
		req.Options.AsyncScan = firstNonNilBool(req.AsyncScan, req.Options.AsyncScan, parseOptionalBool(upload.Value("async_scan")))
		req.Options.DisableSalesforce = firstNonNilBool(req.DisableSalesforce, req.Options.DisableSalesforce, parseOptionalBool(upload.Value("disable_salesforce")))
		req.Options.SalesforceNative = firstNonNilBool(req.SalesforceNative, req.Options.SalesforceNative, parseOptionalBool(upload.Value("salesforce_native")))
		req.Options.AgentforceTransform = firstNonNilBool(req.AgentforceTransform, req.Options.AgentforceTransform, parseOptionalBool(upload.Value("agentforce_transform")))
		if param := firstNonNilBool(req.Parameterize, parseOptionalBool(upload.Value("parameterize"))); param != nil {
			req.Options.Parameterize = *param
		}
		if len(req.ParameterizationSelections) > 0 {
			req.Options.ParameterizationSelections = req.ParameterizationSelections
		}
		if !validHTTPVersionMode(w, req.Options.HTTPVersionMode) || !validHARImportPolicyOptions(w, req.Options) {
			return nil, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		secondary := firstNonEmpty(req.SecondaryContent, req.ComparisonContent, req.ContentB)
		req.Options.ArtifactMode = normalizeHARArtifactMode(req.Options.ArtifactMode, secondary != "")
		return &harImportRequest{
			Data:               []byte(req.Content),
			SecondaryData:      []byte(secondary),
			ProjectID:          upload.jsonProjectID(req.ProjectID),
			Opts:               req.Options,
			RecordingProfileID: profileID,
			SecretBindings:     req.SecretBindings,
		}, true
	}

	// A comparison HAR is a second file, so it only travels on a multipart
	// upload; the other transports carry the baseline capture alone.
	var secondaryData []byte
	if upload.Transport == importTransportMultipart {
		secondaryData, ok = readOptionalHARComparisonFile(w, r)
		if !ok {
			return nil, false
		}
	}
	httpVersionMode := strings.TrimSpace(upload.Value("http_version_mode"))
	profileID := strings.TrimSpace(firstNonEmpty(upload.Value("recording_profile_id"), upload.Value("profile_id")))
	opts := har.ConvertOptions{
		ScriptName:          firstNonEmpty(upload.Value("name"), upload.Name),
		BaseURL:             upload.Value("base_url"),
		RecordingProfileID:  profileID,
		ConversionPolicy:    strings.TrimSpace(upload.Value("conversion_policy")),
		CorrelationMode:     strings.TrimSpace(upload.Value("correlation_mode")),
		ResourceMode:        strings.TrimSpace(upload.Value("resource_mode")),
		ArtifactMode:        normalizeHARArtifactMode(upload.Value("artifact_mode"), len(secondaryData) > 0),
		RetentionDays:       parseOptionalIntDefault(upload.Value("retention_days"), 0),
		RedactSensitive:     parseOptionalBool(upload.Value("redact_sensitive")),
		IncludeAssertions:   parseOptionalBool(upload.Value("include_assertions")),
		InferExtractors:     parseOptionalBool(upload.Value("infer_extractors")),
		IncludeStaticAssets: parseOptionalBool(upload.Value("include_static_assets")),
		PageTitleChecks:     parseOptionalBool(upload.Value("page_title_checks")),
		SalvageTruncated:    parseOptionalBool(upload.Value("salvage_truncated")),
		AsyncScan:           parseOptionalBool(upload.Value("async_scan")),
		DisableSalesforce:   parseOptionalBool(upload.Value("disable_salesforce")),
		SalesforceNative:    parseOptionalBool(upload.Value("salesforce_native")),
		AgentforceTransform: parseOptionalBool(upload.Value("agentforce_transform")),
		Parameterize:        parseBoolDefault(upload.Value("parameterize"), false),
		HTTPVersionMode:     httpVersionMode,
	}
	if !validHTTPVersionMode(w, httpVersionMode) || !validHARImportPolicyOptions(w, opts) {
		return nil, false
	}
	if rawSelections := strings.TrimSpace(upload.Value("parameterization_selections")); rawSelections != "" {
		if err := json.Unmarshal([]byte(rawSelections), &opts.ParameterizationSelections); err != nil {
			writeError(w, http.StatusBadRequest, "invalid parameterization_selections: "+err.Error())
			return nil, false
		}
	}
	var secretBindings []importreview.SecretBindingSelection
	if rawBindings := strings.TrimSpace(upload.Value("secret_bindings")); rawBindings != "" {
		if err := json.Unmarshal([]byte(rawBindings), &secretBindings); err != nil {
			writeError(w, http.StatusBadRequest, "invalid secret_bindings: "+err.Error())
			return nil, false
		}
	}
	return &harImportRequest{
		Data:               upload.Data,
		SecondaryData:      secondaryData,
		ProjectID:          upload.ProjectID,
		RecordingProfileID: profileID,
		Opts:               opts,
		SecretBindings:     secretBindings,
	}, true
}

func firstNonNilBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			copied := *value
			return &copied
		}
	}
	return nil
}

func validHARImportPolicyOptions(w http.ResponseWriter, opts har.ConvertOptions) bool {
	if _, err := sample.ValidateConversionPolicy(opts.ConversionPolicy); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := sample.ValidateCorrelationMode(opts.CorrelationMode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := sample.ValidateResourceMode(opts.ResourceMode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func normalizeHARArtifactMode(value string, hasComparison bool) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	if hasComparison {
		return har.ArtifactModeDesignHARPair
	}
	return har.ArtifactModeDesignHAR
}

func readOptionalHARComparisonFile(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	for _, field := range []string{"secondary_file", "comparison_file", "file_b"} {
		file, _, err := r.FormFile(field)
		if err != nil {
			continue
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read comparison HAR: "+err.Error())
			return nil, false
		}
		return data, true
	}
	return nil, true
}

func parseOptionalIntDefault(value string, defaultValue int) int {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return defaultValue
	}
	return parsed
}

func parseOptionalBool(value string) *bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := parseBoolDefault(value, false)
	return &parsed
}

// validHTTPVersionMode writes 400 for an http_version_mode import option that
// is not upgrade, exact or inherit; empty selects upgrade.
func validHTTPVersionMode(w http.ResponseWriter, mode string) bool {
	if _, err := requestimport.NormalizeHTTPVersionMode(mode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}
