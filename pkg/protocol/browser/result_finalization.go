package browser

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type capturedBrowserPageState struct {
	pageURL    string
	pageTitle  string
	domSummary *protocol.DOMSummary
	html       string
	screenshot string
}

func (b *Browser) finalizeActionResult(ctx context.Context, action *protocol.Action, result *protocol.Result, actionErr error, consoleStart, networkStart int, networkOwner browserNetworkActionOwner, processBefore browserProcessSnapshot, actionDuration time.Duration) {
	b.finalizeActionResultWithAssertions(ctx, action, result, actionErr, nil, false, browserAssertionHTMLCapture{}, consoleStart, networkStart, networkOwner, processBefore, actionDuration)
}

func (b *Browser) finalizeActionResultWithAssertions(ctx context.Context, action *protocol.Action, result *protocol.Result, actionErr, preAssertionErr error, assertionsEvaluated bool, assertionHTML browserAssertionHTMLCapture, consoleStart, networkStart int, networkOwner browserNetworkActionOwner, processBefore browserProcessSnapshot, actionDuration time.Duration) {
	defer b.applyEvidenceOutcome(result)
	priorHTML := result.HTML
	var capturedHTML *string
	if priorHTML != "" || assertionHTML.attempted {
		capturedHTML = &priorHTML
	}
	forceFailureArtifacts := actionErr != nil || preAssertionErr != nil || b.peekDialogError() != nil
	pageState, captureErr := b.captureActionPageState(ctx, action, forceFailureArtifacts, capturedHTML)
	if assertionHTML.err != nil {
		captureErr = combineBrowserCaptureErrors(&browserHTMLCaptureError{cause: assertionHTML.err}, captureErr)
	}
	defer func() { b.recordDebuggerArtifactErrors(result, captureErr) }()
	if pageState.html == "" && priorHTML != "" {
		pageState.html = priorHTML
	}
	networkEntries, acquisitionReason := b.acquireActionNetworkEntries(ctx, networkOwner, networkStart)
	b.applyCapturedPageStateWithNetwork(result, pageState, consoleStart, networkEntries)
	if evidence := auraActionsEvidence(networkEntries); evidence != nil {
		if result.ProtocolEvidence == nil {
			result.ProtocolEvidence = map[string]any{}
		}
		result.ProtocolEvidence["aura_actions"] = evidence
	}
	recordNetworkAcquisitionWarning(result, acquisitionReason)
	result.Duration = actionDuration
	result.ClientMetrics = b.captureClientMetrics(ctx, result.Duration, processBefore)

	if dialogErr := b.consumeDialogError(); dialogErr != nil {
		// A dialog callback can arrive while page evidence is being acquired.
		// The first pass only forces failure artifacts for errors already known
		// before acquisition; capture the missing ones now without retrying any
		// explicitly requested artifact or a previous failed attempt.
		if !forceFailureArtifacts && captureErr == nil {
			captureErr = b.captureLateFailureArtifacts(ctx, action, &pageState, capturedHTML != nil)
			b.applyCapturedPageStateWithNetwork(result, pageState, consoleStart, networkEntries)
		}
		result.ErrorMessage = dialogErr.Error()
		result.Snapshot = browserErrorSnapshot(result)
		return
	}
	if actionErr != nil {
		result.ErrorMessage = actionErr.Error()
		result.Snapshot = browserErrorSnapshot(result)
		return
	}
	if assertionsEvaluated && preAssertionErr != nil {
		result.ErrorMessage = preAssertionErr.Error()
		result.Snapshot = browserErrorSnapshot(result)
		return
	}
	if captureErr != nil {
		result.ErrorMessage = captureErr.Error()
		return
	}
	if assertionsEvaluated {
		result.Success = true
		b.recordSalesforceEPT(result)
		return
	}
	if requiresBrowserAssertionHTML(action.Assertions) && result.HTML == "" {
		html, err := b.captureHTML(ctx)
		if err != nil {
			captureErr = &browserHTMLCaptureError{cause: err}
			captureErr = combineBrowserCaptureErrors(captureErr, b.captureLateFailureArtifacts(ctx, action, &pageState, true))
			b.applyCapturedPageStateWithNetwork(result, pageState, consoleStart, networkEntries)
			result.ErrorMessage = err.Error()
			result.Snapshot = browserErrorSnapshot(result)
			return
		}
		result.HTML = html
		pageState.html = html
	}
	if assertionErr := b.evaluateBrowserAssertions(action, action.Assertions, result); assertionErr != nil {
		pageState.html = result.HTML
		captureErr = b.captureLateFailureArtifacts(ctx, action, &pageState, false)
		b.applyCapturedPageStateWithNetwork(result, pageState, consoleStart, networkEntries)
		result.ErrorMessage = assertionErr.Error()
		result.Snapshot = browserErrorSnapshot(result)
		return
	}

	result.Success = true
	b.recordSalesforceEPT(result)
}

// captureLateFailureArtifacts is only called after a successful initial
// acquisition which did not already force failure artifacts. Explicitly
// requested artifacts have already been attempted, even if empty or suppressed.
func (b *Browser) captureLateFailureArtifacts(ctx context.Context, action *protocol.Action, state *capturedBrowserPageState, htmlAttempted bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var captureErr error
	if !htmlAttempted && state.html == "" && (action.Capture == nil || !action.Capture.HTML) {
		html, err := b.captureHTML(ctx)
		state.html = html
		if err != nil {
			captureErr = &browserHTMLCaptureError{cause: err}
		}
	}
	screenshotRequested := b.captureScreenshots == "per_action" || action.Type == "browser_screenshot" || (action.Capture != nil && action.Capture.Screenshot)
	if state.screenshot == "" && !screenshotRequested && ctx.Err() == nil {
		screenshot, err := b.captureScreenshot(ctx, action)
		state.screenshot = screenshot
		if err != nil {
			captureErr = combineBrowserCaptureErrors(captureErr, &browserScreenshotCaptureError{cause: err})
		}
	}
	if err := ctx.Err(); err != nil {
		captureErr = combineBrowserCaptureErrors(err, captureErr)
	}
	return captureErr
}

func (b *Browser) recordDebuggerArtifactErrors(result *protocol.Result, captureErr error) {
	if !b.debugDisclosureEnabled() {
		return
	}
	setReason := func(key, fallback string, err error) {
		if result.ProtocolEvidence == nil {
			result.ProtocolEvidence = make(map[string]any)
		}
		reason := boundedtext.TruncateUTF8(err.Error(), 4096)
		if strings.TrimSpace(reason) == "" {
			reason = fallback
		}
		result.ProtocolEvidence[key] = reason
	}
	var screenshotErr *browserScreenshotCaptureError
	if errors.As(captureErr, &screenshotErr) {
		setReason("screenshot_capture_error", "Screenshot capture failed", screenshotErr)
	}
	var htmlErr *browserHTMLCaptureError
	if errors.As(captureErr, &htmlErr) {
		setReason("html_capture_error", "HTML capture failed", htmlErr)
	}
}

func (b *Browser) captureActionPageState(ctx context.Context, action *protocol.Action, forceArtifacts bool, capturedHTML *string) (capturedBrowserPageState, error) {
	pageURL, pageTitle, domSummary, html, screenshot, err := b.capturePageStateWithHTML(ctx, action, forceArtifacts, capturedHTML)
	return capturedBrowserPageState{
		pageURL:    pageURL,
		pageTitle:  pageTitle,
		domSummary: domSummary,
		html:       html,
		screenshot: screenshot,
	}, err
}

func (b *Browser) applyCapturedPageState(result *protocol.Result, pageState capturedBrowserPageState, consoleStart, networkStart int) {
	b.applyCapturedPageStateWithNetwork(result, pageState, consoleStart, b.networkEntriesSince(networkStart))
}

func (b *Browser) applyCapturedPageStateWithNetwork(result *protocol.Result, pageState capturedBrowserPageState, consoleStart int, networkEntries []protocol.NetworkEntry) {
	if pageState.pageURL != "" {
		b.currentURL = pageState.pageURL
		result.PageURL = pageState.pageURL
	}
	result.PageTitle = pageState.pageTitle
	result.DOMSummary = pageState.domSummary
	result.HTML = pageState.html
	if !b.debugDisclosureEnabled() && b.hasRuntimeSecretMaterial() {
		result.ScreenshotBase64 = ""
	} else {
		result.ScreenshotBase64 = pageState.screenshot
	}
	result.ConsoleEntries = b.consoleEntriesSince(consoleStart)
	result.NetworkEntries = networkEntries
}
