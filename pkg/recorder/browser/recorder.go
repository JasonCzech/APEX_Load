package browser

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

//go:embed capture.js
var captureScript string

// DeviceEmulation carries a resolved device preset's context options. The API
// layer resolves it from the protocol device catalog so the recorder package
// stays dependency-light.
type DeviceEmulation struct {
	Name              string
	Width             int
	Height            int
	DeviceScaleFactor float64
	Mobile            bool
	HasTouch          bool
	UserAgent         string
}

// Options configures one interactive recording run.
type Options struct {
	// StartURL is opened in the recording browser. Required.
	StartURL string
	// Headless launches without a window. Interactive recording wants a headed
	// browser; headless exists for automated tests of the capture pipeline.
	Headless bool
	// CaptureHAR records the context's network traffic to a side-car HAR
	// (with content) flushed when the recording stops. Default on via New.
	CaptureHAR bool
	// HARPath overrides where the side-car HAR is written. Empty uses a
	// temporary file that is read into Session.HAR and removed on Stop.
	HARPath string
	// HARFilters narrows the side-car HAR by request host at stop time.
	HARFilters HARFilters
	// OnEvent, when set, observes recording activity live (steps, navigations,
	// warnings). Called outside the recorder's lock; implementations must not
	// block for long and may be invoked from Playwright callback goroutines.
	OnEvent func(Event)
	// Device, when set, emulates a mobile/tablet/desktop preset.
	Device *DeviceEmulation
	// StorageStatePath, when set, seeds the context with a saved Playwright
	// storage state (cookies/localStorage) so authenticated flows skip login.
	StorageStatePath string
	// CaptureWSPayloads opts into recording size-capped, scrubbed WebSocket
	// frame payloads (default off — only counts are kept).
	CaptureWSPayloads bool
	// WSPayloadLimitBytes caps a captured frame payload (0 = default).
	WSPayloadLimitBytes int
	// CaptureScreenshots opts into per-step viewport thumbnails.
	CaptureScreenshots bool
	// Section is the initial recording section (init|actions|teardown).
	Section string
	// Window position/size hints (Chromium launch args). 0 leaves the default.
	WindowX      int
	WindowY      int
	WindowWidth  int
	WindowHeight int
	// Scrubber redacts WebSocket frame payloads before storage. Injected by the
	// API layer (which owns pkg/scrub); nil disables payload redaction.
	Scrubber func(string) string
}

// Recorder drives one headed Chromium session and accumulates a Session.
type Recorder struct {
	opts    Options
	pw      *playwright.Playwright
	browser playwright.Browser
	context playwright.BrowserContext

	mu             sync.Mutex
	session        *Session
	stopped        bool
	paused         bool
	assertMode     bool
	currentSection string
	activePage     playwright.Page

	thumbWG  sync.WaitGroup
	done     chan struct{}
	doneOnce sync.Once
	harTemp  bool
}

// EnsureBrowsers downloads the Playwright driver and Chromium if they are not
// already installed. Idempotent, but the first run can be slow (hundreds of MB).
func EnsureBrowsers() error {
	return playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}})
}

// New prepares a recorder; Start launches the browser.
func New(opts Options) *Recorder {
	device := ""
	if opts.Device != nil {
		device = opts.Device.Name
	}
	return &Recorder{
		opts:           opts,
		currentSection: normalizeSection(opts.Section),
		session: &Session{
			ID:            fmt.Sprintf("rec_%d", time.Now().UnixNano()),
			StartURL:      strings.TrimSpace(opts.StartURL),
			StartedAt:     time.Now(),
			DeviceProfile: device,
		},
		done: make(chan struct{}),
	}
}

// Start launches Chromium, injects the capture script into every frame, wires
// the record binding plus navigation/network/WebSocket listeners, and opens the
// start URL. The user interacts with the page; Stop (or closing the browser)
// finishes the session.
func (r *Recorder) Start() error {
	startURL := r.session.StartURL
	if startURL == "" {
		return fmt.Errorf("browser recorder requires a start URL")
	}
	if parsed, err := url.Parse(startURL); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("browser recorder start URL %q must be absolute", startURL)
	}

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("start playwright: %w", err)
	}
	r.pw = pw

	launchOptions := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(r.opts.Headless),
		Timeout:  playwright.Float(30000),
	}
	if args := r.windowArgs(); len(args) > 0 {
		launchOptions.Args = args
	}
	browser, err := pw.Chromium.Launch(launchOptions)
	if err != nil {
		r.cleanup()
		return fmt.Errorf("launch chromium: %w", err)
	}
	r.browser = browser
	browser.OnDisconnected(func(playwright.Browser) { r.signalDone() })

	contextOptions := playwright.BrowserNewContextOptions{}
	if r.opts.CaptureHAR {
		harPath := strings.TrimSpace(r.opts.HARPath)
		if harPath == "" {
			harPath = filepath.Join(os.TempDir(), r.session.ID+".har")
			r.harTemp = true
		}
		r.session.HARPath = harPath
		contextOptions.RecordHarPath = playwright.String(harPath)
		contextOptions.RecordHarContent = playwright.HarContentPolicyEmbed
	}
	if d := r.opts.Device; d != nil {
		if d.Width > 0 && d.Height > 0 {
			contextOptions.Viewport = &playwright.Size{Width: d.Width, Height: d.Height}
		}
		if d.UserAgent != "" {
			contextOptions.UserAgent = playwright.String(d.UserAgent)
		}
		if d.DeviceScaleFactor > 0 {
			contextOptions.DeviceScaleFactor = playwright.Float(d.DeviceScaleFactor)
		}
		contextOptions.IsMobile = playwright.Bool(d.Mobile)
		contextOptions.HasTouch = playwright.Bool(d.HasTouch)
	}
	if path := strings.TrimSpace(r.opts.StorageStatePath); path != "" {
		if _, statErr := os.Stat(path); statErr != nil {
			r.cleanup()
			return fmt.Errorf("storage state %q unavailable: %w", path, statErr)
		}
		contextOptions.StorageStatePath = playwright.String(path)
	}
	context, err := browser.NewContext(contextOptions)
	if err != nil {
		r.cleanup()
		return fmt.Errorf("create browser context: %w", err)
	}
	r.context = context

	if err := context.ExposeBinding("__apexloadRecord", r.handleRecordBinding); err != nil {
		r.cleanup()
		return fmt.Errorf("expose recorder binding: %w", err)
	}
	if err := context.AddInitScript(playwright.Script{Content: playwright.String(captureScript)}); err != nil {
		r.cleanup()
		return fmt.Errorf("inject capture script: %w", err)
	}
	context.OnPage(r.registerPage)

	page, err := context.NewPage()
	if err != nil {
		r.cleanup()
		return fmt.Errorf("open recording page: %w", err)
	}
	if _, err := page.Goto(startURL); err != nil {
		r.cleanup()
		return fmt.Errorf("navigate to %s: %w", startURL, err)
	}
	return nil
}

// registerPage wires capture listeners for the initial page and any popup.
// New pages become the active page (the browser focuses them on open).
func (r *Recorder) registerPage(page playwright.Page) {
	r.mu.Lock()
	r.activePage = page
	isPopup := len(r.session.NavEvents) > 0 || len(r.session.Steps) > 0
	paused := r.paused
	var recorded *Step
	if isPopup && !paused {
		step := r.session.appendStep(Step{
			Kind:      StepSwitchWindow,
			Value:     page.URL(),
			Timestamp: time.Now(),
			URL:       page.URL(),
		})
		recorded = &step
	}
	r.mu.Unlock()
	if recorded != nil {
		r.emit(Event{Type: EventStep, Step: recorded})
	}

	page.OnFrameNavigated(func(frame playwright.Frame) {
		if frame != page.MainFrame() {
			return
		}
		nav := NavEvent{Timestamp: time.Now(), URL: frame.URL()}
		r.mu.Lock()
		paused := r.paused
		if !paused {
			r.session.NavEvents = append(r.session.NavEvents, nav)
		}
		r.mu.Unlock()
		if paused {
			// The fresh document's capture script starts unpaused; re-sync so
			// the in-page toolbar and client-side gating match the server.
			r.pushPauseState(page, true)
			return
		}
		r.emit(Event{Type: EventNavigation, Nav: &nav})
	})
	page.OnRequest(func(playwright.Request) {
		r.mu.Lock()
		if !r.paused {
			r.session.NetworkEvents = append(r.session.NetworkEvents, time.Now())
		}
		r.mu.Unlock()
	})
	page.OnWebSocket(func(ws playwright.WebSocket) {
		r.mu.Lock()
		if r.paused {
			r.mu.Unlock()
			return
		}
		r.session.WebSockets = append(r.session.WebSockets, WebSocketSummary{URL: ws.URL(), OpenedAt: time.Now()})
		index := len(r.session.WebSockets) - 1
		r.mu.Unlock()
		ws.OnFrameSent(func(payload []byte) { r.recordWSFrame(index, "sent", payload) })
		ws.OnFrameReceived(func(payload []byte) { r.recordWSFrame(index, "received", payload) })
	})
	// Native dialogs (alert/confirm/prompt) stall the page until handled, so we
	// always accept them and record a handle_dialog step for replay fidelity.
	page.OnDialog(func(dialog playwright.Dialog) {
		message := dialog.Message()
		dialogType := dialog.Type()
		_ = dialog.Accept()
		r.mu.Lock()
		paused := r.paused
		var recorded *Step
		if !paused {
			step := r.session.appendStep(Step{
				Kind:          StepHandleDialog,
				DialogType:    dialogType,
				DialogMessage: message,
				Section:       r.currentSection,
				Timestamp:     time.Now(),
				URL:           page.URL(),
			})
			recorded = &step
		}
		r.mu.Unlock()
		if recorded != nil {
			r.emit(Event{Type: EventStep, Step: recorded})
		}
	})
	page.OnConsole(func(msg playwright.ConsoleMessage) {
		if msg.Type() != "error" {
			return
		}
		r.recordAnnotation(Annotation{Kind: "console_error", Text: strings.TrimSpace(msg.Text()), URL: page.URL()})
	})
	page.OnRequestFailed(func(req playwright.Request) {
		failure := req.Failure()
		text := req.Method() + " " + req.URL()
		if failure != nil {
			text += " — " + failure.Error()
		}
		r.recordAnnotation(Annotation{Kind: "request_failed", Text: text, URL: req.URL()})
	})
	page.OnResponse(func(resp playwright.Response) {
		if status := resp.Status(); status >= 400 {
			r.recordAnnotation(Annotation{
				Kind:   "request_failed",
				Text:   fmt.Sprintf("%d %s", status, resp.URL()),
				URL:    resp.URL(),
				Status: status,
			})
		}
	})
	page.OnClose(func(closed playwright.Page) {
		r.mu.Lock()
		if r.activePage == closed {
			r.activePage = nil
			if r.context != nil {
				if pages := r.context.Pages(); len(pages) > 0 {
					r.activePage = pages[len(pages)-1]
				}
			}
		}
		r.mu.Unlock()
		if r.context == nil || len(r.context.Pages()) == 0 {
			r.signalDone()
		}
	})
}

// handleRecordBinding receives capture-script payloads. The binding source
// frame resolves cross-origin frames the script cannot identify itself.
func (r *Recorder) handleRecordBinding(source *playwright.BindingSource, args ...any) any {
	if len(args) == 0 {
		return nil
	}
	payload, ok := args[0].(string)
	if !ok {
		return nil
	}
	// Control events from the in-page toolbar round-trip through the server so
	// the panel, recorder state, and every page stay in sync.
	switch controlEventKind(payload) {
	case "pause_toggle":
		r.mu.Lock()
		paused := r.paused
		r.mu.Unlock()
		r.setPaused(!paused)
		return nil
	case "pick_toggle":
		r.mu.Lock()
		assert := r.assertMode
		r.mu.Unlock()
		r.SetAssertMode(!assert)
		return nil
	}
	r.mu.Lock()
	if r.paused {
		r.mu.Unlock()
		return nil
	}
	section := r.currentSection
	r.mu.Unlock()
	step, err := stepFromEvent(payload)
	if err != nil {
		warning := fmt.Sprintf("dropped capture event: %v", err)
		r.mu.Lock()
		r.session.Warnings = append(r.session.Warnings, warning)
		r.mu.Unlock()
		r.emit(Event{Type: EventWarning, Warning: warning})
		return nil
	}
	if step.FrameSelector == "" && source != nil && source.Frame != nil && source.Page != nil {
		if source.Frame != source.Page.MainFrame() {
			step.FrameSelector = frameSelectorFromURL(source.Frame.URL())
		}
	}
	step.Section = section
	r.mu.Lock()
	recorded := r.session.appendStep(step)
	r.mu.Unlock()
	r.emit(Event{Type: EventStep, Step: &recorded})
	if r.opts.CaptureScreenshots && source != nil && source.Page != nil {
		r.captureThumbnailAsync(source.Page, recorded.Seq)
	}
	return nil
}

// emit delivers an event to the configured observer. Never called while
// holding r.mu, so observers may call back into the recorder.
func (r *Recorder) emit(event Event) {
	if r.opts.OnEvent != nil {
		r.opts.OnEvent(event)
	}
}

// Snapshot returns a stable copy of the in-progress (or finished) session.
func (r *Recorder) Snapshot() *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session.clone()
}

// Paused reports the server-side pause state.
func (r *Recorder) Paused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

// Pause suspends capture: steps, navigations, network, and WebSocket events
// are dropped until Resume. The pause window is recorded so conversion can
// subtract it from think times.
func (r *Recorder) Pause() { r.setPaused(true) }

// Resume re-enables capture after Pause.
func (r *Recorder) Resume() { r.setPaused(false) }

func (r *Recorder) setPaused(paused bool) {
	r.mu.Lock()
	if r.stopped || r.paused == paused {
		r.mu.Unlock()
		return
	}
	r.paused = paused
	now := time.Now()
	if paused {
		r.session.PauseIntervals = append(r.session.PauseIntervals, PauseInterval{From: now})
	} else if n := len(r.session.PauseIntervals); n > 0 && r.session.PauseIntervals[n-1].To.IsZero() {
		r.session.PauseIntervals[n-1].To = now
	}
	pages := r.openPagesLocked()
	r.mu.Unlock()

	for _, page := range pages {
		r.pushPauseState(page, paused)
	}
	r.emit(Event{Type: EventPauseState, Paused: paused})
}

// pushPauseState syncs the in-page toolbar and client-side gating with the
// server's authoritative pause flag. Best effort: the server gate drops
// events regardless.
func (r *Recorder) pushPauseState(page playwright.Page, paused bool) {
	if page == nil {
		return
	}
	_, _ = page.Evaluate(`(paused) => { if (window.__apexloadRecorderSetPaused) window.__apexloadRecorderSetPaused(paused) }`, paused)
}

func (r *Recorder) openPagesLocked() []playwright.Page {
	if r.context == nil {
		return nil
	}
	return r.context.Pages()
}

// Navigate drives the active recording page to a URL (control-panel "go to").
// While paused this positions the browser off the record — the navigation
// listener drops the event, which is the point.
func (r *Recorder) Navigate(targetURL string) error {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return fmt.Errorf("navigate requires a URL")
	}
	page := r.currentPage()
	if page == nil {
		return fmt.Errorf("no open recording page to navigate")
	}
	if _, err := page.Goto(targetURL); err != nil {
		return fmt.Errorf("navigate to %s: %w", targetURL, err)
	}
	return nil
}

// Focus brings the active recording window to the front.
func (r *Recorder) Focus() error {
	page := r.currentPage()
	if page == nil {
		return fmt.Errorf("no open recording page to focus")
	}
	if err := page.BringToFront(); err != nil {
		return fmt.Errorf("focus recording browser: %w", err)
	}
	return nil
}

func (r *Recorder) currentPage() playwright.Page {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activePage != nil {
		return r.activePage
	}
	if pages := r.openPagesLocked(); len(pages) > 0 {
		return pages[len(pages)-1]
	}
	return nil
}

// AddMarker inserts a transaction boundary or note into the timeline.
func (r *Recorder) AddMarker(kind, name, text string) (Marker, error) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return Marker{}, fmt.Errorf("recording already stopped")
	}
	marker, err := r.session.AddMarker(Marker{
		Kind:      strings.TrimSpace(kind),
		Name:      strings.TrimSpace(name),
		Text:      strings.TrimSpace(text),
		Timestamp: time.Now(),
	})
	r.mu.Unlock()
	if err != nil {
		return Marker{}, err
	}
	r.emit(Event{Type: EventMarker, Marker: &marker})
	return marker, nil
}

// controlEventKind decodes just the payload kind so toolbar control events
// can be routed before step parsing (and regardless of pause state).
func controlEventKind(payload string) string {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(payload), &probe); err != nil {
		return ""
	}
	switch probe.Kind {
	case "pause_toggle", "pick_toggle":
		return probe.Kind
	}
	return ""
}

// frameSelectorFromURL builds a replayable iframe selector for cross-origin
// frames where the capture script cannot read frameElement.
func frameSelectorFromURL(frameURL string) string {
	parsed, err := url.Parse(frameURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	match := parsed.Host + parsed.Path
	return fmt.Sprintf(`iframe[src*="%s"]`, strings.ReplaceAll(match, `"`, `\"`))
}

// Done is closed when the user closes the browser (or it disconnects).
func (r *Recorder) Done() <-chan struct{} { return r.done }

func (r *Recorder) signalDone() { r.doneOnce.Do(func() { close(r.done) }) }

// Stop closes the browser (flushing the side-car HAR), loads the HAR into the
// session, and returns the finished session. Safe to call after the user
// already closed the window.
func (r *Recorder) Stop() (*Session, error) {
	r.mu.Lock()
	if r.stopped {
		session := r.session
		r.mu.Unlock()
		return session, nil
	}
	r.stopped = true
	r.mu.Unlock()

	// Drain in-flight screenshot goroutines; once they observe r.stopped they
	// no-op, so the finished session is never mutated after we return it.
	r.thumbWG.Wait()

	if r.context != nil {
		// Export storage state before closing so the user can reuse the
		// authenticated session on a later recording.
		if state, stateErr := r.context.StorageState(); stateErr == nil && state != nil {
			if encoded, encErr := json.Marshal(state); encErr == nil {
				r.mu.Lock()
				r.session.StorageState = encoded
				r.mu.Unlock()
			}
		}
		_ = r.context.Close() // flushes RecordHarPath
	}
	r.cleanup()
	r.signalDone()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.session.StoppedAt = time.Now()
	// Close an open pause window so think-time subtraction sees bounded
	// intervals, and auto-close any open transactions (innermost first).
	if n := len(r.session.PauseIntervals); n > 0 && r.session.PauseIntervals[n-1].To.IsZero() {
		r.session.PauseIntervals[n-1].To = r.session.StoppedAt
	}
	for i := len(r.session.OpenTransactions) - 1; i >= 0; i-- {
		name := r.session.OpenTransactions[i]
		r.session.Markers = append(r.session.Markers, Marker{
			Kind:      MarkerTransactionEnd,
			Name:      name,
			Timestamp: r.session.StoppedAt,
			Auto:      true,
		})
		r.session.Warnings = append(r.session.Warnings, fmt.Sprintf(
			"transaction %q was still open and was closed automatically when the recording stopped", name))
	}
	r.session.OpenTransactions = nil
	if r.session.HARPath != "" {
		data, err := os.ReadFile(r.session.HARPath)
		if err != nil {
			r.session.Warnings = append(r.session.Warnings, fmt.Sprintf("side-car HAR unavailable: %v", err))
		} else {
			if !r.opts.HARFilters.empty() {
				filtered, dropped, filterErr := filterHARByHost(data, r.opts.HARFilters)
				if filterErr != nil {
					r.session.Warnings = append(r.session.Warnings, fmt.Sprintf("HAR host filter skipped: %v", filterErr))
				} else {
					data = filtered
					if dropped > 0 {
						r.session.Warnings = append(r.session.Warnings, fmt.Sprintf(
							"%d HAR entr(ies) dropped by host filters", dropped))
					}
				}
			}
			r.session.HAR = data
		}
		if r.harTemp {
			_ = os.Remove(r.session.HARPath)
			r.session.HARPath = ""
		}
	}
	return r.session, nil
}

// SetSection switches the active recording section (init|actions|teardown).
// Subsequent steps are tagged so conversion routes them into the matching DSL
// block.
func (r *Recorder) SetSection(section string) string {
	normalized := normalizeSection(section)
	r.mu.Lock()
	r.currentSection = normalized
	r.mu.Unlock()
	r.emit(Event{Type: EventSection, Section: normalized})
	return normalized
}

// Section reports the active recording section.
func (r *Recorder) Section() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.currentSection
}

// AssertMode reports whether the in-page element picker is active.
func (r *Recorder) AssertMode() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.assertMode
}

// SetAssertMode toggles the in-page element picker (verification mode) across
// every open page and notifies observers.
func (r *Recorder) SetAssertMode(on bool) {
	r.mu.Lock()
	if r.stopped || r.assertMode == on {
		r.mu.Unlock()
		return
	}
	r.assertMode = on
	pages := r.openPagesLocked()
	r.mu.Unlock()
	for _, page := range pages {
		r.pushAssertModeState(page, on)
	}
	r.emit(Event{Type: EventPickState, Picking: on})
}

func (r *Recorder) pushAssertModeState(page playwright.Page, on bool) {
	if page == nil {
		return
	}
	_, _ = page.Evaluate(`(on) => { if (window.__apexloadRecorderSetAssertMode) window.__apexloadRecorderSetAssertMode(on) }`, on)
}

// DeleteLastStep tombstones the most recent non-deleted step.
func (r *Recorder) DeleteLastStep() (*Step, error) {
	r.mu.Lock()
	step, ok := r.session.deleteLastStep()
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no step to delete")
	}
	r.emit(Event{Type: EventStepUpdate, Step: step})
	return step, nil
}

// SetStepExcluded toggles the exclude flag on a step by sequence number.
func (r *Recorder) SetStepExcluded(seq int, excluded bool) (*Step, error) {
	r.mu.Lock()
	step, ok := r.session.setStepExcluded(seq, excluded)
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("step %d not found", seq)
	}
	r.emit(Event{Type: EventStepUpdate, Step: step})
	return step, nil
}

// SetStepMark flags a fill step as a runtime secret or data parameter so
// conversion can feed a parameterization suggestion.
func (r *Recorder) SetStepMark(seq int, secret, param bool) (*Step, error) {
	r.mu.Lock()
	var updated *Step
	for i := range r.session.Steps {
		if r.session.Steps[i].Seq == seq {
			if r.session.Steps[i].Kind != StepFill {
				r.mu.Unlock()
				return nil, fmt.Errorf("step %d is not a fill", seq)
			}
			r.session.Steps[i].MarkSecret = secret
			r.session.Steps[i].MarkParam = param
			s := r.session.Steps[i]
			updated = &s
			break
		}
	}
	r.mu.Unlock()
	if updated == nil {
		return nil, fmt.Errorf("step %d not found", seq)
	}
	r.emit(Event{Type: EventStepUpdate, Step: updated})
	return updated, nil
}

// recordAnnotation appends a console/network annotation tied to the nearest
// step, dropping it while paused.
func (r *Recorder) recordAnnotation(annotation Annotation) {
	r.mu.Lock()
	if r.paused || r.stopped {
		r.mu.Unlock()
		return
	}
	annotation.Timestamp = time.Now()
	annotation.NearStep = len(r.session.Steps)
	r.session.Annotations = append(r.session.Annotations, annotation)
	r.mu.Unlock()
	r.emit(Event{Type: EventAnnotation, Annotation: &annotation})
}

// recordWSFrame records a WebSocket frame: always bumps the count, and stores a
// size-capped, scrubbed payload when opted in.
func (r *Recorder) recordWSFrame(index int, direction string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// In-flight Playwright callbacks can fire after Stop closed the context;
	// don't mutate the (now-final) session afterwards.
	if r.stopped || index < 0 || index >= len(r.session.WebSockets) {
		return
	}
	if direction == "sent" {
		r.session.WebSockets[index].FramesSent++
	} else {
		r.session.WebSockets[index].FramesReceived++
	}
	if !r.opts.CaptureWSPayloads {
		return
	}
	limit := r.opts.WSPayloadLimitBytes
	if limit <= 0 {
		limit = 4096
	}
	frame := WebSocketFrame{Direction: direction, SizeBytes: len(payload), Timestamp: time.Now(), Encoding: "text"}
	body := payload
	if len(body) > limit {
		body = body[:limit]
		frame.Truncated = true
	}
	// Scrub BEFORE choosing the encoding so a frame that fails the strict
	// printable check still has known secret patterns redacted rather than
	// being base64-encoded verbatim.
	text := string(body)
	if r.opts.Scrubber != nil {
		text = r.opts.Scrubber(text)
	}
	if isPrintableUTF8(text) {
		frame.Payload = text
	} else {
		frame.Payload = base64.StdEncoding.EncodeToString([]byte(text))
		frame.Encoding = "base64"
	}
	r.session.WebSockets[index].Frames = append(r.session.WebSockets[index].Frames, frame)
}

// captureThumbnailAsync screenshots the page and attaches a size-capped JPEG
// thumbnail to the step out-of-band so capture is never blocked. Tracked by a
// WaitGroup so Stop can drain in-flight screenshots before the session becomes
// the final read-only artifact (no post-stop mutation of session.Steps).
func (r *Recorder) captureThumbnailAsync(page playwright.Page, seq int) {
	r.thumbWG.Add(1)
	go func() {
		defer r.thumbWG.Done()
		raw, err := page.Screenshot(playwright.PageScreenshotOptions{
			Type:    playwright.ScreenshotTypeJpeg,
			Quality: playwright.Int(45),
		})
		if err != nil || len(raw) == 0 {
			return
		}
		thumb, err := thumbnailDataURL(raw, 200)
		if err != nil {
			return
		}
		r.mu.Lock()
		var updated *Step
		if !r.stopped {
			for i := range r.session.Steps {
				if r.session.Steps[i].Seq == seq {
					r.session.Steps[i].Thumbnail = thumb
					s := r.session.Steps[i]
					updated = &s
					break
				}
			}
		}
		r.mu.Unlock()
		if updated != nil {
			r.emit(Event{Type: EventStepUpdate, Step: updated})
		}
	}()
}

// windowArgs builds Chromium launch args for window positioning/sizing.
func (r *Recorder) windowArgs() []string {
	var args []string
	if r.opts.WindowWidth > 0 && r.opts.WindowHeight > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", r.opts.WindowWidth, r.opts.WindowHeight))
	}
	if r.opts.WindowX != 0 || r.opts.WindowY != 0 {
		args = append(args, fmt.Sprintf("--window-position=%d,%d", r.opts.WindowX, r.opts.WindowY))
	}
	return args
}

func (r *Recorder) cleanup() {
	if r.browser != nil {
		_ = r.browser.Close()
		r.browser = nil
	}
	if r.pw != nil {
		_ = r.pw.Stop()
		r.pw = nil
	}
}
