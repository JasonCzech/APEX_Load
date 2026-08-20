package enterprisearchive

import (
	filepath "path"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	ImportPromotionPromotable = "promotable"
	ImportPromotionBlocked    = "blocked"
)

// ImportFidelityReport summarizes what the legacy archive contained, what the
// importer converted, and what still blocks executable promotion.
type ImportFidelityReport struct {
	SourceFiles        []ImportFidelitySourceFile `json:"source_files,omitempty"`
	ExpectedFunctions  int                        `json:"expected_functions,omitempty"`
	ConvertedFunctions int                        `json:"converted_functions,omitempty"`
	ConvertedActions   int                        `json:"converted_actions,omitempty"`
	DroppedBodies      int                        `json:"dropped_bodies,omitempty"`
	SecretGated        int                        `json:"secret_gated,omitempty"`
	RuntimeParity      map[string]int             `json:"runtime_parity,omitempty"`
	ParameterParity    map[string]int             `json:"parameter_parity,omitempty"`
	PromotionStatus    string                     `json:"promotion_status,omitempty"`
	Blockers           []string                   `json:"blockers,omitempty"`
	Warnings           []string                   `json:"warnings,omitempty"`
}

type ImportFidelitySourceFile struct {
	Path     string `json:"path"`
	Phase    string `json:"phase,omitempty"`
	Language string `json:"language,omitempty"`
	Role     string `json:"role,omitempty"`
	Bytes    int    `json:"bytes,omitempty"`
}

func attachImportFidelityReport(result *ConvertResult, files map[string]string, projectConfig *ProjectConfig, actions []*ParsedAction) {
	if result == nil {
		return
	}
	result.FidelityReport = BuildImportFidelityReport(result, files, projectConfig, actions)
	if result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return
	}
	if result.Script.Metadata.Import.Preserved == nil {
		result.Script.Metadata.Import.Preserved = make(map[string]any)
	}
	result.Script.Metadata.Import.Preserved["fidelity_report"] = result.FidelityReport
}

func BuildImportFidelityReport(result *ConvertResult, files map[string]string, projectConfig *ProjectConfig, actions []*ParsedAction) *ImportFidelityReport {
	report := &ImportFidelityReport{
		SourceFiles:        buildFidelitySourceFiles(files, projectConfig),
		RuntimeParity:      map[string]int{},
		ParameterParity:    map[string]int{},
		PromotionStatus:    ImportPromotionPromotable,
		ExpectedFunctions:  0,
		ConvertedFunctions: 0,
	}
	if result != nil {
		report.ExpectedFunctions = result.Stats.TotalFunctions
		report.ConvertedFunctions = result.Stats.NativeFunctions + result.Stats.ExternalRequiredFunctions
		report.ConvertedActions = result.Stats.ConvertedActions
		report.Warnings = append([]string(nil), result.Warnings...)
		if result.DetectedProtocol != nil && result.DetectedProtocol.ImportMode != ImportModeExecutable && !javaVuserExternalRunnerPreservesSource(result) {
			report.Blockers = append(report.Blockers, "Source protocol is classified as "+string(result.DetectedProtocol.ImportMode)+" and cannot be promoted as executable.")
		}
		report.Blockers = append(report.Blockers, javaVuserLifecycleFidelityBlockers(result)...)
		if result.Script != nil {
			report.SecretGated = countSecretGatedActions(result.Script)
			if hasTrailingRequestContributors(result.Script) {
				report.Blockers = append(report.Blockers, "One or more response contributors or one-shot headers occur after the final request and cannot be bound faithfully.")
			}
			if countCSourceParseFailureGates(result.Script) > 0 {
				report.Blockers = append(report.Blockers, "One or more C lifecycle source files were missing, ambiguous, or incompletely parsed.")
			}
			report.ParameterParity = buildParameterParity(result.Script, files, projectConfig)
			if result.Script.Protocol == "jms-solace" && !result.Script.Config.JMS.Mock {
				report.Blockers = append(report.Blockers, "Real Solace JMS replay requires an approved adapter or governed sidecar runner plus runtime credentials/truststore before promotion.")
			}
			if counts := runtimeParityCounts(result.Script); len(counts) > 0 {
				report.RuntimeParity = counts
			}
		}
	}
	report.DroppedBodies = countDroppedRequestBodies(actions)
	if report.SecretGated > 0 {
		report.Blockers = append(report.Blockers, "One or more lr_unmask request bodies require governed secret resolution before executable promotion.")
	}
	if report.DroppedBodies > 0 {
		report.Blockers = append(report.Blockers, "One or more source request bodies were detected but not converted.")
	}
	if len(report.RuntimeParity) == 0 {
		report.RuntimeParity = nil
	}
	if len(report.ParameterParity) == 0 {
		report.ParameterParity = nil
	}
	report.Blockers = uniqueSortedStrings(report.Blockers)
	report.Warnings = uniqueSortedStrings(report.Warnings)
	if len(report.Blockers) > 0 {
		report.PromotionStatus = ImportPromotionBlocked
	}
	return report
}

func hasTrailingRequestContributors(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	_, ok := script.Metadata.Import.Preserved[trailingRequestContributorsMetadataKey]
	return ok
}

func countCSourceParseFailureGates(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	count := 0
	var walk func([]dsl.Action)
	walk = func(actions []dsl.Action) {
		for _, action := range actions {
			if strings.TrimSpace(stringMapValue(action.Metadata, "execution_blocker", "")) == cSourceParseFailureCode {
				count++
			}
			if action.Control != nil {
				walk(action.Control.Steps)
				walk(action.Control.Else)
			}
		}
	}
	walk(script.Init)
	walk(script.Actions)
	walk(script.Teardown)
	return count
}

func javaReviewPathKey(value string) string {
	value = filepath.Clean(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	return strings.ToLower(strings.TrimPrefix(value, "./"))
}

func javaVuserLifecycleFidelityBlockers(result *ConvertResult) []string {
	blockers := javaReviewArtifactBlockers(result, javaLifecycleBlockersKey)
	blockers = append(blockers, javaReviewArtifactBlockers(result, javaSourceIntegrityKey)...)
	return append(blockers, javaReviewArtifactBlockers(result, javaRunnerSemanticsKey)...)
}

func javaReviewArtifactBlockers(result *ConvertResult, key string) []string {
	if result == nil || result.ReviewArtifact == nil || result.ReviewArtifact.Metadata == nil {
		return nil
	}
	value, ok := result.ReviewArtifact.Metadata[key]
	if !ok {
		return nil
	}
	switch blockers := value.(type) {
	case []string:
		return append([]string(nil), blockers...)
	case []any:
		out := make([]string, 0, len(blockers))
		for _, blocker := range blockers {
			if text, ok := blocker.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// javaVuserExternalRunnerPreservesSource distinguishes execution posture from
// import fidelity. A complete Java Vuser draft intentionally delegates the
// preserved lifecycle to the governed JVM runner; needing that runner is not a
// dropped-source condition. Other external-runner protocols and all concrete
// fidelity blockers continue to block promotion normally.
func javaVuserExternalRunnerPreservesSource(result *ConvertResult) bool {
	if result == nil || result.DetectedProtocol == nil || result.Script == nil {
		return false
	}
	detected := result.DetectedProtocol
	if detected.ImportMode != ImportModeExternalRunnerRequired ||
		!strings.EqualFold(strings.TrimSpace(detected.APEXLoadProtocol), "java_vuser") ||
		!strings.EqualFold(strings.TrimSpace(detected.RunnerRequirement), javaVuserRunnerRequirement) ||
		!strings.EqualFold(strings.TrimSpace(result.Script.Protocol), "java_vuser") {
		return false
	}
	return fidelityHasJavaVuserRun(result.Script.Init) ||
		fidelityHasJavaVuserRun(result.Script.Actions) ||
		fidelityHasJavaVuserRun(result.Script.Teardown)
}

func fidelityHasJavaVuserRun(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			return true
		}
		if action.Control != nil && (fidelityHasJavaVuserRun(action.Control.Steps) || fidelityHasJavaVuserRun(action.Control.Else)) {
			return true
		}
	}
	return false
}

func buildFidelitySourceFiles(files map[string]string, projectConfig *ProjectConfig) []ImportFidelitySourceFile {
	sourceActions := map[string]SourceActionFile{}
	sourceActionsByBase := map[string][]SourceActionFile{}
	if projectConfig != nil {
		for _, sourceFile := range projectConfig.SourceActionFiles {
			key := strings.ToLower(normalizeRuntimeArchivePath(sourceFile.Name))
			sourceActions[key] = sourceFile
			base := strings.ToLower(filepath.Base(sourceFile.Name))
			sourceActionsByBase[base] = append(sourceActionsByBase[base], sourceFile)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ImportFidelitySourceFile, 0, len(names))
	for _, name := range names {
		entry := ImportFidelitySourceFile{
			Path:     name,
			Language: sourceFileLanguage(name),
			Role:     sourceFileRole(name),
			Bytes:    len(files[name]),
		}
		action, ok := sourceActions[strings.ToLower(normalizeRuntimeArchivePath(name))]
		if !ok {
			candidates := sourceActionsByBase[strings.ToLower(filepath.Base(name))]
			if len(candidates) == 1 &&
				!strings.Contains(normalizeRuntimeArchivePath(candidates[0].Name), "/") {
				action, ok = candidates[0], true
			}
		}
		if ok {
			entry.Phase = action.Phase
			entry.Language = convertutil.FirstNonEmpty(action.Language, entry.Language)
			entry.Role = "lifecycle_action"
		} else if phase := sourceActionPhase(name); strings.HasSuffix(strings.ToLower(name), ".c") || strings.HasSuffix(strings.ToLower(name), ".java") {
			entry.Phase = phase
		}
		out = append(out, entry)
	}
	return out
}

func sourceFileLanguage(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".c", ".h":
		return "c"
	case ".java":
		return "java"
	case ".xml":
		return "xml"
	case ".prm", ".dat", ".cfg", ".usr":
		return "loadrunner"
	default:
		return ""
	}
}

func sourceFileRole(name string) string {
	lower := strings.ToLower(filepath.Base(name))
	switch {
	case strings.HasSuffix(lower, ".usr"):
		return "project"
	case strings.HasSuffix(lower, ".cfg"):
		return "runtime_settings"
	case strings.HasSuffix(lower, ".prm"):
		return "parameters"
	case strings.HasSuffix(lower, ".dat"):
		return "parameter_data"
	case strings.HasSuffix(lower, ".xml"):
		return "sidecar_body"
	case strings.Contains(lower, "truststore"):
		return "truststore"
	case strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".java"):
		return "source"
	case strings.HasSuffix(lower, ".h"):
		return "source_helper"
	default:
		return "artifact"
	}
}

func countDroppedRequestBodies(actions []*ParsedAction) int {
	count := 0
	for _, action := range actions {
		if action == nil {
			continue
		}
		for _, fn := range action.Functions {
			if !isRequestFunction(fn.Name) {
				continue
			}
			sourceMentionsBody := strings.Contains(strings.ToLower(fn.RawBlock), "body=") || strings.Contains(strings.ToLower(fn.RawBlock), "lr_unmask(")
			if sourceMentionsBody && strings.TrimSpace(fn.Args[maskedBodySecretArgKey]) != "" {
				continue
			}
			if sourceMentionsBody && strings.TrimSpace(fn.Body) == "" {
				count++
			}
		}
	}
	return count
}

func countSecretGatedActions(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	count := 0
	var walk func([]dsl.Action)
	walk = func(actions []dsl.Action) {
		for _, action := range actions {
			if isActionSecretGated(action) {
				count++
			}
			if action.Control != nil {
				walk(action.Control.Steps)
				walk(action.Control.Else)
			}
		}
	}
	walk(script.Init)
	walk(script.Actions)
	walk(script.Teardown)
	return count
}

func isActionSecretGated(action dsl.Action) bool {
	if strings.Contains(action.Body, "{{secret.") {
		return true
	}
	if action.Metadata == nil {
		return false
	}
	if gated, ok := action.Metadata["body_gated_by_secret"].(bool); ok && gated {
		return true
	}
	if value, ok := action.Metadata["source_secret_ref"].(string); ok && strings.TrimSpace(value) != "" {
		return true
	}
	return false
}

func buildParameterParity(script *dsl.Script, files map[string]string, projectConfig *ProjectConfig) map[string]int {
	if script == nil {
		return nil
	}
	counts := map[string]int{
		"converted_parameters":   len(script.Parameters),
		"converted_data_sources": len(script.DataSources),
	}
	expected := countSourceParameterSections(files, projectConfig)
	if expected > 0 {
		counts["expected_parameters"] = expected
	}
	return counts
}

func countSourceParameterSections(files map[string]string, projectConfig *ProjectConfig) int {
	names := findParameterFiles(files, projectConfig)
	total := 0
	for _, name := range names {
		content, ok := findFile(files, name)
		if !ok {
			continue
		}
		sections := ParseConfigSections(content)
		for sectionName, values := range sections {
			lower := make(map[string]string, len(values))
			for key, value := range values {
				lower[strings.ToLower(strings.TrimSpace(key))] = cleanConfigValue(value)
			}
			if looksLikeParameterSection(sectionName, lower) {
				total++
			}
		}
	}
	return total
}

func runtimeParityCounts(script *dsl.Script) map[string]int {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	raw, ok := script.Metadata.Import.Preserved["runtime_settings_status_counts"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case map[string]int:
		out := make(map[string]int, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	case map[string]any:
		out := make(map[string]int, len(typed))
		for key, value := range typed {
			switch v := value.(type) {
			case int:
				out[key] = v
			case float64:
				out[key] = int(v)
			}
		}
		return out
	default:
		return nil
	}
}
