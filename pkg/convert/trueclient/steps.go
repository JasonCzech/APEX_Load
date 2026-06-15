package trueclient

// steps.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func promoteFrameContext(action *dsl.Action) {
	if action == nil || action.Metadata == nil {
		return
	}
	frame := firstNonEmptyString(stringFromAny(action.Metadata["source_frame_context"]), stringFromAny(action.Metadata["frame_context"]))
	selector := normalizeTruClientFrameSelector(frame)
	if selector == "" {
		return
	}
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	action.Params["frame_selector"] = selector
	action.Metadata["frame_selector"] = selector
}

func switchWindowForAction(action *dsl.Action, state *truConvertState) (dsl.Action, bool) {
	if action == nil || state == nil || action.Metadata == nil {
		return dsl.Action{}, false
	}
	context := firstNonEmptyString(stringFromAny(action.Metadata["source_window_context"]), stringFromAny(action.Metadata["window_context"]))
	if context == state.currentWindowContext {
		return dsl.Action{}, false
	}
	state.currentWindowContext = context
	return switchWindowAction(context, "Switch window"), true
}

func switchWindowAction(context string, name string) dsl.Action {
	action := dsl.Action{
		Name: name,
		Type: "browser_switch_window",
		Metadata: map[string]any{
			"source": "truclient_window_context",
		},
	}
	context = strings.TrimSpace(context)
	if context == "" {
		action.Params = map[string]any{"index": 0}
		return action
	}
	if index, err := strconv.Atoi(context); err == nil {
		action.Params = map[string]any{"index": index}
		return action
	}
	action.Target = context
	action.Params = map[string]any{"target": context}
	return action
}

func normalizeTruClientFrameSelector(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "iframe") || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "[") || strings.HasPrefix(lower, "css=") || strings.HasPrefix(lower, "xpath=") || strings.HasPrefix(value, "//") {
		return value
	}
	escaped := strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
	return fmt.Sprintf(`iframe[name="%s"], iframe[id="%s"], iframe[title="%s"]`, escaped, escaped, escaped)
}

func convertStep(step truXMLStep, descriptors []ObjectDescriptor) ([]dsl.Action, []string, bool, error) {
	switch strings.TrimSpace(step.Action) {
	case "Navigate":
		location, err := parseArgumentValue(step.Arguments, "Location")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Navigate step arguments: %w", err)
		}
		location = trimQuotedValue(location)
		if location == "" {
			return nil, nil, false, fmt.Errorf("TruClient Navigate step is missing a Location value")
		}
		return []dsl.Action{{
			Name:   stepDisplayName(step, "Navigate"),
			Type:   "browser_navigate",
			Target: location,
		}}, nil, false, nil
	case "Click", "Double Click", "Double-click", "DoubleClick", "Submit", "Check", "Uncheck", "Select Radio", "Select Checkbox", "Radio", "Checkbox":
		action, err := convertClickLikeStep(step, descriptors)
		if err != nil {
			return nil, nil, false, err
		}
		var warnings []string
		if reviewRequired(action.Metadata) {
			warnings = append(warnings, reviewGatedClickWarning(step, action.Metadata))
		}
		return []dsl.Action{action}, warnings, false, nil
	case "Hover", "Mouse Over", "MouseOver":
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Hover step arguments: %w", err)
		}
		selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
		if selector == "" {
			return nil, nil, false, fmt.Errorf("TruClient Hover step is missing a selector value")
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Hover"),
			Type:     "browser_hover",
			Target:   selector,
			Selector: selector,
		}}, nil, false, nil
	case "Set / Type", "Set", "Type":
		if strings.EqualFold(strings.TrimSpace(step.Action), "Set") {
			if action, ok, err := convertSetFilesStep(step, descriptors); err != nil {
				return nil, nil, false, err
			} else if ok {
				return []dsl.Action{action}, nil, false, nil
			}
		}
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Set / Type step selector arguments: %w", err)
		}
		value, err := parseFirstArgumentValue(step.Arguments, "Value", "value", "Text", "text")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Set / Type step value arguments: %w", err)
		}
		selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
		value = trimQuotedValue(value)
		if selector == "" {
			return nil, nil, false, fmt.Errorf("TruClient Set / Type step is missing a selector value")
		}
		if value == "" {
			return nil, nil, false, fmt.Errorf("TruClient Set / Type step is missing a value")
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Set / Type"),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}}, nil, false, nil
	case "Select":
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Select step selector arguments: %w", err)
		}
		value, err := parseFirstArgumentValue(step.Arguments, "Value", "value", "Text", "text", "VisibleText", "visible_text", "Selected Value", "selected value", "SelectedValue", "selectedvalue", "Option", "option", "Selection", "selection")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Select step value arguments: %w", err)
		}
		selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
		value = trimQuotedValue(value)
		if selector == "" {
			action := unresolvedBrowserStepAction(step, "Select", "missing_selector")
			return []dsl.Action{action}, []string{reviewGatedStepWarning(step, action.Metadata)}, false, nil
		}
		if value == "" {
			action := unresolvedBrowserStepAction(step, "Select", "missing_select_value")
			action.Metadata["source_selector"] = selector
			return []dsl.Action{action}, []string{reviewGatedStepWarning(step, action.Metadata)}, false, nil
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Select"),
			Type:     "browser_select",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}}, nil, false, nil
	case "Press Key":
		action, err := convertPressKeyStep(step, descriptors)
		if err != nil {
			return nil, nil, false, err
		}
		return []dsl.Action{action}, nil, false, nil
	case "Scroll":
		action, err := convertScrollStep(step)
		if err != nil {
			return nil, nil, false, err
		}
		return []dsl.Action{action}, nil, false, nil
	case "Wait":
		waitAction, ok, err := convertWaitStep(step, descriptors)
		if err != nil {
			return nil, nil, false, err
		}
		if ok {
			return []dsl.Action{waitAction}, nil, false, nil
		}
		return nil, []string{fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", strings.TrimSpace(step.Action))}, true, nil
	case "Evaluate JavaScript":
		code, err := parseArgumentValue(step.Arguments, "Code")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Evaluate JavaScript arguments: %w", err)
		}
		actions, unsupportedLines := convertEvaluateJavaScript(code, stepDisplayName(step, "Evaluate JavaScript"))
		if len(actions) == 0 {
			warnings := evaluateJavaScriptUnsupportedWarnings(unsupportedLines)
			if len(warnings) == 0 {
				warnings = append(warnings, fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", strings.TrimSpace(step.Action)))
			}
			return nil, warnings, true, nil
		}
		warnings := evaluateJavaScriptUnsupportedWarnings(unsupportedLines)
		return actions, warnings, false, nil
	default:
		actionName := strings.TrimSpace(step.Action)
		if actionName == "" {
			return nil, nil, false, nil
		}
		return nil, []string{fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", actionName)}, true, nil
	}
}

func convertPressKeyStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, error) {
	keyName, err := parseFirstArgumentValue(step.Arguments, "Key Name", "KeyName", "Key", "key")
	if err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Press Key arguments: %w", err)
	}
	keyName = trimQuotedValue(keyName)
	if keyName == "" {
		return dsl.Action{}, fmt.Errorf("TruClient Press Key step is missing a key name")
	}
	selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
	if err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Press Key selector arguments: %w", err)
	}
	selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
	action := dsl.Action{
		Name:  stepDisplayName(step, "Press Key"),
		Type:  "browser_press_key",
		Value: keyName,
		Metadata: map[string]any{
			"source":     "truclient_press_key",
			"source_key": keyName,
		},
	}
	if selector != "" {
		action.Target = selector
		action.Selector = selector
	}
	return action, nil
}

func convertSetFilesStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, bool, error) {
	rawPath, err := parseFirstArgumentValue(step.Arguments, "Path", "path", "File", "file", "File Path", "file_path")
	if err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Set file path arguments: %w", err)
	}
	path := trimQuotedValue(rawPath)
	if path == "" {
		return dsl.Action{}, false, nil
	}
	selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
	if err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Set file selector arguments: %w", err)
	}
	selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
	if selector == "" {
		return dsl.Action{}, false, fmt.Errorf("TruClient Set file step is missing a selector value")
	}
	return dsl.Action{
		Name:     stepDisplayName(step, "Set File"),
		Type:     "browser_set_files",
		Target:   selector,
		Selector: selector,
		Value:    path,
		Metadata: map[string]any{
			"source":           "truclient_set_file",
			"source_file_path": path,
		},
	}, true, nil
}

func convertClickLikeStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, error) {
	actionName := strings.TrimSpace(step.Action)
	selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
	if err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient %s step arguments: %w", actionName, err)
	}
	selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
	if selector == "" {
		if x, y, ok := stepCoordinates(step); ok {
			return coordinateClickAction(step, actionName, x, y), nil
		}
		return unresolvedClickAction(step, actionName), nil
	}
	action := dsl.Action{
		Name:     stepDisplayName(step, firstNonEmptyString(actionName, "Click")),
		Type:     "browser_click",
		Target:   selector,
		Selector: selector,
	}
	params := map[string]any{}
	metadata := map[string]any{
		"source": "truclient_" + stableActionToken(actionName),
	}
	switch strings.ToLower(strings.ReplaceAll(actionName, "-", " ")) {
	case "double click", "doubleclick":
		params["click_count"] = 2
	case "check", "select checkbox", "checkbox", "select radio", "radio":
		params["checked"] = true
	case "uncheck":
		params["checked"] = false
	case "submit":
		metadata["submit"] = true
	}
	if len(params) > 0 {
		action.Params = params
	}
	action.Metadata = metadata
	return action, nil
}

func unresolvedClickAction(step truXMLStep, actionName string) dsl.Action {
	return unresolvedBrowserStepAction(step, firstNonEmptyString(actionName, "Click"), "missing_selector")
}

func unresolvedBrowserStepAction(step truXMLStep, fallback string, reason string) dsl.Action {
	actionName := firstNonEmptyString(strings.TrimSpace(step.Action), fallback)
	return dsl.Action{
		Name:   stepDisplayName(step, fallback),
		Type:   "browser_evaluate_js",
		Target: "(() => null)()",
		Metadata: map[string]any{
			"source":          "truclient_" + stableActionToken(actionName),
			"review_required": true,
			"review_reason":   reason,
		},
	}
}

func reviewGatedClickWarning(step truXMLStep, metadata map[string]any) string {
	actionName := firstNonEmptyString(strings.TrimSpace(step.Action), "Click")
	displayName := stepDisplayName(step, "Click")
	switch stringFromAny(metadata["review_reason"]) {
	case "selectorless_coordinate_click":
		return fmt.Sprintf("TruClient %s step %q was converted as a review-gated coordinate click because no selector was available", actionName, displayName)
	case "missing_selector":
		return fmt.Sprintf("TruClient %s step %q has no selector or coordinate fallback; a review-gated placeholder was preserved for manual repair", actionName, displayName)
	default:
		return fmt.Sprintf("TruClient %s step %q was converted with a review gate", actionName, displayName)
	}
}

func reviewGatedStepWarning(step truXMLStep, metadata map[string]any) string {
	actionName := firstNonEmptyString(strings.TrimSpace(step.Action), "step")
	displayName := stepDisplayName(step, actionName)
	switch stringFromAny(metadata["review_reason"]) {
	case "missing_selector":
		return fmt.Sprintf("TruClient %s step %q has no selector; a review-gated placeholder was preserved for manual repair", actionName, displayName)
	case "missing_select_value":
		return fmt.Sprintf("TruClient %s step %q has no selected value; a review-gated placeholder was preserved for manual repair", actionName, displayName)
	default:
		return fmt.Sprintf("TruClient %s step %q was converted with a review gate", actionName, displayName)
	}
}

func reviewRequired(metadata map[string]any) bool {
	value, _ := metadata["review_required"].(bool)
	return value
}

func stepCoordinates(step truXMLStep) (int, int, bool) {
	values, err := parseArgumentValues(step.Arguments)
	if err != nil {
		return 0, 0, false
	}
	x := parseSignedInt(trimArgumentValue(values, "X Coordinate", "X", "x", "ClientX", "client_x"))
	y := parseSignedInt(trimArgumentValue(values, "Y Coordinate", "Y", "y", "ClientY", "client_y"))
	if x == 0 && y == 0 {
		return 0, 0, false
	}
	return x, y, true
}

func coordinateClickAction(step truXMLStep, actionName string, x int, y int) dsl.Action {
	script := fmt.Sprintf(`(() => {
  const x = %d;
  const y = %d;
  const el = document.elementFromPoint(x, y);
  if (!el) {
    throw new Error("No element found at TruClient coordinate click " + x + "," + y);
  }
  el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, clientX: x, clientY: y }));
  return el.tagName;
})()`, x, y)
	return dsl.Action{
		Name:   stepDisplayName(step, firstNonEmptyString(actionName, "Click")),
		Type:   "browser_evaluate_js",
		Target: script,
		Params: map[string]any{
			"x": x,
			"y": y,
		},
		Metadata: map[string]any{
			"source":                  "truclient_" + stableActionToken(actionName),
			"review_required":         true,
			"review_reason":           "selectorless_coordinate_click",
			"source_coordinate_click": true,
		},
	}
}

func convertScrollStep(step truXMLStep) (dsl.Action, error) {
	selector := "body"
	if rawSelector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step selector arguments: %w", err)
	} else if rawSelector = trimQuotedValue(rawSelector); rawSelector != "" {
		selector = rawSelector
	}

	direction := ""
	if rawDirection, err := parseFirstArgumentValue(step.Arguments, "Direction", "direction"); err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step direction arguments: %w", err)
	} else if rawDirection = strings.ToLower(strings.TrimSpace(trimQuotedValue(rawDirection))); rawDirection != "" {
		direction = normalizeScrollDirection(rawDirection)
	}

	distance := 0
	if rawDistance, err := parseFirstArgumentValue(step.Arguments, "Distance", "distance", "Amount", "amount", "Pixels", "pixels"); err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step distance arguments: %w", err)
	} else {
		distance = parsePositiveInt(trimQuotedValue(rawDistance))
	}

	if direction == "" {
		x := 0
		y := 300
		if rawX, err := parseFirstArgumentValue(step.Arguments, "X", "x", "DeltaX", "delta_x", "Horizontal", "horizontal"); err != nil {
			return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step X arguments: %w", err)
		} else if parsed := parseSignedInt(trimQuotedValue(rawX)); parsed != 0 {
			x = parsed
		}
		if rawY, err := parseFirstArgumentValue(step.Arguments, "Y", "y", "DeltaY", "delta_y", "Vertical", "vertical"); err != nil {
			return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step Y arguments: %w", err)
		} else if parsed := parseSignedInt(trimQuotedValue(rawY)); parsed != 0 {
			y = parsed
		}
		direction, distance = scrollGesture(float64(x), float64(y))
	}
	if direction == "" {
		direction = "up"
	}
	if distance <= 0 {
		distance = 300
	}

	return dsl.Action{
		Name:     stepDisplayName(step, "Scroll"),
		Type:     "browser_swipe",
		Target:   selector,
		Selector: selector,
		Params: map[string]any{
			"direction": direction,
			"distance":  distance,
		},
		Metadata: map[string]any{
			"source": "truclient_scroll",
		},
	}, nil
}

func attachSourceStepMetadata(action *dsl.Action, step truXMLStep, transaction string, descriptors []ObjectDescriptor) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	if _, ok := action.Metadata["source"]; !ok {
		action.Metadata["source"] = "truclient_" + stableActionToken(step.Action)
	}
	if value := strings.TrimSpace(step.StepID); value != "" {
		action.Metadata["source_step_id"] = value
	}
	if value := strings.TrimSpace(step.Index); value != "" {
		action.Metadata["source_step_index"] = value
	}
	if value := strings.TrimSpace(step.Action); value != "" {
		action.Metadata["source_action"] = value
	}
	if value := strings.TrimSpace(step.ActionName); value != "" {
		action.Metadata["source_action_name"] = value
	}
	if value := stepObjectDescriptorID(step); value != "" {
		action.Metadata["source_object_descriptor_id"] = value
	}
	if candidates, _ := rankedSelectorCandidates(step, descriptors); len(candidates) > 0 {
		action.Metadata["source_selector_candidates"] = candidates
		action.Metadata["source_selector_confidence"] = candidates[0].Confidence
		if len(candidates) > 1 {
			action.Metadata["source_selector_fallbacks"] = candidates[1:]
		}
	}
	if values, err := parseArgumentValues(step.Arguments); err == nil {
		if value := trimArgumentValue(values, "Frame", "Frame ID", "FrameContext", "Iframe", "Iframe ID"); value != "" {
			action.Metadata["source_frame_context"] = value
		}
		if value := trimArgumentValue(values, "Window", "Window ID", "WindowContext", "Tab", "Browser Window"); value != "" {
			action.Metadata["source_window_context"] = value
		}
		if refs := parameterReferences(values); len(refs) > 0 {
			action.Metadata["source_parameter_refs"] = refs
		}
		if refs := snapshotReferences(values); len(refs) > 0 {
			action.Metadata["source_snapshot_refs"] = refs
		}
	}
	if value := strings.TrimSpace(transaction); value != "" {
		action.Metadata["source_transaction"] = value
	}
}

func convertWaitStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, bool, error) {
	timeoutMs := parseTimeoutMs(step.Arguments)
	if selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Wait step selector arguments: %w", err)
	} else if selector = resolveSelector(step, descriptors, trimQuotedValue(selector)); selector != "" {
		return dsl.Action{
			Name:     stepDisplayName(step, "Wait"),
			Type:     "browser_wait",
			Target:   selector,
			Selector: selector,
			WaitFor: &dsl.WaitCondition{
				Type:      "visible",
				Value:     selector,
				TimeoutMs: timeoutMs,
			},
		}, true, nil
	}
	if text, err := parseFirstArgumentValue(step.Arguments, "Text", "text", "Value", "value"); err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Wait step text arguments: %w", err)
	} else if text = trimQuotedValue(text); text != "" {
		return dsl.Action{
			Name: stepDisplayName(step, "Wait"),
			Type: "browser_wait",
			WaitFor: &dsl.WaitCondition{
				Type:      "text",
				Value:     text,
				TimeoutMs: timeoutMs,
			},
		}, true, nil
	}
	if delay, err := parseFirstArgumentValue(step.Arguments, "Duration", "duration", "Time", "time", "Timeout", "timeout"); err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Wait step duration arguments: %w", err)
	} else if delay = trimQuotedValue(delay); delay != "" {
		return dsl.Action{
			Name: stepDisplayName(step, "Wait"),
			Type: "browser_wait",
			WaitFor: &dsl.WaitCondition{
				Type:      "duration",
				Value:     delay,
				TimeoutMs: timeoutMs,
			},
		}, true, nil
	}
	return dsl.Action{}, false, nil
}

func convertVerifyStep(step truXMLStep) (dsl.Assertion, bool, error) {
	if text, err := parseFirstArgumentValue(step.Arguments, "Text", "text", "Value", "value"); err != nil {
		return dsl.Assertion{}, false, fmt.Errorf("parse TruClient Verify step text arguments: %w", err)
	} else if text = trimQuotedValue(text); text != "" {
		if selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err == nil && trimQuotedValue(selector) != "" {
			return dsl.Assertion{Type: "element_text_equals", Value: trimQuotedValue(selector), Expected: text}, true, nil
		}
		return dsl.Assertion{Type: "body_contains", Value: text}, true, nil
	}
	if title, err := parseFirstArgumentValue(step.Arguments, "Title", "title", "Page Title", "page_title"); err != nil {
		return dsl.Assertion{}, false, fmt.Errorf("parse TruClient Verify step title arguments: %w", err)
	} else if title = trimQuotedValue(title); title != "" {
		return dsl.Assertion{Type: "page_title", Value: title}, true, nil
	}
	if target, err := parseFirstArgumentValue(step.Arguments, "URL", "Url", "url", "Location", "location"); err != nil {
		return dsl.Assertion{}, false, fmt.Errorf("parse TruClient Verify step URL arguments: %w", err)
	} else if target = trimQuotedValue(target); target != "" {
		return dsl.Assertion{Type: "url_contains", Value: target}, true, nil
	}
	if selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err == nil && trimQuotedValue(selector) != "" {
		return dsl.Assertion{Type: "element_visible", Value: trimQuotedValue(selector)}, true, nil
	}
	return dsl.Assertion{}, false, nil
}

func parseTransactionBoundary(step truXMLStep) (bool, string, error) {
	switch strings.TrimSpace(step.Action) {
	case "Start Transaction":
		name, err := parseFirstArgumentValue(step.Arguments, "Transaction Name", "Name", "name", "Value", "value")
		if err != nil {
			return false, "", fmt.Errorf("parse TruClient Start Transaction step arguments: %w", err)
		}
		name = trimQuotedValue(name)
		if name == "" {
			name = stepDisplayName(step, "Start Transaction")
		}
		return true, name, nil
	case "End Transaction":
		name, err := parseFirstArgumentValue(step.Arguments, "Transaction Name", "Name", "name", "Value", "value")
		if err != nil {
			return false, "", fmt.Errorf("parse TruClient End Transaction step arguments: %w", err)
		}
		name = trimQuotedValue(name)
		if name == "" {
			name = stepDisplayName(step, "End Transaction")
		}
		return false, name, nil
	default:
		return false, "", nil
	}
}

func sanitizeTransactionActionName(transaction, name string) string {
	transaction = strings.TrimSpace(transaction)
	name = strings.TrimSpace(name)
	if transaction == "" {
		return name
	}
	sanitizedTransaction := strings.NewReplacer(" ", "_", "/", "_", "-", "_").Replace(transaction)
	if name == "" {
		return sanitizedTransaction
	}
	return sanitizedTransaction + "_" + name
}

func stepDisplayName(step truXMLStep, fallback string) string {
	if name := strings.TrimSpace(step.ActionName); name != "" {
		return name
	}
	if action := strings.TrimSpace(step.Action); action != "" {
		return action
	}
	return fallback
}
