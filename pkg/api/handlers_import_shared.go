package api

// handlers_import_shared — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func ensureScriptImportPreserved(script *dsl.Script, source string) map[string]any {
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

func attachImportReplayPreflight(script *dsl.Script, source string, preflight *ReplayPreflight) {
	if script == nil || preflight == nil {
		return
	}
	preserved := ensureScriptImportPreserved(script, source)
	preserved["replay_preflight"] = cloneReplayPreflight(preflight)
}

func (s *Server) handleAuthoringCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authoringCapabilityResponse())
}

func parseBoolDefault(value string, defaultValue bool) bool {
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

func stringMapAnyValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func truncateReviewText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return strings.TrimSpace(value[:limit-3]) + "..."
}

func stableReviewKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return uniqueSortedStrings(typed)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return uniqueSortedStrings(result)
	default:
		return nil
	}
}

func uniqueSortedStrings(values []string) []string {
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

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func attachSourceReviewToScripts(scripts []*dsl.Script, source string, sourceScriptName string, review any) {
	if isNilImportValue(review) {
		return
	}
	for _, script := range scripts {
		attachSourceReviewToScript(script, source, sourceScriptName, review)
	}
}

func attachSourceReviewToScript(script *dsl.Script, source string, sourceScriptName string, review any) {
	if script == nil || isNilImportValue(review) {
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
		script.Metadata.Import.SourceScriptName = sourceScriptName
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved["source_review"] = review
}

func persistProtocolCompatReport(script *dsl.Script, source string, report *protocolCompatReport) {
	if script == nil || report == nil {
		return
	}
	preserved := ensureScriptImportPreserved(script, source)
	preserved["protocol_compat_report"] = report
}

func protocolPromotionStatus(importMode string, reviewCount int, warningCount int) string {
	switch importMode {
	case string(enterprisearchive.ImportModeExternalRunnerRequired):
		return "external_runner_required"
	case string(enterprisearchive.ImportModeUnsupported):
		return "unsupported"
	case string(enterprisearchive.ImportModeReviewOnly):
		return "review_required"
	}
	if reviewCount > 0 {
		return "review_required"
	}
	if warningCount > 0 {
		return "ready_with_warnings"
	}
	return "ready_for_validation"
}

func protocolNextActions(status string, validationAction string) []string {
	switch status {
	case "external_runner_required":
		return []string{"Preserve source artifact and attach approved external runner evidence before promotion."}
	case "unsupported":
		return []string{"Confirm protocol support strategy or defer this import as unsupported."}
	case "review_required":
		return []string{"Resolve review-gated fragments and unsupported constructs.", validationAction}
	case "ready_with_warnings":
		return []string{"Review converter warnings.", validationAction}
	default:
		return []string{validationAction}
	}
}

func protocolCorrelationCoverage(extractors int, requests int) string {
	if requests <= 0 {
		return "not_applicable"
	}
	if extractors <= 0 {
		return "none_detected"
	}
	return fmt.Sprintf("%d extractor(s) across %d request action(s)", extractors, requests)
}

func protocolRuntimeParityFromScript(script *dsl.Script) map[string]int {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	switch typed := script.Metadata.Import.Preserved["runtime_parity"].(type) {
	case map[string]string:
		counts := map[string]int{}
		for _, status := range typed {
			counts[status]++
		}
		return counts
	case map[string]any:
		counts := map[string]int{}
		for _, status := range typed {
			if text, ok := status.(string); ok {
				counts[text]++
			}
		}
		return counts
	default:
		return nil
	}
}

func protocolDataSourceCount(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return len(script.DataSources)
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	result := replacer.Replace(name)
	if result == "" {
		result = "script"
	}
	return result
}
