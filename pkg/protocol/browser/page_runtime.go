package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

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

func (b *Browser) navigate(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resolvedTarget := target
	if b.baseURL != "" && strings.HasPrefix(target, "/") {
		resolvedTarget = strings.TrimRight(b.baseURL, "/") + target
	} else if !strings.HasPrefix(target, "http") {
		resolvedTarget = "http://" + target
	}

	if _, err := b.currentPage().Goto(resolvedTarget); err != nil {
		return err
	}
	b.currentURL = resolvedTarget
	return b.waitVisible(ctx, "body")
}

func (b *Browser) click(ctx context.Context, selector string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}
	return b.currentPage().Click(selector)
}

func (b *Browser) fill(ctx context.Context, selector, value string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}
	return b.currentPage().Fill(selector, value)
}

func (b *Browser) waitVisible(ctx context.Context, selector string) error {
	_, err := b.currentPage().WaitForSelector(selector)
	return err
}

func (b *Browser) waitHidden(ctx context.Context, selector string) error {
	_, err := b.currentPage().WaitForSelector(selector, playwright.PageWaitForSelectorOptions{State: playwright.WaitForSelectorStateHidden})
	return err
}

func (b *Browser) evaluatePageExpression(ctx context.Context, expression string, result any) error {
	res, err := b.currentPage().Evaluate(expression)
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
	return b.currentPage().Content()
}

func (b *Browser) captureScreenshot(ctx context.Context, action *protocol.Action) (string, error) {
	opts := playwright.PageScreenshotOptions{}
	selector := b.screenshotSelector(action)
	if selector != "" {
		el, err := b.currentPage().QuerySelector(selector)
		if err != nil {
			return "", err
		}
		if el != nil {
			buf, err := el.Screenshot()
			return base64.StdEncoding.EncodeToString(buf), err
		}
	}
	if action != nil && action.Capture != nil && action.Capture.FullPage {
		opts.FullPage = playwright.Bool(true)
	}
	buf, err := b.currentPage().Screenshot(opts)
	return base64.StdEncoding.EncodeToString(buf), err
}

func (b *Browser) readDOMSummary(ctx context.Context) (string, string, *protocol.DOMSummary, error) {
	res, err := b.currentPage().Evaluate(`(() => ({
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

func (b *Browser) selectOption(ctx context.Context, selector, value string) error {
	if err := b.waitVisible(ctx, selector); err != nil {
		return err
	}
	_, err := b.currentPage().SelectOption(selector, playwright.SelectOptionValues{
		ValuesOrLabels: playwright.StringSlice(value),
	})
	return err
}
