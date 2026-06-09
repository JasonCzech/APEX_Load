package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolExecutesFrameScopedActions(t *testing.T) {
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

	b := newChromiumIntegrationBrowser(t, server.URL)
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

func TestBrowserProtocolSetsHiddenFileInput(t *testing.T) {
	uploadFile := filepath.Join(t.TempDir(), "receipt.txt")
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
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "Settle popup",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:  "duration",
			Value: "500ms",
		},
	})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name:   "Switch to popup",
		Type:   "browser_switch_window",
		Target: "Popup Checkout",
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
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         baseURL,
		"browser_engine":   "chromium",
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
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
