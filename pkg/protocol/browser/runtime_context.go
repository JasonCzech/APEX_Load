package browser

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

const browserNetworkBodyCaptureLimitBytes = 512 * 1024

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
	b.registerPage(page, true)
	return nil
}

func (b *Browser) browserContextOptions() playwright.BrowserNewContextOptions {
	contextOptions := playwright.BrowserNewContextOptions{
		HasTouch: playwright.Bool(b.touchMode),
		IsMobile: playwright.Bool(b.mobile),
		Viewport: &playwright.Size{Width: b.viewportW, Height: b.viewportH},
	}
	if b.userAgent != "" {
		contextOptions.UserAgent = playwright.String(b.userAgent)
	}
	b.applyHARCaptureOptions(&contextOptions)
	return contextOptions
}

func (b *Browser) installBrowserContextHandlers(browserContext playwright.BrowserContext) {
	browserContext.OnPage(func(page playwright.Page) {
		b.registerPage(page, true)
	})
	browserContext.OnConsole(func(message playwright.ConsoleMessage) {
		source := ""
		if location := message.Location(); location != nil {
			source = location.URL
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.consoleEntries = append(b.consoleEntries, protocol.ConsoleEntry{
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
		go b.recordNetworkRequest(request, true)
	})
	browserContext.OnRequestFailed(func(request playwright.Request) {
		go b.recordNetworkRequest(request, false)
	})
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
		b.pages = append(b.pages, page)
	}
	if activate {
		b.pwPage = page
	}
	b.mu.Unlock()
	if !known {
		b.observePageWebSockets(page)
	}
	if activate {
		b.installCDPSession(b.pwContext, page)
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
	if b.engine != "chromium" || (b.cpuThrottleRate <= 1.0 && !b.clientMetricsEnabled) {
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
}
