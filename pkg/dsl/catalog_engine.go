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
		{Type: "source_verification", Protocols: []string{"*"}, Summary: "Registers, pauses, or resumes a positional per-VUser HTTP response check.", Fields: []FieldSpec{{Name: "params.operation", Kind: FieldKindString, Required: true, Doc: "register, pause, or resume"}, {Name: "params.id", Kind: FieldKindString, Required: true, Doc: "Stable check ID"}, {Name: "params.text", Kind: FieldKindString}, {Name: "params.scope", Kind: FieldKindString, Doc: "all, body, headers, noresource, or resource"}, {Name: "params.fail_if_found", Kind: FieldKindBool}, {Name: "params.case_insensitive", Kind: FieldKindBool}}, Validate: validateVerificationAction},
		{
			Type:      "control_parallel",
			Protocols: []string{"*"},
			Summary:   "Runs named, isolated protocol branches concurrently and merges extracted variables under var.parallel.<branch>.*.",
			Fields: []FieldSpec{
				{Name: "control.branches", Required: true, Kind: FieldKindArray, Doc: "Two or more uniquely named branches. Each branch contains name and steps."},
				{Name: "control.concurrency", Kind: FieldKindInt, Doc: "Maximum concurrently running branches. Defaults to 4 and cannot exceed 16."},
				{Name: "control.fail_fast", Kind: FieldKindBool, Doc: "Cancel sibling branches after the first failure. Defaults to false."},
			},
		},
		{
			Type:      "control_switch",
			Protocols: []string{"*"},
			Summary:   "Ordered conditional branches: executes the first matching case, or the default branch when none match.",
			Fields: []FieldSpec{
				{Name: "control", Required: true, Kind: FieldKindObject, Doc: "Switch body: {cases:[{condition,steps}], default:[...]}. Cases are evaluated in declaration order with existing CEL/legacy conditions."},
				{Name: "control.cases", Required: true, Kind: FieldKindArray, Doc: "One or more ordered first-match cases. Every case requires a condition and may contain nested actions."},
				{Name: "control.default", Kind: FieldKindArray, Doc: "Optional actions executed when no case matches."},
			},
		},
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
			Type:      "cookie_set",
			Protocols: []string{"*"},
			Summary:   "Sets (or, with params.remove, expires) one cookie in the VUser's HTTP cookie store at this point (imported from web_add_cookie and web_remove_cookie). A later Set-Cookie from the server replaces it. Protocols without a cookie store treat it as a no-op.",
			Fields: []FieldSpec{
				{Name: "params.name", Kind: FieldKindString, Required: true, Doc: "Cookie name."},
				{Name: "params.value", Kind: FieldKindString, Doc: "Cookie value; templates are resolved at run time."},
				{Name: "params.domain", Kind: FieldKindString, Doc: "Domain the cookie is sent to, including subdomains; empty means host-only for the script's base URL host."},
				{Name: "params.path", Kind: FieldKindString, Doc: "Cookie path; defaults to /."},
				{Name: "params.secure", Kind: FieldKindBool, Doc: "Send only over HTTPS."},
				{Name: "params.remove", Kind: FieldKindBool, Doc: "Expire the cookie instead of setting it."},
			},
		},
		{
			Type:      "http_async_start",
			Protocols: []string{"*"},
			Summary:   "Starts a background HTTP conversation (imported from web_reg_async_attributes): params.request is sent repeatedly while the script continues, until http_async_stop or the end of the iteration. Responses are recorded as \"async <id>\" and their extractors and callbacks run on the virtual user between actions. HTTP scripts only.",
			Fields: []FieldSpec{
				{Name: "params.id", Kind: FieldKindString, Required: true, Doc: "Conversation ID used by http_async_stop; at most 8 conversations run per virtual user."},
				{Name: "params.pattern", Kind: FieldKindEnum, Enum: []string{"poll", "long_poll", "push"}, Doc: "poll sends the request every interval_ms; long_poll re-sends it as soon as a response arrives; push re-opens a streaming response (read as server-sent events, see params.stream on HTTP requests) when it ends. Defaults to poll."},
				{Name: "params.interval_ms", Kind: FieldKindInt, Doc: "Poll interval in milliseconds; at least 100. Ignored by long_poll and push, which wait at least 100 ms between requests."},
				{Name: "params.request", Kind: FieldKindObject, Required: true, Doc: "The HTTP request action to repeat. Templates are resolved again after each response, so a value one response extracts is sent by the next request."},
				{Name: "control.branches", Kind: FieldKindArray, Doc: "Callback branches {name, steps} run on the virtual user, at most 64 steps each: on_request before the request is resolved again (at start and after each dispatch of responses), on_response after each response (var.async_response_status, var.async_response_success, var.async_response_body), on_body_chunk for each chunk of a streamed response (var.async_chunk: one server-sent event's data, or the whole body of a response that is not an event stream). var.async_conversation_id names the conversation; http_async_stop inside a callback ends the conversation after the callback."},
			},
		},
		{
			Type:      "http_async_stop",
			Protocols: []string{"*"},
			Summary:   "Stops a background HTTP conversation and applies its last responses (imported from web_stop_async).",
			Fields: []FieldSpec{
				{Name: "params.id", Kind: FieldKindString, Required: true, Doc: "Conversation ID given to http_async_start."},
			},
		},
		{
			Type:      "http_sync",
			Protocols: []string{"*"},
			Summary:   "Waits until a background conversation sets a variable (imported from web_sync), applying conversation responses while it waits.",
			Fields: []FieldSpec{
				{Name: "params.variable", Kind: FieldKindString, Required: true, Doc: "Variable to wait for (without the var. prefix)."},
				{Name: "params.value", Kind: FieldKindString, Doc: "Value to wait for; without it any non-empty value ends the wait."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Maximum wait in milliseconds; defaults to 10000. The step fails when it passes."},
			},
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
