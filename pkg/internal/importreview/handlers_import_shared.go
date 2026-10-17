package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"sort"
	"strings"
)

func EnsureScriptImportPreserved(script *dsl.Script, source string) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: source}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	return script.Metadata.Import.Preserved
}

func AttachImportReplayPreflight(script *dsl.Script, source string, preflight *ReplayPreflight) {
	if script == nil || preflight == nil {
		return
	}
	preserved := EnsureScriptImportPreserved(script, source)
	preserved["replay_preflight"] = CloneReplayPreflight(preflight)
}

func ParseBoolDefault(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func StringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return UniqueSortedStrings(typed)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return UniqueSortedStrings(result)
	default:
		return nil
	}
}

func UniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func StringValue(value any) string {
	Text, _ := value.(string)
	return Text
}

func AttachSourceReviewToScripts(scripts []*dsl.Script, source string, SourceScriptName string, review any) {
	if IsNilImportValue(review) {
		return
	}
	for _, script := range scripts {
		AttachSourceReviewToScript(script, source, SourceScriptName, review)
	}
}

func AttachSourceReviewToScript(script *dsl.Script, source string, SourceScriptName string, review any) {
	if script == nil || IsNilImportValue(review) {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Source == "" {
		script.Metadata.Import.Source = source
	}
	if script.Metadata.Import.SourceScriptName == "" {
		script.Metadata.Import.SourceScriptName = SourceScriptName
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved["source_review"] = review
}
