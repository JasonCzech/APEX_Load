package browser

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// minRecordedThinkTimeMs filters out sub-perceptual gaps, mirroring the proxy
// recorder converter's threshold.
const minRecordedThinkTimeMs = 500

type ConvertOptions struct {
	ScriptName string
	// MaxThinkTimeMs caps recorded think time per action (0 = no cap).
	MaxThinkTimeMs int
	// StepRange trims conversion to an inclusive 1-based step-sequence window;
	// zero bounds mean "from the start" / "to the end".
	StepRange StepRange
	// SplitAtTransactions emits one script per top-level transaction instead of
	// a single combined script.
	SplitAtTransactions bool
}

// StepRange is an inclusive 1-based step-sequence window. Zero bounds are open.
type StepRange struct {
	StartSeq int `json:"start_seq,omitempty"`
	EndSeq   int `json:"end_seq,omitempty"`
}

func (r StepRange) contains(seq int) bool {
	if r.StartSeq > 0 && seq < r.StartSeq {
		return false
	}
	if r.EndSeq > 0 && seq > r.EndSeq {
		return false
	}
	return true
}

func (r StepRange) empty() bool { return r.StartSeq == 0 && r.EndSeq == 0 }

type ConvertStats struct {
	TotalSteps       int `json:"total_steps"`
	ConvertedActions int `json:"converted_actions"`
	Navigations      int `json:"navigations"`
	Assertions       int `json:"assertions"`
	SyncPolicies     int `json:"sync_policies"`
	SkippedSteps     int `json:"skipped_steps"`
	Transactions     int `json:"transactions"`
	Notes            int `json:"notes"`
	Dialogs          int `json:"dialogs"`
	Scripts          int `json:"scripts"`
}

type ConvertResult struct {
	Script   *dsl.Script  `json:"script"`
	Warnings []string     `json:"warnings,omitempty"`
	Stats    ConvertStats `json:"stats"`
	// Scripts holds every generated script. For the single-script path it has
	// exactly one element equal to Script; for SplitAtTransactions it has one
	// per transaction.
	Scripts []*dsl.Script `json:"scripts,omitempty"`
}

// TransactionSuggestion proposes a transaction boundary at a page navigation.
type TransactionSuggestion struct {
	Name      string `json:"name"`
	AtStepSeq int    `json:"at_step_seq"`
	URL       string `json:"url"`
}

// sectionedAction pairs a generated action with its DSL section.
type sectionedAction struct {
	action  dsl.Action
	section string
}

// timelineEntry interleaves recorded interaction steps with explicit
// navigations and user-inserted markers so the generated actions preserve
// real-world order.
type timelineEntry struct {
	timestamp time.Time
	step      *Step
	navURL    string
	marker    *Marker
	wsURL     string
	wsFrame   *WebSocketFrame
	wsOpen    bool
}

// Convert turns a recorded session into a browser-protocol DSL script with
// ranked selector metadata, inferred sync policies, recorded think times,
// assertion actions, transaction markers, and section routing. The session's
// side-car HAR is not consumed here — it stays available for network-level
// script generation through pkg/convert/har. With opts.SplitAtTransactions the
// result carries one script per top-level transaction in result.Scripts.
func Convert(session *Session, opts ConvertOptions) *ConvertResult {
	result := &ConvertResult{Stats: ConvertStats{TotalSteps: len(session.Steps)}}
	name := strings.TrimSpace(opts.ScriptName)
	if name == "" {
		name = "Recorded Browser Script"
	}
	baseURL, startTarget := splitStartURL(session.StartURL)

	assembled := assembleActions(session, opts, baseURL, result)

	if opts.SplitAtTransactions {
		scripts := splitByTransaction(assembled, name, session, baseURL, startTarget, result)
		if len(scripts) > 0 {
			result.Scripts = scripts
			result.Script = scripts[0]
			result.Stats.Scripts = len(scripts)
			result.Stats.ConvertedActions = countScriptActions(scripts)
			finalizeConvertWarnings(session, result)
			return result
		}
		result.Warnings = append(result.Warnings,
			"split at transactions requested, but no transactions were found — produced a single script instead")
	}

	script := newBrowserScript(name, session, baseURL)
	if session.StartURL != "" {
		nav := sectionedAction{
			action:  dsl.Action{Name: "Navigate to " + session.StartURL, Type: "browser_navigate", Target: startTarget},
			section: firstSection(assembled),
		}
		assembled = append([]sectionedAction{nav}, assembled...)
		result.Stats.Navigations++
	}
	partitionInto(script, assembled)
	result.Script = script
	result.Scripts = []*dsl.Script{script}
	result.Stats.Scripts = 1
	result.Stats.ConvertedActions = len(assembled)
	finalizeConvertWarnings(session, result)
	return result
}

// assembleActions walks the trimmed timeline and produces section-tagged
// actions, threading transaction tags and notes onto the actions they enclose.
func assembleActions(session *Session, opts ConvertOptions, baseURL string, result *ConvertResult) []sectionedAction {
	out := make([]sectionedAction, 0, len(session.Steps)+len(session.Markers))
	var txnStack []string
	var pendingNotes []string
	lastSection := SectionActions

	setMeta := func(action *dsl.Action, key string, value any) {
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata[key] = value
	}
	decorate := func(action *dsl.Action, tagTransaction bool) {
		if tagTransaction && len(txnStack) > 0 {
			setMeta(action, "source_transaction", txnStack[len(txnStack)-1])
		}
		if len(pendingNotes) > 0 {
			setMeta(action, "note", strings.Join(pendingNotes, "; "))
			pendingNotes = nil
		}
	}

	for _, entry := range buildTimeline(session, opts.StepRange) {
		switch {
		case entry.wsOpen || entry.wsFrame != nil:
			action, ok := websocketWaitAction(entry)
			if !ok {
				continue
			}
			decorate(&action, true)
			out = append(out, sectionedAction{action: action, section: lastSection})
			result.Stats.SyncPolicies++
		case entry.marker != nil:
			marker := entry.marker
			switch marker.Kind {
			case MarkerTransactionStart:
				action := dsl.Action{Name: marker.Name, Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction"}}
				decorate(&action, false)
				out = append(out, sectionedAction{action: action, section: lastSection})
				txnStack = append(txnStack, marker.Name)
				result.Stats.Transactions++
			case MarkerTransactionEnd:
				index := -1
				for i, n := range txnStack {
					if n == marker.Name {
						index = i
					}
				}
				if index < 0 {
					result.Warnings = append(result.Warnings, fmt.Sprintf(
						"transaction end %q has no matching start and was skipped", marker.Name))
					continue
				}
				txnStack = append(txnStack[:index], txnStack[index+1:]...)
				action := dsl.Action{Name: marker.Name, Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction"}}
				decorate(&action, false)
				out = append(out, sectionedAction{action: action, section: lastSection})
			case MarkerNote:
				pendingNotes = append(pendingNotes, marker.Text)
				result.Stats.Notes++
			}
		case entry.navURL != "":
			action := dsl.Action{Name: "Navigate to " + entry.navURL, Type: "browser_navigate", Target: relativeTarget(baseURL, entry.navURL)}
			decorate(&action, true)
			out = append(out, sectionedAction{action: action, section: lastSection})
			result.Stats.Navigations++
		default:
			step := *entry.step
			lastSection = normalizeSection(step.Section)
			action, ok := convertStep(step, session, opts, result)
			if !ok {
				result.Stats.SkippedSteps++
				continue
			}
			decorate(&action, true)
			sa := sectionedAction{action: action, section: lastSection}
			// A dialog fires from the action just before it, but replay arms the
			// handler one-shot — so place handle_dialog before its trigger. Only
			// reorder past an actual interaction; a preceding marker or
			// navigation is not the trigger, so leave the handler after it.
			if step.Kind == StepHandleDialog && len(out) > 0 && isDialogTrigger(out[len(out)-1].action) {
				out = append(out[:len(out)-1], sa, out[len(out)-1])
			} else {
				out = append(out, sa)
			}
		}
	}

	if len(pendingNotes) > 0 {
		if len(out) > 0 {
			last := &out[len(out)-1].action
			note := strings.Join(pendingNotes, "; ")
			if existing, ok := last.Metadata["note"].(string); ok && existing != "" {
				note = existing + "; " + note
			}
			setMeta(last, "note", note)
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%d note(s) were recorded but no actions were generated to attach them to", len(pendingNotes)))
		}
	}
	return out
}

// isDialogTrigger reports whether an action is the kind of interaction that can
// raise a native dialog (so a handle_dialog handler should be armed before it).
// Markers, navigations, and the handler itself are not triggers.
func isDialogTrigger(action dsl.Action) bool {
	switch action.Type {
	case "source_transaction", "browser_navigate", "browser_handle_dialog", "browser_switch_window":
		return false
	default:
		return strings.HasPrefix(action.Type, "browser_")
	}
}

func newBrowserScript(name string, session *Session, baseURL string) *dsl.Script {
	headless := true
	clientMetrics := true
	browser := dsl.BrowserConfig{Engine: "chromium", Headless: &headless, ClientMetrics: &clientMetrics}
	if session.DeviceProfile != "" {
		browser.DeviceProfile = session.DeviceProfile
	}
	return &dsl.Script{
		Name:        name,
		Version:     "1.0",
		Protocol:    "browser",
		GeneratedBy: "browser-recorder",
		Config:      dsl.ScriptConfig{BaseURL: baseURL, Browser: browser},
		CreatedAt:   time.Now(),
	}
}

// partitionInto routes section-tagged actions into the script's init/actions/
// teardown blocks, preserving order within each block.
func partitionInto(script *dsl.Script, assembled []sectionedAction) {
	for _, sa := range assembled {
		switch sa.section {
		case SectionInit:
			script.Init = append(script.Init, sa.action)
		case SectionTeardown:
			script.Teardown = append(script.Teardown, sa.action)
		default:
			script.Actions = append(script.Actions, sa.action)
		}
	}
}

// firstSection returns the section of the first assembled action (for placing
// the initial navigate), defaulting to actions.
func firstSection(assembled []sectionedAction) string {
	if len(assembled) > 0 {
		return assembled[0].section
	}
	return SectionActions
}

// splitByTransaction produces one script per top-level transaction. The
// source_transaction wrapper actions are dropped (each script IS that
// transaction); every script inherits the initial navigate.
func splitByTransaction(assembled []sectionedAction, baseName string, session *Session, baseURL, startTarget string, result *ConvertResult) []*dsl.Script {
	var scripts []*dsl.Script
	depth := 0
	var current *dsl.Script
	var currentActions []sectionedAction
	droppedOutside := 0

	flush := func() {
		if current == nil {
			return
		}
		// Prepend the initial navigate as a section-tagged action so it lands in
		// the same DSL block as the transaction's first step, not always Actions.
		if session.StartURL != "" {
			nav := sectionedAction{
				action:  dsl.Action{Name: "Navigate to " + session.StartURL, Type: "browser_navigate", Target: startTarget},
				section: firstSection(currentActions),
			}
			currentActions = append([]sectionedAction{nav}, currentActions...)
		}
		partitionInto(current, currentActions)
		scripts = append(scripts, current)
		current = nil
		currentActions = nil
	}

	for _, sa := range assembled {
		isTxn := sa.action.Type == "source_transaction"
		fn, _ := sa.action.Params["function"].(string)
		switch {
		case isTxn && fn == "lr_start_transaction":
			if depth == 0 {
				current = newBrowserScript(fmt.Sprintf("%s — %s", baseName, sa.action.Name), session, baseURL)
				currentActions = nil
			} else {
				currentActions = append(currentActions, sa)
			}
			depth++
		case isTxn && fn == "lr_end_transaction":
			if depth > 0 {
				if depth > 1 {
					currentActions = append(currentActions, sa)
				}
				depth--
				if depth == 0 {
					flush()
				}
			}
		default:
			if depth > 0 {
				currentActions = append(currentActions, sa)
			} else {
				// Any action recorded outside a transaction (including
				// navigations between transactions) is omitted from split output.
				droppedOutside++
			}
		}
	}
	flush()
	if droppedOutside > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d action(s) recorded outside any transaction were omitted from the split scripts", droppedOutside))
	}
	return scripts
}

func countScriptActions(scripts []*dsl.Script) int {
	total := 0
	for _, s := range scripts {
		total += len(s.Init) + len(s.Actions) + len(s.Teardown)
	}
	return total
}

func finalizeConvertWarnings(session *Session, result *ConvertResult) {
	if len(session.WebSockets) > 0 {
		framesCaptured := 0
		for _, ws := range session.WebSockets {
			framesCaptured += len(ws.Frames)
		}
		detail := "connection-level browser_ws_wait actions were generated"
		if framesCaptured > 0 {
			detail = fmt.Sprintf("%d captured frame payload(s) were converted into browser_ws_wait synchronization where possible", framesCaptured)
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d WebSocket connection(s) observed during recording; the replayed page opens its own connections, and %s", len(session.WebSockets), detail))
	}
	if n := len(session.Annotations); n > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d console error(s)/failed request(s) were observed during recording — review them before relying on the script", n))
	}
	result.Warnings = append(result.Warnings, session.Warnings...)
}

// SuggestTransactionsFromSession proposes a transaction boundary at each page
// navigation, naming it from the destination path. Suggestions are anchored to
// the first non-skipped step after the navigation.
func SuggestTransactionsFromSession(session *Session) []TransactionSuggestion {
	if session == nil {
		return nil
	}
	var suggestions []TransactionSuggestion
	seen := map[string]bool{}
	for _, nav := range session.NavEvents {
		name := transactionNameFromURL(nav.URL)
		if name == "" || seen[name] {
			continue
		}
		seq := firstStepAfter(session.Steps, nav.Timestamp)
		if seq == 0 {
			continue
		}
		seen[name] = true
		suggestions = append(suggestions, TransactionSuggestion{Name: name, AtStepSeq: seq, URL: nav.URL})
	}
	return suggestions
}

func firstStepAfter(steps []Step, ts time.Time) int {
	for i := range steps {
		if steps[i].skipForConvert() {
			continue
		}
		if !steps[i].Timestamp.Before(ts) {
			return steps[i].Seq
		}
	}
	return 0
}

func transactionNameFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		if parsed.Host != "" {
			return titleCaseWords(strings.SplitN(parsed.Host, ".", 2)[0])
		}
		return ""
	}
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	last = strings.ReplaceAll(strings.ReplaceAll(last, "-", " "), "_", " ")
	last = strings.TrimSpace(last)
	if last == "" {
		return ""
	}
	return titleCaseWords(last)
}

// titleCaseWords upper-cases the first letter of each whitespace-separated word
// (a small, dependency-free replacement for the deprecated strings.Title).
func titleCaseWords(s string) string {
	fields := strings.Fields(s)
	for i, w := range fields {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		fields[i] = string(r)
	}
	return strings.Join(fields, " ")
}

func buildTimeline(session *Session, stepRange StepRange) []timelineEntry {
	entries := make([]timelineEntry, 0, len(session.Steps)+len(session.Markers))
	var lower, upper time.Time
	haveLower, haveUpper := false, false
	for i := range session.Steps {
		if session.Steps[i].skipForConvert() {
			continue
		}
		if !stepRange.empty() && !stepRange.contains(session.Steps[i].Seq) {
			continue
		}
		entries = append(entries, timelineEntry{timestamp: session.Steps[i].Timestamp, step: &session.Steps[i]})
		if !haveLower {
			lower, haveLower = session.Steps[i].Timestamp, true
		}
		upper, haveUpper = session.Steps[i].Timestamp, true
	}
	// When trimming, only keep navs/markers within the surviving step window so
	// dangling transaction ends and orphan navigations don't leak in.
	within := func(ts time.Time) bool {
		if stepRange.empty() {
			return true
		}
		if haveLower && ts.Before(lower) {
			return false
		}
		if haveUpper && ts.After(upper) {
			return false
		}
		return true
	}
	for _, navIndex := range explicitNavigations(session.Steps, session.NavEvents) {
		nav := session.NavEvents[navIndex]
		if within(nav.Timestamp) {
			entries = append(entries, timelineEntry{timestamp: nav.Timestamp, navURL: nav.URL})
		}
	}
	for i := range session.Markers {
		if within(session.Markers[i].Timestamp) {
			entries = append(entries, timelineEntry{timestamp: session.Markers[i].Timestamp, marker: &session.Markers[i]})
		}
	}
	for _, ws := range session.WebSockets {
		payloadFrames := 0
		for i := range ws.Frames {
			frame := ws.Frames[i]
			if frame.Direction != "received" || strings.TrimSpace(frame.Payload) == "" {
				continue
			}
			ts := frame.Timestamp
			if ts.IsZero() {
				ts = ws.OpenedAt
			}
			if within(ts) {
				entries = append(entries, timelineEntry{timestamp: ts, wsURL: ws.URL, wsFrame: &ws.Frames[i]})
				payloadFrames++
			}
		}
		if payloadFrames == 0 && !ws.OpenedAt.IsZero() && within(ws.OpenedAt) {
			entries = append(entries, timelineEntry{timestamp: ws.OpenedAt, wsURL: ws.URL, wsOpen: true})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].timestamp.Before(entries[j].timestamp) })
	return entries
}

func websocketWaitAction(entry timelineEntry) (dsl.Action, bool) {
	params := map[string]any{
		"url_contains": entry.wsURL,
		"count":        1,
		"timeout_ms":   10000,
	}
	name := "Wait for WebSocket"
	if entry.wsOpen {
		params["direction"] = "open"
		if entry.wsURL != "" {
			name = "Wait for WebSocket open " + entry.wsURL
		}
		return dsl.Action{Name: name, Type: "browser_ws_wait", Params: params}, true
	}
	if entry.wsFrame == nil {
		return dsl.Action{}, false
	}
	params["direction"] = "received"
	payload := strings.TrimSpace(entry.wsFrame.Payload)
	if payload != "" {
		params["match"] = truncateString(payload, 160)
		params["match_mode"] = "contains"
	}
	if entry.wsURL != "" {
		name = "Wait for WebSocket frame " + entry.wsURL
	}
	return dsl.Action{Name: name, Type: "browser_ws_wait", Params: params}, true
}

func truncateString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func convertStep(step Step, session *Session, opts ConvertOptions, result *ConvertResult) (dsl.Action, bool) {
	stepIndex := step.Seq - 1
	best := step.bestSelector()
	action := dsl.Action{Selector: best, Target: best}

	switch step.Kind {
	case StepClick:
		action.Name = "Click " + stepLabel(step)
		action.Type = "browser_click"
	case StepDblClick:
		action.Name = "Double-click " + stepLabel(step)
		action.Type = "browser_dblclick"
	case StepDragAndDrop:
		action.Name = "Drag " + stepLabel(step)
		action.Type = "browser_drag_and_drop"
		if strings.TrimSpace(step.TargetSelector) == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"step %d drag-and-drop has no replayable drop target selector and was skipped", step.Seq))
			return dsl.Action{}, false
		}
		action.Params = map[string]any{"target": step.TargetSelector}
	case StepFill:
		action.Name = "Fill " + stepLabel(step)
		action.Type = "browser_fill"
		action.Value = step.Value
		if step.Masked || step.MarkSecret {
			key := secretEnvKey(step)
			action.Value = "{{env." + key + "}}"
			reason := "filled a password input; the value was masked at capture"
			if !step.Masked {
				reason = "was marked as a secret"
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"step %d %s — provide it at runtime via {{env.%s}}", step.Seq, reason, key))
		} else if step.MarkParam {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"step %d was marked as a data parameter; review it in the parameterization step", step.Seq))
		}
	case StepHandleDialog:
		params := map[string]any{"action": "accept", "match_mode": "contains"}
		if step.DialogType != "" {
			params["dialog_type"] = step.DialogType
			action.Name = "Handle " + step.DialogType + " dialog"
		} else {
			action.Name = "Handle dialog"
		}
		if msg := strings.TrimSpace(step.DialogMessage); msg != "" {
			params["expected_message"] = msg
		}
		action.Type = "browser_handle_dialog"
		action.Selector = ""
		action.Target = ""
		action.Params = params
		result.Stats.Dialogs++
		return action, true
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
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["source_upload_fixture"] = true
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"step %d uploads file(s) %q; bind the recorded names to fixture paths readable by the replay engine during parameterization", step.Seq, step.Value))
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
// ThinkTimeAfter is set), skipping sub-perceptual gaps. Server-side pause
// windows are subtracted so pausing a recording never inflates think time.
func applyRecordedThinkTime(action *dsl.Action, session *Session, stepIndex int, maxThinkTimeMs int) {
	if stepIndex < 0 || stepIndex >= len(session.Steps) {
		return
	}
	// The think time is the gap to the next step that survives conversion, so
	// excluding or deleting a step folds its dwell into the kept neighbours.
	nextIndex := -1
	for i := stepIndex + 1; i < len(session.Steps); i++ {
		if !session.Steps[i].skipForConvert() {
			nextIndex = i
			break
		}
	}
	if nextIndex < 0 {
		return
	}
	cur := session.Steps[stepIndex].Timestamp
	next := session.Steps[nextIndex].Timestamp
	gap := (next.Sub(cur) - pausedDurationBetween(session.PauseIntervals, cur, next)).Milliseconds()
	if gap < minRecordedThinkTimeMs {
		return
	}
	if maxThinkTimeMs > 0 && gap > int64(maxThinkTimeMs) {
		gap = int64(maxThinkTimeMs)
	}
	action.ThinkTimeAfter = true
	action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(gap)}
}

// pausedDurationBetween sums the overlap of pause intervals with [from, to].
// An interval still open (zero To) is clamped to the window end.
func pausedDurationBetween(intervals []PauseInterval, from, to time.Time) time.Duration {
	var total time.Duration
	for _, interval := range intervals {
		start := interval.From
		end := interval.To
		if end.IsZero() {
			end = to
		}
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	return total
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
	if step.Seq > 0 {
		action.Metadata["recorder_step_seq"] = step.Seq
		action.Metadata["source_step_index"] = step.Seq
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
	if step.Masked {
		action.Metadata["source_masked_secret"] = true
	}
	if len(step.TargetCandidates) > 0 {
		action.Metadata["source_target_selector_candidates"] = step.TargetCandidates
	}
}

// secretEnvKey derives a stable env-var name for a masked fill from the
// element's identity (name attr, id, placeholder), defaulting to PASSWORD.
func secretEnvKey(step Step) string {
	candidates := []string{}
	if step.Element != nil {
		candidates = append(candidates, step.Element.Name, step.Element.ID, step.Element.Placeholder, step.Element.AriaLabel)
	}
	for _, raw := range candidates {
		if key := sanitizeEnvKey(raw); key != "" {
			return key
		}
	}
	return "PASSWORD"
}

func sanitizeEnvKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(raw) {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	key := strings.Trim(b.String(), "_")
	if key == "" {
		return ""
	}
	if key[0] >= '0' && key[0] <= '9' {
		key = "SECRET_" + key
	}
	return key
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
