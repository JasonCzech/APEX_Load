package enterprisearchive

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func appendSourceAction(result *ConvertResult, fn ParsedFunction, action dsl.Action, mode SourceFunctionSupportMode, notes string) {
	if result == nil || result.Script == nil {
		return
	}
	tagSourceFlowMetadata(result, fn, &action)
	actionIndex := actionCountForPhase(result.Script, fn.Phase)
	mapping := sourceReviewMappingForFunction(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", statusForSupportMode(mode), notes)
	mapping.SupportMode = string(mode)
	appendSourceReviewMapping(result, mapping)
	appendActionForPhase(result.Script, fn.Phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, mode)
}

// actionPathByName returns the source-review TargetPath of the first top-level
// action whose name matches, searching init -> actions -> teardown (the order
// uniquify dedupes in). Post-uniquify names are unique across these phases, so
// the match is unambiguous. Used to repoint a renamed action's mapping
// positionally rather than by the (possibly duplicated) old name.
func actionPathByName(script *dsl.Script, name string) (string, bool) {
	if script == nil {
		return "", false
	}
	phases := []struct {
		phase   string
		actions []dsl.Action
	}{
		{"init", script.Init},
		{"actions", script.Actions},
		{"teardown", script.Teardown},
	}
	for _, p := range phases {
		for i := range p.actions {
			if p.actions[i].Name == name {
				return sourceReviewActionPath(p.phase, i), true
			}
		}
	}
	return "", false
}

func actionCountForPhase(script *dsl.Script, phase string) int {
	if script == nil {
		return 0
	}
	switch normalizeSourcePhase(phase) {
	case "init":
		return len(script.Init)
	case "teardown":
		return len(script.Teardown)
	default:
		return len(script.Actions)
	}
}

// actionAtPhaseIndex returns a pointer to the action at index i within the given
// phase, or nil when out of range. The pointer aliases the slice element so the
// caller can amend the action in place (used to attach a web_find assertion to
// the preceding request). Resolve and use it immediately — a later append to the
// same phase may reallocate the slice and invalidate the pointer.
func actionAtPhaseIndex(script *dsl.Script, phase string, i int) *dsl.Action {
	if script == nil || i < 0 {
		return nil
	}
	switch normalizeSourcePhase(phase) {
	case "init":
		if i < len(script.Init) {
			return &script.Init[i]
		}
	case "teardown":
		if i < len(script.Teardown) {
			return &script.Teardown[i]
		}
	default:
		if i < len(script.Actions) {
			return &script.Actions[i]
		}
	}
	return nil
}

func appendActionForPhase(script *dsl.Script, phase string, action dsl.Action) {
	if script == nil {
		return
	}
	switch normalizeSourcePhase(phase) {
	case "init":
		script.Init = append(script.Init, action)
	case "teardown":
		script.Teardown = append(script.Teardown, action)
	default:
		script.Actions = append(script.Actions, action)
	}
}

func sourceReviewActionPath(phase string, index int) string {
	switch normalizeSourcePhase(phase) {
	case "init":
		return fmt.Sprintf("init[%d]", index)
	case "teardown":
		return fmt.Sprintf("teardown[%d]", index)
	default:
		return fmt.Sprintf("actions[%d]", index)
	}
}

func normalizeSourcePhase(phase string) string {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "init", "teardown":
		return strings.ToLower(strings.TrimSpace(phase))
	default:
		return "action"
	}
}

func statusForSupportMode(mode SourceFunctionSupportMode) string {
	switch mode {
	case SourceFunctionSupportReviewOnly, SourceFunctionSupportCustomReview:
		return "review_only"
	case SourceFunctionSupportExternalRequired:
		return "external_required"
	default:
		return "converted"
	}
}

func recordSourceFunctionSupport(result *ConvertResult, mode SourceFunctionSupportMode) {
	if result == nil {
		return
	}
	switch mode {
	case SourceFunctionSupportExecutable, SourceFunctionSupportNativeNoop:
		result.Stats.NativeFunctions++
	case SourceFunctionSupportReviewOnly:
		result.Stats.ReviewOnlyFunctions++
	case SourceFunctionSupportExternalRequired:
		result.Stats.ExternalRequiredFunctions++
	case SourceFunctionSupportCustomReview:
		result.Stats.CustomCallFunctions++
	}
}

func sourceReviewMappingWithSupportMode(fn ParsedFunction, targetPath string, targetName string, targetKind string, status string, notes string, mode SourceFunctionSupportMode) SourceReviewMapping {
	mapping := sourceReviewMappingForFunction(fn, targetPath, targetName, targetKind, status, notes)
	mapping.SupportMode = string(mode)
	return mapping
}

func convertSourceFunctionAction(fn ParsedFunction) (dsl.Action, SourceFunctionSupportMode, bool) {
	def, ok := lookupSourceFunction(fn.Name)
	if !ok {
		return convertCustomCallAction(fn), SourceFunctionSupportCustomReview, true
	}

	switch def.Family {
	case "message":
		return convertSourceMessageAction(fn, def), def.SupportMode, true
	case "runtime":
		if fn.Name == "lr_rendezvous" || fn.Name == "lr_rendezvous_ex" {
			return convertSourceRendezvousAction(fn), def.SupportMode, true
		}
		return convertGenericSourceAction(fn, "source_runtime", def), def.SupportMode, true
	case "transaction":
		return convertGenericSourceAction(fn, "source_transaction", def), def.SupportMode, true
	case "timer":
		return convertGenericSourceAction(fn, "source_timer", def), def.SupportMode, true
	case "metric":
		return convertGenericSourceAction(fn, "source_metric", def), def.SupportMode, true
	case "parameter", "attribute", "info":
		return convertGenericSourceAction(fn, "source_parameter", def), def.SupportMode, true
	case "json":
		return convertGenericSourceAction(fn, "source_json", def), def.SupportMode, true
	case "xml":
		return convertGenericSourceAction(fn, "source_xml", def), def.SupportMode, true
	case "database":
		return convertGenericSourceAction(fn, "source_database", def), def.SupportMode, true
	case "secret":
		return convertGenericSourceAction(fn, "source_secret", def), def.SupportMode, true
	default:
		return convertGenericSourceAction(fn, "source_review", def), def.SupportMode, true
	}
}

func convertSourceMessageAction(fn ParsedFunction, def SourceFunctionDefinition) dsl.Action {
	if fn.Name == "lr_error_message" {
		action := convertLRErrorMessageAction(fn)
		annotateSourceAction(&action, fn, def)
		return action
	}
	action := convertGenericSourceAction(fn, "source_message", def)
	args := sourceControlArgs(fn)
	if len(args) > 0 {
		action.Value = convertSourceMessageExpression(args[0])
		action.Params["format"] = action.Value
	}
	if len(args) > 1 {
		messageArgs := make([]any, 0, len(args)-1)
		for _, arg := range args[1:] {
			messageArgs = append(messageArgs, convertSourceMessageExpression(arg))
		}
		action.Params["args"] = messageArgs
	}
	action.Params["level"] = sourceMessageLevel(fn.Name)
	if fn.Name == "lr_vuser_status_message" {
		action.Params["status_message"] = true
	}
	return action
}

func convertWebConvertParamAction(fn ParsedFunction) dsl.Action {
	args := sourceControlArgs(fn)
	saveParam := ""
	if len(args) > 0 {
		saveParam = args[0]
	}
	params := map[string]any{
		"function":     "web_convert_param",
		"family":       "parameter",
		"support_mode": string(SourceFunctionSupportExecutable),
		"args":         sourceActionArgs(fn),
	}
	if saveParam != "" {
		params["save_param"] = saveParam
	}
	for _, arg := range args[1:] {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "sourcestring":
			params["source_string"] = convertSourceMessageExpression(value)
		case "sourceencoding":
			params["source_encoding"] = cleanConfigValue(value)
		case "targetencoding":
			params["target_encoding"] = cleanConfigValue(value)
		}
	}
	if _, ok := params["source_string"]; !ok && saveParam != "" {
		params["source_string"] = "{{var." + canonicalSourceParameterName(saveParam) + "}}"
	}
	action := dsl.Action{
		Name:     sourceActionName(fn),
		Type:     "source_parameter",
		Metadata: sourceFunctionMetadata(fn),
		Params:   params,
	}
	action.Metadata["support_mode"] = string(SourceFunctionSupportExecutable)
	return action
}

func convertGenericSourceAction(fn ParsedFunction, actionType string, def SourceFunctionDefinition) dsl.Action {
	action := dsl.Action{
		Name:     sourceActionName(fn),
		Type:     actionType,
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"function":     fn.Name,
			"family":       def.Family,
			"support_mode": string(def.SupportMode),
			"args":         sourceActionArgs(fn),
		},
	}
	if def.SupportMode == SourceFunctionSupportExternalRequired {
		action.Type = "source_external"
	}
	if def.SupportMode == SourceFunctionSupportReviewOnly {
		action.Type = "source_review"
	}
	if len(fn.CallArgs) > 0 {
		action.Params["call_args"] = sourceStructuredCallArgs(fn.CallArgs)
	}
	annotateSourceAction(&action, fn, def)
	return action
}

func convertCustomCallAction(fn ParsedFunction) dsl.Action {
	return dsl.Action{
		Name:     sourceActionName(fn),
		Type:     "custom_call",
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"function":     fn.Name,
			"support_mode": string(SourceFunctionSupportCustomReview),
			"args":         sourceActionArgs(fn),
			"call_args":    sourceStructuredCallArgs(fn.CallArgs),
		},
	}
}

func convertSourceRendezvousAction(fn ParsedFunction) dsl.Action {
	args := sourceControlArgs(fn)
	key := convertutil.FirstNonEmpty(fn.StepName, fn.Name)
	if len(args) > 0 {
		key = convertSourceMessageExpression(args[0])
	}
	action := dsl.Action{
		Name:     sanitizeName(convertutil.FirstNonEmpty(key, "lr_rendezvous")),
		Type:     "rendezvous",
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"function":     fn.Name,
			"support_mode": string(SourceFunctionSupportExecutable),
			"key":          key,
		},
	}
	if len(args) > 1 {
		action.Params["args"] = sourceActionArgs(fn)
	}
	return action
}

func annotateSourceAction(action *dsl.Action, fn ParsedFunction, def SourceFunctionDefinition) {
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

func sourceActionName(fn ParsedFunction) string {
	base := convertutil.FirstNonEmpty(fn.StepName, firstNonEmptyArg(fn, "Name"), fn.Name, "source_function")
	name := sanitizeName(base)
	if name == "" {
		name = "source_function"
	}
	if fn.LineNumber > 0 && strings.EqualFold(name, sanitizeName(fn.Name)) {
		name = fmt.Sprintf("%s_%d", name, fn.LineNumber)
	}
	return name
}

func sourceActionArgs(fn ParsedFunction) []any {
	args := sourceControlArgs(fn)
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, convertSourceMessageExpression(arg))
	}
	return values
}

func sourceStructuredCallArgs(args []ParsedArgument) []map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(args))
	for _, arg := range args {
		entry := map[string]any{
			"raw":   arg.Raw,
			"value": convertSourceMessageExpression(arg.Value),
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

func sourceMessageLevel(name string) string {
	switch name {
	case "lr_debug_message":
		return "debug"
	case "lr_error_message", "lr_set_custom_error_message":
		return "error"
	default:
		return "info"
	}
}

func sourceSupportNotes(fn ParsedFunction, mode SourceFunctionSupportMode) string {
	if mode == SourceFunctionSupportCustomReview {
		return "Parsed custom C helper call preserved as a review action."
	}
	if def, ok := lookupSourceFunction(fn.Name); ok {
		switch mode {
		case SourceFunctionSupportExternalRequired:
			return "Documented source function requires external runtime or governed secret integration."
		case SourceFunctionSupportReviewOnly:
			return "Documented source function preserved as a native review action."
		case SourceFunctionSupportNativeNoop:
			return "Documented source function has no direct APEX Load side effect and is handled as a native no-op."
		default:
			return "Converted documented source " + def.Family + " function to a native action."
		}
	}
	return "Parsed source function converted to a native action."
}
