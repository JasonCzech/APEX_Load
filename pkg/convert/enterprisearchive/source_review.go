package enterprisearchive

import "github.com/jasonczech/apexload/pkg/convert/sourceview"

// SourceReview contains the redacted source files and source-to-DSL mappings
// produced during an enterprise script archive import.
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

// reconcileSourceReviewName repoints action-kind source-review mappings at a
// renamed action so the import-review table keeps matching the final DSL. It
// matches TargetName exactly (case-sensitive): the only names that reach the
// final uniquify pass with byte-identical TargetName are http_sequence wrappers
// whose mappings are TargetKind=="transaction" (skipped here), so in practice a
// rename maps to a single action mapping.
func reconcileSourceReviewName(result *ConvertResult, oldName, newName string) {
	if result == nil || result.SourceReview == nil || oldName == newName {
		return
	}
	for i := range result.SourceReview.Mappings {
		mapping := &result.SourceReview.Mappings[i]
		if mapping.TargetKind == "action" && mapping.TargetName == oldName {
			mapping.TargetName = newName
		}
	}
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
			mode := SourceFunctionSupportCustomReview
			if def, ok := lookupSourceFunction(fn.Name); ok {
				mode = def.SupportMode
			}
			mappingStatus := status
			if mode == SourceFunctionSupportExternalRequired {
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

func redactSourceSourceContent(content string) (string, bool) {
	return sourceview.Redact(content)
}
