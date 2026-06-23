package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
)

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

	review, err := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{
		BuildNative: true,
		HARData:     importReq.Data,
	})
	if err != nil {
		result.Warnings = append(result.Warnings, "Salesforce review failed: "+err.Error())
	}
	reviewForResponse := salesforceReviewForResponse(review)

	script := result.Script
	correlations := any(result.Correlations)
	if review != nil && review.NativePreview != nil && review.NativePreview.Script != nil {
		script = review.NativePreview.Script
		script.ProjectID = normalizeProjectID(importReq.ProjectID)
		correlations = review.NativePreview.Correlations
	}
	if script != nil {
		attachSourceReviewToScript(script, "salesforce_har", importReq.Opts.ScriptName, result.SourceReview)
		preserved := ensureScriptImportPreserved(script, "salesforce_har")
		if reviewForResponse != nil {
			preserved["salesforce_review"] = reviewForResponse
		}
	}

	reviewSections := harImportReviewSections(result)
	if review != nil {
		severity := "info"
		if !review.Detected {
			severity = "warning"
		}
		reviewSections = append(reviewSections, scriptImportReviewSection{
			Key:      "salesforce_review",
			Title:    "Salesforce Native Review",
			Severity: severity,
			Summary:  salesforceReviewSummary(review),
			Items:    salesforceReviewItems(review),
		})
	}
	reviewSections = attachMuleSoftReview(script, "salesforce_har", reviewSections)
	reviewSections = attachAgentforceReview(script, "salesforce_har", reviewSections, agentforceTransformRequested(r))

	// Validate AFTER the MuleSoft/Agentforce attach calls: agentforce.Transform
	// mutates the script in place (injects an OAuth init, rewrites headers, appends
	// a teardown), so validating earlier would compute warnings against a script
	// that no longer matches what is returned.
	if issues := validateScript(script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	summary := harImportSummary(result)
	summary["generation_mode"] = "salesforce_native_http"
	summary["protocol_scope"] = "salesforce_http_family"
	if reviewForResponse != nil {
		summary["salesforce_review"] = reviewForResponse
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Correlations:        correlations,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      reviewSections,
		SourceReview:        result.SourceReview,
		Summary:             summary,
		Parameterization:    buildImportParameterization(script, sampleCorrelationsToPreApplied(result.Correlations)),
	}))
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
