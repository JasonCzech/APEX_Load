package dsl

import (
	"fmt"
	"strings"
)

func brunoActionSpecs() []ActionSpec {
	return []ActionSpec{
		{
			Type:      "bruno_run",
			Protocols: []string{"bruno"},
			Summary:   "Executes a selectable Bruno collection, folder, request, or tag set from the immutable source bundle.",
			Fields: []FieldSpec{
				{Name: "params.selection", Kind: FieldKindEnum, Enum: []string{"collection", "folder", "request", "tag"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Selection kind; defaults to collection."},
				{Name: "params.paths", Kind: FieldKindArray, Doc: "Relative collection paths selected for execution."},
				{Name: "params.include_tags", Kind: FieldKindArray, Doc: "Only execute items carrying one of these tags."},
				{Name: "params.exclude_tags", Kind: FieldKindArray, Doc: "Exclude items carrying one of these tags."},
				{Name: "params.recursive", Kind: FieldKindBool, Doc: "Recursively execute selected folders."},
				{Name: "params.environment", Kind: FieldKindString, Doc: "Collection environment override."},
				{Name: "params.global_environment", Kind: FieldKindString, Doc: "Workspace global-environment override."},
				{Name: "params.data_source", Kind: FieldKindString, Doc: "CSV/JSON data source or bundled relative path."},
				{Name: "params.delay_ms", Kind: FieldKindInt, Maximum: 3_600_000, Doc: "Delay between selected requests."},
				{Name: "params.bail", Kind: FieldKindBool, Doc: "Stop after the first request, test, or assertion failure."},
				{Name: "params.tests_only", Kind: FieldKindBool, Doc: "Run only requests containing executable tests or assertions."},
				{Name: "params.execution", Kind: FieldKindEnum, Enum: []string{"sequential", "parallel"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Data-iteration execution mode; requests remain in Bruno source order within each iteration."},
				{Name: "params.max_parallel", Kind: FieldKindInt, Maximum: 100_000, Doc: "Bounded data-iteration concurrency when execution is parallel."},
				{Name: "params.persist_session_across_iterations", Kind: FieldKindBool, Doc: "Retain this VUser's Bruno runtime variables, cookies, and connections between APEX iterations."},
				{Name: "params.stream_timeout_ms", Kind: FieldKindInt, Maximum: 86_400_000, Doc: "Deadline for long-lived gRPC or WebSocket work."},
			},
			Validate: validateBrunoRunAction,
		},
	}
}

func validateBrunoRunAction(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	selection := strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "selection")))
	switch selection {
	case "", "collection":
	case "folder", "request":
		if len(actionParamStrings(action.Params, "paths")) == 0 {
			return []string{fmt.Sprintf("%s: params.paths is required for %s selection", label, selection)}
		}
	case "tag":
		if len(actionParamStrings(action.Params, "include_tags")) == 0 {
			return []string{fmt.Sprintf("%s: params.include_tags is required for tag selection", label)}
		}
	default:
		return []string{fmt.Sprintf("%s: params.selection must be collection, folder, request, or tag", label)}
	}
	if execution := strings.ToLower(strings.TrimSpace(actionParamString(action.Params, "execution"))); execution != "" && execution != "sequential" && execution != "parallel" {
		return []string{fmt.Sprintf("%s: params.execution must be sequential or parallel", label)}
	}
	var issues []string
	for key, maximum := range map[string]int{
		"delay_ms":          3_600_000,
		"max_parallel":      100_000,
		"stream_timeout_ms": 86_400_000,
	} {
		if value, ok := actionParamIntValue(action.Params, key); ok && (value < 0 || value > maximum) {
			issues = append(issues, fmt.Sprintf("%s: params.%s must be between 0 and %d", label, key, maximum))
		}
	}
	return issues
}

func actionParamStrings(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	switch values := params[key].(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if item, ok := value.(string); ok && strings.TrimSpace(item) != "" {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}
