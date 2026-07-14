package api

// handlers_import_har — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/har"
)

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
	if len(importReq.SecondaryData) > 0 {
		diff, err := har.Diff(importReq.Data, importReq.SecondaryData, importReq.Opts)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "HAR pair diff failed: "+err.Error())
			return
		}
		result.Diff = diff
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	salesforceAugmentation := augmentHARImportWithSalesforce(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "har", salesforceNativeRequested(r), agentforceTransformRequested(r), true)
	if salesforceAugmentation.Detected {
		issues := validateScript(salesforceAugmentation.Script)
		if len(issues) > 0 {
			result.Warnings = append(result.Warnings, issues...)
		}
		preflight := buildReplayPreflight(salesforceAugmentation.Script, nil, issues, result.Warnings)
		attachImportReplayPreflight(salesforceAugmentation.Script, salesforceAugmentation.Source, preflight)

		preApplied := sampleCorrelationsToPreApplied(result.Correlations)
		if salesforceAugmentation.Script != result.Script {
			preApplied = nil
		}
		writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
			Result:              result,
			Script:              salesforceAugmentation.Script,
			Warnings:            result.Warnings,
			Stats:               result.Stats,
			Correlations:        salesforceAugmentation.Correlations,
			UnsupportedElements: result.Unsupported.Strings(),
			ReviewSections:      salesforceAugmentation.ReviewSections,
			SourceReview:        result.SourceReview,
			Summary:             salesforceAugmentation.Summary,
			Parameterization:    buildImportParameterization(salesforceAugmentation.Script, preApplied),
			Preflight:           preflight,
		}))
		return
	}

	attachSourceReviewToScript(result.Script, "har", importReq.Opts.ScriptName, result.SourceReview)
	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	preflight := buildReplayPreflight(result.Script, nil, issues, result.Warnings)
	attachImportReplayPreflight(result.Script, "har", preflight)

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Correlations:        result.Correlations,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      harImportReviewSections(result),
		SourceReview:        result.SourceReview,
		Summary:             harImportSummary(result),
		Parameterization:    buildImportParameterization(result.Script, sampleCorrelationsToPreApplied(result.Correlations)),
		Preflight:           preflight,
	}))
}

func salesforceNativeRequested(r *http.Request) bool {
	if r == nil {
		return true
	}
	return parseBoolDefault(r.URL.Query().Get("salesforce_native"), true)
}

func harImportSummary(result *har.ConvertResult) map[string]any {
	summary := map[string]any{
		"artifact_mode":      har.ArtifactModeDesignHAR,
		"generation_mode":    "deterministic_har_spine",
		"protocol_scope":     "http_family_only",
		"llm_role":           "naming_assertions_parameterization_semantic_review",
		"non_http_protocols": []string{"solace", "kafka", "ibm_mq"},
	}
	if result == nil {
		return summary
	}
	if result.ArtifactMode != "" {
		summary["artifact_mode"] = result.ArtifactMode
	}
	if result.Governance != nil {
		summary["artifact_sensitivity"] = result.Governance.Sensitivity
		summary["retention_days"] = result.Governance.RetentionDays
		summary["governance"] = result.Governance
	}
	if result.Diff != nil {
		summary["har_diff"] = result.Diff
	}
	return summary
}

func harImportReviewSections(result *har.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return []scriptImportReviewSection{}
	}
	sections := importDiagnosticReviewSections(result.Unsupported)
	if result.Governance != nil {
		items := []string{
			fmt.Sprintf("Artifact mode: %s", result.Governance.ArtifactMode),
			fmt.Sprintf("Sensitivity: %s", result.Governance.Sensitivity),
			fmt.Sprintf("Retention: %d day(s)", result.Governance.RetentionDays),
			fmt.Sprintf("Scope: %s", result.Governance.Scope),
		}
		if result.Governance.RedactionEnabled {
			items = append(items, "Scrub-on-write redaction was applied before the HAR source review was created.")
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "har_governance",
			Title:    "HAR Artifact Governance",
			Severity: "info",
			Summary:  "HAR-with-bodies artifacts are treated as sensitive and governed before review.",
			Items:    items,
		})
	}
	if result.Diff != nil {
		items := []string{
			fmt.Sprintf("%d dynamic candidate(s)", result.Diff.DynamicCandidates),
			fmt.Sprintf("%d static candidate(s)", result.Diff.StaticCandidates),
			fmt.Sprintf("%d unmatched baseline candidate(s)", result.Diff.UnmatchedCandidates),
		}
		for _, classification := range result.Diff.Classifications {
			if classification.Classification != har.DiffClassificationDynamic {
				continue
			}
			items = append(items, fmt.Sprintf("%s changed between recordings with %.0f%% confidence", classification.Name, classification.DiffConfidence*100))
			if len(items) >= 8 {
				break
			}
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "har_pair_diff",
			Title:    "Two-Recording Correlation Diff",
			Severity: "info",
			Summary:  "Values that change between otherwise matching recordings are promoted as dynamic correlation candidates.",
			Items:    items,
		})
	}
	return sections
}

func readHARImportRequest(w http.ResponseWriter, r *http.Request) (*harImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, harImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req struct {
			Name              string             `json:"name,omitempty"`
			ProjectID         string             `json:"project_id,omitempty"`
			Content           string             `json:"content"`
			SecondaryContent  string             `json:"secondary_content,omitempty"`
			ComparisonContent string             `json:"comparison_content,omitempty"`
			ContentB          string             `json:"content_b,omitempty"`
			Options           har.ConvertOptions `json:"options,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		secondary := firstNonEmpty(req.SecondaryContent, req.ComparisonContent, req.ContentB)
		req.Options.ArtifactMode = normalizeHARArtifactMode(req.Options.ArtifactMode, secondary != "")
		return &harImportRequest{Data: []byte(req.Content), SecondaryData: []byte(secondary), ProjectID: firstNonEmpty(req.ProjectID, r.URL.Query().Get("project_id")), Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, uploadName, ok := readImportMultipartFile(w, r, harImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		secondaryData, ok := readOptionalHARComparisonFile(w, r)
		if !ok {
			return nil, false
		}
		artifactMode := normalizeHARArtifactMode(r.FormValue("artifact_mode"), len(secondaryData) > 0)
		return &harImportRequest{
			Data:          data,
			SecondaryData: secondaryData,
			ProjectID:     authorizedImportProjectID(r),
			Opts: har.ConvertOptions{
				ScriptName:          firstNonEmpty(r.FormValue("name"), strings.TrimSuffix(uploadName, filepath.Ext(uploadName))),
				BaseURL:             r.FormValue("base_url"),
				ArtifactMode:        artifactMode,
				RetentionDays:       parseOptionalIntDefault(r.FormValue("retention_days"), 0),
				RedactSensitive:     parseOptionalBool(r.FormValue("redact_sensitive")),
				IncludeStaticAssets: parseOptionalBool(r.FormValue("include_static_assets")),
			},
		}, true
	}
	data, ok := readImportBody(w, r)
	if !ok {
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &harImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Opts: har.ConvertOptions{
			ScriptName:          strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
			BaseURL:             r.URL.Query().Get("base_url"),
			ArtifactMode:        normalizeHARArtifactMode(r.URL.Query().Get("artifact_mode"), false),
			RetentionDays:       parseOptionalIntDefault(r.URL.Query().Get("retention_days"), 0),
			RedactSensitive:     parseOptionalBool(r.URL.Query().Get("redact_sensitive")),
			IncludeStaticAssets: parseOptionalBool(r.URL.Query().Get("include_static_assets")),
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
