package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The fixtures dispatch the lifecycle events themselves: under Playwright a
// back navigation reloads the page in both engines (pageshow is not
// persisted) rather than restoring it from the back/forward cache, and a
// headless page never becomes hidden. Everything after the event (the
// official web-vitals callbacks, the bootstrap, the binding and the evidence)
// runs in the real engine.
// documentFixtureTransition enters (pagehide) or restores from (pageshow)
// the back/forward cache.
func documentFixtureTransition(event string) string {
	return fmt.Sprintf(`(() => { dispatchEvent(new PageTransitionEvent(%q, {persisted: true})); return true })()`, event)
}

// documentFixtureVisibility makes the page hidden or visible again.
func documentFixtureVisibility(state string) string {
	return fmt.Sprintf(`(() => {
 Object.defineProperty(document, 'visibilityState', {configurable: true, get: () => %q});
 Object.defineProperty(document, 'hidden', {configurable: true, get: () => %t});
 document.dispatchEvent(new Event('visibilitychange', {bubbles: true}));
 return true;
})()`, state, state == "hidden")
}

func TestBrowserDocumentActualHiddenResumeAndBFCacheRestore(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<!doctype html><title>lifecycle</title><body><h1 style="font-size:64px">hidden, resumed and restored</h1></body>`)
			}))
			defer server.Close()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
			// The fixture page's document is the one that painted; the
			// initial empty document ended when the page navigated.
			page := ""
			documentActualWait(t, func() bool {
				for _, o := range documentActualMetric(t, b.BrowserDocumentEvidence(), "fcp", "main_frame") {
					if o.Available {
						page = o.DocumentID
					}
				}
				return page != ""
			})
			navigation := func(metric string) *protocol.BrowserDocumentObservation {
				for _, o := range documentActualMetric(t, b.BrowserDocumentEvidence(), metric, "main_frame") {
					if o.DocumentID == page && o.LifecyclePhase == "navigation" {
						return &o
					}
				}
				return nil
			}
			documentActualWait(t, func() bool { o := navigation("lcp"); return o != nil && o.Available })
			var ok bool
			// Five hidden/resume cycles: each hide resends progress, and the
			// first one finalizes LCP whether or not its value changed.
			for i := 0; i < 5; i++ {
				if err := b.evaluatePageExpression(ctx, documentFixtureVisibility("hidden"), &ok); err != nil || !ok {
					t.Fatalf("hide fixture: %v", err)
				}
				documentActualWait(t, func() bool { o := navigation("lcp"); return o != nil && o.State == "final" })
				if err := b.evaluatePageExpression(ctx, documentFixtureVisibility("visible"), &ok); err != nil || !ok {
					t.Fatalf("resume fixture: %v", err)
				}
			}
			if lcp := navigation("lcp"); lcp.Boundary != "hidden" || !lcp.Available || lcp.Value == nil {
				t.Fatalf("navigation LCP did not finalize when hidden: %+v", lcp)
			}
			// Entering the cache ends the navigation lifecycle; restoring
			// starts a separate one in the same document.
			if err := b.evaluatePageExpression(ctx, documentFixtureTransition("pagehide"), &ok); err != nil || !ok {
				t.Fatalf("pagehide fixture: %v", err)
			}
			documentActualWait(t, func() bool { o := navigation("cls"); return o != nil && o.State == "final" })
			if cls := navigation("cls"); cls.Boundary != "pagehide" {
				t.Fatalf("navigation lifecycle not final at pagehide: %+v", cls)
			}
			if err := b.evaluatePageExpression(ctx, documentFixtureTransition("pageshow"), &ok); err != nil || !ok {
				t.Fatalf("restore fixture: %v", err)
			}
			documentActualWait(t, func() bool {
				for _, o := range documentActualMetric(t, b.BrowserDocumentEvidence(), "fcp", "main_frame") {
					if o.LifecyclePhase == "bfcache_restore" && o.Available {
						return true
					}
				}
				return false
			})
			if err := b.Teardown(ctx); err != nil {
				t.Fatal(err)
			}
			evidence := b.BrowserDocumentEvidence()
			var observations []protocol.BrowserDocumentObservation
			for _, o := range evidence.Observations {
				if o.DocumentID == page {
					observations = append(observations, o)
				}
			}
			lifecycles := map[string]map[string]bool{}
			seen := map[string]bool{}
			var revised bool
			for _, o := range observations {
				if seen[o.MeasurementID] {
					t.Fatalf("measurement reported twice: %s", o.MeasurementID)
				}
				seen[o.MeasurementID] = true
				if o.FrameScope != "main_frame" {
					continue
				}
				if lifecycles[o.LifecyclePhase] == nil {
					lifecycles[o.LifecyclePhase] = map[string]bool{}
				}
				lifecycles[o.LifecyclePhase][o.LifecycleID] = true
				if o.LifecyclePhase == "navigation" && o.Revision > 1 {
					revised = true
				}
				if o.State == "provisional" {
					t.Fatalf("teardown left a provisional observation: %+v", o)
				}
			}
			if len(lifecycles["navigation"]) != 1 || len(lifecycles["bfcache_restore"]) != 1 {
				t.Fatalf("restore did not start one separate lifecycle: %v", lifecycles)
			}
			if !revised {
				t.Fatal("hidden/resume cycles recorded no progress revisions")
			}
			// Every document lifecycle metric is one final population member,
			// however many progress revisions it recorded, and a restore is
			// its own tuple.
			summary := protocol.SummarizeBrowserDocumentObservations(observations, 0, false)
			phases := map[string]map[string]int64{}
			for _, m := range summary.Metrics {
				if m.FrameScope != "main_frame" {
					continue
				}
				if phases[m.Metric] == nil {
					phases[m.Metric] = map[string]int64{}
				}
				phases[m.Metric][m.LifecyclePhase] += m.EligibleFinal + m.UnqualifiedFinal + m.UnavailableFinal
			}
			for _, metric := range []string{"lcp", "fcp", "cls"} {
				if phases[metric]["navigation"] != 1 || phases[metric]["bfcache_restore"] != 1 {
					t.Fatalf("%s final populations per lifecycle: %v", metric, phases[metric])
				}
			}
		})
	}
}

func TestBrowserActualWebKitProcessMetricsStayUnavailable(t *testing.T) {
	t.Setenv("APEXLOAD_BROWSER_PROCESS_METRICS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>process</title><body>process attribution</body>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := &Browser{}
	if err := b.Init(ctx, map[string]any{"base_url": server.URL, "browser_engine": "webkit", "browser_headless": true, "browser_client_metrics": true}); err != nil {
		requireBrowserRuntime(t, "webkit", err)
	}
	defer b.Teardown(context.Background())
	result := documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
	if result.ClientMetrics == nil || result.ClientMetrics.RuntimeAvailability == nil {
		t.Fatal("runtime availability not reported")
	}
	unavailable := 0
	for _, row := range result.ClientMetrics.RuntimeAvailability.Metrics {
		if row.Metric == "browser_process_cpu_pct" || row.Metric == "browser_process_mem_mb" {
			if row.Available || row.UnavailableReason != "unsupported" {
				t.Fatalf("WebKit process metric advertised: %+v", row)
			}
			unavailable++
		}
	}
	// Even with process attribution enabled, the driver's process tree never
	// stands in for WebKit's browser process.
	if unavailable != 2 || result.ClientMetrics.BrowserProcessCPUPct != 0 || result.ClientMetrics.BrowserProcessMemMB != 0 {
		t.Fatalf("WebKit process CPU/RSS: %+v", result.ClientMetrics)
	}
}
