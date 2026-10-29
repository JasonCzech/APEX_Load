package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolRegistered(t *testing.T) {
	p, err := protocol.Get("browser")
	if err != nil {
		t.Fatalf("Get(browser) returned error: %v", err)
	}
	if p.Name() != "browser" {
		t.Fatalf("Name() = %q, want browser", p.Name())
	}
}

func TestBrowserExecuteNilActionReturnsFailureResult(t *testing.T) {
	b := &Browser{}
	result, err := b.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("result = nil")
	}
	if result.Success {
		t.Fatal("Success = true, want false")
	}
	if result.ErrorMessage != "browser action is required" {
		t.Fatalf("ErrorMessage = %q, want browser action is required", result.ErrorMessage)
	}
}

func TestBrowserScreenshotWithRuntimeSecretsIsDebuggerOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>confidential test content</body></html>"))
	}))
	defer server.Close()
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", debug), func(t *testing.T) {
			b := &Browser{}
			if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_headless": true, "browser_capture_screenshots": "per_action", protocol.ReplayUnredactedDebugConfigKey: debug}); err != nil {
				requireBrowserRuntime(t, "", err)
			}
			defer func() { _ = b.Teardown(context.Background()) }()
			b.SetRuntimeSecretActivity(func() bool { return true })
			result, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
			if err != nil || !result.Success {
				t.Fatalf("navigate failed: %v (%s)", err, result.ErrorMessage)
			}
			if (result.ScreenshotBase64 != "") != debug {
				t.Fatalf("debug=%v final screenshot length=%d", debug, len(result.ScreenshotBase64))
			}
			screenshot, err := b.captureScreenshot(context.Background(), nil)
			if err != nil || (screenshot != "") != debug {
				t.Fatalf("debug=%v direct capture length=%d error=%v", debug, len(screenshot), err)
			}
		})
	}
}

func TestBrowserProtocolExecuteBrowserActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Test</title>
    <script>
      function submitForm() {
        const value = document.querySelector('#name').value;
        document.querySelector('#status').textContent = 'Saved ' + value;
        console.log('saved', value);
      }
      window.addEventListener('load', () => console.log('page loaded'));
    </script>
  </head>
  <body>
    <label for="name">Name</label>
    <input id="name" />
    <button id="submit" onclick="submitForm()">Save</button>
    <div id="status">Idle</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                    server.URL,
		"browser_headless":            true,
		"browser_capture_screenshots": "per_action",
		"browser_viewport_width":      1280,
		"browser_viewport_height":     720,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open homepage",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}
	if navigate.PageTitle != "Browser Test" {
		t.Fatalf("PageTitle = %q, want Browser Test", navigate.PageTitle)
	}
	if navigate.ScreenshotBase64 == "" {
		t.Fatal("navigate did not capture per-action screenshot")
	}
	if len(navigate.NetworkEntries) == 0 {
		t.Fatal("navigate did not capture network activity")
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
	})
	if err != nil {
		t.Fatalf("click returned error: %v", err)
	}
	if !click.Success {
		t.Fatalf("click failed: %s", click.ErrorMessage)
	}
	if len(click.ConsoleEntries) == 0 {
		t.Fatal("click did not capture console activity")
	}

	wait, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Wait for saved state",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "Saved apexload",
			TimeoutMs: 3000,
		},
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if !wait.Success {
		t.Fatalf("wait failed: %s", wait.ErrorMessage)
	}

	screenshot, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Capture page",
		Type: "browser_screenshot",
		Capture: &protocol.BrowserCaptureOptions{
			Screenshot: true,
			HTML:       true,
		},
	})
	if err != nil {
		t.Fatalf("screenshot returned error: %v", err)
	}
	if !screenshot.Success {
		t.Fatalf("screenshot failed: %s", screenshot.ErrorMessage)
	}
	if screenshot.ScreenshotBase64 == "" {
		t.Fatal("ScreenshotBase64 = empty, want captured screenshot")
	}
	if !strings.Contains(screenshot.HTML, "Saved apexload") {
		t.Fatalf("HTML did not contain saved marker: %q", screenshot.HTML)
	}
	if screenshot.DOMSummary == nil || screenshot.DOMSummary.Title != "Browser Test" {
		t.Fatalf("DOMSummary = %#v, want title Browser Test", screenshot.DOMSummary)
	}
}
