package browser

import (
	"context"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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
	captureScreenshots         string
	clientMetricsEnabled       bool
	experienceCanary           bool
	traceparentEnabled         bool
	harCapturePolicy           string
	harDir                     string
	harWorkingPath             string
	harErrorSeen               bool
	harSLABreachSeen           bool
	harSLABreachThreshold      time.Duration
	lastHARArtifactPath        string
	mobile                     bool
	touchMode                  bool
	orientation                string
	deviceScaleFactor          float64
	cpuThrottleRate            float64
	viewportW                  int
	viewportH                  int

	pwContext            playwright.BrowserContext
	pwPage               playwright.Page
	pwCDP                playwright.CDPSession
	currentFrameSelector string

	mu                 sync.Mutex
	consoleEntries     []protocol.ConsoleEntry
	networkEntries     []protocol.NetworkEntry
	resourceValidators map[string]string
	pages              []playwright.Page

	dialogMu      sync.Mutex
	pendingDialog *dialogPolicy
	dialogErr     error
}

// dialogPolicy is armed by a browser_handle_dialog action and consumed by the
// next native dialog the page raises.
type dialogPolicy struct {
	action          string // "accept" | "dismiss"
	expectedMessage string
	expectedType    string
	promptText      string
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
	actionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	processBefore, _ := b.captureBrowserProcessSnapshot(actionCtx)
	b.applyActionTraceparent(actionCtx, result)

	actionErr, terminal := b.dispatchAction(actionCtx, action, result)
	if terminal {
		return result, nil
	}

	if actionErr == nil {
		actionErr = b.runPostActionSync(actionCtx, action)
	}
	if actionErr == nil {
		actionErr = b.afterActionDelay(actionCtx)
	}
	b.finalizeActionResult(actionCtx, action, result, actionErr, consoleStart, networkStart, processBefore)
	if b.harSLABreachThreshold > 0 && result.Duration > b.harSLABreachThreshold {
		b.noteHARSLABreach()
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
