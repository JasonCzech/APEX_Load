package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

const playwrightGoVersion = "v0.5700.1"

func WebKitInstallCommand() string {
	return "go run github.com/playwright-community/playwright-go/cmd/playwright@" + playwrightGoVersion + " install webkit"
}

func WebKitInstallWarning() string {
	return fmt.Sprintf("WebKit execution requires Playwright WebKit assets installed on the controller host. Run %s before executing WebKit browser tests or replay sessions.", WebKitInstallCommand())
}

func (b *Browser) initialized() bool {
	if b.engine == "webkit" {
		return b.pwPage != nil
	}
	return b.browserCtx != nil
}

func (b *Browser) initPlaywright(_ context.Context) error {
	pw, err := playwright.Run()
	if err != nil {
		return wrapPlaywrightError(err)
	}

	browser, err := pw.WebKit.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(b.headless),
		Timeout:  playwright.Float(30000),
	})
	if err != nil {
		_ = pw.Stop()
		return wrapPlaywrightError(err)
	}

	contextOptions := playwright.BrowserNewContextOptions{
		HasTouch: playwright.Bool(b.touchMode),
		IsMobile: playwright.Bool(b.mobile),
		Viewport: &playwright.Size{Width: b.viewportW, Height: b.viewportH},
	}
	if b.userAgent != "" {
		contextOptions.UserAgent = playwright.String(b.userAgent)
	}

	browserContext, err := browser.NewContext(contextOptions)
	if err != nil {
		_ = browser.Close()
		_ = pw.Stop()
		return wrapPlaywrightError(err)
	}
	if b.clientMetricsEnabled {
		if err := browserContext.AddInitScript(playwright.Script{Content: playwright.String(browserMetricsBootstrapScript)}); err != nil {
			_ = browserContext.Close()
			_ = browser.Close()
			_ = pw.Stop()
			return wrapPlaywrightError(err)
		}
	}

	browserContext.OnConsole(func(message playwright.ConsoleMessage) {
		b.recordPlaywrightConsole(message)
	})
	browserContext.OnDialog(func(dialog playwright.Dialog) {
		_ = dialog.Dismiss()
	})
	browserContext.OnRequestFinished(func(request playwright.Request) {
		b.recordPlaywrightRequest(request, true)
	})
	browserContext.OnRequestFailed(func(request playwright.Request) {
		b.recordPlaywrightRequest(request, false)
	})

	page, err := browserContext.NewPage()
	if err != nil {
		_ = browserContext.Close()
		_ = browser.Close()
		_ = pw.Stop()
		return wrapPlaywrightError(err)
	}

	b.pw = pw
	b.pwBrowser = browser
	b.pwContext = browserContext
	b.pwPage = page
	return nil
}

func wrapPlaywrightError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	return fmt.Errorf("webkit runtime unavailable: %s. %s", message, WebKitInstallWarning())
}

func (b *Browser) navigate(ctx context.Context, target string) error {
	if b.engine == "webkit" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := b.pwPage.Goto(target); err != nil {
			return err
		}
		return b.waitVisible(ctx, "body")
	}
	return chromedp.Run(ctx,
		chromedp.Navigate(target),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
}

func (b *Browser) click(ctx context.Context, selector string) error {
	if b.engine == "webkit" {
		if err := b.waitVisible(ctx, selector); err != nil {
			return err
		}
		return b.pwPage.Click(selector)
	}
	return chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	)
}

func (b *Browser) fill(ctx context.Context, selector, value string) error {
	if b.engine == "webkit" {
		if err := b.waitVisible(ctx, selector); err != nil {
			return err
		}
		return b.pwPage.Fill(selector, value)
	}
	return chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.SetValue(selector, value, chromedp.ByQuery),
	)
}

func (b *Browser) waitVisible(ctx context.Context, selector string) error {
	if b.engine == "webkit" {
		return b.waitForPlaywrightSelectorState(ctx, selector, true)
	}
	return chromedp.Run(ctx, chromedp.WaitVisible(selector, chromedp.ByQuery))
}

func (b *Browser) waitHidden(ctx context.Context, selector string) error {
	if b.engine == "webkit" {
		return b.waitForPlaywrightSelectorState(ctx, selector, false)
	}
	return chromedp.Run(ctx, chromedp.WaitNotPresent(selector, chromedp.ByQuery))
}

func (b *Browser) waitForPlaywrightSelectorState(ctx context.Context, selector string, wantVisible bool) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	expression := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) {
			return %t;
		}
		const style = window.getComputedStyle(el);
		const visible = style.display !== 'none' && style.visibility !== 'hidden' && Number(style.opacity || '1') !== 0;
		return %t ? visible : !visible;
	})()`, selector, !wantVisible, wantVisible)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var matched bool
		if err := b.evaluatePageExpression(ctx, expression, &matched); err == nil && matched {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *Browser) evaluatePageExpression(ctx context.Context, expression string, out any) error {
	if b.engine == "webkit" {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := b.pwPage.Evaluate(expression)
		if err != nil {
			return err
		}
		return decodePlaywrightValue(value, out)
	}
	return chromedp.Run(ctx, chromedp.Evaluate(expression, out))
}

func decodePlaywrightValue(value any, out any) error {
	if out == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (b *Browser) readPlaywrightDOMSummary(ctx context.Context) (string, string, *protocol.DOMSummary, error) {
	if err := ctx.Err(); err != nil {
		return "", "", nil, err
	}
	var state domState
	if err := b.evaluatePageExpression(ctx, `(() => ({ readyState: document.readyState, visible: (document.body?.innerText || '').split(/\n+/).map((line) => line.trim()).filter(Boolean).slice(0, 10) }))()`, &state); err != nil {
		return "", "", nil, err
	}
	title, err := b.pwPage.Title()
	if err != nil {
		return "", "", nil, err
	}
	pageURL := b.pwPage.URL()
	return pageURL, title, &protocol.DOMSummary{
		Title:       title,
		ReadyState:  state.ReadyState,
		VisibleText: state.Visible,
	}, nil
}

func (b *Browser) capturePlaywrightHTML(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var html string
	err := b.evaluatePageExpression(ctx, `document.documentElement ? document.documentElement.outerHTML : ''`, &html)
	return html, err
}

func (b *Browser) capturePlaywrightScreenshot(ctx context.Context, _ *protocol.Action) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	buf, err := b.pwPage.Screenshot()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func (b *Browser) recordPlaywrightConsole(message playwright.ConsoleMessage) {
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
}

func (b *Browser) recordPlaywrightRequest(request playwright.Request, finished bool) {
	statusCode := 0
	if response, err := request.Response(); err == nil && response != nil {
		statusCode = response.Status()
	}
	var bytesReceived int64
	if sizes, err := request.Sizes(); err == nil && sizes != nil {
		bytesReceived = int64(sizes.ResponseHeadersSize + sizes.ResponseBodySize)
	}
	durationMs := 0.0
	if timing := request.Timing(); timing != nil {
		switch {
		case timing.ResponseEnd >= 0:
			durationMs = timing.ResponseEnd
		case timing.ResponseStart >= 0:
			durationMs = timing.ResponseStart
		}
	}
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

func (b *Browser) closePlaywright() {
	if b.pwPage != nil {
		_ = b.pwPage.Close()
		b.pwPage = nil
	}
	if b.pwContext != nil {
		_ = b.pwContext.Close()
		b.pwContext = nil
	}
	if b.pwBrowser != nil {
		_ = b.pwBrowser.Close()
		b.pwBrowser = nil
	}
	if b.pw != nil {
		_ = b.pw.Stop()
		b.pw = nil
	}
}