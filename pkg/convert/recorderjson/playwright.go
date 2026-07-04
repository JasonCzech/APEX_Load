package recorderjson

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

// looksLikePlaywrightScript recognizes codegen output across the JS/TS targets.
func looksLikePlaywrightScript(content string) bool {
	return strings.Contains(content, "page.") &&
		(strings.Contains(content, "goto(") || strings.Contains(content, "locator(") ||
			strings.Contains(content, "getBy"))
}

var (
	gotoPattern = regexp.MustCompile(`page\d*\.goto\(\s*['"]([^'"]+)['"]`)
	// actionPattern captures: locator-expression, method, raw argument list.
	actionPattern        = regexp.MustCompile(`page\d*\.(.+?)\.(click|dblclick|hover|fill|selectOption|press|check|uncheck)\(\s*(.*?)\)\s*;?\s*$`)
	expectPattern        = regexp.MustCompile(`expect\(\s*page\d*\.(.+?)\s*\)\.(toBeVisible|toHaveText|toHaveValue)\(\s*(.*?)\)\s*;?\s*$`)
	pageExpectPattern    = regexp.MustCompile(`expect\(\s*page\d*\s*\)\.(toHaveURL|toHaveTitle)\(\s*(.*?)\)\s*;?\s*$`)
	waitURLPattern       = regexp.MustCompile(`page\d*\.waitForURL\(\s*['"]([^'"]+)['"]`)
	waitLoadStatePattern = regexp.MustCompile(`page\d*\.waitForLoadState\(\s*['"]?(\w+)['"]?`)
	waitTimeoutPattern   = regexp.MustCompile(`page\d*\.waitForTimeout\(\s*(\d+)`)
	keyboardPressPattern = regexp.MustCompile(`page\d*\.keyboard\.press\(\s*['"]([^'"]+)['"]`)
	mouseClickPattern    = regexp.MustCompile(`page\d*\.mouse\.click\(\s*(\d+)\s*,\s*(\d+)`)
	directSelectPattern  = regexp.MustCompile(`page\d*\.selectOption\(\s*['"]([^'"]+)['"]\s*,\s*['"]([^'"]+)['"]`)
	directActionPattern  = regexp.MustCompile(`page\d*\.(click|dblclick|hover|fill|selectOption|press|check|uncheck)\(\s*(.*?)\s*\)\s*;?\s*$`)
	pageVariablePattern  = regexp.MustCompile(`\b(page\d*)\s*(?:\.|\))`)

	buttonArgPattern    = regexp.MustCompile(`\bbutton\s*:\s*['"](\w+)['"]`)
	modifiersArgPattern = regexp.MustCompile(`\bmodifiers\s*:\s*\[([^\]]*)\]`)
)

const extraSelectorCandidatesKey = "__extra_selector_candidates"

// convertPlaywright parses a Playwright codegen script (JavaScript/TypeScript
// target) line by line into browser actions, deriving a ranked candidate from
// each locator form. Lines that look like page interactions but do not parse
// are surfaced as unsupported diagnostics rather than silently dropped.
//
// This intentionally remains a conservative line-oriented parser for common
// codegen output. Multi-line statements and complex locator composition
// (.and/.or, arbitrary fixture helpers, custom test setup) are preserved as
// unsupported diagnostics instead of being guessed. A full JS/TS AST import is
// a larger follow-up because it needs project-aware module resolution and
// fixture semantics, not just syntax parsing.
func convertPlaywright(content string, opts ConvertOptions) (*ConvertResult, error) {
	result := &ConvertResult{Stats: ConvertStats{Format: "playwright_codegen"}}
	unsupported := &diag.Collector{}

	headless := true
	script := &dsl.Script{
		Name:        convertutil.FirstNonEmpty(opts.ScriptName, "Imported Playwright Recording"),
		Version:     "1.0",
		Protocol:    "browser",
		GeneratedBy: "apexload-recorder-importer",
		Config: dsl.ScriptConfig{
			BaseURL: opts.BaseURL,
			Browser: dsl.BrowserConfig{Engine: "chromium", Headless: &headless},
		},
		CreatedAt: time.Now(),
	}

	for lineNo, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		location := fmt.Sprintf("line %d", lineNo+1)
		sourceIndex := lineNo + 1
		pageVariable := playwrightPageVariable(line)

		if match := gotoPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = originOf(match[1])
			}
			action := dsl.Action{
				Name:   "Navigate to " + match[1],
				Type:   "browser_navigate",
				Target: relativeTo(script.Config.BaseURL, match[1]),
			}
			annotatePlaywrightPageContext(&action, pageVariable)
			disposition, reason := actionDisposition(action.Metadata, "mapped", "")
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "goto",
				RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
			}, action)
			continue
		}

		if match := waitURLPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action := dsl.Action{
				Name:    "Wait for URL",
				Type:    "browser_wait",
				WaitFor: &dsl.WaitCondition{Type: "url_contains", Value: portableURLFragment(match[1])},
			}
			annotatePlaywrightPageContext(&action, pageVariable)
			disposition, reason := actionDisposition(action.Metadata, "mapped", "")
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "waitForURL",
				RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
			}, action)
			continue
		}

		if match := waitLoadStatePattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action := dsl.Action{
				Name:    "Wait for load state",
				Type:    "browser_wait",
				WaitFor: &dsl.WaitCondition{Type: playwrightLoadState(match[1])},
			}
			annotatePlaywrightPageContext(&action, pageVariable)
			disposition, reason := actionDisposition(action.Metadata, "mapped", "")
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "waitForLoadState",
				RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
			}, action)
			continue
		}

		if match := waitTimeoutPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action := dsl.Action{
				Name:    "Wait " + match[1] + "ms",
				Type:    "browser_wait",
				WaitFor: &dsl.WaitCondition{Type: "duration", Value: match[1] + "ms"},
			}
			annotatePlaywrightPageContext(&action, pageVariable)
			disposition, reason := actionDisposition(action.Metadata, "mapped", "")
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "waitForTimeout",
				RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
			}, action)
			continue
		}

		if match := keyboardPressPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action := dsl.Action{Name: "Press " + match[1], Type: "browser_press_key", Value: match[1]}
			annotatePlaywrightPageContext(&action, pageVariable)
			disposition, reason := actionDisposition(action.Metadata, "mapped", "")
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "keyboard.press",
				RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
			}, action)
			continue
		}

		if match := mouseClickPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "mouse.click",
				RecorderStepSeq: sourceIndex, Disposition: "degraded",
				Reason: "Converted coordinate mouse click to review-gated JavaScript using document.elementFromPoint.",
			}, dsl.Action{
				Name:   "Click coordinates",
				Type:   "browser_evaluate_js",
				Target: fmt.Sprintf("document.elementFromPoint(%s, %s)?.click()", match[1], match[2]),
				Metadata: map[string]any{
					"review_required": true,
					"review_reason":   "playwright_mouse_coordinate_click",
				},
			})
			continue
		}

		if match := directActionPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action, ok := playwrightDirectPageAction(match[1], match[2], location, result, unsupported)
			if ok {
				annotatePlaywrightPageContext(&action, pageVariable)
				disposition, reason := actionDisposition(action.Metadata, "mapped", "")
				appendImportedAction(script, result, StepMapping{
					SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "page." + match[1],
					RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
				}, action)
			} else {
				appendUnsupportedStepMapping(result, sourceIndex, location, "page."+match[1], "Playwright direct page action could not be converted.")
			}
			continue
		}

		if match := directSelectPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			candidate := selector.Candidate{Kind: "css", Value: match[1], Confidence: cssConfidence(match[1]), Reason: "Playwright page.selectOption selector."}
			action := dsl.Action{
				Name:     "Select " + match[1],
				Type:     "browser_select",
				Selector: match[1],
				Target:   match[1],
				Value:    match[2],
				Metadata: map[string]any{
					"source_selector_candidates": []selector.Candidate{candidate},
					"source_selector_confidence": candidate.Confidence,
				},
			}
			annotatePlaywrightPageContext(&action, pageVariable)
			disposition, reason := actionDisposition(action.Metadata, "mapped", "")
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "selectOption",
				RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
			}, action)
			continue
		}

		if match := pageExpectPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action := playwrightPageAssertionAction(match[1], match[2])
			annotatePlaywrightPageContext(&action, pageVariable)
			disposition, reason := actionDisposition(action.Metadata, "mapped", "")
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: match[1],
				RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
			}, action)
			continue
		}

		if match := expectPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action, ok := playwrightAssertionAction(match[1], match[2], match[3], location, result, unsupported)
			if ok {
				annotatePlaywrightPageContext(&action, pageVariable)
				disposition, reason := actionDisposition(action.Metadata, "mapped", "")
				appendImportedAction(script, result, StepMapping{
					SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: match[2],
					RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
				}, action)
			} else {
				appendUnsupportedStepMapping(result, sourceIndex, location, match[2], "Playwright assertion locator could not be converted.")
			}
			continue
		}

		match := actionPattern.FindStringSubmatch(line)
		if match == nil {
			if strings.Contains(line, "page.") && strings.Contains(line, "(") {
				result.Stats.TotalSteps++
				result.Stats.SkippedSteps++
				unsupported.Add(diag.Warning, "playwright_line", location,
					"Playwright statement was not convertible to a browser action: "+truncate(line, 160))
				appendUnsupportedStepMapping(result, sourceIndex, location, "playwright_line", "Playwright statement was not convertible to a browser action.")
			}
			continue
		}
		result.Stats.TotalSteps++

		candidate, metadata, ok := candidateFromLocatorExpression(strings.TrimSpace(match[1]))
		if !ok {
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "playwright_locator", location,
				"Unsupported locator expression: "+truncate(match[1], 120))
			appendUnsupportedStepMapping(result, sourceIndex, location, match[2], "Unsupported locator expression.")
			continue
		}
		best, usable := selector.Usable(candidate)
		if !usable {
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "playwright_locator", location,
				"Locator has no replayable selector form: "+truncate(match[1], 120))
			appendUnsupportedStepMapping(result, sourceIndex, location, match[2], "Locator has no replayable selector form.")
			continue
		}

		candidates := selectorCandidatesForAction(candidate, metadata)
		action := dsl.Action{
			Selector: best,
			Target:   best,
			Metadata: map[string]any{
				"source_selector_candidates": candidates,
				"source_selector_confidence": candidate.Confidence,
			},
		}
		for key, value := range metadata {
			action.Metadata[key] = value
		}
		argValue := firstStringArg(match[3])
		switch match[2] {
		case "click":
			action.Type = "browser_click"
			action.Name = "Click " + candidate.Value
			applyPlaywrightClickOptions(&action, match[3])
		case "check", "uncheck":
			action.Type = "browser_check"
			action.Name = strings.Title(match[2]) + " " + candidate.Value
			if match[2] == "uncheck" {
				action.Value = "false"
			} else {
				action.Value = "true"
			}
			action.Metadata["source_checkbox_state"] = match[2]
		case "dblclick":
			action.Type = "browser_dblclick"
			action.Name = "Double-click " + candidate.Value
			applyPlaywrightClickOptions(&action, match[3])
		case "hover":
			action.Type = "browser_hover"
			action.Name = "Hover " + candidate.Value
		case "fill":
			action.Type = "browser_fill"
			action.Name = "Fill " + candidate.Value
			action.Value = argValue
		case "selectOption":
			action.Type = "browser_select"
			action.Name = "Select " + candidate.Value
			action.Value = argValue
		case "press":
			action.Type = "browser_press_key"
			action.Name = "Press " + argValue
			action.Value = argValue
		}
		annotatePlaywrightPageContext(&action, pageVariable)
		disposition, reason := actionDisposition(action.Metadata, "mapped", "")
		appendImportedAction(script, result, StepMapping{
			SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: match[2],
			RecorderStepSeq: sourceIndex, Disposition: disposition, Reason: reason,
		}, action)
	}

	if len(script.Actions) == 0 {
		return nil, fmt.Errorf("no convertible Playwright statements found")
	}
	result.Script = script
	result.Stats.ConvertedActions = len(script.Actions)
	result.Unsupported = unsupported.Items()
	for _, item := range result.Unsupported {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %s", item.Location, item.Detail))
	}
	return result, nil
}

// candidateFromLocatorExpression maps one Playwright locator expression onto
// the shared candidate model.
func candidateFromLocatorExpression(expr string) (selector.Candidate, map[string]any, bool) {
	metadata := map[string]any{}
	expr = normalizeLocatorExpression(expr, metadata)
	if frameSelector, childExpr, ok := splitFrameLocatorExpression(expr); ok {
		candidate, childMetadata, ok := candidateFromLocatorExpression(strings.TrimSpace(childExpr))
		if !ok {
			return selector.Candidate{}, nil, false
		}
		for key, value := range childMetadata {
			metadata[key] = value
		}
		metadata["frame_selector"] = frameSelector
		return candidate, metadata, true
	}
	if value, rest, ok := parseCallFirstStringArg(expr, "locator"); ok && strings.TrimSpace(rest) == ")" {
		kind := "css"
		confidence := cssConfidence(value)
		if strings.HasPrefix(strings.ToLower(value), "xpath=") || strings.HasPrefix(value, "//") {
			kind = "xpath"
			confidence = selector.ConfidenceXPath
			value = strings.TrimPrefix(strings.TrimPrefix(value, "xpath="), "XPATH=")
		}
		return selector.Candidate{Kind: kind, Value: value, Confidence: confidence,
			Reason: "Playwright codegen locator."}, metadata, true
	}
	if value, _, ok := parseCallFirstStringArg(expr, "getByTestId"); ok {
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("data-testid", value),
			Confidence: selector.ConfidenceDataTestID, Reason: "Playwright getByTestId locator."}, metadata, true
	}
	if role, rest, ok := parseCallFirstStringArg(expr, "getByRole"); ok {
		name := parseNamedStringOption(rest, "name")
		if name == "" {
			return selector.Candidate{}, nil, false // role without name is too ambiguous
		}
		return selector.Candidate{Kind: "role", Value: selector.RoleSelector(role, name),
			Confidence: selector.ConfidenceRoleName, Reason: "Playwright getByRole locator."}, metadata, true
	}
	if value, _, ok := parseCallFirstStringArg(expr, "getByText"); ok {
		return selector.Candidate{Kind: "text", Value: selector.TextSelector(value),
			Confidence: selector.ConfidenceText, Reason: "Playwright getByText locator; verify uniqueness on replay."}, metadata, true
	}
	if value, _, ok := parseCallFirstStringArg(expr, "getByLabel"); ok {
		metadata[extraSelectorCandidatesKey] = []selector.Candidate{
			{
				Kind:       "text",
				Value:      selector.TextSelector(value),
				Confidence: selector.ConfidenceText,
				Reason:     "Fallback for Playwright getByLabel when the control is associated by a visible <label>.",
			},
		}
		metadata["import_disposition"] = "degraded"
		metadata["import_disposition_reason"] = "Playwright getByLabel was approximated with aria-label plus a visible-label fallback; review labelled controls."
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("aria-label", value),
			Confidence: selector.ConfidenceAriaLabel,
			Reason:     "Playwright getByLabel locator approximated as aria-label; review controls labelled by <label> elements."}, metadata, true
	}
	if value, _, ok := parseCallFirstStringArg(expr, "getByPlaceholder"); ok {
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("placeholder", value),
			Confidence: selector.ConfidencePlaceholder, Reason: "Playwright getByPlaceholder locator."}, metadata, true
	}
	return selector.Candidate{}, nil, false
}

func splitFrameLocatorExpression(expr string) (string, string, bool) {
	frameSelector, rest, ok := parseCallFirstStringArg(expr, "frameLocator")
	if !ok {
		return "", "", false
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, ")") {
		return "", "", false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ")"))
	if !strings.HasPrefix(rest, ".") {
		return "", "", false
	}
	child := strings.TrimSpace(strings.TrimPrefix(rest, "."))
	return frameSelector, child, child != ""
}

func normalizeLocatorExpression(expr string, metadata map[string]any) string {
	expr = strings.TrimSpace(expr)
	suffixes := []string{".first()", ".last()"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(expr, suffix) {
			expr = strings.TrimSuffix(expr, suffix)
			metadata["import_disposition"] = "degraded"
			metadata["import_disposition_reason"] = "Playwright locator suffix " + suffix + " was stripped; review selector uniqueness."
		}
	}
	nthPattern := regexp.MustCompile(`\.nth\(\s*\d+\s*\)$`)
	if nthPattern.MatchString(expr) {
		expr = nthPattern.ReplaceAllString(expr, "")
		metadata["import_disposition"] = "degraded"
		metadata["import_disposition_reason"] = "Playwright locator nth() suffix was stripped; review selector uniqueness."
	}
	filterPattern := regexp.MustCompile(`\.filter\(\s*\{.*\}\s*\)$`)
	if filterPattern.MatchString(expr) {
		expr = filterPattern.ReplaceAllString(expr, "")
		metadata["import_disposition"] = "degraded"
		metadata["import_disposition_reason"] = "Playwright locator filter() suffix was stripped; review selector uniqueness."
	}
	return expr
}

func selectorCandidatesForAction(primary selector.Candidate, metadata map[string]any) []selector.Candidate {
	candidates := []selector.Candidate{primary}
	if extras, ok := metadata[extraSelectorCandidatesKey].([]selector.Candidate); ok {
		candidates = append(candidates, extras...)
		delete(metadata, extraSelectorCandidatesKey)
	}
	return selector.Unique(candidates)
}

func playwrightDirectPageAction(method, rawArgs, location string, result *ConvertResult, unsupported *diag.Collector) (dsl.Action, bool) {
	selectorValue, rest, ok := parseLeadingJSStringLiteral(rawArgs)
	if !ok {
		result.Stats.SkippedSteps++
		unsupported.Add(diag.Warning, "playwright_direct_action", location,
			"Direct Playwright page."+method+" action has no string selector.")
		return dsl.Action{}, false
	}
	candidate := candidateFromDirectPageSelector(selectorValue)
	best, usable := selector.Usable(candidate)
	if !usable {
		result.Stats.SkippedSteps++
		unsupported.Add(diag.Warning, "playwright_direct_action", location,
			"Direct Playwright page."+method+" selector is not replayable: "+truncate(selectorValue, 120))
		return dsl.Action{}, false
	}
	action := dsl.Action{
		Selector: best,
		Target:   best,
		Metadata: map[string]any{
			"source_selector_candidates": []selector.Candidate{candidate},
			"source_selector_confidence": candidate.Confidence,
			"source_direct_page_action":  true,
		},
	}
	value := firstCommaStringArg(rest)
	switch method {
	case "click":
		action.Type = "browser_click"
		action.Name = "Click " + candidate.Value
		applyPlaywrightClickOptions(&action, rest)
	case "dblclick":
		action.Type = "browser_dblclick"
		action.Name = "Double-click " + candidate.Value
		applyPlaywrightClickOptions(&action, rest)
	case "hover":
		action.Type = "browser_hover"
		action.Name = "Hover " + candidate.Value
	case "fill":
		action.Type = "browser_fill"
		action.Name = "Fill " + candidate.Value
		action.Value = value
	case "selectOption":
		action.Type = "browser_select"
		action.Name = "Select " + candidate.Value
		action.Value = value
	case "press":
		action.Type = "browser_press_key"
		action.Name = "Press " + value
		action.Value = value
	case "check":
		action.Type = "browser_check"
		action.Name = "Check " + candidate.Value
		action.Value = "true"
		action.Metadata["source_checkbox_state"] = "check"
	case "uncheck":
		action.Type = "browser_check"
		action.Name = "Uncheck " + candidate.Value
		action.Value = "false"
		action.Metadata["source_checkbox_state"] = "uncheck"
	default:
		return dsl.Action{}, false
	}
	return action, true
}

func candidateFromDirectPageSelector(value string) selector.Candidate {
	kind := "css"
	confidence := cssConfidence(value)
	lower := strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.HasPrefix(lower, "xpath=") || strings.HasPrefix(value, "//"):
		kind = "xpath"
		confidence = selector.ConfidenceXPath
		value = strings.TrimPrefix(strings.TrimPrefix(value, "xpath="), "XPATH=")
	case strings.HasPrefix(lower, "role="):
		kind = "role"
		confidence = selector.ConfidenceRoleName
	case strings.HasPrefix(lower, "text="):
		kind = "text"
		confidence = selector.ConfidenceText
	}
	return selector.Candidate{Kind: kind, Value: value, Confidence: confidence, Reason: "Playwright direct page action selector."}
}

func actionDisposition(metadata map[string]any, fallbackDisposition, fallbackReason string) (string, string) {
	disposition := fallbackDisposition
	reason := fallbackReason
	if value, ok := metadata["import_disposition"].(string); ok && strings.TrimSpace(value) != "" {
		disposition = strings.TrimSpace(value)
	}
	if value, ok := metadata["import_disposition_reason"].(string); ok && strings.TrimSpace(value) != "" {
		reason = strings.TrimSpace(value)
	}
	if disposition == "" {
		disposition = "mapped"
	}
	return disposition, reason
}

func playwrightPageVariable(line string) string {
	match := pageVariablePattern.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func annotatePlaywrightPageContext(action *dsl.Action, pageVariable string) {
	pageVariable = strings.TrimSpace(pageVariable)
	if action == nil || pageVariable == "" || pageVariable == "page" {
		return
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata["source_page_variable"] = pageVariable
	markActionDisposition(action, "degraded", "Playwright page variable "+pageVariable+" may represent a popup or new tab; review imported window targeting.")
}

func playwrightPageAssertionAction(assertion, rawArgs string) dsl.Action {
	expected := firstAssertionValue(rawArgs)
	action := dsl.Action{
		Name: "Assert page",
		Type: "browser_wait",
	}
	switch assertion {
	case "toHaveTitle":
		action.Name = "Assert page title"
		action.WaitFor = &dsl.WaitCondition{Type: "load"}
		action.Assertions = []dsl.Assertion{{Type: "page_title", Value: expected}}
	default:
		action.Name = "Assert URL"
		value := portableURLFragment(expected)
		action.WaitFor = &dsl.WaitCondition{Type: "url_contains", Value: value}
		action.Assertions = []dsl.Assertion{{Type: "url_contains", Value: value}}
	}
	return action
}

func firstAssertionValue(raw string) string {
	if value := firstStringArg(raw); value != "" {
		return value
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "/") {
		if end := strings.LastIndex(trimmed[1:], "/"); end >= 0 {
			pattern := trimmed[1 : end+1]
			pattern = strings.TrimPrefix(pattern, "^")
			pattern = strings.TrimSuffix(pattern, "$")
			pattern = strings.ReplaceAll(pattern, ".*", "")
			return pattern
		}
	}
	return trimmed
}

func playwrightAssertionAction(expr, assertion, rawArgs, location string, result *ConvertResult, unsupported *diag.Collector) (dsl.Action, bool) {
	candidate, metadata, ok := candidateFromLocatorExpression(strings.TrimSpace(expr))
	if !ok {
		result.Stats.SkippedSteps++
		unsupported.Add(diag.Warning, "playwright_assertion", location,
			"Unsupported assertion locator expression: "+truncate(expr, 120))
		return dsl.Action{}, false
	}
	best, usable := selector.Usable(candidate)
	if !usable {
		result.Stats.SkippedSteps++
		unsupported.Add(diag.Warning, "playwright_assertion", location,
			"Assertion locator has no replayable selector form: "+truncate(expr, 120))
		return dsl.Action{}, false
	}
	action := dsl.Action{
		Name:     "Assert " + candidate.Value,
		Type:     "browser_wait",
		Selector: best,
		Target:   best,
		WaitFor:  &dsl.WaitCondition{Type: "visible", Value: best},
		Metadata: map[string]any{
			"source_selector_candidates": selectorCandidatesForAction(candidate, metadata),
			"source_selector_confidence": candidate.Confidence,
		},
	}
	for key, value := range metadata {
		action.Metadata[key] = value
	}
	switch assertion {
	case "toBeVisible":
		action.Assertions = []dsl.Assertion{{Type: "element_visible", Value: best}}
	case "toHaveText":
		expected := firstStringArg(rawArgs)
		action.Assertions = []dsl.Assertion{{Type: "element_text_contains", Value: best, Expected: expected}}
	case "toHaveValue":
		expected := firstStringArg(rawArgs)
		if candidate.Kind != "css" {
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "playwright_assertion", location,
				"Playwright toHaveValue was not converted because "+candidate.Kind+" locators cannot be evaluated with document.querySelector.")
			return dsl.Action{}, false
		}
		action.Type = "browser_evaluate_js"
		action.Name = "Assert value " + candidate.Value
		action.Target = fmt.Sprintf(`(() => {
  const el = document.querySelector(%q);
  if (!el) throw new Error("selector not found");
  if (String(el.value || "") !== %q) throw new Error("value assertion failed");
  return true;
})()`, best, expected)
		action.Assertions = nil
		action.Metadata["review_required"] = true
		action.Metadata["compatibility_gate"] = "playwright_to_have_value"
		action.Metadata["import_disposition"] = "degraded"
		action.Metadata["import_disposition_reason"] = "Playwright toHaveValue was converted to review-gated JavaScript because the browser assertion DSL has no element-value assertion."
	}
	return action, true
}

func firstStringArg(raw string) string {
	if value, _, ok := parseLeadingJSStringLiteral(raw); ok {
		return value
	}
	return ""
}

func firstCommaStringArg(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, ",") {
		return ""
	}
	return firstStringArg(strings.TrimSpace(strings.TrimPrefix(raw, ",")))
}

func parseCallFirstStringArg(expr, name string) (string, string, bool) {
	expr = strings.TrimSpace(expr)
	prefix := name + "("
	if !strings.HasPrefix(expr, prefix) {
		return "", "", false
	}
	return parseLeadingJSStringLiteral(strings.TrimPrefix(expr, prefix))
}

func parseNamedStringOption(raw, name string) string {
	index := strings.Index(raw, name)
	if index < 0 {
		return ""
	}
	afterName := strings.TrimSpace(raw[index+len(name):])
	if !strings.HasPrefix(afterName, ":") {
		return ""
	}
	value, _, ok := parseLeadingJSStringLiteral(strings.TrimSpace(strings.TrimPrefix(afterName, ":")))
	if !ok {
		return ""
	}
	return value
}

func parseLeadingJSStringLiteral(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	quote := raw[0]
	if quote != '\'' && quote != '"' {
		return "", "", false
	}
	var b strings.Builder
	escaped := false
	for i := 1; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			switch ch {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			default:
				b.WriteByte(ch)
			}
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == quote {
			return b.String(), raw[i+1:], true
		}
		b.WriteByte(ch)
	}
	return "", "", false
}

func applyPlaywrightClickOptions(action *dsl.Action, rawArgs string) {
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	if match := buttonArgPattern.FindStringSubmatch(rawArgs); match != nil {
		if button := normalizeMouseButton(match[1]); button != "" {
			action.Params["button"] = button
		}
	}
	if match := modifiersArgPattern.FindStringSubmatch(rawArgs); match != nil {
		var modifiers []string
		for _, part := range strings.Split(match[1], ",") {
			if value := firstStringArg(strings.TrimSpace(part)); value != "" {
				modifiers = append(modifiers, normalizeKeyboardModifier(value))
			}
		}
		if len(modifiers) > 0 {
			action.Params["modifiers"] = modifiers
		}
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
}

func normalizeMouseButton(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "right", "secondary":
		return "right"
	case "middle", "auxiliary", "aux":
		return "middle"
	case "left", "primary":
		return "left"
	default:
		return ""
	}
}

func normalizeKeyboardModifier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ctrl":
		return "Control"
	case "cmd", "command":
		return "Meta"
	case "controlormeta", "control_or_meta", "cmdorctrl", "commandorcontrol":
		return "ControlOrMeta"
	case "alt":
		return "Alt"
	case "control":
		return "Control"
	case "meta":
		return "Meta"
	case "shift":
		return "Shift"
	default:
		return strings.TrimSpace(value)
	}
}

func portableURLFragment(rawURL string) string {
	if fragment := navigationAssertionValue(rawURL); fragment != "" {
		return fragment
	}
	return rawURL
}

func playwrightLoadState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "networkidle":
		return "networkidle"
	case "domcontentloaded":
		return "domcontentloaded"
	default:
		return "load"
	}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
