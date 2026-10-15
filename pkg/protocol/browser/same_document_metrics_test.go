package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A step in a document an earlier step loaded (a single-page app changing
// route with pushState) reports its own measures, not the page load again;
// loading another page reports that load.
func TestBrowserPageMetricsAreScopedToTheDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head><title>App</title></head><body>
<button id="next">Account</button><h1 id="view">Home</h1>
<script>
document.getElementById('next').addEventListener('click', () => {
  history.pushState({}, '', '/lightning/r/Account/001/view');
  document.getElementById('view').textContent = 'Account';
});
</script></body></html>`)
	}))
	defer server.Close()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_headless":       true,
		"browser_client_metrics": true,
	}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	defer b.Teardown(context.Background())
	run := func(action protocol.Action) *protocol.ClientMetrics {
		t.Helper()
		result, err := b.Execute(context.Background(), &action)
		if err != nil || !result.Success || result.ClientMetrics == nil {
			t.Fatalf("%s: err %v, result %+v", action.Name, err, result)
		}
		return result.ClientMetrics
	}
	home := run(protocol.Action{Name: "Open home", Type: "browser_navigate", Target: "/lightning/page/home"})
	if home.SameDocument || home.TotalResourceCount == 0 {
		t.Fatalf("home load = %+v", home)
	}
	account := run(protocol.Action{Name: "Open account", Type: "browser_click", Selector: "#next"})
	if !account.SameDocument || account.FCPMs != 0 || account.LCPMs != 0 || account.TTFBMs != 0 || account.DOMContentLoadedMs != 0 {
		t.Fatalf("in-page step = %+v", account)
	}
	other := run(protocol.Action{Name: "Open setup", Type: "browser_navigate", Target: "/lightning/setup/home"})
	if other.SameDocument || other.TotalResourceCount == 0 {
		t.Fatalf("second load = %+v", other)
	}
}
