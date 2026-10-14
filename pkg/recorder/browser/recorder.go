package browser

import (
	"archive/zip"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/internal/pwshutdown"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
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
	// CaptureHAR records the context's network traffic to a side-car HAR flushed
	// when the recording stops. Response bodies are attached rather than embedded
	// so Chromium does not retain unbounded base64-expanded content in memory.
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
	// DisableQUIC launches the recorded Chromium with --disable-quic, so the
	// recording captures no HTTP/3 traffic.
	DisableQUIC bool
	// Scrubber redacts WebSocket frame payloads before storage. Injected by the
	// API layer (which owns pkg/scrub); nil disables payload redaction.
	Scrubber func(string) string
	// Sidebar enables the in-page advanced control sidebar (the panel behind
	// the in-page bar's expand button). When false the page shows only the
	// minimal REC/Pause/Assert bar. The API manager defaults this on.
	Sidebar bool
}

// BrowserRecorderBridgeStoragePrefix is the dashboard localStorage key prefix
// used for local recording bridge pairings.
const BrowserRecorderBridgeStoragePrefix = "apexload.browserRecorderBridge."

// Recorder drives one headed Chromium session and accumulates a Session.
type Recorder struct {
	opts           Options
	pw             *playwright.Playwright
	browser        playwright.Browser
	context        playwright.BrowserContext
	controlContext playwright.BrowserContext

	mu             sync.Mutex
	stopMu         sync.Mutex
	session        *Session
	stopped        bool
	paused         bool
	assertMode     bool
	currentSection string
	// sidebarLayout is where the in-page bar sits and whether it is expanded or
	// hidden. It lives in the recording, not in the recorded site's storage, so
	// it follows the user across origins and stays out of exported state.
	sidebarLayout sidebarLayoutView
	// frameStateSeq orders pause and pick pushes. It is taken under mu with
	// the state it carries, so a late evaluation in a slow iframe can never
	// overwrite newer state there.
	frameStateSeq  uint64
	activePage     playwright.Page
	controlPages   map[playwright.Page]struct{}
	openingControl bool

	// controlNonce is a per-session secret baked into the capture script's
	// closure. Destructive in-page control events (stop/delete/exclude/mark)
	// must echo it; arbitrary recorded-page JavaScript cannot read a closure
	// constant, so it cannot forge them against the live recording.
	controlNonce string

	thumbWG              sync.WaitGroup
	done                 chan struct{}
	doneOnce             sync.Once
	harTemp              bool
	harCapturePath       string
	harLimitExceeded     bool
	wsFrames             int
	wsBytes              int
	bindingBytes         int64
	thumbnailSlots       chan struct{}
	thumbnailBytes       int64
	thumbnailLimitWarned bool
	nativeMetadataBytes  int64
	warningBytes         int64
	navigationSlots      chan struct{}
	controlPanelSlots    chan struct{}

	// sidebarPush coalesces live-state pushes to the in-page sidebar. A single
	// drainer goroutine (started in Start) debounces bursts of events into one
	// page.Evaluate per open page so rapid capture never floods the browser.
	sidebarPush chan struct{}
}

// maxBrowserHARCaptureBytes bounds Playwright's raw HAR capture on disk. It
// is larger than maxBrowserHARBytes, the HAR a recording keeps: the capture
// still holds excluded hosts' bodies, which filtering removes, and hydration
// reads only the bodies that fit the kept HAR.
const maxBrowserHARCaptureBytes = 4 * maxBrowserHARBytes

const (
	maxBrowserHARBytes          = 100 << 20
	maxBrowserPages             = 32
	maxBrowserControlPages      = 4
	maxBrowserSteps             = 100_000
	maxBrowserNetworkEvents     = 100_000
	maxBrowserWebSockets        = 1_000
	maxBrowserWSFrames          = 10_000
	maxBrowserAnnotations       = 10_000
	maxBrowserAnnotationLen     = 4_096
	maxBrowserBindingBytes      = 64 << 10
	maxBrowserBindingTotalBytes = 64 << 20
	maxBrowserWarnings          = 10_000
	maxBrowserWSPayload         = 64 << 10
	maxBrowserWSBytes           = 64 << 20
	maxBrowserThumbnailWorkers  = 4
	maxBrowserThumbnailBytes    = 64 << 20
	maxBrowserThumbnailRawBytes = 8 << 20
	maxBrowserNativeMetadata    = 32 << 20
	maxBrowserWarningBytes      = 4 << 20
	maxBrowserNativeURLBytes    = 16 << 10
	maxBrowserNativeTitleBytes  = 4 << 10
	maxBrowserNativeTextBytes   = 16 << 10
	maxBrowserNativeKindBytes   = 128
	maxBrowserPauseIntervals    = 10_000
	maxBrowserViewportDimension = 8192
	maxBrowserViewportPixels    = 16 << 20
	maxBrowserDevicePixels      = 32 << 20
	maxBrowserDeviceScale       = 4
	maxBrowserStopDuration      = 10 * time.Second
	maxBrowserOperationDuration = 2 * time.Second
	// minBrowserCloseGrace is the least time a browser gets to close before
	// its Playwright driver is stopped, whatever is left of a deadline.
	minBrowserCloseGrace       = time.Second
	maxBrowserHARFlushDuration = 3 * time.Second
	// Playwright stores one attachment per distinct body, so a long
	// recording holds tens of thousands; the central directory bound keeps
	// opening the archive cheap.
	maxAttachedHARZipEntries   = 32_768
	maxAttachedHARZipNameBytes = 1 << 10
	maxAttachedHARCentralBytes = 8 << 20
)

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

func (r *Recorder) appendWarningLocked(warning string) {
	if len(r.session.Warnings) >= maxBrowserWarnings || r.warningBytes >= maxBrowserWarningBytes {
		return
	}
	remaining := int64(maxBrowserWarningBytes) - r.warningBytes
	warning = boundedtext.TruncateUTF8(warning, min(maxBrowserNativeTextBytes, int(remaining)))
	if warning == "" {
		return
	}
	r.session.Warnings = append(r.session.Warnings, warning)
	r.warningBytes += int64(len(warning))
}

func (r *Recorder) recordCapHitLocked(name, warning string) string {
	if r.session.CapHits == nil {
		r.session.CapHits = make(map[string]int64)
	}
	r.session.CapHits[name]++
	if r.session.CapHits[name] != 1 {
		return ""
	}
	r.appendWarningLocked(warning)
	return warning
}

func (r *Recorder) recordCapHitEventLocked(name, warning string) (string, Event) {
	retainedWarning := r.recordCapHitLocked(name, warning)
	return retainedWarning, Event{Type: EventCapHit, CapName: name, CapCount: r.session.CapHits[name]}
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
		"pause_toggle", "pick_toggle", "sidebar_layout":
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
	if opts.Device != nil {
		device := *opts.Device
		opts.Device = &device
	}
	device := ""
	if opts.Device != nil {
		device = boundedtext.TruncateUTF8(opts.Device.Name, maxBrowserNativeTitleBytes)
	}
	return &Recorder{
		opts:           opts,
		currentSection: normalizeSection(opts.Section),
		sidebarLayout:  defaultSidebarLayout(),
		controlNonce:   newControlNonce(),
		session: &Session{
			ID:            fmt.Sprintf("rec_%d", time.Now().UnixNano()),
			StartURL:      strings.TrimSpace(opts.StartURL),
			StartedAt:     time.Now(),
			DeviceProfile: device,
		},
		done:              make(chan struct{}),
		controlPages:      make(map[playwright.Page]struct{}),
		sidebarPush:       make(chan struct{}, 1),
		navigationSlots:   make(chan struct{}, 1),
		controlPanelSlots: make(chan struct{}, 1),
	}
}

func (r *Recorder) retainNativeStringLocked(value string, perValueLimit int) string {
	if perValueLimit <= 0 || r.nativeMetadataBytes >= maxBrowserNativeMetadata {
		return ""
	}
	remaining := int64(maxBrowserNativeMetadata) - r.nativeMetadataBytes
	value = boundedtext.TruncateUTF8(value, min(perValueLimit, int(remaining)))
	if value == "" {
		return ""
	}
	r.nativeMetadataBytes += int64(len(value))
	return value
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
	return filepath.Join(dir, "apexload_browser_"+safeID+".har.zip")
}

func firstNonEmptyRecorderPath(primary string, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
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
	if err := validateRecorderOptions(r.opts, startURL); err != nil {
		return err
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
	if err := recordingTestLaunchOptions(&launchOptions); err != nil {
		r.cleanup()
		return err
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
		requestedPath := strings.TrimSpace(r.opts.HARPath)
		capturePath := requestedPath
		if requestedPath == "" {
			capturePath = managedHARTempPath(r.opts.HARArtifactDir, r.session.ID)
			r.harTemp = true
		} else if !strings.EqualFold(filepath.Ext(requestedPath), ".zip") {
			capturePath = requestedPath + ".attachments.zip.tmp"
		}
		if err := prepareHARPath(capturePath); err != nil {
			r.cleanup()
			return fmt.Errorf("prepare side-car HAR path: %w", err)
		}
		r.harCapturePath = capturePath
		r.session.HARPath = firstNonEmptyRecorderPath(requestedPath, capturePath)
		contextOptions.RecordHarPath = playwright.String(capturePath)
		contextOptions.RecordHarContent = playwright.HarContentPolicyAttach
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
		state, stateErr := loadRecorderStorageState(path)
		if stateErr != nil {
			r.cleanup()
			return fmt.Errorf("storage state %q unavailable: %w", path, stateErr)
		}
		contextOptions.StorageState = state
	}
	observedEnvironment := recordingEnvironmentForContext(r.opts.Device, contextOptions)
	context, err := browser.NewContext(contextOptions)
	if err != nil {
		r.cleanup()
		return fmt.Errorf("create browser context: %w", err)
	}
	r.context = context
	r.mu.Lock()
	r.session.RecordingEnvironment = observedEnvironment.Clone()
	r.mu.Unlock()
	if r.opts.CaptureHAR {
		go r.monitorHARSize(r.harCapturePath, context)
	}

	if err := context.ExposeBinding("__apexloadRecord", r.handleRecordBinding); err != nil {
		r.cleanup()
		return fmt.Errorf("expose recorder binding: %w", err)
	}
	// The config script runs before the capture script in every frame so the
	// in-page bar reads its initial mode (sidebar enabled, collapsed at the
	// bottom-right). Moves, hides and expands persist in the recording's live
	// state (sidebarLayout), never in the recorded site's storage.
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

func validateRecorderOptions(opts Options, startURL string) error {
	if strings.TrimSpace(startURL) == "" {
		return fmt.Errorf("browser recorder requires a start URL")
	}
	if len(startURL) > maxBrowserNativeURLBytes {
		return fmt.Errorf("browser recorder start URL exceeds %d bytes", maxBrowserNativeURLBytes)
	}
	if err := navigableURL(startURL); err != nil {
		return fmt.Errorf("browser recorder start URL: %w", err)
	}
	if device := opts.Device; device != nil {
		if (device.Width <= 0) != (device.Height <= 0) {
			return fmt.Errorf("browser recorder device width and height must be configured together")
		}
		if device.Width > maxBrowserViewportDimension || device.Height > maxBrowserViewportDimension || (device.Width > 0 && int64(device.Width)*int64(device.Height) > maxBrowserViewportPixels) {
			return fmt.Errorf("browser recorder device viewport exceeds capture limits")
		}
		scale := device.DeviceScaleFactor
		if math.IsNaN(scale) || math.IsInf(scale, 0) || scale < 0 || scale > maxBrowserDeviceScale {
			return fmt.Errorf("browser recorder device scale factor must be between 0 and %d", maxBrowserDeviceScale)
		}
		if scale > 0 && float64(device.Width)*float64(device.Height)*scale*scale > maxBrowserDevicePixels {
			return fmt.Errorf("browser recorder scaled device viewport exceeds capture limits")
		}
	}
	if (opts.WindowWidth <= 0) != (opts.WindowHeight <= 0) || opts.WindowWidth > maxBrowserViewportDimension || opts.WindowHeight > maxBrowserViewportDimension {
		return fmt.Errorf("browser recorder window width and height must be configured together and not exceed %d", maxBrowserViewportDimension)
	}
	for label, patterns := range map[string][]string{"include": opts.HARFilters.IncludeHosts, "exclude": opts.HARFilters.ExcludeHosts} {
		if len(patterns) > 128 {
			return fmt.Errorf("browser recorder HAR %s filter exceeds 128 entries", label)
		}
		for _, pattern := range patterns {
			if len(pattern) > 1024 {
				return fmt.Errorf("browser recorder HAR %s filter entry exceeds 1024 bytes", label)
			}
		}
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
	if r.stopped {
		r.mu.Unlock()
		_ = page.Close()
		return
	}
	if len(r.recordablePagesLocked()) >= maxBrowserPages {
		warning, capEvent := r.recordCapHitEventLocked("pages", "browser page limit reached; excess popup was closed")
		r.mu.Unlock()
		r.emit(capEvent)
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		_ = page.Close()
		return
	}
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
	var capEvent *Event
	warning := ""
	switchStepSeq := 0
	if isPopup && !paused && len(r.session.Steps) < maxBrowserSteps {
		pageURL := r.retainNativeStringLocked(page.URL(), maxBrowserNativeURLBytes)
		step := r.session.appendStep(Step{
			Kind:      StepSwitchWindow,
			Value:     pageURL,
			Timestamp: time.Now(),
			URL:       pageURL,
		})
		recorded = &step
		switchStepSeq = step.Seq
	} else if isPopup && !paused {
		var event Event
		warning, event = r.recordCapHitEventLocked("steps", "recording step limit reached; subsequent capture events were dropped")
		capEvent = &event
	}
	r.mu.Unlock()
	if recorded != nil {
		r.emit(Event{Type: EventStep, Step: recorded})
	}
	if capEvent != nil {
		r.emit(*capEvent)
	}
	if warning != "" {
		r.emit(Event{Type: EventWarning, Warning: warning})
	}

	page.OnFrameNavigated(func(frame playwright.Frame) {
		if frame != page.MainFrame() {
			r.mu.Lock()
			stopped, paused, picking := r.stopped, r.paused, r.assertMode
			seq := r.nextFrameStateSeqLocked()
			r.mu.Unlock()
			if !stopped {
				// Off the event callback: evaluating waits on the browser.
				go syncFrameState(frame, paused, picking, seq)
			}
			return
		}
		rawURL := frame.URL()
		nav := NavEvent{Timestamp: time.Now()}
		var updatedSwitchStep *Step
		var capEvent *Event
		warning := ""
		retained := false
		r.mu.Lock()
		paused := r.paused || r.stopped
		var pauseSeq uint64
		if paused {
			pauseSeq = r.nextFrameStateSeqLocked()
		}
		if !paused {
			if len(r.session.NavEvents) < maxBrowserNetworkEvents {
				nav.URL = r.retainNativeStringLocked(rawURL, maxBrowserNativeURLBytes)
				r.session.NavEvents = append(r.session.NavEvents, nav)
				retained = true
				if switchStepSeq > 0 {
					updatedSwitchStep, _ = r.session.updateSwitchWindowStep(switchStepSeq, nav.URL)
				}
			} else {
				var event Event
				warning, event = r.recordCapHitEventLocked("navigation_events", "recording navigation-event limit reached; subsequent navigations were dropped")
				capEvent = &event
			}
		}
		r.mu.Unlock()
		if paused {
			// The fresh document's capture script starts unpaused; re-sync so
			// the in-page toolbar and client-side gating match the server, and
			// hydrate the new document's sidebar with current live state.
			r.pushPauseState(page, true, pauseSeq)
			r.markSidebarDirty()
			return
		}
		if capEvent != nil {
			r.emit(*capEvent)
		}
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		if !retained {
			return
		}
		if updatedSwitchStep != nil {
			r.emit(Event{Type: EventStepUpdate, Step: updatedSwitchStep})
		}
		r.emit(Event{Type: EventNavigation, Nav: &nav})
	})
	page.OnRequest(func(playwright.Request) {
		r.mu.Lock()
		retained := false
		warning := ""
		var capEvent *Event
		if !r.paused && !r.stopped && len(r.session.NetworkEvents) < maxBrowserNetworkEvents {
			r.session.NetworkEvents = append(r.session.NetworkEvents, time.Now())
			retained = true
		} else if !r.paused && !r.stopped {
			var event Event
			warning, event = r.recordCapHitEventLocked("network_events", "recording network-event limit reached; subsequent request events were dropped")
			capEvent = &event
		}
		r.mu.Unlock()
		if retained {
			r.emit(Event{Type: EventNetworkRequest})
		}
		if capEvent != nil {
			r.emit(*capEvent)
		}
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
	})
	page.OnWebSocket(func(ws playwright.WebSocket) {
		r.mu.Lock()
		if r.paused || r.stopped {
			r.mu.Unlock()
			return
		}
		if len(r.session.WebSockets) >= maxBrowserWebSockets {
			warning, capEvent := r.recordCapHitEventLocked("websockets", "recording WebSocket limit reached; subsequent sockets were dropped")
			r.mu.Unlock()
			r.emit(capEvent)
			if warning != "" {
				r.emit(Event{Type: EventWarning, Warning: warning})
			}
			return
		}
		wsURL := r.retainNativeStringLocked(ws.URL(), maxBrowserNativeURLBytes)
		r.session.WebSockets = append(r.session.WebSockets, WebSocketSummary{URL: wsURL, OpenedAt: time.Now()})
		index := len(r.session.WebSockets) - 1
		r.mu.Unlock()
		r.emit(Event{Type: EventWebSocket})
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
		paused := r.paused || r.stopped
		var recorded *Step
		var capEvent *Event
		warning := ""
		if !paused && len(r.session.Steps) < maxBrowserSteps {
			message = r.retainNativeStringLocked(message, maxBrowserNativeTextBytes)
			dialogType = r.retainNativeStringLocked(dialogType, maxBrowserNativeKindBytes)
			pageURL := r.retainNativeStringLocked(page.URL(), maxBrowserNativeURLBytes)
			step := r.session.appendStep(Step{
				Kind:          StepHandleDialog,
				DialogType:    dialogType,
				DialogMessage: message,
				Section:       r.currentSection,
				Timestamp:     time.Now(),
				URL:           pageURL,
			})
			recorded = &step
		} else if !paused {
			var event Event
			warning, event = r.recordCapHitEventLocked("steps", "recording step limit reached; subsequent capture events were dropped")
			capEvent = &event
		}
		r.mu.Unlock()
		if recorded != nil {
			r.emit(Event{Type: EventStep, Step: recorded})
		}
		if capEvent != nil {
			r.emit(*capEvent)
		}
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
	})
	page.OnConsole(func(msg playwright.ConsoleMessage) {
		if msg.Type() != "error" {
			return
		}
		r.recordAnnotation(Annotation{Kind: "console_error", Text: strings.TrimSpace(msg.Text()), URL: consoleMessageSource(msg, page.URL())})
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

// consoleMessageSource is the document or script that logged a console
// message, so an error a third-party frame raised (an ad or video player
// iframe) is listed under that frame's host, not the recorded page's. A
// message with no web source (about:blank, the browser itself) stays with
// the page.
func consoleMessageSource(msg playwright.ConsoleMessage, pageURL string) string {
	if location := msg.Location(); location != nil {
		if parsed, err := url.Parse(location.URL); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
			return location.URL
		}
	}
	return pageURL
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
	if !ok || len(payload) > maxBrowserBindingBytes {
		return nil
	}
	// ExposeBinding is reachable by every script in the recorded page. Only the
	// init script's closure knows the per-session nonce; reject direct page calls
	// before they can fabricate steps or consume the session byte budget. Direct
	// nil-source calls are reserved for package-level tests/internal helpers—the
	// Playwright binding always supplies a source.
	if source != nil && !r.trustedCapturePayload(payload) {
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
	if r.paused || r.stopped {
		r.mu.Unlock()
		return nil
	}
	if len(r.session.Steps) >= maxBrowserSteps {
		warning, capEvent := r.recordCapHitEventLocked("steps", "recording step limit reached; subsequent capture events were dropped")
		r.mu.Unlock()
		r.emit(capEvent)
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		return nil
	}
	if r.bindingBytes+int64(len(payload)) > maxBrowserBindingTotalBytes {
		warning, capEvent := r.recordCapHitEventLocked("binding_bytes", "recording capture byte limit reached; subsequent events were dropped")
		r.mu.Unlock()
		r.emit(capEvent)
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		return nil
	}
	r.bindingBytes += int64(len(payload))
	section := r.currentSection
	r.mu.Unlock()
	steps, err := stepsFromEvent(payload)
	if err != nil {
		warning := fmt.Sprintf("dropped capture event: %v", err)
		r.mu.Lock()
		r.appendWarningLocked(warning)
		r.mu.Unlock()
		r.emit(Event{Type: EventWarning, Warning: warning})
		return nil
	}
	for index := range steps {
		if steps[index].FrameSelector == "" && source != nil && source.Frame != nil && source.Page != nil {
			if source.Frame != source.Page.MainFrame() {
				steps[index].FrameSelector = frameSelectorFromURL(source.Frame.URL())
			}
		}
		steps[index].Section = section
	}
	r.mu.Lock()
	if r.paused || r.stopped {
		r.mu.Unlock()
		return nil
	}
	if len(steps) > maxBrowserSteps-len(r.session.Steps) {
		warning, capEvent := r.recordCapHitEventLocked("steps", "recording step limit reached; subsequent capture events were dropped")
		r.mu.Unlock()
		r.emit(capEvent)
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		return nil
	}
	var switched *Step
	if source != nil && source.Page != nil && len(steps) > 0 {
		switched = r.recordPageSwitchIfNeededLocked(source.Page, steps[0].Timestamp, section, len(steps))
	}
	recorded := make([]Step, 0, len(steps))
	for _, step := range steps {
		recorded = append(recorded, r.session.appendStep(step))
	}
	r.mu.Unlock()
	if switched != nil {
		r.emit(Event{Type: EventStep, Step: switched})
	}
	for index := range recorded {
		// Picking is one-shot in capture.js. Consume the authoritative state
		// too, otherwise the next sidebar push re-enables it in every page.
		if recorded[index].Kind == StepAssert {
			r.SetAssertMode(false)
		}
		r.emit(Event{Type: EventStep, Step: &recorded[index]})
		if r.opts.CaptureScreenshots && source != nil && source.Page != nil {
			r.captureThumbnailAsync(source.Page, recorded[index].Seq)
		}
	}
	return nil
}

func (r *Recorder) trustedCapturePayload(payload string) bool {
	var envelope struct {
		Kind  string `json:"kind"`
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil || strings.TrimSpace(envelope.Kind) == "" {
		return false
	}
	return r.trustedControlNonce(envelope.Nonce)
}

func (r *Recorder) recordPageSwitchIfNeededLocked(page playwright.Page, before time.Time, section string, incomingSteps int) *Step {
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
	// Reserve every slot in the atomic event batch that triggered this page
	// switch. Near the ceiling, keep the user events and omit the synthetic
	// switch rather than allowing the batch to exceed maxBrowserSteps.
	if incomingSteps < 1 {
		incomingSteps = 1
	}
	if incomingSteps >= maxBrowserSteps || len(r.session.Steps) > maxBrowserSteps-incomingSteps-1 {
		return nil
	}
	targetURL := r.retainNativeStringLocked(page.URL(), maxBrowserNativeURLBytes)
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
	if paused {
		r.flushPendingCapture(time.Now().Add(2 * time.Second))
	}
	r.mu.Lock()
	if r.stopped || r.paused == paused {
		r.mu.Unlock()
		return
	}
	r.paused = paused
	now := time.Now()
	if paused {
		if len(r.session.PauseIntervals) < maxBrowserPauseIntervals {
			r.session.PauseIntervals = append(r.session.PauseIntervals, PauseInterval{From: now})
		}
	} else if n := len(r.session.PauseIntervals); n > 0 && r.session.PauseIntervals[n-1].To.IsZero() {
		r.session.PauseIntervals[n-1].To = now
	}
	pages := r.recordablePagesLocked()
	seq := r.nextFrameStateSeqLocked()
	r.mu.Unlock()

	for _, page := range pages {
		r.pushPauseState(page, paused, seq)
	}
	r.emit(Event{Type: EventPauseState, Paused: paused})
}

// pushPauseState syncs the in-page toolbar and client-side gating with the
// server's authoritative pause flag. Best effort: the server gate drops
// events regardless.
func (r *Recorder) pushPauseState(page playwright.Page, paused bool, seq uint64) {
	pushFrameState(page, `([paused, seq]) => { if (window.__apexloadRecorderSetPaused) window.__apexloadRecorderSetPaused(paused, seq) }`, paused, seq)
}

func (r *Recorder) nextFrameStateSeqLocked() uint64 {
	r.frameStateSeq++
	return r.frameStateSeq
}

// pushFrameState runs a state setter in every frame of a page. The capture
// script runs in each frame, so pause and pick state must reach iframes too:
// otherwise a pick inside an iframe records a click and Esc there is captured
// as a key press. The main frame is updated before returning; iframes are
// updated concurrently so a frame stuck mid-navigation cannot stall the
// control, and the sequence number makes each frame ignore a push older than
// one it already applied. Best effort: a frame that detaches is skipped.
func pushFrameState(page playwright.Page, script string, value bool, seq uint64) {
	if page == nil {
		return
	}
	args := []any{value, seq}
	main := page.MainFrame()
	_, _ = main.Evaluate(script, args)
	for _, frame := range page.Frames() {
		if frame == main {
			continue
		}
		go func(frame playwright.Frame) { _, _ = frame.Evaluate(script, args) }(frame)
	}
}

// syncFrameState brings an iframe that just loaded or navigated up to the
// current pause and pick state; its fresh document starts unpaused and not
// picking.
func syncFrameState(frame playwright.Frame, paused, picking bool, seq uint64) {
	_, _ = frame.Evaluate(`([paused, picking, seq]) => {
		if (window.__apexloadRecorderSetPaused) window.__apexloadRecorderSetPaused(paused, seq)
		if (window.__apexloadRecorderSetAssertMode) window.__apexloadRecorderSetAssertMode(picking, seq)
	}`, []any{paused, picking, seq})
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
	select {
	case r.navigationSlots <- struct{}{}:
		defer func() { <-r.navigationSlots }()
	default:
		return fmt.Errorf("browser navigation already in progress")
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
// tab in a separate context in the same browser. Besides excluding its clicks,
// this keeps dashboard API traffic out of the application's side-car HAR.
func (r *Recorder) OpenControlPanel(targetURL string, localStorage map[string]string) error {
	targetURL = strings.TrimSpace(targetURL)
	if err := ValidateControlPanelURL(targetURL); err != nil {
		return err
	}
	select {
	case r.controlPanelSlots <- struct{}{}:
		defer func() { <-r.controlPanelSlots }()
	default:
		return fmt.Errorf("browser control panel open already in progress")
	}

	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return fmt.Errorf("recording already stopped")
	}
	if len(r.controlPages) >= maxBrowserControlPages {
		r.mu.Unlock()
		return fmt.Errorf("browser control panel limit reached")
	}
	context := r.controlContext
	browser := r.browser
	active := r.activePage
	r.mu.Unlock()
	if context == nil {
		if browser == nil {
			r.mu.Lock()
			r.openingControl = false
			r.mu.Unlock()
			return fmt.Errorf("browser recorder context unavailable")
		}
		var err error
		context, err = browser.NewContext()
		r.mu.Lock()
		if err == nil {
			r.controlContext = context
		}
		r.openingControl = false
		r.mu.Unlock()
		if err != nil {
			return fmt.Errorf("open control panel context: %w", err)
		}
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
	page.OnClose(func(closed playwright.Page) { r.handlePageClose(closed) })
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
	if atStepSeq == 0 {
		r.flushPendingCapture(time.Now().Add(2 * time.Second))
	}
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
		"recording_stop", "sidebar_layout":
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
	// On sets the picker explicitly (pick_toggle); absent, the picker toggles.
	On *bool `json:"on"`
	// Bar layout (sidebar_layout).
	Edge     string  `json:"edge"`
	Offset   float64 `json:"offset"`
	Expanded bool    `json:"expanded"`
	Hidden   bool    `json:"hidden"`
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
		if ev.On != nil {
			r.SetAssertMode(*ev.On)
			break
		}
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
	case "sidebar_layout":
		err = r.setSidebarLayout(sidebarLayoutView{Edge: ev.Edge, Offset: ev.Offset, Expanded: ev.Expanded, Hidden: ev.Hidden})
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
	return boundedtext.TruncateUTF8(fmt.Sprintf(`iframe[src*="%s"]`, strings.ReplaceAll(match, `"`, `\"`)), maxBrowserNativeURLBytes)
}

// Done is closed when the user closes the browser (or it disconnects).
func (r *Recorder) Done() <-chan struct{} { return r.done }

func (r *Recorder) signalDone() { r.doneOnce.Do(func() { close(r.done) }) }

// flushPendingCapture settles debounced application input before a control
// transition can change its section, inclusion window, or transaction boundary.
// Never hold mu while evaluating: the bindings re-enter handleRecordBinding.
func (r *Recorder) flushPendingCapture(deadline time.Time) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	pages := append([]playwright.Page(nil), r.recordablePagesLocked()...)
	r.mu.Unlock()
	for _, page := range pages {
		for _, frame := range page.Frames() {
			_ = runOperationUntil(deadline, func() error {
				_, err := frame.Evaluate(`() => window.__apexloadRecorderFlush ? window.__apexloadRecorderFlush() : undefined`)
				return err
			}, nil)
		}
	}
}

// Stop closes the browser (flushing the side-car HAR), loads the HAR into the
// session, and returns the finished session. Safe to call after the user
// already closed the window.
func (r *Recorder) Stop() (*Session, error) {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	recordingTestBeforeStop(r.done)
	stopDeadline := time.Now().Add(maxBrowserStopDuration)
	r.mu.Lock()
	if r.stopped {
		session := r.session
		r.mu.Unlock()
		return session, nil
	}
	r.mu.Unlock()
	r.flushPendingCapture(minTime(stopDeadline, time.Now().Add(2*time.Second)))
	r.mu.Lock()
	if r.stopped { // ForceStop may have won while a page was unresponsive.
		session := r.session.clone()
		r.mu.Unlock()
		return session, nil
	}
	r.stopped = true
	r.mu.Unlock()

	// Drain in-flight screenshot goroutines; once they observe r.stopped they
	// no-op, so the finished session is never mutated after we return it.
	if !waitGroupUntil(&r.thumbWG, stopDeadline) {
		r.mu.Lock()
		r.appendWarningLocked("thumbnail capture did not stop before the recorder shutdown deadline; unfinished thumbnails were omitted")
		r.mu.Unlock()
	}

	r.mu.Lock()
	context := r.context
	pages := append([]playwright.Page(nil), r.recordablePagesLocked()...)
	r.mu.Unlock()
	if context != nil {
		// Export storage state before closing so the user can reuse the
		// authenticated session on a later recording.
		stateResult := make(chan recorderStorageExport, 1)
		go func() {
			stateResult <- exportRecorderStorageState(context, pages)
		}()
		select {
		case exported := <-stateResult:
			if exported.err != nil {
				r.mu.Lock()
				r.appendWarningLocked(fmt.Sprintf("browser storage-state export failed: %v", exported.err))
				r.mu.Unlock()
			} else if len(exported.data) > 0 {
				r.mu.Lock()
				r.session.StorageState = exported.data
				if exported.truncated {
					r.appendWarningLocked("browser storage-state export was truncated to safety limits")
				}
				r.mu.Unlock()
			}
		case <-time.After(durationUntil(stopDeadline, maxBrowserOperationDuration)):
			r.mu.Lock()
			r.appendWarningLocked("browser storage-state export timed out during recorder shutdown")
			r.mu.Unlock()
		}
		if r.harCapturePath == "" {
			if err := runOperationUntil(stopDeadline, func() error { return context.Close() }, r.closeBrowserAsync); err != nil {
				r.mu.Lock()
				r.appendWarningLocked("browser context close timed out during recorder shutdown")
				r.mu.Unlock()
			}
		} else {
			exceeded, flushErr := policeHARFlushUntil(r.harCapturePath, maxBrowserHARCaptureBytes, func() error {
				return context.Close()
			}, func() {
				r.closeBrowserAsync()
			}, minTime(stopDeadline, time.Now().Add(maxBrowserHARFlushDuration)))
			if exceeded {
				r.mu.Lock()
				r.harLimitExceeded = true
				r.appendWarningLocked(fmt.Sprintf("side-car HAR capture exceeded %d bytes during final flush and was discarded", maxBrowserHARCaptureBytes))
				r.mu.Unlock()
			}
			if flushErr != nil {
				r.mu.Lock()
				r.appendWarningLocked(fmt.Sprintf("side-car HAR final flush did not complete cleanly: %v", flushErr))
				r.mu.Unlock()
			}
		}
	}
	if !r.cleanupUntil(stopDeadline) {
		r.mu.Lock()
		r.appendWarningLocked("browser process cleanup exceeded the recorder shutdown deadline")
		r.mu.Unlock()
	}
	r.signalDone()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.session.StoppedAt = time.Now()
	// Close an open pause window so think-time subtraction sees bounded
	// intervals, and auto-close any open transactions (innermost first).
	if n := len(r.session.PauseIntervals); n > 0 && r.session.PauseIntervals[n-1].To.IsZero() {
		r.session.PauseIntervals[n-1].To = r.session.StoppedAt
	}
	openTransactions := append([]string(nil), r.session.OpenTransactions...)
	for i := len(openTransactions) - 1; i >= 0; i-- {
		name := openTransactions[i]
		_, markerErr := r.session.AddMarker(Marker{
			Kind:      MarkerTransactionEnd,
			Name:      name,
			Timestamp: r.session.StoppedAt,
			Auto:      true,
		})
		if markerErr != nil {
			r.appendWarningLocked(fmt.Sprintf("transaction %q could not be auto-closed: %v", name, markerErr))
			continue
		}
		r.appendWarningLocked(fmt.Sprintf(
			"transaction %q was still open and was closed automatically when the recording stopped", name))
	}
	r.session.OpenTransactions = nil
	if r.harCapturePath != "" {
		if r.harLimitExceeded {
			_ = os.Remove(r.harCapturePath)
		} else {
			artifact, err := loadAndFilterHARUntil(r.harCapturePath, maxBrowserHARBytes, r.opts.HARFilters, stopDeadline)
			if err != nil {
				r.appendWarningLocked(fmt.Sprintf("side-car HAR unavailable: %v", err))
			} else {
				for _, warning := range artifact.warnings(maxBrowserHARBytes) {
					r.appendWarningLocked(warning)
				}
				data := artifact.data
				r.session.HAR = data
				if !r.harTemp && r.session.HARPath != r.harCapturePath {
					path := r.session.HARPath
					if writeErr := runOperationUntil(stopDeadline, func() error { return os.WriteFile(path, data, 0o600) }, nil); writeErr != nil {
						r.appendWarningLocked(fmt.Sprintf("write side-car HAR: %v", writeErr))
					}
				}
			}
		}
		if r.harTemp {
			_ = os.Remove(r.harCapturePath)
			r.session.HARPath = ""
		} else if r.harCapturePath != r.session.HARPath {
			_ = os.Remove(r.harCapturePath)
		}
		r.harCapturePath = ""
		r.harLimitExceeded = false
	}
	return r.session, nil
}

type harShutdownLoadResult struct {
	artifact harArtifact
	err      error
}

func loadAndFilterHARUntil(path string, limit int64, filters HARFilters, deadline time.Time) (harArtifact, error) {
	if !deadline.After(time.Now()) {
		return harArtifact{}, fmt.Errorf("HAR processing exceeded recorder shutdown deadline")
	}
	done := make(chan harShutdownLoadResult, 1)
	go func() {
		artifact, err := readHARArtifact(path, limit, filters)
		if err == nil && artifact.filterErr == nil {
			// Each entry names the browser as its version's source; import
			// treats that as description only.
			if tagged, tagErr := protocol.TagBrowserHARHTTPVersions(artifact.data, protocol.HARProvenanceBrowser); tagErr == nil {
				artifact.data = tagged
			}
		}
		done <- harShutdownLoadResult{artifact: artifact, err: err}
	}()
	select {
	case result := <-done:
		return result.artifact, result.err
	case <-time.After(durationUntil(deadline, maxBrowserStopDuration)):
		return harArtifact{}, fmt.Errorf("HAR processing exceeded recorder shutdown deadline")
	}
}

// ForceStop is the best-effort teardown path used when graceful Stop is stuck.
// It intentionally skips storage-state/HAR flushing, because the caller keeps a
// snapshot artifact and may still recover a late Stop result if it completes.
func (r *Recorder) ForceStop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.signalDone()
	_ = r.cleanupUntil(time.Now().Add(maxBrowserOperationDuration))
	r.discardManagedHARTemp()
}

func (r *Recorder) discardManagedHARTemp() {
	r.mu.Lock()
	if r.session == nil {
		r.mu.Unlock()
		return
	}
	capturePath := r.harCapturePath
	if strings.TrimSpace(capturePath) == "" && r.harTemp {
		capturePath = r.session.HARPath
	}
	if strings.TrimSpace(capturePath) == "" || (!r.harTemp && capturePath == r.session.HARPath) {
		r.mu.Unlock()
		return
	}
	path := capturePath
	r.harCapturePath = ""
	if r.harTemp {
		r.session.HARPath = ""
	}
	r.mu.Unlock()
	_ = os.Remove(path)
}

// SetSection switches the active recording section (init|actions|teardown).
// Subsequent steps are tagged so conversion routes them into the matching DSL
// block.
func (r *Recorder) SetSection(section string) string {
	r.flushPendingCapture(time.Now().Add(2 * time.Second))
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
	seq := r.nextFrameStateSeqLocked()
	r.mu.Unlock()
	for _, page := range pages {
		r.pushAssertModeState(page, on, seq)
	}
	r.emit(Event{Type: EventPickState, Picking: on})
}

func (r *Recorder) pushAssertModeState(page playwright.Page, on bool, seq uint64) {
	pushFrameState(page, `([on, seq]) => { if (window.__apexloadRecorderSetAssertMode) window.__apexloadRecorderSetAssertMode(on, seq) }`, on, seq)
}

// configScript builds the init script that seeds the in-page panel's initial
// mode before the capture script runs.
func (r *Recorder) configScript() string {
	// The bar opens collapsed at the bottom-right; the recorder's live state
	// then carries any position the user chose (sidebarLayout).
	layout := defaultSidebarLayout()
	config := map[string]any{
		"sidebar": r.opts.Sidebar,
		"open":    layout.Expanded,
		"edge":    layout.Edge,
		"offset":  layout.Offset,
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
	Layout           sidebarLayoutView `json:"layout"`
}

// sidebarLayoutView places the in-page bar: the viewport edge it sits on, its
// position along that edge (0 = top/left end, 1 = bottom/right end), whether
// the advanced panel is open, and whether it is hidden to an edge tab.
type sidebarLayoutView struct {
	Edge     string  `json:"edge"`
	Offset   float64 `json:"offset"`
	Expanded bool    `json:"expanded"`
	Hidden   bool    `json:"hidden"`
}

func defaultSidebarLayout() sidebarLayoutView {
	return sidebarLayoutView{Edge: "bottom", Offset: 1}
}

// setSidebarLayout stores the bar's layout and pushes it to every page, so a
// move, hide or expand in one tab or document applies to all of them.
func (r *Recorder) setSidebarLayout(layout sidebarLayoutView) error {
	switch layout.Edge {
	case "top", "bottom", "left", "right":
	default:
		return fmt.Errorf("unknown edge %q", layout.Edge)
	}
	if math.IsNaN(layout.Offset) || layout.Offset < 0 {
		layout.Offset = 0
	} else if layout.Offset > 1 {
		layout.Offset = 1
	}
	r.mu.Lock()
	r.sidebarLayout = layout
	r.mu.Unlock()
	r.markSidebarDirty()
	return nil
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
		Layout:           r.sidebarLayout,
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
	if len(r.session.Annotations) >= maxBrowserAnnotations {
		warning, capEvent := r.recordCapHitEventLocked("annotations", "recording annotation limit reached; subsequent diagnostics were dropped")
		r.mu.Unlock()
		r.emit(capEvent)
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		return
	}
	annotation.Timestamp = time.Now()
	annotation.Kind = r.retainNativeStringLocked(annotation.Kind, maxBrowserNativeKindBytes)
	annotation.Text = r.retainNativeStringLocked(annotation.Text, maxBrowserAnnotationLen)
	annotation.URL = r.retainNativeStringLocked(annotation.URL, maxBrowserNativeURLBytes)
	annotation.NearStep = len(r.session.Steps)
	r.session.Annotations = append(r.session.Annotations, annotation)
	r.mu.Unlock()
	r.emit(Event{Type: EventAnnotation, Annotation: &annotation})
}

// recordWSFrame records a WebSocket frame: always bumps the count, and stores a
// size-capped, scrubbed payload when opted in.
func (r *Recorder) recordWSFrame(index int, direction string, payload []byte) {
	r.mu.Lock()
	// In-flight Playwright callbacks can fire after Stop closed the context;
	// don't mutate the (now-final) session afterwards.
	if r.stopped || index < 0 || index >= len(r.session.WebSockets) {
		r.mu.Unlock()
		return
	}
	if direction == "sent" {
		r.session.WebSockets[index].FramesSent++
	} else {
		r.session.WebSockets[index].FramesReceived++
	}
	if !r.opts.CaptureWSPayloads {
		r.mu.Unlock()
		return
	}
	if r.wsFrames >= maxBrowserWSFrames || r.wsBytes >= maxBrowserWSBytes {
		warning, capEvent := r.recordCapHitEventLocked("websocket_frames", "recording WebSocket-frame limit reached; subsequent frame payloads were dropped")
		r.mu.Unlock()
		r.emit(capEvent)
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		return
	}
	limit := r.opts.WSPayloadLimitBytes
	if limit <= 0 {
		limit = 4096
	}
	if limit > maxBrowserWSPayload {
		limit = maxBrowserWSPayload
	}
	if remaining := maxBrowserWSBytes - r.wsBytes; limit > remaining {
		limit = remaining
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
	if len(frame.Payload) > maxBrowserWSBytes-r.wsBytes {
		warning, capEvent := r.recordCapHitEventLocked("websocket_frames", "recording WebSocket-frame limit reached; subsequent frame payloads were dropped")
		r.mu.Unlock()
		r.emit(capEvent)
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		return
	}
	r.session.WebSockets[index].Frames = append(r.session.WebSockets[index].Frames, frame)
	r.wsFrames++
	r.wsBytes += len(frame.Payload)
	r.mu.Unlock()
}

func readFileLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("artifact is %d bytes; maximum is %d", info.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("artifact exceeds maximum of %d bytes", limit)
	}
	return data, nil
}

// harArtifact is a recording's side-car HAR as kept for export and
// conversion: the entries the host filters keep, with their bodies embedded.
type harArtifact struct {
	data []byte
	// hostFilter is what the host filters left out; filterErr reports
	// filters that could not be applied.
	hostFilter hostFilterResult
	filterErr  error
	// omittedBodies response bodies, omittedBytes of them encoded, were left
	// out to keep the HAR under its size limit.
	omittedBodies int
	omittedBytes  int64
}

// warnings say what the side-car HAR left out.
func (artifact harArtifact) warnings(limit int64) []string {
	var warnings []string
	if artifact.filterErr != nil {
		warnings = append(warnings, fmt.Sprintf("HAR host filter skipped: %v", artifact.filterErr))
	} else if summary := artifact.hostFilter.summary(); summary != "" {
		warnings = append(warnings, summary)
	}
	if artifact.omittedBodies > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"the side-car HAR kept every request but left out %d response bod(ies) (%.1f MB) to stay under its %d MB limit; checks and correlations cannot use those responses. Exclude media or third-party hosts to keep them",
			artifact.omittedBodies, float64(artifact.omittedBytes)/(1<<20), limit>>20))
	}
	return warnings
}

func readHARArtifact(path string, limit int64, filters HARFilters) (harArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return harArtifact{}, err
	}
	header := make([]byte, 4)
	_, readErr := io.ReadFull(file, header)
	_ = file.Close()
	if readErr != nil || string(header[:2]) != "PK" {
		data, err := readFileLimited(path, limit)
		if err != nil {
			return harArtifact{}, err
		}
		artifact := harArtifact{data: data}
		if filtered, hostFilter, filterErr := filterHARByHost(data, filters); filterErr != nil {
			artifact.filterErr = filterErr
		} else {
			artifact.data, artifact.hostFilter = filtered, hostFilter
		}
		return artifact, nil
	}
	return readAttachedHARZip(path, limit, filters)
}

// harAttachmentOverheadBytes bounds what embedding one attachment adds to the
// HAR beyond its encoded body: the "text", "encoding" or "comment" fields that
// replace its "_file" reference.
const harAttachmentOverheadBytes = 128

// harAttachment is one "_file" reference in a Playwright HAR.
type harAttachment struct {
	holder      map[string]any
	name        string
	rawName     string
	encodedSize int64
	// responseBody marks a response's content, which may be left out; any
	// other attachment, such as a request body, is part of what replay sends.
	responseBody bool
	textLike     bool
}

// readAttachedHARZip reads a Playwright HAR archive and embeds its attached
// bodies. Host filters apply first, so the entries they drop cost nothing.
// When the bodies still exceed limit, the HAR keeps every entry and leaves
// out response bodies, largest and non-text first, each with a comment
// saying why.
func readAttachedHARZip(path string, limit int64, filters HARFilters) (harArtifact, error) {
	if err := validateAttachedHARZipStructure(path); err != nil {
		return harArtifact{}, err
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return harArtifact{}, err
	}
	defer archive.Close()
	if len(archive.File) > maxAttachedHARZipEntries {
		return harArtifact{}, fmt.Errorf("attached HAR archive exceeds %d entries", maxAttachedHARZipEntries)
	}
	files := make(map[string]*zip.File, len(archive.File))
	var harFile *zip.File
	for _, file := range archive.File {
		if len(file.Name) > maxAttachedHARZipNameBytes {
			return harArtifact{}, fmt.Errorf("attached HAR archive entry name exceeds %d bytes", maxAttachedHARZipNameBytes)
		}
		name := strings.TrimPrefix(filepath.ToSlash(file.Name), "./")
		if _, duplicate := files[name]; duplicate {
			return harArtifact{}, fmt.Errorf("attached HAR archive contains duplicate entry %q", name)
		}
		files[name] = file
		if name == "har.har" || (harFile == nil && strings.HasSuffix(strings.ToLower(name), ".har")) {
			harFile = file
		}
	}
	if harFile == nil {
		return harArtifact{}, fmt.Errorf("attached HAR archive does not contain a HAR document")
	}
	harData, err := readHARZipEntry(harFile, limit)
	if err != nil {
		return harArtifact{}, err
	}
	var document any
	if err := json.Unmarshal(harData, &document); err != nil {
		return harArtifact{}, fmt.Errorf("decode attached HAR document: %w", err)
	}
	artifact := harArtifact{}
	if root, ok := document.(map[string]any); ok {
		artifact.hostFilter = filterHARDocumentByHost(root, filters)
	}

	var attachments []harAttachment
	var collect func(value any, key, parentKey string) error
	collect = func(value any, key, parentKey string) error {
		switch typed := value.(type) {
		case map[string]any:
			if rawName, ok := typed["_file"].(string); ok && strings.TrimSpace(rawName) != "" {
				name := strings.TrimPrefix(filepath.ToSlash(rawName), "./")
				entry := files[name]
				if entry == nil {
					return fmt.Errorf("HAR attachment %q is missing", rawName)
				}
				mimeType, _ := typed["mimeType"].(string)
				attachments = append(attachments, harAttachment{
					holder:       typed,
					name:         name,
					rawName:      rawName,
					encodedSize:  harEncodedSize(entry.UncompressedSize64),
					responseBody: key == "content" && parentKey == "response",
					textLike:     harTextMIMEType(mimeType),
				})
			}
			keys := make([]string, 0, len(typed))
			for childKey := range typed {
				keys = append(keys, childKey)
			}
			sort.Strings(keys)
			for _, childKey := range keys {
				if err := collect(typed[childKey], childKey, key); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := collect(child, key, parentKey); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(document, "", ""); err != nil {
		return harArtifact{}, err
	}
	base, err := json.Marshal(document)
	if err != nil {
		return harArtifact{}, err
	}
	remaining := limit - int64(len(base)) - int64(len(attachments))*harAttachmentOverheadBytes
	if remaining < 0 {
		return harArtifact{}, fmt.Errorf("HAR document exceeds maximum of %d bytes", limit)
	}
	// Bodies replay needs come first, then text responses, smallest first,
	// so a limit reached leaves out the fewest, least useful bodies.
	sort.SliceStable(attachments, func(i, j int) bool {
		if attachments[i].responseBody != attachments[j].responseBody {
			return !attachments[i].responseBody
		}
		if attachments[i].textLike != attachments[j].textLike {
			return attachments[i].textLike
		}
		return attachments[i].encodedSize < attachments[j].encodedSize
	})
	encodedCache := make(map[string]string)
	for _, attachment := range attachments {
		encoded, cached := encodedCache[attachment.name]
		if !cached && attachment.encodedSize <= remaining {
			content, readErr := readHARZipEntry(files[attachment.name], limit)
			if readErr != nil {
				return harArtifact{}, fmt.Errorf("read HAR attachment %q: %w", attachment.rawName, readErr)
			}
			encoded = base64.StdEncoding.EncodeToString(content)
			encodedCache[attachment.name] = encoded
			cached = true
		}
		if !cached || int64(len(encoded)) > remaining {
			if !attachment.responseBody {
				return harArtifact{}, fmt.Errorf("HAR attachments exceed maximum of %d bytes", limit)
			}
			delete(attachment.holder, "_file")
			attachment.holder["comment"] = fmt.Sprintf("response body left out to keep the recording HAR under %d MB", limit>>20)
			artifact.omittedBodies++
			artifact.omittedBytes += attachment.encodedSize
			continue
		}
		remaining -= int64(len(encoded))
		attachment.holder["text"] = encoded
		attachment.holder["encoding"] = "base64"
		delete(attachment.holder, "_file")
	}
	data, err := json.Marshal(document)
	if err != nil {
		return harArtifact{}, err
	}
	if int64(len(data)) > limit {
		return harArtifact{}, fmt.Errorf("hydrated HAR exceeds maximum of %d bytes", limit)
	}
	artifact.data = data
	return artifact, nil
}

// harEncodedSize is the base64 size of an attachment of size bytes, as the
// archive declares it.
func harEncodedSize(size uint64) int64 {
	return int64((min(size, 1<<60) + 2) / 3 * 4)
}

// harTextMIMEType reports a response type that checks and correlation read:
// markup, scripts, JSON, XML, forms and plain text.
func harTextMIMEType(mimeType string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if mediaType, _, found := strings.Cut(mimeType, ";"); found {
		mimeType = strings.TrimSpace(mediaType)
	}
	switch {
	case strings.HasPrefix(mimeType, "text/"):
		return true
	case strings.HasSuffix(mimeType, "+json"), strings.HasSuffix(mimeType, "+xml"):
		return true
	}
	switch mimeType {
	case "application/json", "application/javascript", "application/x-javascript", "application/ecmascript",
		"application/xml", "application/xhtml+xml", "application/x-www-form-urlencoded", "application/graphql":
		return true
	default:
		return false
	}
}

// validateAttachedHARZipStructure checks a raw HAR capture's ZIP structure
// before it is opened; the capture may be up to maxBrowserHARCaptureBytes.
func validateAttachedHARZipStructure(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 22 {
		return fmt.Errorf("attached HAR archive is truncated")
	}
	if info.Size() > maxBrowserHARCaptureBytes {
		return fmt.Errorf("attached HAR archive is %d bytes; maximum is %d", info.Size(), maxBrowserHARCaptureBytes)
	}
	tailSize := min(info.Size(), int64(22+0xffff))
	tail := make([]byte, tailSize)
	if _, err := file.ReadAt(tail, info.Size()-tailSize); err != nil {
		return err
	}
	eocd := -1
	for index := len(tail) - 22; index >= 0; index-- {
		if binary.LittleEndian.Uint32(tail[index:index+4]) == 0x06054b50 {
			eocd = index
			break
		}
	}
	if eocd < 0 {
		return fmt.Errorf("attached HAR archive end record is missing")
	}
	disk := binary.LittleEndian.Uint16(tail[eocd+4 : eocd+6])
	centralDisk := binary.LittleEndian.Uint16(tail[eocd+6 : eocd+8])
	diskEntries := binary.LittleEndian.Uint16(tail[eocd+8 : eocd+10])
	totalEntries := binary.LittleEndian.Uint16(tail[eocd+10 : eocd+12])
	centralSize := binary.LittleEndian.Uint32(tail[eocd+12 : eocd+16])
	centralOffset := binary.LittleEndian.Uint32(tail[eocd+16 : eocd+20])
	if disk != 0 || centralDisk != 0 || diskEntries != totalEntries {
		return fmt.Errorf("multi-disk attached HAR archives are not supported")
	}
	if totalEntries == 0xffff || centralSize == 0xffffffff || centralOffset == 0xffffffff {
		return fmt.Errorf("ZIP64 attached HAR archives are not supported")
	}
	if int(totalEntries) > maxAttachedHARZipEntries {
		return fmt.Errorf("attached HAR archive exceeds %d entries", maxAttachedHARZipEntries)
	}
	if centralSize > maxAttachedHARCentralBytes {
		return fmt.Errorf("attached HAR central directory exceeds %d bytes", maxAttachedHARCentralBytes)
	}
	centralEnd := uint64(centralOffset) + uint64(centralSize)
	if centralEnd > uint64(info.Size()) {
		return fmt.Errorf("attached HAR central directory is outside the archive")
	}
	central := make([]byte, int(centralSize))
	if len(central) > 0 {
		if _, err := file.ReadAt(central, int64(centralOffset)); err != nil {
			return err
		}
	}
	offset := 0
	for entry := 0; entry < int(totalEntries); entry++ {
		if len(central)-offset < 46 || binary.LittleEndian.Uint32(central[offset:offset+4]) != 0x02014b50 {
			return fmt.Errorf("attached HAR central directory entry %d is malformed", entry)
		}
		nameBytes := int(binary.LittleEndian.Uint16(central[offset+28 : offset+30]))
		extraBytes := int(binary.LittleEndian.Uint16(central[offset+30 : offset+32]))
		commentBytes := int(binary.LittleEndian.Uint16(central[offset+32 : offset+34]))
		if nameBytes > maxAttachedHARZipNameBytes {
			return fmt.Errorf("attached HAR archive entry name exceeds %d bytes", maxAttachedHARZipNameBytes)
		}
		next := offset + 46 + nameBytes + extraBytes + commentBytes
		if next < offset || next > len(central) {
			return fmt.Errorf("attached HAR central directory entry %d is truncated", entry)
		}
		offset = next
	}
	if offset != len(central) {
		return fmt.Errorf("attached HAR central directory has trailing data")
	}
	return nil
}

func readHARZipEntry(file *zip.File, limit int64) ([]byte, error) {
	if file == nil {
		return nil, fmt.Errorf("HAR zip entry is missing")
	}
	if limit > 0 && file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("artifact exceeds maximum of %d bytes", limit)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var source io.Reader = reader
	if limit > 0 {
		source = io.LimitReader(reader, limit+1)
	}
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(data)) > limit {
		return nil, fmt.Errorf("artifact exceeds maximum of %d bytes", limit)
	}
	return data, nil
}

func (r *Recorder) monitorHARSize(path string, context playwright.BrowserContext) {
	if strings.TrimSpace(path) == "" || context == nil {
		return
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil || info.Size() <= maxBrowserHARCaptureBytes {
				continue
			}
			r.mu.Lock()
			if r.stopped || r.context != context {
				r.mu.Unlock()
				return
			}
			r.context = nil
			r.harLimitExceeded = true
			r.appendWarningLocked(fmt.Sprintf("recording stopped because the HAR capture exceeded %d bytes", maxBrowserHARCaptureBytes))
			r.mu.Unlock()
			_, _ = policeHARFlush(path, maxBrowserHARCaptureBytes, func() error {
				return context.Close()
			}, func() {
				r.mu.Lock()
				browser := r.browser
				r.mu.Unlock()
				if browser != nil {
					_ = browser.Close()
				}
			})
			r.signalDone()
			return
		}
	}
}

// policeHARFlush runs the Playwright context close asynchronously so the HAR
// file remains observable while Playwright performs its final write. If the
// file crosses the cap, it is truncated immediately and abort is invoked to
// interrupt the browser flush. The final size check closes the race where the
// flush completes between polling ticks.
func policeHARFlush(path string, limit int64, flush func() error, abort func()) (bool, error) {
	return policeHARFlushUntil(path, limit, flush, abort, time.Now().Add(maxBrowserHARFlushDuration))
}

func policeHARFlushUntil(path string, limit int64, flush func() error, abort func(), deadline time.Time) (bool, error) {
	if flush == nil {
		return false, nil
	}
	done := make(chan error, 1)
	go func() { done <- flush() }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadlineTimer := time.NewTimer(durationUntil(deadline, maxBrowserHARFlushDuration))
	defer deadlineTimer.Stop()
	exceeded := false
	aborted := false
	check := func() {
		info, err := os.Stat(path)
		if err != nil || info.Size() <= limit {
			return
		}
		exceeded = true
		_ = os.Truncate(path, limit)
		if !aborted && abort != nil {
			aborted = true
			go abort()
		}
	}
	check()
	for {
		select {
		case err := <-done:
			check()
			return exceeded, err
		case <-ticker.C:
			check()
		case <-deadlineTimer.C:
			check()
			if !aborted && abort != nil {
				aborted = true
				go abort()
			}
			return exceeded, fmt.Errorf("HAR flush exceeded shutdown deadline")
		}
	}
}

// captureThumbnailAsync screenshots the page and attaches a size-capped JPEG
// thumbnail to the step out-of-band so capture is never blocked. Tracked by a
// WaitGroup so Stop can drain in-flight screenshots before the session becomes
// the final read-only artifact (no post-stop mutation of session.Steps).
func (r *Recorder) captureThumbnailAsync(page playwright.Page, seq int) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	if r.thumbnailSlots == nil {
		r.thumbnailSlots = make(chan struct{}, maxBrowserThumbnailWorkers)
	}
	select {
	case r.thumbnailSlots <- struct{}{}:
	default:
		warning := "thumbnail capture concurrency limit reached; some step thumbnails were omitted"
		if !r.thumbnailLimitWarned {
			r.thumbnailLimitWarned = true
			r.appendWarningLocked(warning)
			r.mu.Unlock()
			r.emit(Event{Type: EventWarning, Warning: warning})
			return
		}
		r.mu.Unlock()
		return
	}
	r.thumbWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.thumbWG.Done()
		defer func() { <-r.thumbnailSlots }()
		raw, err := page.Screenshot(playwright.PageScreenshotOptions{
			Type:    playwright.ScreenshotTypeJpeg,
			Quality: playwright.Int(45),
			Scale:   playwright.ScreenshotScaleCss,
			Timeout: playwright.Float(float64(maxBrowserOperationDuration / time.Millisecond)),
		})
		if err != nil || len(raw) == 0 || len(raw) > maxBrowserThumbnailRawBytes {
			return
		}
		thumb, err := thumbnailDataURL(raw, 200)
		if err != nil {
			return
		}
		r.mu.Lock()
		var updated *Step
		var warning string
		if !r.stopped {
			for i := range r.session.Steps {
				if r.session.Steps[i].Seq == seq {
					previousBytes := int64(len(r.session.Steps[i].Thumbnail))
					delta := int64(len(thumb)) - previousBytes
					if r.thumbnailBytes+delta > maxBrowserThumbnailBytes {
						if !r.thumbnailLimitWarned {
							r.thumbnailLimitWarned = true
							warning = "thumbnail capture byte limit reached; some step thumbnails were omitted"
							r.appendWarningLocked(warning)
						}
						break
					}
					r.session.Steps[i].Thumbnail = thumb
					r.thumbnailBytes += delta
					s := r.session.Steps[i]
					updated = &s
					break
				}
			}
		}
		r.mu.Unlock()
		if warning != "" {
			r.emit(Event{Type: EventWarning, Warning: warning})
		}
		if updated != nil {
			r.emit(Event{Type: EventStepUpdate, Step: updated})
		}
	}()
}

// windowArgs builds Chromium launch args for window positioning/sizing and
// the recorded browser's QUIC mode.
func (r *Recorder) windowArgs() []string {
	var args []string
	if r.opts.DisableQUIC {
		args = append(args, "--disable-quic")
	}
	if r.opts.WindowWidth > 0 && r.opts.WindowHeight > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", r.opts.WindowWidth, r.opts.WindowHeight))
	}
	if r.opts.WindowX != 0 || r.opts.WindowY != 0 {
		args = append(args, fmt.Sprintf("--window-position=%d,%d", r.opts.WindowX, r.opts.WindowY))
	}
	return args
}

func (r *Recorder) cleanup() {
	browser, pw := r.detachRuntime()
	if browser != nil {
		_ = browser.Close()
	}
	if pw != nil {
		_ = pw.Stop()
	}
}

func (r *Recorder) detachRuntime() (playwright.Browser, *playwright.Playwright) {
	r.mu.Lock()
	r.context = nil
	r.controlContext = nil
	browser := r.browser
	r.browser = nil
	pw := r.pw
	r.pw = nil
	r.mu.Unlock()
	return browser, pw
}

func (r *Recorder) closeBrowserAsync() {
	r.mu.Lock()
	browser := r.browser
	r.mu.Unlock()
	if browser != nil {
		go func() { _ = browser.Close() }()
	}
}

func (r *Recorder) cleanupUntil(deadline time.Time) bool {
	browser, pw := r.detachRuntime()
	// The browser closes before Playwright stops: stopping the driver first
	// crashes Chromium (see pwshutdown).
	var closeBrowser, stopDriver func() error
	if browser != nil {
		closeBrowser = func() error { return browser.Close() }
	}
	if pw != nil {
		stopDriver = pw.Stop
	}
	// A stop that spent its deadline on the HAR flush still lets the browser
	// close before the driver goes.
	wait := max(durationUntil(deadline, maxBrowserOperationDuration), minBrowserCloseGrace)
	return pwshutdown.CloseThenStop(wait, stopDriver, closeBrowser)
}

func waitGroupUntil(group *sync.WaitGroup, deadline time.Time) bool {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(durationUntil(deadline, maxBrowserOperationDuration)):
		return false
	}
}

func runOperationUntil(deadline time.Time, operation func() error, abort func()) error {
	if !deadline.After(time.Now()) {
		return fmt.Errorf("operation exceeded shutdown deadline")
	}
	done := make(chan error, 1)
	go func() { done <- operation() }()
	select {
	case err := <-done:
		return err
	case <-time.After(durationUntil(deadline, maxBrowserOperationDuration)):
		if abort != nil {
			abort()
		}
		return fmt.Errorf("operation exceeded shutdown deadline")
	}
}

func durationUntil(deadline time.Time, ceiling time.Duration) time.Duration {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Nanosecond
	}
	if ceiling > 0 && remaining > ceiling {
		return ceiling
	}
	return remaining
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
