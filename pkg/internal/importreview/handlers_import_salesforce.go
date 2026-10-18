package importreview

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type SalesforceHARAugmentation struct {
	Script            *dsl.Script
	Correlations      any
	ReviewSections    []ScriptImportReviewSection
	Summary           map[string]any
	Source            string
	Detected          bool
	ReviewForResponse *salesforce.Review
}

// analyzeSalesforce is salesforce.Analyze; tests replace it to count analyses.
var analyzeSalesforce = salesforce.Analyze

func AugmentHARImportWithSalesforce(result *har.ConvertResult, data []byte, projectID, sourceName, source string, buildNative, agentforce, requireDetected bool) SalesforceHARAugmentation {
	augmentation := SalesforceHARAugmentation{
		Script:         result.Script,
		Correlations:   any(result.Correlations),
		ReviewSections: HarImportReviewSections(result),
		Summary:        HarImportSummary(result),
		Source:         source,
	}
	// The native preview (a full script clone + per-action rewrite in
	// buildNativePreview) must never be built for non-Salesforce HARs, and
	// handleImportHAR runs this for every HAR import. With requireDetected,
	// Analyze returns an undetected review before its correlation pass and native
	// build, so one call detects and builds; a second call would repeat the
	// correlation pass, which dominates a large Salesforce import. Without it,
	// detect first with BuildNative:false.
	nativeWithDetection := buildNative && requireDetected
	review, err := analyzeSalesforce(result.Script, salesforce.AnalyzeOptions{
		BuildNative:     nativeWithDetection,
		HARData:         data,
		RequireDetected: requireDetected,
	})
	var nativeErr error
	var previewErr *salesforce.NativePreviewError
	if errors.As(err, &previewErr) {
		// The review succeeded but its native preview did not: keep the review
		// without the native build and report the failure, as the two-call path
		// does.
		nativeErr = err
		review, err = analyzeSalesforce(result.Script, salesforce.AnalyzeOptions{
			HARData:         data,
			RequireDetected: requireDetected,
		})
	}
	if err != nil {
		if !requireDetected {
			result.Warnings = append(result.Warnings, "Salesforce review failed: "+err.Error())
		}
		return augmentation
	}
	if requireDetected && (review == nil || !review.Detected) {
		return augmentation
	}

	// Only build the native Salesforce preview once Salesforce traffic is confirmed.
	if buildNative && !nativeWithDetection && review != nil && review.Detected {
		nativeReview, err := analyzeSalesforce(result.Script, salesforce.AnalyzeOptions{
			BuildNative: true,
			HARData:     data,
		})
		if err != nil {
			nativeErr = err
		} else if nativeReview != nil {
			review = nativeReview
		}
	}
	if nativeErr != nil {
		result.Warnings = append(result.Warnings, "Salesforce native preview failed: "+nativeErr.Error())
	}

	augmentation.Detected = review != nil && review.Detected
	reviewForResponse := SalesforceReviewForResponse(review)
	augmentation.ReviewForResponse = reviewForResponse

	nativeApplied := false
	if buildNative && review != nil && review.NativePreview != nil && review.NativePreview.Script != nil {
		augmentation.Script = review.NativePreview.Script
		augmentation.Source = "salesforce_har"
		augmentation.Correlations = review.NativePreview.Correlations
		if augmentation.Script != nil {
			augmentation.Script.ProjectID = NormalizeProjectID(projectID)
			// The native builder starts from the HAR script and therefore
			// inherits its import metadata. Once the native Salesforce rewrite
			// is selected, provenance must describe the destination behavior,
			// not the intermediate HAR adapter. This assignment is intentional
			// even when the shared HAR finalizer already populated Source.
			if augmentation.Script.Metadata == nil {
				augmentation.Script.Metadata = &dsl.ScriptMetadata{}
			}
			if augmentation.Script.Metadata.Import == nil {
				augmentation.Script.Metadata.Import = &dsl.ImportMetadata{}
			}
			augmentation.Script.Metadata.Import.Source = "salesforce"
		}
		nativeApplied = true
	}
	// Advertise the native generation mode only when a native script was actually
	// emitted, so a detected-but-not-rebuilt import is not mislabeled as native.
	if nativeApplied {
		result.SourceReview = OriginalImportSourceReview(result.SourceReview)
		augmentation.Summary["generation_mode"] = "salesforce_native_http"
		augmentation.Summary["protocol_scope"] = "salesforce_http_family"
	}
	if reviewForResponse != nil {
		augmentation.Summary["salesforce_review"] = reviewForResponse
	}
	if augmentation.Script != nil && reviewForResponse != nil {
		EnsureScriptImportPreserved(augmentation.Script, augmentation.Source)["salesforce_review"] = reviewForResponse
	}
	if review != nil {
		severity := "info"
		if len(review.Blockers) > 0 {
			severity = "blocker"
		} else if !review.Detected || len(review.Warnings) > 0 {
			severity = "warning"
		}
		augmentation.ReviewSections = append(augmentation.ReviewSections, ScriptImportReviewSection{
			Key:      "salesforce_review",
			Title:    "Salesforce Native Review",
			Severity: severity,
			Summary:  SalesforceReviewSummary(review),
			Items:    SalesforceReviewItems(review),
		})
	}
	// The MuleSoft and Agentforce detectors, the source review, validation, and
	// the replay preflight are the shared draft pass's job. This function's
	// contract is narrower: decide which script the import ships (the HAR draft
	// or its native Salesforce rewrite) and under which source profile.
	return augmentation
}

func SalesforceReviewForResponse(review *salesforce.Review) *salesforce.Review {
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
