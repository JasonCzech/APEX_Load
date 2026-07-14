package browser

import (
	"context"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/selector"
	playwright "github.com/playwright-community/playwright-go"
)

func init() {
	protocol.Register("browser", func() protocol.Protocol {
		return &Browser{}
	})
}

type Browser struct {
	engine                     string
	baseURL                    string
	currentURL                 string
	deviceProfile              string
	devicePlatform             string
	deviceTier                 string
	headless                   bool
	cacheEnabled               bool
	cacheContentURLs           bool
	cacheContentURLsConfigured bool
	checkNewerVersions         bool
	recordedResourcesAvailable bool
	recordedResources          map[string]struct{}
	downloadFilterMode         string
	downloadFilterHosts        []string
	networkMode                string
	networkPreset              string
	networkBandwidthKB         int
	networkLatencyMS           int
	downloadResources          string
	slowMo                     time.Duration
	userAgent                  string
	locale                     string
	timezone                   string
	geolocation                *playwright.Geolocation
	geolocationIncomplete      bool
	httpCredentials            *playwright.HttpCredentials
	proxy                      *playwright.Proxy
	clientCertificates         []playwright.ClientCertificate
	captureScreenshots         string
	clientMetricsEnabled       bool
	// autoActivatePopups restores the pre-change behavior where a newly-opened
	// popup/window becomes the active page. It defaults to false (popups are
	// registered but not activated; scripts switch explicitly with
	// browser_switch_window); scripts recorded against the old auto-switch
	// behavior set browser_auto_activate_popups=true as a migration path.
	autoActivatePopups    bool
	experienceCanary      bool
	traceparentEnabled    bool
	selfHealMode          string
	runMode               string
	harCapturePolicy      string
	harDir                string
	harWorkingPath        string
	harErrorSeen          bool
	harSLABreachSeen      bool
	harManualSeen         bool
	harSLABreachThreshold time.Duration
	lastHARArtifactPath   string
	harMonitorStop        chan struct{}
	harMonitorWG          sync.WaitGroup
	harLimitExceeded      chan struct{}
	harLimitOnce          sync.Once
	harObservedBytes      int64
	harObservedEntries    int
	mobile                bool
	touchMode             bool
	orientation           string
	orientationExplicit   bool
	deviceScaleFactor     float64
	cpuThrottleRate       float64
	cpuThrottleExplicit   bool
	viewportW             int
	viewportH             int

	pwContext            playwright.BrowserContext
	pwPage               playwright.Page
	pwCDP                playwright.CDPSession
	currentFrameSelector string

	mu             sync.Mutex
	consoleEntries []protocol.ConsoleEntry
	consoleDropped int
	networkEntries []protocol.NetworkEntry
	// networkEntriesDropped counts entries evicted from the front of
	// networkEntries by the ring/byte-budget cap, so absolute offset-based
	// slicing (eventOffsets/networkEntriesSince) stays correct across eviction.
	networkEntriesDropped int
	// prevActionNetworkStart / waitResponseScanStart let browser_wait_response
	// scan network entries from the start of the PREVIOUS action (the trigger,
	// e.g. a click), not from the wait action's own start. A response fired by
	// the trigger often completes during that trigger's finalization (HTML
	// capture, screenshot), before the wait action begins, and would otherwise
	// never be seen. Both are absolute network offsets, guarded by b.mu.
	prevActionNetworkStart int
	waitResponseScanStart  int
	// networkBodyBytes tracks the captured request-body bytes currently retained,
	// used to enforce the byte budget.
	networkBodyBytes      int
	resourceValidators    map[string]string
	resourceValidatorKeys []string
	pages                 []playwright.Page
	closing               bool
	networkTasks          chan browserNetworkTask
	networkWorkerStop     chan struct{}
	networkWorkerWG       sync.WaitGroup
	networkTasksDropped   int64

	dialogMu      sync.Mutex
	pendingDialog *dialogPolicy
	dialogErr     error

	selfHealMu    sync.Mutex
	selfHealCache map[string]selector.Candidate

	wsMu        sync.Mutex
	wsEvents    []browserWSEvent
	wsSeq       int64
	wsWatermark int64
	// wsLastWaitMinSeq keeps the action-local lower bound used by the last
	// successful wait so an immediate assert can validate the same frame without
	// rewinding the wait watermark for future waits.
	wsLastWaitMinSeq int64
	wsLastWaitValid  bool
}

// dialogPolicy is armed by a browser_handle_dialog action and consumed by the
// next native dialog the page raises.
type dialogPolicy struct {
	action          string // "accept" | "dismiss"
	expectedMessage string
	expectedType    string
	matchMode       string
	promptText      string
	wait            time.Duration
	// expectImmediate is true when the dialog is expected on the very next
	// action (the strict default: the following action fails if no dialog fires
	// within wait). Scripts that arm a handler several steps ahead of the
	// dialog-triggering action set expect_immediate:false so the armed policy
	// persists across intermediate actions instead of failing them.
	expectImmediate bool
}

type browserProcessSnapshot struct {
	CPUTimeSeconds float64
	MemoryMB       float64
	CapturedAt     time.Time
}

func (b *Browser) Name() string { return "browser" }

func (b *Browser) Init(ctx context.Context, config map[string]any) error {
	if err := b.applyConfig(config); err != nil {
		return err
	}
	return b.openBrowserContext()
}

func (b *Browser) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action == nil {
		return &protocol.Result{
			StartTime:    time.Now(),
			ErrorMessage: "browser action is required",
		}, nil
	}
	result := &protocol.Result{
		ActionName:  action.Name,
		StartTime:   time.Now(),
		Extractions: map[string]string{},
	}
	if b.currentPage() == nil {
		result.ErrorMessage = "browser protocol is not initialized"
		return result, nil
	}

	consoleStart, networkStart := b.eventOffsets()
	// Remember the previous action's network start so a browser_wait_response
	// here scans from the trigger action rather than from its own start.
	b.mu.Lock()
	b.waitResponseScanStart = b.prevActionNetworkStart
	b.prevActionNetworkStart = networkStart
	b.mu.Unlock()
	dialogArmedBefore, dialogWait, dialogExpectImmediate := b.pendingDialogState()
	// An arm-ahead policy (expect_immediate:false) persists across intermediate
	// actions until its dialog actually fires, so it must not fail or clear the
	// following action the way the strict (default) immediate policy does.
	dialogGuardsThisAction := dialogArmedBefore && dialogExpectImmediate && action.Type != "browser_handle_dialog"
	actionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var processBefore browserProcessSnapshot
	if b.clientMetricsEnabled {
		processBefore, _ = b.captureBrowserProcessSnapshot(actionCtx)
	}
	b.applyActionTraceparent(actionCtx, result)

	actionErr, terminal := b.dispatchAction(actionCtx, action, result)
	if terminal {
		result.Duration = time.Since(result.StartTime)
		if dialogGuardsThisAction {
			b.clearPendingDialog()
		}
		return result, nil
	}

	if actionErr == nil {
		actionErr = b.runPostActionSync(actionCtx, action)
	}
	if actionErr == nil && dialogGuardsThisAction {
		actionErr = b.waitForPendingDialog(actionCtx, dialogWait)
	} else if actionErr != nil && dialogGuardsThisAction {
		b.clearPendingDialog()
	}
	actionDuration := time.Since(result.StartTime)
	if actionErr == nil {
		actionErr = b.afterActionDelay(actionCtx)
	}
	b.finalizeActionResult(actionCtx, action, result, actionErr, consoleStart, networkStart, processBefore, actionDuration)
	if b.harSLABreachThreshold > 0 && result.Duration > b.harSLABreachThreshold {
		b.noteHARSLABreach()
	}
	if browserActionRequestsHARCapture(action) {
		b.noteHARManual()
	}
	if result.ErrorMessage != "" {
		b.noteHARError()
	}
	return result, nil
}

func (b *Browser) Teardown(_ context.Context) error {
	b.close()
	return nil
}

func (b *Browser) HealthCheck(_ context.Context) error {
	return nil
}
