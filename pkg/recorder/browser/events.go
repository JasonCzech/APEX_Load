// Package browser implements the embedded interaction recorder: a headed
// Playwright-driven Chromium session that captures DOM interactions (with ranked
// selector candidates), main-frame navigations, network activity, and WebSocket
// traffic, and converts the captured session into a browser-protocol DSL script
// plus a side-car HAR. Design-time only — replay runs in pkg/protocol/browser.
package browser

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/recorder/marker"
	"github.com/jasonczech/apexload/pkg/selector"
)

// Step kinds produced by the capture script and recorder wiring.
const (
	StepNavigate     = "navigate"
	StepClick        = "click"
	StepDblClick     = "dblclick"
	StepDragAndDrop  = "drag_and_drop"
	StepHover        = "hover"
	StepFill         = "fill"
	StepSelect       = "select"
	StepScroll       = "scroll"
	StepPressKey     = "press_key"
	StepSetFiles     = "set_files"
	StepAssert       = "assert"
	StepSwitchWindow = "switch_window"
	// StepHandleDialog is appended Go-side from a page dialog (alert/confirm/
	// prompt), not from the capture script.
	StepHandleDialog = "handle_dialog"
)

// Recording sections map to the DSL script's init/actions/teardown blocks.
// Empty Section is treated as SectionActions.
const (
	SectionInit     = "init"
	SectionActions  = "actions"
	SectionTeardown = "teardown"
)

func normalizeSection(section string) string {
	switch strings.ToLower(strings.TrimSpace(section)) {
	case SectionInit:
		return SectionInit
	case SectionTeardown:
		return SectionTeardown
	default:
		return SectionActions
	}
}

// capturedEvent is the JSON payload reported by the injected capture script
// through the exposed binding. Field names match the script's payload keys.
type capturedEvent struct {
	Kind          string                    `json:"kind"`
	Value         string                    `json:"value,omitempty"`
	Values        []string                  `json:"values,omitempty"`
	Button        string                    `json:"button,omitempty"`
	Modifiers     []string                  `json:"modifiers,omitempty"`
	DeltaX        int                       `json:"delta_x,omitempty"`
	DeltaY        int                       `json:"delta_y,omitempty"`
	ScrollX       int                       `json:"scroll_x,omitempty"`
	ScrollY       int                       `json:"scroll_y,omitempty"`
	Masked        bool                      `json:"masked,omitempty"`
	TimestampMs   int64                     `json:"ts"`
	URL           string                    `json:"url,omitempty"`
	Title         string                    `json:"title,omitempty"`
	FrameSelector string                    `json:"frame_selector,omitempty"`
	FramePath     []string                  `json:"frame_path,omitempty"`
	Element       *selector.ElementMetadata `json:"element,omitempty"`
	TargetElement *selector.ElementMetadata `json:"target_element,omitempty"`
	// ViewportWidth and ViewportHeight are the top frame's page size when the
	// event was captured (absent for events from frames).
	ViewportWidth  int `json:"viewport_width,omitempty"`
	ViewportHeight int `json:"viewport_height,omitempty"`
}

const maxCapturedEventBatch = 8

type capturedEventBatch struct {
	Kind   string          `json:"kind"`
	Events []capturedEvent `json:"events"`
}

// Step is one recorded user interaction with everything replay needs: the
// ranked selector candidates, frame context, page URL, and timing. Masked
// marks fills whose value was withheld at capture (password inputs).
type Step struct {
	Seq              int                       `json:"seq"`
	Kind             string                    `json:"kind"`
	Value            string                    `json:"value,omitempty"`
	Values           []string                  `json:"values,omitempty"`
	Button           string                    `json:"button,omitempty"`
	Modifiers        []string                  `json:"modifiers,omitempty"`
	DeltaX           int                       `json:"delta_x,omitempty"`
	DeltaY           int                       `json:"delta_y,omitempty"`
	ScrollX          int                       `json:"scroll_x,omitempty"`
	ScrollY          int                       `json:"scroll_y,omitempty"`
	Masked           bool                      `json:"masked,omitempty"`
	Timestamp        time.Time                 `json:"timestamp"`
	URL              string                    `json:"url,omitempty"`
	Title            string                    `json:"title,omitempty"`
	FrameSelector    string                    `json:"frame_selector,omitempty"`
	FramePath        []string                  `json:"frame_path,omitempty"`
	Element          *selector.ElementMetadata `json:"element,omitempty"`
	Candidates       []selector.Candidate      `json:"candidates,omitempty"`
	TargetElement    *selector.ElementMetadata `json:"target_element,omitempty"`
	TargetSelector   string                    `json:"target_selector,omitempty"`
	TargetCandidates []selector.Candidate      `json:"target_candidates,omitempty"`
	// Section routes the converted action into the DSL init/actions/teardown
	// block. Empty means actions.
	Section string `json:"section,omitempty"`
	// Excluded steps are kept for review but skipped during conversion;
	// Deleted steps are tombstoned (delete-last-step) and also skipped.
	Excluded bool `json:"excluded,omitempty"`
	Deleted  bool `json:"deleted,omitempty"`
	// Thumbnail is an optional size-capped data:image/jpeg;base64 preview of
	// the page at capture time (per-step screenshots, opt-in).
	Thumbnail string `json:"thumbnail,omitempty"`
	// DialogType/DialogMessage describe a captured native dialog for
	// StepHandleDialog steps (alert|confirm|prompt).
	DialogType    string `json:"dialog_type,omitempty"`
	DialogMessage string `json:"dialog_message,omitempty"`
	// MarkSecret/MarkParam flag a fill the user marked (via the picker) as a
	// runtime secret or data parameter; conversion feeds these to paramize.
	MarkSecret bool `json:"mark_secret,omitempty"`
	MarkParam  bool `json:"mark_param,omitempty"`
	// ViewportWidth and ViewportHeight are the page size the step was
	// recorded at (absent for a step captured in a frame, or synthesized).
	// A desktop recording follows its window, so they change with a resize.
	ViewportWidth  int `json:"viewport_width,omitempty"`
	ViewportHeight int `json:"viewport_height,omitempty"`
}

func (s Step) skipForConvert() bool { return s.Excluded || s.Deleted }

// Event types delivered to Options.OnEvent observers.
const (
	EventStep           = "step"
	EventNavigation     = "navigation"
	EventNetworkRequest = "network_request"
	EventWebSocket      = "websocket"
	EventWarning        = "warning"
	// EventCapHit reports every bounded-capture drop, including repeats after
	// the one retained warning. This lets control-plane summaries expose exact
	// live truncation counts without cloning the full recording artifact.
	EventCapHit     = "cap_hit"
	EventPauseState = "pause_state"
	EventMarker     = "marker"
	// EventPickState reports the in-page element-picker (assert) mode toggling.
	EventPickState = "pick_state"
	// EventAnnotation reports a console error or failed request captured live.
	EventAnnotation = "annotation"
	// EventSection reports the active recording section changing.
	EventSection = "section"
	// EventStepUpdate reports an in-place step change (exclude/delete) so the
	// live feed can refresh without re-fetching the whole session.
	EventStepUpdate = "step_update"
	// EventStopRequested reports the in-page recorder controls requesting that
	// the API manager stop the recording through its normal async stop path.
	EventStopRequested = "stop_requested"
)

// Event notifies a live observer (e.g. the API control-panel manager) of
// recording activity as it happens. Exactly one payload field is set per type
// (Paused accompanies EventPauseState; Picking accompanies EventPickState).
type Event struct {
	Type       string
	Step       *Step
	Nav        *NavEvent
	Warning    string
	CapName    string
	CapCount   int64
	Paused     bool
	Picking    bool
	Marker     *Marker
	Annotation *Annotation
	Section    string
}

// Annotation is a non-interaction diagnostic captured during recording: a page
// console error or a failed network request, tied to the nearest step.
type Annotation struct {
	Kind      string    `json:"kind"` // "console_error" | "request_failed"
	Text      string    `json:"text"`
	URL       string    `json:"url,omitempty"`
	Status    int       `json:"status,omitempty"`
	NearStep  int       `json:"near_step,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Marker kinds inserted from the control panel (or in-page toolbar).
const (
	MarkerTransactionStart = marker.KindTransactionStart
	MarkerTransactionEnd   = marker.KindTransactionEnd
	MarkerNote             = marker.KindNote
)

// Marker is a user-inserted annotation in the recording timeline, shared with
// the proxy recorder.
type Marker = marker.Marker

// PauseInterval records one server-side pause window. To is zero while the
// pause is still open; Stop closes any open interval.
type PauseInterval struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to,omitempty"`
}

// NavEvent is a main-frame navigation observed during recording.
type NavEvent struct {
	Timestamp time.Time `json:"timestamp"`
	URL       string    `json:"url"`
	// SameDocument marks an in-page URL change (history.pushState or a
	// fragment), which Chromium reports as one; NavigationType is its kind
	// when Chromium names it (historyApi, fragment, other).
	SameDocument   bool   `json:"same_document,omitempty"`
	NavigationType string `json:"navigation_type,omitempty"`
}

// WebSocketSummary describes one WebSocket connection observed during recording.
type WebSocketSummary struct {
	URL            string    `json:"url"`
	OpenedAt       time.Time `json:"opened_at"`
	FramesSent     int       `json:"frames_sent"`
	FramesReceived int       `json:"frames_received"`
	// Frames holds size-capped, scrubbed frame payloads when payload capture is
	// opted in (off by default); otherwise only the counts above are recorded.
	Frames []WebSocketFrame `json:"frames,omitempty"`
}

// WebSocketFrame is one captured WebSocket frame payload (opt-in).
type WebSocketFrame struct {
	Direction string    `json:"direction"` // "sent" | "received"
	Payload   string    `json:"payload,omitempty"`
	Encoding  string    `json:"encoding,omitempty"` // "text" | "base64"
	SizeBytes int       `json:"size_bytes"`
	Truncated bool      `json:"truncated,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// HARFilters narrows the side-car HAR by request host before export/convert,
// mirroring the proxy recorder's include/exclude host semantics (substring
// match). Empty filters keep everything.
type HARFilters struct {
	IncludeHosts []string `json:"include_hosts,omitempty"`
	ExcludeHosts []string `json:"exclude_hosts,omitempty"`
}

func (f HARFilters) empty() bool {
	return len(f.IncludeHosts) == 0 && len(f.ExcludeHosts) == 0
}

// Session is the complete artifact of one recording run.
type Session struct {
	ID            string             `json:"id"`
	StartURL      string             `json:"start_url"`
	StartedAt     time.Time          `json:"started_at"`
	StoppedAt     time.Time          `json:"stopped_at,omitempty"`
	Steps         []Step             `json:"steps"`
	NavEvents     []NavEvent         `json:"nav_events,omitempty"`
	NetworkEvents []time.Time        `json:"network_events,omitempty"`
	WebSockets    []WebSocketSummary `json:"websockets,omitempty"`
	Markers       []Marker           `json:"markers,omitempty"`
	// OpenTransactions tracks started-but-not-ended transaction names in
	// start order (nesting allowed; ends must match an open name).
	OpenTransactions []string `json:"open_transactions,omitempty"`
	// PauseIntervals are server-side pause windows; conversion subtracts them
	// from recorded think times so a pause never becomes a giant think time.
	PauseIntervals []PauseInterval `json:"pause_intervals,omitempty"`
	// Annotations are console errors and failed requests observed during the
	// recording, tied to the nearest step.
	Annotations []Annotation `json:"annotations,omitempty"`
	// DeviceProfile is the emulated device preset applied at launch, if any.
	DeviceProfile        string                `json:"device_profile,omitempty"`
	RecordingEnvironment *RecordingEnvironment `json:"recording_environment,omitempty"`
	HARPath              string                `json:"har_path,omitempty"`
	HAR                  []byte                `json:"-"`
	// StorageState is the Playwright storage state (cookies/localStorage)
	// exported at stop, available for login reuse on a later recording.
	StorageState        []byte           `json:"-"`
	Warnings            []string         `json:"warnings,omitempty"`
	CapHits             map[string]int64 `json:"cap_hits,omitempty"`
	retainedMarkerBytes int
}

var validStepKinds = map[string]bool{
	StepNavigate:     true,
	StepClick:        true,
	StepDblClick:     true,
	StepDragAndDrop:  true,
	StepHover:        true,
	StepFill:         true,
	StepSelect:       true,
	StepScroll:       true,
	StepPressKey:     true,
	StepSetFiles:     true,
	StepAssert:       true,
	StepSwitchWindow: true,
}

// stepFromEvent decodes one capture-script payload into a Step, deriving ranked
// selector candidates from the captured element metadata.
func stepFromEvent(payload string) (Step, error) {
	var event capturedEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return Step{}, fmt.Errorf("decode capture event: %w", err)
	}
	return stepFromCapturedEvent(event)
}

// stepsFromEvent accepts the ordinary one-event payload and the small atomic
// batches used for semantically ordered interactions such as fill+Tab. The
// batch size is intentionally tiny so an arbitrary recorded page cannot use
// the exposed binding to bypass the recorder's per-event work bounds.
func stepsFromEvent(payload string) ([]Step, error) {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		return nil, fmt.Errorf("decode capture event: %w", err)
	}
	if probe.Kind != "capture_batch" {
		step, err := stepFromEvent(payload)
		if err != nil {
			return nil, err
		}
		return []Step{step}, nil
	}
	var batch capturedEventBatch
	if err := json.Unmarshal([]byte(payload), &batch); err != nil {
		return nil, fmt.Errorf("decode capture event batch: %w", err)
	}
	if len(batch.Events) == 0 || len(batch.Events) > maxCapturedEventBatch {
		return nil, fmt.Errorf("capture event batch must contain between 1 and %d events", maxCapturedEventBatch)
	}
	steps := make([]Step, 0, len(batch.Events))
	for _, event := range batch.Events {
		step, err := stepFromCapturedEvent(event)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func stepFromCapturedEvent(event capturedEvent) (Step, error) {
	kind := strings.TrimSpace(event.Kind)
	if !validStepKinds[kind] {
		return Step{}, fmt.Errorf("unsupported capture event kind %q", event.Kind)
	}
	step := Step{
		Kind:          kind,
		Value:         event.Value,
		Values:        compactStringSlice(event.Values),
		Button:        normalizeMouseButton(event.Button),
		Modifiers:     normalizeKeyboardModifiers(event.Modifiers),
		DeltaX:        event.DeltaX,
		DeltaY:        event.DeltaY,
		ScrollX:       event.ScrollX,
		ScrollY:       event.ScrollY,
		Masked:        event.Masked,
		Timestamp:     time.UnixMilli(event.TimestampMs),
		URL:           strings.TrimSpace(event.URL),
		Title:         strings.TrimSpace(event.Title),
		FrameSelector: strings.TrimSpace(event.FrameSelector),
		FramePath:     compactStringSlice(event.FramePath),
		Element:       event.Element,
		TargetElement: event.TargetElement,
	}
	// A size outside the engine's viewport limits is not one a replay could
	// use, so the step keeps none.
	if validRecordedViewport(event.ViewportWidth, event.ViewportHeight) {
		step.ViewportWidth, step.ViewportHeight = event.ViewportWidth, event.ViewportHeight
	}
	if event.TimestampMs <= 0 {
		step.Timestamp = time.Now()
	}
	if event.Element != nil {
		step.Candidates = selector.Candidates(*event.Element)
	}
	if event.TargetElement != nil {
		step.TargetCandidates = selector.Candidates(*event.TargetElement)
		step.TargetSelector = selector.PlaywrightSelector(step.TargetCandidates)
		if step.TargetSelector == "" {
			for _, candidate := range step.TargetCandidates {
				if value, ok := selector.Usable(candidate); ok {
					step.TargetSelector = value
					break
				}
			}
		}
	}
	return step, nil
}

func compactStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
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

func normalizeKeyboardModifiers(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		var normalized string
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "alt", "option":
			normalized = "Alt"
		case "control", "ctrl":
			normalized = "Control"
		case "controlormeta", "control_or_meta", "cmdorctrl", "commandorcontrol":
			normalized = "ControlOrMeta"
		case "meta", "cmd", "command":
			normalized = "Meta"
		case "shift":
			normalized = "Shift"
		}
		if normalized != "" && !seen[normalized] {
			out = append(out, normalized)
			seen[normalized] = true
		}
	}
	return out
}

// appendStep adds a step to the session, assigning the sequence number and
// collapsing duplicate delivery of the same fill event. Distinct edits can
// enter the same value again after an application clears the control, so only
// identical event timestamps may be merged. Change/blur deduplication belongs
// to the capture script, which can distinguish those notifications from edits.
func (s *Session) appendStep(step Step) Step {
	if len(s.Steps) > 0 && step.Kind == StepFill {
		last := &s.Steps[len(s.Steps)-1]
		if last.Kind == StepFill && !last.Excluded && !last.Deleted && last.Timestamp.Equal(step.Timestamp) && last.Value == step.Value && last.Masked == step.Masked && normalizeSection(last.Section) == normalizeSection(step.Section) && last.URL == step.URL && last.FrameSelector == step.FrameSelector && slices.Equal(last.FramePath, step.FramePath) && sameElement(last.Element, step.Element) {
			last.Value = step.Value
			last.Masked = step.Masked
			last.Timestamp = step.Timestamp
			return *last
		}
	}
	step.Seq = len(s.Steps) + 1
	s.Steps = append(s.Steps, step)
	return step
}

func validRecordedViewport(width, height int) bool {
	return width > 0 && height > 0 && width <= maxBrowserViewportDimension &&
		height <= maxBrowserViewportDimension && int64(width)*int64(height) <= maxBrowserViewportPixels
}

// updateSwitchWindowStep replaces the provisional about:blank target captured
// when a popup opens with the popup's first real navigation URL.
func (s *Session) updateSwitchWindowStep(seq int, targetURL string) (*Step, bool) {
	targetURL = strings.TrimSpace(targetURL)
	if isBlankPageURL(targetURL) {
		return nil, false
	}
	for i := range s.Steps {
		if s.Steps[i].Seq != seq || s.Steps[i].Kind != StepSwitchWindow {
			continue
		}
		if s.Steps[i].Value == targetURL && s.Steps[i].URL == targetURL {
			return nil, false
		}
		s.Steps[i].Value = targetURL
		s.Steps[i].URL = targetURL
		step := s.Steps[i]
		return &step, true
	}
	return nil, false
}

func isBlankPageURL(raw string) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	return raw == "" || raw == "about:blank"
}

// clone deep-copies the session's slices so callers can read a stable snapshot
// while recording continues. Element/candidate pointers are shared but treated
// as immutable after capture.
func (s *Session) clone() *Session {
	clone := *s
	clone.RecordingEnvironment = s.RecordingEnvironment.Clone()
	clone.Steps = append([]Step(nil), s.Steps...)
	for i := range clone.Steps {
		clone.Steps[i].Values = append([]string(nil), s.Steps[i].Values...)
		clone.Steps[i].Modifiers = append([]string(nil), s.Steps[i].Modifiers...)
		clone.Steps[i].FramePath = append([]string(nil), s.Steps[i].FramePath...)
		clone.Steps[i].Candidates = append([]selector.Candidate(nil), s.Steps[i].Candidates...)
		clone.Steps[i].TargetCandidates = append([]selector.Candidate(nil), s.Steps[i].TargetCandidates...)
	}
	clone.NavEvents = append([]NavEvent(nil), s.NavEvents...)
	clone.NetworkEvents = append([]time.Time(nil), s.NetworkEvents...)
	clone.WebSockets = append([]WebSocketSummary(nil), s.WebSockets...)
	clone.Markers = append([]Marker(nil), s.Markers...)
	clone.OpenTransactions = append([]string(nil), s.OpenTransactions...)
	clone.PauseIntervals = append([]PauseInterval(nil), s.PauseIntervals...)
	clone.Annotations = append([]Annotation(nil), s.Annotations...)
	clone.Warnings = append([]string(nil), s.Warnings...)
	clone.CapHits = make(map[string]int64, len(s.CapHits))
	for name, count := range s.CapHits {
		clone.CapHits[name] = count
	}
	if len(s.HAR) > 0 {
		clone.HAR = append([]byte(nil), s.HAR...)
	}
	if len(s.StorageState) > 0 {
		clone.StorageState = append([]byte(nil), s.StorageState...)
	}
	return &clone
}

// Clone deep-copies the session for callers outside the recorder package.
func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	return s.clone()
}

// deleteLastStep tombstones the most recent non-deleted step and returns it.
// Callers hold the recorder mutex.
func (s *Session) deleteLastStep() (*Step, bool) {
	for i := len(s.Steps) - 1; i >= 0; i-- {
		if !s.Steps[i].Deleted {
			s.Steps[i].Deleted = true
			step := s.Steps[i]
			return &step, true
		}
	}
	return nil, false
}

// setStepExcluded toggles the exclude flag on the step with the given seq.
func (s *Session) setStepExcluded(seq int, excluded bool) (*Step, bool) {
	for i := range s.Steps {
		if s.Steps[i].Seq == seq {
			if s.Steps[i].Deleted {
				return nil, false
			}
			s.Steps[i].Excluded = excluded
			step := s.Steps[i]
			return &step, true
		}
	}
	return nil, false
}

// PromoteStepSelector prepends a replay-verified selector to a stopped session
// step so the next conversion uses it as the primary selector while preserving
// the original captured candidates as fallbacks.
func (s *Session) PromoteStepSelector(seq int, sel string) (*Step, bool) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return nil, false
	}
	for i := range s.Steps {
		if s.Steps[i].Seq == seq {
			if s.Steps[i].Deleted {
				return nil, false
			}
			promoted := selector.Candidate{
				Kind:       promotedSelectorKind(sel),
				Value:      sel,
				Confidence: 100,
				Reason:     "self-heal promotion",
			}
			next := []selector.Candidate{promoted}
			for _, candidate := range s.Steps[i].Candidates {
				if strings.EqualFold(candidate.Kind, promoted.Kind) && strings.TrimSpace(candidate.Value) == sel {
					continue
				}
				next = append(next, candidate)
			}
			s.Steps[i].Candidates = next
			step := s.Steps[i]
			return &step, true
		}
	}
	return nil, false
}

func promotedSelectorKind(sel string) string {
	lower := strings.ToLower(strings.TrimSpace(sel))
	switch {
	case strings.HasPrefix(lower, "xpath="), strings.HasPrefix(lower, "//"), strings.HasPrefix(lower, "(//"):
		return "xpath"
	case strings.HasPrefix(lower, "role="):
		return "role"
	case strings.HasPrefix(lower, "text="):
		return "text"
	default:
		return "css"
	}
}

// AddMarker validates and appends a marker, maintaining open-transaction
// state. Not goroutine-safe: the Recorder calls it under its mutex; other
// callers (test fakes) must synchronize themselves.
func (s *Session) AddMarker(m Marker) (Marker, error) {
	book := s.markerBook()
	added, err := book.Add(m, marker.DefaultLimits)
	s.setMarkerBook(book)
	return added, err
}

func (s *Session) markerBook() marker.Book {
	return marker.Book{Markers: s.Markers, OpenTransactions: s.OpenTransactions, RetainedBytes: s.retainedMarkerBytes}
}

func (s *Session) setMarkerBook(book marker.Book) {
	s.Markers, s.OpenTransactions, s.retainedMarkerBytes = book.Markers, book.OpenTransactions, book.RetainedBytes
}

// TransactionWindow is one transaction marked while recording, from its start
// marker to its end marker (see marker.Window).
type TransactionWindow = marker.Window

// TransactionWindows pairs the session's transaction markers in recorded
// order: an end closes the latest open transaction of its name.
func (s *Session) TransactionWindows() []TransactionWindow {
	return marker.Windows(s.Markers)
}

func sameElement(a, b *selector.ElementMetadata) bool {
	if a == nil || b == nil {
		return false
	}
	if a.CSSPath != "" || b.CSSPath != "" {
		return a.CSSPath == b.CSSPath
	}
	return a.Tag == b.Tag && a.ID == b.ID && a.Name == b.Name
}

// bestSelector returns the strongest replayable selector for the step:
// replay-verified self-heal promotions first (including role/text), then the
// normal css/xpath promotion, then any engine-usable candidate.
func (s Step) bestSelector() string {
	for _, candidate := range s.Candidates {
		if !strings.Contains(strings.ToLower(candidate.Reason), "self-heal promotion") {
			continue
		}
		if value, ok := selector.Usable(candidate); ok {
			return value
		}
	}
	if promoted := selector.PlaywrightSelector(s.Candidates); promoted != "" {
		return promoted
	}
	for _, candidate := range s.Candidates {
		if value, ok := selector.Usable(candidate); ok {
			return value
		}
	}
	return ""
}
