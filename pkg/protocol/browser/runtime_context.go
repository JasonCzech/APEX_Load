package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

const browserNetworkBodyCaptureLimitBytes = 512 * 1024

// Bounds on the per-session network-entry buffer. Without these the buffer grows
// for the whole browser session (reset only at Close), retaining full headers and
// up to 512KiB request bodies each — an unbounded memory leak on long replay
// sessions. We cap both the entry count and the total retained body bytes,
// evicting the oldest entries first (the sibling wsEvents buffer is similarly
// ring-bounded).
const (
	browserNetworkEntryRingLimit      = 2048
	browserNetworkBodyBudgetBytes     = 32 * 1024 * 1024
	browserNetworkMetadataBudgetBytes = 16 * 1024 * 1024
	browserNetworkURLLimitBytes       = 16 * 1024
	browserNetworkHeaderEntriesLimit  = 128
	browserNetworkHeaderBytesLimit    = 64 * 1024
	browserNetworkHeaderNameLimit     = 1024
	browserNetworkHeaderValueLimit    = 16 * 1024
	browserConsoleEntryRingLimit      = 2048
	browserConsoleTextLimitBytes      = 4096
	browserConsoleSourceLimitBytes    = 16 * 1024
	browserConsoleLevelLimitBytes     = 128
	browserPageLimit                  = 32
	browserNetworkWorkerCount         = 4
	browserNetworkTaskQueueSize       = 256
)

type browserNetworkTask struct {
	generation string
	request    playwright.Request
	finished   bool
	evidence   *protocol.BrowserResourceEvidence
	join       resourceJoin
	receipt    *browserNetworkTaskReceipt
}

// openBrowserContext admits and opens the session's context. A memory
// refusal is not an Init error: the session stays open without a context and
// every action fails as a resource-limit result, so a refused VUser is
// reported instead of aborting the run.
func (b *Browser) openBrowserContext() error {
	b.mu.Lock()
	b.resourceLimit, b.harUnavailableReason, b.harUnavailableReported = "", "", false
	b.mu.Unlock()
	admission, cancel := context.WithTimeout(context.Background(), runtimeHARCloseWait)
	err := browserEvidenceBudget.admitBrowserContext(admission, b)
	cancel()
	if errors.Is(err, errBrowserResourceLimit) {
		b.mu.Lock()
		b.resourceLimit = resourceLimitAdmission
		b.admissionRetryAt = time.Now().Add(browserContextReadmissionBackoff)
		// One log line per run of refusals: a refused session retries every
		// iteration while memory stays short.
		logRefusal := !b.admissionRefusalLogged
		b.admissionRefusalLogged = true
		b.mu.Unlock()
		if logRefusal {
			logEvidenceEvent("browser context refused to protect the generator; the session retries at each iteration", "error", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := b.openAdmittedBrowserContext(); err != nil {
		browserEvidenceBudget.releaseBrowserContext(b)
		return err
	}
	b.mu.Lock()
	b.admissionRefusalLogged = false
	b.mu.Unlock()
	return nil
}

// browserContextReadmissionBackoff is how long a refused session waits
// before its next iteration tries admission again; several memory samples
// fit inside it.
var browserContextReadmissionBackoff = 5 * time.Second

// BeginIteration gives a session refused at admission another try at an
// iteration boundary, so a reopened context starts the iteration at its
// first action instead of joining it midway on a blank page. The try first
// waits out the backoff, so a refused VUser's iterations neither spin nor
// flood the results; while it stays refused, each action fails at once as a
// resource limit. A context that is admitted but cannot open fails the
// iteration setup, as the same error fails Init, and so does a session a
// resource limit closed.
func (b *Browser) BeginIteration(ctx context.Context) error {
	b.mu.Lock()
	limit, retryAt, lifecycle := b.resourceLimit, b.admissionRetryAt, b.evidence.lifecycleContext
	b.mu.Unlock()
	switch limit {
	case "":
		return nil
	case resourceLimitAdmission:
	default:
		// The memory watchdog or the HAR size cap closed this session: its
		// recording was cut short and memory may still be short. It does not
		// reopen; the VUser ends here instead of failing every action at once
		// for the rest of the run.
		return fmt.Errorf("%w (%s): the browser session was closed by its resource limit; this VUser stops", errBrowserResourceLimit, limit)
	}
	if wait := time.Until(retryAt); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil
		}
	}
	if err := b.openBrowserContext(); err != nil {
		return err
	}
	// Opening reset the evidence state; it keeps the session's lifecycle.
	b.mu.Lock()
	b.evidence.lifecycleContext = lifecycle
	b.mu.Unlock()
	return nil
}

func (b *Browser) openAdmittedBrowserContext() error {
	options := b.browserContextOptions()
	if err := b.applyHARCaptureOptions(&options); err != nil {
		return err
	}
	pwBrowser, err := getBrowserInstance(b.engine, b.headless, b.quic)
	if err != nil {
		return errors.Join(err, b.discardHARWorkingFile())
	}
	b.mu.Lock()
	owner := b.harOwner
	b.mu.Unlock()
	if owner != nil {
		if err := owner.beginWriter(); err != nil {
			owner.abandonWriter()
			return errors.Join(err, b.discardHARWorkingFile())
		}
	}
	browserContext, err := pwBrowser.NewContext(options)
	if err != nil {
		if owner != nil {
			owner.abandonWriter()
		}
		return errors.Join(err, b.discardHARWorkingFile())
	}
	if owner != nil {
		owner.bindWriter(func() error { return closeOwnedHARBrowserContext(browserContext) })
	}
	b.mu.Lock()
	b.closing = false
	b.requestPolicyStop = make(chan struct{})
	b.mu.Unlock()
	if err := b.installRequestPolicy(browserContext); err != nil {
		b.stopRequestPolicyWaiters()
		return b.failBrowserContextInitialization(browserContext, err)
	}
	if b.clientMetricsEnabled {
		if err := browserContext.AddInitScript(playwright.Script{Content: playwright.String(browserMetricsBootstrapScript)}); err != nil {
			b.stopRequestPolicyWaiters()
			return b.failBrowserContextInitialization(browserContext, err)
		}
	}

	if err := b.installDocumentEvidence(browserContext, pwBrowser.Version()); err != nil {
		b.stopRequestPolicyWaiters()
		return b.failBrowserContextInitialization(browserContext, err)
	}
	if b.interactionsEnabled {
		b.mu.Lock()
		generation := b.evidence.generation
		// Document bounds belong to this context's documents.
		b.interactions.documents = nil
		b.mu.Unlock()
		if err := browserContext.AddInitScript(playwright.Script{Content: playwright.String(browserInteractionObserver(browserInteractionGlobal(generation)))}); err != nil {
			b.stopRequestPolicyWaiters()
			return b.failBrowserContextInitialization(browserContext, err)
		}
	}
	b.installBrowserContextHandlers(browserContext)

	if script := b.environmentEmulationScript(); script != "" {
		if err := browserContext.AddInitScript(playwright.Script{Content: playwright.String(script)}); err != nil {
			b.stopRequestPolicyWaiters()
			return b.failBrowserContextInitialization(browserContext, err)
		}
	}

	page, err := browserContext.NewPage()
	if err != nil {
		b.stopRequestPolicyWaiters()
		return b.failBrowserContextInitialization(browserContext, err)
	}

	b.setBrowserContext(browserContext)
	b.startHARMonitor()
	b.startNetworkWorkers()
	b.registerPage(page, true)
	return nil
}

func (b *Browser) failBrowserContextInitialization(browserContext playwright.BrowserContext, cause error) error {
	b.mu.Lock()
	owner := b.harOwner
	b.mu.Unlock()
	if owner != nil {
		return errors.Join(cause, b.discardHARWorkingFile())
	}
	return errors.Join(cause, browserContext.Close())
}

func (b *Browser) browserContextOptions() playwright.BrowserNewContextOptions {
	viewport := b.browserViewport()
	deviceScaleFactor := b.deviceScaleFactor
	if deviceScaleFactor <= 0 {
		deviceScaleFactor = 1
	}
	contextOptions := playwright.BrowserNewContextOptions{
		StorageState:      b.storageState,
		AcceptDownloads:   playwright.Bool(true),
		DeviceScaleFactor: playwright.Float(deviceScaleFactor),
		HasTouch:          playwright.Bool(b.touchMode),
		IsMobile:          playwright.Bool(b.mobile),
		Screen:            viewport,
		Viewport:          viewport,
	}
	if len(b.allowedTargetHosts) > 0 {
		contextOptions.ServiceWorkers = playwright.ServiceWorkerPolicyBlock
	}
	if b.userAgent != "" {
		contextOptions.UserAgent = playwright.String(b.userAgent)
	}
	if b.locale != "" {
		contextOptions.Locale = playwright.String(b.locale)
	}
	if b.timezone != "" {
		contextOptions.TimezoneId = playwright.String(b.timezone)
	}
	if b.geolocation != nil {
		contextOptions.Geolocation = b.geolocation
		contextOptions.Permissions = append(contextOptions.Permissions, "geolocation")
	}
	if b.httpCredentials != nil {
		contextOptions.HttpCredentials = b.httpCredentials
	}
	if b.proxy != nil {
		contextOptions.Proxy = b.proxy
	}
	if len(b.clientCertificates) > 0 {
		contextOptions.ClientCertificates = b.clientCertificates
	}
	return contextOptions
}

func (b *Browser) browserViewport() *playwright.Size {
	width := b.viewportW
	height := b.viewportH
	if width <= 0 {
		width = 1440
	}
	if height <= 0 {
		height = 900
	}
	switch b.orientation {
	case "portrait":
		if width > height {
			width, height = height, width
		}
	case "landscape":
		if height > width {
			width, height = height, width
		}
	}
	return &playwright.Size{Width: width, Height: height}
}

func (b *Browser) installBrowserContextHandlers(browserContext playwright.BrowserContext) {
	b.mu.Lock()
	generation := b.evidence.generation
	b.mu.Unlock()
	browserContext.OnPage(func(page playwright.Page) {
		// Popups do not auto-activate by default; a script switches explicitly
		// with browser_switch_window. browser_auto_activate_popups restores the
		// old auto-switch behavior for scripts recorded against it.
		b.registerPage(page, b.autoActivatePopups, generation)
	})
	browserContext.OnConsole(func(message playwright.ConsoleMessage) {
		source := ""
		if location := message.Location(); location != nil {
			source = location.URL
		}
		b.appendConsoleEntry(protocol.ConsoleEntry{
			Level:     message.Type(),
			Text:      strings.TrimSpace(message.Text()),
			Source:    source,
			Timestamp: time.Now(),
		}, generation)
	})
	browserContext.OnDialog(func(dialog playwright.Dialog) {
		// A browser_handle_dialog action arms a one-shot policy for the next
		// dialog; without one we dismiss so the page never hangs.
		policy, admitted := b.takeDialogPolicy(generation)
		if !admitted {
			return
		}
		if policy != nil {
			if expectedType := strings.TrimSpace(policy.expectedType); expectedType != "" && !strings.EqualFold(dialog.Type(), expectedType) {
				b.setDialogError(fmt.Errorf("browser dialog type = %q, want %q", dialog.Type(), expectedType))
			}
			if expectedMessage := strings.TrimSpace(policy.expectedMessage); expectedMessage != "" && !dialogMessageMatches(dialog.Message(), expectedMessage, policy.matchMode) {
				b.setDialogError(fmt.Errorf("browser dialog message = %q, want %q", dialog.Message(), expectedMessage))
			}
		}
		switch {
		case policy == nil, policy.action == "dismiss":
			_ = dialog.Dismiss()
		case policy.promptText != "":
			_ = dialog.Accept(policy.promptText)
		default:
			_ = dialog.Accept()
		}
	})
	browserContext.OnResponse(func(response playwright.Response) {
		b.rememberResourceValidator(response.URL(), safeResponseHeaders(response), generation)
	})
	browserContext.OnFrameDetached(func(frame playwright.Frame) { b.censorFrameEvidence(frame) })
	browserContext.OnRequest(func(request playwright.Request) { b.beginRequestEvidence(request, generation) })
	browserContext.OnRequestFinished(func(request playwright.Request) {
		if !b.browserEvidenceGenerationCurrent(generation) {
			return
		}
		b.noteHARRequest(request, generation)
		b.enqueueNetworkRequest(request, true, generation)
	})
	browserContext.OnRequestFailed(func(request playwright.Request) {
		if !b.browserEvidenceGenerationCurrent(generation) {
			return
		}
		b.noteHARRequest(request, generation)
		b.enqueueNetworkRequest(request, false, generation)
	})
}

func (b *Browser) appendConsoleEntry(entry protocol.ConsoleEntry, expectedGeneration ...string) {
	if len(entry.Text) > browserConsoleTextLimitBytes {
		entry.Text = boundedtext.TruncateUTF8(entry.Text, browserConsoleTextLimitBytes)
	}
	entry.Source = boundedtext.TruncateUTF8(entry.Source, browserConsoleSourceLimitBytes)
	entry.Level = boundedtext.TruncateUTF8(entry.Level, browserConsoleLevelLimitBytes)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closing || len(expectedGeneration) > 0 && expectedGeneration[0] != b.evidence.generation {
		return
	}
	b.consoleEntries = append(b.consoleEntries, entry)
	if excess := len(b.consoleEntries) - browserConsoleEntryRingLimit; excess > 0 {
		retained := make([]protocol.ConsoleEntry, len(b.consoleEntries)-excess)
		copy(retained, b.consoleEntries[excess:])
		b.consoleEntries = retained
		b.consoleDropped += excess
	}
}

func (b *Browser) startNetworkWorkers() {
	b.mu.Lock()
	b.retireNetworkWorkersLocked()
	b.networkWorkers = newBrowserNetworkWorkerState()
	b.closing = false
	b.networkTasks = make(chan browserNetworkTask, browserNetworkTaskQueueSize)
	b.networkWorkerStop = make(chan struct{})
	tasks := b.networkTasks
	stop := b.networkWorkerStop
	b.mu.Unlock()
	for range browserNetworkWorkerCount {
		b.networkWorkerWG.Add(1)
		go func() {
			defer b.networkWorkerWG.Done()
			for {
				select {
				case <-stop:
					return
				case task := <-tasks:
					b.processNetworkTask(task)
				}
			}
		}()
	}
}

func (b *Browser) enqueueNetworkRequest(request playwright.Request, finished bool, expectedGeneration ...string) {
	evidence, join := b.completeRequestEvidence(request, finished, expectedGeneration...)
	b.mu.Lock()
	if b.closing || len(expectedGeneration) > 0 && expectedGeneration[0] != b.evidence.generation || b.networkTasks == nil || b.networkWorkerStop == nil {
		b.mu.Unlock()
		return
	}
	b.admitNetworkTaskLocked(browserNetworkTask{request: request, finished: finished, evidence: evidence, join: join, generation: b.evidence.generation})
	b.mu.Unlock()
}

// Only the admitted context may consume the next action's dialog policy.
// Lock context identity and policy together so late old-context callbacks cannot
// steal a new iteration's one-shot policy after passing an earlier check.
func (b *Browser) takeDialogPolicy(generation string) (*dialogPolicy, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closing || generation != b.evidence.generation {
		return nil, false
	}
	b.dialogMu.Lock()
	defer b.dialogMu.Unlock()
	policy := b.pendingDialog
	b.pendingDialog = nil
	return policy, true
}

func dialogMessageMatches(actual, expected, mode string) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "contains":
		return strings.Contains(actual, expected)
	case "normalized":
		return strings.EqualFold(normalizeDialogMessage(actual), normalizeDialogMessage(expected))
	default:
		return actual == expected
	}
}

func normalizeDialogMessage(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (b *Browser) setDialogError(err error) {
	if err == nil {
		return
	}
	b.dialogMu.Lock()
	defer b.dialogMu.Unlock()
	b.dialogErr = err
}

func (b *Browser) consumeDialogError() error {
	b.dialogMu.Lock()
	defer b.dialogMu.Unlock()
	err := b.dialogErr
	b.dialogErr = nil
	return err
}

func (b *Browser) pendingDialogState() (armed bool, wait time.Duration, expectImmediate bool) {
	b.dialogMu.Lock()
	defer b.dialogMu.Unlock()
	if b.pendingDialog == nil {
		return false, 0, false
	}
	wait = b.pendingDialog.wait
	if wait <= 0 {
		wait = time.Second
	}
	return true, wait, b.pendingDialog.expectImmediate
}

func (b *Browser) waitForPendingDialog(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		armed, _, _ := b.pendingDialogState()
		if !armed {
			return nil
		}
		select {
		case <-ctx.Done():
			b.clearPendingDialog()
			return ctx.Err()
		case <-deadline.C:
			b.clearPendingDialog()
			return fmt.Errorf("expected browser dialog was not observed within %s", timeout)
		case <-ticker.C:
		}
	}
}

func (b *Browser) clearPendingDialog() {
	b.dialogMu.Lock()
	defer b.dialogMu.Unlock()
	b.pendingDialog = nil
}

// currentPage returns the active page under b.mu. The OnPage handler activates
// new pages (registerPage) on Playwright's dispatch goroutine, so every read of
// the active page from the execution goroutine must go through this accessor to
// avoid racing that write.
func (b *Browser) currentPage() playwright.Page {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pwPage
}

func (b *Browser) currentBrowserContext() playwright.BrowserContext {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pwContext
}

func (b *Browser) setBrowserContext(browserContext playwright.BrowserContext) {
	b.mu.Lock()
	b.pwContext = browserContext
	b.mu.Unlock()
}

func (b *Browser) registerPage(page playwright.Page, activate bool, expectedGeneration ...string) {
	if page == nil {
		return
	}
	b.mu.Lock()
	if b.closing || len(expectedGeneration) > 0 && expectedGeneration[0] != b.evidence.generation {
		b.mu.Unlock()
		return
	}
	known := false
	for _, existing := range b.pages {
		if existing == page {
			known = true
			break
		}
	}
	if !known {
		if len(b.pages) >= browserPageLimit {
			b.mu.Unlock()
			_ = page.Close()
			return
		}
		b.pages = append(b.pages, page)
	}
	if activate {
		b.pwPage = page
	}
	b.mu.Unlock()
	// The context's page event can register a page before the context is
	// stored; the association installs on the first registration that has it.
	// Close and crash handlers come first: installing the association makes
	// blocking protocol calls, and a popup can close while they run.
	if !known {
		b.observePageWebSockets(page)
		page.OnClose(func(closed playwright.Page) { b.censorDocumentEvidence(closed, "context_closed"); b.removePage(closed) })
		page.OnCrash(func(crashed playwright.Page) { b.censorDocumentEvidence(crashed, "crash") })
	}
	b.mu.Lock()
	generation := b.evidence.generation
	b.mu.Unlock()
	b.installResourceAssociation(b.currentBrowserContext(), page, generation)
	if activate {
		b.installCDPSession(b.currentBrowserContext(), page)
	}
}

func (b *Browser) removePage(page playwright.Page) {
	b.mu.Lock()
	delete(b.evidence.shapedPages, page)
	delete(b.evidence.cdpPages, page)
	for i, existing := range b.pages {
		if existing != page {
			continue
		}
		b.pages = append(b.pages[:i], b.pages[i+1:]...)
		break
	}
	activeChanged := false
	if b.pwPage == page {
		b.pwPage = nil
		if len(b.pages) > 0 {
			b.pwPage = b.pages[len(b.pages)-1]
		}
		activeChanged = true
	}
	staleCDP := playwright.CDPSession(nil)
	if b.pwCDPPage == page {
		staleCDP = b.pwCDP
		b.pwCDP = nil
		b.pwCDPPage = nil
	}
	if activeChanged || staleCDP != nil {
		// Invalidate a NewCDPSession call that may still be in flight for the
		// closed page. Its commit path will detach the stale result.
		b.cdpInstallGeneration++
	}
	fallback := b.pwPage
	browserContext := b.pwContext
	needsFallbackCDP := fallback != nil && b.pwCDPPage != fallback
	b.mu.Unlock()

	if staleCDP != nil {
		_ = staleCDP.Detach()
	}
	if needsFallbackCDP {
		b.installCDPSession(browserContext, fallback)
	}
}

func (b *Browser) recordNetworkRequest(request playwright.Request, finished bool) {
	b.recordNetworkTask(browserNetworkTask{request: request, finished: finished})
}
func (b *Browser) recordNetworkTask(task browserNetworkTask) {
	request, finished := task.request, task.finished
	b.mu.Lock()
	current := b.networkTaskCurrentLocked(task)
	b.mu.Unlock()
	if !current {
		return
	}
	statusCode := 0
	var responseHeaders map[string]string
	var nativeResponse playwright.Response
	if response, err := request.Response(); err == nil && response != nil {
		nativeResponse = response
		statusCode = response.Status()
		responseHeaders = safeResponseHeaders(response)
		b.rememberResourceValidator(request.URL(), responseHeaders, task.generation)
	}
	requestHeaders := safeRequestHeaders(request)
	requestBody, requestBodyEncoding, requestBodyTruncated := browserRequestBody(request)
	var bytesReceived int64
	var nativeSizes *playwright.RequestSizesResult
	if sizes, err := request.Sizes(); err == nil && sizes != nil {
		nativeSizes = sizes
		bytesReceived = int64(sizes.ResponseHeadersSize + sizes.ResponseBodySize)
		rawBytes := int64(sizes.RequestHeadersSize + sizes.RequestBodySize + sizes.ResponseHeadersSize + sizes.ResponseBodySize)
		b.noteHARBytes(rawBytes*2, task.generation)
	}
	durationMs := requestDurationMs(request.Timing())
	if !finished && durationMs < 0 {
		durationMs = 0
	}
	var auraBatch *protocol.AuraBatch
	if finished && auraRequest(request) {
		auraBatch = summarizeAuraRequest(request, nativeResponse, nativeSizes)
	}
	entry := boundBrowserNetworkEntry(protocol.NetworkEntry{
		BrowserEvidence:      b.associateResourceEvidence(populateResourceEvidence(task.evidence, request, nativeResponse, nativeSizes), task.join, task.generation),
		FailureKind:          browserNetworkFailureKind(finished, statusCode),
		URL:                  request.URL(),
		Method:               request.Method(),
		StatusCode:           statusCode,
		ResourceType:         request.ResourceType(),
		DurationMs:           durationMs,
		BytesReceived:        bytesReceived,
		RequestHeaders:       requestHeaders,
		RequestBody:          requestBody,
		RequestBodyEncoding:  requestBodyEncoding,
		RequestBodyTruncated: requestBodyTruncated,
		ResponseHeaders:      responseHeaders,
		Aura:                 auraBatch,
	})
	b.mu.Lock()
	if b.networkTaskCurrentLocked(task) {
		b.appendNetworkEntryLocked(entry)
	}
	b.mu.Unlock()
}

// The failed-request event is the evidence for a transport failure. A missing
// response/status alone is not proof of failure, and raw browser errors can
// contain URLs or credentials and must never become a diagnostic label.
func browserNetworkFailureKind(finished bool, statusCode int) string {
	// Browsers can fire a failed-request event for rejected HTTP resources too.
	// Preserve the observed HTTP failure instead of calling it transport-only.
	if statusCode >= 400 {
		return "http"
	}
	if !finished {
		return "transport"
	}
	return ""
}

func (b *Browser) appendNetworkEntryLocked(entry protocol.NetworkEntry) {
	b.networkEntries = append(b.networkEntries, entry)
	b.networkBodyBytes += len(entry.RequestBody)
	b.networkMetadataBytes += browserNetworkEntryMetadataBytes(entry)
	b.trimNetworkEntriesLocked()
}

// trimNetworkEntriesLocked evicts the oldest network entries once the count or
// retained-body-byte budget is exceeded, keeping at least one entry. It
// reallocates the retained slice so the evicted entries (and their large bodies)
// become unreachable and collectable, and advances networkEntriesDropped so the
// absolute offsets used by eventOffsets/networkEntriesSince stay valid. Callers
// must hold b.mu.
func (b *Browser) trimNetworkEntriesLocked() {
	keepFrom := 0
	for keepFrom < len(b.networkEntries) {
		overCount := len(b.networkEntries)-keepFrom > browserNetworkEntryRingLimit
		overBodyBytes := b.networkBodyBytes > browserNetworkBodyBudgetBytes && len(b.networkEntries)-keepFrom > 1
		overMetadataBytes := b.networkMetadataBytes > browserNetworkMetadataBudgetBytes && len(b.networkEntries)-keepFrom > 1
		if !overCount && !overBodyBytes && !overMetadataBytes {
			break
		}
		b.networkBodyBytes -= len(b.networkEntries[keepFrom].RequestBody)
		b.networkMetadataBytes -= browserNetworkEntryMetadataBytes(b.networkEntries[keepFrom])
		keepFrom++
	}
	if keepFrom == 0 {
		return
	}
	retained := len(b.networkEntries) - keepFrom
	compacted := make([]protocol.NetworkEntry, retained)
	copy(compacted, b.networkEntries[keepFrom:])
	b.networkEntries = compacted
	b.networkEntriesDropped += keepFrom
}

func boundBrowserNetworkEntry(entry protocol.NetworkEntry) protocol.NetworkEntry {
	entry.BrowserEvidence = entry.BrowserEvidence.Clone()
	entry.URL = boundedtext.TruncateUTF8(entry.URL, browserNetworkURLLimitBytes)
	entry.Method = boundedtext.TruncateUTF8(entry.Method, 128)
	entry.ResourceType = boundedtext.TruncateUTF8(entry.ResourceType, 128)
	entry.RequestBodyEncoding = boundedtext.TruncateUTF8(entry.RequestBodyEncoding, 64)
	entry.RequestHeaders = boundedBrowserNetworkHeaders(entry.RequestHeaders)
	entry.ResponseHeaders = boundedBrowserNetworkHeaders(entry.ResponseHeaders)
	return entry
}

func boundedBrowserNetworkHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > browserNetworkHeaderEntriesLimit {
		keys = keys[:browserNetworkHeaderEntriesLimit]
	}
	bounded := make(map[string]string, len(keys))
	remaining := browserNetworkHeaderBytesLimit
	for _, originalKey := range keys {
		const entryOverhead = 64
		if remaining <= entryOverhead {
			break
		}
		key := boundedtext.TruncateUTF8(originalKey, min(browserNetworkHeaderNameLimit, remaining-entryOverhead))
		if key == "" {
			continue
		}
		remaining -= entryOverhead + len(key)
		if remaining <= 0 {
			break
		}
		value := boundedtext.TruncateUTF8(headers[originalKey], min(browserNetworkHeaderValueLimit, remaining))
		remaining -= len(value)
		if _, exists := bounded[key]; exists {
			continue
		}
		bounded[key] = value
	}
	if len(bounded) == 0 {
		return nil
	}
	return bounded
}

func browserNetworkEntryMetadataBytes(entry protocol.NetworkEntry) int {
	bytes := 256 + len(entry.URL) + len(entry.Method) + len(entry.ResourceType) + len(entry.RequestBodyEncoding)
	if entry.BrowserEvidence != nil {
		bytes += 4096
	}
	for key, value := range entry.RequestHeaders {
		bytes += 64 + len(key) + len(value)
	}
	for key, value := range entry.ResponseHeaders {
		bytes += 64 + len(key) + len(value)
	}
	if entry.Aura != nil {
		for _, action := range entry.Aura.Actions {
			bytes += 32 + len(action.Name) + len(action.State)
		}
	}
	return bytes
}

func safeRequestHeaders(request playwright.Request) (headers map[string]string) {
	defer func() {
		if recover() != nil {
			headers = nil
		}
	}()
	if request == nil {
		return nil
	}
	return boundedBrowserNetworkHeaders(request.Headers())
}

func safeResponseHeaders(response playwright.Response) (headers map[string]string) {
	defer func() {
		if recover() != nil {
			headers = nil
		}
	}()
	if response == nil {
		return nil
	}
	return boundedBrowserNetworkHeaders(response.Headers())
}

func browserRequestBody(request playwright.Request) (string, string, bool) {
	body, err := request.PostDataBuffer()
	if err != nil || len(body) == 0 {
		text, textErr := request.PostData()
		if textErr != nil || text == "" {
			return "", "", false
		}
		if len(text) > browserNetworkBodyCaptureLimitBytes {
			return text[:browserNetworkBodyCaptureLimitBytes], "", true
		}
		return text, "", false
	}
	truncated := false
	if len(body) > browserNetworkBodyCaptureLimitBytes {
		body = body[:browserNetworkBodyCaptureLimitBytes]
		truncated = true
	}
	if utf8.Valid(body) {
		return string(body), "", truncated
	}
	return base64.StdEncoding.EncodeToString(body), "base64", truncated
}

func requestDurationMs(timing *playwright.RequestTiming) float64 {
	if timing == nil {
		return 0
	}
	switch {
	case timing.ResponseEnd >= 0:
		return timing.ResponseEnd
	case timing.ResponseStart >= 0:
		return timing.ResponseStart
	default:
		return 0
	}
}

func (b *Browser) installCDPSession(browserContext playwright.BrowserContext, page playwright.Page) {
	if browserContext == nil || page == nil {
		return
	}
	if !b.supportsCDP() || (b.cpuThrottleRate <= 1.0 && !b.clientMetricsEnabled && !b.browserNetworkSimulationActive()) {
		return
	}
	b.mu.Lock()
	if b.closing || b.pwPage != page {
		b.mu.Unlock()
		return
	}
	if b.pwCDP != nil && b.pwCDPPage == page {
		b.mu.Unlock()
		return
	}
	b.cdpInstallGeneration++
	generation := b.cdpInstallGeneration
	createCDPSession := b.createCDPSession
	b.mu.Unlock()
	var (
		cdp playwright.CDPSession
		err error
	)
	if createCDPSession != nil {
		cdp, err = createCDPSession(browserContext, page)
	} else {
		cdp, err = browserContext.NewCDPSession(page)
	}
	if err != nil || cdp == nil {
		return
	}
	// b.pwCDP is read by the action goroutine (captureRuntimeMetrics) and close();
	// registerPage runs this on Playwright's OnPage dispatch goroutine, so guard
	// the write with b.mu (registerPage already released b.mu before calling here).
	b.mu.Lock()
	if b.closing || b.pwPage != page || b.cdpInstallGeneration != generation {
		b.mu.Unlock()
		_ = cdp.Detach()
		return
	}
	previous := b.pwCDP
	b.pwCDP = cdp
	b.pwCDPPage = page
	b.mu.Unlock()
	if previous != nil && previous != cdp {
		_ = previous.Detach()
	}
	applied := true
	if b.cpuThrottleRate > 1.0 {
		_, err := cdp.Send("Emulation.setCPUThrottlingRate", map[string]any{"rate": b.cpuThrottleRate})
		applied = err == nil
	}
	applied = b.applyCDPNetworkConditions(cdp) && applied
	b.mu.Lock()
	if b.evidence.shapedPages != nil && b.pwCDP == cdp && b.pwCDPPage == page && b.cdpInstallGeneration == generation {
		b.evidence.shapedPages[page] = applied
	}
	b.mu.Unlock()
}

func (b *Browser) supportsCDP() bool {
	return b.engine == "chromium" || b.engine == "chrome"
}

func (b *Browser) browserNetworkSimulationActive() bool {
	return b.networkLatencyMS > 0 || b.networkBandwidthKB > 0
}

func (b *Browser) applyCDPNetworkConditions(cdp playwright.CDPSession) bool {
	if !b.browserNetworkSimulationActive() {
		return true
	}
	if cdp == nil {
		return false
	}
	if _, err := cdp.Send("Network.enable", map[string]any{}); err != nil {
		return false
	}
	throughput := -1.0
	if b.networkBandwidthKB > 0 {
		throughput = float64(b.networkBandwidthKB*1024) / 8.0
	}
	_, err := cdp.Send("Network.emulateNetworkConditions", map[string]any{
		"offline":            false,
		"latency":            b.networkLatencyMS,
		"downloadThroughput": throughput,
		"uploadThroughput":   throughput,
		"connectionType":     browserNetworkConnectionType(b.networkPreset),
	})
	return err == nil
}

func browserNetworkConnectionType(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "56k", "2g":
		return "cellular2g"
	case "dsl", "3g":
		return "cellular3g"
	case "4g", "10mbps", "t1":
		return "cellular4g"
	default:
		return "other"
	}
}
