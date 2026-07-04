package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
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
	return page.Content()
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
			buf, err := el.Screenshot(playwright.ElementHandleScreenshotOptions{
				Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
			})
			return base64.StdEncoding.EncodeToString(buf), err
		}
	}
	if action != nil && action.Capture != nil && action.Capture.FullPage {
		opts.FullPage = playwright.Bool(true)
	}
	opts.Timeout = playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action)))
	buf, err := page.Screenshot(opts)
	return base64.StdEncoding.EncodeToString(buf), err
}

func (b *Browser) readDOMSummary(ctx context.Context) (string, string, *protocol.DOMSummary, error) {
	if err := ctx.Err(); err != nil {
		return "", "", nil, err
	}
	page := b.currentPage()
	if page == nil {
		return "", "", nil, fmt.Errorf("browser page is not initialized")
	}
	res, err := page.Evaluate(`(() => ({
		url: window.location.href,
		title: document.title,
		readyState: document.readyState,
		visible: (document.body?.innerText || '').split(/\n+/).map((line) => line.trim()).filter(Boolean).slice(0, 10)
	}))()`)
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

	visibleList, _ := data["visible"].([]any)
	visibleText := []string{}
	for _, v := range visibleList {
		if s, ok := v.(string); ok {
			visibleText = append(visibleText, s)
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
