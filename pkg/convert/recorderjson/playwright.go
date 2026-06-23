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

	locatorPattern        = regexp.MustCompile(`^locator\(\s*['"](.+?)['"]\s*\)$`)
	frameLocatorPattern   = regexp.MustCompile(`^frameLocator\(\s*['"](.+?)['"]\s*\)\.(.+)$`)
	getByTestIDPattern    = regexp.MustCompile(`^getByTestId\(\s*['"](.+?)['"]\s*\)$`)
	getByTextPattern      = regexp.MustCompile(`^getByText\(\s*['"](.+?)['"]`)
	getByLabelPattern     = regexp.MustCompile(`^getByLabel\(\s*['"](.+?)['"]`)
	getByPlaceholderPat   = regexp.MustCompile(`^getByPlaceholder\(\s*['"](.+?)['"]`)
	getByRolePattern      = regexp.MustCompile(`^getByRole\(\s*['"](\w+)['"]\s*(?:,\s*\{\s*name:\s*['"](.+?)['"].*\})?\s*\)$`)
	firstStringArgPattern = regexp.MustCompile(`^['"](.*?)['"]`)
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

		if match := gotoPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = originOf(match[1])
			}
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "goto",
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, dsl.Action{
				Name:   "Navigate to " + match[1],
				Type:   "browser_navigate",
				Target: relativeTo(script.Config.BaseURL, match[1]),
			})
			continue
		}

		if match := waitURLPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "waitForURL",
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, dsl.Action{
				Name:    "Wait for URL",
				Type:    "browser_wait",
				WaitFor: &dsl.WaitCondition{Type: "url_contains", Value: portableURLFragment(match[1])},
			})
			continue
		}

		if match := waitLoadStatePattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "waitForLoadState",
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, dsl.Action{
				Name:    "Wait for load state",
				Type:    "browser_wait",
				WaitFor: &dsl.WaitCondition{Type: playwrightLoadState(match[1])},
			})
			continue
		}

		if match := waitTimeoutPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "waitForTimeout",
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, dsl.Action{
				Name:    "Wait " + match[1] + "ms",
				Type:    "browser_wait",
				WaitFor: &dsl.WaitCondition{Type: "duration", Value: match[1] + "ms"},
			})
			continue
		}

		if match := keyboardPressPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "keyboard.press",
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, dsl.Action{Name: "Press " + match[1], Type: "browser_press_key", Value: match[1]})
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

		if match := directSelectPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			candidate := selector.Candidate{Kind: "css", Value: match[1], Confidence: cssConfidence(match[1]), Reason: "Playwright page.selectOption selector."}
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: "selectOption",
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, dsl.Action{
				Name:     "Select " + match[1],
				Type:     "browser_select",
				Selector: match[1],
				Target:   match[1],
				Value:    match[2],
				Metadata: map[string]any{
					"source_selector_candidates": []selector.Candidate{candidate},
					"source_selector_confidence": candidate.Confidence,
				},
			})
			continue
		}

		if match := pageExpectPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action := playwrightPageAssertionAction(match[1], match[2])
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: match[1],
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, action)
			continue
		}

		if match := expectPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			action, ok := playwrightAssertionAction(match[1], match[2], match[3], location, result, unsupported)
			if ok {
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
			if strings.Contains(line, "page.") && strings.Contains(line, "(") &&
				(strings.Contains(line, "locator") || strings.Contains(line, "getBy")) {
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
		argValue := ""
		if argMatch := firstStringArgPattern.FindStringSubmatch(strings.TrimSpace(match[3])); argMatch != nil {
			argValue = argMatch[1]
		}
		switch match[2] {
		case "click":
			action.Type = "browser_click"
			action.Name = "Click " + candidate.Value
		case "check", "uncheck":
			action.Type = "browser_click"
			action.Name = strings.Title(match[2]) + " " + candidate.Value
			action.Metadata["source_checkbox_state"] = match[2]
			action.Metadata["import_disposition"] = "degraded"
			action.Metadata["import_disposition_reason"] = "Playwright check/uncheck intent is preserved as metadata on a browser_click action."
		case "dblclick":
			action.Type = "browser_dblclick"
			action.Name = "Double-click " + candidate.Value
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
	if match := frameLocatorPattern.FindStringSubmatch(expr); match != nil {
		candidate, childMetadata, ok := candidateFromLocatorExpression(strings.TrimSpace(match[2]))
		if !ok {
			return selector.Candidate{}, nil, false
		}
		for key, value := range childMetadata {
			metadata[key] = value
		}
		metadata["frame_selector"] = match[1]
		return candidate, metadata, true
	}
	if match := locatorPattern.FindStringSubmatch(expr); match != nil {
		value := match[1]
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
	if match := getByTestIDPattern.FindStringSubmatch(expr); match != nil {
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("data-testid", match[1]),
			Confidence: selector.ConfidenceDataTestID, Reason: "Playwright getByTestId locator."}, metadata, true
	}
	if match := getByRolePattern.FindStringSubmatch(expr); match != nil {
		if match[2] == "" {
			return selector.Candidate{}, nil, false // role without name is too ambiguous
		}
		return selector.Candidate{Kind: "role", Value: selector.RoleSelector(match[1], match[2]),
			Confidence: selector.ConfidenceRoleName, Reason: "Playwright getByRole locator."}, metadata, true
	}
	if match := getByTextPattern.FindStringSubmatch(expr); match != nil {
		return selector.Candidate{Kind: "text", Value: selector.TextSelector(match[1]),
			Confidence: selector.ConfidenceText, Reason: "Playwright getByText locator; verify uniqueness on replay."}, metadata, true
	}
	if match := getByLabelPattern.FindStringSubmatch(expr); match != nil {
		metadata[extraSelectorCandidatesKey] = []selector.Candidate{
			{
				Kind:       "text",
				Value:      selector.TextSelector(match[1]),
				Confidence: selector.ConfidenceText,
				Reason:     "Fallback for Playwright getByLabel when the control is associated by a visible <label>.",
			},
		}
		metadata["import_disposition"] = "degraded"
		metadata["import_disposition_reason"] = "Playwright getByLabel was approximated with aria-label plus a visible-label fallback; review labelled controls."
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("aria-label", match[1]),
			Confidence: selector.ConfidenceAriaLabel,
			Reason:     "Playwright getByLabel locator approximated as aria-label; review controls labelled by <label> elements."}, metadata, true
	}
	if match := getByPlaceholderPat.FindStringSubmatch(expr); match != nil {
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("placeholder", match[1]),
			Confidence: selector.ConfidencePlaceholder, Reason: "Playwright getByPlaceholder locator."}, metadata, true
	}
	return selector.Candidate{}, nil, false
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
	if match := firstStringArgPattern.FindStringSubmatch(strings.TrimSpace(raw)); match != nil {
		return match[1]
	}
	return ""
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
