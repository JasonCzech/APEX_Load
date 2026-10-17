package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func OriginalImportSourceReview(review *sourceview.Review) *sourceview.Review {
	if review == nil {
		return nil
	}
	clone := *review
	clone.TargetDomain = sourceview.OriginalImportTargetDomain
	return &clone
}

func TrustedNativeSalesforceReview(script *dsl.Script) *salesforce.Review {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return nil
	}
	review, _ := script.Metadata.Import.Preserved["salesforce_review"].(*salesforce.Review)
	if review == nil || review.NativePreview == nil {
		return nil
	}
	return review
}

func FinalizeSalesforceNativeReferences(review *salesforce.Review, script *dsl.Script, source any) {
	if review == nil || review.NativePreview == nil || script == nil {
		return
	}
	salesforce.BindNativePreviewReferences(review.NativePreview, SourceReviewValue(source), script, salesforce.FinalNativePathDomain)
	EnsureScriptImportPreserved(script, "salesforce")["salesforce_review"] = review
}
