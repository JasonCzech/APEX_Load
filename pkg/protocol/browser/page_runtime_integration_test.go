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
