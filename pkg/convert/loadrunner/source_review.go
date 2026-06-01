package loadrunner

import "github.com/jasonczech/apexload/pkg/convert/sourceview"

// SourceReview contains the redacted source files and source-to-DSL mappings
// produced during a LoadRunner import. The type aliases keep the public
// LoadRunner API stable while sharing the generic import review model.
type SourceReview = sourceview.Review
type SourceReviewFile = sourceview.SourceFile
type SourceReviewMapping = sourceview.Mapping

func BuildSourceReview(files map[string]string, mappings []SourceReviewMapping) *SourceReview {
	return sourceview.Build(files, mappings)
}

func sourceReviewMappings(result *ConvertResult) []SourceReviewMapping {
	if result == nil || result.SourceReview == nil {
		return nil
	}
	return append([]SourceReviewMapping(nil), result.SourceReview.Mappings...)
}

func appendSourceReviewMapping(result *ConvertResult, mapping SourceReviewMapping) {
	if result == nil {
		return
	}
	mapping = normalizeSourceReviewMapping(mapping)
	if mapping.SourceFile == "" && mapping.TargetPath == "" && mapping.Function == "" {
		return
	}
	if result.SourceReview == nil {
		result.SourceReview = &SourceReview{}
	}
	result.SourceReview.Mappings = append(result.SourceReview.Mappings, mapping)
}

func sourceReviewMappingForFunction(fn ParsedFunction, targetPath string, targetName string, targetKind string, status string, notes string) SourceReviewMapping {
	mapping := SourceReviewMapping{
		SourceFile: fn.Filename,
		StartLine:  fn.LineNumber,
		EndLine:    fn.EndLine,
		Function:   fn.Name,
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
	return normalizeSourceReviewMapping(mapping)
}

func reviewOnlySourceReviewMappings(actions []*ParsedAction, status string, notes string) []SourceReviewMapping {
	mappings := make([]SourceReviewMapping, 0)
	for _, action := range actions {
		if action == nil {
			continue
		}
		for _, fn := range action.Functions {
			mode := LoadRunnerSupportCustomReview
			if def, ok := lookupLoadRunnerFunction(fn.Name); ok {
				mode = def.SupportMode
			}
			mappingStatus := status
			if mode == LoadRunnerSupportExternalRequired {
				mappingStatus = "external_required"
			}
			mappings = append(mappings, sourceReviewMappingWithSupportMode(fn, "", "", "review", mappingStatus, notes, mode))
		}
	}
	return mappings
}

func normalizeSourceReviewMappings(mappings []SourceReviewMapping) []SourceReviewMapping {
	return sourceview.NormalizeMappings(mappings)
}

func normalizeSourceReviewMapping(mapping SourceReviewMapping) SourceReviewMapping {
	return sourceview.NormalizeMapping(mapping)
}

func sourceReviewLanguage(name string) string {
	return sourceview.Language(name)
}

func redactLoadRunnerSourceContent(content string) (string, bool) {
	return sourceview.Redact(content)
}
