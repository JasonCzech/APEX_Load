package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

const browserNetworkBodyCaptureLimitBytes = 512 * 1024

// Bounds on the per-session network-entry buffer. Without these the buffer grows
// for the whole browser session (reset only at Close), retaining full headers and
// up to 512KiB request bodies each — an unbounded memory leak on long replay
// sessions. We cap both the entry count and the total retained body bytes,
// evicting the oldest entries first (the sibling wsEvents buffer is similarly
// ring-bounded).
const (
	browserNetworkEntryRingLimit  = 2048
	browserNetworkBodyBudgetBytes = 32 * 1024 * 1024
	browserConsoleEntryRingLimit  = 2048
	browserConsoleTextLimitBytes  = 4096
	browserPageLimit              = 32
	browserNetworkWorkerCount     = 4
	browserNetworkTaskQueueSize   = 256
)

type browserNetworkTask struct {
	request  playwright.Request
	finished bool
}

func (b *Browser) openBrowserContext() error {
	pwBrowser, err := getBrowserInstance(b.engine, b.headless)
	if err != nil {
		return err
	}

	browserContext, err := pwBrowser.NewContext(b.browserContextOptions())
	if err != nil {
		return err
	}
	if err := b.installRequestPolicy(browserContext); err != nil {
		_ = browserContext.Close()
		return err
	}
	if b.clientMetricsEnabled {
		if err := browserContext.AddInitScript(playwright.Script{Content: playwright.String(browserMetricsBootstrapScript)}); err != nil {
			_ = browserContext.Close()
			return err
		}
	}

	b.installBrowserContextHandlers(browserContext)

	page, err := browserContext.NewPage()
	if err != nil {
		_ = browserContext.Close()
		return err
	}

	b.pwContext = browserContext
	b.startHARMonitor()
	b.startNetworkWorkers()
	b.registerPage(page, true)
	return nil
}

func (b *Browser) browserContextOptions() playwright.BrowserNewContextOptions {
	viewport := b.browserViewport()
	deviceScaleFactor := b.deviceScaleFactor
	if deviceScaleFactor <= 0 {
		deviceScaleFactor = 1
	}
	contextOptions := playwright.BrowserNewContextOptions{
		AcceptDownloads:   playwright.Bool(true),
		DeviceScaleFactor: playwright.Float(deviceScaleFactor),
		HasTouch:          playwright.Bool(b.touchMode),
		IsMobile:          playwright.Bool(b.mobile),
		Screen:            viewport,
		Viewport:          viewport,
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
	b.applyHARCaptureOptions(&contextOptions)
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
	browserContext.OnPage(func(page playwright.Page) {
		// Popups do not auto-activate by default; a script switches explicitly
		// with browser_switch_window. browser_auto_activate_popups restores the
		// old auto-switch behavior for scripts recorded against it.
		b.registerPage(page, b.autoActivatePopups)
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
		})
	})
	browserContext.OnDialog(func(dialog playwright.Dialog) {
		// A browser_handle_dialog action arms a one-shot policy for the next
		// dialog; without one we dismiss so the page never hangs.
		b.dialogMu.Lock()
		policy := b.pendingDialog
		b.pendingDialog = nil
		b.dialogMu.Unlock()
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
		b.rememberResourceValidator(response.URL(), safeResponseHeaders(response))
	})
	browserContext.OnRequestFinished(func(request playwright.Request) {
		b.enqueueNetworkRequest(request, true)
	})
	browserContext.OnRequestFailed(func(request playwright.Request) {
		b.enqueueNetworkRequest(request, false)
	})
}

func (b *Browser) appendConsoleEntry(entry protocol.ConsoleEntry) {
	if len(entry.Text) > browserConsoleTextLimitBytes {
		entry.Text = entry.Text[:browserConsoleTextLimitBytes]
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closing {
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
					b.recordNetworkRequest(task.request, task.finished)
				}
			}
		}()
	}
}

func (b *Browser) enqueueNetworkRequest(request playwright.Request, finished bool) {
	b.mu.Lock()
	if b.closing || b.networkTasks == nil || b.networkWorkerStop == nil {
		b.mu.Unlock()
		return
	}
	tasks := b.networkTasks
	stop := b.networkWorkerStop
	b.mu.Unlock()
	select {
	case tasks <- browserNetworkTask{request: request, finished: finished}:
	case <-stop:
	default:
		b.mu.Lock()
		b.networkTasksDropped++
		b.mu.Unlock()
	}
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

func (b *Browser) registerPage(page playwright.Page, activate bool) {
	if page == nil {
		return
	}
	b.mu.Lock()
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
	if !known {
		b.observePageWebSockets(page)
		page.OnClose(func(closed playwright.Page) { b.removePage(closed) })
	}
	if activate {
		b.installCDPSession(b.pwContext, page)
	}
}

func (b *Browser) removePage(page playwright.Page) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, existing := range b.pages {
		if existing != page {
			continue
		}
		b.pages = append(b.pages[:i], b.pages[i+1:]...)
		break
	}
	if b.pwPage == page {
		b.pwPage = nil
		if len(b.pages) > 0 {
			b.pwPage = b.pages[len(b.pages)-1]
		}
	}
}

func (b *Browser) recordNetworkRequest(request playwright.Request, finished bool) {
	statusCode := 0
	var responseHeaders map[string]string
	if response, err := request.Response(); err == nil && response != nil {
		statusCode = response.Status()
		responseHeaders = safeResponseHeaders(response)
		b.rememberResourceValidator(request.URL(), responseHeaders)
	}
	requestHeaders := safeRequestHeaders(request)
	requestBody, requestBodyEncoding, requestBodyTruncated := browserRequestBody(request)
	var bytesReceived int64
	if sizes, err := request.Sizes(); err == nil && sizes != nil {
		bytesReceived = int64(sizes.ResponseHeadersSize + sizes.ResponseBodySize)
	}
	durationMs := requestDurationMs(request.Timing())
	if !finished && durationMs < 0 {
		durationMs = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.networkEntries = append(b.networkEntries, protocol.NetworkEntry{
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
	})
	b.networkBodyBytes += len(requestBody)
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
		overBytes := b.networkBodyBytes > browserNetworkBodyBudgetBytes && len(b.networkEntries)-keepFrom > 1
		if !overCount && !overBytes {
			break
		}
		b.networkBodyBytes -= len(b.networkEntries[keepFrom].RequestBody)
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

func safeRequestHeaders(request playwright.Request) (headers map[string]string) {
	defer func() {
		if recover() != nil {
			headers = nil
		}
	}()
	if request == nil {
		return nil
	}
	return request.Headers()
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
	return response.Headers()
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
	cdp, err := browserContext.NewCDPSession(page)
	if err != nil {
		return
	}
	// b.pwCDP is read by the action goroutine (captureRuntimeMetrics) and close();
	// registerPage runs this on Playwright's OnPage dispatch goroutine, so guard
	// the write with b.mu (registerPage already released b.mu before calling here).
	b.mu.Lock()
	b.pwCDP = cdp
	b.mu.Unlock()
	if b.cpuThrottleRate > 1.0 {
		_, _ = cdp.Send("Emulation.setCPUThrottlingRate", map[string]any{"rate": b.cpuThrottleRate})
	}
	b.applyCDPNetworkConditions(cdp)
}

func (b *Browser) supportsCDP() bool {
	return b.engine == "chromium" || b.engine == "chrome"
}

func (b *Browser) browserNetworkSimulationActive() bool {
	return b.networkLatencyMS > 0 || b.networkBandwidthKB > 0
}

func (b *Browser) applyCDPNetworkConditions(cdp playwright.CDPSession) {
	if cdp == nil || !b.browserNetworkSimulationActive() {
		return
	}
	_, _ = cdp.Send("Network.enable", map[string]any{})
	throughput := -1.0
	if b.networkBandwidthKB > 0 {
		throughput = float64(b.networkBandwidthKB*1024) / 8.0
	}
	_, _ = cdp.Send("Network.emulateNetworkConditions", map[string]any{
		"offline":            false,
		"latency":            b.networkLatencyMS,
		"downloadThroughput": throughput,
		"uploadThroughput":   throughput,
		"connectionType":     browserNetworkConnectionType(b.networkPreset),
	})
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
