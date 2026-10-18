package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolExecutesFrameScopedActions(t *testing.T) {
	// BE-4: controlled pages in Chromium and WebKit.
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) { testFrameScopedActions(t, engine) })
	}
}

func testFrameScopedActions(t *testing.T, engine string) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/frame":
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <button id="pay" onclick="parent.postMessage('paid', '*')">Pay</button>
    <button id="coupon" onclick="parent.postMessage('coupon', '*')">Coupon</button>
  </body>
</html>`))
		default:
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Frame Checkout</title>
    <script>
      window.addEventListener('message', event => {
        if (event.data === 'paid') document.querySelector('#status').textContent = 'Paid from iframe';
        if (event.data === 'coupon') document.querySelector('#status').textContent = 'Coupon from iframe';
      });
    </script>
  </head>
  <body>
    <iframe id="checkout-frame" name="checkout-frame" src="/frame"></iframe>
    <div id="status">Idle</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := newIntegrationBrowser(t, engine, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open frame checkout", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:     "Pay in preserved frame",
		Type:     "browser_click",
		Selector: "#pay",
		Metadata: map[string]any{"source_frame_context": "checkout-frame"},
	})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Wait for iframe pay",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "Paid from iframe",
			TimeoutMs: 3000,
		},
	})

	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:   "Switch to checkout frame",
		Type:   "browser_switch_frame",
		Params: map[string]any{"frame": "checkout-frame"},
	})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:     "Apply coupon in active frame",
		Type:     "browser_click",
		Selector: "#coupon",
	})
	result := executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Wait for iframe coupon",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "Coupon from iframe",
			TimeoutMs: 3000,
		},
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Coupon from iframe"}},
	})
	if !strings.Contains(result.HTML, "Coupon from iframe") {
		t.Fatalf("HTML did not reflect frame-scoped click: %q", result.HTML)
	}
}

func TestBrowserProtocolDoubleClicksInsideAFrame(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/frame" {
					_, _ = w.Write([]byte(`<!doctype html><button id="open" ondblclick="parent.postMessage('opened', '*')">Open</button>`))
					return
				}
				_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Framed list</title>
    <script>
      window.addEventListener('message', event => {
        if (event.data === 'opened') document.querySelector('#status').textContent = 'Opened from iframe';
      });
    </script>
  </head>
  <body>
    <iframe id="list-frame" src="/frame"></iframe>
    <div id="status">Idle</div>
  </body>
</html>`))
			}))
			defer server.Close()

			b := newIntegrationBrowser(t, engine, server.URL)
			defer b.Teardown(context.Background())
			executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open framed list", Type: "browser_navigate", Target: "/"})
			executeSuccessfulBrowserAction(t, b, &protocol.Action{
				Name:     "Open the row",
				Type:     "browser_dblclick",
				Selector: "#open",
				Params:   map[string]any{"frame_selector": "#list-frame", "button": "left"},
			})
			executeSuccessfulBrowserAction(t, b, &protocol.Action{
				Name:    "Wait for the framed double click",
				Type:    "browser_wait",
				WaitFor: &protocol.WaitCondition{Type: "text", Value: "Opened from iframe", TimeoutMs: 3000},
			})
		})
	}
}

func TestBrowserProtocolExecutesNestedFramePathActions(t *testing.T) {
	// BE-4: controlled pages in Chromium and WebKit.
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) { testNestedFramePathActions(t, engine) })
	}
}

func testNestedFramePathActions(t *testing.T, engine string) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/inner":
			_, _ = w.Write([]byte(`<!doctype html><html><body>
  <button id="confirm" onclick="parent.parent.postMessage('nested-confirmed', '*')">Confirm</button>
</body></html>`))
		case "/outer":
			_, _ = w.Write([]byte(`<!doctype html><html><body>
  <iframe id="inner-frame" name="inner-frame" src="/inner"></iframe>
</body></html>`))
		default:
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Nested Frame Checkout</title>
  <script>
    window.addEventListener('message', event => {
      if (event.data === 'nested-confirmed') document.querySelector('#status').textContent = 'Nested confirmed';
    });
  </script>
</head><body>
  <iframe id="outer-frame" name="outer-frame" src="/outer"></iframe>
  <div id="status">Idle</div>
</body></html>`))
		}
	}))
	defer server.Close()

	b := newIntegrationBrowser(t, engine, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open nested frame checkout", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:     "Confirm in nested frame",
		Type:     "browser_click",
		Selector: "#confirm",
		Metadata: map[string]any{"frame_path": []any{"outer-frame", "inner-frame"}},
	})
	result := executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Wait for nested confirmation",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "Nested confirmed",
			TimeoutMs: 3000,
		},
	})
	if result.PageTitle != "Nested Frame Checkout" {
		t.Fatalf("PageTitle = %q, want main page title", result.PageTitle)
	}
}

func TestBrowserProtocolSetsHiddenFileInput(t *testing.T) {
	uploadDir := t.TempDir()
	if err := fileasset.RegisterRoot(uploadDir); err != nil {
		t.Fatal(err)
	}
	uploadFile := filepath.Join(uploadDir, "receipt.txt")
	if err := os.WriteFile(uploadFile, []byte("receipt"), 0o600); err != nil {
		t.Fatalf("write upload fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <input id="uploadFile" type="file" style="display:none" onchange="document.querySelector('#status').textContent = this.files[0].name" />
    <div id="status">No file</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open upload", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:     "Set upload file",
		Type:     "browser_set_files",
		Selector: "#uploadFile",
		Value:    uploadFile,
	})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Wait for upload",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "receipt.txt",
			TimeoutMs: 3000,
		},
	})
	result := executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:   "Read upload status",
		Type:   "browser_evaluate_js",
		Target: "document.querySelector('#status').textContent",
	})
	if result.Extractions["result"] != "receipt.txt" {
		t.Fatalf("upload status = %#v, want receipt.txt", result.Extractions)
	}
}

func TestBrowserProtocolPressesKeyOnSelector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <form onsubmit="event.preventDefault(); document.querySelector('#status').textContent = document.querySelector('#search').value">
      <input id="search" />
    </form>
    <div id="status">Idle</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open search", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Fill search", Type: "browser_fill", Selector: "#search", Value: "hp"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Press Enter", Type: "browser_press_key", Selector: "#search", Value: "Enter"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Wait for submit",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "hp",
			TimeoutMs: 3000,
		},
	})
	result := executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:   "Read search status",
		Type:   "browser_evaluate_js",
		Target: "document.querySelector('#status').textContent",
	})
	if result.Extractions["result"] != "hp" {
		t.Fatalf("search status = %#v, want hp", result.Extractions)
	}
}

func TestBrowserProtocolClearsInputWithEmptyFill(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <input id="name" value="prefilled" />
  </body>
</html>`))
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open form", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Clear name", Type: "browser_fill", Selector: "#name", Value: ""})
	result := executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:   "Read name",
		Type:   "browser_evaluate_js",
		Target: "document.querySelector('#name').value",
	})
	if result.Extractions["result"] != "" {
		t.Fatalf("name value = %#v, want empty string", result.Extractions)
	}
}

func TestBrowserProtocolEvaluatesFrameScopedElementAssertions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/frame":
			_, _ = w.Write([]byte(`<!doctype html>
<html><body><div id="status">Frame ready</div></body></html>`))
		default:
			_, _ = w.Write([]byte(`<!doctype html>
<html><body><iframe id="checkout-frame" name="checkout-frame" src="/frame"></iframe></body></html>`))
		}
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open frame", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:     "Assert frame status",
		Type:     "browser_wait",
		Selector: "#status",
		WaitFor: &protocol.WaitCondition{
			Type:      "visible",
			Value:     "#status",
			TimeoutMs: 3000,
		},
		Assertions: []protocol.Assertion{
			{Type: "element_visible", Value: "#status"},
			{Type: "element_text_contains", Value: "#status", Expected: "Frame ready"},
		},
		Metadata: map[string]any{"source_frame_context": "checkout-frame"},
	})
}

func TestBrowserProtocolFailsOnUnexpectedDialogMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <button id="alert" onclick="alert('actual message')">Alert</button>
  </body>
</html>`))
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open alert page", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Arm dialog",
		Type: "browser_handle_dialog",
		Params: map[string]any{
			"action":           "accept",
			"dialog_type":      "alert",
			"expected_message": "expected message",
		},
	})
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "Trigger alert", Type: "browser_click", Selector: "#alert"})
	if err != nil {
		t.Fatalf("Trigger alert returned error: %v", err)
	}
	if result.Success {
		t.Fatal("dialog trigger succeeded, want expected_message mismatch failure")
	}
	if !strings.Contains(result.ErrorMessage, "actual message") || !strings.Contains(result.ErrorMessage, "expected message") {
		t.Fatalf("ErrorMessage = %q, want actual and expected dialog messages", result.ErrorMessage)
	}
}

func TestBrowserProtocolClearsUnobservedDialogPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <button id="plain" onclick="document.querySelector('#status').textContent = 'plain clicked'">Plain</button>
    <button id="alert" onclick="alert('late dialog')">Alert</button>
    <div id="status">idle</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open dialog page", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Arm dialog",
		Type: "browser_handle_dialog",
		Params: map[string]any{
			"action":           "accept",
			"expected_message": "never appears",
			"wait_ms":          100,
		},
	})
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "Plain click", Type: "browser_click", Selector: "#plain"})
	if err != nil {
		t.Fatalf("Plain click returned error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "expected browser dialog was not observed") {
		t.Fatalf("plain click result = success:%v error:%q, want missing dialog failure", result.Success, result.ErrorMessage)
	}

	late, err := b.Execute(context.Background(), &protocol.Action{Name: "Late unrelated dialog", Type: "browser_click", Selector: "#alert"})
	if err != nil {
		t.Fatalf("Late dialog returned error: %v", err)
	}
	if !late.Success {
		t.Fatalf("late unrelated dialog failed after stale policy should have cleared: %q", late.ErrorMessage)
	}
}

func TestBrowserWaitHonorsActionTimeoutParam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body><div id="status">ready</div></body></html>`))
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open wait page", Type: "browser_navigate", Target: "/"})
	start := time.Now()
	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Wait briefly for missing element",
		Type:   "browser_wait",
		Target: "#never",
		Params: map[string]any{"timeout_ms": 100},
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if result.Success {
		t.Fatal("wait for missing element succeeded, want timeout failure")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("browser_wait ignored timeout_ms=100; elapsed %s error=%q", elapsed, result.ErrorMessage)
	}
}

func TestBrowserProtocolExecutesWindowSwitchActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/popup":
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Popup Checkout</title></head>
  <body>
    <button id="confirm" onclick="document.querySelector('#status').textContent = 'Popup confirmed'">Confirm</button>
    <div id="status">Popup idle</div>
  </body>
</html>`))
		default:
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Main Checkout</title></head>
  <body>
    <a id="open-popup" target="_blank" href="/popup">Open popup</a>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := newChromiumIntegrationBrowser(t, server.URL)
	defer b.Teardown(context.Background())

	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "Open main checkout", Type: "browser_navigate", Target: "/"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:     "Open popup",
		Type:     "browser_click",
		Selector: "#open-popup",
	})
	settle := executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Settle popup",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:  "duration",
			Value: "500ms",
		},
	})
	if settle.PageTitle != "Main Checkout" {
		t.Fatalf("PageTitle before explicit switch = %q, want main page to remain active", settle.PageTitle)
	}
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:   "Switch to popup",
		Type:   "browser_switch_window",
		Params: map[string]any{"index": 1},
	})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:     "Confirm popup",
		Type:     "browser_click",
		Selector: "#confirm",
	})
	result := executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Wait for popup confirmation",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "Popup confirmed",
			TimeoutMs: 3000,
		},
	})
	if result.PageTitle != "Popup Checkout" {
		t.Fatalf("PageTitle = %q, want Popup Checkout", result.PageTitle)
	}
}

func newChromiumIntegrationBrowser(t *testing.T, baseURL string) *Browser {
	t.Helper()
	return newIntegrationBrowser(t, "chromium", baseURL)
}

func newIntegrationBrowser(t *testing.T, engine, baseURL string) *Browser {
	t.Helper()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         baseURL,
		"browser_engine":   engine,
		"browser_headless": true,
	}); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	return b
}

func executeSuccessfulBrowserAction(t *testing.T, b *Browser, action *protocol.Action) *protocol.Result {
	t.Helper()
	result, err := b.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("%s returned error: %v", action.Name, err)
	}
	if !result.Success {
		t.Fatalf("%s failed: %s", action.Name, result.ErrorMessage)
	}
	return result
}
