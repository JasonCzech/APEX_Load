package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// A single-page app that changes its URL with history.pushState 3 s after a
// click (Salesforce Lightning moving to a record page) converts with no
// navigation of its own: loading the URL would reload the app.
func TestRecordedAppRouteChangeIsNotANavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	const page = `<!doctype html><html><body>
<button id="open">Open account</button>
<button id="related" hidden>Related</button>
<script>
document.getElementById('open').addEventListener('click', () => setTimeout(() => {
  history.pushState({}, '', '/lightning/r/Account/001/view');
  document.getElementById('related').hidden = false;
}, 3000));
</script></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	}))
	defer server.Close()
	recorder := New(Options{StartURL: server.URL + "/lightning/page/home", Headless: true})
	if err := recorder.Start(); err != nil {
		if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
			t.Fatal(err)
		}
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer recorder.Stop()
	browserPage := recorder.context.Pages()[0]
	if err := browserPage.Click("#open"); err != nil {
		t.Fatal(err)
	}
	waitForSteps(t, recorder, 1, 5*time.Second)
	if err := browserPage.Click("#related"); err != nil {
		t.Fatal(err)
	}
	waitForSteps(t, recorder, 2, 5*time.Second)
	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	inPage := 0
	for _, nav := range session.NavEvents {
		if nav.SameDocument {
			inPage++
		}
	}
	if inPage != 1 {
		t.Fatalf("navigations = %+v, want the pushState marked in-page", session.NavEvents)
	}
	navigations := 0
	for _, action := range Convert(session, ConvertOptions{}).Script.Actions {
		if action.Type == "browser_navigate" {
			navigations++
		}
	}
	if navigations != 1 {
		t.Fatalf("%d browser_navigate actions, want only the start", navigations)
	}
}
