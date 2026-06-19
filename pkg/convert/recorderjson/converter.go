// Package recorderjson imports browser recordings produced outside APEX Load —
// Chrome DevTools Recorder JSON exports and Playwright codegen scripts — into
// browser-protocol DSL scripts with ranked selector candidates, so users who
// record in their own browser still get the embedded recorder's review flow.
package recorderjson

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

type ConvertOptions struct {
	ScriptName string `json:"script_name,omitempty"`
	BaseURL    string `json:"base_url,omitempty"`
}

type ConvertStats struct {
	Format           string `json:"format"`
	TotalSteps       int    `json:"total_steps"`
	ConvertedActions int    `json:"converted_actions"`
	SkippedSteps     int    `json:"skipped_steps"`
}

type ConvertResult struct {
	Script      *dsl.Script      `json:"script"`
	Warnings    []string         `json:"warnings,omitempty"`
	Unsupported diag.Diagnostics `json:"unsupported,omitempty"`
	Stats       ConvertStats     `json:"stats"`
}

// chromeRecording is the Chrome DevTools Recorder export format
// (https://developer.chrome.com/docs/devtools/recorder): a title plus a flat
// list of typed steps whose selectors arrays carry ranked locator candidates.
type chromeRecording struct {
	Title string       `json:"title"`
	Steps []chromeStep `json:"steps"`
}

type chromeStep struct {
	Type           string               `json:"type"`
	URL            string               `json:"url,omitempty"`
	Key            string               `json:"key,omitempty"`
	Value          string               `json:"value,omitempty"`
	Expression     string               `json:"expression,omitempty"`
	Width          int                  `json:"width,omitempty"`
	Height         int                  `json:"height,omitempty"`
	Selectors      [][]string           `json:"selectors,omitempty"`
	AssertedEvents []chromeAssertedItem `json:"assertedEvents,omitempty"`
}

type chromeAssertedItem struct {
	Type  string `json:"type"`
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
}

// Convert sniffs the payload format and dispatches: a JSON object with a
// "steps" array is a Chrome DevTools Recorder export; anything else is treated
// as a Playwright codegen script.
func Convert(data []byte, opts ConvertOptions) (*ConvertResult, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("recording content is required")
	}
	if strings.HasPrefix(trimmed, "{") {
		var recording chromeRecording
		if err := json.Unmarshal([]byte(trimmed), &recording); err != nil {
			return nil, fmt.Errorf("parse Chrome DevTools Recorder JSON: %w", err)
		}
		if len(recording.Steps) == 0 {
			return nil, fmt.Errorf("recording contains no steps")
		}
		return convertChrome(recording, opts)
	}
	if looksLikePlaywrightScript(trimmed) {
		return convertPlaywright(trimmed, opts)
	}
	return nil, fmt.Errorf("unrecognized recording format: expected Chrome DevTools Recorder JSON or a Playwright codegen script")
}

func convertChrome(recording chromeRecording, opts ConvertOptions) (*ConvertResult, error) {
	result := &ConvertResult{Stats: ConvertStats{Format: "chrome_devtools_recorder", TotalSteps: len(recording.Steps)}}
	unsupported := &diag.Collector{}

	name := convertutil.FirstNonEmpty(opts.ScriptName, recording.Title, "Imported Chrome Recording")
	headless := true
	script := &dsl.Script{
		Name:        name,
		Version:     "1.0",
		Protocol:    "browser",
		GeneratedBy: "apexload-recorder-importer",
		Config: dsl.ScriptConfig{
			BaseURL: opts.BaseURL,
			Browser: dsl.BrowserConfig{Engine: "chromium", Headless: &headless},
		},
		CreatedAt: time.Now(),
	}

	for i, step := range recording.Steps {
		location := fmt.Sprintf("step %d", i+1)
		switch step.Type {
		case "setViewport":
			if step.Width > 0 && step.Height > 0 {
				script.Config.Browser.Viewport = dsl.BrowserViewport{Width: step.Width, Height: step.Height}
			}
		case "navigate":
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = originOf(step.URL)
			}
			script.Actions = append(script.Actions, dsl.Action{
				Name:   "Navigate to " + step.URL,
				Type:   "browser_navigate",
				Target: relativeTo(script.Config.BaseURL, step.URL),
			})
		case "click", "doubleClick", "hover":
			action, ok := chromeElementAction(step, location, result)
			if !ok {
				continue
			}
			switch step.Type {
			case "doubleClick":
				action.Type = "browser_dblclick"
				action.Name = "Double-click " + chromeStepLabel(step)
			case "hover":
				action.Type = "browser_hover"
				action.Name = "Hover " + chromeStepLabel(step)
			default:
				action.Type = "browser_click"
				action.Name = "Click " + chromeStepLabel(step)
			}
			applyAssertedNavigation(&action, step)
			script.Actions = append(script.Actions, action)
		case "change":
			action, ok := chromeElementAction(step, location, result)
			if !ok {
				continue
			}
			action.Type = "browser_fill"
			action.Name = "Fill " + chromeStepLabel(step)
			action.Value = step.Value
			applyAssertedNavigation(&action, step)
			script.Actions = append(script.Actions, action)
		case "keyDown":
			if key := strings.TrimSpace(step.Key); key != "" {
				script.Actions = append(script.Actions, dsl.Action{
					Name:  "Press " + key,
					Type:  "browser_press_key",
					Value: key,
				})
			}
		case "keyUp":
			// The paired keyDown already produced the press action.
		case "waitForElement":
			action, ok := chromeElementAction(step, location, result)
			if !ok {
				continue
			}
			action.Type = "browser_wait"
			action.Name = "Wait for " + chromeStepLabel(step)
			action.WaitFor = &dsl.WaitCondition{Type: "visible", Value: action.Selector}
			script.Actions = append(script.Actions, action)
		default:
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "chrome_recorder_step:"+step.Type, location,
				"Chrome DevTools Recorder step type is not convertible to a browser action; review the recording for coverage gaps.")
		}
	}

	result.Script = script
	result.Stats.ConvertedActions = len(script.Actions)
	result.Unsupported = unsupported.Items()
	for _, item := range result.Unsupported {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %s", item.Location, item.Element))
	}
	return result, nil
}

// chromeElementAction builds the shared element-action skeleton: ranked
// candidates from the step's selectors arrays, best-selector promotion, and
// the same source_selector_* metadata the embedded recorder emits.
func chromeElementAction(step chromeStep, location string, result *ConvertResult) (dsl.Action, bool) {
	candidates := candidatesFromChromeSelectors(step.Selectors)
	best := selector.PlaywrightSelector(candidates)
	if best == "" {
		for _, candidate := range candidates {
			if value, ok := selector.Usable(candidate); ok {
				best = value
				break
			}
		}
	}
	if best == "" {
		result.Stats.SkippedSteps++
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s (%s) has no replayable selector and was skipped", location, step.Type))
		return dsl.Action{}, false
	}
	action := dsl.Action{Selector: best, Target: best}
	action.Metadata = map[string]any{}
	if len(candidates) > 0 {
		action.Metadata["source_selector_candidates"] = candidates
		action.Metadata["source_selector_confidence"] = candidates[0].Confidence
		if len(candidates) > 1 {
			action.Metadata["source_selector_fallbacks"] = candidates[1:]
		}
	}
	return action, true
}

// applyAssertedNavigation maps the recorder's asserted navigation events onto
// the replay vocabulary: the step waits for the page load, and the recorded
// destination URL/title become verifiable assertions so a wrong landing page
// fails the replay instead of passing silently.
func applyAssertedNavigation(action *dsl.Action, step chromeStep) {
	for _, asserted := range step.AssertedEvents {
		if asserted.Type != "navigation" {
			continue
		}
		action.Sync = &dsl.BrowserSyncPolicy{Type: "load"}
		if value := navigationAssertionValue(asserted.URL); value != "" {
			action.Assertions = appendUniqueAssertion(action.Assertions, dsl.Assertion{Type: "url_contains", Value: value})
		}
		if title := strings.TrimSpace(asserted.Title); title != "" {
			action.Assertions = appendUniqueAssertion(action.Assertions, dsl.Assertion{Type: "page_title", Value: title})
		}
	}
}

// navigationAssertionValue picks the most environment-portable fragment of the
// asserted destination URL: the path survives host changes between recording
// and replay targets; host-only destinations fall back to the full URL.
func navigationAssertionValue(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Path == "" || parsed.Path == "/" {
		return trimmed
	}
	return parsed.Path
}

func appendUniqueAssertion(assertions []dsl.Assertion, candidate dsl.Assertion) []dsl.Assertion {
	for _, existing := range assertions {
		if existing.Type == candidate.Type && existing.Value == candidate.Value {
			return assertions
		}
	}
	return append(assertions, candidate)
}

var ariaWithRolePattern = regexp.MustCompile(`^(.*)\[role="([^"]+)"\]$`)

// candidatesFromChromeSelectors converts the recorder's ranked selectors
// arrays. Each entry is a chain of selector parts; multi-part chains pierce
// shadow roots and are joined with a descendant combinator (Playwright's CSS
// engine pierces open shadow DOM). Prefixes: aria/, xpath/, pierce/, text/.
func candidatesFromChromeSelectors(selectorChains [][]string) []selector.Candidate {
	candidates := make([]selector.Candidate, 0, len(selectorChains))
	for _, chain := range selectorChains {
		if len(chain) == 0 {
			continue
		}
		pierced := len(chain) > 1
		switch {
		case strings.HasPrefix(chain[0], "aria/"):
			value := strings.TrimPrefix(chain[0], "aria/")
			if match := ariaWithRolePattern.FindStringSubmatch(value); match != nil {
				candidates = append(candidates, selector.Candidate{
					Kind:       "role",
					Value:      selector.RoleSelector(match[2], match[1]),
					Confidence: selector.ConfidenceRoleName,
					Reason:     "Chrome Recorder ARIA selector with explicit role.",
				})
			} else if value != "" {
				candidates = append(candidates, selector.Candidate{
					Kind:       "text",
					Value:      selector.TextSelector(value),
					Confidence: selector.ConfidenceText,
					Reason:     "Chrome Recorder ARIA accessible-name selector; matched by visible text on replay.",
				})
			}
		case strings.HasPrefix(chain[0], "xpath/"):
			if pierced {
				continue // XPath cannot pierce shadow roots
			}
			value := strings.TrimPrefix(chain[0], "xpath/")
			candidates = append(candidates, selector.Candidate{
				Kind:       "xpath",
				Value:      value,
				Confidence: selector.ConfidenceXPath,
				Reason:     "Chrome Recorder XPath selector; validate stability on replay.",
			})
		case strings.HasPrefix(chain[0], "text/"):
			value := strings.TrimPrefix(chain[0], "text/")
			candidates = append(candidates, selector.Candidate{
				Kind:       "text",
				Value:      selector.TextSelector(value),
				Confidence: selector.ConfidenceText,
				Reason:     "Chrome Recorder text selector; verify uniqueness on replay.",
			})
		default:
			parts := make([]string, 0, len(chain))
			for _, part := range chain {
				parts = append(parts, strings.TrimPrefix(part, "pierce/"))
			}
			value := strings.Join(parts, " ")
			confidence := cssConfidence(value)
			reason := "Chrome Recorder CSS selector."
			if pierced || strings.HasPrefix(chain[0], "pierce/") {
				confidence = selector.ConfidenceCSSPath
				reason = "Chrome Recorder shadow-piercing CSS selector chain."
			}
			candidates = append(candidates, selector.Candidate{
				Kind:       "css",
				Value:      value,
				Confidence: confidence,
				Reason:     reason,
			})
		}
	}
	return selector.Unique(candidates)
}

// cssConfidence maps a plain CSS selector onto the shared confidence ladder by
// the locator family it leads with.
func cssConfidence(value string) int {
	switch {
	case strings.HasPrefix(value, "[data-test"), strings.HasPrefix(value, "[data-cy"), strings.HasPrefix(value, "[data-qa"):
		return selector.ConfidenceDataTestID
	case strings.HasPrefix(value, "#"):
		return selector.ConfidenceHTMLID
	case strings.HasPrefix(value, "[aria-label"):
		return selector.ConfidenceAriaLabel
	default:
		return selector.ConfidenceCapturedCSS
	}
}

func chromeStepLabel(step chromeStep) string {
	for _, chain := range step.Selectors {
		if len(chain) == 0 {
			continue
		}
		if strings.HasPrefix(chain[0], "aria/") {
			label := strings.TrimPrefix(chain[0], "aria/")
			if match := ariaWithRolePattern.FindStringSubmatch(label); match != nil {
				label = match[1]
			}
			if label != "" {
				return label
			}
		}
	}
	for _, chain := range step.Selectors {
		if len(chain) > 0 && chain[0] != "" {
			return chain[len(chain)-1]
		}
	}
	return "element"
}

func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func relativeTo(baseURL string, raw string) string {
	if baseURL != "" && strings.HasPrefix(raw, baseURL) {
		if trimmed := strings.TrimPrefix(raw, baseURL); trimmed != "" {
			return trimmed
		}
		return "/"
	}
	return raw
}
