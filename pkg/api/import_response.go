package api

type scriptImportResponseOptions struct {
	Result              any
	Scripts             any
	Script              any
	Warnings            []string
	Stats               any
	Correlations        any
	Assumptions         []string
	UnsupportedElements []string
	ScenarioDraft       any
	Summary             any
}

func newScriptImportResponse(opts scriptImportResponseOptions) map[string]any {
	scripts := opts.Scripts
	if scripts == nil {
		if opts.Script != nil {
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
		"scripts":              scripts,
		"script":               opts.Script,
		"warnings":             warnings,
		"stats":                stats,
		"correlations":         correlations,
		"assumptions":          assumptions,
		"unsupported_elements": unsupported,
		"scenario_draft":       opts.ScenarioDraft,
		"summary":              summary,
	}
	if opts.Result != nil {
		response["result"] = opts.Result
	}

	return response
}

func newRecorderImportStats(actionCount int, correlationCount int) map[string]int {
	return map[string]int{
		"converted_actions":     actionCount,
		"correlations_detected": correlationCount,
	}
}
