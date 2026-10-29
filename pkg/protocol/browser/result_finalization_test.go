package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

type failingScreenshotPage struct {
	playwright.Page
	oversized bool
	err       error
	htmlErr   error
	domErr    error
	image     []byte
	calls     int
	htmlCalls int
	onDOM     func()
	onHTML    func()
	onShot    func()
}

type emptyAssertionHTMLPage struct {
	playwright.Page
	calls int
}

func (page *emptyAssertionHTMLPage) Evaluate(_ string, _ ...any) (any, error) {
	page.calls++
	return "", nil
}

func TestMultipleAssertionsReuseEmptyHTML(t *testing.T) {
	page := &emptyAssertionHTMLPage{}
	b := &Browser{pwPage: page}
	result := &protocol.Result{}
	outcomes, err, capture := b.evaluateBrowserAssertionsWithOutcomes(context.Background(), nil, []protocol.Assertion{{Type: "body_not_contains", Value: "a"}, {Type: "body_not_contains", Value: "b"}}, result, false)
	if err != nil || page.calls != 1 || !capture.attempted || capture.err != nil || len(outcomes) != 2 || outcomes[1].Observed != protocol.InteractionOutcomeMet {
		t.Fatalf("outcomes=%#v capture=%#v calls=%d err=%v", outcomes, capture, page.calls, err)
	}
}

func TestEmptyEvaluatedAssertionHTMLIsNotRecaptured(t *testing.T) {
	for _, requested := range []bool{false, true} {
		page := &failingScreenshotPage{image: []byte("png")}
		b := &Browser{pwPage: page, unredactedDebug: true}
		action := &protocol.Action{Type: "browser_click"}
		if requested {
			action.Capture = &protocol.BrowserCaptureOptions{HTML: true}
		}
		result := &protocol.Result{}
		b.finalizeActionResultWithAssertions(context.Background(), action, result, nil, errors.New("assertion failed"), true, browserAssertionHTMLCapture{attempted: true}, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
		if page.htmlCalls != 0 || result.HTML != "" || page.calls != 1 || result.ErrorMessage != "assertion failed" {
			t.Fatalf("requested=%v HTML calls=%d result=%#v screenshot calls=%d", requested, page.htmlCalls, result, page.calls)
		}
	}
}

func (page *failingScreenshotPage) Evaluate(expression string, _ ...any) (any, error) {
	if strings.Contains(expression, "documentElement?.outerHTML") {
		page.htmlCalls++
		if page.onHTML != nil {
			page.onHTML()
		}
		if page.htmlErr != nil {
			return nil, page.htmlErr
		}
		return "<html>content</html>", nil
	}
	if page.onDOM != nil {
		page.onDOM()
	}
	if page.domErr != nil {
		return nil, page.domErr
	}
	return map[string]any{"url": "https://example.test", "title": "test", "readyState": "complete"}, nil
}

func (page *failingScreenshotPage) Screenshot(...playwright.PageScreenshotOptions) ([]byte, error) {
	page.calls++
	if page.onShot != nil {
		page.onShot()
	}
	if page.oversized {
		return make([]byte, browserScreenshotLimitBytes+1), nil
	}
	return page.image, page.err
}

func TestApplyCapturedPageState(t *testing.T) {
	b := &Browser{
		consoleEntries: []protocol.ConsoleEntry{{Text: "old"}, {Text: "new"}},
		networkEntries: []protocol.NetworkEntry{
			{URL: "/old"},
			{URL: "/new"},
		},
	}
	result := &protocol.Result{}
	pageState := capturedBrowserPageState{
		pageURL:    "https://example.test/next",
		pageTitle:  "Next",
		domSummary: &protocol.DOMSummary{Title: "Next", VisibleText: []string{"ready"}},
		html:       "<main>ready</main>",
		screenshot: "encoded",
	}

	b.applyCapturedPageState(result, pageState, 1, 1)

	if b.currentURL != pageState.pageURL || result.PageURL != pageState.pageURL {
		t.Fatalf("current/result URL = %q/%q, want %q", b.currentURL, result.PageURL, pageState.pageURL)
	}
	if result.PageTitle != "Next" || result.DOMSummary == nil || result.HTML != pageState.html || result.ScreenshotBase64 != pageState.screenshot {
		t.Fatalf("result page fields = %#v", result)
	}
	if len(result.ConsoleEntries) != 1 || result.ConsoleEntries[0].Text != "new" {
		t.Fatalf("ConsoleEntries = %#v, want new entry only", result.ConsoleEntries)
	}
	if len(result.NetworkEntries) != 1 || result.NetworkEntries[0].URL != "/new" {
		t.Fatalf("NetworkEntries = %#v, want new entry only", result.NetworkEntries)
	}
	result.ConsoleEntries[0].Text = "changed"
	result.NetworkEntries[0].URL = "/changed"
	if b.consoleEntries[1].Text != "new" || b.networkEntries[1].URL != "/new" {
		t.Fatal("event entries were aliased")
	}
}

func TestApplyCapturedPageStateLeavesCurrentURLEmptyWhenPageURLMissing(t *testing.T) {
	b := &Browser{currentURL: "https://example.test/current"}
	result := &protocol.Result{PageURL: "https://example.test/previous"}

	b.applyCapturedPageState(result, capturedBrowserPageState{pageTitle: "Untitled"}, 0, 0)

	if b.currentURL != "https://example.test/current" {
		t.Fatalf("currentURL = %q, want unchanged", b.currentURL)
	}
	if result.PageURL != "https://example.test/previous" {
		t.Fatalf("result.PageURL = %q, want unchanged", result.PageURL)
	}
	if result.PageTitle != "Untitled" {
		t.Fatalf("PageTitle = %q, want Untitled", result.PageTitle)
	}
}

func TestApplyCapturedPageStateSuppressesScreenshotWhenRuntimeSecretsAreActive(t *testing.T) {
	b := &Browser{}
	b.SetRuntimeSecretActivity(func() bool { return true })
	result := &protocol.Result{}
	b.applyCapturedPageState(result, capturedBrowserPageState{screenshot: "opaque-image"}, 0, 0)
	if result.ScreenshotBase64 != "" {
		t.Fatalf("ScreenshotBase64 = %q, want suppressed", result.ScreenshotBase64)
	}
	if screenshot, err := b.captureScreenshot(context.Background(), nil); err != nil || screenshot != "" {
		t.Fatalf("captureScreenshot with active secrets = %q, %v", screenshot, err)
	}
}

func TestApplyCapturedPageStateRetainsDebuggerScreenshotWithRuntimeSecrets(t *testing.T) {
	b := &Browser{unredactedDebug: true}
	b.SetRuntimeSecretActivity(func() bool { return true })
	result := &protocol.Result{}
	b.applyCapturedPageState(result, capturedBrowserPageState{screenshot: "opaque-image"}, 0, 0)
	if result.ScreenshotBase64 != "opaque-image" {
		t.Fatalf("debugger screenshot = %q, want retained", result.ScreenshotBase64)
	}
}

func TestFailureScreenshotCaptureReasonIsDebuggerOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		oversized bool
		err       error
		want      string
	}{
		{"oversized", true, nil, fmt.Sprintf("exceeds %d-byte capture limit", browserScreenshotLimitBytes)},
		{"capture failure", false, errors.New("renderer unavailable: private detail"), "renderer unavailable: private detail"},
		{"long error", false, errors.New(strings.Repeat("界", 2000)), strings.Repeat("界", 100)},
	} {
		for _, debug := range []bool{false, true} {
			for _, requested := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/debug=%v/requested=%v", tc.name, debug, requested), func(t *testing.T) {
					page := &failingScreenshotPage{oversized: tc.oversized, err: tc.err}
					b := &Browser{pwPage: page, unredactedDebug: debug}
					result := &protocol.Result{}
					primary := errors.New("primary action failure")
					action := &protocol.Action{Type: "browser_click"}
					if requested {
						b.captureScreenshots = "per_action"
					}
					b.finalizeActionResult(context.Background(), action, result, primary, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
					if result.ErrorMessage != primary.Error() || result.Success || page.calls != 1 {
						t.Fatalf("primary error=%q success=%v screenshot calls=%d", result.ErrorMessage, result.Success, page.calls)
					}
					reason, present := result.ProtocolEvidence["screenshot_capture_error"].(string)
					if present != debug || (debug && (!strings.Contains(reason, tc.want) || len(reason) > 4096 || !utf8.ValidString(reason))) {
						t.Fatalf("debug=%v screenshot capture reason=%q present=%v", debug, reason, present)
					}
				})
			}
		}
	}
}

func TestScreenshotSurvivesLaterHTMLCaptureFailure(t *testing.T) {
	image := []byte("complete screenshot bytes")
	for _, tc := range []struct {
		name          string
		debug         bool
		secret        bool
		primary       bool
		wantImage     bool
		wantPageCalls int
	}{
		{"failed debug action", true, true, true, true, 1},
		{"debug capture error", true, true, false, true, 1},
		{"ordinary action", false, false, true, true, 1},
		{"ordinary secret suppression", false, true, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := &failingScreenshotPage{image: image, htmlErr: errors.New("HTML renderer unavailable")}
			b := &Browser{pwPage: page, unredactedDebug: tc.debug}
			b.SetRuntimeSecretActivity(func() bool { return tc.secret })
			action := &protocol.Action{Type: "browser_click", Capture: &protocol.BrowserCaptureOptions{Screenshot: true, HTML: true}}
			result := &protocol.Result{}
			var actionErr error
			if tc.primary {
				actionErr = errors.New("primary action failure")
			}
			b.finalizeActionResult(context.Background(), action, result, actionErr, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
			wantErr := "HTML renderer unavailable"
			if tc.primary {
				wantErr = "primary action failure"
			}
			if result.ErrorMessage != wantErr || result.Success || page.calls != tc.wantPageCalls || page.htmlCalls != 1 {
				t.Fatalf("error=%q success=%v screenshot calls=%d HTML calls=%d", result.ErrorMessage, result.Success, page.calls, page.htmlCalls)
			}
			if tc.wantImage && result.ScreenshotBase64 != base64.StdEncoding.EncodeToString(image) || !tc.wantImage && result.ScreenshotBase64 != "" {
				t.Fatalf("screenshot = %q; want image=%v", result.ScreenshotBase64, tc.wantImage)
			}
			if tc.primary && (result.Snapshot == nil || result.Snapshot.ScreenshotBase64 != result.ScreenshotBase64) {
				t.Fatalf("failure snapshot lost screenshot: %#v", result.Snapshot)
			}
		})
	}
}

func TestScreenshotFailureDoesNotReplaceAssertionOrDialogError(t *testing.T) {
	for _, kind := range []string{"assertion", "dialog"} {
		t.Run(kind, func(t *testing.T) {
			page := &failingScreenshotPage{oversized: true}
			b := &Browser{pwPage: page, unredactedDebug: true}
			primary := errors.New(kind + " failure")
			var assertionErr error
			if kind == "dialog" {
				b.setDialogError(primary)
			} else {
				assertionErr = primary
			}
			result := &protocol.Result{}
			b.finalizeActionResultWithAssertions(context.Background(), &protocol.Action{Type: "browser_click"}, result, nil, assertionErr, true, browserAssertionHTMLCapture{}, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
			if result.Success || result.ErrorMessage != primary.Error() || result.Snapshot == nil || page.calls != 1 {
				t.Fatalf("error=%q success=%v snapshot=%v calls=%d", result.ErrorMessage, result.Success, result.Snapshot != nil, page.calls)
			}
			if reason, _ := result.ProtocolEvidence["screenshot_capture_error"].(string); !strings.Contains(reason, "capture limit") {
				t.Fatalf("missing screenshot capture reason: %q", reason)
			}
		})
	}
}

func TestLateDialogFailureCapturesMissingArtifactsWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hook      string
		debug     bool
		secret    bool
		cancel    bool
		shotError bool
		wantShots int
		wantHTML  int
		wantImage bool
	}{
		{"DOM callback", "dom", true, true, false, false, 1, 1, true},
		{"HTML callback", "html", true, true, false, false, 1, 1, true},
		{"screenshot callback", "screenshot", true, true, false, false, 1, 1, true},
		{"ordinary secret suppression", "dom", false, true, false, false, 0, 1, false},
		{"canceled parent", "dom", true, true, true, false, 0, 0, false},
		{"late screenshot failure", "dom", true, true, false, true, 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			page := &failingScreenshotPage{image: []byte("late screenshot")}
			if tc.shotError {
				page.err = errors.New("late screenshot unavailable")
			}
			b := &Browser{pwPage: page, unredactedDebug: tc.debug}
			b.SetRuntimeSecretActivity(func() bool { return tc.secret })
			entered, resume := make(chan struct{}), make(chan struct{})
			block := func() { close(entered); <-resume }
			action := &protocol.Action{Type: "browser_click"}
			switch tc.hook {
			case "dom":
				page.onDOM = block
			case "html":
				page.onHTML = block
				action.Capture = &protocol.BrowserCaptureOptions{HTML: true}
			case "screenshot":
				page.onShot = block
				action.Capture = &protocol.BrowserCaptureOptions{Screenshot: true}
			}
			result := &protocol.Result{}
			done := make(chan struct{})
			go func() {
				defer close(done)
				b.finalizeActionResultWithAssertions(ctx, action, result, nil, nil, true, browserAssertionHTMLCapture{}, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("acquisition did not begin")
			}
			b.setDialogError(errors.New("late dialog failure"))
			if tc.cancel {
				cancel()
			}
			close(resume)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("finalization did not finish")
			}
			if result.Success || result.ErrorMessage != "late dialog failure" || result.Snapshot == nil || page.calls != tc.wantShots || page.htmlCalls != tc.wantHTML {
				t.Fatalf("error=%q success=%v snapshot=%v screenshot calls=%d HTML calls=%d", result.ErrorMessage, result.Success, result.Snapshot != nil, page.calls, page.htmlCalls)
			}
			if (result.ScreenshotBase64 != "") != tc.wantImage {
				t.Fatalf("screenshot=%q", result.ScreenshotBase64)
			}
			if tc.shotError && result.ProtocolEvidence["screenshot_capture_error"] != "late screenshot unavailable" {
				t.Fatalf("late capture error not retained: %#v", result.ProtocolEvidence)
			}
		})
	}
}

func TestLateAssertionFailureCapturesMissingArtifacts(t *testing.T) {
	page := &failingScreenshotPage{image: []byte("assertion screenshot")}
	b := &Browser{pwPage: page, unredactedDebug: true}
	result := &protocol.Result{}
	action := &protocol.Action{Type: "browser_click", Assertions: []protocol.Assertion{{Type: "page_title", Value: "different"}}}
	b.finalizeActionResult(context.Background(), action, result, nil, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
	if result.Success || !strings.Contains(result.ErrorMessage, "different") || result.ScreenshotBase64 == "" || page.calls != 1 || page.htmlCalls != 1 {
		t.Fatalf("error=%q screenshot=%q screenshot calls=%d HTML calls=%d", result.ErrorMessage, result.ScreenshotBase64, page.calls, page.htmlCalls)
	}
}

func TestLateAssertionHTMLFailurePreservesReceiptAndScreenshot(t *testing.T) {
	page := &failingScreenshotPage{image: []byte("assertion screenshot"), htmlErr: errors.New("HTML failed")}
	b := &Browser{pwPage: page, unredactedDebug: true}
	result := &protocol.Result{}
	action := &protocol.Action{Type: "browser_click", Assertions: []protocol.Assertion{{Type: "body_contains", Value: "expected"}}}
	b.finalizeActionResult(context.Background(), action, result, nil, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
	if result.Success || result.ErrorMessage != "HTML failed" || result.ProtocolEvidence["html_capture_error"] != "HTML failed" || result.ScreenshotBase64 == "" || page.calls != 1 || page.htmlCalls != 1 {
		t.Fatalf("error=%q evidence=%#v screenshot=%q screenshot calls=%d HTML calls=%d", result.ErrorMessage, result.ProtocolEvidence, result.ScreenshotBase64, page.calls, page.htmlCalls)
	}
}

func TestDOMFailureDoesNotDiscardIndependentScreenshot(t *testing.T) {
	image := []byte("screenshot after DOM failure")
	for _, tc := range []struct {
		name      string
		debug     bool
		secrets   bool
		primary   bool
		wantImage bool
		wantCalls int
	}{
		{"debug with primary failure", true, true, true, true, 1},
		{"debug DOM failure", true, true, false, true, 1},
		{"ordinary without secrets", false, false, false, true, 1},
		{"ordinary with secrets", false, true, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := &failingScreenshotPage{image: image, domErr: errors.New("DOM unavailable")}
			b := &Browser{pwPage: page, unredactedDebug: tc.debug}
			b.SetRuntimeSecretActivity(func() bool { return tc.secrets })
			var actionErr error
			if tc.primary {
				actionErr = errors.New("primary action failure")
			}
			result := &protocol.Result{}
			b.finalizeActionResult(context.Background(), &protocol.Action{Type: "browser_click", Capture: &protocol.BrowserCaptureOptions{Screenshot: true}}, result, actionErr, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
			wantError := "DOM unavailable"
			if tc.primary {
				wantError = "primary action failure"
			}
			if result.ErrorMessage != wantError || result.Success || page.calls != tc.wantCalls {
				t.Fatalf("error=%q success=%v screenshot calls=%d", result.ErrorMessage, result.Success, page.calls)
			}
			if (result.ScreenshotBase64 == base64.StdEncoding.EncodeToString(image)) != tc.wantImage {
				t.Fatalf("screenshot=%q want image=%v", result.ScreenshotBase64, tc.wantImage)
			}
		})
	}
}

func TestScreenshotFailureDoesNotDiscardRequestedHTML(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		for _, primary := range []bool{false, true} {
			t.Run(fmt.Sprintf("oversized=%v/primary=%v", oversized, primary), func(t *testing.T) {
				page := &failingScreenshotPage{oversized: oversized, err: errors.New("screenshot failed")}
				b := &Browser{pwPage: page, unredactedDebug: true}
				var actionErr error
				if primary {
					actionErr = errors.New("primary action failure")
				}
				result := &protocol.Result{}
				action := &protocol.Action{Type: "browser_click", Capture: &protocol.BrowserCaptureOptions{Screenshot: true, HTML: true}}
				b.finalizeActionResult(context.Background(), action, result, actionErr, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
				if result.HTML != "<html>content</html>" || page.calls != 1 || page.htmlCalls != 1 {
					t.Fatalf("HTML=%q screenshot calls=%d HTML calls=%d", result.HTML, page.calls, page.htmlCalls)
				}
				reason, _ := result.ProtocolEvidence["screenshot_capture_error"].(string)
				if reason == "" {
					t.Fatal("missing screenshot error receipt")
				}
				if primary && result.ErrorMessage != "primary action failure" || !primary && result.ErrorMessage != reason {
					t.Fatalf("primary=%v error=%q screenshot reason=%q", primary, result.ErrorMessage, reason)
				}
			})
		}
	}
}

func TestCaptureCancellationStopsFurtherArtifacts(t *testing.T) {
	t.Run("DOM cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		page := &failingScreenshotPage{domErr: errors.New("DOM interrupted"), onDOM: cancel}
		b := &Browser{pwPage: page, unredactedDebug: true}
		_, _, _, _, _, err := b.capturePageState(ctx, &protocol.Action{Type: "browser_click", Capture: &protocol.BrowserCaptureOptions{Screenshot: true, HTML: true}}, true)
		if !errors.Is(err, context.Canceled) || page.calls != 0 || page.htmlCalls != 0 {
			t.Fatalf("error=%v screenshot calls=%d HTML calls=%d", err, page.calls, page.htmlCalls)
		}
	})
	t.Run("screenshot cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		page := &failingScreenshotPage{err: errors.New("screenshot interrupted"), onShot: cancel}
		b := &Browser{pwPage: page, unredactedDebug: true}
		_, _, _, _, _, err := b.capturePageState(ctx, &protocol.Action{Type: "browser_click", Capture: &protocol.BrowserCaptureOptions{Screenshot: true, HTML: true}}, true)
		var screenshotErr *browserScreenshotCaptureError
		if !errors.Is(err, context.Canceled) || !errors.As(err, &screenshotErr) || page.calls != 1 || page.htmlCalls != 0 {
			t.Fatalf("error=%v screenshot calls=%d HTML calls=%d", err, page.calls, page.htmlCalls)
		}
	})
	t.Run("HTML cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		page := &failingScreenshotPage{htmlErr: errors.New("HTML interrupted"), onHTML: cancel}
		b := &Browser{pwPage: page, unredactedDebug: true}
		_, _, _, _, _, err := b.capturePageState(ctx, &protocol.Action{Type: "browser_click", Capture: &protocol.BrowserCaptureOptions{HTML: true}}, false)
		var htmlErr *browserHTMLCaptureError
		if !errors.Is(err, context.Canceled) || !errors.As(err, &htmlErr) || page.calls != 0 || page.htmlCalls != 1 {
			t.Fatalf("error=%v screenshot calls=%d HTML calls=%d", err, page.calls, page.htmlCalls)
		}
	})
}

func TestDOMAndScreenshotFailuresRetainSeparateDiagnostics(t *testing.T) {
	page := &failingScreenshotPage{domErr: errors.New("DOM unavailable"), err: errors.New("screenshot unavailable")}
	b := &Browser{pwPage: page, unredactedDebug: true}
	result := &protocol.Result{}
	b.finalizeActionResult(context.Background(), &protocol.Action{Type: "browser_click", Capture: &protocol.BrowserCaptureOptions{Screenshot: true, HTML: true}}, result, nil, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
	if result.ErrorMessage != "DOM unavailable" || result.ProtocolEvidence["screenshot_capture_error"] != "screenshot unavailable" || result.HTML != "<html>content</html>" {
		t.Fatalf("error=%q evidence=%#v HTML=%q", result.ErrorMessage, result.ProtocolEvidence, result.HTML)
	}
}

func TestHTMLCaptureErrorIsDebuggerOnlyAndPreservesPrimaryFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		debug         bool
		explicitHTML  bool
		screenshotErr error
		primary       bool
		htmlErr       string
	}{
		{"forced debug", true, false, nil, true, "forced HTML error"},
		{"explicit debug", true, true, nil, false, "explicit HTML error"},
		{"both artifacts fail", true, true, errors.New("screenshot unavailable"), true, "HTML unavailable"},
		{"normal with primary", false, false, nil, true, "private HTML error"},
		{"bounded UTF-8", true, false, nil, true, strings.Repeat("界", 2000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := &failingScreenshotPage{image: []byte("image"), err: tc.screenshotErr, htmlErr: errors.New(tc.htmlErr)}
			b := &Browser{pwPage: page, unredactedDebug: tc.debug}
			action := &protocol.Action{Type: "browser_click"}
			if tc.explicitHTML {
				action.Capture = &protocol.BrowserCaptureOptions{Screenshot: tc.screenshotErr != nil, HTML: true}
			}
			var actionErr error
			if tc.primary {
				actionErr = errors.New("primary action failure")
			}
			result := &protocol.Result{}
			b.finalizeActionResult(context.Background(), action, result, actionErr, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
			reason, present := result.ProtocolEvidence["html_capture_error"].(string)
			if present != tc.debug || (tc.debug && (reason == "" || len(reason) > 4096 || !utf8.ValidString(reason) || !strings.HasPrefix(tc.htmlErr, reason))) {
				t.Fatalf("HTML capture reason=%q present=%v", reason, present)
			}
			if tc.primary && result.ErrorMessage != "primary action failure" || !tc.primary && result.ErrorMessage != tc.htmlErr {
				t.Fatalf("primary=%v error=%q", tc.primary, result.ErrorMessage)
			}
			if tc.screenshotErr != nil && result.ProtocolEvidence["screenshot_capture_error"] != tc.screenshotErr.Error() {
				t.Fatalf("missing screenshot capture error: %#v", result.ProtocolEvidence)
			}
			if page.htmlCalls != 1 || page.calls != 1 {
				t.Fatalf("HTML calls=%d screenshot calls=%d", page.htmlCalls, page.calls)
			}
		})
	}
}

func TestArtifactFailureTriggersIndependentScreenshot(t *testing.T) {
	image := []byte("screenshot after acquisition failure")
	for _, kind := range []string{"DOM", "HTML"} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/debug=%v", kind, debug), func(t *testing.T) {
				page := &failingScreenshotPage{image: image}
				action := &protocol.Action{Type: "browser_click"}
				if kind == "DOM" {
					page.domErr = errors.New("DOM failed")
				} else {
					page.htmlErr = errors.New("HTML failed")
					action.Capture = &protocol.BrowserCaptureOptions{HTML: true}
				}
				b := &Browser{pwPage: page, unredactedDebug: debug}
				b.SetRuntimeSecretActivity(func() bool { return true })
				result := &protocol.Result{}
				b.finalizeActionResult(context.Background(), action, result, nil, 0, 0, browserNetworkActionOwner{}, browserProcessSnapshot{}, time.Millisecond)
				if result.Success || result.ErrorMessage != kind+" failed" || (result.ScreenshotBase64 == base64.StdEncoding.EncodeToString(image)) != debug {
					t.Fatalf("debug=%v error=%q success=%v screenshot=%q", debug, result.ErrorMessage, result.Success, result.ScreenshotBase64)
				}
				wantCalls := 0
				if debug {
					wantCalls = 1
				}
				if page.calls != wantCalls {
					t.Fatalf("debug=%v actual screenshot calls=%d", debug, page.calls)
				}
			})
		}
	}
}

func TestApplyCapturedPageStateUsesOwnedNetworkSnapshotWithoutRecapture(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.networkEntries = []protocol.NetworkEntry{{URL: "/owned", RequestHeaders: map[string]string{"x-owned": "yes"}}}
	_, _, owner := b.actionEvidenceOffsets()
	entries, reason := b.acquireActionNetworkEntries(context.Background(), owner, 0)
	if reason != "" {
		t.Fatalf("owned snapshot reason = %q", reason)
	}
	b.mu.Lock()
	b.networkEntries = []protocol.NetworkEntry{{URL: "/later"}}
	b.mu.Unlock()
	result := &protocol.Result{}
	b.applyCapturedPageStateWithNetwork(result, capturedBrowserPageState{pageTitle: "Owned"}, 0, entries)
	if len(result.NetworkEntries) != 1 || result.NetworkEntries[0].URL != "/owned" || result.PageTitle != "Owned" {
		t.Fatalf("page finalization recaptured a newer network buffer: %#v", result)
	}
}
