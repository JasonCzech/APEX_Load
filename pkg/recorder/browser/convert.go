package browser

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// minRecordedThinkTimeMs filters out sub-perceptual gaps, mirroring the proxy
// recorder converter's threshold.
const minRecordedThinkTimeMs = 500

type ConvertOptions struct {
	ScriptName string
	// MaxThinkTimeMs caps recorded think time per action (0 = no cap).
	MaxThinkTimeMs int
}

type ConvertStats struct {
	TotalSteps       int `json:"total_steps"`
	ConvertedActions int `json:"converted_actions"`
	Navigations      int `json:"navigations"`
	Assertions       int `json:"assertions"`
	SyncPolicies     int `json:"sync_policies"`
	SkippedSteps     int `json:"skipped_steps"`
}

type ConvertResult struct {
	Script   *dsl.Script  `json:"script"`
	Warnings []string     `json:"warnings,omitempty"`
	Stats    ConvertStats `json:"stats"`
}

// timelineEntry interleaves recorded interaction steps with explicit
// navigations so the generated actions preserve real-world order.
type timelineEntry struct {
	timestamp time.Time
	step      *Step
	navURL    string
}

// Convert turns a recorded session into a browser-protocol DSL script with
// ranked selector metadata, inferred sync policies, recorded think times, and
// assertion actions. The session's side-car HAR is not consumed here — it stays
// available for network-level script generation through pkg/convert/har.
func Convert(session *Session, opts ConvertOptions) *ConvertResult {
	result := &ConvertResult{Stats: ConvertStats{TotalSteps: len(session.Steps)}}
	name := strings.TrimSpace(opts.ScriptName)
	if name == "" {
		name = "Recorded Browser Script"
	}

	baseURL, startTarget := splitStartURL(session.StartURL)
	headless := true
	clientMetrics := true
	script := &dsl.Script{
		Name:        name,
		Version:     "1.0",
		Protocol:    "browser",
		GeneratedBy: "browser-recorder",
		Config: dsl.ScriptConfig{
			BaseURL: baseURL,
			Browser: dsl.BrowserConfig{
				Engine:        "chromium",
				Headless:      &headless,
				ClientMetrics: &clientMetrics,
			},
		},
		CreatedAt: time.Now(),
	}

	actions := make([]dsl.Action, 0, len(session.Steps)+2)
	if session.StartURL != "" {
		actions = append(actions, dsl.Action{
			Name:   "Navigate to " + session.StartURL,
			Type:   "browser_navigate",
			Target: startTarget,
		})
		result.Stats.Navigations++
	}

	for _, entry := range buildTimeline(session) {
		if entry.navURL != "" {
			actions = append(actions, dsl.Action{
				Name:   "Navigate to " + entry.navURL,
				Type:   "browser_navigate",
				Target: relativeTarget(baseURL, entry.navURL),
			})
			result.Stats.Navigations++
			continue
		}
		action, ok := convertStep(*entry.step, session, opts, result)
		if !ok {
			result.Stats.SkippedSteps++
			continue
		}
		actions = append(actions, action)
	}

	script.Actions = actions
	result.Stats.ConvertedActions = len(actions)
	result.Script = script

	if len(session.WebSockets) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d WebSocket connection(s) observed during recording; the replayed page opens its own connections, and captured frames are available in the side-car HAR for network-level scripting", len(session.WebSockets)))
	}
	result.Warnings = append(result.Warnings, session.Warnings...)
	return result
}

func buildTimeline(session *Session) []timelineEntry {
	entries := make([]timelineEntry, 0, len(session.Steps))
	for i := range session.Steps {
		entries = append(entries, timelineEntry{timestamp: session.Steps[i].Timestamp, step: &session.Steps[i]})
	}
	for _, navIndex := range explicitNavigations(session.Steps, session.NavEvents) {
		nav := session.NavEvents[navIndex]
		entries = append(entries, timelineEntry{timestamp: nav.Timestamp, navURL: nav.URL})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].timestamp.Before(entries[j].timestamp) })
	return entries
}

func convertStep(step Step, session *Session, opts ConvertOptions, result *ConvertResult) (dsl.Action, bool) {
	stepIndex := step.Seq - 1
	best := step.bestSelector()
	action := dsl.Action{Selector: best, Target: best}

	switch step.Kind {
	case StepClick:
		action.Name = "Click " + stepLabel(step)
		action.Type = "browser_click"
	case StepFill:
		action.Name = "Fill " + stepLabel(step)
		action.Type = "browser_fill"
		action.Value = step.Value
	case StepSelect:
		action.Name = "Select " + stepLabel(step)
		action.Type = "browser_select"
		action.Value = step.Value
	case StepPressKey:
		action.Name = "Press " + step.Value
		action.Type = "browser_press_key"
		action.Value = step.Value
	case StepSetFiles:
		action.Name = "Upload files to " + stepLabel(step)
		action.Type = "browser_set_files"
		action.Value = step.Value
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"step %d uploads file(s) %q; replace the recorded names with paths readable by the replay engine", step.Seq, step.Value))
	case StepAssert:
		return assertAction(step, best, result)
	case StepSwitchWindow:
		action.Name = "Switch window"
		action.Type = "browser_switch_window"
		action.Selector = ""
		action.Target = step.Value
		return action, true
	default:
		return dsl.Action{}, false
	}

	if best == "" {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"step %d (%s) has no replayable selector; review the captured element before replay", step.Seq, step.Kind))
		if step.Kind != StepPressKey {
			return dsl.Action{}, false
		}
	}
	if sync := inferSync(session.Steps, stepIndex, session.NavEvents, session.NetworkEvents); sync != nil {
		action.Sync = sync
		result.Stats.SyncPolicies++
	}
	applyRecordedThinkTime(&action, session, stepIndex, opts.MaxThinkTimeMs)
	attachStepMetadata(&action, step)
	return action, true
}

// applyRecordedThinkTime encodes the recorded pause after this step onto the
// action (the engine plays per-action think time after the action when
// ThinkTimeAfter is set), skipping sub-perceptual gaps.
func applyRecordedThinkTime(action *dsl.Action, session *Session, stepIndex int, maxThinkTimeMs int) {
	if stepIndex < 0 || stepIndex+1 >= len(session.Steps) {
		return
	}
	gap := session.Steps[stepIndex+1].Timestamp.Sub(session.Steps[stepIndex].Timestamp).Milliseconds()
	if gap < minRecordedThinkTimeMs {
		return
	}
	if maxThinkTimeMs > 0 && gap > int64(maxThinkTimeMs) {
		gap = int64(maxThinkTimeMs)
	}
	action.ThinkTimeAfter = true
	action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(gap)}
}

func assertAction(step Step, best string, result *ConvertResult) (dsl.Action, bool) {
	if best == "" {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"assert step %d has no replayable selector and was skipped", step.Seq))
		return dsl.Action{}, false
	}
	action := dsl.Action{
		Name:     "Verify " + stepLabel(step),
		Type:     "browser_wait",
		Selector: best,
		Target:   best,
		WaitFor:  &dsl.WaitCondition{Type: "visible", Value: best},
		Assertions: []dsl.Assertion{
			{Type: "element_visible", Value: best},
		},
	}
	if text := strings.TrimSpace(step.Value); text != "" {
		action.Assertions = append(action.Assertions, dsl.Assertion{
			Type:     "element_text_contains",
			Value:    best,
			Expected: text,
		})
	}
	result.Stats.Assertions += len(action.Assertions)
	attachStepMetadata(&action, step)
	return action, true
}

// attachStepMetadata records the ranked selector candidates and frame context
// using the same metadata keys the TruClient importer emits and the replay
// engine reads (frame_selector, source_selector_*).
func attachStepMetadata(action *dsl.Action, step Step) {
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	if len(step.Candidates) > 0 {
		action.Metadata["source_selector_candidates"] = step.Candidates
		action.Metadata["source_selector_confidence"] = step.Candidates[0].Confidence
		if len(step.Candidates) > 1 {
			action.Metadata["source_selector_fallbacks"] = step.Candidates[1:]
		}
	}
	if step.FrameSelector != "" {
		action.Metadata["frame_selector"] = step.FrameSelector
	}
	if step.URL != "" {
		action.Metadata["source_url"] = step.URL
	}
	if step.Element != nil && step.Element.InShadowDOM {
		action.Metadata["source_shadow_dom"] = true
	}
}

func stepLabel(step Step) string {
	if step.Element != nil {
		for _, label := range []string{step.Element.AriaLabel, step.Element.Text, step.Element.Name, step.Element.ID, step.Element.Placeholder} {
			if trimmed := strings.TrimSpace(label); trimmed != "" {
				if len(trimmed) > 48 {
					trimmed = trimmed[:48] + "…"
				}
				return trimmed
			}
		}
		if step.Element.Tag != "" {
			return "<" + step.Element.Tag + ">"
		}
	}
	return "element"
}

func splitStartURL(raw string) (baseURL string, target string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", raw
	}
	baseURL = parsed.Scheme + "://" + parsed.Host
	target = parsed.Path
	if target == "" {
		target = "/"
	}
	if parsed.RawQuery != "" {
		target += "?" + parsed.RawQuery
	}
	return baseURL, target
}

func relativeTarget(baseURL string, raw string) string {
	if baseURL != "" && strings.HasPrefix(raw, baseURL) {
		if trimmed := strings.TrimPrefix(raw, baseURL); trimmed != "" {
			return trimmed
		}
		return "/"
	}
	return raw
}
