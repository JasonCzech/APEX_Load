package importreview

import (
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

func AugmentHARImportWithSalesforce(result *har.ConvertResult, data []byte, projectID, sourceName, source string, buildNative, agentforce, requireDetected bool) SalesforceHARAugmentation {
	return augmentHARImportWithSalesforce(result, data, projectID, sourceName, source, buildNative, agentforce, requireDetected, salesforce.Analyze)
}

// Keep the analyzer callback local to one augmentation; concurrent imports do
// not depend on mutable package-level analysis state.
func augmentHARImportWithSalesforce(result *har.ConvertResult, data []byte, projectID, sourceName, source string, buildNative, agentforce, requireDetected bool, analyze func(*dsl.Script, salesforce.AnalyzeOptions) (*salesforce.Review, error)) SalesforceHARAugmentation {
	augmentation := SalesforceHARAugmentation{
		Script:         result.Script,
		Correlations:   any(result.Correlations),
		ReviewSections: HarImportReviewSections(result),
		Summary:        HarImportSummary(result),
		Source:         source,
	}
	// Optional review callers stop before matching or correlation when detection
	// is negative. A detected capture is analyzed only once, including its native
	// preview; native preparation failures retain the completed review.
	review, err := analyze(result.Script, salesforce.AnalyzeOptions{
		HARData:               data,
		Document:              result.Document,
		RequireDetected:       requireDetected,
		SkipUndetectedReview:  requireDetected,
		BuildNativeIfDetected: buildNative,
		Source:                source,
	})
	if err != nil {
		if review != nil {
			result.Warnings = append(result.Warnings, "Salesforce native preview failed: "+err.Error())
		} else {
			if !requireDetected {
				result.Warnings = append(result.Warnings, "Salesforce review failed: "+err.Error())
			}
			return augmentation
		}
	}
	if requireDetected && (review == nil || !review.Detected) {
		return augmentation
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
