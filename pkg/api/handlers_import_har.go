package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// handlers_import_har — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"

	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
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

	agentforceTransform := agentforceTransformRequested(r)
	// A Salesforce HAR may ship a native rewrite instead of the raw HAR draft.
	// Everything downstream of that choice — which script, which source profile,
	// which sections — is the same for both outcomes.
	salesforceAugmentation := augmentHARImportWithSalesforce(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "har", salesforceNativeRequested(r), agentforceTransform, true)
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
	applyClientGeneratedUUIDs(script, importReq.Data, result)
	applyClientTimestamps(script, importReq.Data, result)

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
	}
	augmented := draft.AugmentWithAgentforceTransform(agentforceTransform)
	result.Warnings = augmented.Warnings

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                result,
		Script:                script,
		Warnings:              result.Warnings,
		Stats:                 result.Stats,
		Correlations:          correlations,
		Diagnostics:           result.Unsupported,
		UnsupportedElements:   result.Unsupported.Strings(),
		ReviewSections:        augmented.Sections,
		SourceReview:          result.SourceReview,
		Summary:               summary,
		Parameterization:      buildImportParameterization(script, preApplied),
		Preflight:             augmented.Preflight,
		ProtectedRequirements: protectedRequirements,
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
			Name              string             `json:"name,omitempty"`
			ProjectID         string             `json:"project_id,omitempty"`
			Content           string             `json:"content"`
			SecondaryContent  string             `json:"secondary_content,omitempty"`
			ComparisonContent string             `json:"comparison_content,omitempty"`
			ContentB          string             `json:"content_b,omitempty"`
			Options           har.ConvertOptions `json:"options,omitempty"`
			// RecordingProfileID names a project recording profile to apply.
			RecordingProfileID string `json:"recording_profile_id,omitempty"`
		}
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if !validHTTPVersionMode(w, req.Options.HTTPVersionMode) {
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
			RecordingProfileID: strings.TrimSpace(req.RecordingProfileID),
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
	if !validHTTPVersionMode(w, httpVersionMode) {
		return nil, false
	}
	return &harImportRequest{
		Data:               upload.Data,
		SecondaryData:      secondaryData,
		ProjectID:          upload.ProjectID,
		RecordingProfileID: strings.TrimSpace(upload.Value("recording_profile_id")),
		Opts: har.ConvertOptions{
			ScriptName:          firstNonEmpty(upload.Value("name"), upload.Name),
			BaseURL:             upload.Value("base_url"),
			ArtifactMode:        normalizeHARArtifactMode(upload.Value("artifact_mode"), len(secondaryData) > 0),
			RetentionDays:       parseOptionalIntDefault(upload.Value("retention_days"), 0),
			RedactSensitive:     parseOptionalBool(upload.Value("redact_sensitive")),
			IncludeStaticAssets: parseOptionalBool(upload.Value("include_static_assets")),
			SalvageTruncated:    parseOptionalBool(upload.Value("salvage_truncated")),
			AsyncScan:           parseOptionalBool(upload.Value("async_scan")),
			HTTPVersionMode:     httpVersionMode,
		},
	}, true
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
