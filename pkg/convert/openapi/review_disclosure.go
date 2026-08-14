package openapi

import "github.com/jasonczech/apexload/pkg/scriptdisclosure"

// sanitizeOpenAPIResultReviewText removes imported credential literals from
// descriptive conversion-result surfaces without changing the executable
// destination script. A value can legitimately be reused as an operation ID,
// title, warning fragment, or source-mapping label, so semantic-name scrubbing
// alone is not sufficient once that same value was proven to be a credential
// in the generated request.
func sanitizeOpenAPIResultReviewText(result *ConvertResult) {
	if result == nil || result.Script == nil {
		return
	}
	refs := make([]*string, 0)
	for index := range result.Warnings {
		refs = append(refs, &result.Warnings[index])
	}
	for index := range result.Unsupported {
		refs = append(
			refs,
			&result.Unsupported[index].Element,
			&result.Unsupported[index].Detail,
			&result.Unsupported[index].Location,
		)
	}
	for index := range result.Correlations {
		refs = append(
			refs,
			&result.Correlations[index].Name,
			&result.Correlations[index].Source,
			&result.Correlations[index].Expression,
			&result.Correlations[index].ExtractType,
			&result.Correlations[index].SampleValue,
		)
	}
	refs = append(
		refs,
		&result.Stats.Format,
		&result.Stats.OpenAPIVersion,
		&result.Summary.Title,
		&result.Summary.Version,
		&result.Summary.BaseURL,
	)
	appendOpenAPIStringRefs(&refs, result.Stats.AuthSchemesDetected)
	appendOpenAPIStringRefs(&refs, result.Stats.SkippedOperations)
	appendOpenAPIStringRefs(&refs, result.Summary.Servers)
	appendOpenAPIStringRefs(&refs, result.Summary.AuthSchemes)
	appendOpenAPIStringRefs(&refs, result.Summary.DataColumns)
	appendOpenAPIStringRefs(&refs, result.Summary.OperationNames)
	appendOpenAPIStringRefs(&refs, result.Summary.OperationAuth)

	if result.SourceReview != nil {
		for index := range result.SourceReview.SourceFiles {
			// Source content is redacted by the schema-aware source-review pass
			// before this generic label projection. Treating a short credential
			// example as a bare substring here would erase unrelated paths,
			// operation IDs, and prose from the operator's review copy.
			refs = append(
				refs,
				&result.SourceReview.SourceFiles[index].Path,
				&result.SourceReview.SourceFiles[index].Language,
			)
		}
		appendOpenAPIStringRefs(&refs, result.SourceReview.OmittedSourceFiles)
		for index := range result.SourceReview.Mappings {
			refs = append(
				refs,
				&result.SourceReview.Mappings[index].ID,
				&result.SourceReview.Mappings[index].SourceFile,
				&result.SourceReview.Mappings[index].Function,
				&result.SourceReview.Mappings[index].TargetPath,
				&result.SourceReview.Mappings[index].TargetName,
				&result.SourceReview.Mappings[index].TargetKind,
				&result.SourceReview.Mappings[index].Status,
				&result.SourceReview.Mappings[index].SupportMode,
				&result.SourceReview.Mappings[index].Notes,
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
	projected := scriptdisclosure.ProjectLabels(raw, result.Script)
	for index, ref := range refs {
		*ref = projected[index]
	}
}

func appendOpenAPIStringRefs(refs *[]*string, values []string) {
	for index := range values {
		*refs = append(*refs, &values[index])
	}
}
