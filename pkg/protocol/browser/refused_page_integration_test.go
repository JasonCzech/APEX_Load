package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A page the site refuses (a bot wall's 403 "Access Denied") fails its
// navigation, and the steps that needed that page fail at once with the same
// reason instead of each waiting out its timeout.
func TestBrowserNavigateFailsFastOnARefusedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/blocked" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Access Denied</title></head><body>You don't have permission.</body></html>`))
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Products</title></head><body><button id="go">Go</button></body></html>`))
	}))
	defer server.Close()

	b := newIntegrationBrowser(t, "", server.URL)
	defer func() { _ = b.Teardown(context.Background()) }()

	refused, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/blocked"})
	if err != nil {
		t.Fatal(err)
	}
	if refused.Success || refused.StatusCode != http.StatusForbidden ||
		!strings.Contains(refused.ErrorMessage, "the site refused the page") || !strings.Contains(refused.ErrorMessage, "HTTP 403") || !strings.Contains(refused.ErrorMessage, `"Access Denied"`) {
		t.Fatalf("navigate = success %v status %d error %q, want a refusal naming 403 and the page title", refused.Success, refused.StatusCode, refused.ErrorMessage)
	}

	started := time.Now()
	skipped, err := b.Execute(context.Background(), &protocol.Action{Name: "click", Type: "browser_click", Selector: "#go", TimeoutMs: 15000})
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Success || !strings.HasPrefix(skipped.ErrorMessage, "skipped: the site refused the page") || time.Since(started) > 2*time.Second {
		t.Fatalf("click on refused page = success %v error %q after %v, want an immediate skip", skipped.Success, skipped.ErrorMessage, time.Since(started))
	}

	// A reload the site refuses again fails the same way and keeps the page's
	// steps skipped.
	reloaded, err := b.Execute(context.Background(), &protocol.Action{Name: "reload", Type: "browser_reload"})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Success || !strings.Contains(reloaded.ErrorMessage, "HTTP 403") {
		t.Fatalf("reload of refused page = success %v error %q, want the refusal", reloaded.Success, reloaded.ErrorMessage)
	}
	if again, _ := b.Execute(context.Background(), &protocol.Action{Name: "click", Type: "browser_click", Selector: "#go", TimeoutMs: 15000}); again.Success || !strings.HasPrefix(again.ErrorMessage, "skipped:") {
		t.Fatalf("click after refused reload = success %v error %q, want a skip", again.Success, again.ErrorMessage)
	}

	// Leaving the refused page lets the next steps run, whether by a new
	// navigation or back through history.
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "products", Type: "browser_navigate", Target: "/products"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "click", Type: "browser_click", Selector: "#go"})
	if refusedAgain, _ := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/blocked"}); refusedAgain.Success {
		t.Fatal("second refused navigation succeeded")
	}
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "back", Type: "browser_back"})
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "click", Type: "browser_click", Selector: "#go"})

	// A step that expects the refusal asserts its status and passes.
	executeSuccessfulBrowserAction(t, b, &protocol.Action{
		Name: "expect denied", Type: "browser_navigate", Target: "/blocked",
		Assertions: []protocol.Assertion{{Type: "status_code", Expected: 403}},
	})
}

// A script that treats HTTP errors as warnings accepts an error page load.
func TestBrowserNavigateAcceptsErrorStatusWhenErrorsAreWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>App</title></head><body><button id="go">Go</button></body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                server.URL,
		"browser_headless":        true,
		"http_errors_as_warnings": true,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = b.Teardown(context.Background()) }()

	result := executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "deep link", Type: "browser_navigate", Target: "/orders/42"})
	if result.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want the recorded 404", result.StatusCode)
	}
	executeSuccessfulBrowserAction(t, b, &protocol.Action{Name: "click", Type: "browser_click", Selector: "#go"})
}
