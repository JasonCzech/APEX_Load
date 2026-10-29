package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// Change the real page immediately after the assertion reads its HTML, so the
// test does not depend on timing between two browser round trips.
type changingAssertionPage struct {
	playwright.Page
	htmlReads int
}

type failedAssertionHTMLPage struct {
	playwright.Page
	htmlReads int
	shotReads int
	cancel    context.CancelFunc
	shotErr   error
}

func (page *failedAssertionHTMLPage) Evaluate(expression string, args ...any) (any, error) {
	if strings.Contains(expression, "documentElement?.outerHTML") {
		page.htmlReads++
		if page.htmlReads == 1 {
			if page.cancel != nil {
				page.cancel()
				return nil, context.Canceled
			}
			return nil, errors.New("HTML failed: private assertion detail")
		}
	}
	return page.Page.Evaluate(expression, args...)
}

func (page *failedAssertionHTMLPage) Screenshot(opts ...playwright.PageScreenshotOptions) ([]byte, error) {
	page.shotReads++
	if page.shotErr != nil {
		return nil, page.shotErr
	}
	return page.Page.Screenshot(opts...)
}

func TestBrowserAssertionHTMLFailureIsNotRetried(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><button id="test">later document</button></body></html>`))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name                  string
		debug, requested      bool
		cancel, screenshotErr bool
	}{
		{name: "ordinary", requested: true},
		{name: "debug forced", debug: true},
		{name: "debug requested", debug: true, requested: true},
		{name: "combined errors", debug: true, screenshotErr: true},
		{name: "canceled", debug: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Browser{}
			if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_headless": true, protocol.ReplayUnredactedDebugConfigKey: tc.debug}); err != nil {
				requireBrowserRuntime(t, "", err)
			}
			defer func() { _ = b.Teardown(context.Background()) }()
			b.SetRuntimeSecretActivity(func() bool { return true })
			if got, err := b.Execute(context.Background(), &protocol.Action{Type: "browser_navigate", Target: "/"}); err != nil || !got.Success {
				t.Fatalf("navigate result=%#v err=%v", got, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := b.currentPage()
			page := &failedAssertionHTMLPage{Page: original}
			if tc.cancel {
				page.cancel = cancel
			}
			if tc.screenshotErr {
				page.shotErr = errors.New("screenshot failed separately")
			}
			b.mu.Lock()
			b.pwPage = page
			b.mu.Unlock()
			defer func() { b.mu.Lock(); b.pwPage = original; b.mu.Unlock() }()
			action := &protocol.Action{Type: "browser_evaluate_js", Target: "() => 1", Assertions: []protocol.Assertion{{Type: "body_contains", Value: "later document"}}}
			if tc.requested {
				action.Capture = &protocol.BrowserCaptureOptions{HTML: true}
			}
			got, err := b.Execute(ctx, action)
			wantErr := "HTML failed: private assertion detail"
			if tc.cancel {
				wantErr = context.Canceled.Error()
			}
			if err != nil || got.Success || got.ErrorMessage != wantErr || page.htmlReads != 1 || got.HTML != "" {
				t.Fatalf("result=%#v HTML reads=%d err=%v", got, page.htmlReads, err)
			}
			reason, present := got.ProtocolEvidence["html_capture_error"]
			if present != tc.debug || tc.debug && reason != wantErr {
				t.Fatalf("debug=%v HTML capture reason=%v present=%v", tc.debug, reason, present)
			}
			if tc.screenshotErr && got.ProtocolEvidence["screenshot_capture_error"] != page.shotErr.Error() {
				t.Fatalf("secondary screenshot error lost: %#v", got.ProtocolEvidence)
			}
			if tc.cancel && page.shotReads != 0 {
				t.Fatalf("screenshot attempted after cancellation: %d", page.shotReads)
			}
		})
	}
}

func (page *changingAssertionPage) Evaluate(expression string, args ...any) (any, error) {
	value, err := page.Page.Evaluate(expression, args...)
	if strings.Contains(expression, "documentElement?.outerHTML") {
		page.htmlReads++
		if err == nil && page.htmlReads == 1 {
			_, err = page.Page.Evaluate(`document.body.textContent = 'later document'`)
		}
	}
	return value, err
}

func TestBrowserAssertionRetainsEvaluatedHTMLWhenDocumentChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><button id="test">evaluated document</button></body></html>`))
	}))
	defer server.Close()
	for _, debug := range []bool{false, true} {
		for _, requested := range []bool{false, true} {
			t.Run(fmt.Sprintf("debug=%v/requested=%v", debug, requested), func(t *testing.T) {
				b := &Browser{}
				if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_headless": true, protocol.ReplayUnredactedDebugConfigKey: debug}); err != nil {
					requireBrowserRuntime(t, "", err)
				}
				defer func() { _ = b.Teardown(context.Background()) }()
				b.SetRuntimeSecretActivity(func() bool { return true })
				if got, err := b.Execute(context.Background(), &protocol.Action{Type: "browser_navigate", Target: "/"}); err != nil || !got.Success {
					t.Fatalf("navigate result=%#v err=%v", got, err)
				}
				original := b.currentPage()
				page := &changingAssertionPage{Page: original}
				b.mu.Lock()
				b.pwPage = page
				b.mu.Unlock()
				defer func() { b.mu.Lock(); b.pwPage = original; b.mu.Unlock() }()
				action := &protocol.Action{Type: "browser_click", Selector: "#test", Assertions: []protocol.Assertion{{Type: "body_contains", Value: "later document"}}}
				if requested {
					action.Capture = &protocol.BrowserCaptureOptions{HTML: true}
				}
				got, err := b.Execute(context.Background(), action)
				if err != nil || got.Success || !strings.Contains(got.ErrorMessage, "later document") || page.htmlReads != 1 {
					t.Fatalf("result=%#v html reads=%d err=%v", got, page.htmlReads, err)
				}
				if !strings.Contains(got.HTML, "evaluated document") || strings.Contains(got.HTML, "later document") || got.Snapshot == nil || got.Snapshot.HTML != got.HTML {
					t.Fatalf("wrong assertion evidence: result=%q snapshot=%#v", got.HTML, got.Snapshot)
				}
				if (got.ScreenshotBase64 != "") != debug {
					t.Fatalf("debug=%v screenshot length=%d", debug, len(got.ScreenshotBase64))
				}
			})
		}
	}
}

func TestBrowserAssertionAndDialogFailuresCaptureDebuggerScreenshots(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Actual title</title></head><body><button id="alert" onclick="alert('actual message')">Alert</button></body></html>`))
	}))
	defer server.Close()
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%v", debug), func(t *testing.T) {
			b := &Browser{}
			if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_headless": true, protocol.ReplayUnredactedDebugConfigKey: debug}); err != nil {
				requireBrowserRuntime(t, "", err)
			}
			defer func() { _ = b.Teardown(context.Background()) }()
			b.SetRuntimeSecretActivity(func() bool { return true })
			assertion, err := b.Execute(context.Background(), &protocol.Action{Name: "assert title", Type: "browser_navigate", Target: "/", Assertions: []protocol.Assertion{{Type: "page_title", Value: "Different title"}}})
			if err != nil || assertion.Success || !strings.Contains(assertion.ErrorMessage, "Different title") {
				t.Fatalf("assertion result success=%v error=%q execution error=%v", assertion.Success, assertion.ErrorMessage, err)
			}
			if (assertion.ScreenshotBase64 != "") != debug || assertion.Snapshot == nil || assertion.Snapshot.ScreenshotBase64 != assertion.ScreenshotBase64 {
				t.Fatalf("assertion screenshot missing or exposed: debug=%v screenshot bytes=%d", debug, len(assertion.ScreenshotBase64))
			}
			armed, err := b.Execute(context.Background(), &protocol.Action{Name: "arm", Type: "browser_handle_dialog", Params: map[string]any{"action": "accept", "dialog_type": "alert", "expected_message": "expected message"}})
			if err != nil || !armed.Success {
				t.Fatalf("arm dialog: %v (%s)", err, armed.ErrorMessage)
			}
			dialog, err := b.Execute(context.Background(), &protocol.Action{Name: "trigger", Type: "browser_click", Selector: "#alert"})
			if err != nil || dialog.Success || !strings.Contains(dialog.ErrorMessage, "expected message") {
				t.Fatalf("dialog result success=%v error=%q execution error=%v", dialog.Success, dialog.ErrorMessage, err)
			}
			if (dialog.ScreenshotBase64 != "") != debug || dialog.Snapshot == nil || dialog.Snapshot.ScreenshotBase64 != dialog.ScreenshotBase64 {
				t.Fatalf("dialog screenshot missing or exposed: debug=%v screenshot bytes=%d", debug, len(dialog.ScreenshotBase64))
			}
		})
	}
}

func TestBrowserProtocolEvaluatesAssertions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Assertions</title>
    <script>
      function submitForm() {
        const value = document.querySelector('#name').value;
        document.querySelector('#status').textContent = 'Saved ' + value;
      }
    </script>
  </head>
  <body>
    <input id="name" />
    <button id="submit" onclick="submitForm()">Save</button>
    <div id="status">Idle</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Open homepage",
		Type:       "browser_navigate",
		Target:     "/",
		Assertions: []protocol.Assertion{{Type: "page_title", Value: "Browser Assertions"}, {Type: "url_contains", Value: server.URL}},
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	fill, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Fill name",
		Type:     "browser_fill",
		Selector: "#name",
		Value:    "apexload",
	})
	if err != nil {
		t.Fatalf("fill returned error: %v", err)
	}
	if !fill.Success {
		t.Fatalf("fill failed: %s", fill.ErrorMessage)
	}

	click, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Click save",
		Type:     "browser_click",
		Selector: "#submit",
		Assertions: []protocol.Assertion{
			{Type: "body_contains", Value: "Saved apexload"},
			{Type: "body_not_contains", Value: "Error"},
			{Type: "response_time_ms", Max: 5000},
		},
	})
	if err != nil {
		t.Fatalf("click returned error: %v", err)
	}
	if !click.Success {
		t.Fatalf("click failed: %s", click.ErrorMessage)
	}
	if !strings.Contains(click.HTML, "Saved apexload") {
		t.Fatalf("HTML = %q, want Saved apexload", click.HTML)
	}

	failed, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Fail assertion",
		Type:       "browser_click",
		Selector:   "#submit",
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Never appears"}},
	})
	if err != nil {
		t.Fatalf("failed assertion click returned error: %v", err)
	}
	if failed.Success {
		t.Fatal("Success = true, want false on browser assertion failure")
	}
	if !strings.Contains(failed.ErrorMessage, `browser page HTML does not contain "Never appears"`) {
		t.Fatalf("ErrorMessage = %q, want browser assertion failure", failed.ErrorMessage)
	}
	if failed.Snapshot == nil {
		t.Fatal("Snapshot = nil, want populated snapshot on browser assertion failure")
	}
	if !strings.Contains(failed.Snapshot.HTML, "Saved apexload") {
		t.Fatalf("Snapshot.HTML = %q, want saved page HTML", failed.Snapshot.HTML)
	}
}
