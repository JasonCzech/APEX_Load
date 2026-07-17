package browser

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type capturedBrowserPageState struct {
	pageURL    string
	pageTitle  string
	domSummary *protocol.DOMSummary
	html       string
	screenshot string
}

func (b *Browser) finalizeActionResult(ctx context.Context, action *protocol.Action, result *protocol.Result, actionErr error, consoleStart, networkStart int, processBefore browserProcessSnapshot, actionDuration time.Duration) {
	pageState, captureErr := b.captureActionPageState(ctx, action, actionErr != nil)
	b.applyCapturedPageState(result, pageState, consoleStart, networkStart)
	result.Duration = actionDuration
	result.ClientMetrics = b.captureClientMetrics(ctx, result.Duration, processBefore)

	if dialogErr := b.consumeDialogError(); dialogErr != nil {
		result.ErrorMessage = dialogErr.Error()
		result.Snapshot = browserErrorSnapshot(result)
		return
	}
	if actionErr != nil {
		result.ErrorMessage = actionErr.Error()
		result.Snapshot = browserErrorSnapshot(result)
		return
	}
	if captureErr != nil {
		result.ErrorMessage = captureErr.Error()
		return
	}
	if requiresBrowserAssertionHTML(action.Assertions) && result.HTML == "" {
		html, err := b.captureHTML(ctx)
		if err != nil {
			result.ErrorMessage = err.Error()
			return
		}
		result.HTML = html
	}
	if assertionErr := b.evaluateBrowserAssertions(action, action.Assertions, result); assertionErr != nil {
		result.ErrorMessage = assertionErr.Error()
		result.Snapshot = browserErrorSnapshot(result)
		return
	}

	result.Success = true
}

func (b *Browser) captureActionPageState(ctx context.Context, action *protocol.Action, forceArtifacts bool) (capturedBrowserPageState, error) {
	pageURL, pageTitle, domSummary, html, screenshot, err := b.capturePageState(ctx, action, forceArtifacts)
	return capturedBrowserPageState{
		pageURL:    pageURL,
		pageTitle:  pageTitle,
		domSummary: domSummary,
		html:       html,
		screenshot: screenshot,
	}, err
}

func (b *Browser) applyCapturedPageState(result *protocol.Result, pageState capturedBrowserPageState, consoleStart, networkStart int) {
	if pageState.pageURL != "" {
		b.currentURL = pageState.pageURL
		result.PageURL = pageState.pageURL
	}
	result.PageTitle = pageState.pageTitle
	result.DOMSummary = pageState.domSummary
	result.HTML = pageState.html
	if b.hasRuntimeSecretMaterial() {
		result.ScreenshotBase64 = ""
	} else {
		result.ScreenshotBase64 = pageState.screenshot
	}
	result.ConsoleEntries = b.consoleEntriesSince(consoleStart)
	result.NetworkEntries = b.networkEntriesSince(networkStart)
}
