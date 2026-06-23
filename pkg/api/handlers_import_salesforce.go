package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type salesforceHARAugmentation struct {
	Script            *dsl.Script
	Correlations      any
	ReviewSections    []scriptImportReviewSection
	Summary           map[string]any
	Source            string
	Detected          bool
	ReviewForResponse *salesforce.Review
}

func (s *Server) handleImportSalesforce(w http.ResponseWriter, r *http.Request) {
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

	augmented := augmentHARImportWithSalesforce(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "salesforce_har", true, agentforceTransformRequested(r), false)

	// Validate AFTER the MuleSoft/Agentforce attach calls: agentforce.Transform
	// mutates the script in place (injects an OAuth init, rewrites headers, appends
	// a teardown), so validating earlier would compute warnings against a script
	// that no longer matches what is returned.
	if issues := validateScript(augmented.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	preApplied := sampleCorrelationsToPreApplied(result.Correlations)
	if augmented.Script != result.Script {
		preApplied = nil
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              augmented.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Correlations:        augmented.Correlations,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      augmented.ReviewSections,
		SourceReview:        result.SourceReview,
		Summary:             augmented.Summary,
		Parameterization:    buildImportParameterization(augmented.Script, preApplied),
	}))
}

func augmentHARImportWithSalesforce(result *har.ConvertResult, data []byte, projectID, sourceName, source string, buildNative, agentforce, requireDetected bool) salesforceHARAugmentation {
	augmentation := salesforceHARAugmentation{
		Script:         result.Script,
		Correlations:   any(result.Correlations),
		ReviewSections: harImportReviewSections(result),
		Summary:        harImportSummary(result),
		Source:         source,
	}
	review, err := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{
		BuildNative: buildNative,
		HARData:     data,
	})
	if err != nil {
		if !requireDetected {
			result.Warnings = append(result.Warnings, "Salesforce review failed: "+err.Error())
		}
		return augmentation
	}
	if requireDetected && (review == nil || !review.Detected) {
		return augmentation
	}

	augmentation.Detected = review != nil && review.Detected
	reviewForResponse := salesforceReviewForResponse(review)
	augmentation.ReviewForResponse = reviewForResponse

	if buildNative && review != nil && review.NativePreview != nil && review.NativePreview.Script != nil {
		augmentation.Script = review.NativePreview.Script
		augmentation.Source = "salesforce_har"
		augmentation.Correlations = review.NativePreview.Correlations
		if augmentation.Script != nil {
			augmentation.Script.ProjectID = normalizeProjectID(projectID)
		}
		augmentation.Summary["generation_mode"] = "salesforce_native_http"
		augmentation.Summary["protocol_scope"] = "salesforce_http_family"
	}
	if reviewForResponse != nil {
		augmentation.Summary["salesforce_review"] = reviewForResponse
	}
	if source == "salesforce_har" && buildNative {
		augmentation.Summary["generation_mode"] = "salesforce_native_http"
		augmentation.Summary["protocol_scope"] = "salesforce_http_family"
	}
	if augmentation.Script != nil {
		attachSourceReviewToScript(augmentation.Script, augmentation.Source, sourceName, result.SourceReview)
		if reviewForResponse != nil {
			ensureScriptImportPreserved(augmentation.Script, augmentation.Source)["salesforce_review"] = reviewForResponse
		}
	}
	if review != nil {
		severity := "info"
		if !review.Detected {
			severity = "warning"
		}
		augmentation.ReviewSections = append(augmentation.ReviewSections, scriptImportReviewSection{
			Key:      "salesforce_review",
			Title:    "Salesforce Native Review",
			Severity: severity,
			Summary:  salesforceReviewSummary(review),
			Items:    salesforceReviewItems(review),
		})
	}
	augmentation.ReviewSections = attachMuleSoftReview(augmentation.Script, augmentation.Source, augmentation.ReviewSections)
	augmentation.ReviewSections = attachAgentforceReview(augmentation.Script, augmentation.Source, augmentation.ReviewSections, agentforce)
	return augmentation
}

func salesforceReviewForResponse(review *salesforce.Review) *salesforce.Review {
	if review == nil {
		return nil
	}
	clone := *review
	if review.NativePreview != nil {
		nativePreview := *review.NativePreview
		nativePreview.Script = nil
		clone.NativePreview = &nativePreview
	}
	return &clone
}
