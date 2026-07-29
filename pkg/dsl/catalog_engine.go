package dsl

// engineActionSpecs declares the engine-level action types that are valid in
// any protocol script (Protocols: "*"). The validator intercepts these before
// protocol dispatch; legacy "loadrunner_*" spellings resolve to the same
// specs via CanonicalActionType. Validation rules for rendezvous,
// error_message, and the control-flow blocks remain in validate.go because
// they need path/protocol context for labels and recursion.
func engineActionSpecs() []ActionSpec {
	sourceFunctionFields := func(functionDoc string) []FieldSpec {
		return []FieldSpec{
			{Name: "params.function", Kind: FieldKindString, Doc: functionDoc},
			{Name: "params.args", Kind: FieldKindArray, Doc: "Positional arguments of the imported source function call."},
		}
	}
	return []ActionSpec{
		{
			Type:      "control_if",
			Protocols: []string{"*"},
			Summary:   "Conditional block: runs control.steps when the condition holds, control.else otherwise.",
			Fields: []FieldSpec{
				{Name: "control", Required: true, Kind: FieldKindObject, Doc: "Branch body: {condition, steps, else}. condition may use legacy left/op/right or CEL expr over var/data/env. Requires steps or else."},
			},
		},
		{
			Type:      "control_loop",
			Protocols: []string{"*"},
			Summary:   "Loop block: repeats control.steps for a fixed count, for each row of a data source, or while a condition holds.",
			Fields: []FieldSpec{
				{Name: "control", Required: true, Kind: FieldKindObject, Doc: "Loop body: {condition, steps, count, max_iterations, for_each, do_while}. Mode precedence is for_each > count > condition; requires a for_each.source, a positive count, or a condition. condition may use legacy left/op/right or CEL expr over var/data/env. Each pass exposes a 1-based {{var.loop_index}} to the body and the condition (e.g. expr \"int(var.loop_index) <= int(var.total)\")."},
				{Name: "control.do_while", Kind: FieldKindBool, Doc: "Condition mode only: run the body once before the predicate is first evaluated (post-test / run-at-least-once) instead of the default pre-test loop. Useful when the predicate depends on a variable the body itself produces. Ignored for count/for_each modes."},
				{Name: "control.for_each.source", Kind: FieldKindString, Doc: "Data source name to iterate row-by-row. Required when using for_each mode; takes precedence over count and condition."},
				{Name: "control.for_each.var", Kind: FieldKindString, Doc: "Var-namespace prefix each iterated row binds under: row column c is available to the body as {{var.<var>.c}}. Distinct from the per-VUser {{data.source.c}} binding."},
				{Name: "control.for_each.condition", Kind: FieldKindObject, Doc: "Optional per-row early-exit predicate {condition}. Iteration stops the first row this evaluates false (the row's columns are bound before it is checked)."},
			},
		},
		{
			Type:      "control_break",
			Protocols: []string{"*"},
			Summary:   "Breaks out of the nearest enclosing control_loop (no-op outside a loop).",
		},
		{
			Type:      "control_continue",
			Protocols: []string{"*"},
			Summary:   "Skips to the next pass of the nearest enclosing control_loop (no-op outside a loop).",
		},
		{
			Type:      "rendezvous",
			Protocols: []string{"*"},
			Summary:   "Synchronization barrier: VUsers wait until the rendezvous point is reached.",
			Fields: []FieldSpec{
				{Name: "params.count", Kind: FieldKindInt, Doc: "Participants to wait for; defaults to all VUsers. Must be greater than zero."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Maximum wait in milliseconds; zero or greater."},
			},
		},
		{
			Type:      "cookie_clear",
			Protocols: []string{"*"},
			Summary:   "Clears every cookie in the VUser's HTTP session at this point (imported from web_cleanup_cookies). Protocols without a cookie store treat it as a no-op.",
		},
		{
			Type:      "cache_clear",
			Protocols: []string{"*"},
			Summary:   "Clears the VUser's simulated HTTP response cache at this point. Protocols without a response cache treat it as a no-op.",
		},
		{
			Type:      "http_connections_close",
			Protocols: []string{"*"},
			Summary:   "Closes the VUser's idle pooled HTTP connections without interrupting active requests. Protocols without a connection pool treat it as a no-op.",
		},
		{
			Type:         "error_message",
			Protocols:    []string{"*"},
			Summary:      "Logs an error-level message rendered from value or a printf-style format.",
			RequireOneOf: [][]string{{"value", "params.format"}},
			Fields: []FieldSpec{
				{Name: "value", Kind: FieldKindString, Doc: "Literal message. Required unless params.format is set."},
				{Name: "params.format", Kind: FieldKindString, Doc: "Printf-style format rendered with params.args."},
				{Name: "params.args", Kind: FieldKindArray, Doc: "Arguments substituted into params.format."},
			},
		},
		{
			Type:      "source_message",
			Protocols: []string{"*"},
			Summary:   "Logs an informational message from an imported script (lr_output_message family).",
			Fields: []FieldSpec{
				{Name: "value", Kind: FieldKindString, Doc: "Literal message. Used when params.format is absent."},
				{Name: "params.format", Kind: FieldKindString, Doc: "Printf-style format rendered with params.args."},
				{Name: "params.args", Kind: FieldKindArray, Doc: "Arguments substituted into params.format."},
				{Name: "params.level", Kind: FieldKindEnum, Enum: []string{"debug", "info", "error"}, Doc: "Log level; defaults to info."},
				{Name: "params.status_message", Kind: FieldKindBool, Doc: "Also publish the message as the VUser status."},
			},
		},
		{
			Type:      "source_runtime",
			Protocols: []string{"*"},
			Summary:   "Replays an imported runtime-control call (lr_abort, lr_exit, IP-spoofing toggles, ...).",
			Fields:    sourceFunctionFields("Imported runtime function name, e.g. lr_abort, lr_exit, lr_enable_ip_spoofing."),
		},
		{
			Type:      "source_transaction",
			Protocols: []string{"*"},
			Summary:   "Starts, ends, or annotates a named transaction imported from a source script.",
			Fields:    sourceFunctionFields("Imported transaction function, e.g. lr_start_transaction, lr_end_transaction, lr_fail_trans_with_error."),
		},
		{
			Type:      "source_timer",
			Protocols: []string{"*"},
			Summary:   "Starts or ends a named timer imported from a source script (lr_start_timer/lr_end_timer).",
			Fields:    sourceFunctionFields("Imported timer function: lr_start_timer or lr_end_timer."),
		},
		{
			Type:      "source_metric",
			Protocols: []string{"*"},
			Summary:   "Records a user-defined data point imported from a source script (lr_user_data_point).",
			Fields:    sourceFunctionFields("Imported metric function; args carry the metric name and value."),
		},
		{
			Type:      "source_parameter",
			Protocols: []string{"*"},
			Summary:   "Replays imported parameter operations (lr_save_string, lr_param_sprintf, lr_param_increment, ...).",
			Fields: append(sourceFunctionFields("Imported parameter function, e.g. lr_save_string, lr_save_var, lr_param_sprintf."),
				FieldSpec{Name: "params.save_param", Kind: FieldKindString, Doc: "Variable name receiving the result when the function does not name one."}),
		},
		{
			Type:      "source_json",
			Protocols: []string{"*"},
			Summary:   "Replays an imported JSON document operation, saving the result into a variable.",
			Fields: append(sourceFunctionFields("Imported JSON function from the source script."),
				FieldSpec{Name: "params.save_param", Kind: FieldKindString, Doc: "Variable name receiving the extracted value."}),
		},
		{
			Type:      "source_xml",
			Protocols: []string{"*"},
			Summary:   "Replays an imported XML document operation, saving the result into a variable.",
			Fields: append(sourceFunctionFields("Imported XML function from the source script."),
				FieldSpec{Name: "params.save_param", Kind: FieldKindString, Doc: "Variable name receiving the extracted value."}),
		},
		{
			Type:      "source_database",
			Protocols: []string{"*"},
			Summary:   "Placeholder for an imported database call; logged for review during replay.",
			Fields:    sourceFunctionFields("Imported database function from the source script."),
		},
		{
			Type:      "source_secret",
			Protocols: []string{"*"},
			Summary:   "Replays an imported secret/credential operation (lr_decrypt family).",
			Fields:    sourceFunctionFields("Imported secret function from the source script."),
		},
		{
			Type:      "source_external",
			Protocols: []string{"*"},
			Summary:   "Placeholder for an imported external/DLL call; logged for manual review during replay.",
			Fields:    sourceFunctionFields("Imported external function from the source script."),
		},
		{
			Type:      "source_review",
			Protocols: []string{"*"},
			Summary:   "Imported step that converted with caveats; logged for manual review during replay.",
			Fields:    sourceFunctionFields("Original source function awaiting review."),
		},
		{
			Type:      "custom_call",
			Protocols: []string{"*"},
			Summary:   "Imported custom/user C function; logged for manual review during replay.",
			Fields:    sourceFunctionFields("Custom function name from the source script."),
		},
		{
			Type:      "review_required",
			Protocols: []string{"*"},
			Summary:   "Authoring placeholder for a step that must be resolved by a human before launch; not dispatched by the engine.",
			Fields: []FieldSpec{
				{Name: "params.reason", Kind: FieldKindString, Doc: "Why the step needs review."},
			},
		},
	}
}
