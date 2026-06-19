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
	actionPattern = regexp.MustCompile(`page\d*\.(.+?)\.(click|dblclick|hover|fill|selectOption|press|check|uncheck)\(\s*(.*?)\)\s*;?\s*$`)

	locatorPattern        = regexp.MustCompile(`^locator\(\s*['"](.+?)['"]\s*\)$`)
	getByTestIDPattern    = regexp.MustCompile(`^getByTestId\(\s*['"](.+?)['"]\s*\)$`)
	getByTextPattern      = regexp.MustCompile(`^getByText\(\s*['"](.+?)['"]`)
	getByLabelPattern     = regexp.MustCompile(`^getByLabel\(\s*['"](.+?)['"]`)
	getByPlaceholderPat   = regexp.MustCompile(`^getByPlaceholder\(\s*['"](.+?)['"]`)
	getByRolePattern      = regexp.MustCompile(`^getByRole\(\s*['"](\w+)['"]\s*(?:,\s*\{\s*name:\s*['"](.+?)['"].*\})?\s*\)$`)
	firstStringArgPattern = regexp.MustCompile(`^['"](.*?)['"]`)
)

// convertPlaywright parses a Playwright codegen script (JavaScript/TypeScript
// target) line by line into browser actions, deriving a ranked candidate from
// each locator form. Lines that look like page interactions but do not parse
// are surfaced as unsupported diagnostics rather than silently dropped.
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

		if match := gotoPattern.FindStringSubmatch(line); match != nil {
			result.Stats.TotalSteps++
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = originOf(match[1])
			}
			script.Actions = append(script.Actions, dsl.Action{
				Name:   "Navigate to " + match[1],
				Type:   "browser_navigate",
				Target: relativeTo(script.Config.BaseURL, match[1]),
			})
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
			}
			continue
		}
		result.Stats.TotalSteps++

		candidate, ok := candidateFromLocatorExpression(strings.TrimSpace(match[1]))
		if !ok {
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "playwright_locator", location,
				"Unsupported locator expression: "+truncate(match[1], 120))
			continue
		}
		best, usable := selector.Usable(candidate)
		if !usable {
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "playwright_locator", location,
				"Locator has no replayable selector form: "+truncate(match[1], 120))
			continue
		}

		action := dsl.Action{
			Selector: best,
			Target:   best,
			Metadata: map[string]any{
				"source_selector_candidates": []selector.Candidate{candidate},
				"source_selector_confidence": candidate.Confidence,
			},
		}
		argValue := ""
		if argMatch := firstStringArgPattern.FindStringSubmatch(strings.TrimSpace(match[3])); argMatch != nil {
			argValue = argMatch[1]
		}
		switch match[2] {
		case "click", "check", "uncheck":
			action.Type = "browser_click"
			action.Name = "Click " + candidate.Value
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
		script.Actions = append(script.Actions, action)
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
func candidateFromLocatorExpression(expr string) (selector.Candidate, bool) {
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
			Reason: "Playwright codegen locator."}, true
	}
	if match := getByTestIDPattern.FindStringSubmatch(expr); match != nil {
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("data-testid", match[1]),
			Confidence: selector.ConfidenceDataTestID, Reason: "Playwright getByTestId locator."}, true
	}
	if match := getByRolePattern.FindStringSubmatch(expr); match != nil {
		if match[2] == "" {
			return selector.Candidate{}, false // role without name is too ambiguous
		}
		return selector.Candidate{Kind: "role", Value: selector.RoleSelector(match[1], match[2]),
			Confidence: selector.ConfidenceRoleName, Reason: "Playwright getByRole locator."}, true
	}
	if match := getByTextPattern.FindStringSubmatch(expr); match != nil {
		return selector.Candidate{Kind: "text", Value: selector.TextSelector(match[1]),
			Confidence: selector.ConfidenceText, Reason: "Playwright getByText locator; verify uniqueness on replay."}, true
	}
	if match := getByLabelPattern.FindStringSubmatch(expr); match != nil {
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("aria-label", match[1]),
			Confidence: selector.ConfidenceAriaLabel,
			Reason:     "Playwright getByLabel locator approximated as aria-label; review controls labelled by <label> elements."}, true
	}
	if match := getByPlaceholderPat.FindStringSubmatch(expr); match != nil {
		return selector.Candidate{Kind: "css", Value: selector.CSSAttributeSelector("placeholder", match[1]),
			Confidence: selector.ConfidencePlaceholder, Reason: "Playwright getByPlaceholder locator."}, true
	}
	return selector.Candidate{}, false
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
