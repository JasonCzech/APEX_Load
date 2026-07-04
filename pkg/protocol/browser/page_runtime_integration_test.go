package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolExecutesSelectAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Select</title>
    <script>
      function updateStatus() {
        const value = document.querySelector('#country').value;
        document.querySelector('#status').textContent = 'Selected ' + value;
      }
    </script>
  </head>
  <body>
    <select id="country" onchange="updateStatus()">
      <option value="">Choose</option>
      <option value="US">United States</option>
      <option value="CA">Canada</option>
    </select>
    <div id="status">Selected none</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	selectResult, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Choose country",
		Type:       "browser_select",
		Selector:   "#country",
		Value:      "United States",
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Selected US"}},
	})
	if err != nil {
		t.Fatalf("select returned error: %v", err)
	}
	if !selectResult.Success {
		t.Fatalf("select failed: %s", selectResult.ErrorMessage)
	}
	if !strings.Contains(selectResult.HTML, "Selected US") {
		t.Fatalf("HTML did not reflect selected option: %q", selectResult.HTML)
	}
}

func TestBrowserProtocolExecutesScrollAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body style="margin:0">
<main style="height:2200px;padding-top:20px">Scroll fixture</main>
</body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	if result, err := b.Execute(context.Background(), &protocol.Action{Name: "Open page", Type: "browser_navigate", Target: "/"}); err != nil || !result.Success {
		t.Fatalf("navigate result=%+v err=%v", result, err)
	}
	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Scroll down",
		Type:   "browser_scroll",
		Params: map[string]any{"delta_y": 360},
	})
	if err != nil {
		t.Fatalf("scroll returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("scroll failed: %s", result.ErrorMessage)
	}
	value, err := b.currentPage().Evaluate(`() => window.scrollY`)
	if err != nil {
		t.Fatalf("read scrollY: %v", err)
	}
	var scrollY float64
	switch typed := value.(type) {
	case int:
		scrollY = float64(typed)
	case int64:
		scrollY = float64(typed)
	case float64:
		scrollY = typed
	default:
		t.Fatalf("scrollY value = %T(%v), want numeric", value, value)
	}
	if scrollY < 300 {
		t.Fatalf("scrollY = %.0f, want >= 300", scrollY)
	}
}

func TestBrowserProtocolExecutesHistoryNavigationActions(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/one":
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>One</title></head><body><main>Page One</main></body></html>`))
		case "/two":
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Two</title></head><body><main>Page Two</main></body></html>`))
		case "/reload":
			hits++
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Reload</title></head><body><main>Reload ` + string(rune('0'+hits)) + `</main></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	for _, action := range []protocol.Action{
		{Name: "Open one", Type: "browser_navigate", Target: "/one"},
		{Name: "Open two", Type: "browser_navigate", Target: "/two"},
		{Name: "Back", Type: "browser_back", Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Page One"}}},
		{Name: "Forward", Type: "browser_forward", Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Page Two"}}},
		{Name: "Open reload", Type: "browser_navigate", Target: "/reload"},
	} {
		result, err := b.Execute(context.Background(), &action)
		if err != nil {
			t.Fatalf("%s returned error: %v", action.Name, err)
		}
		if !result.Success {
			t.Fatalf("%s failed: %s", action.Name, result.ErrorMessage)
		}
	}

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Reload",
		Type:       "browser_reload",
		Params:     map[string]any{"wait_until": "domcontentloaded"},
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Reload 2"}},
	})
	if err != nil {
		t.Fatalf("reload returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("reload failed: %s", result.ErrorMessage)
	}
	if hits != 2 {
		t.Fatalf("reload hits = %d, want 2", hits)
	}
}

func TestBrowserProtocolWaitsForNetworkResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<!doctype html><html><body><main>Response wait fixture</main></body></html>`))
		case "/api/orders/123":
			time.Sleep(75 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	if result, err := b.Execute(context.Background(), &protocol.Action{Name: "Open page", Type: "browser_navigate", Target: "/"}); err != nil || !result.Success {
		t.Fatalf("navigate result=%+v err=%v", result, err)
	}
	trigger, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Trigger delayed fetch",
		Type:   "browser_evaluate_js",
		Target: `setTimeout(() => fetch('/api/orders/123', {method: 'POST'}), 25); 'scheduled'`,
	})
	if err != nil {
		t.Fatalf("trigger returned error: %v", err)
	}
	if !trigger.Success {
		t.Fatalf("trigger failed: %s", trigger.ErrorMessage)
	}

	wait, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Wait for order response",
		Type:   "browser_wait_response",
		Target: "/api/orders",
		Params: map[string]any{
			"method":      "POST",
			"status_code": 201,
			"timeout_ms":  3000,
		},
	})
	if err != nil {
		t.Fatalf("wait response returned error: %v", err)
	}
	if !wait.Success {
		t.Fatalf("wait response failed: %s", wait.ErrorMessage)
	}
	match, ok := wait.ProtocolEvidence["browser_response_match"].(protocol.NetworkEntry)
	if !ok {
		t.Fatalf("browser_response_match = %#v, want NetworkEntry", wait.ProtocolEvidence["browser_response_match"])
	}
	if match.StatusCode != http.StatusCreated || match.Method != "POST" || !strings.Contains(match.URL, "/api/orders/123") {
		t.Fatalf("matched response = %+v, want POST 201 /api/orders/123", match)
	}
}

func TestBrowserProtocolExecutesDownloadAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<!doctype html><html><body><a id="download" href="/report" download>Download report</a></body></html>`))
		case "/report":
			w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte("id,name\n1,Ada\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	if result, err := b.Execute(context.Background(), &protocol.Action{Name: "Open page", Type: "browser_navigate", Target: "/"}); err != nil || !result.Success {
		t.Fatalf("navigate result=%+v err=%v", result, err)
	}
	saveDir := t.TempDir()
	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Download report",
		Type:     "browser_download",
		Selector: "#download",
		Params: map[string]any{
			"filename_contains": "report",
			"save_dir":          saveDir,
			"timeout_ms":        3000,
		},
	})
	if err != nil {
		t.Fatalf("download returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("download failed: %s", result.ErrorMessage)
	}
	evidence, ok := result.ProtocolEvidence["browser_download"].(map[string]any)
	if !ok {
		t.Fatalf("browser_download evidence = %#v, want map", result.ProtocolEvidence["browser_download"])
	}
	if evidence["suggested_filename"] != "report.csv" {
		t.Fatalf("suggested filename = %v, want report.csv", evidence["suggested_filename"])
	}
	savedPath, ok := evidence["saved_path"].(string)
	if !ok || savedPath == "" {
		t.Fatalf("saved_path = %#v, want non-empty string", evidence["saved_path"])
	}
	bytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read saved download: %v", err)
	}
	if string(bytes) != "id,name\n1,Ada\n" {
		t.Fatalf("saved download content = %q", string(bytes))
	}
}

func TestBrowserProtocolUsesHTTPCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "perf-user" || password != "perf-pass" {
			w.Header().Set("WWW-Authenticate", `Basic realm="apexload"`)
			http.Error(w, "auth required", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><html><body><main>Authenticated</main></body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                          server.URL,
		"browser_headless":                  true,
		"browser_http_credentials_username": "perf-user",
		"browser_http_credentials_password": "perf-pass",
		"browser_http_credentials_origin":   server.URL,
		"browser_http_credentials_send":     "unauthorized",
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Open protected page",
		Type:       "browser_navigate",
		Target:     "/",
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Authenticated"}},
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("navigate failed: %s", result.ErrorMessage)
	}
	if !strings.Contains(result.HTML, "Authenticated") {
		t.Fatalf("HTML = %q, want authenticated body", result.HTML)
	}
}

func TestBrowserProtocolAppliesLocaleTimezoneAndGeolocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body><main>Geo fixture</main></body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                      server.URL,
		"browser_headless":              true,
		"browser_locale":                "fr-FR",
		"browser_timezone":              "Europe/Paris",
		"browser_geolocation_latitude":  48.8566,
		"browser_geolocation_longitude": 2.3522,
		"browser_geolocation_accuracy":  20.0,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	if result, err := b.Execute(context.Background(), &protocol.Action{Name: "Open page", Type: "browser_navigate", Target: "/"}); err != nil || !result.Success {
		t.Fatalf("navigate result=%+v err=%v", result, err)
	}
	initial := evaluateBrowserJSON(t, b, `new Promise(resolve => navigator.geolocation.getCurrentPosition(
		pos => resolve(JSON.stringify({
			language: navigator.language,
			timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
			latitude: pos.coords.latitude,
			longitude: pos.coords.longitude
		})),
		err => resolve(JSON.stringify({error: err.code}))
	))`)
	if initial["language"] != "fr-FR" || initial["timezone"] != "Europe/Paris" {
		t.Fatalf("locale/timezone = %v/%v, want fr-FR/Europe/Paris", initial["language"], initial["timezone"])
	}
	if initial["latitude"] != 48.8566 || initial["longitude"] != 2.3522 {
		t.Fatalf("initial geolocation = %v/%v", initial["latitude"], initial["longitude"])
	}

	setGeo, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Move geolocation",
		Type: "browser_set_geolocation",
		Params: map[string]any{
			"latitude":  47.6062,
			"longitude": -122.3321,
			"accuracy":  10,
		},
	})
	if err != nil {
		t.Fatalf("set geolocation returned error: %v", err)
	}
	if !setGeo.Success {
		t.Fatalf("set geolocation failed: %s", setGeo.ErrorMessage)
	}
	updated := evaluateBrowserJSON(t, b, `new Promise(resolve => navigator.geolocation.getCurrentPosition(
		pos => resolve(JSON.stringify({latitude: pos.coords.latitude, longitude: pos.coords.longitude})),
		err => resolve(JSON.stringify({error: err.code}))
	))`)
	if updated["latitude"] != 47.6062 || updated["longitude"] != -122.3321 {
		t.Fatalf("updated geolocation = %v/%v", updated["latitude"], updated["longitude"])
	}
}

func evaluateBrowserJSON(t *testing.T, b *Browser, script string) map[string]any {
	t.Helper()
	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Evaluate JSON",
		Type:   "browser_evaluate_js",
		Target: script,
	})
	if err != nil {
		t.Fatalf("evaluate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("evaluate failed: %s", result.ErrorMessage)
	}
	raw := result.Extractions["result"]
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("evaluate result %q is not JSON: %v", raw, err)
	}
	if data["error"] != nil {
		t.Fatalf("evaluate returned geolocation error: %v", data["error"])
	}
	return data
}

func TestBrowserWaitHonorsShortSelectorTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body><main>ready</main></body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	start := time.Now()
	result, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Wait missing",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "visible",
			Value:     "#missing",
			TimeoutMs: 100,
		},
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if result.Success {
		t.Fatal("wait unexpectedly succeeded for missing selector")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("wait elapsed = %v, want short timeout", elapsed)
	}
}
