package api

// handlers_import_har — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	augmented := draft.augmentWithAgentforceTransform(agentforceTransform)
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

// harImportReviewSections is the HAR importer's own sections. Converter
// diagnostics are not included: the shared augmentation pass appends those for
// every format, and adding them here too would show each one twice.
func harImportReviewSections(result *har.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return []scriptImportReviewSection{}
	}
	sections := []scriptImportReviewSection{}
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
		}
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		secondary := firstNonEmpty(req.SecondaryContent, req.ComparisonContent, req.ContentB)
		req.Options.ArtifactMode = normalizeHARArtifactMode(req.Options.ArtifactMode, secondary != "")
		return &harImportRequest{
			Data:          []byte(req.Content),
			SecondaryData: []byte(secondary),
			ProjectID:     upload.jsonProjectID(req.ProjectID),
			Opts:          req.Options,
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
	return &harImportRequest{
		Data:          upload.Data,
		SecondaryData: secondaryData,
		ProjectID:     upload.ProjectID,
		Opts: har.ConvertOptions{
			ScriptName:          firstNonEmpty(upload.Value("name"), upload.Name),
			BaseURL:             upload.Value("base_url"),
			ArtifactMode:        normalizeHARArtifactMode(upload.Value("artifact_mode"), len(secondaryData) > 0),
			RetentionDays:       parseOptionalIntDefault(upload.Value("retention_days"), 0),
			RedactSensitive:     parseOptionalBool(upload.Value("redact_sensitive")),
			IncludeStaticAssets: parseOptionalBool(upload.Value("include_static_assets")),
			SalvageTruncated:    parseOptionalBool(upload.Value("salvage_truncated")),
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
