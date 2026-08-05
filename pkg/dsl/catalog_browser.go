package dsl

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// browserActionSpecs declares the browser protocol action types. The
// validators preserve the exact issue messages and ordering of the former
// switch-based validateBrowserActionIssues: locator checks first, then the
// shared sync-policy rules, then per-type value checks. browser_wait and
// browser_screenshot intentionally skip the sync-policy rules, matching the
// early returns in the original validator.
func browserActionSpecs() []ActionSpec {
	fieldSelector := FieldSpec{Name: "selector", Kind: FieldKindString, Doc: "Inline element locator (CSS/XPath). For a repository-managed object use object_ref instead."}
	fieldObjectRef := FieldSpec{Name: "object_ref", Kind: FieldKindString, Doc: "Object-repository binding: a logical object name (\"name\" or \"repoID:name\") resolved on the controller at script preparation into a concrete selector plus ranked fallbacks. Alternative to an inline selector."}
	fieldSync := FieldSpec{Name: "sync", Kind: FieldKindObject, Doc: "Post-step synchronization: {type (network_idle|dom_stable|element_visible|duration|load|domcontentloaded), value, timeout_ms, stable_ms}."}
	fieldCapture := FieldSpec{Name: "capture", Kind: FieldKindObject, Doc: "Evidence capture: {screenshot, full_page, html}."}
	locatorFields := func(extra ...FieldSpec) []FieldSpec {
		fields := []FieldSpec{
			{Name: "target", Kind: FieldKindString, Doc: "Element locator; target, selector, or object_ref is required."},
			fieldSelector,
			fieldObjectRef,
		}
		fields = append(fields, extra...)
		fields = append(fields, fieldSync, fieldCapture)
		return fields
	}
	// locatorRequireOneOf marks the element-targeting actions as satisfiable by an
	// inline target/selector or an object-repository binding.
	locatorRequireOneOf := []string{"target", "selector", "object_ref"}
	navigationFields := []FieldSpec{
		{Name: "params.wait_until", Kind: FieldKindEnum, Enum: []string{"load", "domcontentloaded", "networkidle", "commit"}, EnumAliases: []string{"network_idle"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Navigation readiness point; defaults to Playwright's load event."},
		fieldSync, fieldCapture,
	}
	navigationValidate := browserValidator(nil, browserNavigationChecks)

	return []ActionSpec{
		{
			Type:         "browser_navigate",
			Protocols:    []string{"browser"},
			Summary:      "Navigates the page to a URL.",
			RequireOneOf: [][]string{{"target", "selector"}},
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Destination URL; target or selector is required."},
				fieldSelector, fieldSync, fieldCapture,
			},
			Validate: browserValidator(browserRequireTargetOrSelector, nil),
		},
		{
			Type:      "browser_back",
			Protocols: []string{"browser"},
			Summary:   "Navigates the current page to the previous browser-history entry.",
			Fields:    navigationFields,
			Validate:  navigationValidate,
		},
		{
			Type:      "browser_forward",
			Protocols: []string{"browser"},
			Summary:   "Navigates the current page to the next browser-history entry.",
			Fields:    navigationFields,
			Validate:  navigationValidate,
		},
		{
			Type:      "browser_reload",
			Protocols: []string{"browser"},
			Summary:   "Reloads the current page.",
			Fields:    navigationFields,
			Validate:  navigationValidate,
		},
		{
			Type:         "browser_open_window",
			Protocols:    []string{"browser"},
			Summary:      "Opens a new browser window or tab and navigates it to a URL.",
			RequireOneOf: [][]string{{"target", "params.url"}},
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Destination URL."},
				{Name: "params.url", Kind: FieldKindString, Doc: "Destination URL alternative to target."},
				fieldSync, fieldCapture,
			},
			Validate: browserValidator(browserOpenWindowChecks, nil),
		},
		{
			Type:         "browser_click",
			Protocols:    []string{"browser"},
			Summary:      "Clicks an element.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "params.button", Kind: FieldKindEnum, Enum: []string{"left", "right", "middle"}, EnumAliases: []string{"primary", "secondary", "auxiliary", "aux"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Mouse button to use; defaults to left. The runtime also accepts primary, secondary, auxiliary, and aux aliases case-insensitively."},
				FieldSpec{Name: "params.modifiers", Kind: FieldKindArray, Doc: "Modifier keys held for the click, such as Control, Shift, Alt, Meta, or ControlOrMeta."},
			),
			Validate: browserValidator(browserRequireElementLocator, browserMouseButtonChecks),
		},
		{
			Type:         "browser_download",
			Protocols:    []string{"browser"},
			Summary:      "Clicks an element and waits for the resulting browser download.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "params.url_contains", Kind: FieldKindString, Doc: "Substring that must appear in the download URL."},
				FieldSpec{Name: "params.filename_contains", Kind: FieldKindString, Doc: "Substring that must appear in the suggested filename."},
				FieldSpec{Name: "params.filename_regex", Kind: FieldKindString, Doc: "Regular expression matched against the suggested filename."},
				FieldSpec{Name: "params.timeout_ms", Kind: FieldKindInt, Maximum: MaxConfiguredProtocolTimeoutMS, Doc: "Maximum wait in milliseconds; defaults to the action timeout."},
				FieldSpec{Name: "params.save_as", Kind: FieldKindString, Doc: "Full local path to save the downloaded file."},
				FieldSpec{Name: "params.save_dir", Kind: FieldKindString, Doc: "Directory where the download should be saved using the suggested filename."},
			),
			Validate: browserValidator(browserRequireElementLocator, browserDownloadChecks),
		},
		{
			Type:         "browser_check",
			Protocols:    []string{"browser"},
			Summary:      "Sets a checkbox or radio element to checked or unchecked.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "value", Kind: FieldKindString, Doc: "Desired state. Use false/unchecked to uncheck; defaults to true."},
				FieldSpec{Name: "params.checked", Kind: FieldKindBool, Doc: "Desired checked state; overrides value when present."},
			),
			Validate: browserValidator(browserRequireElementLocator, nil),
		},
		{
			Type:         "browser_dblclick",
			Protocols:    []string{"browser"},
			Summary:      "Double-clicks an element.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "params.button", Kind: FieldKindEnum, Enum: []string{"left", "right", "middle"}, EnumAliases: []string{"primary", "secondary", "auxiliary", "aux"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Mouse button to use; defaults to left. The runtime also accepts primary, secondary, auxiliary, and aux aliases case-insensitively."},
				FieldSpec{Name: "params.modifiers", Kind: FieldKindArray, Doc: "Modifier keys held for the double-click, such as Control, Shift, Alt, Meta, or ControlOrMeta."},
			),
			Validate: browserValidator(browserRequireElementLocator, browserMouseButtonChecks),
		},
		{
			Type:         "browser_hover",
			Protocols:    []string{"browser"},
			Summary:      "Hovers the pointer over an element.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields:       locatorFields(),
			Validate:     browserValidator(browserRequireElementLocator, nil),
		},
		{
			Type:         "browser_fill",
			Protocols:    []string{"browser"},
			Summary:      "Types a value into an input element.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields:       locatorFields(FieldSpec{Name: "value", Kind: FieldKindString, Doc: "Text to enter; empty string clears the field and template variables are expanded."}),
			Validate:     browserValidator(browserRequireElementLocator, nil),
		},
		{
			Type:         "browser_select",
			Protocols:    []string{"browser"},
			Summary:      "Selects one or more options in a select element.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "value", Kind: FieldKindString, Doc: "Single option value or label to select."},
				FieldSpec{Name: "params.values", Kind: FieldKindArray, Doc: "Option values or labels to select for a multi-select."},
			),
			Validate: browserValidator(browserRequireElementLocator, browserRequireSelectValue),
		},
		{
			Type:      "browser_scroll",
			Protocols: []string{"browser"},
			Summary:   "Scrolls the page or a scrollable element by a pixel delta.",
			Fields: locatorFields(
				FieldSpec{Name: "params.delta_x", Kind: FieldKindInt, Doc: "Horizontal scroll delta in pixels. Positive values scroll right."},
				FieldSpec{Name: "params.delta_y", Kind: FieldKindInt, Doc: "Vertical scroll delta in pixels. Positive values scroll down."},
				FieldSpec{Name: "params.scroll_x", Kind: FieldKindInt, Doc: "Recorded absolute horizontal scroll position after capture; informational."},
				FieldSpec{Name: "params.scroll_y", Kind: FieldKindInt, Doc: "Recorded absolute vertical scroll position after capture; informational."},
			),
			Validate: browserValidator(nil, browserScrollChecks),
		},
		{
			Type:         "browser_set_files",
			Protocols:    []string{"browser"},
			Summary:      "Attaches files to a file input element.",
			RequireOneOf: [][]string{locatorRequireOneOf, {"value", "params.file", "params.path", "params.files"}},
			Fields: locatorFields(
				FieldSpec{Name: "value", Kind: FieldKindString, Doc: "File path; one of value, params.file, params.path, or params.files is required."},
				FieldSpec{Name: "params.file", Kind: FieldKindString, Doc: "Single file path alternative to value."},
				FieldSpec{Name: "params.path", Kind: FieldKindString, Doc: "Single file path alternative to value."},
				FieldSpec{Name: "params.files", Kind: FieldKindArray, Doc: "Multiple file paths."},
			),
			Validate: browserValidator(browserRequireElementLocator, browserRequireSetFilesValue),
		},
		{
			Type:         "browser_press_key",
			Protocols:    []string{"browser"},
			Summary:      "Presses a keyboard key, optionally scoped to an element.",
			RequireOneOf: [][]string{{"value", "params.key", "params.key_name", "params.keyName"}},
			Fields: []FieldSpec{
				fieldSelector,
				{Name: "value", Kind: FieldKindString, Doc: "Key to press; one of value, params.key, or params.key_name is required."},
				{Name: "params.key", Kind: FieldKindString, Doc: "Key name alternative to value."},
				{Name: "params.key_name", Kind: FieldKindString, Doc: "Key name alternative to value."},
				{Name: "params.modifiers", Kind: FieldKindArray, Doc: "Modifier keys held for the press, such as Control, Shift, Alt, Meta, or ControlOrMeta."},
				fieldSync, fieldCapture,
			},
			Validate: browserValidator(nil, browserRequirePressKeyValue),
		},
		{
			Type:         "browser_tap",
			Protocols:    []string{"browser"},
			Summary:      "Taps an element (touch input).",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields:       locatorFields(),
			Validate:     browserValidator(browserRequireElementLocator, nil),
		},
		{
			Type:         "browser_long_press",
			Protocols:    []string{"browser"},
			Summary:      "Long-presses an element (touch input).",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields:       locatorFields(FieldSpec{Name: "params.duration_ms", Kind: FieldKindInt, Maximum: MaxConfiguredProtocolTimeoutMS, Doc: "Press duration in milliseconds; must be greater than zero and at most 3600000."}),
			Validate:     browserValidator(browserRequireElementLocator, browserLongPressChecks),
		},
		{
			Type:         "browser_swipe",
			Protocols:    []string{"browser"},
			Summary:      "Swipes from an element in a direction (touch input).",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "params.direction", Kind: FieldKindEnum, Enum: []string{"left", "right", "up", "down"}, SchemaEnum: true, Doc: "Swipe direction."},
				FieldSpec{Name: "params.distance", Kind: FieldKindInt, Doc: "Swipe distance in pixels; must be greater than zero."},
			),
			Validate: browserValidator(browserRequireElementLocator, browserSwipeChecks),
		},
		{
			Type:         "browser_drag_and_drop",
			Protocols:    []string{"browser"},
			Summary:      "Drags an element onto a drop target.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "params.target", Required: true, Kind: FieldKindString, Doc: "Drop-target locator."},
			),
			Validate: browserValidator(browserDragAndDropChecks, nil),
		},
		{
			Type:         "browser_wait",
			Protocols:    []string{"browser"},
			Summary:      "Waits for a condition, element, or timeout before continuing.",
			RequireOneOf: [][]string{{"wait_for", "selector", "target"}},
			Fields: []FieldSpec{
				{Name: "wait_for", Kind: FieldKindObject, Doc: "Wait condition: {type, value, timeout_ms}. One of wait_for, selector, or target is required."},
				fieldSelector,
				{Name: "target", Kind: FieldKindString, Doc: "Element locator alternative to selector."},
				{Name: "params.operator", Kind: FieldKindEnum, Enum: []string{"=", "==", "===", "!=", "!==", ">", ">=", "<", "<="}, SchemaEnum: true, Doc: "Element-count comparison operator used with params.count; defaults to >= when count is set."},
				{Name: "params.count", Kind: FieldKindInt, Doc: "Required element count for count-based waits; zero or greater."},
			},
			Validate: browserValidator(browserRequireWaitCondition, browserWaitChecks),
		},
		{
			Type:      "browser_wait_response",
			Protocols: []string{"browser"},
			Summary:   "Waits for a browser network response matching URL, method, status, or resource filters.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "URL substring to match."},
				{Name: "params.url_contains", Kind: FieldKindString, Doc: "URL substring to match; target is used when this is empty."},
				{Name: "params.url", Kind: FieldKindString, Doc: "Exact response URL to match."},
				{Name: "params.url_regex", Kind: FieldKindString, Doc: "Regular expression matched against response URL."},
				{Name: "params.method", Kind: FieldKindEnum, Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "HTTP method filter."},
				{Name: "params.status", Kind: FieldKindInt, Doc: "Expected HTTP status code; alias of params.status_code."},
				{Name: "params.status_code", Kind: FieldKindInt, Doc: "Expected HTTP status code."},
				{Name: "params.resource_type", Kind: FieldKindString, Doc: "Browser resource type filter, such as xhr, fetch, document, script, or image."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Maximum: MaxConfiguredProtocolTimeoutMS, Doc: "Maximum wait in milliseconds; defaults to 10000."},
				{Name: "params.include_failed", Kind: FieldKindBool, Doc: "Also allow failed requests with status_code 0 to satisfy the wait."},
				fieldCapture,
			},
			Validate: browserValidator(nil, browserWaitResponseChecks),
		},
		{
			Type:      "browser_set_geolocation",
			Protocols: []string{"browser"},
			Summary:   "Sets the replay browser geolocation for subsequent page actions.",
			Fields: []FieldSpec{
				{Name: "params.latitude", Required: true, Kind: FieldKindFloat, Doc: "Latitude in decimal degrees, between -90 and 90."},
				{Name: "params.longitude", Required: true, Kind: FieldKindFloat, Doc: "Longitude in decimal degrees, between -180 and 180."},
				{Name: "params.accuracy", Kind: FieldKindFloat, Doc: "Non-negative accuracy radius in meters."},
				{Name: "params.grant_permission", Kind: FieldKindBool, Doc: "Grant geolocation permission before setting the location; defaults to true."},
				fieldCapture,
			},
			Validate: browserValidator(nil, browserSetGeolocationChecks),
		},
		{
			Type:      "browser_ws_wait",
			Protocols: []string{"browser"},
			Summary:   "Waits for a WebSocket open event or frame observed in the replayed browser page.",
			Fields: []FieldSpec{
				{Name: "params.url_contains", Kind: FieldKindString, Doc: "Substring that must appear in the WebSocket URL."},
				{Name: "params.direction", Kind: FieldKindEnum, Enum: []string{"open", "sent", "received"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Observed event direction."},
				{Name: "params.match", Kind: FieldKindString, Doc: "Payload match text or regex."},
				{Name: "params.match_mode", Kind: FieldKindEnum, Enum: []string{"contains", "exact", "regex"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Payload comparison mode."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Maximum: MaxConfiguredProtocolTimeoutMS, Doc: "Maximum wait in milliseconds."},
				{Name: "params.count", Kind: FieldKindInt, Doc: "Number of matching events required."},
				fieldCapture,
			},
			Validate: browserValidator(nil, browserWSChecks),
		},
		{
			Type:      "browser_ws_assert",
			Protocols: []string{"browser"},
			Summary:   "Asserts that matching WebSocket events have been observed in the replayed browser page.",
			Fields: []FieldSpec{
				{Name: "params.url_contains", Kind: FieldKindString, Doc: "Substring that must appear in the WebSocket URL."},
				{Name: "params.direction", Kind: FieldKindEnum, Enum: []string{"open", "sent", "received"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Observed event direction."},
				{Name: "params.match", Kind: FieldKindString, Doc: "Payload match text or regex."},
				{Name: "params.match_mode", Kind: FieldKindEnum, Enum: []string{"contains", "exact", "regex"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Payload comparison mode."},
				{Name: "params.count", Kind: FieldKindInt, Doc: "Number of matching events required."},
				fieldCapture,
			},
			Validate: browserValidator(nil, browserWSChecks),
		},
		{
			Type:      "browser_screenshot",
			Protocols: []string{"browser"},
			Summary:   "Captures a screenshot of the current page, or of one element when a locator is set.",
			Fields: []FieldSpec{
				fieldSelector,
				{Name: "target", Kind: FieldKindString, Doc: "Element locator alternative to selector; empty captures the full page."},
				fieldCapture,
			},
		},
		{
			Type:      "browser_evaluate_js",
			Protocols: []string{"browser"},
			Summary:   "Evaluates a reviewed JavaScript snippet in the page (subject to the browser review policy).",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "JavaScript source to evaluate."},
				fieldSync, fieldCapture,
			},
			Validate: browserValidator(func(label string, action Action) []string {
				if strings.TrimSpace(action.Target) == "" {
					return []string{fmt.Sprintf("%s: browser_evaluate_js requires target script", label)}
				}
				return nil
			}, nil),
		},
		{
			Type:         "browser_switch_frame",
			Protocols:    []string{"browser"},
			Summary:      "Switches the action scope into an iframe.",
			RequireOneOf: [][]string{{"selector", "target", "params.frame", "params.frame_selector"}},
			Fields: []FieldSpec{
				fieldSelector,
				{Name: "target", Kind: FieldKindString, Doc: "Frame locator alternative to selector."},
				{Name: "params.frame", Kind: FieldKindAny, Doc: "Frame name or zero-based index."},
				{Name: "params.frame_selector", Kind: FieldKindString, Doc: "Frame element locator."},
				fieldSync, fieldCapture,
			},
			Validate: browserValidator(browserSwitchFrameChecks, nil),
		},
		{
			Type:      "browser_switch_window",
			Protocols: []string{"browser"},
			Summary:   "Switches the action scope to another window or tab.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Window title, URL, or index to switch to."},
				fieldSync, fieldCapture,
			},
			Validate: browserValidator(nil, nil),
		},
		{
			Type:      "browser_handle_dialog",
			Protocols: []string{"browser"},
			Summary:   "Handles the next native dialog (alert/confirm/prompt) by accepting or dismissing it.",
			Fields: []FieldSpec{
				{Name: "params.action", Required: true, Kind: FieldKindEnum, Enum: []string{"accept", "dismiss"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, Doc: "How to respond: accept or dismiss (required)."},
				{Name: "params.dialog_type", Kind: FieldKindEnum, Enum: []string{"alert", "confirm", "prompt", "beforeunload"}, Doc: "Expected dialog kind for documentation/validation."},
				{Name: "params.expected_message", Kind: FieldKindString, Doc: "If set, the captured dialog message must contain this text."},
				{Name: "params.match_mode", Kind: FieldKindEnum, Enum: []string{"exact", "contains", "normalized"}, SchemaEnum: true, EnumCaseFold: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Message comparison mode; recorder-origin dialogs default to contains."},
				{Name: "params.prompt_text", Kind: FieldKindString, Doc: "Text entered into a prompt dialog when accepting."},
				{Name: "params.wait_ms", Kind: FieldKindInt, Maximum: MaxConfiguredProtocolTimeoutMS, Doc: fmt.Sprintf("How long to wait for the dialog to appear (default %d; maximum 3600000).", browserprotocol.DefaultDialogWaitMS)},
				fieldCapture,
			},
			Validate: browserValidator(nil, browserHandleDialogChecks),
		},
	}
}

func validateBrowserActionIssues(index int, action Action) []string {
	if action.Type == "rendezvous" {
		return validateRendezvousActionIssues(index, action)
	}
	spec, ok := protocolActionSpec("browser", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported browser action type %q", index, action.Name, action.Type)}
	}
	var issues []string
	if spec.Validate != nil {
		issues = append(issues, spec.Validate(index, action)...)
	}
	return append(issues, validateBrowserActionTimingIssues(fmt.Sprintf("actions[%d] %q", index, action.Name), action)...)
}

func validateBrowserActionTimingIssues(label string, action Action) []string {
	var issues []string
	if action.WaitFor != nil {
		if action.WaitFor.TimeoutMs < 0 {
			issues = append(issues, fmt.Sprintf("%s: wait_for.timeout_ms must be zero or greater", label))
		} else if action.WaitFor.TimeoutMs > MaxConfiguredProtocolTimeoutMS {
			issues = append(issues, fmt.Sprintf("%s: wait_for.timeout_ms must not exceed %d", label, MaxConfiguredProtocolTimeoutMS))
		}
	}
	for _, key := range []string{"timeout_ms", "duration_ms", "wait_ms"} {
		if value, present := actionParamIntValue(action.Params, key); present && value > MaxConfiguredProtocolTimeoutMS {
			issues = append(issues, fmt.Sprintf("%s: params.%s must not exceed %d", label, key, MaxConfiguredProtocolTimeoutMS))
		}
	}
	return issues
}

func validateBrowserSyncPolicyIssues(label string, sync *BrowserSyncPolicy) []string {
	if sync == nil {
		return nil
	}
	var issues []string
	syncType := strings.ToLower(strings.TrimSpace(sync.Type))
	if syncType == "" {
		syncType = "network_idle"
	}
	if !isAllowedValue(syncType, "network_idle", "networkidle", "dom_stable", "element_visible", "visible", "duration", "sleep", "load", "domcontentloaded") {
		issues = append(issues, fmt.Sprintf("%s: sync.type %q is not supported", label, sync.Type))
	}
	if sync.TimeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: sync.timeout_ms must be zero or greater", label))
	} else if sync.TimeoutMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("%s: sync.timeout_ms must not exceed %d", label, MaxConfiguredProtocolTimeoutMS))
	}
	if sync.StableMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: sync.stable_ms must be zero or greater", label))
	} else if sync.StableMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("%s: sync.stable_ms must not exceed %d", label, MaxConfiguredProtocolTimeoutMS))
	}
	if (syncType == "element_visible" || syncType == "visible") && strings.TrimSpace(sync.Value) == "" {
		issues = append(issues, fmt.Sprintf("%s: sync.value is required for element_visible", label))
	}
	if (syncType == "duration" || syncType == "sleep") && strings.TrimSpace(sync.Value) == "" {
		issues = append(issues, fmt.Sprintf("%s: sync.value is required for duration", label))
	}
	return issues
}

func validateRendezvousActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	if count, ok := actionParamIntValue(action.Params, "count"); ok && count <= 0 {
		issues = append(issues, fmt.Sprintf("%s: rendezvous count must be greater than zero", label))
	}
	if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok {
		if timeoutMs < 0 {
			issues = append(issues, fmt.Sprintf("%s: rendezvous timeout_ms must be zero or greater", label))
		} else if timeoutMs > MaxConfiguredProtocolTimeoutMS {
			issues = append(issues, fmt.Sprintf("%s: rendezvous timeout_ms must not exceed %d", label, MaxConfiguredProtocolTimeoutMS))
		}
	}
	return issues
}

func browserHandleDialogChecks(label string, action Action) []string {
	raw, _ := actionParamStringValue(action.Params, "action")
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "accept", "dismiss":
	case "":
		return []string{fmt.Sprintf("%s: browser_handle_dialog requires params.action (accept or dismiss)", label)}
	default:
		return []string{fmt.Sprintf("%s: browser_handle_dialog params.action must be accept or dismiss, got %q", label, value)}
	}
	if mode, ok := actionParamStringValue(action.Params, "match_mode"); ok {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "", "exact", "contains", "normalized":
		default:
			return []string{fmt.Sprintf("%s: browser_handle_dialog params.match_mode must be exact, contains, or normalized, got %q", label, mode)}
		}
	}
	return nil
}

func browserNavigationChecks(label string, action Action) []string {
	if waitUntil, ok := actionParamStringValue(action.Params, "wait_until"); ok && strings.TrimSpace(waitUntil) != "" {
		value := strings.ToLower(strings.TrimSpace(waitUntil))
		if value == "network_idle" {
			value = "networkidle"
		}
		if !isAllowedValue(value, "load", "domcontentloaded", "networkidle", "commit") {
			return []string{fmt.Sprintf("%s: %s params.wait_until must be load, domcontentloaded, networkidle, or commit", label, action.Type)}
		}
	}
	return nil
}

// browserValidator composes the shared browser validation pipeline: the
// per-type locator phase, then sync-policy checks, then the per-type value
// phase — the same order the original switch-based validator produced.
func browserValidator(locator func(label string, action Action) []string, value func(label string, action Action) []string) func(int, Action) []string {
	return func(index int, action Action) []string {
		label := fmt.Sprintf("actions[%d] %q", index, action.Name)
		var issues []string
		if locator != nil {
			issues = append(issues, locator(label, action)...)
		}
		issues = append(issues, validateBrowserSyncPolicyIssues(label, action.Sync)...)
		if value != nil {
			issues = append(issues, value(label, action)...)
		}
		return issues
	}
}

func browserRequireTargetOrSelector(label string, action Action) []string {
	if action.Target == "" && action.Selector == "" {
		return []string{fmt.Sprintf("%s: missing target or selector", label)}
	}
	return nil
}

func browserRequireWaitCondition(label string, action Action) []string {
	if action.WaitFor == nil && action.Selector == "" && action.Target == "" {
		return []string{fmt.Sprintf("%s: browser_wait requires wait_for, selector, or target", label)}
	}
	return nil
}

func browserOpenWindowChecks(label string, action Action) []string {
	if strings.TrimSpace(action.Target) == "" {
		if urlValue, ok := actionParamStringValue(action.Params, "url"); !ok || strings.TrimSpace(urlValue) == "" {
			return []string{fmt.Sprintf("%s: browser_open_window requires target or params.url", label)}
		}
	}
	return nil
}

func browserMouseButtonChecks(label string, action Action) []string {
	if action.Params == nil {
		return nil
	}
	raw, present := action.Params["button"]
	if !present || raw == nil {
		return nil
	}
	value, ok := raw.(string)
	if !ok {
		return []string{fmt.Sprintf("%s: params.button must be a string", label)}
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "left", "right", "middle", "primary", "secondary", "auxiliary", "aux":
		return nil
	default:
		return []string{fmt.Sprintf("%s: params.button must be one of left, right, middle (aliases primary, secondary, auxiliary, and aux are also accepted), got %q", label, value)}
	}
}

func browserWSChecks(label string, action Action) []string {
	var issues []string
	if direction, ok := actionParamStringValue(action.Params, "direction"); ok && strings.TrimSpace(direction) != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(direction)), "open", "sent", "received") {
		issues = append(issues, fmt.Sprintf("%s: %s params.direction must be open, sent, or received", label, action.Type))
	}
	if mode, ok := actionParamStringValue(action.Params, "match_mode"); ok && strings.TrimSpace(mode) != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(mode)), "contains", "exact", "regex") {
		issues = append(issues, fmt.Sprintf("%s: %s params.match_mode must be contains, exact, or regex", label, action.Type))
	}
	if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: %s params.timeout_ms must be >= 0", label, action.Type))
	}
	if count, ok := actionParamIntValue(action.Params, "count"); ok && count <= 0 {
		issues = append(issues, fmt.Sprintf("%s: %s params.count must be > 0", label, action.Type))
	}
	return issues
}

func browserWaitResponseChecks(label string, action Action) []string {
	var issues []string
	if rawRegex, ok := actionParamStringValue(action.Params, "url_regex"); ok && strings.TrimSpace(rawRegex) != "" {
		if _, err := regexp.Compile(rawRegex); err != nil {
			issues = append(issues, fmt.Sprintf("%s: browser_wait_response params.url_regex is invalid: %v", label, err))
		}
	}
	for _, key := range []string{"status", "status_code"} {
		if statusCode, ok := actionParamIntValue(action.Params, key); ok && statusCode < 0 {
			issues = append(issues, fmt.Sprintf("%s: browser_wait_response params.%s must be >= 0", label, key))
		}
	}
	if method, ok := actionParamStringValue(action.Params, "method"); ok && strings.TrimSpace(method) != "" {
		if !isAllowedValue(strings.ToUpper(strings.TrimSpace(method)), "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS") {
			issues = append(issues, fmt.Sprintf("%s: browser_wait_response params.method must be a valid HTTP method", label))
		}
	}
	if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: browser_wait_response params.timeout_ms must be >= 0", label))
	}
	return issues
}

func browserWaitChecks(label string, action Action) []string {
	var issues []string
	operator, hasOperator := actionParamStringValue(action.Params, "operator")
	_, hasCount := actionParamIntValue(action.Params, "count")
	if hasOperator && !isAllowedValue(operator, "=", "==", "===", "!=", "!==", ">", ">=", "<", "<=") {
		issues = append(issues, fmt.Sprintf("%s: browser_wait params.operator must be one of =, ==, ===, !=, !==, >, >=, <, <=", label))
	}
	if hasOperator && !hasCount {
		issues = append(issues, fmt.Sprintf("%s: browser_wait params.count is required when params.operator is set", label))
	}
	if count, ok := actionParamIntValue(action.Params, "count"); ok && count < 0 {
		issues = append(issues, fmt.Sprintf("%s: browser_wait params.count must be >= 0", label))
	}
	return issues
}

func browserDownloadChecks(label string, action Action) []string {
	var issues []string
	if rawRegex, ok := actionParamStringValue(action.Params, "filename_regex"); ok && strings.TrimSpace(rawRegex) != "" {
		if _, err := regexp.Compile(rawRegex); err != nil {
			issues = append(issues, fmt.Sprintf("%s: browser_download params.filename_regex is invalid: %v", label, err))
		}
	}
	if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: browser_download params.timeout_ms must be >= 0", label))
	}
	if saveAs, hasSaveAs := actionParamStringValue(action.Params, "save_as"); hasSaveAs && strings.TrimSpace(saveAs) != "" {
		if saveDir, hasSaveDir := actionParamStringValue(action.Params, "save_dir"); hasSaveDir && strings.TrimSpace(saveDir) != "" {
			issues = append(issues, fmt.Sprintf("%s: browser_download params.save_as and params.save_dir are mutually exclusive", label))
		}
	}
	return issues
}

func browserSetGeolocationChecks(label string, action Action) []string {
	var issues []string
	latitude, hasLatitude := actionParamFloatValue(action.Params, "latitude")
	longitude, hasLongitude := actionParamFloatValue(action.Params, "longitude")
	if !hasLatitude {
		issues = append(issues, fmt.Sprintf("%s: browser_set_geolocation requires params.latitude", label))
	} else if latitude < -90 || latitude > 90 {
		issues = append(issues, fmt.Sprintf("%s: browser_set_geolocation params.latitude must be between -90 and 90", label))
	}
	if !hasLongitude {
		issues = append(issues, fmt.Sprintf("%s: browser_set_geolocation requires params.longitude", label))
	} else if longitude < -180 || longitude > 180 {
		issues = append(issues, fmt.Sprintf("%s: browser_set_geolocation params.longitude must be between -180 and 180", label))
	}
	if accuracy, ok := actionParamFloatValue(action.Params, "accuracy"); ok && accuracy < 0 {
		issues = append(issues, fmt.Sprintf("%s: browser_set_geolocation params.accuracy must be zero or greater", label))
	}
	return issues
}

// browserRequireElementLocator is the element-action variant of
// browserRequireTargetOrSelector that also accepts an object-repository binding
// (object_ref). The controller resolves object_ref into a concrete selector at
// script preparation, so an action bound only by object_ref is valid at
// authoring time. The message is intentionally identical so existing tooling
// and assertions are unaffected.
func browserRequireElementLocator(label string, action Action) []string {
	if action.Target == "" && action.Selector == "" && strings.TrimSpace(action.ObjectRef) == "" {
		return []string{fmt.Sprintf("%s: missing target or selector", label)}
	}
	return nil
}

func browserDragAndDropChecks(label string, action Action) []string {
	issues := browserRequireElementLocator(label, action)
	if target, ok := actionParamStringValue(action.Params, "target"); !ok || strings.TrimSpace(target) == "" {
		issues = append(issues, fmt.Sprintf("%s: browser_drag_and_drop requires params.target", label))
	}
	return issues
}

func browserSwitchFrameChecks(label string, action Action) []string {
	if action.Target == "" && action.Selector == "" {
		if !browserFrameParamPresent(action.Params, "frame") {
			if frameSelector, ok := actionParamStringValue(action.Params, "frame_selector"); !ok || strings.TrimSpace(frameSelector) == "" {
				return []string{fmt.Sprintf("%s: browser_switch_frame requires selector, target, params.frame, or params.frame_selector", label)}
			}
		}
	}
	return nil
}

func browserFrameParamPresent(params map[string]any, key string) bool {
	if frame, ok := actionParamStringValue(params, key); ok && strings.TrimSpace(frame) != "" {
		return true
	}
	switch value := params[key].(type) {
	case int:
		return value >= 0
	case int64:
		return value >= 0
	case float64:
		return value >= 0 && value == math.Trunc(value)
	case float32:
		asFloat := float64(value)
		return value >= 0 && asFloat == math.Trunc(asFloat)
	default:
		return false
	}
}

func browserRequireValue(label string, action Action) []string {
	if action.Value == "" {
		return []string{fmt.Sprintf("%s: %s requires value", label, action.Type)}
	}
	return nil
}

func browserRequireSelectValue(label string, action Action) []string {
	if strings.TrimSpace(action.Value) != "" {
		return nil
	}
	if browserSelectParamsHaveValue(action.Params) {
		return nil
	}
	if value, ok := actionParamStringValue(action.Params, "value"); ok && strings.TrimSpace(value) != "" {
		return nil
	}
	return []string{fmt.Sprintf("%s: browser_select requires value", label)}
}

func browserSelectParamsHaveValue(params map[string]any) bool {
	if params == nil {
		return false
	}
	raw, ok := params["values"]
	if !ok {
		return false
	}
	hasValue := func(value any) bool {
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed) != ""
		default:
			return strings.TrimSpace(fmt.Sprint(typed)) != ""
		}
	}
	switch typed := raw.(type) {
	case []string:
		for _, value := range typed {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	case []any:
		for _, value := range typed {
			if hasValue(value) {
				return true
			}
		}
	case string:
		for _, part := range strings.Split(typed, ",") {
			if strings.TrimSpace(part) != "" {
				return true
			}
		}
	default:
		return hasValue(raw)
	}
	return false
}

func browserScrollChecks(label string, action Action) []string {
	deltaX, hasX := actionParamIntValue(action.Params, "delta_x")
	deltaY, hasY := actionParamIntValue(action.Params, "delta_y")
	if !hasX {
		deltaX, hasX = actionParamIntValue(action.Params, "x")
	}
	if !hasY {
		deltaY, hasY = actionParamIntValue(action.Params, "y")
	}
	if (!hasX || deltaX == 0) && (!hasY || deltaY == 0) {
		return []string{fmt.Sprintf("%s: browser_scroll requires params.delta_x or params.delta_y", label)}
	}
	return nil
}

func browserRequireSetFilesValue(label string, action Action) []string {
	if action.Value == "" {
		if file, ok := actionParamStringValue(action.Params, "file"); !ok || strings.TrimSpace(file) == "" {
			if path, ok := actionParamStringValue(action.Params, "path"); !ok || strings.TrimSpace(path) == "" {
				if _, ok := action.Params["files"]; !ok {
					return []string{fmt.Sprintf("%s: browser_set_files requires value, params.file, params.path, or params.files", label)}
				}
			}
		}
	}
	return nil
}

func browserRequirePressKeyValue(label string, action Action) []string {
	if strings.TrimSpace(action.Value) == "" {
		if key, ok := actionParamStringValue(action.Params, "key"); !ok || strings.TrimSpace(key) == "" {
			if keyName, ok := actionParamStringValue(action.Params, "key_name"); !ok || strings.TrimSpace(keyName) == "" {
				if camelKeyName, ok := actionParamStringValue(action.Params, "keyName"); !ok || strings.TrimSpace(camelKeyName) == "" {
					return []string{fmt.Sprintf("%s: browser_press_key requires value, params.key, or params.key_name", label)}
				}
			}
		}
	}
	return nil
}

func browserLongPressChecks(label string, action Action) []string {
	if durationMs, ok := actionParamIntValue(action.Params, "duration_ms"); ok && durationMs <= 0 {
		return []string{fmt.Sprintf("%s: browser_long_press duration_ms must be greater than zero", label)}
	}
	return nil
}

func browserSwipeChecks(label string, action Action) []string {
	var issues []string
	if direction, ok := actionParamStringValue(action.Params, "direction"); ok && !isAllowedValue(direction, "left", "right", "up", "down") {
		issues = append(issues, fmt.Sprintf("%s: browser_swipe direction must be left, right, up, or down", label))
	}
	if distance, ok := actionParamIntValue(action.Params, "distance"); ok && distance <= 0 {
		issues = append(issues, fmt.Sprintf("%s: browser_swipe distance must be greater than zero", label))
	}
	return issues
}
