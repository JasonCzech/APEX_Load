package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

const (
	// Artifact capture happens before the engine's retained-result projection.
	// Keep the acquisition itself bounded so a hostile page cannot make the
	// generator materialize an arbitrary-size Playwright response first.
	browserHTMLCaptureLimitBytes        = 4 << 20
	browserHTMLCaptureLimitCodeUnits    = 4 << 20
	browserScreenshotLimitBytes         = 8 << 20
	browserScreenshotDimensionLimit     = 16_384
	browserScreenshotPixelLimit         = 16 << 20
	browserDOMURLLimitBytes             = 16 << 10
	browserDOMTitleLimitBytes           = 4 << 10
	browserDOMReadyStateLimitBytes      = 128
	browserDOMVisibleEntryLimit         = 10
	browserDOMVisibleLineLimitBytes     = 16 << 10
	browserDOMVisibleAggregateLimitByte = 128 << 10
)

func (b *Browser) resolveURL(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if b.baseURL == "" {
		return target
	}
	return strings.TrimRight(b.baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

func (b *Browser) captureRequestedArtifacts(ctx context.Context, action *protocol.Action) (string, string, error) {
	captureEveryAction := b.captureScreenshots == "per_action"
	if action.Type != "browser_screenshot" && action.Capture == nil && !captureEveryAction {
		return "", "", nil
	}

	var html string
	var screenshot string
	var err error
	if captureEveryAction || action.Type == "browser_screenshot" || (action.Capture != nil && action.Capture.Screenshot) {
		screenshot, err = b.captureScreenshot(ctx, action)
		if err != nil {
			return "", "", err
		}
	}
	if action.Capture != nil && action.Capture.HTML {
		html, err = b.captureHTML(ctx)
		if err != nil {
			return "", "", err
		}
	}
	return html, screenshot, nil
}

func (b *Browser) capturePageState(ctx context.Context, action *protocol.Action, forceArtifacts bool) (string, string, *protocol.DOMSummary, string, string, error) {
	pageURL, pageTitle, domSummary, err := b.readDOMSummary(ctx)
	if err != nil {
		return "", "", nil, "", "", err
	}

	html, screenshot, err := b.captureRequestedArtifacts(ctx, action)
	if err != nil {
		return pageURL, pageTitle, domSummary, html, screenshot, err
	}
	if forceArtifacts {
		if html == "" {
			html, _ = b.captureHTML(ctx)
		}
		if screenshot == "" {
			screenshot, _ = b.captureScreenshot(ctx, action)
		}
	}
	return pageURL, pageTitle, domSummary, html, screenshot, nil
}

func (b *Browser) navigate(ctx context.Context, action *protocol.Action, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	resolvedTarget := target
	if b.baseURL != "" && strings.HasPrefix(target, "/") {
		resolvedTarget = strings.TrimRight(b.baseURL, "/") + target
	} else if !strings.HasPrefix(target, "http") {
		resolvedTarget = "http://" + target
	}

	timeout := b.actionTimeout(action)
	if _, err := page.Goto(resolvedTarget, playwright.PageGotoOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
	}); err != nil {
		return err
	}
	b.currentURL = resolvedTarget
	return b.waitVisibleWithTimeout(ctx, "body", timeout)
}

func (b *Browser) click(ctx context.Context, action *protocol.Action, selector string) error {
	timeout := b.actionTimeout(action)
	if err := b.waitVisibleWithTimeout(ctx, selector, timeout); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	options := pageClickOptions(action)
	options.Timeout = playwright.Float(b.timeoutMS(ctx, timeout))
	return page.Click(selector, options)
}

func (b *Browser) fill(ctx context.Context, action *protocol.Action, selector, value string) error {
	timeout := b.actionTimeout(action)
	if err := b.waitVisibleWithTimeout(ctx, selector, timeout); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	return page.Fill(selector, value, playwright.PageFillOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
	})
}

func (b *Browser) waitVisible(ctx context.Context, selector string) error {
	return b.waitVisibleWithTimeout(ctx, selector, 10*time.Second)
}

func (b *Browser) waitVisibleWithTimeout(ctx context.Context, selector string, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	_, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
	})
	return err
}

func (b *Browser) waitHidden(ctx context.Context, selector string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	_, err := page.WaitForSelector(selector, playwright.PageWaitForSelectorOptions{
		State:   playwright.WaitForSelectorStateHidden,
		Timeout: playwright.Float(b.timeoutMS(ctx, 10*time.Second)),
	})
	return err
}

func (b *Browser) evaluatePageExpression(ctx context.Context, expression string, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	res, err := page.Evaluate(expression)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	data, _ := json.Marshal(res)
	return json.Unmarshal(data, result)
}

func (b *Browser) captureHTML(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	page := b.currentPage()
	if page == nil {
		return "", fmt.Errorf("browser page is not initialized")
	}
	// page.Content serializes the entire document into the Playwright transport
	// before the caller can enforce any result limit. Slice in the browser
	// process as well, then apply a byte-accurate UTF-8 cap in Go.
	res, err := page.Evaluate(fmt.Sprintf(`(() => {
		const html = document.documentElement?.outerHTML || '';
		return html.slice(0, %d);
	})()`, browserHTMLCaptureLimitCodeUnits))
	if err != nil {
		return "", err
	}
	html, ok := res.(string)
	if !ok {
		return "", fmt.Errorf("invalid HTML capture")
	}
	return boundedtext.TruncateUTF8(html, browserHTMLCaptureLimitBytes), nil
}

func (b *Browser) captureScreenshot(ctx context.Context, action *protocol.Action) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	opts := playwright.PageScreenshotOptions{}
	selector := b.screenshotSelector(action)
	page := b.currentPage()
	if page == nil {
		return "", fmt.Errorf("browser page is not initialized")
	}
	if selector != "" {
		el, err := page.QuerySelector(selector)
		if err != nil {
			return "", err
		}
		if el != nil {
			box, err := el.BoundingBox()
			if err != nil {
				return "", err
			}
			if box == nil {
				return "", fmt.Errorf("browser screenshot target is not visible")
			}
			if err := validateBrowserScreenshotDimensions(box.Width, box.Height); err != nil {
				return "", err
			}
			buf, err := el.Screenshot(playwright.ElementHandleScreenshotOptions{
				Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
				Scale:   playwright.ScreenshotScaleCss,
			})
			return encodeBrowserScreenshot(buf, err)
		}
	}
	if action != nil && action.Capture != nil && action.Capture.FullPage {
		width, height, err := browserFullPageDimensions(page)
		if err != nil {
			return "", err
		}
		if err := validateBrowserScreenshotDimensions(width, height); err != nil {
			return "", err
		}
		opts.FullPage = playwright.Bool(true)
	} else {
		viewport := b.browserViewport()
		if err := validateBrowserScreenshotDimensions(float64(viewport.Width), float64(viewport.Height)); err != nil {
			return "", err
		}
	}
	opts.Timeout = playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action)))
	opts.Scale = playwright.ScreenshotScaleCss
	buf, err := page.Screenshot(opts)
	return encodeBrowserScreenshot(buf, err)
}

func encodeBrowserScreenshot(buf []byte, captureErr error) (string, error) {
	if captureErr != nil {
		return "", captureErr
	}
	if len(buf) > browserScreenshotLimitBytes {
		return "", fmt.Errorf("browser screenshot exceeds %d-byte capture limit", browserScreenshotLimitBytes)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

func validateBrowserScreenshotDimensions(width, height float64) error {
	if math.IsNaN(width) || math.IsInf(width, 0) || math.IsNaN(height) || math.IsInf(height, 0) || width <= 0 || height <= 0 {
		return fmt.Errorf("browser screenshot has invalid dimensions")
	}
	if width > browserScreenshotDimensionLimit || height > browserScreenshotDimensionLimit || width*height > browserScreenshotPixelLimit {
		return fmt.Errorf("browser screenshot dimensions %.0fx%.0f exceed capture limit", width, height)
	}
	return nil
}

func browserFullPageDimensions(page playwright.Page) (float64, float64, error) {
	res, err := page.Evaluate(`(() => ({
		width: Math.max(document.documentElement?.scrollWidth || 0, document.body?.scrollWidth || 0),
		height: Math.max(document.documentElement?.scrollHeight || 0, document.body?.scrollHeight || 0)
	}))()`)
	if err != nil {
		return 0, 0, err
	}
	data, ok := res.(map[string]any)
	if !ok {
		return 0, 0, fmt.Errorf("invalid browser page dimensions")
	}
	width, widthOK := data["width"].(float64)
	height, heightOK := data["height"].(float64)
	if !widthOK || !heightOK {
		return 0, 0, fmt.Errorf("invalid browser page dimensions")
	}
	return width, height, nil
}

func (b *Browser) readDOMSummary(ctx context.Context) (string, string, *protocol.DOMSummary, error) {
	if err := ctx.Err(); err != nil {
		return "", "", nil, err
	}
	page := b.currentPage()
	if page == nil {
		return "", "", nil, fmt.Errorf("browser page is not initialized")
	}
	res, err := page.Evaluate(`(() => {
		const visible = [];
		const body = document.body;
		if (body) {
			const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
			let visited = 0;
			while (visible.length < 10 && visited < 4096) {
				const node = walker.nextNode();
				if (!node) break;
				visited++;
				const parent = node.parentElement;
				if (!parent) continue;
				const style = window.getComputedStyle(parent);
				if (style.display === 'none' || style.visibility === 'hidden') continue;
				const lines = (node.nodeValue || '').slice(0, 16384).split(/\n+/);
				for (const raw of lines) {
					const line = raw.trim();
					if (line) visible.push(line.slice(0, 16384));
					if (visible.length >= 10) break;
				}
			}
		}
		return {
			url: String(window.location.href || '').slice(0, 16384),
			title: String(document.title || '').slice(0, 4096),
			readyState: String(document.readyState || '').slice(0, 128),
			visible
		};
	})()`)
	if err != nil {
		return "", "", nil, err
	}
	data, ok := res.(map[string]any)
	if !ok {
		return "", "", nil, fmt.Errorf("invalid dom summary")
	}
	return domSummaryFromPageData(data)
}

func domSummaryFromPageData(data map[string]any) (string, string, *protocol.DOMSummary, error) {
	title, _ := data["title"].(string)
	url, _ := data["url"].(string)
	readyState, _ := data["readyState"].(string)
	title = boundedtext.TruncateUTF8(title, browserDOMTitleLimitBytes)
	url = boundedtext.TruncateUTF8(url, browserDOMURLLimitBytes)
	readyState = boundedtext.TruncateUTF8(readyState, browserDOMReadyStateLimitBytes)

	visibleList, _ := data["visible"].([]any)
	visibleText := make([]string, 0, min(len(visibleList), browserDOMVisibleEntryLimit))
	remaining := browserDOMVisibleAggregateLimitByte
	for index, v := range visibleList {
		if index >= browserDOMVisibleEntryLimit || remaining <= 0 {
			break
		}
		if s, ok := v.(string); ok {
			line := boundedtext.TruncateUTF8(s, min(browserDOMVisibleLineLimitBytes, remaining))
			remaining -= len(line)
			if line != "" {
				visibleText = append(visibleText, line)
			}
		}
	}

	return url, title, &protocol.DOMSummary{
		Title:       title,
		ReadyState:  readyState,
		VisibleText: visibleText,
	}, nil
}

func (b *Browser) selectOption(ctx context.Context, action *protocol.Action, selector string, values []string) error {
	timeout := b.actionTimeout(action)
	if err := b.waitVisibleWithTimeout(ctx, selector, timeout); err != nil {
		return err
	}
	page := b.currentPage()
	if page == nil {
		return fmt.Errorf("browser page is not initialized")
	}
	_, err := page.SelectOption(selector, playwright.SelectOptionValues{
		ValuesOrLabels: playwright.StringSlice(values...),
	}, playwright.PageSelectOptionOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
	})
	return err
}
