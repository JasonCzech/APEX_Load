package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Browser acceptance (BE-4, D8): controlled pages exercised in Chromium and
// WebKit, reconciled against the fixture server's own clock and the native
// HAR. Each test names the BE-4 behavior it accepts; the inventory in
// docs/qa/browser_acceptance_test.go maps every BE-4 item to its tests.

// acceptanceServerClock records, per path, when the fixture server received
// a request and when it wrote the first byte and the last byte: a clock
// independent of the browser's.
type acceptanceServerClock struct {
	mu    sync.Mutex
	marks map[string][3]time.Time
}

func (c *acceptanceServerClock) mark(path string, index int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.marks == nil {
		c.marks = map[string][3]time.Time{}
	}
	m := c.marks[path]
	m[index] = time.Now()
	c.marks[path] = m
}

func (c *acceptanceServerClock) durations(path string) (wait, receive time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.marks[path]
	return m[1].Sub(m[0]), m[2].Sub(m[1])
}

func acceptanceEntry(t *testing.T, b *Browser, suffix string) protocol.NetworkEntry {
	t.Helper()
	var found protocol.NetworkEntry
	documentActualWait(t, func() bool {
		for _, entry := range b.networkEntriesSince(0) {
			if strings.HasSuffix(entry.URL, suffix) && entry.BrowserEvidence != nil && entry.BrowserEvidence.State != "" {
				found = entry
				return true
			}
		}
		return false
	})
	return found
}

// acceptancePhaseUnavailable is why the evidence has no span for phase name.
func acceptancePhaseUnavailable(e *protocol.BrowserResourceEvidence, name string) string {
	for _, phase := range e.Phases {
		if phase.Name == name {
			return phase.UnavailableReason
		}
	}
	return ""
}

func acceptancePhase(e *protocol.BrowserResourceEvidence, name string) (float64, bool) {
	for _, phase := range e.Phases {
		if phase.Name == name && phase.StartMS != nil && phase.EndMS != nil {
			return *phase.EndMS - *phase.StartMS, true
		}
	}
	return 0, false
}

// TestBrowserAcceptanceWaterfallMatchesFixtureClockAndHAR accepts BE-4
// "reconcile waterfall timing with independent fixture clocks and native
// HAR": a response whose first byte the server holds for 300 ms and whose
// body it streams over 200 ms shows those spans in the request-wait and
// response phases, and in the native HAR's wait and receive timings.
func TestBrowserAcceptanceWaterfallMatchesFixtureClockAndHAR(t *testing.T) {
	if testing.Short() {
		t.Skip("browser acceptance requires installed browsers")
	}
	const holdFirstByte, streamBody = 300 * time.Millisecond, 200 * time.Millisecond
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			clock := &acceptanceServerClock{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/slow" {
					w.Header().Set("Content-Type", "text/html")
					_, _ = fmt.Fprint(w, `<!doctype html><title>waterfall</title><button id="go" onclick="fetch('/slow').then(r=>r.text()).then(t=>document.querySelector('#out').textContent=t.length)">go</button><div id="out">pending</div>`)
					return
				}
				clock.mark("/slow", 0)
				time.Sleep(holdFirstByte)
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(strings.Repeat("a", 1024)))
				w.(http.Flusher).Flush()
				clock.mark("/slow", 1)
				time.Sleep(streamBody)
				_, _ = w.Write([]byte(strings.Repeat("b", 1024)))
				clock.mark("/slow", 2)
			}))
			defer server.Close()
			harDir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b := &Browser{}
			if err := b.Init(ctx, map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_har_capture": HARCaptureAlways, "browser_har_dir": harDir}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			defer func() { _ = b.Teardown(context.Background()) }()
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "fetch", Type: "browser_click", Selector: "#go"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "wait", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "2048", TimeoutMs: 5000}})
			entry := acceptanceEntry(t, b, "/slow")
			serverWait, serverReceive := clock.durations("/slow")
			wait, ok := acceptancePhase(entry.BrowserEvidence, "request_wait")
			if !ok && engine == "webkit" && runtime.GOOS == "windows" && acceptancePhaseUnavailable(entry.BrowserEvidence, "request_wait") == "not_exposed" {
				// Playwright's Windows WebKit reports no request start, so
				// there is no request-wait span to reconcile; the evidence
				// says so rather than inventing one. Its native HAR is still
				// recorded and published.
				if err := b.Teardown(context.Background()); err != nil {
					t.Fatal(err)
				}
				harWait, harReceive, found := acceptanceHARTimings(t, b.TakeBrowserHARArtifacts(), "/slow")
				if !found {
					t.Fatal("native HAR has no /slow entry")
				}
				t.Skipf("WebKit on Windows exposes no request timing (native HAR wait=%.1f ms receive=%.1f ms): %+v", harWait, harReceive, entry.BrowserEvidence.Phases)
			}
			if !ok {
				t.Fatalf("request_wait phase unavailable: %+v", entry.BrowserEvidence.Phases)
			}
			receive, ok := acceptancePhase(entry.BrowserEvidence, "response")
			if !ok {
				t.Fatalf("response phase unavailable: %+v", entry.BrowserEvidence.Phases)
			}
			// The browser's spans contain the server's: never shorter than
			// what the server held (less clock granularity), and not longer
			// by more than local transport and scheduling.
			const granularity, slack = 5.0, 250.0
			if wait < float64(serverWait.Milliseconds())-granularity || wait > float64(serverWait.Milliseconds())+slack {
				t.Fatalf("request_wait %.1f ms, server held the first byte %d ms", wait, serverWait.Milliseconds())
			}
			if receive < float64(serverReceive.Milliseconds())-granularity || receive > float64(serverReceive.Milliseconds())+slack {
				t.Fatalf("response %.1f ms, server streamed the body %d ms", receive, serverReceive.Milliseconds())
			}
			t.Logf("%s waterfall request_wait=%.1f ms (server %d ms) response=%.1f ms (server %d ms)", engine, wait, serverWait.Milliseconds(), receive, serverReceive.Milliseconds())
			// The native HAR, written by the browser's own recorder, agrees.
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
			artifacts := b.TakeBrowserHARArtifacts()
			if len(artifacts) == 0 {
				t.Fatal("no native HAR artifact")
			}
			harWait, harReceive, found := acceptanceHARTimings(t, artifacts, "/slow")
			if !found {
				t.Fatal("native HAR has no /slow entry")
			}
			if diff := harWait - wait; diff < -50 || diff > 50 {
				t.Fatalf("HAR wait %.1f ms vs evidence request_wait %.1f ms", harWait, wait)
			}
			if diff := harReceive - receive; diff < -50 || diff > 50 {
				t.Fatalf("HAR receive %.1f ms vs evidence response %.1f ms", harReceive, receive)
			}
		})
	}
}

func acceptanceHARTimings(t *testing.T, artifacts []protocol.BrowserHARArtifact, suffix string) (float64, float64, bool) {
	t.Helper()
	for _, artifact := range artifacts {
		raw, err := os.ReadFile(artifact.Path)
		if err != nil {
			t.Fatal(err)
		}
		var har struct {
			Log struct {
				Entries []struct {
					Request struct {
						URL string `json:"url"`
					} `json:"request"`
					Timings struct {
						Wait    float64 `json:"wait"`
						Receive float64 `json:"receive"`
					} `json:"timings"`
				} `json:"entries"`
			} `json:"log"`
		}
		if err := json.Unmarshal(raw, &har); err != nil {
			t.Fatal(err)
		}
		for _, entry := range har.Log.Entries {
			if strings.HasSuffix(entry.Request.URL, suffix) {
				return entry.Timings.Wait, entry.Timings.Receive, true
			}
		}
	}
	return 0, 0, false
}

// TestBrowserAcceptanceRedirectsHTTPErrorsAndTransportClose accepts BE-4
// "redirects", "HTTP errors" and "transport close": a redirect chain keeps
// one chain identity with ordered hops, an HTTP error is a finished
// response with its status, and a connection the server closes mid-response
// is a failed transport.
func TestBrowserAcceptanceRedirectsHTTPErrorsAndTransportClose(t *testing.T) {
	if testing.Short() {
		t.Skip("browser acceptance requires installed browsers")
	}
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hop1":
					http.Redirect(w, r, "/hop2", http.StatusFound)
				case "/hop2":
					http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
				case "/final":
					_, _ = fmt.Fprint(w, "final")
				case "/broken":
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprint(w, "server error")
				case "/reset":
					hijacker, _ := w.(http.Hijacker)
					conn, _, err := hijacker.Hijack()
					if err == nil {
						_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100000\r\n\r\npartial"))
						if tcp, ok := conn.(*net.TCPConn); ok {
							_ = tcp.SetLinger(0)
						}
						_ = conn.Close()
					}
				default:
					w.Header().Set("Content-Type", "text/html")
					_, _ = fmt.Fprint(w, `<!doctype html><title>outcomes</title><button id="go" onclick="Promise.allSettled([fetch('/hop1').then(r=>r.text()),fetch('/broken').then(r=>r.text()),fetch('/reset').then(r=>r.text())]).then(()=>document.querySelector('#out').textContent='settled')">go</button><div id="out">pending</div>`)
				}
			}))
			defer server.Close()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "fetch", Type: "browser_click", Selector: "#go"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "wait", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "settled", TimeoutMs: 10000}})
			final := acceptanceEntry(t, b, "/final")
			if final.BrowserEvidence.RedirectHop != 2 || final.BrowserEvidence.RedirectChainID == "" || final.BrowserEvidence.RedirectFromRequestID == "" || final.StatusCode != http.StatusOK {
				t.Fatalf("final redirect hop = %+v status %d", final.BrowserEvidence, final.StatusCode)
			}
			first := acceptanceEntry(t, b, "/hop1")
			if first.BrowserEvidence.RedirectChainID != final.BrowserEvidence.RedirectChainID || first.BrowserEvidence.RedirectHop != 0 {
				t.Fatalf("first hop %+v does not share the chain of %+v", first.BrowserEvidence, final.BrowserEvidence)
			}
			broken := acceptanceEntry(t, b, "/broken")
			if broken.StatusCode != http.StatusInternalServerError || broken.BrowserEvidence.State != "finished" {
				t.Fatalf("HTTP error = status %d state %q", broken.StatusCode, broken.BrowserEvidence.State)
			}
			if broken.FailureKind != "http" {
				t.Fatalf("HTTP error failure kind = %q", broken.FailureKind)
			}
			reset := acceptanceEntry(t, b, "/reset")
			if reset.BrowserEvidence.State != "failed" || reset.FailureKind != "transport" {
				t.Fatalf("closed transport = state %q failure %q", reset.BrowserEvidence.State, reset.FailureKind)
			}
		})
	}
}

// TestBrowserAcceptanceCrossOriginResourcesKeepTheirTiming accepts BE-4
// "cross-origin timing restrictions": a cross-origin subresource without
// Timing-Allow-Origin still has its network phases from the browser's own
// recorder (the page's Resource Timing hides them), names only its origin,
// and keeps its document association honest.
func TestBrowserAcceptanceCrossOriginResourcesKeepTheirTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("browser acceptance requires installed browsers")
	}
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				time.Sleep(100 * time.Millisecond)
				_, _ = fmt.Fprint(w, "cross")
			}))
			defer other.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprintf(w, `<!doctype html><title>cross</title><button id="go" onclick="fetch('%s/data?secret=1').then(r=>r.text()).then(t=>document.querySelector('#out').textContent=t)">go</button><div id="out">pending</div>`, other.URL)
			}))
			defer server.Close()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "fetch", Type: "browser_click", Selector: "#go"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "wait", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "cross", TimeoutMs: 5000}})
			// The restriction is in effect: without Timing-Allow-Origin the
			// page's own Resource Timing hides the phases.
			hidden, err := b.currentPage().Evaluate(`() => { const e = performance.getEntriesByType('resource').find(e => e.name.includes('/data?secret=1')); return !!e && e.requestStart === 0 && e.responseStart === 0 }`)
			if err != nil || hidden != true {
				t.Fatalf("page Resource Timing exposed cross-origin phases: %v %v", hidden, err)
			}
			entry := acceptanceEntry(t, b, "/data?secret=1")
			evidence := entry.BrowserEvidence
			if evidence.State != "finished" || evidence.TimingSource != "playwright" {
				t.Fatalf("cross-origin evidence = %+v", evidence)
			}
			// The browser's own recorder still has the phases.
			if wait, ok := acceptancePhase(evidence, "request_wait"); !ok || wait < 95 {
				t.Fatalf("cross-origin request_wait = %.1f %v", wait, ok)
			}
			origin, err := url.Parse(other.URL)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.Origin != origin.Scheme+"://"+origin.Host {
				t.Fatalf("origin = %q, want only %s://%s", evidence.Origin, origin.Scheme, origin.Host)
			}
			// The document association is proven or explicitly unavailable.
			if (evidence.DocumentID == "" || evidence.DocumentAssociation == "") && evidence.IdentityUnavailableReason == "" {
				t.Fatalf("cross-origin document association = %+v", evidence)
			}
		})
	}
}

// TestBrowserAcceptanceServiceWorkerServedResponses accepts BE-4 "cache and
// service workers": a page registers a service worker that answers one path
// itself, and the response it served is recorded with service-worker
// provenance, never as a network response. (A CI target policy refuses the session before launch:
// TestCITargetPolicyFailsClosedBeforeBrowserLaunch.)
func TestBrowserAcceptanceServiceWorkerServedResponses(t *testing.T) {
	if testing.Short() {
		t.Skip("browser acceptance requires installed browsers")
	}
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			var workerFetches, servedFetches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/sw.js":
					workerFetches.Add(1)
					w.Header().Set("Content-Type", "application/javascript")
					_, _ = fmt.Fprint(w, `self.addEventListener('install', event => event.waitUntil(self.skipWaiting()));
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));
self.addEventListener('fetch', event => { if (new URL(event.request.url).pathname === '/sw-served') event.respondWith(new Response('from-worker')) });`)
				case "/sw-served":
					// Reached only when the worker did not answer.
					servedFetches.Add(1)
					_, _ = fmt.Fprint(w, "from-network")
				default:
					w.Header().Set("Content-Type", "text/html")
					_, _ = fmt.Fprint(w, `<!doctype html><title>workers</title><button id="go" onclick="navigator.serviceWorker.register('/sw.js').then(() => navigator.serviceWorker.ready).then(() => new Promise(resolve => navigator.serviceWorker.controller ? resolve() : navigator.serviceWorker.addEventListener('controllerchange', resolve))).then(() => fetch('/sw-served')).then(response => response.text()).then(text => document.querySelector('#out').textContent = 'settled:' + text, error => document.querySelector('#out').textContent = 'settled:error ' + error)">go</button><div id="out">pending</div>`)
				}
			}))
			defer server.Close()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "register", Type: "browser_click", Selector: "#go"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "wait", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "settled:", TimeoutMs: 15000}})
			outcome, err := b.currentPage().Locator("#out").TextContent()
			if err != nil {
				t.Fatal(err)
			}
			if outcome != "settled:from-worker" || workerFetches.Load() == 0 || servedFetches.Load() != 0 {
				t.Fatalf("the worker did not answer: %q, worker script fetched %d times, network answered %d times", outcome, workerFetches.Load(), servedFetches.Load())
			}
			entry := acceptanceEntry(t, b, "/sw-served")
			evidence := entry.BrowserEvidence
			served := evidence.ServiceWorker != nil && *evidence.ServiceWorker
			if !served {
				t.Fatalf("a worker-served response lacks service-worker provenance: %+v", evidence)
			}
		})
	}
}
