# DSL Action Reference

Generated from the action catalog in `pkg/dsl` — do not edit by hand.
Regenerate with `go generate ./pkg/dsl` (or `go run ./cmd/dslschema`).

Template variables (`{{run.id}}`, `{{env.KEY}}`, `{{data.source.col}}`, `{{var.name}}`, `{{fn.uuid()}}`)
are expanded in string fields at runtime. Legacy `loadrunner_*` action types resolve
to their `source_*` equivalents.

## Common fields (all action types)

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `pacing_ids` | array |  | Source sampler schedule identities; requires JMeter messaging compatibility. Currently execution-gated pending runtime conformance. |
| `name` | string | yes | Unique step name; used in transaction reporting and issue labels. |
| `type` | string | yes | Action type discriminator; see the per-protocol action catalog. |
| `think_time` | object |  | Per-action think-time override (type, mean_ms, ...). |
| `think_time_after` | bool |  | Apply think time after this action completes. |
| `extractors` | array |  | Response extractors: {name, source, type, expression, on_miss, default_value, mode, save_offset, save_length, convert}. on_miss: "fail" fails the request when nothing matches, "empty" assigns an empty string; "default" assigns default_value (including an explicitly empty default); mode "all" saves every match as name_1..name_N plus name_count; save_offset/save_length trim each value (bytes); convert post-processes it (url_encode | url_decode | html_decode | html_url_encode). The behavior fields are honored by the HTTP protocol family. |
| `assertions` | array |  | Response assertions: {type, expected, value, max}. HTTP supports status/time/body checks plus header_contains (value is a case-insensitive header name; expected is a case-sensitive substring) and body_regex_capture_equals (value is a Go/RE2 pattern up to 4 KiB; expected equals the first capture group, or the full match when no group exists). aura_actions_success structurally verifies that the actual outgoing Aura request and response contain unique matching action IDs and SUCCESS states; malformed, incomplete, unknown or over-limit envelopes fail with bounded value-free evidence, and framework error responses (*/…/*ERROR*/ or an exception event) fail as invalid_session, client_out_of_sync or framework_error. Its optional expected, {"recorded": [{"ordinal", "state"}]}, lists actions that failed in the recording: a repeat failure is labelled "also failed in the recording", and config.http.aura_recorded_failures "accept_recorded" accepts one that ends in the recorded state. |
| `metadata` | object |  | Free-form metadata; importers preserve source details here. |
| `timeout_ms` | int |  | Per-action timeout override in milliseconds. |
| `max_redirects` | int |  | Per-action redirect-policy override (-1 disables). |
| `auth` | object |  | Per-action auth override: {type, username, password, token, domain}. |

## Engine actions (any protocol)

Valid in every script, including JMS, Kafka, and Event Hubs; these actions execute in the common engine lifecycle rather than protocol dispatch.

### `cache_clear`

Clears the VUser's simulated HTTP response cache at this point. Protocols without a response cache treat it as a no-op.

### `control_break`

Breaks out of the nearest enclosing control_loop (no-op outside a loop).

### `control_continue`

Skips to the next pass of the nearest enclosing control_loop (no-op outside a loop).

### `control_if`

Conditional block: runs control.steps when the condition holds, control.else otherwise.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `control` | object | yes | Branch body: {condition, steps, else}. condition may use legacy left/op/right or CEL expr over var/data/env. Requires steps or else. |

### `control_loop`

Loop block: repeats control.steps for a fixed count, for each row of a data source, or while a condition holds.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `control` | object | yes | Loop body: {condition, steps, count, max_iterations, for_each, do_while}. Mode precedence is for_each > count > condition; requires a for_each.source, a positive count, or a condition. condition may use legacy left/op/right or CEL expr over var/data/env. Each pass exposes a 1-based {{var.loop_index}} to the body and the condition (e.g. expr "int(var.loop_index) <= int(var.total)"). |
| `control.do_while` | bool |  | Condition mode only: run the body once before the predicate is first evaluated (post-test / run-at-least-once) instead of the default pre-test loop. Useful when the predicate depends on a variable the body itself produces. Ignored for count/for_each modes. |
| `control.for_each.source` | string |  | Data source name to iterate row-by-row. Required when using for_each mode; takes precedence over count and condition. |
| `control.for_each.var` | string |  | Var-namespace prefix each iterated row binds under: row column c is available to the body as {{var.<var>.c}}. Distinct from the per-VUser {{data.source.c}} binding. |
| `control.for_each.condition` | object |  | Optional per-row early-exit predicate {condition}. Iteration stops the first row this evaluates false (the row's columns are bound before it is checked). |

### `control_parallel`

Runs named, isolated protocol branches concurrently and merges extracted variables under var.parallel.<branch>.*.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `control.branches` | array | yes | Two or more uniquely named branches. Each branch contains name and steps. |
| `control.concurrency` | int |  | Maximum concurrently running branches. Defaults to 4 and cannot exceed 16. |
| `control.fail_fast` | bool |  | Cancel sibling branches after the first failure. Defaults to false. |

### `control_switch`

Ordered conditional branches: executes the first matching case, or the default branch when none match.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `control` | object | yes | Switch body: {cases:[{condition,steps}], default:[...]}. Cases are evaluated in declaration order with existing CEL/legacy conditions. |
| `control.cases` | array | yes | One or more ordered first-match cases. Every case requires a condition and may contain nested actions. |
| `control.default` | array |  | Optional actions executed when no case matches. |

### `cookie_clear`

Clears every cookie in the VUser's HTTP session at this point (imported from web_cleanup_cookies). Protocols without a cookie store treat it as a no-op.

### `custom_call`

Imported custom/user C function; logged for manual review during replay.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Custom function name from the source script. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `error_message`

Logs an error-level message rendered from value or a printf-style format.

Requires one of: `value`, `params.format`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `value` | string |  | Literal message. Required unless params.format is set. |
| `params.format` | string |  | Printf-style format rendered with params.args. |
| `params.args` | array |  | Arguments substituted into params.format. |

### `http_connections_close`

Closes the VUser's idle pooled HTTP connections without interrupting active requests. Protocols without a connection pool treat it as a no-op.

### `rendezvous`

Synchronization barrier: VUsers wait until the rendezvous point is reached.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.count` | int |  | Participants to wait for; defaults to all VUsers. Must be greater than zero. |
| `params.timeout_ms` | int |  | Maximum wait in milliseconds; zero or greater. |

### `review_required`

Authoring placeholder for a step that must be resolved by a human before launch; not dispatched by the engine.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.reason` | string |  | Why the step needs review. |

### `source_database`

Placeholder for an imported database call; logged for review during replay.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported database function from the source script. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_external`

Placeholder for an imported external/DLL call; logged for manual review during replay.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported external function from the source script. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_json`

Replays an imported JSON document operation, saving the result into a variable.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported JSON function from the source script. |
| `params.args` | array |  | Positional arguments of the imported source function call. |
| `params.save_param` | string |  | Variable name receiving the extracted value. |

### `source_message`

Logs an informational message from an imported script (lr_output_message family).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `value` | string |  | Literal message. Used when params.format is absent. |
| `params.format` | string |  | Printf-style format rendered with params.args. |
| `params.args` | array |  | Arguments substituted into params.format. |
| `params.level` | enum: debug\|info\|error |  | Log level; defaults to info. |
| `params.status_message` | bool |  | Also publish the message as the VUser status. |

### `source_metric`

Records a user-defined data point imported from a source script (lr_user_data_point).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported metric function; args carry the metric name and value. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_parameter`

Replays imported parameter operations (lr_save_string, lr_param_sprintf, lr_param_increment, ...).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported parameter function, e.g. lr_save_string, lr_save_var, lr_param_sprintf. |
| `params.args` | array |  | Positional arguments of the imported source function call. |
| `params.save_param` | string |  | Variable name receiving the result when the function does not name one. |

### `source_review`

Imported step that converted with caveats; logged for manual review during replay.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Original source function awaiting review. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_runtime`

Replays an imported runtime-control call (lr_abort, lr_exit, IP-spoofing toggles, ...).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported runtime function name, e.g. lr_abort, lr_exit, lr_enable_ip_spoofing. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_secret`

Replays an imported secret/credential operation (lr_decrypt family).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported secret function from the source script. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_timer`

Starts or ends a named timer imported from a source script (lr_start_timer/lr_end_timer).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported timer function: lr_start_timer or lr_end_timer. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_transaction`

Starts, ends, or annotates a named transaction imported from a source script.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported transaction function, e.g. lr_start_transaction, lr_end_transaction, lr_fail_trans_with_error. |
| `params.args` | array |  | Positional arguments of the imported source function call. |

### `source_verification`

Registers, pauses, or resumes a positional per-VUser HTTP response check.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.operation` | string | yes | register, pause, or resume |
| `params.id` | string | yes | Stable check ID |
| `params.text` | string |  |  |
| `params.scope` | string |  | all, body, headers, noresource, or resource |
| `params.fail_if_found` | bool |  |  |
| `params.case_insensitive` | bool |  |  |

### `source_xml`

Replays an imported XML document operation, saving the result into a variable.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.function` | string |  | Imported XML function from the source script. |
| `params.args` | array |  | Positional arguments of the imported source function call. |
| `params.save_param` | string |  | Variable name receiving the extracted value. |

## Protocol: browser

### `browser_back`

Navigates the current page to the previous browser-history entry.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.wait_until` | enum: load\|domcontentloaded\|networkidle\|commit |  | Navigation readiness point; defaults to Playwright's load event. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_check`

Sets a checkbox or radio element to checked or unchecked.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `value` | string |  | Desired state. Use false/unchecked to uncheck; defaults to true. |
| `params.checked` | bool |  | Desired checked state; overrides value when present. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_click`

Clicks an element.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `params.button` | enum: left\|right\|middle |  | Mouse button to use; defaults to left. The runtime also accepts primary, secondary, auxiliary, and aux aliases case-insensitively. |
| `params.modifiers` | array |  | Modifier keys held for the click, such as Control, Shift, Alt, Meta, or ControlOrMeta. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_dblclick`

Double-clicks an element.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `params.button` | enum: left\|right\|middle |  | Mouse button to use; defaults to left. The runtime also accepts primary, secondary, auxiliary, and aux aliases case-insensitively. |
| `params.modifiers` | array |  | Modifier keys held for the double-click, such as Control, Shift, Alt, Meta, or ControlOrMeta. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_download`

Clicks an element and waits for the resulting browser download.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `params.url_contains` | string |  | Substring that must appear in the download URL. |
| `params.filename_contains` | string |  | Substring that must appear in the suggested filename. |
| `params.filename_regex` | string |  | Regular expression matched against the suggested filename. |
| `params.timeout_ms` | int |  | Maximum wait in milliseconds; defaults to the action timeout. |
| `params.save_as` | string |  | Full local path to save the downloaded file. |
| `params.save_dir` | string |  | Directory where the download should be saved using the suggested filename. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_drag_and_drop`

Drags an element onto a drop target.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `params.target` | string | yes | Drop-target locator. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_evaluate_js`

Evaluates a reviewed JavaScript snippet in the page (subject to the browser review policy).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | JavaScript source to evaluate. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_fill`

Fills an input, optionally issuing per-character keyboard events.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `value` | string |  | Text to enter; empty string clears the field and template variables are expanded. |
| `params.input_mode` | enum: fill\|type |  | fill (default) sets the value; type clears the field and sends per-character keyboard events. |
| `params.typing_delay_ms` | int |  | Delay between keys in type mode, 0–1000 milliseconds. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_forward`

Navigates the current page to the next browser-history entry.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.wait_until` | enum: load\|domcontentloaded\|networkidle\|commit |  | Navigation readiness point; defaults to Playwright's load event. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_handle_dialog`

Handles the next native dialog (alert/confirm/prompt) by accepting or dismissing it.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.action` | enum: accept\|dismiss | yes | How to respond: accept or dismiss (required). |
| `params.dialog_type` | enum: alert\|confirm\|prompt\|beforeunload |  | Expected dialog kind for documentation/validation. |
| `params.expected_message` | string |  | If set, the captured dialog message must contain this text. |
| `params.match_mode` | enum: exact\|contains\|normalized |  | Message comparison mode; recorder-origin dialogs default to contains. |
| `params.prompt_text` | string |  | Text entered into a prompt dialog when accepting. |
| `params.wait_ms` | int |  | How long to wait for the dialog to appear (default 1000; maximum 3600000). |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_hover`

Hovers the pointer over an element.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_long_press`

Long-presses an element (touch input).

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `params.duration_ms` | int |  | Press duration in milliseconds; must be greater than zero and at most 3600000. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_navigate`

Navigates the page to a URL.

Requires one of: `target`, `selector`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Destination URL; target or selector is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_open_window`

Opens a new browser window or tab and navigates it to a URL.

Requires one of: `target`, `params.url`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Destination URL. |
| `params.url` | string |  | Destination URL alternative to target. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_press_key`

Presses a keyboard key, optionally scoped to an element.

Requires one of: `value`, `params.key`, `params.key_name`, `params.keyName`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `value` | string |  | Key to press; one of value, params.key, or params.key_name is required. |
| `params.key` | string |  | Key name alternative to value. |
| `params.key_name` | string |  | Key name alternative to value. |
| `params.modifiers` | array |  | Modifier keys held for the press, such as Control, Shift, Alt, Meta, or ControlOrMeta. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_reload`

Reloads the current page.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.wait_until` | enum: load\|domcontentloaded\|networkidle\|commit |  | Navigation readiness point; defaults to Playwright's load event. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_screenshot`

Captures a screenshot of the current page, or of one element when a locator is set.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `target` | string |  | Element locator alternative to selector; empty captures the full page. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_scroll`

Scrolls the page or a scrollable element by a pixel delta.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `params.delta_x` | int |  | Horizontal scroll delta in pixels. Positive values scroll right. |
| `params.delta_y` | int |  | Vertical scroll delta in pixels. Positive values scroll down. |
| `params.scroll_x` | int |  | Recorded absolute horizontal scroll position after capture; informational. |
| `params.scroll_y` | int |  | Recorded absolute vertical scroll position after capture; informational. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_select`

Selects one or more options in a select element.

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `value` | string |  | Single option value or label to select. |
| `params.values` | array |  | Option values or labels to select for a multi-select. |
| `params.option_index` | int |  | Zero-based option index, 0–999999. Cannot be combined with value or values. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_set_files`

Attaches files to a file input element.

Requires one of: `target`, `selector`, `object_ref`

Requires one of: `value`, `params.file`, `params.path`, `params.files`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `value` | string |  | File path; one of value, params.file, params.path, or params.files is required. |
| `params.file` | string |  | Single file path alternative to value. |
| `params.path` | string |  | Single file path alternative to value. |
| `params.files` | array |  | Multiple file paths. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_set_geolocation`

Sets the replay browser geolocation for subsequent page actions.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.latitude` | float | yes | Latitude in decimal degrees, between -90 and 90. |
| `params.longitude` | float | yes | Longitude in decimal degrees, between -180 and 180. |
| `params.accuracy` | float |  | Non-negative accuracy radius in meters. |
| `params.grant_permission` | bool |  | Grant geolocation permission before setting the location; defaults to true. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_swipe`

Swipes from an element in a direction (touch input).

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `params.direction` | enum: left\|right\|up\|down |  | Swipe direction. |
| `params.distance` | int |  | Swipe distance in pixels; must be greater than zero. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_switch_frame`

Switches the action scope into an iframe.

Requires one of: `selector`, `target`, `params.frame`, `params.frame_selector`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `target` | string |  | Frame locator alternative to selector. |
| `params.frame` | any |  | Frame name or zero-based index. |
| `params.frame_selector` | string |  | Frame element locator. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_switch_window`

Switches the action scope to another window or tab.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Window title, URL, or index to switch to. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_tap`

Taps an element (touch input).

Requires one of: `target`, `selector`, `object_ref`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Element locator; target, selector, or object_ref is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `object_ref` | string |  | Object-repository binding: a logical object name ("name" or "repoID:name") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector. |
| `sync` | object |  | Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_wait`

Waits for a condition, element, or timeout before continuing.

Requires one of: `wait_for`, `selector`, `target`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `wait_for` | object |  | Wait condition: {type, value, timeout_ms}. One of wait_for, selector, or target is required. |
| `selector` | string |  | Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead. |
| `target` | string |  | Element locator alternative to selector. |
| `params.operator` | enum: =\|==\|===\|!=\|!==\|>\|>=\|<\|<= |  | Element-count comparison operator used with params.count; defaults to >= when count is set. |
| `params.text` | string |  | Required text substring for wait_for.type=element_text; waits for visible matching text in the selected element/frame. |
| `params.count` | int |  | Required element count for count-based waits; zero or greater. |

### `browser_wait_response`

Waits for a browser network response matching URL, method, status, or resource filters.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | URL substring to match. |
| `params.url_contains` | string |  | URL substring to match; target is used when this is empty. |
| `params.url` | string |  | Exact response URL to match. |
| `params.url_regex` | string |  | Regular expression matched against response URL. |
| `params.method` | enum: GET\|POST\|PUT\|PATCH\|DELETE\|HEAD\|OPTIONS |  | HTTP method filter. |
| `params.status` | int |  | Expected HTTP status code; alias of params.status_code. |
| `params.status_code` | int |  | Expected HTTP status code. |
| `params.resource_type` | string |  | Browser resource type filter, such as xhr, fetch, document, script, or image. |
| `params.timeout_ms` | int |  | Maximum wait in milliseconds; defaults to 10000. |
| `params.include_failed` | bool |  | Also allow failed requests with status_code 0 to satisfy the wait. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_ws_assert`

Asserts that matching WebSocket events have been observed in the replayed browser page.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.url_contains` | string |  | Substring that must appear in the WebSocket URL. |
| `params.direction` | enum: open\|sent\|received |  | Observed event direction. |
| `params.match` | string |  | Payload match text or regex. |
| `params.match_mode` | enum: contains\|exact\|regex |  | Payload comparison mode. |
| `params.count` | int |  | Number of matching events required. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

### `browser_ws_wait`

Waits for a WebSocket open event or frame observed in the replayed browser page.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.url_contains` | string |  | Substring that must appear in the WebSocket URL. |
| `params.direction` | enum: open\|sent\|received |  | Observed event direction. |
| `params.match` | string |  | Payload match text or regex. |
| `params.match_mode` | enum: contains\|exact\|regex |  | Payload comparison mode. |
| `params.timeout_ms` | int |  | Maximum wait in milliseconds. |
| `params.count` | int |  | Number of matching events required. |
| `capture` | object |  | Evidence capture: {screenshot, full_page, html}. |

## Protocol: bruno

### `bruno_run`

Executes a selectable Bruno collection, folder, request, or tag set from the immutable source bundle.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.selection` | enum: collection\|folder\|request\|tag |  | Selection kind; defaults to collection. |
| `params.paths` | array |  | Relative collection paths selected for execution. |
| `params.include_tags` | array |  | Only execute items carrying one of these tags. |
| `params.exclude_tags` | array |  | Exclude items carrying one of these tags. |
| `params.recursive` | bool |  | Recursively execute selected folders. |
| `params.environment` | string |  | Collection environment override. |
| `params.global_environment` | string |  | Workspace global-environment override. |
| `params.data_source` | string |  | CSV/JSON data source or bundled relative path. |
| `params.delay_ms` | int |  | Delay between selected requests. |
| `params.bail` | bool |  | Stop after the first request, test, or assertion failure. |
| `params.tests_only` | bool |  | Run only requests containing executable tests or assertions. |
| `params.execution` | enum: sequential\|parallel |  | Data-iteration execution mode; requests remain in Bruno source order within each iteration. |
| `params.max_parallel` | int |  | Bounded data-iteration concurrency when execution is parallel. |
| `params.persist_session_across_iterations` | bool |  | Retain this VUser's Bruno runtime variables, cookies, and connections between APEX iterations. |
| `params.stream_timeout_ms` | int |  | Deadline for long-lived gRPC or WebSocket work. |

## Protocol: database

### `db_begin`

Begins a transaction on the VUser's connection.

Aliases: `database_begin`, `begin`

### `db_commit`

Commits the open transaction.

Aliases: `database_commit`, `commit`

### `db_exec`

Executes a SQL statement without reading rows (DML/DDL).

Aliases: `database_exec`, `exec`, `execute`, `insert`, `update`, `delete`

Requires one of: `target`, `body`, `body_file`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | SQL statement; target or body is required. |
| `body` | string |  | SQL statement alternative to target (useful for multiline SQL). |
| `body_file` | string |  | Local file containing the SQL statement. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `params.args` | array |  | Positional bind arguments; template variables are expanded. |
| `params.max_rows` | int |  | Row cap override; zero or greater. |

### `db_health`

Pings the database connection.

Aliases: `database_health`, `ping`

### `db_procedure`

Calls a stored procedure.

Aliases: `stored_procedure`

Requires one of: `target`, `body`, `body_file`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | SQL statement; target or body is required. |
| `body` | string |  | SQL statement alternative to target (useful for multiline SQL). |
| `body_file` | string |  | Local file containing the SQL statement. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `params.args` | array |  | Positional bind arguments; template variables are expanded. |
| `params.max_rows` | int |  | Row cap override; zero or greater. |

### `db_query`

Runs a SQL query and reads the result rows.

Aliases: `database_query`, `query`, `select`

Requires one of: `target`, `body`, `body_file`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | SQL statement; target or body is required. |
| `body` | string |  | SQL statement alternative to target (useful for multiline SQL). |
| `body_file` | string |  | Local file containing the SQL statement. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `params.args` | array |  | Positional bind arguments; template variables are expanded. |
| `params.max_rows` | int |  | Row cap override; zero or greater. |

### `db_rollback`

Rolls back the open transaction.

Aliases: `database_rollback`, `rollback`

## Protocol: eventhubs

### `eventhubs_consume`

Reads the next event using the configured observer, checkpointed processor or exclusive epoch consumer.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Event hub; falls back to config.eventhubs.event_hub or params.event_hub. |
| `params.event_hub` | string |  | Event hub alternative to target. |
| `params.consumer_group` | string |  | Consumer group; falls back to config.eventhubs.consumer_group ($Default). |
| `params.timeout_ms` | int |  | Receive timeout in milliseconds; falls back to config.eventhubs.timeout_ms. |
| `config.eventhubs.consumer_mode` | string |  | observer (default), processor with durable checkpoints, or exclusive with one fixed partition and explicit owner_level. |
| `config.eventhubs.owner_level` | int |  | Explicit exclusive consumer epoch, 0 through 9007199254740991. A higher epoch takes partition ownership; loss is terminal for this client. Not accepted in observer/processor mode. |
| `config.eventhubs.start_position` | string |  | Probe start position: latest (default, partition tail — for sustained-load observation) or earliest (partition start — required for produce→consume round-trips so the just-produced event is observed). |

### `eventhubs_produce`

Publishes an event to an event hub (the primary ingestion action).

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Event hub; falls back to config.eventhubs.event_hub or params.event_hub. |
| `params.messages` | array |  | String message list sampled as one action; mutually exclusive with body. Maximum 4096 messages and 64 MiB total. |
| `body` | string |  | Event body; template variables are expanded. |
| `body_file` | string |  | Local file containing the event body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Application properties set on the event. |
| `params.event_hub` | string |  | Event hub alternative to target. |
| `params.partition_key` | string |  | Partition key (hash-routes co-located events); falls back to config.eventhubs.partition_key. |
| `params.partition_id` | string |  | Explicit partition id (mutually exclusive with partition_key); falls back to config.eventhubs.partition_id. |

## Protocol: grpc

### `grpc_bidi_stream`

Bidirectional-streaming gRPC call.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Fully-qualified service/method, e.g. pkg.Service/Method. |
| `body` | string |  | Request message as JSON; template variables are expanded. |
| `body_file` | string |  | Local file containing the request message JSON. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Per-call gRPC metadata merged over config.grpc.metadata. |
| `params.timeout_ms` | int |  | Per-call deadline in milliseconds; zero through 3600000. |
| `params.max_messages` | int |  | Cap on streamed messages; zero through 10000. |

### `grpc_client_stream`

Client-streaming gRPC call.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Fully-qualified service/method, e.g. pkg.Service/Method. |
| `body` | string |  | Request message as JSON; template variables are expanded. |
| `body_file` | string |  | Local file containing the request message JSON. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Per-call gRPC metadata merged over config.grpc.metadata. |
| `params.timeout_ms` | int |  | Per-call deadline in milliseconds; zero through 3600000. |
| `params.max_messages` | int |  | Cap on streamed messages; zero through 10000. |

### `grpc_server_stream`

Server-streaming gRPC call; reads until the stream ends or max_messages.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Fully-qualified service/method, e.g. pkg.Service/Method. |
| `body` | string |  | Request message as JSON; template variables are expanded. |
| `body_file` | string |  | Local file containing the request message JSON. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Per-call gRPC metadata merged over config.grpc.metadata. |
| `params.timeout_ms` | int |  | Per-call deadline in milliseconds; zero through 3600000. |
| `params.max_messages` | int |  | Cap on streamed messages; zero through 10000. |

### `grpc_unary`

Unary gRPC call.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Fully-qualified service/method, e.g. pkg.Service/Method. |
| `body` | string |  | Request message as JSON; template variables are expanded. |
| `body_file` | string |  | Local file containing the request message JSON. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Per-call gRPC metadata merged over config.grpc.metadata. |
| `params.timeout_ms` | int |  | Per-call deadline in milliseconds; zero through 3600000. |
| `params.max_messages` | int |  | Cap on streamed messages; zero through 10000. |

## Protocol: http

### `DELETE`

HTTP DELETE request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `GET`

HTTP GET request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `HEAD`

HTTP HEAD request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `OPTIONS`

HTTP OPTIONS request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `PATCH`

HTTP PATCH request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `POST`

HTTP POST request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `PUT`

HTTP PUT request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `http_oauth2_token`

Fetch and cache an OAuth2 access token for later HTTP requests.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.token_url` | string | yes | OAuth2 token endpoint URL. |
| `params.grant_type` | string | yes | Grant type: client_credentials, jwt_bearer, refresh_token, or password. |
| `params.client_id` | string | yes | OAuth2 client ID or Salesforce connected-app consumer key. |
| `params.client_secret` | string |  | OAuth2 client secret when required by the grant; prefer a {{secret.NAME}} template. |
| `params.scope` | string |  | Space-delimited OAuth2 scopes. |
| `params.audience` | string |  | JWT-bearer audience; defaults to the token endpoint origin. |
| `params.issuer` | string |  | JWT-bearer issuer; defaults to client_id. |
| `params.subject` | string |  | JWT-bearer subject, usually the Salesforce integration username. |
| `params.private_key` | string |  | PEM-encoded RSA private key for JWT-bearer assertions; prefer a {{secret.NAME}} template. |
| `params.refresh_token` | string |  | Refresh token for refresh_token grant; prefer a {{secret.NAME}} template. |
| `params.username` | string |  | Resource owner username for password grant; prefer an {{env.NAME}} template. |
| `params.password` | string |  | Resource owner password for password grant; prefer a {{secret.NAME}} template. |
| `params.save_as` | string |  | VUser variable name for the access token; defaults to access_token. |

### `http_sequence`

Composite HTTP step with sequential execution by default or bounded concurrent requests sharing one VUser session.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.requests` | array | yes | Child HTTP requests with full action fields. At least one required; concurrent mode permits at most 256. Concurrent children are prepared in source order, joined, then publish extractions in source order. |
| `params.execution_mode` | enum: sequential\|concurrent |  | Sequential when absent. Concurrent requests share the parent HTTP session; debugger pause and step occur at the group boundary. |
| `params.max_concurrency` | int |  | Integer 1..16. Concurrent default is min(8, child count). Concurrent preparation is bounded to 16 MiB of source and prepared surfaces and 64 MiB of work. Duplicate extractor names and nonzero child think time are rejected before traffic. |

## Protocol: java_http

### `DELETE`

HTTP DELETE request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `GET`

HTTP GET request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `HEAD`

HTTP HEAD request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `OPTIONS`

HTTP OPTIONS request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `PATCH`

HTTP PATCH request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `POST`

HTTP POST request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `PUT`

HTTP PUT request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

## Protocol: java_vuser

### `java_vuser_run`

Runs a LoadRunner Java Vuser lifecycle through the governed JVM sidecar runner.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Java class name to load, usually Actions. |
| `params.method` | enum: init\|vuser_init\|initialize\|action\|run\|end\|vuser_end\|teardown\|full_lifecycle |  | Source lifecycle method name or legacy full lifecycle sequence to invoke. |
| `params.source_archive_ref` | string |  | Preserved source archive or authoring artifact reference. |
| `params.timeout_ms` | int |  | Per-run timeout in milliseconds (maximum 3600000); falls back to config.java_vuser.action_timeout_ms. |
| `params.runner_requirement` | string |  | Governance gate required before execution, usually java_vuser_jvm_runner. |
| `params.event_contract` | string |  | Runner event stream contract expected from the sidecar shim. |
| `params.shim_event_types` | array |  | Event type names emitted as JSON lines by the lrapi.lr sidecar shim under the declared event contract. |
| `params.lr_parameters` | object |  | Initial LoadRunner parameter values exposed to lrapi.lr.eval_string as {Name}. |

## Protocol: jms

### `jms_ack`

Acknowledges consumed messages in mock mode. Real adapters reject generic JMS settlement; use provider-specific settlement where available.

### `jms_browse`

Browses a queue non-destructively (JMS QueueBrowser); messages are not consumed.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Queue to browse; falls back to config.jms.destination. |
| `params.destination` | string |  | Queue alternative to target; falls back to config.jms.destination. |
| `params.destination_type` | enum: queue\|topic |  | Destination kind; falls back to config.jms.destination_type. |
| `params.subscription` | string |  | Topic subscription name to browse (required when browsing a topic). |
| `params.selector` | string |  | JMS message selector (SQL-92 subset) restricting which messages are browsed. |
| `params.max` | int |  | Maximum messages to browse (default 10). |
| `params.timeout_ms` | int |  | Browse timeout in milliseconds; falls back to config.jms.timeout_ms. |

### `jms_cancel_scheduled`

Cancels scheduled (future-enqueue) Azure Service Bus messages by sequence number. Requires jms_servicebus_real.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Destination the messages were scheduled to; falls back to config.jms.destination. |
| `params.destination` | string |  | Scheduled-message destination alternative to target; falls back to config.jms.destination. |
| `params.sequence_numbers` | any | yes | Positive scheduled sequence numbers to cancel (array, one number, or comma-separated string; values come from jms_scheduled_sequence). |
| `params.timeout_ms` | int |  | Cancellation timeout in milliseconds; falls back to config.jms.timeout_ms. |

### `jms_commit`

Commits the transacted mock session. Real adapters reject generic JMS transaction actions.

### `jms_consume`

Consumes the next message from a queue or topic.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Source queue/topic; falls back to config.jms.destination. |
| `params.destination` | string |  | Source alternative to target; falls back to config.jms.destination. |
| `params.destination_type` | enum: queue\|topic |  | Destination kind; falls back to config.jms.destination_type. |
| `params.subscription` | string |  | Topic subscription name (also the Azure Service Bus topic subscription); falls back to config.jms.subscription. Required to consume from a topic. |
| `params.subscription_mode` | enum: non_durable\|durable\|shared |  | Topic subscription mode. A subscription is identified by name: multiple consumers using the same subscription name compete for its messages (shared), while distinct names each get their own copy (fan-out). durable persists the named subscription on Solace/AMQP; non_durable/shared do not. Service Bus subscriptions are natively durable. |
| `params.timeout_ms` | int |  | Consume timeout in milliseconds; falls back to config.jms.timeout_ms. |
| `params.dead_letter` | bool |  | Consume from / classify the dead-letter destination. |
| `params.sub_queue` | enum: none\|dead_letter\|transfer_dead_letter |  | Azure Service Bus sub-queue to receive from (requires jms_servicebus_real). |
| `params.peek_lock` | bool |  | Azure Service Bus: defer settlement instead of auto-completing, returning a settlement_token for a later jms_settle (requires jms_servicebus_real). |
| `params.session_token` | string |  | Token from jms_session_accept used to consume through an accepted Azure Service Bus session (requires jms_servicebus_real). |
| `params.selector` | string |  | JMS message selector (SQL-92 subset over headers/properties) — only matching messages are consumed. Supported by Solace/AMQP and the mock; Azure Service Bus uses subscription rules instead. |

### `jms_publish`

Publishes a message to a queue or topic.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Destination queue/topic; falls back to config.jms.destination. |
| `body` | string |  | Message payload; template variables are expanded. |
| `body_file` | string |  | Local file containing the message payload. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Message properties/headers. |
| `params.destination` | string |  | Destination alternative to target; falls back to config.jms.destination. |
| `params.destination_type` | enum: queue\|topic |  | Destination kind; falls back to config.jms.destination_type. |
| `params.correlation_id` | string |  | Correlation ID set on the outgoing message. |
| `params.dead_letter` | bool |  | Marks this action as targeting a dead-letter destination (drives dead-letter classification). |
| `params.message_id` | string |  | Explicit message ID (Service Bus duplicate detection uses it); defaults to a generated UUID. |
| `params.session_id` | string |  | Azure Service Bus session ID for session-enabled entities; requires the real Service Bus adapter. |
| `params.partition_key` | string |  | Azure Service Bus partition key (co-locates messages on one partition); requires the real Service Bus adapter. |
| `params.scheduled_enqueue_ms` | int |  | Azure Service Bus scheduled enqueue delay in milliseconds; requires the real Service Bus adapter, which holds the message until then. |
| `params.time_to_live_ms` | int |  | Message time-to-live in milliseconds before the broker expires it. |
| `params.message_type` | enum: text\|bytes\|map\|stream\|object |  | JMS message type. map/stream read params.map_body/params.stream_body; text/bytes/object use body. |
| `params.map_body` | object |  | Named values for a MapMessage (message_type=map); JSON-encoded into the body for replay. |
| `params.stream_body` | array |  | Ordered values for a StreamMessage (message_type=stream); JSON-encoded into the body for replay. |
| `params.delivery_mode` | enum: persistent\|non_persistent |  | Per-message delivery mode; falls back to config.jms.delivery_mode. Applied by the AMQP/Solace adapter. |
| `params.priority` | int |  | JMS message priority 0-9; applied by the AMQP/Solace adapter (Azure Service Bus carries it as a JMSPriority property but does not honor priority ordering). |

### `jms_receive_deferred`

Receives previously deferred Azure Service Bus messages by sequence number (peek-locked). Requires jms_servicebus_real.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Source queue/topic; falls back to config.jms.destination. |
| `params.destination` | string |  | Source queue/topic alternative to target; falls back to config.jms.destination. |
| `params.destination_type` | enum: queue\|topic |  | Source destination kind; falls back to config.jms.destination_type. |
| `params.subscription` | string |  | Topic subscription when receiving deferred messages from a topic. |
| `params.sequence_numbers` | any | yes | Positive sequence numbers to fetch (array of numbers, one number, or a comma-separated string). |
| `params.timeout_ms` | int |  | Receive timeout in milliseconds. |

### `jms_renew_lock`

Renews the broker lock on a peek-locked Azure Service Bus message. Requires jms_servicebus_real.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.settlement_token` | string | yes | Token from a peek_lock jms_consume identifying the in-flight message. |
| `params.timeout_ms` | int |  | Lock-renewal timeout in milliseconds; falls back to config.jms.timeout_ms. |

### `jms_request_reply`

Sends a request message and waits for the correlated reply.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Request destination; falls back to config.jms.destination. |
| `body` | string |  | Request payload; template variables are expanded. |
| `body_file` | string |  | Local file containing the request payload. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Request message properties/headers. |
| `params.reply_to` | string |  | Reply destination; required unless config.jms.reply_to is set. |
| `params.subscription` | string |  | Azure Service Bus topic subscription for consuming replies from a topic; falls back to config.jms.subscription. |
| `params.timeout_ms` | int |  | Reply timeout in milliseconds. |
| `params.destination` | string |  | Request destination alternative to target; falls back to config.jms.destination. |
| `params.destination_type` | enum: queue\|topic |  | Destination kind; falls back to config.jms.destination_type. |
| `params.correlation_id` | string |  | Correlation ID for matching the reply; defaults to the request message ID. |
| `params.dead_letter` | bool |  | Marks the request destination as a dead-letter destination for dependency classification. |
| `params.message_id` | string |  | Explicit request message ID; defaults to a generated UUID. |
| `params.session_id` | string |  | Azure Service Bus session ID for a session-enabled request entity; requires the real Service Bus adapter. |
| `params.partition_key` | string |  | Azure Service Bus partition key for the request message; requires the real Service Bus adapter. |
| `params.scheduled_enqueue_ms` | int |  | Azure Service Bus scheduled enqueue delay for the request message; requires the real Service Bus adapter. |
| `params.time_to_live_ms` | int |  | Request message time-to-live in milliseconds. |
| `params.message_type` | enum: text\|bytes\|map\|stream\|object |  | JMS message type of the request. map/stream read params.map_body/params.stream_body; text/bytes/object use body. |
| `params.map_body` | object |  | Named values for a MapMessage request (message_type=map); JSON-encoded into the body for replay. |
| `params.stream_body` | array |  | Ordered values for a StreamMessage request (message_type=stream); JSON-encoded into the body for replay. |
| `params.delivery_mode` | enum: persistent\|non_persistent |  | Per-message delivery mode of the request; falls back to config.jms.delivery_mode. |
| `params.priority` | int |  | JMS message priority 0-9 of the request; applied by the AMQP/Solace adapter. |

### `jms_rollback`

Rolls back and requeues the transacted mock session. Real adapters reject generic JMS transaction actions.

### `jms_session_accept`

Accepts an Azure Service Bus session (specific or next available), returning a session token. Requires jms_servicebus_real.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Session-enabled queue/topic; falls back to config.jms.destination. |
| `params.destination` | string |  | Session-enabled queue/topic alternative to target; falls back to config.jms.destination. |
| `params.destination_type` | enum: queue\|topic |  | Destination kind; falls back to config.jms.destination_type. |
| `params.session_id` | string |  | Specific session id to accept; empty accepts the next available session. |
| `params.subscription` | string |  | Topic subscription when the destination is a topic. |
| `params.timeout_ms` | int |  | Session-accept timeout in milliseconds; falls back to config.jms.timeout_ms. |

### `jms_session_close`

Closes an accepted Azure Service Bus session and releases its receiver. Requires jms_servicebus_real.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.session_token` | string | yes | Token from jms_session_accept. |
| `params.timeout_ms` | int |  | Session-close timeout in milliseconds; falls back to config.jms.timeout_ms. |

### `jms_session_state`

Gets or sets the session state for an accepted Azure Service Bus session. Requires jms_servicebus_real.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.session_token` | string | yes | Token from jms_session_accept. |
| `params.session_state` | string |  | When set, stores this as the session state; when absent, the current state is read into the jms_session_state extraction. |
| `params.timeout_ms` | int |  | Session-state operation timeout in milliseconds; falls back to config.jms.timeout_ms. |

### `jms_settle`

Settles a peek-locked Azure Service Bus message (complete/abandon/dead-letter/defer). Requires jms_servicebus_real.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.settlement_token` | string | yes | Token from a peek_lock jms_consume identifying the in-flight message. |
| `params.disposition` | enum: complete\|abandon\|dead_letter\|defer |  | How to settle the message (default complete). |
| `params.dead_letter_reason` | string |  | Reason recorded when disposition=dead_letter. |
| `params.dead_letter_description` | string |  | Description recorded when disposition=dead_letter. |
| `params.timeout_ms` | int |  | Settlement operation timeout in milliseconds; falls back to config.jms.timeout_ms. |

## Protocol: kafka

### `kafka_abort_transaction`

Aborts the open producer transaction, discarding buffered records.

### `kafka_begin_transaction`

Begins a transaction (requires config.kafka.transactional_id). consumer_transactions also commits consumed offsets atomically and aborts on ownership loss.

### `kafka_commit`

Commits consumer group offsets.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.consumer_group` | string |  | Consumer group identity; falls back to config.kafka.consumer_group. |

### `kafka_commit_transaction`

Commits the open producer transaction, making buffered records atomically visible (EOS).

### `kafka_consume`

Consumes the next record from a topic.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Topic; falls back to config.kafka.topic or params.topic. |
| `params.topic` | string |  | Topic alternative to target. |
| `params.consumer_group` | string |  | Consumer group override; falls back to config.kafka.consumer_group. |
| `params.timeout_ms` | int |  | Consume timeout in milliseconds; falls back to config.kafka.timeout_ms. |

### `kafka_produce`

Produces a record to a topic.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Topic; falls back to config.kafka.topic or params.topic. |
| `body` | string |  | Record value; template variables are expanded. |
| `body_file` | string |  | Local file containing the record value. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `headers` | object |  | Record headers. |
| `params.topic` | string |  | Topic alternative to target. |
| `params.key` | string |  | Record key (drives key-hash partitioning). |
| `params.partition` | int |  | Explicit partition (with partition_strategy: explicit). |
| `params.correlation_id` | string |  | Explicit record correlation ID; when omitted kafka_request_reply generates one. |

### `kafka_request_reply`

Produces a request record and waits for the correlated reply record.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Request topic; falls back to config.kafka.topic or params.topic. |
| `body` | string |  | Request record value. |
| `body_file` | string |  | Local file containing the request record value. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `params.reply_to` | string | yes | Reply topic. |
| `params.correlation_id` | string |  | Explicit request correlation ID; generated automatically when omitted. |
| `params.timeout_ms` | int |  | Reply timeout in milliseconds. |

### `kafka_seek`

Repositions the consumer cursor. The real adapter requires a configured group and previously polled owned partitions; earliest/latest respect isolation.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.consumer_group` | string |  | Consumer group override; falls back to config.kafka.consumer_group. |
| `params.offset` | any |  | Target offset: a number, "earliest", or "latest". |
| `params.partition` | int |  | Reserved: not yet honored at runtime — seek repositions the whole (topic, group) cursor, not a single partition. |

## Protocol: network

### `dns_query`

Resolves a DNS record.

Aliases: `dns_lookup`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Hostname to resolve. |
| `params.record_type` | enum: A\|AAAA\|CNAME\|MX\|NS\|TXT |  | DNS record type; defaults to A. |

### `ftp_command`

Issues an FTP command on the configured connection.

Requires one of: `target`, `value`, `body`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Command or recipient; one of target, value, or body is required. |
| `value` | string |  | Command argument alternative to target. |
| `body` | string |  | Message body alternative to target. |

### `ftp_login`

Logs in to the configured FTP server.

Requires one of: `target`, `value`, `body`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Command or recipient; one of target, value, or body is required. |
| `value` | string |  | Command argument alternative to target. |
| `body` | string |  | Message body alternative to target. |

### `ftp_noop`

Sends an FTP NOOP keep-alive.

Requires one of: `target`, `value`, `body`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Command or recipient; one of target, value, or body is required. |
| `value` | string |  | Command argument alternative to target. |
| `body` | string |  | Message body alternative to target. |

### `imap_command`

Issues an IMAP command on the configured connection.

Requires one of: `target`, `value`, `body`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Command or recipient; one of target, value, or body is required. |
| `value` | string |  | Command argument alternative to target. |
| `body` | string |  | Message body alternative to target. |

### `ldap_bind`

Binds to the configured LDAP server.

### `ldap_search`

Runs an LDAP search.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Search base DN. |
| `value` | string |  | Search filter. |
| `params.max_entries` | int |  | Result cap; zero or greater. |

### `network_health`

Verifies the configured network endpoint is reachable.

### `pop3_command`

Issues a POP3 command on the configured connection.

Requires one of: `target`, `value`, `body`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Command or recipient; one of target, value, or body is required. |
| `value` | string |  | Command argument alternative to target. |
| `body` | string |  | Message body alternative to target. |

### `smtp_send`

Sends an email through the configured SMTP server.

Requires one of: `target`, `value`, `body`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Command or recipient; one of target, value, or body is required. |
| `value` | string |  | Command argument alternative to target. |
| `body` | string |  | Message body alternative to target. |

### `tcp_request`

Sends bytes over a TCP connection and optionally reads a response.

Aliases: `tcp_send`, `winsock_tcp`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Destination address host:port; defaults to the configured endpoint when set. |
| `body` | string |  | Bytes to send; template variables are expanded. |
| `params.read_bytes` | int |  | Bytes to read back; 0 through 1048576. |
| `params.timeout_ms` | int |  | Socket timeout in milliseconds; zero or greater. |

### `udp_request`

Sends a UDP datagram and optionally reads a response.

Aliases: `udp_send`, `winsock_udp`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Destination address host:port; defaults to the configured endpoint when set. |
| `body` | string |  | Bytes to send; template variables are expanded. |
| `params.read_bytes` | int |  | Bytes to read back; 0 through 1048576. |
| `params.timeout_ms` | int |  | Socket timeout in milliseconds; zero or greater. |

## Protocol: sap_gui

### `sapgui_call`

Preserves a raw SAP GUI scripting call for review-gated replay.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

Requires one of: `target`, `params.method`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Control ID or raw receiver path. |
| `params.method` | string |  | Raw SAP GUI scripting method/property. |
| `params.args` | array |  | Raw argument list. |

### `sapgui_connect`

Connects to a SAP GUI application server or connection entry.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Connection string or SAP GUI connection entry. |
| `params.system` | string |  | Optional SAP system identifier. |

### `sapgui_grid`

Executes a SAP GUI grid operation.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Full SAP GUI control ID. |
| `value` | string |  | Operation value, when applicable. |
| `params.method` | enum: select_row\|select_cell\|double_click_cell\|press_toolbar_button\|set_cell\|sort\|filter |  | Typed operation. |
| `params.row` | int |  | Row index. |
| `params.column` | string |  | Column name or ID. |

### `sapgui_logon`

Logs on to a SAP GUI session.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.client` | string |  | SAP client number. |
| `params.user` | string |  | Recorded username or template. |
| `params.password` | string |  | Password secret template or retained imported value. |
| `params.language` | string |  | SAP logon language. |

### `sapgui_press`

Presses a button or sends a virtual key.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

Requires one of: `target`, `params.vkey`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Button/control ID when pressing a control. |
| `params.vkey` | int |  | SAP GUI virtual key code. |
| `params.description` | string |  | Recorded control description. |

### `sapgui_select`

Selects an item by text, index, or ID.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

Requires one of: `value`, `params.text`, `params.index`, `params.id`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Full SAP GUI control ID. |
| `value` | string |  | Selected text/value. |
| `params.text` | string |  | Selected text. |
| `params.index` | int |  | Selected zero-based index. |
| `params.id` | string |  | Selected SAP item ID. |

### `sapgui_set`

Sets a value on a SAP GUI control.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Full SAP GUI control ID. |
| `value` | string | yes | Value to set. |
| `params.control` | enum: text\|combo\|checkbox\|radio\|button\|grid\|table\|tree\|menu\|shell\|unknown |  | Recorded control family. |
| `params.description` | string |  | Recorded control description. |

### `sapgui_sync`

Waits for SAP GUI status, screen change, or session-idle synchronization.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string |  | Optional control/status target. |
| `params.wait_for` | enum: status_bar\|screen_change\|session_idle |  | Synchronization mode. |
| `params.timeout_ms` | int |  | Timeout in milliseconds. |

### `sapgui_table`

Executes a SAP GUI table operation.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Full SAP GUI control ID. |
| `value` | string |  | Operation value, when applicable. |
| `params.method` | enum: select_row\|set_cell\|get_cell\|press_cell\|scroll |  | Typed operation. |
| `params.row` | int |  | Row index. |
| `params.column` | string |  | Column name or ID. |

### `sapgui_tree`

Executes a SAP GUI tree operation.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Full SAP GUI control ID. |
| `value` | string |  | Operation value, when applicable. |
| `params.method` | enum: select_node\|expand_node\|collapse_node\|double_click_node |  | Typed operation. |
| `params.row` | int |  | Row index. |
| `params.column` | string |  | Column name or ID. |

### `sapgui_verify`

Verifies a SAP GUI control value or status text.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Control or status bar ID. |
| `value` | string | yes | Expected value/text. |
| `params.method` | enum: equals\|contains\|matches |  | Comparison method. |

### `sapgui_window`

Activates, maximizes, resizes, or closes a SAP GUI window.

Execution posture: `external_runner_required` — Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Window ID such as wnd[0]. |
| `params.method` | enum: activate\|maximize\|resize\|close |  | Window operation. |

## Protocol: soap

### `DELETE`

HTTP DELETE request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `GET`

HTTP GET request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `HEAD`

HTTP HEAD request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `OPTIONS`

HTTP OPTIONS request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `PATCH`

HTTP PATCH request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `POST`

HTTP POST request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

### `PUT`

HTTP PUT request.

Protocols: http, soap, java_http

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | Request path or absolute URL; relative paths resolve against config.base_url. |
| `method` | string |  | Explicit HTTP method override; defaults to the action type. |
| `http_version` | enum: auto\|1.1\|2 |  | Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid. |
| `headers` | object |  | Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative. |
| `header_list` | array |  | Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate. |
| `body` | string |  | Request body; template variables are expanded. |
| `body_file` | string |  | Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `body_encoding` | string |  | Body wire encoding: base64 for binary payloads, or windows-1252 for strict text encoding after interpolation. Unrepresentable characters fail. |
| `params.resources` | array |  | Explicit resource GETs belonging to this request: {url, referer?}. Maximum 64; relative URLs use the final response URL. Included in parent timing, cache, and verification. |
| `params` | object |  | Protocol-specific request options. |
| `soap` | object |  | SOAP-only: {action, operation, body_mode (raw_xml|generated)}. |

## Protocol: websocket

### `ws_close`

Closes the WebSocket connection.

### `ws_connect`

Opens the WebSocket connection.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `target` | string | yes | WebSocket URL (ws:// or wss://); relative paths resolve against config.base_url. |
| `headers` | object |  | Handshake request headers. |

### `ws_dispatch`

Waits until the next callback interval and dispatches queued messages serially.

### `ws_on_message`

Registers serial per-VUser message callbacks for the current connection; reconnect requires registration again.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `control` | object | yes | control.steps executes for each queued message; var.ws_message and var.ws_message_type contain the event. |

### `ws_ping`

Sends a ping control frame.

### `ws_receive`

Waits for the next inbound message.

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `params.timeout_ms` | int |  | Receive timeout in milliseconds; zero through 3600000. |

### `ws_send`

Sends a message over the open connection.

Requires one of: `body`, `body_file`

| Field | Kind | Required | Description |
|-------|------|----------|-------------|
| `body` | string |  | Message payload; template variables are expanded. |
| `body_file` | string |  | Local file containing the message payload. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there. |
| `params.type` | enum: text\|binary |  | Message frame type; defaults to text. |

