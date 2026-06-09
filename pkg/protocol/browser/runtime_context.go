package browser

import (
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

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
		_ = dialog.Dismiss()
	})
	browserContext.OnResponse(func(response playwright.Response) {
		b.rememberResourceValidator(response.URL(), response.Headers())
	})
	browserContext.OnRequestFinished(func(request playwright.Request) {
		go b.recordNetworkRequest(request, true)
	})
	browserContext.OnRequestFailed(func(request playwright.Request) {
		go b.recordNetworkRequest(request, false)
	})
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
	if activate {
		b.installCDPSession(b.pwContext, page)
	}
}

func (b *Browser) recordNetworkRequest(request playwright.Request, finished bool) {
	statusCode := 0
	if response, err := request.Response(); err == nil && response != nil {
		statusCode = response.Status()
		b.rememberResourceValidator(request.URL(), response.Headers())
	}
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
		URL:           request.URL(),
		Method:        request.Method(),
		StatusCode:    statusCode,
		ResourceType:  request.ResourceType(),
		DurationMs:    durationMs,
		BytesReceived: bytesReceived,
	})
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
	b.pwCDP = cdp
	if b.cpuThrottleRate > 1.0 {
		_, _ = cdp.Send("Emulation.setCPUThrottlingRate", map[string]any{"rate": b.cpuThrottleRate})
	}
}
