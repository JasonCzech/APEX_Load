package dsl

import (
	"fmt"
	"strings"
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
			Fields:       locatorFields(),
			Validate:     browserValidator(browserRequireElementLocator, nil),
		},
		{
			Type:         "browser_dblclick",
			Protocols:    []string{"browser"},
			Summary:      "Double-clicks an element.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields:       locatorFields(),
			Validate:     browserValidator(browserRequireElementLocator, nil),
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
			Summary:      "Selects an option in a select element.",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields:       locatorFields(FieldSpec{Name: "value", Required: true, Kind: FieldKindString, Doc: "Option value or label to select."}),
			Validate:     browserValidator(browserRequireElementLocator, browserRequireValue),
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
			Fields:       locatorFields(FieldSpec{Name: "params.duration_ms", Kind: FieldKindInt, Doc: "Press duration in milliseconds; must be greater than zero."}),
			Validate:     browserValidator(browserRequireElementLocator, browserLongPressChecks),
		},
		{
			Type:         "browser_swipe",
			Protocols:    []string{"browser"},
			Summary:      "Swipes from an element in a direction (touch input).",
			RequireOneOf: [][]string{locatorRequireOneOf},
			Fields: locatorFields(
				FieldSpec{Name: "params.direction", Kind: FieldKindEnum, Enum: []string{"left", "right", "up", "down"}, Doc: "Swipe direction."},
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
			},
			Validate: func(index int, action Action) []string {
				if action.WaitFor == nil && action.Selector == "" && action.Target == "" {
					return []string{fmt.Sprintf("actions[%d] %q: browser_wait requires wait_for, selector, or target", index, action.Name)}
				}
				return nil
			},
		},
		{
			Type:      "browser_ws_wait",
			Protocols: []string{"browser"},
			Summary:   "Waits for a WebSocket open event or frame observed in the replayed browser page.",
			Fields: []FieldSpec{
				{Name: "params.url_contains", Kind: FieldKindString, Doc: "Substring that must appear in the WebSocket URL."},
				{Name: "params.direction", Kind: FieldKindEnum, Enum: []string{"open", "sent", "received"}, Doc: "Observed event direction."},
				{Name: "params.match", Kind: FieldKindString, Doc: "Payload match text or regex."},
				{Name: "params.match_mode", Kind: FieldKindEnum, Enum: []string{"contains", "exact", "regex"}, Doc: "Payload comparison mode."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Maximum wait in milliseconds."},
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
				{Name: "params.direction", Kind: FieldKindEnum, Enum: []string{"open", "sent", "received"}, Doc: "Observed event direction."},
				{Name: "params.match", Kind: FieldKindString, Doc: "Payload match text or regex."},
				{Name: "params.match_mode", Kind: FieldKindEnum, Enum: []string{"contains", "exact", "regex"}, Doc: "Payload comparison mode."},
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
				{Name: "params.frame", Kind: FieldKindString, Doc: "Frame name or index."},
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
				{Name: "params.action", Kind: FieldKindEnum, Enum: []string{"accept", "dismiss"}, Doc: "How to respond: accept or dismiss (required)."},
				{Name: "params.dialog_type", Kind: FieldKindEnum, Enum: []string{"alert", "confirm", "prompt", "beforeunload"}, Doc: "Expected dialog kind for documentation/validation."},
				{Name: "params.expected_message", Kind: FieldKindString, Doc: "If set, the captured dialog message must contain this text."},
				{Name: "params.match_mode", Kind: FieldKindEnum, Enum: []string{"exact", "contains", "normalized"}, Doc: "Message comparison mode; recorder-origin dialogs default to contains."},
				{Name: "params.prompt_text", Kind: FieldKindString, Doc: "Text entered into a prompt dialog when accepting."},
				{Name: "params.wait_ms", Kind: FieldKindInt, Doc: "How long to wait for the dialog to appear (default 5000)."},
				fieldCapture,
			},
			Validate: browserValidator(nil, browserHandleDialogChecks),
		},
	}
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

func browserOpenWindowChecks(label string, action Action) []string {
	if strings.TrimSpace(action.Target) == "" {
		if urlValue, ok := actionParamStringValue(action.Params, "url"); !ok || strings.TrimSpace(urlValue) == "" {
			return []string{fmt.Sprintf("%s: browser_open_window requires target or params.url", label)}
		}
	}
	return nil
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
		if frame, ok := actionParamStringValue(action.Params, "frame"); !ok || strings.TrimSpace(frame) == "" {
			if frameSelector, ok := actionParamStringValue(action.Params, "frame_selector"); !ok || strings.TrimSpace(frameSelector) == "" {
				return []string{fmt.Sprintf("%s: browser_switch_frame requires selector, target, params.frame, or params.frame_selector", label)}
			}
		}
	}
	return nil
}

func browserRequireValue(label string, action Action) []string {
	if action.Value == "" {
		return []string{fmt.Sprintf("%s: %s requires value", label, action.Type)}
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
