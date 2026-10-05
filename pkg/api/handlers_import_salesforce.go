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
	importReq.noteSalvage(result)
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	agentforceTransform := agentforceTransformRequested(r)
	salesforceAugmentation := augmentHARImportWithSalesforce(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "salesforce_har", true, agentforceTransform, false)

	draft := importDraft{
		Source:           salesforceAugmentation.Source,
		SourceScriptName: importReq.Opts.ScriptName,
		ProjectID:        importReq.ProjectID,
		Script:           salesforceAugmentation.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         salesforceAugmentation.ReviewSections,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.augmentWithAgentforceTransform(agentforceTransform)
	result.Warnings = augmented.Warnings

	preApplied := sampleCorrelationsToPreApplied(result.Correlations)
	if salesforceAugmentation.Script != result.Script {
		preApplied = nil
	}

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                result,
		Script:                salesforceAugmentation.Script,
		Warnings:              result.Warnings,
		Stats:                 result.Stats,
		Correlations:          salesforceAugmentation.Correlations,
		Diagnostics:           result.Unsupported,
		UnsupportedElements:   result.Unsupported.Strings(),
		ReviewSections:        augmented.Sections,
		SourceReview:          result.SourceReview,
		Summary:               salesforceAugmentation.Summary,
		Parameterization:      buildImportParameterization(salesforceAugmentation.Script, preApplied),
		Preflight:             augmented.Preflight,
		ProtectedRequirements: protectedRequirements,
	})
}

func augmentHARImportWithSalesforce(result *har.ConvertResult, data []byte, projectID, sourceName, source string, buildNative, agentforce, requireDetected bool) salesforceHARAugmentation {
	augmentation := salesforceHARAugmentation{
		Script:         result.Script,
		Correlations:   any(result.Correlations),
		ReviewSections: harImportReviewSections(result),
		Summary:        harImportSummary(result),
		Source:         source,
	}
	// Detect first with BuildNative:false so the expensive native preview (a full
	// script clone + per-action rewrite in buildNativePreview) is never built for
	// non-Salesforce HARs. handleImportHAR runs this for every HAR import, so
	// detection must stay off the native-build hot path.
	review, err := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{
		BuildNative: false,
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

	// Only build the native Salesforce preview once Salesforce traffic is confirmed.
	if buildNative && review != nil && review.Detected {
		nativeReview, nativeErr := salesforce.Analyze(result.Script, salesforce.AnalyzeOptions{
			BuildNative: true,
			HARData:     data,
		})
		if nativeErr != nil {
			result.Warnings = append(result.Warnings, "Salesforce native preview failed: "+nativeErr.Error())
		} else if nativeReview != nil {
			review = nativeReview
		}
	}

	augmentation.Detected = review != nil && review.Detected
	reviewForResponse := salesforceReviewForResponse(review)
	augmentation.ReviewForResponse = reviewForResponse

	nativeApplied := false
	if buildNative && review != nil && review.NativePreview != nil && review.NativePreview.Script != nil {
		augmentation.Script = review.NativePreview.Script
		augmentation.Source = "salesforce_har"
		augmentation.Correlations = review.NativePreview.Correlations
		if augmentation.Script != nil {
			augmentation.Script.ProjectID = normalizeProjectID(projectID)
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
		result.SourceReview = originalImportSourceReview(result.SourceReview)
		augmentation.Summary["generation_mode"] = "salesforce_native_http"
		augmentation.Summary["protocol_scope"] = "salesforce_http_family"
	}
	if reviewForResponse != nil {
		augmentation.Summary["salesforce_review"] = reviewForResponse
	}
	if augmentation.Script != nil && reviewForResponse != nil {
		ensureScriptImportPreserved(augmentation.Script, augmentation.Source)["salesforce_review"] = reviewForResponse
	}
	if review != nil {
		severity := "info"
		if len(review.Blockers) > 0 {
			severity = "blocker"
		} else if !review.Detected || len(review.Warnings) > 0 {
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
	// The MuleSoft and Agentforce detectors, the source review, validation, and
	// the replay preflight are the shared draft pass's job. This function's
	// contract is narrower: decide which script the import ships (the HAR draft
	// or its native Salesforce rewrite) and under which source profile.
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
