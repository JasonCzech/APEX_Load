package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/selector"
	playwright "github.com/mxschmitt/playwright-go"
)

func init() {
	protocol.Register("browser", func() protocol.Protocol {
		return &Browser{}
	})
}

type Browser struct {
	replayHAR                   *protocol.ReplayHARCapture
	sourceLease                 *protocol.BrowserSourceLease // Execution-owner expectation predates Init. Protected by mu.
	contextCloseGate            chan struct{}                // Context-aware physical-close ownership; initialized under mu.
	authoredIdentityReadPending bool                         // At most one native identity read, including a timed-out reader.
	evidence                    browserEvidenceState
	newUserPolicy               string
	documentProofVersion        int
	documentMetricsEnabled      bool
	storageState                *playwright.OptionalStorageState
	systemLanguage              string
	availableScreenWidth        int
	availableScreenHeight       int

	engine                     string
	quic                       string // browserQUICAuto or browserQUICOff
	baseURL                    string
	currentURL                 string
	deviceProfile              string
	devicePlatform             string
	deviceTier                 string
	headless                   bool
	httpErrorsAsWarnings       bool
	cacheEnabled               bool
	cacheContentURLs           bool
	cacheContentURLsConfigured bool
	checkNewerVersions         bool
	recordedResourcesAvailable bool
	recordedResources          map[string]struct{}
	downloadFilterMode         string
	downloadFilterHosts        []string
	allowedTargetHosts         []string
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
	harOwner              *harCaptureOwner
	harErrorSeen          bool
	harSLABreachSeen      bool
	harManualSeen         bool
	harSLABreachThreshold time.Duration
	lastHARArtifactPath   string
	// publishedHARArtifacts are the artifacts published since the engine last
	// took them (see TakeBrowserHARArtifacts).
	publishedHARArtifacts []protocol.BrowserHARArtifact
	harCaptureOmissions   map[string]int64
	// harEvidenceGeneration is the current context's browser evidence
	// generation, published with its native HAR.
	harEvidenceGeneration string
	// interactionsEnabled records gesture interaction evidence (E3).
	interactionsEnabled        bool
	interactionVersion         int
	authoredPageVersion        int
	interactions               browserInteractionState
	currentNavPreSequence      uint64
	activePageBindings         map[playwright.Page]*protocol.BrowserAuthoredPageBinding
	lifecycleRouteSeq          map[string]uint64
	lastActionPopup            playwright.Page
	lastDownloadOutcome        *protocol.BrowserInteractionOutcome
	harMonitorStop             chan struct{}
	harMonitorWG               sync.WaitGroup
	harLimitExceeded           chan struct{}
	harLimitOnce               sync.Once
	harObservedBytes           int64
	harObservedEntries         int
	harInFlightExceeded        bool
	runtimeTextRedactor        protocol.TextRedactor
	runtimeBoundedTextRedactor protocol.BoundedTextRedactor
	runtimeBoundedTextSnapshot protocol.BoundedTextRedactorSnapshot
	wsProjectionManaged        bool
	wsProjectionRevision       uint64
	runtimeSecretsActive       func() bool
	unredactedDebug            bool
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
	pwCDPPage            playwright.Page
	cdpInstallGeneration uint64
	createCDPSession     func(playwright.BrowserContext, playwright.Page) (playwright.CDPSession, error)
	currentFrameSelector string
	// metricsMark is the document the last page-metrics capture measured
	// (runtime_metrics.go), guarded by mu.
	metricsMark pageMetricsMark
	// recordedBrowserVersion is the browser build the script was recorded
	// with; browserVersionChecked and browserVersionSkews are the session's
	// version check and its unreported skews (version_skew.go), guarded by mu.
	recordedBrowserVersion string
	browserVersionChecked  bool
	browserVersionSkews    []browserVersionSkew
	// contextIterations counts the iterations the current browser context
	// has run (IterationTemperature), guarded by mu.
	contextIterations int
	// identityIndex and identityUsername name the observer's session
	// identity, reported once in its first step's evidence; guarded by mu.
	identityIndex    int
	identityUsername string
	identityReported bool

	mu sync.Mutex
	// refusedPage is the page whose document at refusedURL the site refused,
	// and refusal why; its later steps are skipped while it shows that URL.
	refusedPage    playwright.Page
	refusedURL     string
	refusal        string
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
	networkBodyBytes int
	// networkMetadataBytes tracks URLs and request/response header maps, which
	// need an independent aggregate bound even when requests have no body.
	networkMetadataBytes  int
	resourceValidators    map[string]string
	resourceValidatorKeys []string
	pages                 []playwright.Page
	closing               bool
	networkTasks          chan browserNetworkTask
	networkWorkerStop     chan struct{}
	networkWorkerWG       sync.WaitGroup
	networkWorkers        *browserNetworkWorkerState
	networkTasksDropped   int64
	requestPolicyStop     chan struct{}

	dialogMu      sync.Mutex
	pendingDialog *dialogPolicy
	dialogErr     error

	selfHealMu    sync.Mutex
	selfHealCache map[string]selector.Candidate

	wsMu        sync.Mutex
	wsEvents    []browserWSEvent
	wsSeq       int64
	wsWatermark int64
	// wsEpoch fences frames captured across a navigation reset. wsPageGeneration
	// separately fences handlers owned by a closed browser context.
	wsEpoch          uint64
	wsPageGeneration uint64
	// wsLastWaitMinSeq keeps the action-local lower bound used by the last
	// successful wait so an immediate assert can validate the same frame without
	// rewinding the wait watermark for future waits.
	wsLastWaitMinSeq int64
	wsLastWaitValid  bool

	// Evidence budget state, guarded by mu. harUnavailableReason is why the
	// budget refused this context's HAR writer, reported once per context;
	// resourceLimit is set when the context was refused or closed to protect
	// the generator, and every later result of the context then fails; a
	// refused context is retried at the next iteration after
	// admissionRetryAt, and admissionRefusalLogged limits its log to one line
	// per run of refusals.
	harUnavailableReason        string
	harUnavailableReported      bool
	resourceLimit               string
	admissionRetryAt            time.Time
	admissionRefusalLogged      bool
	networkTasksDroppedReported int64

	// salesforceJWT signs each new context in (session_jwt.go), or is nil.
	salesforceJWT *salesforceJWTSession
	// eptCalibration reads Salesforce's EPT counter after each step, and
	// lastSalesforceEPTMs is the context's last reading, guarded by mu
	// (salesforce_ept.go).
	eptCalibration      bool
	lastSalesforceEPTMs float64
}

// SetRuntimeTextRedactor installs a memory-only projection owned by the VUser.
// It is intentionally set after Init and is never copied into browser config or
// retained artifacts.
func (b *Browser) SetRuntimeTextRedactor(redactor protocol.TextRedactor) {
	b.mu.Lock()
	b.runtimeTextRedactor = redactor
	b.wsProjectionRevision++
	if redactor != nil {
		b.wsProjectionManaged = true
	}
	b.mu.Unlock()
}

// SetRuntimeBoundedTextRedactor installs a fail-closed projection for
// WebSocket evidence from untrusted frames. The matcher uses separate raw data.
func (b *Browser) SetRuntimeBoundedTextRedactor(redactor protocol.BoundedTextRedactor) {
	b.mu.Lock()
	b.runtimeBoundedTextRedactor = redactor
	b.wsProjectionRevision++
	if redactor != nil {
		b.wsProjectionManaged = true
	}
	b.mu.Unlock()
}

// SetRuntimeBoundedTextRedactorSnapshot captures one versioned policy for a
// complete WebSocket evidence publication.
func (b *Browser) SetRuntimeBoundedTextRedactorSnapshot(snapshot protocol.BoundedTextRedactorSnapshot) {
	b.mu.Lock()
	b.runtimeBoundedTextSnapshot = snapshot
	b.wsProjectionRevision++
	if snapshot != nil {
		b.wsProjectionManaged = true
	}
	b.mu.Unlock()
}

type wsTextProjection struct {
	redactor protocol.BoundedTextRedactor
	guard    protocol.BoundedTextRedactorCommitGuard
	revision uint64
	managed  bool
	debug    bool
}

func (b *Browser) wsProjectionSnapshot() (projection wsTextProjection) {
	b.mu.Lock()
	// A direct bounded callback has no policy-publication fence. It is unsafe
	// for retained evidence even though its individual calls are work-bounded.
	projection = wsTextProjection{revision: b.wsProjectionRevision, managed: b.wsProjectionManaged, debug: b.unredactedDebug}
	factory := b.runtimeBoundedTextSnapshot
	b.mu.Unlock()
	if factory != nil && !projection.debug {
		projection.redactor = nil
		defer func() {
			if recover() != nil {
				projection.redactor, projection.guard = nil, nil
			}
		}()
		redactor, guard := factory()
		if redactor != nil && guard != nil {
			projection.redactor, projection.guard = redactor, guard
		}
	}
	return projection
}

func (p wsTextProjection) redact(value string) (projected string, ok bool) {
	defer func() {
		if recover() != nil {
			projected, ok = "", false
		}
	}()
	const maxProjectionBytes = 16 << 10
	if len(value) > maxProjectionBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", false
	}
	if p.redactor == nil {
		// With no runtime redactor there are no engine-managed exact matches to
		// preserve. The fixed built-in scrubber is bounded by the input limit.
		// A legacy-only callback cannot provide the bounded work guarantee.
		if p.managed {
			return "", false
		}
		projected := scrub.Value(value, scrub.Policy{})
		return projected, len(projected) <= maxProjectionBytes && utf8.ValidString(projected) && !strings.ContainsRune(projected, 0)
	}
	projected, ok = p.redactor(value)
	return projected, ok && len(projected) <= maxProjectionBytes && utf8.ValidString(projected) && !strings.ContainsRune(projected, 0)
}

// SetRuntimeSecretActivity installs the VUser-owned, memory-only signal used
// to suppress ordinary pixel evidence whenever governed secret material is available.
func (b *Browser) SetRuntimeSecretActivity(active func() bool) {
	b.mu.Lock()
	b.runtimeSecretsActive = active
	b.wsProjectionRevision++
	if active != nil {
		b.wsProjectionManaged = true
	}
	b.mu.Unlock()
}

func (b *Browser) hasRuntimeSecretMaterial() bool {
	b.mu.Lock()
	active := b.runtimeSecretsActive
	b.mu.Unlock()
	return active != nil && active()
}

func (b *Browser) redactRuntimeText(value string) string {
	b.mu.Lock()
	redactor := b.runtimeTextRedactor
	b.mu.Unlock()
	if redactor == nil || value == "" {
		return value
	}
	return redactor(value)
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
	b.mu.Lock()
	owner := b.harOwner
	b.mu.Unlock()
	if owner != nil && !owner.retiredSuccessfully() {
		return errHAROwnershipUnproven
	}
	if owner != nil {
		b.mu.Lock()
		if b.harOwner == owner {
			b.harOwner = nil
			b.harWorkingPath = ""
			b.pwContext = nil
			unregisterRuntimeHARWorkingPath(owner.path)
		}
		b.mu.Unlock()
	}
	if err := b.applyConfig(config); err != nil {
		return err
	}
	if len(b.allowedTargetHosts) > 0 {
		// Playwright routing does not intercept every redirect leg, and page init
		// scripts do not run inside Dedicated/Shared Workers. Until browser egress
		// is enforced below the Chromium process, accepting a CI host allowlist
		// would provide a bypassable security boundary. Fail closed before launch.
		return fmt.Errorf("browser execution is disabled while the CI target-host policy is active")
	}
	if err := b.openBrowserContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	b.evidence.lifecycleContext = ctx
	b.mu.Unlock()
	return nil
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
	if err := validateBrowserActionTiming(action); err != nil {
		result.Duration = time.Since(result.StartTime)
		result.StatusCode = 400
		result.ErrorMessage = err.Error()
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	if b.currentPage() == nil {
		result.ErrorMessage = "browser protocol is not initialized"
		// A context closed to protect the generator says so explicitly.
		b.applyEvidenceOutcome(result)
		return result, nil
	}
	if refusal := b.refusalBlocking(action); refusal != "" {
		// Every step on a refused page would wait out its timeout; one
		// reason, at once, says what happened instead.
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = "skipped: " + refusal
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}

	b.beginEvidenceAction()
	defer func() { b.endEvidenceAction(ctx.Err() != nil) }()
	consoleStart, networkStart, networkOwner := b.actionEvidenceOffsets()
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
	b.clearActionTransientEvidence()
	preActionPage := b.currentPage()
	preActionNavSeq := b.recordNavPreSequence(preActionPage)
	actionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var processBefore browserProcessSnapshot
	if b.clientMetricsEnabled {
		processBefore, _ = b.captureBrowserProcessSnapshot(actionCtx)
	}
	b.applyActionTraceparent(actionCtx, result)

	preRead := time.Now()
	interactionStart := b.beginBrowserInteraction(actionCtx, action)
	if interactionStart != nil {
		// The gesture begins after the evidence read: its duration leaves out
		// the read's own round trip, but not a busy page's wait behind it,
		// which the gesture's own actionability checks would have waited for.
		result.StartTime = result.StartTime.Add(min(time.Since(preRead), interactionReadOverhead))
	}
	b.mu.Lock()
	if !b.evidence.clock.start.IsZero() {
		b.evidence.actionCreationLowerNS = time.Since(b.evidence.clock.start).Nanoseconds()
	}
	b.mu.Unlock()
	actionErr, terminal := b.dispatchAction(actionCtx, action, result)
	if terminal {
		result.Duration = time.Since(result.StartTime)
		if dialogGuardsThisAction {
			b.clearPendingDialog()
		}
		return result, nil
	}

	var interactionSync browserInteractionSync
	if actionErr == nil {
		var clock *protocol.BrowserOutcomeClock
		if b.interactionsEnabled && (b.interactionVersion == protocol.BrowserInteractionVersion3 || b.interactionVersion == protocol.BrowserInteractionVersion4) {
			clock = &protocol.BrowserOutcomeClock{}
		}
		actionErr = b.runPostActionSyncWithClock(actionCtx, action, clock)
		interactionSync = browserInteractionSync{reached: true, err: actionErr, contextErr: actionCtx.Err(), clock: clock.Clone()}
	}
	var dialogOutcome *protocol.BrowserInteractionOutcome
	if dialogGuardsThisAction {
		dialogBudgetMS := max(1, int(dialogWait/time.Millisecond))
		if actionErr == nil {
			var dialogClock protocol.BrowserOutcomeClock
			dialogWaitErr := b.waitForPendingDialogWithClock(actionCtx, time.Duration(dialogBudgetMS)*time.Millisecond, &dialogClock)
			if dialogWaitErr == nil {
				if b.peekDialogError() != nil {
					dialogOutcome = &protocol.BrowserInteractionOutcome{
						Kind:       protocol.InteractionOutcomeKindDialog,
						Declared:   "dialog",
						DeadlineMS: dialogBudgetMS,
						Observed:   protocol.InteractionOutcomeMissed,
						Reason:     "dialog_mismatch",
						Clock:      dialogClock.Clone(),
					}
				} else {
					dialogOutcome = &protocol.BrowserInteractionOutcome{
						Kind:       protocol.InteractionOutcomeKindDialog,
						Declared:   "dialog",
						DeadlineMS: dialogBudgetMS,
						Observed:   protocol.InteractionOutcomeMet,
						Clock:      dialogClock.Clone(),
					}
				}
			} else {
				actionErr = dialogWaitErr
				if actionCtx.Err() != nil || errors.Is(dialogWaitErr, context.Canceled) {
					dialogOutcome = &protocol.BrowserInteractionOutcome{
						Kind:       protocol.InteractionOutcomeKindDialog,
						Declared:   "dialog",
						DeadlineMS: dialogBudgetMS,
						Observed:   protocol.InteractionOutcomeNotReached,
						Reason:     "context_canceled",
					}
				} else {
					if dialogClock.ObservedAt.Before(dialogClock.DeadlineAt) {
						dialogClock.ClampToDeadline()
					}
					dialogOutcome = &protocol.BrowserInteractionOutcome{
						Kind:       protocol.InteractionOutcomeKindDialog,
						Declared:   "dialog",
						DeadlineMS: dialogBudgetMS,
						Observed:   protocol.InteractionOutcomeMissed,
						Reason:     "deadline_missed",
						Clock:      dialogClock.Clone(),
					}
				}
			}
		} else {
			b.clearPendingDialog()
			dialogOutcome = &protocol.BrowserInteractionOutcome{
				Kind:       protocol.InteractionOutcomeKindDialog,
				Declared:   "dialog",
				DeadlineMS: dialogBudgetMS,
				Observed:   protocol.InteractionOutcomeNotReached,
				Reason:     "action_failed",
			}
		}
	}
	downloadOutcome := b.takeLastDownloadOutcome()
	navOutcome := b.observeActionPageTransitionV3(actionCtx, action, result, preActionPage, preActionNavSeq)
	actionDuration := time.Since(result.StartTime)
	result.Duration = actionDuration
	assertionOutcomes, assertionErr, assertionHTML := b.evaluateBrowserAssertionsWithOutcomes(actionCtx, action, action.Assertions, result, actionErr != nil || b.peekDialogError() != nil)
	if actionErr == nil && assertionErr == nil {
		actionErr = b.afterActionDelay(actionCtx)
	}
	b.finalizeActionResultWithAssertions(actionCtx, action, result, actionErr, assertionErr, true, assertionHTML, consoleStart, networkStart, networkOwner, processBefore, actionDuration)
	b.captureBrowserInteraction(actionCtx, action, result, actionErr, interactionStart, interactionSync, dialogOutcome, downloadOutcome, assertionOutcomes, navOutcome)
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

// browserPageNavigation reports the steps that leave the current page or
// change pages, which still run after a page was refused.
func browserPageNavigation(actionType string) bool {
	switch actionType {
	case "browser_navigate", "browser_back", "browser_forward", "browser_reload", "browser_open_window", "browser_switch_window", "browser_close_window":
		return true
	default:
		return false
	}
}

// noteDocumentResponse records how the site answered a page load on page:
// its status on result, and, for an error status the step does not expect,
// the refusal the load fails with. Another answer ends an earlier refusal.
func (b *Browser) noteDocumentResponse(ctx context.Context, action *protocol.Action, result *protocol.Result, page playwright.Page, response playwright.Response) error {
	b.noteAuthoredPageResponse(ctx, action, result, response)
	if response == nil {
		return nil
	}
	result.StatusCode = response.Status()
	refusal := b.documentRefusal(action, response.URL(), response.Status(), page)
	pageURL := page.URL()
	b.mu.Lock()
	defer b.mu.Unlock()
	if refusal == "" {
		b.refusedPage, b.refusedURL, b.refusal = nil, "", ""
		return nil
	}
	b.refusedPage, b.refusedURL, b.refusal = page, pageURL, refusal
	return errors.New(refusal)
}

// refusalBlocking is why action cannot run: the current page still shows a
// document the site refused, and action does not leave it.
func (b *Browser) refusalBlocking(action *protocol.Action) string {
	if browserPageNavigation(action.Type) {
		return ""
	}
	b.mu.Lock()
	page, refusedPage, refusedURL, refusal := b.pwPage, b.refusedPage, b.refusedURL, b.refusal
	b.mu.Unlock()
	if refusedPage == nil || refusedPage != page || page.URL() != refusedURL {
		return ""
	}
	return refusal
}

func (b *Browser) Teardown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	owned := b.harOwner != nil
	b.mu.Unlock()
	if owned {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, runtimeHARCloseWait)
		defer cancel()
	}
	documentErr := b.finalizeDocumentEvidenceContext(ctx)
	return errors.Join(documentErr, b.closeWithContext(ctx))
}

func (b *Browser) HealthCheck(_ context.Context) error {
	return nil
}
