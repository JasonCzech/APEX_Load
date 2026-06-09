package api

import "reflect"

type scriptImportResponseOptions struct {
	Result              any
	Scripts             any
	Script              any
	Warnings            []string
	Stats               any
	Correlations        any
	Assumptions         []string
	UnsupportedElements []string
	ReviewSections      []scriptImportReviewSection
	RepairSuggestions   []scriptImportRepairSuggestion
	ScenarioDraft       any
	Summary             any
	DetectedProtocol    any
	ReviewArtifact      any
	SourceReview        any
	TrueClient2Compat   any
}

type scriptImportReviewSection struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Items    []string `json:"items"`
}

type scriptImportRepairSuggestion struct {
	ID                     string   `json:"id"`
	Category               string   `json:"category"`
	Severity               string   `json:"severity"`
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Action                 string   `json:"action"`
	TargetAction           string   `json:"target_action,omitempty"`
	SelectorStabilityScore int      `json:"selector_stability_score,omitempty"`
	SelectorSignals        []string `json:"selector_signals,omitempty"`
}

func newScriptImportResponse(opts scriptImportResponseOptions) map[string]any {
	scripts := opts.Scripts
	if isNilImportValue(scripts) {
		if !isNilImportValue(opts.Script) {
			scripts = []any{opts.Script}
		} else {
			scripts = []any{}
		}
	}

	warnings := opts.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	assumptions := opts.Assumptions
	if assumptions == nil {
		assumptions = []string{}
	}
	unsupported := opts.UnsupportedElements
	if unsupported == nil {
		unsupported = []string{}
	}
	reviewSections := opts.ReviewSections
	if reviewSections == nil {
		reviewSections = []scriptImportReviewSection{}
	}
	repairSuggestions := opts.RepairSuggestions
	if repairSuggestions == nil {
		repairSuggestions = []scriptImportRepairSuggestion{}
	}
	correlations := opts.Correlations
	if correlations == nil {
		correlations = []any{}
	}
	stats := opts.Stats
	if stats == nil {
		stats = map[string]any{}
	}
	summary := opts.Summary
	if summary == nil {
		summary = map[string]any{}
	}

	response := map[string]any{
		"scripts":                   scripts,
		"script":                    opts.Script,
		"warnings":                  warnings,
		"stats":                     stats,
		"correlations":              correlations,
		"assumptions":               assumptions,
		"unsupported_elements":      unsupported,
		"review_sections":           reviewSections,
		"repair_suggestions":        repairSuggestions,
		"scenario_draft":            opts.ScenarioDraft,
		"summary":                   summary,
		"detected_protocol":         opts.DetectedProtocol,
		"review_artifact":           opts.ReviewArtifact,
		"source_review":             opts.SourceReview,
		"trueclient2_compat_report": opts.TrueClient2Compat,
	}
	if opts.Result != nil {
		response["result"] = opts.Result
	}

	return response
}

func isNilImportValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func newRecorderImportStats(actionCount int, correlationCount int) map[string]int {
	return map[string]int{
		"converted_actions":     actionCount,
		"correlations_detected": correlationCount,
	}
}
