package browser

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
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
	// managed temporary file that is read into Session.HAR and removed on Stop.
	HARPath string
	// HARArtifactDir overrides the directory for managed temporary HAR files.
	// Empty uses os.TempDir().
	HARArtifactDir string
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
	// Sidebar enables the in-page advanced control sidebar (the expandable,
	// draggable, corner-snapping panel embedded in the recorded browser). When
	// false the page shows only the minimal REC/Pause/Assert toolbar. The API
	// manager defaults this on.
	Sidebar bool
}

// BrowserRecorderBridgeStoragePrefix is the dashboard localStorage key prefix
// used for local recording bridge pairings.
const BrowserRecorderBridgeStoragePrefix = "apexload.browserRecorderBridge."

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
	controlPages   map[playwright.Page]struct{}
	openingControl bool

	// controlNonce is a per-session secret baked into the capture script's
	// closure. Destructive in-page control events (stop/delete/exclude/mark)
	// must echo it; arbitrary recorded-page JavaScript cannot read a closure
	// constant, so it cannot forge them against the live recording.
	controlNonce string

	thumbWG  sync.WaitGroup
	done     chan struct{}
	doneOnce sync.Once
	harTemp  bool

	// sidebarPush coalesces live-state pushes to the in-page sidebar. A single
	// drainer goroutine (started in Start) debounces bursts of events into one
	// page.Evaluate per open page so rapid capture never floods the browser.
	sidebarPush chan struct{}
}

// controlNoncePlaceholder is the literal in capture.js that newControlNonce's
// value is substituted for at injection time, baking the secret into the
// script's closure rather than a page-readable window global.
const controlNoncePlaceholder = "__APEXLOAD_CONTROL_NONCE__"

// newControlNonce returns a per-session secret. Uniqueness is the floor; on the
// (vanishingly unlikely) crypto/rand failure we degrade to a time-derived value
// rather than ship an empty nonce, since an empty nonce would disable the gate.
func newControlNonce() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// trustedControlNonce reports whether an in-page control event carried this
// session's nonce, using a constant-time compare. An empty session nonce never
// trusts (fail closed).
func (r *Recorder) trustedControlNonce(got string) bool {
	if r.controlNonce == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(r.controlNonce)) == 1
}

// controlKindRequiresTrust reports whether a control kind mutates or terminates
// the live recording and therefore must prove it came from the recorder's own
// sidebar (via the closure nonce) rather than arbitrary recorded-page script.
func controlKindRequiresTrust(kind string) bool {
	switch kind {
	// Destructive controls plus state-mutating controls that change how subsequent
	// steps are recorded. The __apexloadRecord binding is exposed context-wide, so
	// arbitrary recorded-page JS could otherwise forge these to corrupt the
	// recording (misclassify steps via section_set, fabricate transaction
	// markers/notes). The legitimate sidebar stamps the closure nonce on all of
	// them, so gating rejects only forged events.
	case "recording_stop", "delete_last_step", "step_exclude", "step_mark",
		"section_set", "transaction_start", "transaction_end", "note_add",
		"pause_toggle", "pick_toggle":
		return true
	default:
		return false
	}
}

// EnsureBrowsers downloads the Playwright driver and Chromium if they are not
// already installed. Idempotent, but the first run can be slow (hundreds of MB).
func EnsureBrowsers() error {
	return playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}})
}

func CheckChromiumInstalled() error {
	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("start playwright: %w", err)
	}
	defer pw.Stop()
	path := strings.TrimSpace(pw.Chromium.ExecutablePath())
	if path == "" {
		return fmt.Errorf("launch chromium: executable doesn't exist; please run: playwright install chromium")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("launch chromium: executable doesn't exist at %s; please run: playwright install chromium: %w", path, err)
	}
	return nil
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
		controlNonce:   newControlNonce(),
		session: &Session{
			ID:            fmt.Sprintf("rec_%d", time.Now().UnixNano()),
			StartURL:      strings.TrimSpace(opts.StartURL),
			StartedAt:     time.Now(),
			DeviceProfile: device,
		},
		done:         make(chan struct{}),
		controlPages: make(map[playwright.Page]struct{}),
		sidebarPush:  make(chan struct{}, 1),
	}
}

func managedHARTempPath(dir string, sessionID string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = os.TempDir()
	}
	safeID := strings.NewReplacer(string(filepath.Separator), "_", "/", "_", "\\", "_").Replace(strings.TrimSpace(sessionID))
	if safeID == "" {
		safeID = fmt.Sprintf("rec_%d", time.Now().UnixNano())
	}
	return filepath.Join(dir, "apexload_browser_"+safeID+".har.tmp")
}

func prepareHARPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if closeErr := file.Close(); closeErr != nil {
		return closeErr
	}
	return os.Chmod(path, 0o600)
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
			harPath = managedHARTempPath(r.opts.HARArtifactDir, r.session.ID)
			r.harTemp = true
		}
		if err := prepareHARPath(harPath); err != nil {
			r.cleanup()
			return fmt.Errorf("prepare side-car HAR path: %w", err)
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
	// The config script runs before the capture script in every frame so the
	// in-page panel reads its initial mode (sidebar enabled, open by default,
	// snapped to the bottom-right corner). User changes persist via localStorage.
	if err := context.AddInitScript(playwright.Script{Content: playwright.String(r.configScript())}); err != nil {
		r.cleanup()
		return fmt.Errorf("inject recorder config: %w", err)
	}
	// Bake this session's control nonce into the capture script's closure so the
	// sidebar can prove destructive control events originate from it.
	injectedCapture := strings.Replace(captureScript, controlNoncePlaceholder, r.controlNonce, 1)
	if err := context.AddInitScript(playwright.Script{Content: playwright.String(injectedCapture)}); err != nil {
		r.cleanup()
		return fmt.Errorf("inject capture script: %w", err)
	}
	context.OnPage(r.registerPage)
	go r.sidebarPushLoop()

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
	// A recorded-site popup (window.open / target=_blank) always has an opener;
	// the control panel (context.NewPage) never does. Requiring opener == nil for
	// the control-page paths stops a popup that fires OnPage during control-panel
	// creation from stealing the openingControl flag — which would permanently
	// exclude that popup from capture and leak the real control tab's events.
	opener, _ := page.Opener()
	isPanelURL := isRecorderControlPanelURL(page.URL())

	r.mu.Lock()
	if opener == nil && (r.openingControl || isPanelURL) {
		r.markControlPageLocked(page)
		r.openingControl = false
		r.mu.Unlock()
		page.OnClose(func(closed playwright.Page) { r.handlePageClose(closed) })
		return
	}
	r.activePage = page
	isPopup := len(r.session.NavEvents) > 0 || len(r.session.Steps) > 0
	paused := r.paused
	var recorded *Step
	switchStepSeq := 0
	if isPopup && !paused {
		step := r.session.appendStep(Step{
			Kind:      StepSwitchWindow,
			Value:     page.URL(),
			Timestamp: time.Now(),
			URL:       page.URL(),
		})
		recorded = &step
		switchStepSeq = step.Seq
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
		var updatedSwitchStep *Step
		r.mu.Lock()
		paused := r.paused
		if !paused {
			r.session.NavEvents = append(r.session.NavEvents, nav)
			if switchStepSeq > 0 {
				updatedSwitchStep, _ = r.session.updateSwitchWindowStep(switchStepSeq, nav.URL)
			}
		}
		r.mu.Unlock()
		if paused {
			// The fresh document's capture script starts unpaused; re-sync so
			// the in-page toolbar and client-side gating match the server, and
			// hydrate the new document's sidebar with current live state.
			r.pushPauseState(page, true)
			r.markSidebarDirty()
			return
		}
		if updatedSwitchStep != nil {
			r.emit(Event{Type: EventStepUpdate, Step: updatedSwitchStep})
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
		r.handlePageClose(closed)
	})
}

func (r *Recorder) handlePageClose(closed playwright.Page) {
	r.mu.Lock()
	delete(r.controlPages, closed)
	if r.activePage == closed || r.activePage == nil || r.isControlPageLocked(r.activePage) {
		r.activePage = nil
		if pages := r.recordablePagesLocked(); len(pages) > 0 {
			r.activePage = pages[len(pages)-1]
		}
	}
	noRecordablePages := len(r.recordablePagesLocked()) == 0
	r.mu.Unlock()
	if noRecordablePages {
		r.signalDone()
	}
}

// handleRecordBinding receives capture-script payloads. The binding source
// frame resolves cross-origin frames the script cannot identify itself.
func (r *Recorder) handleRecordBinding(source *playwright.BindingSource, args ...any) any {
	if len(args) == 0 {
		return nil
	}
	if source != nil && source.Page != nil {
		r.mu.Lock()
		isControlPage := r.isControlPageLocked(source.Page)
		r.mu.Unlock()
		if isControlPage {
			return nil
		}
	}
	payload, ok := args[0].(string)
	if !ok {
		return nil
	}
	// Control events from the in-page toolbar/sidebar round-trip through the
	// server so the panel, recorder state, and every page stay in sync. They are
	// handled before (and regardless of) the pause gate, matching the REST
	// control handlers which stay usable while paused.
	switch kind := controlEventKind(payload); kind {
	case "":
		// Not a control event; fall through to step parsing below.
	default:
		r.handleControlEvent(payload)
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
	var switched *Step
	if source != nil && source.Page != nil {
		switched = r.recordPageSwitchIfNeededLocked(source.Page, step.Timestamp, section)
	}
	recorded := r.session.appendStep(step)
	r.mu.Unlock()
	if switched != nil {
		r.emit(Event{Type: EventStep, Step: switched})
	}
	r.emit(Event{Type: EventStep, Step: &recorded})
	if r.opts.CaptureScreenshots && source != nil && source.Page != nil {
		r.captureThumbnailAsync(source.Page, recorded.Seq)
	}
	return nil
}

func (r *Recorder) recordPageSwitchIfNeededLocked(page playwright.Page, before time.Time, section string) *Step {
	if page == nil || r.isControlPageLocked(page) {
		return nil
	}
	if r.activePage == nil {
		r.activePage = page
		return nil
	}
	if r.activePage == page {
		return nil
	}
	r.activePage = page
	targetURL := page.URL()
	ts := time.Now()
	if !before.IsZero() {
		ts = before.Add(-time.Millisecond)
	}
	step := r.session.appendStep(Step{
		Kind:      StepSwitchWindow,
		Value:     targetURL,
		Timestamp: ts,
		URL:       targetURL,
		Section:   section,
	})
	return &step
}

// emit delivers an event to the configured observer. Never called while
// holding r.mu, so observers may call back into the recorder.
func (r *Recorder) emit(event Event) {
	if r.opts.OnEvent != nil {
		r.opts.OnEvent(event)
	}
	// Every observed event can change what the in-page sidebar shows (counts,
	// recent steps, section, transactions, pause/pick). Flag a coalesced push.
	r.markSidebarDirty()
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
	pages := r.recordablePagesLocked()
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

func (r *Recorder) recordablePagesLocked() []playwright.Page {
	pages := r.openPagesLocked()
	out := make([]playwright.Page, 0, len(pages))
	for _, page := range pages {
		if !r.isControlPageLocked(page) {
			out = append(out, page)
		}
	}
	return out
}

func (r *Recorder) markControlPageLocked(page playwright.Page) {
	if page == nil {
		return
	}
	if r.controlPages == nil {
		r.controlPages = make(map[playwright.Page]struct{})
	}
	r.controlPages[page] = struct{}{}
}

func (r *Recorder) isControlPageLocked(page playwright.Page) bool {
	if page == nil {
		return false
	}
	if _, ok := r.controlPages[page]; ok {
		return true
	}
	return isRecorderControlPanelURL(page.URL())
}

// navigableURL reports whether a navigation target is an absolute http(s) URL.
// It is the single guard shared by the REST navigate handler and the in-page
// control binding: the binding is exposed context-wide, so any recorded page's
// JavaScript could otherwise drive the recording browser to file://, chrome://,
// view-source:// and similar local-resource schemes.
func navigableURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("navigate requires an absolute http(s) URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("navigate scheme %q is not allowed; use http or https", parsed.Scheme)
	}
}

// Navigate drives the active recording page to a URL (control-panel "go to").
// While paused this positions the browser off the record — the navigation
// listener drops the event, which is the point.
func (r *Recorder) Navigate(targetURL string) error {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return fmt.Errorf("navigate requires a URL")
	}
	if err := navigableURL(targetURL); err != nil {
		return err
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

// OpenControlPanel opens the standalone dashboard control panel as a sibling
// tab in the same browser context as the recorded page. The tab is deliberately
// excluded from capture so panel clicks do not become script steps.
func (r *Recorder) OpenControlPanel(targetURL string, localStorage map[string]string) error {
	targetURL = strings.TrimSpace(targetURL)
	if err := ValidateControlPanelURL(targetURL); err != nil {
		return err
	}

	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return fmt.Errorf("recording already stopped")
	}
	context := r.context
	active := r.activePage
	r.openingControl = true
	r.mu.Unlock()
	if context == nil {
		r.mu.Lock()
		r.openingControl = false
		r.mu.Unlock()
		return fmt.Errorf("browser recorder context unavailable")
	}

	page, err := context.NewPage()
	r.mu.Lock()
	r.openingControl = false
	if page != nil {
		r.markControlPageLocked(page)
	}
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("open control panel tab: %w", err)
	}
	if script := controlPanelLocalStorageScript(localStorage); script != "" {
		if err := page.AddInitScript(playwright.Script{Content: playwright.String(script)}); err != nil {
			_ = page.Close()
			return fmt.Errorf("seed control panel bridge connection: %w", err)
		}
	}
	if _, err := page.Goto(targetURL); err != nil {
		_ = page.Close()
		return fmt.Errorf("open control panel %s: %w", targetURL, err)
	}

	r.mu.Lock()
	if active != nil && !r.isControlPageLocked(active) {
		r.activePage = active
	} else if pages := r.recordablePagesLocked(); len(pages) > 0 {
		r.activePage = pages[len(pages)-1]
	}
	r.mu.Unlock()
	return nil
}

func (r *Recorder) currentPage() playwright.Page {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activePage != nil && !r.isControlPageLocked(r.activePage) {
		return r.activePage
	}
	if pages := r.recordablePagesLocked(); len(pages) > 0 {
		return pages[len(pages)-1]
	}
	return nil
}

// AddMarker inserts a transaction boundary or note into the timeline.
func (r *Recorder) AddMarker(kind, name, text string, atStepSeq int) (Marker, error) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return Marker{}, fmt.Errorf("recording already stopped")
	}
	timestamp := time.Now()
	if atStepSeq > 0 {
		var ok bool
		timestamp, ok = r.markerTimestampForStepLocked(atStepSeq)
		if !ok {
			r.mu.Unlock()
			return Marker{}, fmt.Errorf("step #%d not found for marker anchor", atStepSeq)
		}
	}
	marker, err := r.session.AddMarker(Marker{
		Kind:      strings.TrimSpace(kind),
		Name:      strings.TrimSpace(name),
		Text:      strings.TrimSpace(text),
		Timestamp: timestamp,
	})
	r.mu.Unlock()
	if err != nil {
		return Marker{}, err
	}
	r.emit(Event{Type: EventMarker, Marker: &marker})
	return marker, nil
}

func (r *Recorder) markerTimestampForStepLocked(seq int) (time.Time, bool) {
	for _, step := range r.session.Steps {
		if step.Seq == seq && !step.Timestamp.IsZero() {
			return step.Timestamp, true
		}
	}
	return time.Time{}, false
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
	case "pause_toggle", "pick_toggle",
		"section_set", "transaction_start", "transaction_end", "note_add",
		"navigate", "delete_last_step", "step_exclude", "step_mark",
		"recording_stop":
		return probe.Kind
	}
	return ""
}

// controlEvent carries the optional fields the in-page sidebar's advanced
// actions send alongside their kind (transaction/note/section/navigate/step
// edits). Field names match the capture script's payload keys.
type controlEvent struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Text     string `json:"text"`
	URL      string `json:"url"`
	Section  string `json:"section"`
	Seq      int    `json:"seq"`
	Secret   bool   `json:"secret"`
	Param    bool   `json:"param"`
	Excluded bool   `json:"excluded"`
	Nonce    string `json:"nonce"`
}

// handleControlEvent dispatches an advanced-sidebar action to the matching
// recorder method. Each method already emits its own observer event (marker/
// section/step_update), which in turn re-pushes live state to every page.
// Errors surface as warning events without polluting the recorded artifact.
func (r *Recorder) handleControlEvent(payload string) {
	var ev controlEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return
	}
	// Destructive kinds must carry this session's closure nonce. A recorded page
	// can reach the context-wide binding but cannot read the nonce, so a forged
	// stop/delete/exclude/mark is dropped (with a transient warning) here.
	if controlKindRequiresTrust(ev.Kind) && !r.trustedControlNonce(ev.Nonce) {
		r.emit(Event{Type: EventWarning, Warning: fmt.Sprintf("recorder control %q rejected: untrusted origin", ev.Kind)})
		return
	}
	var err error
	switch ev.Kind {
	case "pause_toggle":
		r.mu.Lock()
		paused := r.paused
		r.mu.Unlock()
		r.setPaused(!paused)
	case "pick_toggle":
		r.mu.Lock()
		assert := r.assertMode
		r.mu.Unlock()
		r.SetAssertMode(!assert)
	case "section_set":
		r.SetSection(ev.Section)
	case "transaction_start":
		_, err = r.AddMarker(MarkerTransactionStart, ev.Name, "", 0)
	case "transaction_end":
		_, err = r.AddMarker(MarkerTransactionEnd, ev.Name, "", 0)
	case "note_add":
		_, err = r.AddMarker(MarkerNote, "", ev.Text, 0)
	case "navigate":
		// Reject non-http(s) schemes synchronously (with a warning) before
		// spawning: the binding is reachable by any recorded page's JS, so this
		// is the trust boundary that stops file:///chrome:// navigation.
		if verr := navigableURL(ev.URL); verr != nil {
			err = verr
			break
		}
		// Navigate blocks on page.Goto; run it off the binding callback so the
		// Playwright event loop is never stalled.
		go func(target string) { _ = r.Navigate(target) }(ev.URL)
	case "delete_last_step":
		_, err = r.DeleteLastStep()
	case "step_exclude":
		_, err = r.SetStepExcluded(ev.Seq, ev.Excluded)
	case "step_mark":
		_, err = r.SetStepMark(ev.Seq, ev.Secret, ev.Param)
	case "recording_stop":
		r.emit(Event{Type: EventStopRequested})
	}
	if err != nil {
		r.emit(Event{Type: EventWarning, Warning: fmt.Sprintf("recorder control %q: %v", ev.Kind, err)})
	}
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

	r.mu.Lock()
	context := r.context
	r.mu.Unlock()
	if context != nil {
		// Export storage state before closing so the user can reuse the
		// authenticated session on a later recording.
		if state, stateErr := context.StorageState(); stateErr == nil && state != nil {
			if encoded, encErr := json.Marshal(state); encErr == nil {
				r.mu.Lock()
				r.session.StorageState = encoded
				r.mu.Unlock()
			}
		}
		_ = context.Close() // flushes RecordHarPath
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

// ForceStop is the best-effort teardown path used when graceful Stop is stuck.
// It intentionally skips storage-state/HAR flushing, because the caller keeps a
// snapshot artifact and may still recover a late Stop result if it completes.
func (r *Recorder) ForceStop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.signalDone()
	r.cleanup()
	r.discardManagedHARTemp()
}

func (r *Recorder) discardManagedHARTemp() {
	r.mu.Lock()
	if !r.harTemp || r.session == nil || strings.TrimSpace(r.session.HARPath) == "" {
		r.mu.Unlock()
		return
	}
	path := r.session.HARPath
	r.session.HARPath = ""
	r.mu.Unlock()
	_ = os.Remove(path)
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
	pages := r.recordablePagesLocked()
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

// configScript builds the init script that seeds the in-page panel's initial
// mode before the capture script runs.
func (r *Recorder) configScript() string {
	config := map[string]any{
		"sidebar": r.opts.Sidebar,
		"open":    true,
		"corner":  "br",
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		encoded = []byte("{}")
	}
	return "window.__apexloadRecorderConfig = " + string(encoded) + ";"
}

// ValidateControlPanelURL checks that a browser-recorder companion tab points
// at the standalone recorder panel route served by the dashboard.
func ValidateControlPanelURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("control panel url must be an absolute http(s) URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("control panel url scheme %q is not allowed; use http or https", parsed.Scheme)
	}
	if !isRecorderControlPanelURL(raw) {
		return fmt.Errorf("control panel url must use the #/recorder-panel route")
	}
	return nil
}

func isRecorderControlPanelURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	fragment := strings.TrimPrefix(parsed.Fragment, "/")
	return fragment == "recorder-panel" ||
		strings.HasPrefix(fragment, "recorder-panel?") ||
		strings.HasPrefix(fragment, "recorder-panel/")
}

func controlPanelLocalStorageScript(entries map[string]string) string {
	if len(entries) == 0 {
		return ""
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	return `(() => {
  const entries = ` + string(encoded) + `;
  try {
    for (const [key, value] of Object.entries(entries)) {
      window.localStorage.setItem(key, value);
    }
    window.dispatchEvent(new CustomEvent('apexload:browser-recorder-bridge-changed'));
  } catch (e) {}
})();`
}

// sidebarRecentLimit caps how many recent steps the in-page sidebar shows.
const sidebarRecentLimit = 8

// sidebarStateView is the live snapshot pushed into the recorded page so the
// in-page sidebar renders counts, the active section, open transactions, and a
// scrolling tail of recent steps without calling back into the controller REST
// API (which it cannot reach cross-origin).
type sidebarStateView struct {
	Section          string            `json:"section"`
	StepCount        int               `json:"stepCount"`
	NavCount         int               `json:"navCount"`
	Paused           bool              `json:"paused"`
	Picking          bool              `json:"picking"`
	OpenTransactions []string          `json:"openTransactions"`
	RecentSteps      []sidebarStepView `json:"recentSteps"`
}

// sidebarStepView is one entry in the sidebar's recent-steps tail.
type sidebarStepView struct {
	Seq      int    `json:"seq"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
	Excluded bool   `json:"excluded"`
	Masked   bool   `json:"masked"`
	Secret   bool   `json:"secret"`
	Param    bool   `json:"param"`
	Fill     bool   `json:"fill"`
}

// buildSidebarStateLocked assembles the live sidebar snapshot. Callers hold r.mu.
func (r *Recorder) buildSidebarStateLocked() sidebarStateView {
	view := sidebarStateView{
		Section:          r.currentSection,
		NavCount:         len(r.session.NavEvents),
		Paused:           r.paused,
		Picking:          r.assertMode,
		OpenTransactions: append([]string{}, r.session.OpenTransactions...),
		RecentSteps:      []sidebarStepView{},
	}
	for _, step := range r.session.Steps {
		if step.Deleted {
			continue
		}
		if !step.Excluded {
			view.StepCount++
		}
		view.RecentSteps = append(view.RecentSteps, sidebarStepView{
			Seq:      step.Seq,
			Kind:     step.Kind,
			Detail:   sidebarStepDetail(step),
			Excluded: step.Excluded,
			Masked:   step.Masked,
			Secret:   step.MarkSecret,
			Param:    step.MarkParam,
			Fill:     step.Kind == StepFill,
		})
	}
	if len(view.RecentSteps) > sidebarRecentLimit {
		view.RecentSteps = view.RecentSteps[len(view.RecentSteps)-sidebarRecentLimit:]
	}
	return view
}

// buildSidebarStateJSON marshals the current sidebar snapshot for page push.
func (r *Recorder) buildSidebarStateJSON() string {
	r.mu.Lock()
	view := r.buildSidebarStateLocked()
	r.mu.Unlock()
	encoded, err := json.Marshal(view)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// sidebarStepDetail produces a short, human-readable description of a step for
// the in-page recent-steps tail (masked fills never leak their value).
func sidebarStepDetail(step Step) string {
	switch step.Kind {
	case StepNavigate, StepSwitchWindow:
		return step.URL
	case StepFill:
		if step.Masked {
			return "••••"
		}
		return step.Value
	case StepSelect:
		if len(step.Values) > 1 {
			return strings.Join(step.Values, ", ")
		}
		return step.Value
	case StepScroll:
		return fmt.Sprintf("dx=%d dy=%d", step.DeltaX, step.DeltaY)
	case StepSetFiles, StepPressKey:
		return step.Value
	case StepHandleDialog:
		return step.DialogMessage
	default:
		if step.Element != nil {
			if text := strings.TrimSpace(step.Element.Text); text != "" {
				return text
			}
			if step.Element.CSSPath != "" {
				return step.Element.CSSPath
			}
		}
		return step.Value
	}
}

// markSidebarDirty requests a coalesced live-state push to the sidebar. The
// buffered channel collapses bursts into a single drained signal; a nil channel
// (recorder built without New, in some tests) simply no-ops via the default.
func (r *Recorder) markSidebarDirty() {
	select {
	case r.sidebarPush <- struct{}{}:
	default:
	}
}

// sidebarPushLoop debounces dirty signals and pushes the freshest live state to
// every open page. It exits when the recording is done.
func (r *Recorder) sidebarPushLoop() {
	for {
		select {
		case <-r.done:
			return
		case <-r.sidebarPush:
			select {
			case <-r.done:
				return
			case <-time.After(80 * time.Millisecond):
			}
			r.broadcastSidebarState()
		}
	}
}

// broadcastSidebarState pushes the current snapshot into every open page.
func (r *Recorder) broadcastSidebarState() {
	state := r.buildSidebarStateJSON()
	r.mu.Lock()
	pages := r.recordablePagesLocked()
	r.mu.Unlock()
	for _, page := range pages {
		r.pushSidebarState(page, state)
	}
}

// pushSidebarState delivers one live-state snapshot to a single page's sidebar.
func (r *Recorder) pushSidebarState(page playwright.Page, state string) {
	if page == nil {
		return
	}
	_, _ = page.Evaluate(`(s) => { if (window.__apexloadRecorderSetState) window.__apexloadRecorderSetState(s) }`, state)
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
	r.mu.Lock()
	r.context = nil
	browser := r.browser
	r.browser = nil
	pw := r.pw
	r.pw = nil
	r.mu.Unlock()
	if browser != nil {
		_ = browser.Close()
	}
	if pw != nil {
		_ = pw.Stop()
	}
}
