package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserWaitElementCountPredicate(t *testing.T) {
	tests := []struct {
		name      string
		params    map[string]any
		wantOK    bool
		wantMatch map[int]bool
		wantError string
	}{
		{name: "missing", params: nil},
		{name: "default greater or equal", params: map[string]any{"count": 2}, wantOK: true, wantMatch: map[int]bool{1: false, 2: true, 3: true}},
		{name: "absence", params: map[string]any{"operator": "<", "count": 1}, wantOK: true, wantMatch: map[int]bool{0: true, 1: false}},
		{name: "zero equality", params: map[string]any{"operator": "==", "count": 0}, wantOK: true, wantMatch: map[int]bool{0: true, 1: false}},
		{name: "operator missing count", params: map[string]any{"operator": ">="}, wantError: "params.count is required"},
		{name: "negative count", params: map[string]any{"operator": ">=", "count": -1}, wantError: "params.count must be >= 0"},
		{name: "invalid operator", params: map[string]any{"operator": "~", "count": 1}, wantError: "params.operator must be one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			predicate, ok, err := browserWaitElementCountPredicate(&protocol.Action{Params: tt.params})
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			for actual, want := range tt.wantMatch {
				if got := predicate.matches(actual); got != want {
					t.Fatalf("matches(%d) = %v, want %v", actual, got, want)
				}
			}
		})
	}
}

func TestBrowserProtocolWaitsForElementCountPredicate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <body>
    <div class="item">one</div>
    <script>
      setTimeout(() => {
        const item = document.createElement('div');
        item.className = 'item';
        item.textContent = 'two';
        document.body.appendChild(item);
      }, 75);
    </script>
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

	wait, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Wait for two items",
		Type:     "browser_wait",
		Selector: ".item",
		WaitFor:  &protocol.WaitCondition{Type: "visible", TimeoutMs: 1000},
		Params: map[string]any{
			"operator": ">=",
			"count":    2,
		},
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if !wait.Success {
		t.Fatalf("wait failed: %s", wait.ErrorMessage)
	}
}
