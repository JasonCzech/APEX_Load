package enterprisearchive

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func appendSourceAction(result *ConvertResult, fn ParsedFunction, action dsl.Action, mode SourceFunctionSupportMode, notes string) {
	if result == nil || result.Script == nil {
		return
	}
	flowFn := fn
	effectiveMode := mode
	if sourceFunctionHasUnsupportedTransactionDependency(fn) {
		effectiveMode = SourceFunctionSupportReviewOnly
		makeEmbeddedTransactionConsumerDisclosureSafe(&action, fn, effectiveMode)
		gateEmbeddedUnsupportedTransactionConsumer(&action, fn)
		flowFn = disclosureSafeSourceFlow(fn)
	} else if sourceSupportModeRequiresDisclosureSafePlaceholder(mode) {
		makeSourceReviewActionDisclosureSafe(&action, fn, mode)
		flowFn = disclosureSafeSourceFlow(fn)
	}
	tagSourceFlowMetadata(result, flowFn, &action)
	actionIndex := actionCountForPhase(result.Script, fn.Phase)
	mapping := sourceReviewMappingForFunction(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", statusForSupportMode(effectiveMode), notes)
	mapping.SupportMode = string(effectiveMode)
	appendSourceReviewMapping(result, mapping)
	appendActionForPhase(result.Script, fn.Phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, effectiveMode)
}

func sourceFunctionHasUnsupportedTransactionDependency(fn ParsedFunction) bool {
	return fn.containsUnsupportedTransactionCall ||
		fn.dependsOnUnsupportedTransactionFlow
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
		action := convertGenericSourceAction(fn, "source_parameter", def)
		if sourceRandomArrayAssignmentRequiresReview(fn) {
			action.Type = "source_review"
			makeSourceReviewActionDisclosureSafe(&action, fn, SourceFunctionSupportReviewOnly)
			importreview.ApplyToAction(&action, []importreview.Finding{{
				Code: "source_parameter_array_expression_requires_review", ReasonCode: "source_parameter_array_expression_requires_review",
				Element: fn.Name, Source: "enterprise_script_archive", SourceRef: fn.Filename,
				Detail: "The source random-array expression requires an exact literal array name and a verified standalone selection call.",
				Gate:   importreview.GateExecution,
			}})
			return action, SourceFunctionSupportReviewOnly, true
		}
		return action, def.SupportMode, true
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

func nativeSourceTransactionOwnershipUnsupported(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lr_start_cross_vuser_transaction", "lr_end_cross_vuser_transaction",
		"lr_start_transaction_instance", "lr_end_transaction_instance",
		"lr_stop_transaction_instance", "lr_resume_transaction_instance",
		"lr_set_transaction_instance_status",
		"lr_get_trans_instance_duration", "lr_get_trans_instance_status",
		"lr_get_trans_instance_think_time", "lr_get_trans_instance_wasted_time":
		return true
	default:
		return false
	}
}

const unsupportedSourceTransactionOwnershipCode = "source_transaction_ownership_unsupported"

func gateUnsupportedSourceTransactionOwnership(action *dsl.Action, fn ParsedFunction, detail string) {
	gateUnsupportedSourceTransactionAction(
		action,
		fn,
		unsupportedSourceTransactionOwnershipCode,
		detail,
		"replace_with_single_vuser_transaction_semantics_and_remove_gate",
	)
}

func nativeSourceTransactionValueUnsupported(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lr_get_transaction_duration", "lr_get_transaction_status",
		"lr_get_transaction_think_time", "lr_get_transaction_wasted_time",
		"lr_wasted_time":
		return true
	default:
		return false
	}
}

func nativeSourceTransactionReviewUnsupported(name string) bool {
	return nativeSourceTransactionOwnershipUnsupported(name) ||
		nativeSourceTransactionValueUnsupported(name)
}

const unsupportedNativeSourceTransactionValueCode = "source_transaction_native_value_unsupported"

func gateUnsupportedNativeSourceTransactionValue(action *dsl.Action, fn ParsedFunction, detail string) {
	gateUnsupportedSourceTransactionAction(
		action,
		fn,
		unsupportedNativeSourceTransactionValueCode,
		detail,
		"replace_the_return_value_or_wasted_time_dependency_and_remove_gate",
	)
}

const unsupportedNativeSourceTransactionCallCode = "source_transaction_native_call_unsupported"

func gateUnsupportedNativeSourceTransactionCall(action *dsl.Action, fn ParsedFunction, detail string) {
	gateUnsupportedSourceTransactionAction(
		action,
		fn,
		unsupportedNativeSourceTransactionCallCode,
		detail,
		"replace_the_unsupported_transaction_call_and_remove_gate",
	)
}

const unsupportedEmbeddedSourceTransactionConsumerCode = "source_transaction_embedded_consumer_unsupported"

func gateEmbeddedUnsupportedTransactionConsumer(action *dsl.Action, fn ParsedFunction) {
	if action == nil {
		return
	}
	sourceRef := strings.TrimSpace(fn.Filename)
	if fn.LineNumber > 0 {
		if fn.EndLine > fn.LineNumber {
			sourceRef = fmt.Sprintf("%s:%d-%d", sourceRef, fn.LineNumber, fn.EndLine)
		} else {
			sourceRef = fmt.Sprintf("%s:%d", sourceRef, fn.LineNumber)
		}
	}
	detail := "This source call consumes an unsupported native transaction return value; its original semantics cannot be executed safely."
	resolution := "replace_the_embedded_transaction_value_dependency_and_remove_gate"
	if fn.dependsOnUnsupportedTransactionFlow {
		detail = "This source call is controlled by an unsupported native transaction return value; its branch or loop semantics cannot be executed safely."
		resolution = "replace_the_transaction_dependent_control_flow_and_remove_gate"
	}
	importreview.ApplyToAction(action, []importreview.Finding{{
		Code:       unsupportedEmbeddedSourceTransactionConsumerCode,
		Element:    fn.Name,
		Detail:     detail,
		Location:   sourceRef,
		Source:     "enterprise_script_archive",
		ReasonCode: unsupportedEmbeddedSourceTransactionConsumerCode,
		SourceRef:  sourceRef,
		Resolution: resolution,
		Gate:       importreview.GateExecution,
	}})
}

func gateUnsupportedSourceTransactionAction(
	action *dsl.Action,
	fn ParsedFunction,
	code string,
	detail string,
	resolution string,
) {
	if action == nil {
		return
	}
	action.Type = "source_review"
	if action.Params == nil {
		action.Params = make(map[string]any)
	}
	action.Params["support_mode"] = string(SourceFunctionSupportReviewOnly)
	makeSourceReviewActionDisclosureSafe(action, fn, SourceFunctionSupportReviewOnly)

	sourceRef := strings.TrimSpace(fn.Filename)
	if fn.LineNumber > 0 {
		if fn.EndLine > fn.LineNumber {
			sourceRef = fmt.Sprintf("%s:%d-%d", sourceRef, fn.LineNumber, fn.EndLine)
		} else {
			sourceRef = fmt.Sprintf("%s:%d", sourceRef, fn.LineNumber)
		}
	}
	importreview.ApplyToAction(action, []importreview.Finding{{
		Code:       code,
		Element:    fn.Name,
		Detail:     detail,
		Location:   sourceRef,
		Source:     "enterprise_script_archive",
		ReasonCode: code,
		SourceRef:  sourceRef,
		Resolution: resolution,
		Gate:       importreview.GateExecution,
	}})
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
	if action.Type == "source_parameter" {
		annotateSourceParameterAssignment(&action, fn)
	}
	annotateSourceAction(&action, fn, def)
	if sourceSupportModeRequiresDisclosureSafePlaceholder(def.SupportMode) {
		makeSourceReviewActionDisclosureSafe(&action, fn, def.SupportMode)
	}
	return action
}

// Positional parameter functions publish real variables, just like named
// SaveParam contributors. Expose their executable reads and writes so review
// does not mistake the diagnostic call_args snapshot for dependency evidence.
func annotateSourceParameterAssignment(action *dsl.Action, fn ParsedFunction) {
	args := sourceControlArgs(fn)
	saveIndex := -1
	switch fn.Name {
	case "lr_save_string", "lr_save_int":
		saveIndex = 1
		if len(args) > 0 {
			expression := args[0]
			if len(fn.CallArgs) > 0 && fn.CallArgs[0].Raw != "" {
				expression = fn.CallArgs[0].Raw
			}
			action.Params["source_string"] = convertSourceMessageExpression(args[0])
			if literal, ok := decodeAdjacentCStringLiterals(expression); ok {
				// Authored C strings may contain function-looking text. Decode the
				// literal before considering executable nested-call syntax.
				value := convertParamReferences(literal)
				action.Params["source_string"] = value
				if executable, ok := action.Params["args"].([]any); ok && len(executable) > 0 {
					executable[0] = value
				}
			} else if array, ok := sourceRandomParameterArray(expression); ok {
				action.Params["source_array"] = map[string]any{"name": array, "selection": "random"}
				action.Params["source_string"] = "{{var." + array + "_count}}"
			}
		}
	case "lr_save_var":
		saveIndex = 3
		if len(args) > 0 {
			action.Params["source_string"] = convertSourceMessageExpression(args[0])
		}
	case "lr_save_timestamp", "lr_param_sprintf":
		saveIndex = 0
	}
	if saveIndex >= 0 && saveIndex < len(args) && strings.TrimSpace(args[saveIndex]) != "" {
		action.Params["save_param"] = args[saveIndex]
	}
}

func sourceRandomParameterArray(value string) (string, bool) {
	value = strings.TrimSpace(value)
	open := strings.Index(value, "(")
	if open <= 0 || strings.TrimSpace(value[:open]) != "lr_paramarr_random" || !strings.HasSuffix(value, ")") {
		return "", false
	}
	args := splitCFunctionArgs(value[open+1 : len(value)-1])
	if len(args) != 1 {
		return "", false
	}
	literal, ok := decodeAdjacentCStringLiterals(args[0])
	if !ok {
		return "", false
	}
	name := canonicalSourceParameterName(literal)
	return name, name != ""
}

func sourceRandomArrayAssignmentRequiresReview(fn ParsedFunction) bool {
	if fn.Name != "lr_save_string" && fn.Name != "lr_save_int" {
		return false
	}
	args := sourceControlArgs(fn)
	if len(args) == 0 {
		return false
	}
	expression := args[0]
	if len(fn.CallArgs) > 0 {
		// Use the source token to distinguish an authored literal containing a
		// function name from an executable C call, before argument cleanup.
		expression = fn.CallArgs[0].Raw
	}
	if _, literal := decodeAdjacentCStringLiterals(expression); literal {
		return false
	}
	expression = strings.TrimSpace(expression)
	open := strings.Index(expression, "(")
	if open <= 0 || strings.TrimSpace(expression[:open]) != "lr_paramarr_random" {
		return false
	}
	_, exact := sourceRandomParameterArray(expression)
	return !exact
}

// labelSourceTransactionMarker gives the executable start/end markers distinct
// display names without changing the source transaction identity stored in
// params.args[0]. In particular, this keeps a compacted http_sequence free to
// retain the original LoadRunner transaction name.
func labelSourceTransactionMarker(action dsl.Action, fn ParsedFunction, verb string) dsl.Action {
	transaction := sourceControlArgAt(fn, 0)
	if transaction == "" {
		transaction = convertutil.FirstNonEmpty(fn.StepName, "transaction")
	}
	action.Name = sanitizeName(fmt.Sprintf("%s %s transaction", verb, transaction))
	return action
}

func convertCustomCallAction(fn ParsedFunction) dsl.Action {
	action := dsl.Action{
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
	makeSourceReviewActionDisclosureSafe(&action, fn, SourceFunctionSupportCustomReview)
	return action
}

func sourceSupportModeRequiresDisclosureSafePlaceholder(mode SourceFunctionSupportMode) bool {
	switch mode {
	case SourceFunctionSupportReviewOnly, SourceFunctionSupportExternalRequired, SourceFunctionSupportCustomReview:
		return true
	default:
		return false
	}
}

// makeSourceReviewActionDisclosureSafe keeps imported argument values on
// review-only behavior while retaining its execution gate.
func makeSourceReviewActionDisclosureSafe(action *dsl.Action, fn ParsedFunction, mode SourceFunctionSupportMode) {
	if action == nil {
		return
	}
	previousMetadata := action.Metadata
	action.Name = disclosureSafeSourceReviewActionName(fn)
	action.Target = ""
	action.Value = ""
	action.Body = ""
	action.BodyFile = ""
	action.Headers = nil
	action.HeaderList = nil
	action.Auth = nil

	params := map[string]any{
		"function":     fn.Name,
		"support_mode": string(mode),
	}
	if def, ok := lookupSourceFunction(fn.Name); ok && strings.TrimSpace(def.Family) != "" {
		params["family"] = def.Family
	}
	if args := disclosureSafeSourceReviewArgs(fn); len(args) > 0 {
		params["args"] = args
	}
	if args := disclosureSafeStructuredCallArgs(fn.CallArgs); len(args) > 0 {
		params["call_args"] = args
	}
	action.Params = params
	action.Metadata = sourceFunctionMetadata(fn)
	action.Metadata["support_mode"] = string(mode)
	action.Metadata["source_argument_count"] = sourceFunctionArgumentCount(fn)
	action.Metadata["source_call_shape"] = disclosureSafeSourceCallShape(fn)
	for _, key := range []string{
		"execution_blocker",
		"execution_blockers",
		"import_review",
		"promotion_blocker",
		"review_gate",
		"review_reason",
		"review_reasons",
		"review_required",
		"runner_requirement",
		"source",
		"source_protocol",
	} {
		if value, ok := previousMetadata[key]; ok {
			action.Metadata[key] = value
		}
	}
}

func makeEmbeddedTransactionConsumerDisclosureSafe(
	action *dsl.Action,
	fn ParsedFunction,
	mode SourceFunctionSupportMode,
) {
	if action == nil {
		return
	}
	sourceConsumerType := strings.TrimSpace(action.Type)
	action.Name = disclosureSafeSourceReviewActionName(fn)
	action.Type = "source_review"
	action.Method = ""
	action.Target = ""
	action.Selector = ""
	action.ObjectRef = ""
	action.Value = ""
	action.Body = ""
	action.BodyFile = ""
	action.BodyEncoding = ""
	action.Headers = nil
	action.HeaderList = nil
	action.Auth = nil
	action.WaitFor = nil
	action.Sync = nil
	action.Capture = nil
	action.Extractors = nil
	action.Assertions = nil
	action.ThinkTime = dsl.ThinkTimeConfig{}
	action.ThinkTimeAfter = false
	action.Control = nil
	action.TimeoutMs = 0
	action.MaxRedirects = nil
	action.SOAP = nil
	action.Params = map[string]any{
		"function":     fn.Name,
		"support_mode": string(mode),
	}
	if def, ok := lookupSourceFunction(fn.Name); ok && strings.TrimSpace(def.Family) != "" {
		action.Params["family"] = def.Family
	}
	if args := disclosureSafeSourceReviewArgs(fn); len(args) > 0 {
		action.Params["args"] = args
	}
	if args := disclosureSafeStructuredCallArgs(fn.CallArgs); len(args) > 0 {
		action.Params["call_args"] = args
	}
	action.Metadata = sourceFunctionMetadata(fn)
	action.Metadata["support_mode"] = string(mode)
	if sourceConsumerType != "" {
		action.Metadata["source_consumer_type"] = sourceConsumerType
	}
	if fn.containsUnsupportedTransactionCall {
		action.Metadata["embedded_unsupported_transaction_call"] = true
	}
	if fn.dependsOnUnsupportedTransactionFlow {
		action.Metadata["unsupported_transaction_flow_dependency"] = true
	}
	action.Metadata["source_argument_count"] = sourceFunctionArgumentCount(fn)
	action.Metadata["source_call_shape"] = disclosureSafeSourceCallShape(fn)
}

func disclosureSafeSourceFlow(fn ParsedFunction) ParsedFunction {
	out := fn
	out.Conditions = append([]string(nil), fn.Conditions...)
	out.Loops = append([]string(nil), fn.Loops...)
	return out
}

func sourceFunctionArgumentCount(fn ParsedFunction) int {
	if len(fn.CallArgs) > 0 {
		return len(fn.CallArgs)
	}
	return len(sourceControlArgs(fn))
}

func disclosureSafeSourceCallShape(fn ParsedFunction) string {
	return strings.TrimSpace(fn.Name) + "(" + strings.Join(sourceControlArgs(fn), ", ") + ")"
}

func disclosureSafeSourceReviewActionName(fn ParsedFunction) string {
	name := sanitizeName(convertutil.FirstNonEmpty(fn.Name, "source_review"))
	if name == "" {
		name = "source_review"
	}
	if fn.LineNumber > 0 {
		return fmt.Sprintf("%s_%d", name, fn.LineNumber)
	}
	return name
}

func disclosureSafeSourceReviewArgs(fn ParsedFunction) []any {
	values := sourceControlArgs(fn)
	if len(values) == 0 {
		return nil
	}
	args := make([]any, len(values))
	for index, value := range values {
		args[index] = value
	}
	return args
}

func disclosureSafeStructuredCallArgs(args []ParsedArgument) []map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make([]map[string]any, len(args))
	for index, arg := range args {
		kind := strings.ToLower(strings.TrimSpace(arg.Kind))
		switch kind {
		case "string", "key_value", "number", "identifier", "macro", "nested_call", "expression":
		default:
			kind = "expression"
		}
		out[index] = map[string]any{
			"kind":  kind,
			"value": arg.Value,
			"raw":   arg.Raw,
		}
	}
	return out
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
