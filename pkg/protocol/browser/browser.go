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
	experienceCanary           bool
	traceparentEnabled         bool
	selfHealMode               string
	runMode                    string
	harCapturePolicy           string
	harDir                     string
	harWorkingPath             string
	harErrorSeen               bool
	harSLABreachSeen           bool
	harManualSeen              bool
	harSLABreachThreshold      time.Duration
	lastHARArtifactPath        string
	mobile                     bool
	touchMode                  bool
	orientation                string
	orientationExplicit        bool
	deviceScaleFactor          float64
	cpuThrottleRate            float64
	cpuThrottleExplicit        bool
	viewportW                  int
	viewportH                  int

	pwContext            playwright.BrowserContext
	pwPage               playwright.Page
	pwCDP                playwright.CDPSession
	currentFrameSelector string

	mu             sync.Mutex
	consoleEntries []protocol.ConsoleEntry
	networkEntries []protocol.NetworkEntry
	// networkEntriesDropped counts entries evicted from the front of
	// networkEntries by the ring/byte-budget cap, so absolute offset-based
	// slicing (eventOffsets/networkEntriesSince) stays correct across eviction.
	networkEntriesDropped int
	// networkBodyBytes tracks the captured request-body bytes currently retained,
	// used to enforce the byte budget.
	networkBodyBytes      int
	resourceValidators    map[string]string
	resourceValidatorKeys []string
	pages                 []playwright.Page

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
	dialogArmedBefore, dialogWait := b.pendingDialogState()
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
		if action.Type != "browser_handle_dialog" && dialogArmedBefore {
			b.clearPendingDialog()
		}
		return result, nil
	}

	if actionErr == nil {
		actionErr = b.runPostActionSync(actionCtx, action)
	}
	if actionErr == nil && action.Type != "browser_handle_dialog" && dialogArmedBefore {
		actionErr = b.waitForPendingDialog(actionCtx, dialogWait)
	} else if actionErr != nil && action.Type != "browser_handle_dialog" && dialogArmedBefore {
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
