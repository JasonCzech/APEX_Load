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
// loading another page reports that load. The step's resources count although
// the page's load filled its resource timing buffer, as Lightning's does.
func TestBrowserPageMetricsAreScopedToTheDocument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `window.appLoaded = true;`)
			return
		case "/aura":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"actions":[]}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head><title>App</title>
<script>performance.setResourceTimingBufferSize(1);</script>
<script src="/app.js"></script></head><body>
<button id="next">Account</button><h1 id="view">Home</h1>
<script>
document.getElementById('next').addEventListener('click', () => {
  const request = new XMLHttpRequest();
  request.open('POST', '/aura', false);
  request.send('message={}');
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
	if account.TotalResourceCount < 1 || account.ResourceTransferKB <= 0 {
		t.Fatalf("in-page step resources = %d (%.3f KB), want the request it sent although the buffer is full", account.TotalResourceCount, account.ResourceTransferKB)
	}
	other := run(protocol.Action{Name: "Open setup", Type: "browser_navigate", Target: "/lightning/setup/home"})
	if other.SameDocument || other.TotalResourceCount == 0 {
		t.Fatalf("second load = %+v", other)
	}
}
