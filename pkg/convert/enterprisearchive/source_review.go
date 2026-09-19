package enterprisearchive

import (
	"fmt"
	"path"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// SourceReview contains imported source files and source-to-DSL mappings
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
	// Prefer a positional match: find the single action uniquify renamed to
	// newName and repoint only the mapping at that action's path. Matching purely
	// by TargetName clobbers the kept action's mapping when two actions shared
	// oldName (e.g. two byte-identical utility-action names), routing both source
	// lines to the renamed _N action and leaving the kept action unmapped.
	if path, ok := actionPathByName(result.Script, newName); ok {
		for i := range result.SourceReview.Mappings {
			mapping := &result.SourceReview.Mappings[i]
			if mapping.TargetKind == "action" && mapping.TargetName == oldName && mapping.TargetPath == path {
				mapping.TargetName = newName
				return
			}
		}
	}
	// Fallback (no positional match, e.g. a nested/non-top-level rename): update
	// the first action mapping still carrying oldName. newName is unique after
	// uniquify, so this preserves the historical single-mapping behavior.
	for i := range result.SourceReview.Mappings {
		mapping := &result.SourceReview.Mappings[i]
		if mapping.TargetKind == "action" && mapping.TargetName == oldName {
			mapping.TargetName = newName
			return
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

// Record provenance after merging, so alternate names and final indices point
// at the actual destination rather than a file-local pre-merge index.
func appendParameterSourceMappings(result *ConvertResult, script *dsl.Script, filename, content string, files map[string]string, parameterStart, dataSourceStart int) {
	sourceFile, _, found := findFileEntry(files, filename)
	if !found {
		sourceFile = filename
	}
	lines := strings.Split(content, "\n")
	for index := parameterStart; index < len(script.Parameters); index++ {
		parameter := script.Parameters[index]
		start, end := 1, len(lines)
		for line, text := range lines {
			if strings.EqualFold(strings.TrimSpace(text), "[parameter:"+parameter.Name+"]") {
				start = line + 1
				for next := line + 1; next < len(lines); next++ {
					if strings.HasPrefix(strings.TrimSpace(lines[next]), "[") {
						end = next
						break
					}
				}
				break
			}
		}
		appendSourceReviewMapping(result, SourceReviewMapping{SourceFile: sourceFile, StartLine: start, EndLine: end, TargetPath: fmt.Sprintf("parameters[%d]", index), TargetName: parameter.Name, TargetKind: "parameter", Status: "converted", Notes: "Parameter definition imported from this source file; destination indices include merged definitions."})
	}
	for index := dataSourceStart; index < len(script.DataSources); index++ {
		source := script.DataSources[index]
		file, data := sourceFile, content
		matches := 0
		for candidate, candidateData := range files {
			if source.FileName != "" && strings.EqualFold(path.Base(candidate), source.FileName) {
				matches++
				file, data = candidate, candidateData
			}
		}
		if matches != 1 {
			file, data = sourceFile, content
		}
		appendSourceReviewMapping(result, SourceReviewMapping{SourceFile: file, StartLine: 1, EndLine: len(strings.Split(data, "\n")), TargetPath: fmt.Sprintf("data_sources[%d]", index), TargetName: source.Name, TargetKind: "data_source", Status: "converted", Notes: "Data source generated from the archive parameter data. Parameter settings specify column selection and allocation."})
	}
}
