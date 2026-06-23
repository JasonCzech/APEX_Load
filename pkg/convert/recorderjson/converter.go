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
	StepMapping []StepMapping    `json:"step_mapping,omitempty"`
}

type StepMapping struct {
	SourceIndex     int    `json:"source_index,omitempty"`
	SourceLocation  string `json:"source_location,omitempty"`
	SourceStepType  string `json:"source_step_type,omitempty"`
	ActionIndex     *int   `json:"action_index,omitempty"`
	Name            string `json:"name,omitempty"`
	Type            string `json:"type,omitempty"`
	TargetPath      string `json:"target_path,omitempty"`
	RecorderStepSeq int    `json:"recorder_step_seq,omitempty"`
	Disposition     string `json:"disposition"`
	Reason          string `json:"reason,omitempty"`
}

// chromeRecording is the Chrome DevTools Recorder export format
// (https://developer.chrome.com/docs/devtools/recorder): a title plus a flat
// list of typed steps whose selectors arrays carry ranked locator candidates.
type chromeRecording struct {
	Title string       `json:"title"`
	Steps []chromeStep `json:"steps"`
}

type chromeStep struct {
	Type               string               `json:"type"`
	URL                string               `json:"url,omitempty"`
	Key                string               `json:"key,omitempty"`
	Value              string               `json:"value,omitempty"`
	Expression         string               `json:"expression,omitempty"`
	Width              int                  `json:"width,omitempty"`
	Height             int                  `json:"height,omitempty"`
	X                  int                  `json:"x,omitempty"`
	Y                  int                  `json:"y,omitempty"`
	DistancePercentage float64              `json:"distancePercentage,omitempty"`
	Timeout            int                  `json:"timeout,omitempty"`
	OffsetX            int                  `json:"offsetX,omitempty"`
	OffsetY            int                  `json:"offsetY,omitempty"`
	Duration           int                  `json:"duration,omitempty"`
	Button             string               `json:"button,omitempty"`
	DeviceType         string               `json:"deviceType,omitempty"`
	Target             string               `json:"target,omitempty"`
	Frame              []int                `json:"frame,omitempty"`
	Operator           string               `json:"operator,omitempty"`
	Count              int                  `json:"count,omitempty"`
	Visible            *bool                `json:"visible,omitempty"`
	Properties         map[string]string    `json:"properties,omitempty"`
	Attributes         map[string]string    `json:"attributes,omitempty"`
	DeviceScaleFactor  float64              `json:"deviceScaleFactor,omitempty"`
	IsMobile           bool                 `json:"isMobile,omitempty"`
	HasTouch           bool                 `json:"hasTouch,omitempty"`
	IsLandscape        bool                 `json:"isLandscape,omitempty"`
	Latency            int                  `json:"latency,omitempty"`
	DownloadThroughput int                  `json:"downloadThroughput,omitempty"`
	UploadThroughput   int                  `json:"uploadThroughput,omitempty"`
	Selectors          [][]string           `json:"selectors,omitempty"`
	AssertedEvents     []chromeAssertedItem `json:"assertedEvents,omitempty"`
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
		sourceIndex := i + 1
		switch step.Type {
		case "setViewport":
			if step.Width > 0 && step.Height > 0 {
				script.Config.Browser.Viewport = dsl.BrowserViewport{Width: step.Width, Height: step.Height}
			}
			if step.HasTouch {
				touch := true
				script.Config.Browser.TouchMode = &touch
			}
			if step.IsLandscape {
				script.Config.Browser.Orientation = "landscape"
			}
			if step.IsMobile {
				script.Config.Browser.DeviceProfile = convertutil.FirstNonEmpty(script.Config.Browser.DeviceProfile, "mobile")
			}
			ensureChromeImportMetadata(script)["viewport"] = chromeStepMetadata(step)
			appendConfigStepMapping(result, sourceIndex, location, step.Type, "mapped", "Applied viewport and device settings to the script browser configuration.")
		case "emulateNetworkConditions":
			script.Config.WebRuntime.NetworkSimulation.Mode = "custom"
			if step.Latency > 0 {
				script.Config.WebRuntime.NetworkSimulation.LatencyMs = step.Latency
			}
			if step.DownloadThroughput > 0 {
				script.Config.WebRuntime.NetworkSimulation.BandwidthKbps = step.DownloadThroughput / 1024
			}
			ensureChromeImportMetadata(script)["network_conditions"] = chromeStepMetadata(step)
			appendConfigStepMapping(result, sourceIndex, location, step.Type, "mapped", "Applied Chrome network conditions to script runtime settings.")
		case "navigate":
			if script.Config.BaseURL == "" {
				script.Config.BaseURL = originOf(step.URL)
			}
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: step.Type,
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, dsl.Action{
				Name:   "Navigate to " + step.URL,
				Type:   "browser_navigate",
				Target: relativeTo(script.Config.BaseURL, step.URL),
			})
		case "click", "doubleClick", "hover":
			action, ok := chromeElementAction(step, location, result)
			if !ok {
				appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "No replayable selector was available for the element action.")
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
			attachChromeActionMetadata(&action, step)
			applyAssertedNavigation(&action, step)
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: step.Type,
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, action)
		case "change":
			action, ok := chromeElementAction(step, location, result)
			if !ok {
				appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "No replayable selector was available for the change step.")
				continue
			}
			action.Type = "browser_fill"
			action.Name = "Fill " + chromeStepLabel(step)
			action.Value = step.Value
			attachChromeActionMetadata(&action, step)
			applyAssertedNavigation(&action, step)
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: step.Type,
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, action)
		case "keyDown":
			if key := strings.TrimSpace(step.Key); key != "" {
				action := dsl.Action{
					Name:  "Press " + key,
					Type:  "browser_press_key",
					Value: key,
				}
				attachChromeActionMetadata(&action, step)
				appendImportedAction(script, result, StepMapping{
					SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: step.Type,
					RecorderStepSeq: sourceIndex, Disposition: "mapped",
				}, action)
			} else {
				appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "Chrome keyDown step had no key value.")
			}
		case "keyUp":
			// The paired keyDown already produced the press action.
			appendConfigStepMapping(result, sourceIndex, location, step.Type, "mapped", "Paired keyUp is represented by the browser_press_key action emitted for keyDown.")
		case "waitForElement":
			action, ok := chromeElementAction(step, location, result)
			if !ok {
				appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "No replayable selector was available for the waitForElement step.")
				continue
			}
			action.Type = "browser_wait"
			action.Name = "Wait for " + chromeStepLabel(step)
			waitType := "visible"
			if step.Visible != nil && !*step.Visible {
				waitType = "hidden"
			}
			action.WaitFor = &dsl.WaitCondition{Type: waitType, Value: action.Selector, TimeoutMs: step.Timeout}
			if action.Params == nil {
				action.Params = map[string]any{}
			}
			if step.Operator != "" {
				action.Params["operator"] = step.Operator
			}
			if step.Count > 0 {
				action.Params["count"] = step.Count
			}
			if len(step.Properties) > 0 {
				action.Params["properties"] = step.Properties
			}
			if len(step.Attributes) > 0 {
				action.Params["attributes"] = step.Attributes
			}
			attachChromeActionMetadata(&action, step)
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: step.Type,
				RecorderStepSeq: sourceIndex, Disposition: "mapped",
			}, action)
		case "waitForExpression":
			expr := strings.TrimSpace(step.Expression)
			if expr == "" {
				result.Stats.SkippedSteps++
				unsupported.Add(diag.Warning, "chrome_recorder_step:"+step.Type, location,
					"Chrome waitForExpression step has no expression and was skipped.")
				appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "Chrome waitForExpression step had no expression.")
				continue
			}
			timeout := step.Timeout
			if timeout <= 0 {
				timeout = 10000
			}
			action := dsl.Action{
				Name:      "Wait for expression",
				Type:      "browser_evaluate_js",
				Target:    chromeWaitForExpressionScript(expr, timeout),
				TimeoutMs: timeout,
				Metadata: map[string]any{
					"review_required":    true,
					"compatibility_gate": "chrome_recorder_wait_for_expression",
					"source_expression":  expr,
				},
			}
			attachChromeActionMetadata(&action, step)
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: step.Type,
				RecorderStepSeq: sourceIndex, Disposition: "degraded",
				Reason: "Converted waitForExpression to review-gated JavaScript evaluation.",
			}, action)
		case "scroll":
			targetScript, scrollReason := chromeScrollScript(step, "")
			action := dsl.Action{
				Name:   "Scroll page",
				Type:   "browser_evaluate_js",
				Target: targetScript,
				Metadata: map[string]any{
					"review_required":    true,
					"compatibility_gate": "chrome_recorder_scroll",
				},
			}
			if len(step.Selectors) > 0 {
				if elementAction, ok := chromeElementActionNoSkip(step); ok {
					elementScript, elementReason := chromeScrollScript(step, elementAction.Selector)
					action.Selector = elementAction.Selector
					action.Target = elementScript
					action.Metadata = elementAction.Metadata
					action.Metadata["review_required"] = true
					action.Metadata["compatibility_gate"] = "chrome_recorder_element_scroll"
					scrollReason = elementReason
				}
			}
			attachChromeActionMetadata(&action, step)
			appendImportedAction(script, result, StepMapping{
				SourceIndex: sourceIndex, SourceLocation: location, SourceStepType: step.Type,
				RecorderStepSeq: sourceIndex, Disposition: "degraded",
				Reason: scrollReason,
			}, action)
		case "close":
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "chrome_recorder_step:"+step.Type, location,
				"Chrome close step was preserved as a review diagnostic; browser replay keeps the active page open.")
			appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "Chrome close is unsupported in browser replay imports; replay keeps the active page open.")
		case "customStep":
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "chrome_recorder_step:"+step.Type, location,
				"Chrome customStep requires manual review before it can be replayed.")
			appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "Chrome customStep requires manual review before replay.")
		default:
			result.Stats.SkippedSteps++
			unsupported.Add(diag.Warning, "chrome_recorder_step:"+step.Type, location,
				"Chrome DevTools Recorder step type is not convertible to a browser action; review the recording for coverage gaps.")
			appendUnsupportedStepMapping(result, sourceIndex, location, step.Type, "Chrome DevTools Recorder step type is not convertible to a browser action.")
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

func appendImportedAction(script *dsl.Script, result *ConvertResult, mapping StepMapping, action dsl.Action) {
	actionIndex := len(script.Init) + len(script.Actions) + len(script.Teardown)
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	if mapping.SourceStepType != "" {
		action.Metadata["source_step_type"] = mapping.SourceStepType
	}
	if mapping.SourceIndex > 0 {
		action.Metadata["source_step_index"] = mapping.SourceIndex
	}
	if mapping.RecorderStepSeq > 0 {
		action.Metadata["recorder_step_seq"] = mapping.RecorderStepSeq
	}
	if mapping.Disposition != "" {
		action.Metadata["import_disposition"] = mapping.Disposition
	}
	if mapping.Reason != "" {
		action.Metadata["import_disposition_reason"] = mapping.Reason
	}
	script.Actions = append(script.Actions, action)
	mapping.ActionIndex = &actionIndex
	mapping.Name = action.Name
	mapping.Type = action.Type
	mapping.TargetPath = fmt.Sprintf("actions[%d]", len(script.Actions)-1)
	if mapping.Disposition == "" {
		mapping.Disposition = "mapped"
	}
	result.StepMapping = append(result.StepMapping, mapping)
}

func appendConfigStepMapping(result *ConvertResult, sourceIndex int, location, sourceType, disposition, reason string) {
	result.StepMapping = append(result.StepMapping, StepMapping{
		SourceIndex:     sourceIndex,
		SourceLocation:  location,
		SourceStepType:  sourceType,
		RecorderStepSeq: sourceIndex,
		Disposition:     disposition,
		Reason:          reason,
	})
}

func appendUnsupportedStepMapping(result *ConvertResult, sourceIndex int, location, sourceType, reason string) {
	appendConfigStepMapping(result, sourceIndex, location, sourceType, "unsupported", reason)
}

func chromeScrollScript(step chromeStep, selectorValue string) (string, string) {
	target := "window"
	if selectorValue != "" {
		target = fmt.Sprintf("document.querySelector(%q)", selectorValue)
	}
	if step.DistancePercentage != 0 {
		script := fmt.Sprintf(`(() => {
  const target = %s;
  if (!target) throw new Error("scroll target not found");
  const amount = Math.round((target === window ? window.innerHeight : target.clientHeight) * %g / 100);
  target.scrollBy(0, amount);
})()`, target, step.DistancePercentage)
		return script, "Converted Chrome scroll distancePercentage to review-gated JavaScript scrollBy."
	}
	if selectorValue != "" {
		return fmt.Sprintf(`(() => {
  const target = %s;
  if (!target) throw new Error("scroll target not found");
  target.scrollTo(%d, %d);
})()`, target, step.X, step.Y), "Converted element scroll coordinates to review-gated JavaScript."
	}
	return fmt.Sprintf("window.scrollTo(%d, %d)", step.X, step.Y), "Converted page scroll coordinates to review-gated JavaScript."
}

// chromeElementAction builds the shared element-action skeleton: ranked
// candidates from the step's selectors arrays, best-selector promotion, and
// the same source_selector_* metadata the embedded recorder emits.
func chromeElementAction(step chromeStep, location string, result *ConvertResult) (dsl.Action, bool) {
	action, ok := chromeElementActionNoSkip(step)
	if ok {
		return action, true
	}
	result.Stats.SkippedSteps++
	result.Warnings = append(result.Warnings, fmt.Sprintf("%s (%s) has no replayable selector and was skipped", location, step.Type))
	return dsl.Action{}, false
}

func chromeElementActionNoSkip(step chromeStep) (dsl.Action, bool) {
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

func ensureChromeImportMetadata(script *dsl.Script) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "chrome_devtools_recorder"}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	return script.Metadata.Import.Preserved
}

func attachChromeActionMetadata(action *dsl.Action, step chromeStep) {
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	for key, value := range chromeStepMetadata(step) {
		action.Metadata[key] = value
	}
	if step.Timeout > 0 && action.TimeoutMs == 0 {
		action.TimeoutMs = step.Timeout
	}
}

func chromeStepMetadata(step chromeStep) map[string]any {
	metadata := map[string]any{
		"source_format":    "chrome_devtools_recorder",
		"source_step_type": step.Type,
	}
	if step.Target != "" {
		metadata["source_target"] = step.Target
	}
	if len(step.Frame) > 0 {
		metadata["source_frame"] = step.Frame
	}
	if step.Timeout > 0 {
		metadata["source_timeout_ms"] = step.Timeout
	}
	if step.Button != "" {
		metadata["source_click_button"] = step.Button
	}
	if step.OffsetX != 0 || step.OffsetY != 0 {
		metadata["source_click_offset"] = map[string]int{"x": step.OffsetX, "y": step.OffsetY}
	}
	if step.DistancePercentage != 0 {
		metadata["source_scroll_distance_percentage"] = step.DistancePercentage
	}
	if step.Duration > 0 {
		metadata["source_duration_ms"] = step.Duration
	}
	if step.DeviceType != "" {
		metadata["source_device_type"] = step.DeviceType
	}
	if step.DeviceScaleFactor > 0 {
		metadata["source_device_scale_factor"] = step.DeviceScaleFactor
	}
	if step.IsMobile {
		metadata["source_is_mobile"] = true
	}
	if step.HasTouch {
		metadata["source_has_touch"] = true
	}
	if step.IsLandscape {
		metadata["source_is_landscape"] = true
	}
	if step.Latency > 0 {
		metadata["source_network_latency_ms"] = step.Latency
	}
	if step.DownloadThroughput > 0 {
		metadata["source_download_throughput"] = step.DownloadThroughput
	}
	if step.UploadThroughput > 0 {
		metadata["source_upload_throughput"] = step.UploadThroughput
	}
	return metadata
}

func chromeWaitForExpressionScript(expression string, timeoutMs int) string {
	return fmt.Sprintf(`(() => new Promise((resolve, reject) => {
  const deadline = Date.now() + %d;
  const check = () => {
    try {
      if ((%s)) {
        resolve(true);
        return;
      }
      if (Date.now() >= deadline) {
        reject(new Error("Chrome Recorder waitForExpression timed out"));
        return;
      }
      setTimeout(check, 100);
    } catch (err) {
      reject(err);
    }
  };
  check();
}))()`, timeoutMs, expression)
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
