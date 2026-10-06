package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func documentActualBrowser(t *testing.T, engine, baseURL string) (*Browser, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	b := &Browser{}
	if err := b.Init(ctx, map[string]any{"base_url": baseURL, "browser_engine": engine, "browser_headless": true, "browser_capture_document_metrics": true, "browser_runtime_new_user_policy": "persistent_context", "browser_viewport_width": 1280, "browser_viewport_height": 900}); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	b.mu.Lock()
	cohort := b.evidence.cohort.Clone()
	b.mu.Unlock()
	t.Logf("actual native runtime engine=%s browser_version=%s viewport=%dx%d platform=%s", engine, cohort.BrowserVersion, cohort.ViewportWidth, cohort.ViewportHeight, cohort.Platform)
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	var errorMu sync.Mutex
	var pageErrors []string
	b.currentPage().OnPageError(func(err error) {
		errorMu.Lock()
		defer errorMu.Unlock()
		if len(pageErrors) < 4 {
			message := err.Error()
			if len(message) > 256 {
				message = message[:256]
			}
			pageErrors = append(pageErrors, message)
		}
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		b.mu.Lock()
		documents, frames, issued := len(b.evidence.documents), len(b.evidence.frames), b.evidence.issued
		states := make(map[string]int)
		for _, document := range b.evidence.documents {
			for _, observation := range document.observations {
				states[observation.FrameScope+"/"+observation.Metric+"/"+observation.State+"/"+observation.Boundary+"/"+observation.UnavailableReason]++
			}
		}
		b.mu.Unlock()
		b.mu.Lock()
		version := ""
		if b.evidence.cohort != nil {
			version = b.evidence.cohort.BrowserVersion
		}
		b.mu.Unlock()
		errorMu.Lock()
		errors := append([]string(nil), pageErrors...)
		errorMu.Unlock()
		t.Logf("owned fixture diagnostics documents=%d frames=%d issued=%d browser_version=%s errors=%q observation_states=%v", documents, frames, issued, version, errors, states)
	})
	return b, ctx
}
func documentActualExecute(t *testing.T, b *Browser, ctx context.Context, action *protocol.Action) *protocol.Result {
	t.Helper()
	result, err := b.Execute(ctx, action)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("actual browser action %s failed: %v %+v", action.Type, err, result)
	}
	return result
}
func documentActualWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("actual browser evidence barrier was not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func documentActualMetric(t *testing.T, e *protocol.BrowserDocumentEvidence, metric, scope string) []protocol.BrowserDocumentObservation {
	t.Helper()
	if e == nil || e.Clone() == nil {
		t.Fatal("native evidence unavailable or malformed")
	}
	var result []protocol.BrowserDocumentObservation
	for _, o := range e.Observations {
		if o.Metric == metric && o.FrameScope == scope {
			result = append(result, o)
		}
	}
	return result
}
func TestBrowserDocumentActualNativeIdentityAndNoInteraction(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			var mu sync.Mutex
			requests := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests[r.URL.Path]++
				mu.Unlock()
				w.Header().Set("Content-Type", "text/html")
				switch r.URL.Path {
				case "/":
					_, _ = fmt.Fprint(w, `<!doctype html><title>same-url frames</title><iframe src="/frame"></iframe><iframe src="/frame"></iframe><body>main evidence</body>`)
				case "/frame":
					_, _ = fmt.Fprint(w, `<!doctype html><title>frame</title><body>same-url child evidence</body>`)
				default:
					w.WriteHeader(204)
				}
			}))
			defer server.Close()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate first", Type: "browser_navigate", Target: "/"})
			documentActualWait(t, func() bool {
				e := b.BrowserDocumentEvidence()
				return e != nil && len(documentActualMetric(t, e, "ttfb", "child_frame")) >= 2
			})
			before := b.BrowserDocumentEvidence()
			children := documentActualMetric(t, before, "ttfb", "child_frame")
			if len(children) != 2 || children[0].DocumentID == children[1].DocumentID || children[0].FrameID == children[1].FrameID {
				t.Fatal("same-URL concurrent frames collapsed")
			}
			beforeMain := map[string]bool{}
			for _, o := range documentActualMetric(t, before, "ttfb", "main_frame") {
				beforeMain[o.DocumentID] = true
			}
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "reload same URL", Type: "browser_navigate", Target: "/"})
			documentActualWait(t, func() bool {
				e := b.BrowserDocumentEvidence()
				for _, o := range documentActualMetric(t, e, "ttfb", "main_frame") {
					if !beforeMain[o.DocumentID] {
						return true
					}
				}
				return false
			})
			if err := b.Teardown(ctx); err != nil {
				t.Fatal(err)
			}
			evidence := b.BrowserDocumentEvidence()
			var unavailable int
			for _, o := range evidence.Observations {
				if o.Metric != "inp" {
					continue
				}
				if o.State == "final" {
					unavailable++
					if o.Available || o.Qualified || o.Value != nil || o.UnavailableReason != "no_interaction" && o.UnavailableReason != "unsupported" {
						t.Fatalf("no-interaction INP invented value: %+v", o)
					}
				}
				if o.State == "provisional" {
					t.Fatal("teardown left uncensored provisional observation")
				}
			}
			if unavailable < 3 {
				t.Fatal("independent frame INP finality missing")
			}
			mu.Lock()
			defer mu.Unlock()
			for path := range requests {
				if path != "/" && path != "/frame" && path != "/favicon.ico" {
					t.Fatalf("observer added unapproved fixture target fetch %q", path)
				}
			}
			t.Logf("owned target request counters: %#v", requests)
			if requests["/"] != 2 || requests["/frame"] != 4 {
				t.Fatalf("observer produced unexpected target fetches: %#v", requests)
			}
		})
	}
}

func TestBrowserDocumentActualTrustedInteractionRank(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>trusted interaction oracle</title><button id="interact">interact</button><script>
 window.fixtureFirstInputs=[];new PerformanceObserver(list=>fixtureFirstInputs.push(...list.getEntries().map(e=>({id:e.interactionId,duration:e.duration,time:e.startTime})))).observe({type:'first-input',buffered:true});
 window.fixtureEvents=[];new PerformanceObserver(list=>fixtureEvents.push(...list.getEntries().map(e=>({id:e.interactionId,duration:e.duration})))).observe({type:'event',buffered:true,durationThreshold:0});
 let calls=0;document.querySelector('#interact').onclick=()=>{let end=performance.now()+(calls===0?300:calls===1?200:50);calls++;while(performance.now()<end){};};
 </script>`)
	}))
	defer server.Close()
	b, ctx := documentActualBrowser(t, "chromium", server.URL)
	documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
	for i := 0; i < 50; i++ {
		documentActualExecute(t, b, ctx, &protocol.Action{Name: "trusted click", Type: "browser_click", Selector: "#interact"})
	}
	var oracle struct {
		Count       int `json:"count"`
		FirstInputs []struct {
			ID       int64   `json:"id"`
			Duration float64 `json:"duration"`
			Time     float64 `json:"time"`
		} `json:"first_inputs"`
		Events []struct {
			ID       int64   `json:"id"`
			Duration float64 `json:"duration"`
		} `json:"events"`
	}
	documentActualWait(t, func() bool {
		if b.evaluatePageExpression(ctx, `({count:performance.interactionCount,events:window.fixtureEvents,first_inputs:window.fixtureFirstInputs})`, &oracle) != nil {
			return false
		}
		return oracle.Count >= 50 && len(oracle.Events) > 0
	})
	grouped := map[int64]float64{}
	for _, event := range oracle.Events {
		if event.ID != 0 {
			grouped[event.ID] = max(grouped[event.ID], event.Duration)
		}
	}
	values := make([]float64, 0, len(grouped))
	for _, value := range grouped {
		values = append(values, value)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(values)))
	if len(values) < 2 {
		t.Fatal("native oracle did not observe multiple trusted interactions")
	}
	rank := min(len(values)-1, oracle.Count/50)
	expected := values[rank]
	for _, o := range documentActualMetric(t, b.BrowserDocumentEvidence(), "inp", "main_frame") {
		if o.Available && o.Value != nil {
			t.Logf("pre-final native source value=%g interaction_count=%d settled_oracle_count=%d", *o.Value, o.InteractionCount, oracle.Count)
		}
	}
	if err := b.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range documentActualMetric(t, b.BrowserDocumentEvidence(), "inp", "main_frame") {
		if o.State == "final" && o.Available && o.InteractionCount >= 50 {
			found = true
			if o.Value == nil || *o.Value != expected {
				actual := float64(-1)
				if o.Value != nil {
					actual = *o.Value
				}
				t.Fatalf("INP differs from independent native interaction grouping/rank: actual=%g expected=%g count=%d source_count=%d top_native=%v first_inputs=%+v", actual, expected, oracle.Count, o.InteractionCount, values[:min(3, len(values))], oracle.FirstInputs)
			}
		}
	}
	if !found {
		t.Fatal("trusted interaction document final not retained")
	}
}

func TestBrowserDocumentActualCLSSessionWindows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>layout session oracle</title><div id="shift" style="width:200px;height:200px;background:blue">content</div><script>
 window.fixtureShifts=[];new PerformanceObserver(list=>fixtureShifts.push(...list.getEntries().filter(e=>!e.hadRecentInput).map(e=>({value:e.value,time:e.startTime})))).observe({type:'layout-shift',buffered:true});
 setTimeout(()=>document.querySelector('#shift').style.marginTop='100px',500);setTimeout(()=>document.querySelector('#shift').style.marginTop='200px',900);setTimeout(()=>document.querySelector('#shift').style.marginTop='0px',2200);
 </script>`)
	}))
	defer server.Close()
	b, ctx := documentActualBrowser(t, "chromium", server.URL)
	documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
	var shifts []struct {
		Value float64 `json:"value"`
		Time  float64 `json:"time"`
	}
	documentActualWait(t, func() bool {
		return b.evaluatePageExpression(ctx, `window.fixtureShifts`, &shifts) == nil && len(shifts) >= 3
	})
	var expected, sum, session, start, last float64
	for i, shift := range shifts {
		sum += shift.Value
		if i > 0 && shift.Time-last < 1000 && shift.Time-start < 5000 {
			session += shift.Value
		} else {
			session = shift.Value
			start = shift.Time
		}
		last = shift.Time
		expected = max(expected, session)
	}
	if expected <= 0 || sum <= expected {
		t.Fatal("native fixture does not distinguish maximum session from sum")
	}
	if err := b.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range documentActualMetric(t, b.BrowserDocumentEvidence(), "cls", "main_frame") {
		if o.State == "final" && o.Available {
			found = true
			if o.Value == nil || *o.Value != expected {
				t.Fatalf("CLS differs from native maximum session window: actual=%v expected=%g total=%g", o.Value, expected, sum)
			}
		}
	}
	if !found {
		t.Fatal("native CLS final missing")
	}
}

func TestBrowserResourceActualCompletionAcrossActions(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hold":
					once.Do(func() { close(entered) })
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					_, _ = fmt.Fprint(w, "complete")
				default:
					w.Header().Set("Content-Type", "text/html")
					_, _ = fmt.Fprint(w, `<!doctype html><title>held response</title><button id="fetch" onclick="fetch('/hold').then(r=>r.text()).then(t=>document.querySelector('#status').textContent=t)">fetch</button><div id="status">pending</div>`)
				}
			}))
			defer server.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "trigger", Type: "browser_click", Selector: "#fetch"})
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("genuine request not received")
			}
			var initiating string
			b.mu.Lock()
			for nativeRequest, request := range b.evidence.requests {
				if strings.HasSuffix(nativeRequest.URL(), "/hold") && request.evidence.State == "" && request.evidence.InitiatingActionID != "" {
					initiating = request.evidence.InitiatingActionID
				}
			}
			b.mu.Unlock()
			if initiating == "" {
				t.Fatal("native request initiation identity absent")
			}
			done := make(chan *protocol.Result, 1)
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				result, _ := b.Execute(ctx, &protocol.Action{Name: "wait completion", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "complete", TimeoutMs: 5000}})
				done <- result
			}()
			t.Cleanup(func() {
				_ = b.Teardown(context.Background())
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("owned action worker did not join")
				}
			})
			documentActualWait(t, func() bool {
				b.mu.Lock()
				defer b.mu.Unlock()
				return b.evidence.currentAction != "" && b.evidence.currentAction != initiating
			})
			close(release)
			select {
			case result := <-done:
				if result == nil || !result.Success {
					t.Fatalf("completion wait failed: %+v", result)
				}
			case <-ctx.Done():
				t.Fatal("completion action did not return")
			}
			documentActualWait(t, func() bool {
				for _, entry := range b.networkEntriesSince(0) {
					if strings.HasSuffix(entry.URL, "/hold") {
						e := entry.BrowserEvidence
						if e == nil || e.Clone() == nil || e.RequestID == "" || e.InitiatingActionID != initiating || e.ObservationActionID == "" || e.ObservationActionID == initiating || e.DocumentID != "" || e.IdentityUnavailableReason != "document_unavailable" || e.State != "finished" {
							t.Fatalf("resource native identity/finality was invented or lost: %+v", e)
						}
						return true
					}
				}
				return false
			})
			<-joined
		})
	}
}

func TestBrowserDocumentActualCancelledRunCannotInventFinality(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<!doctype html><title>cancelled run</title><body>source captured before cancellation</body>`)
			}))
			defer server.Close()
			runCtx, cancelRun := context.WithCancel(context.Background())
			defer cancelRun()
			b := &Browser{}
			if err := b.Init(runCtx, map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_capture_document_metrics": true}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			defer b.Teardown(context.Background())
			actionCtx, cancelAction := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancelAction()
			documentActualExecute(t, b, actionCtx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
			documentActualWait(t, func() bool { return len(documentActualMetric(t, b.BrowserDocumentEvidence(), "cls", "main_frame")) > 0 })
			cancelRun()
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
			var censored int
			for _, o := range b.BrowserDocumentEvidence().Observations {
				if o.Metric == "cls" || o.Metric == "inp" {
					if o.State != "censored" || o.Boundary != "cancelled" || o.Available || o.Qualified || o.Value != nil {
						t.Fatalf("cancelled lifecycle incorrectly finalized: %+v", o)
					}
					censored++
				}
			}
			if censored < 2 {
				t.Fatal("cancelled run lost source population")
			}
		})
	}
}
func TestBrowserDocumentActualBlockedDrainClosesOwnedContext(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<!doctype html><title>owned blocked drain</title><body>controlled loop fixture</body>`)
			}))
			defer server.Close()
			b, ctx := documentActualBrowser(t, engine, server.URL)
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
			if _, err := b.currentPage().Evaluate(`() => {setTimeout(()=>{console.log('owned-loop-active');while(true){}},100);return true}`); err != nil {
				t.Fatal("owned loop could not be scheduled")
			}
			documentActualWait(t, func() bool {
				for _, entry := range b.consoleEntriesSince(0) {
					if entry.Text == "owned-loop-active" {
						return true
					}
				}
				return false
			})
			started := time.Now()
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
			duration := time.Since(started)
			if duration < 1500*time.Millisecond || duration > 8*time.Second {
				t.Fatalf("actual held native drain did not obey existing2second owner budget: %s", duration)
			}
			var censored int
			for _, o := range b.BrowserDocumentEvidence().Observations {
				if o.Metric == "cls" || o.Metric == "inp" {
					if o.State != "censored" || o.Available || o.Qualified || o.Value != nil {
						t.Fatalf("blocked drain invented final metric: %+v", o)
					}
					censored++
				}
			}
			if censored < 2 {
				t.Fatal("blocked native drain lost censored population")
			}
			t.Logf("actual native held drain physically closed and joined in %s", duration)
		})
	}
}
