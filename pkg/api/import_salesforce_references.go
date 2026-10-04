package api

import (
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func originalImportSourceReview(review *sourceview.Review) *sourceview.Review {
	if review == nil {
		return nil
	}
	clone := *review
	clone.TargetDomain = sourceview.OriginalImportTargetDomain
	return &clone
}

func trustedNativeSalesforceReview(script *dsl.Script) *salesforce.Review {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return nil
	}
	review, _ := script.Metadata.Import.Preserved["salesforce_review"].(*salesforce.Review)
	if review == nil || review.NativePreview == nil {
		return nil
	}
	return review
}

func finalizeSalesforceNativeReferences(review *salesforce.Review, script *dsl.Script, source any) {
	if review == nil || review.NativePreview == nil || script == nil {
		return
	}
	salesforce.BindNativePreviewReferences(review.NativePreview, sourceReviewValue(source), script, salesforce.FinalNativePathDomain)
	ensureScriptImportPreserved(script, "salesforce")["salesforce_review"] = review
}
