package enterprisearchive

import "github.com/jasonczech/apexload/pkg/scriptdisclosure"

// sanitizeEnterpriseResultReviewText applies exact-value equivalence from the
// retained executable/protected script to detached review surfaces. Source
// scrubbing by semantic field name remains the first line of defense, but a
// JDBC password or rejected JVM literal can also be repeated in a neutral
// comment, filename-derived label, warning, or mapping. Those copies are
// disclosure-only and must not survive merely because their local key is not
// sensitive.
func sanitizeEnterpriseResultReviewText(result *ConvertResult) {
	if result == nil {
		return
	}
	scripts := result.effectiveScripts()
	if len(scripts) == 0 {
		return
	}
	refs := make([]*string, 0)
	appendEnterpriseStringRefs(&refs, result.Warnings)
	appendEnterpriseStringRefs(&refs, result.Stats.UnsupportedFuncs)
	appendEnterpriseStringRefs(&refs, result.RecordedResourceURLs)

	if result.SourceReview != nil {
		for index := range result.SourceReview.SourceFiles {
			refs = append(
				refs,
				&result.SourceReview.SourceFiles[index].Path,
				&result.SourceReview.SourceFiles[index].Language,
				&result.SourceReview.SourceFiles[index].Content,
			)
		}
		appendEnterpriseStringRefs(
			&refs,
			result.SourceReview.OmittedSourceFiles,
		)
		for index := range result.SourceReview.Mappings {
			mapping := &result.SourceReview.Mappings[index]
			refs = append(
				refs,
				&mapping.ID,
				&mapping.SourceFile,
				&mapping.Function,
				&mapping.TargetPath,
				&mapping.TargetName,
				&mapping.TargetKind,
				&mapping.Status,
				&mapping.SupportMode,
				&mapping.Notes,
			)
		}
	}
	if result.DetectedProtocol != nil {
		appendEnterpriseDetectedProtocolRefs(&refs, result.DetectedProtocol)
	}
	if result.ReviewArtifact != nil {
		artifact := result.ReviewArtifact
		refs = append(
			refs,
			&artifact.ID,
			&artifact.Title,
			&artifact.Status,
			&artifact.Summary,
		)
		appendEnterpriseStringRefs(&refs, artifact.SourceFiles)
		appendEnterpriseStringRefs(&refs, artifact.PreservedFunctions)
		appendEnterpriseStringRefs(&refs, artifact.RequiredEvidence)
		appendEnterpriseStringRefs(&refs, artifact.NextActions)
		appendEnterpriseDetectedProtocolRefs(&refs, &artifact.Protocol)
	}
	if result.FidelityReport != nil {
		report := result.FidelityReport
		refs = append(refs, &report.PromotionStatus)
		appendEnterpriseStringRefs(&refs, report.Blockers)
		appendEnterpriseStringRefs(&refs, report.Warnings)
		for index := range report.SourceFiles {
			source := &report.SourceFiles[index]
			refs = append(
				refs,
				&source.Path,
				&source.Phase,
				&source.Language,
				&source.Role,
			)
		}
	}
	if len(refs) == 0 {
		return
	}
	raw := make([]string, len(refs))
	for index, ref := range refs {
		raw[index] = *ref
	}
	projected := scriptdisclosure.ProjectLabels(raw, scripts...)
	for index, ref := range refs {
		*ref = projected[index]
	}
}

func appendEnterpriseDetectedProtocolRefs(
	refs *[]*string,
	detected *DetectedProtocol,
) {
	if detected == nil {
		return
	}
	*refs = append(
		*refs,
		&detected.CanonicalSourceProtocol,
		&detected.APEXLoadProtocol,
		&detected.Bundle,
		&detected.DisplayName,
		&detected.SourceType,
		&detected.RunnerRequirement,
	)
	appendEnterpriseStringRefs(refs, detected.Signals)
	appendEnterpriseStringRefs(refs, detected.Conflicts)
	appendEnterpriseStringRefs(refs, detected.Warnings)
	appendEnterpriseStringRefs(refs, detected.RequiredEvidence)
}

func appendEnterpriseStringRefs(refs *[]*string, values []string) {
	for index := range values {
		*refs = append(*refs, &values[index])
	}
}
