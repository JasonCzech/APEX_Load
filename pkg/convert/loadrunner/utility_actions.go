package loadrunner

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func appendLoadRunnerAction(result *ConvertResult, fn ParsedFunction, action dsl.Action, mode LoadRunnerSupportMode, notes string) {
	if result == nil || result.Script == nil {
		return
	}
	actionIndex := actionCountForPhase(result.Script, fn.Phase)
	mapping := sourceReviewMappingForFunction(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", statusForSupportMode(mode), notes)
	mapping.SupportMode = string(mode)
	appendSourceReviewMapping(result, mapping)
	appendActionForPhase(result.Script, fn.Phase, action)
	result.Stats.ConvertedActions++
	recordLoadRunnerFunctionSupport(result, mode)
}

func actionCountForPhase(script *dsl.Script, phase string) int {
	if script == nil {
		return 0
	}
	switch normalizeLoadRunnerPhase(phase) {
	case "init":
		return len(script.Init)
	case "teardown":
		return len(script.Teardown)
	default:
		return len(script.Actions)
	}
}

func appendActionForPhase(script *dsl.Script, phase string, action dsl.Action) {
	if script == nil {
		return
	}
	switch normalizeLoadRunnerPhase(phase) {
	case "init":
		script.Init = append(script.Init, action)
	case "teardown":
		script.Teardown = append(script.Teardown, action)
	default:
		script.Actions = append(script.Actions, action)
	}
}

func sourceReviewActionPath(phase string, index int) string {
	switch normalizeLoadRunnerPhase(phase) {
	case "init":
		return fmt.Sprintf("init[%d]", index)
	case "teardown":
		return fmt.Sprintf("teardown[%d]", index)
	default:
		return fmt.Sprintf("actions[%d]", index)
	}
}

func normalizeLoadRunnerPhase(phase string) string {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "init", "teardown":
		return strings.ToLower(strings.TrimSpace(phase))
	default:
		return "action"
	}
}

func statusForSupportMode(mode LoadRunnerSupportMode) string {
	switch mode {
	case LoadRunnerSupportReviewOnly, LoadRunnerSupportCustomReview:
		return "review_only"
	case LoadRunnerSupportExternalRequired:
		return "external_required"
	default:
		return "converted"
	}
}

func recordLoadRunnerFunctionSupport(result *ConvertResult, mode LoadRunnerSupportMode) {
	if result == nil {
		return
	}
	switch mode {
	case LoadRunnerSupportExecutable, LoadRunnerSupportNativeNoop:
		result.Stats.NativeFunctions++
	case LoadRunnerSupportReviewOnly:
		result.Stats.ReviewOnlyFunctions++
	case LoadRunnerSupportExternalRequired:
		result.Stats.ExternalRequiredFunctions++
	case LoadRunnerSupportCustomReview:
		result.Stats.CustomCallFunctions++
	}
}

func sourceReviewMappingWithSupportMode(fn ParsedFunction, targetPath string, targetName string, targetKind string, status string, notes string, mode LoadRunnerSupportMode) SourceReviewMapping {
	mapping := sourceReviewMappingForFunction(fn, targetPath, targetName, targetKind, status, notes)
	mapping.SupportMode = string(mode)
	return mapping
}

func convertLoadRunnerFunctionAction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	def, ok := lookupLoadRunnerFunction(fn.Name)
	if !ok {
		return convertCustomCallAction(fn), LoadRunnerSupportCustomReview, true
	}

	switch def.Family {
	case "message":
		return convertLoadRunnerMessageAction(fn, def), def.SupportMode, true
	case "runtime":
		if fn.Name == "lr_rendezvous" || fn.Name == "lr_rendezvous_ex" {
			return convertLoadRunnerRendezvousAction(fn), def.SupportMode, true
		}
		return convertGenericLoadRunnerAction(fn, "loadrunner_runtime", def), def.SupportMode, true
	case "transaction":
		return convertGenericLoadRunnerAction(fn, "loadrunner_transaction", def), def.SupportMode, true
	case "timer":
		return convertGenericLoadRunnerAction(fn, "loadrunner_timer", def), def.SupportMode, true
	case "metric":
		return convertGenericLoadRunnerAction(fn, "loadrunner_metric", def), def.SupportMode, true
	case "parameter", "attribute", "info":
		return convertGenericLoadRunnerAction(fn, "loadrunner_parameter", def), def.SupportMode, true
	case "json":
		return convertGenericLoadRunnerAction(fn, "loadrunner_json", def), def.SupportMode, true
	case "xml":
		return convertGenericLoadRunnerAction(fn, "loadrunner_xml", def), def.SupportMode, true
	case "database":
		return convertGenericLoadRunnerAction(fn, "loadrunner_database", def), def.SupportMode, true
	case "secret":
		return convertGenericLoadRunnerAction(fn, "loadrunner_secret", def), def.SupportMode, true
	default:
		return convertGenericLoadRunnerAction(fn, "loadrunner_review", def), def.SupportMode, true
	}
}

func convertLoadRunnerMessageAction(fn ParsedFunction, def LoadRunnerFunctionDefinition) dsl.Action {
	if fn.Name == "lr_error_message" {
		action := convertLRErrorMessageAction(fn)
		annotateLoadRunnerAction(&action, fn, def)
		return action
	}
	action := convertGenericLoadRunnerAction(fn, "loadrunner_message", def)
	args := loadRunnerControlArgs(fn)
	if len(args) > 0 {
		action.Value = convertLoadRunnerMessageExpression(args[0])
		action.Params["format"] = action.Value
	}
	if len(args) > 1 {
		messageArgs := make([]any, 0, len(args)-1)
		for _, arg := range args[1:] {
			messageArgs = append(messageArgs, convertLoadRunnerMessageExpression(arg))
		}
		action.Params["args"] = messageArgs
	}
	action.Params["level"] = loadRunnerMessageLevel(fn.Name)
	if fn.Name == "lr_vuser_status_message" {
		action.Params["status_message"] = true
	}
	return action
}

func convertGenericLoadRunnerAction(fn ParsedFunction, actionType string, def LoadRunnerFunctionDefinition) dsl.Action {
	action := dsl.Action{
		Name:     loadRunnerActionName(fn),
		Type:     actionType,
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"function":     fn.Name,
			"family":       def.Family,
			"support_mode": string(def.SupportMode),
			"args":         loadRunnerActionArgs(fn),
		},
	}
	if def.SupportMode == LoadRunnerSupportExternalRequired {
		action.Type = "loadrunner_external"
	}
	if def.SupportMode == LoadRunnerSupportReviewOnly {
		action.Type = "loadrunner_review"
	}
	if len(fn.CallArgs) > 0 {
		action.Params["call_args"] = loadRunnerStructuredCallArgs(fn.CallArgs)
	}
	annotateLoadRunnerAction(&action, fn, def)
	return action
}

func convertCustomCallAction(fn ParsedFunction) dsl.Action {
	return dsl.Action{
		Name:     loadRunnerActionName(fn),
		Type:     "custom_call",
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"function":     fn.Name,
			"support_mode": string(LoadRunnerSupportCustomReview),
			"args":         loadRunnerActionArgs(fn),
			"call_args":    loadRunnerStructuredCallArgs(fn.CallArgs),
		},
	}
}

func convertLoadRunnerRendezvousAction(fn ParsedFunction) dsl.Action {
	args := loadRunnerControlArgs(fn)
	key := firstNonEmpty(fn.StepName, fn.Name)
	if len(args) > 0 {
		key = convertLoadRunnerMessageExpression(args[0])
	}
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(key, "lr_rendezvous")),
		Type:     "rendezvous",
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"function":     fn.Name,
			"support_mode": string(LoadRunnerSupportExecutable),
			"key":          key,
		},
	}
	if len(args) > 1 {
		action.Params["args"] = loadRunnerActionArgs(fn)
	}
	return action
}

func annotateLoadRunnerAction(action *dsl.Action, fn ParsedFunction, def LoadRunnerFunctionDefinition) {
	if action == nil {
		return
	}
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	action.Params["function"] = fn.Name
	action.Params["family"] = def.Family
	action.Params["support_mode"] = string(def.SupportMode)
	if action.Metadata == nil {
		action.Metadata = sourceFunctionMetadata(fn)
	}
	action.Metadata["support_mode"] = string(def.SupportMode)
}

func loadRunnerActionName(fn ParsedFunction) string {
	base := firstNonEmpty(fn.StepName, firstNonEmptyArg(fn, "Name"), fn.Name, "loadrunner_function")
	name := sanitizeName(base)
	if name == "" {
		name = "loadrunner_function"
	}
	if fn.LineNumber > 0 && strings.EqualFold(name, sanitizeName(fn.Name)) {
		name = fmt.Sprintf("%s_%d", name, fn.LineNumber)
	}
	return name
}

func loadRunnerActionArgs(fn ParsedFunction) []any {
	args := loadRunnerControlArgs(fn)
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, convertLoadRunnerMessageExpression(arg))
	}
	return values
}

func loadRunnerStructuredCallArgs(args []ParsedArgument) []map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(args))
	for _, arg := range args {
		entry := map[string]any{
			"raw":   arg.Raw,
			"value": convertLoadRunnerMessageExpression(arg.Value),
			"kind":  arg.Kind,
		}
		if arg.Name != "" {
			entry["name"] = arg.Name
		}
		if arg.NestedCall != "" {
			entry["nested_call"] = arg.NestedCall
		}
		out = append(out, entry)
	}
	return out
}

func loadRunnerMessageLevel(name string) string {
	switch name {
	case "lr_debug_message":
		return "debug"
	case "lr_error_message", "lr_set_custom_error_message":
		return "error"
	default:
		return "info"
	}
}

func loadRunnerSupportNotes(fn ParsedFunction, mode LoadRunnerSupportMode) string {
	if mode == LoadRunnerSupportCustomReview {
		return "Parsed custom C helper call preserved as a review action."
	}
	if def, ok := lookupLoadRunnerFunction(fn.Name); ok {
		switch mode {
		case LoadRunnerSupportExternalRequired:
			return "Documented LoadRunner function requires external runtime or governed secret integration."
		case LoadRunnerSupportReviewOnly:
			return "Documented LoadRunner function preserved as a native review action."
		case LoadRunnerSupportNativeNoop:
			return "Documented LoadRunner function has no direct APEX Load side effect and is handled as a native no-op."
		default:
			return "Converted documented LoadRunner " + def.Family + " function to a native action."
		}
	}
	return "Parsed LoadRunner function converted to a native action."
}
