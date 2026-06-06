package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

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
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
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
