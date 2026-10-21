package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// These fixtures exercise native failure callbacks, not synthetic lifecycle
// events. They qualify only the engine/platform actually logged by the runtime.
func documentFailureFixture(t *testing.T, engine, profile string) (*Browser, context.Context, map[string]protocol.BrowserDocumentObservation) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>failure evidence</title><h1>Painted before the source disappears</h1>`)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	b := &Browser{}
	config := map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_capture_document_metrics": true, "browser_runtime_new_user_policy": "persistent_context"}
	if profile == "" {
		config["browser_viewport_width"], config["browser_viewport_height"] = 1280, 900
	} else {
		config["browser_device_profile"] = profile
	}
	if err := b.Init(ctx, config); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	b.mu.Lock()
	cohort := b.evidence.cohort.Clone()
	b.mu.Unlock()
	if cohort == nil || !cohort.Valid() {
		t.Fatal("missing native runtime cohort")
	}
	if profile != "" && (cohort.Mobile == nil || !*cohort.Mobile || cohort.Touch == nil || !*cohort.Touch) {
		t.Fatalf("mobile profile did not enable mobile/touch emulation: %+v", cohort)
	}
	t.Logf("native failure cohort engine=%s profile=%q browser_version=%s viewport=%dx%d dpr=%g platform=%s", engine, profile, cohort.BrowserVersion, cohort.ViewportWidth, cohort.ViewportHeight, cohort.DeviceScaleFactor, cohort.Platform)
	documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	// Independently read the real page: configuration labels alone cannot
	// prove that the native context applied its viewport or scale. Native touch
	// capability is logged independently; WebKit may report zero even for a
	// touch-enabled emulation context. This test does not qualify touch dispatch.
	var native struct {
		Width  int     `json:"width"`
		Height int     `json:"height"`
		DPR    float64 `json:"dpr"`
		Touch  *int    `json:"touch"`
	}
	if err := b.evaluatePageExpression(ctx, `({width:innerWidth,height:innerHeight,dpr:devicePixelRatio,touch:navigator.maxTouchPoints})`, &native); err != nil {
		t.Fatal(err)
	}
	if native.Width != cohort.ViewportWidth || native.Height != cohort.ViewportHeight || native.DPR != cohort.DeviceScaleFactor {
		t.Fatalf("native page disagrees with requested cohort: page=%+v cohort=%+v", native, cohort)
	}

	touch := "unavailable"
	if native.Touch != nil {
		touch = fmt.Sprint(*native.Touch)
	}
	t.Logf("native page oracle viewport=%dx%d dpr=%g max_touch_points=%s", native.Width, native.Height, native.DPR, touch)
	var before map[string]protocol.BrowserDocumentObservation
	documentActualWait(t, func() bool {
		evidence := b.BrowserDocumentEvidence()
		var doc string
		for _, o := range evidence.Observations {
			if o.FrameScope == "main_frame" && o.Metric == "fcp" && o.State == "final" && o.Available {
				doc = o.DocumentID
			}
		}
		if doc == "" {
			return false
		}
		before = map[string]protocol.BrowserDocumentObservation{}
		pending := 0
		for _, o := range evidence.Observations {
			if o.DocumentID != doc {
				continue
			}
			if o.DeviceProfile != profile {
				t.Fatalf("profile identity changed: got %q want %q", o.DeviceProfile, profile)
			}
			before[o.MeasurementID] = o
			if (o.Metric == "cls" || o.Metric == "inp") && o.State == "provisional" {
				pending++
			}
		}
		return pending == 2 && len(before) == 5
	})
	return b, ctx, before
}

func assertDocumentFailurePopulation(t *testing.T, b *Browser, before map[string]protocol.BrowserDocumentObservation, boundary string) map[string]protocol.BrowserDocumentObservation {
	t.Helper()
	documents := map[string]bool{}
	for _, o := range before {
		documents[o.DocumentID] = true
	}
	var after map[string]protocol.BrowserDocumentObservation
	documentActualWait(t, func() bool {
		after = map[string]protocol.BrowserDocumentObservation{}
		for _, o := range b.BrowserDocumentEvidence().Observations {
			if documents[o.DocumentID] {
				if _, known := before[o.MeasurementID]; !known {
					t.Fatalf("failure created a new measurement population: %+v", o)
				}
				if _, duplicate := after[o.MeasurementID]; duplicate {
					t.Fatalf("duplicate native failure population: %s", o.MeasurementID)
				}
				after[o.MeasurementID] = o
			}
		}
		if len(after) != len(before) {
			return false
		}
		for id, old := range before {
			if old.State == "provisional" && after[id].State == "provisional" {
				return false
			}
		}
		return true
	})
	for id, old := range before {
		got := after[id]
		if !got.Valid() {
			t.Fatalf("invalid native failure observation: %+v", got)
		}
		if got.DocumentID != old.DocumentID || got.LifecycleID != old.LifecycleID || got.SourceGeneration != old.SourceGeneration || !got.WindowStart.Equal(old.WindowStart) {
			t.Fatalf("native failure replaced the observation identity: before=%+v after=%+v", old, got)
		}
		if old.State == "final" {
			if !reflect.DeepEqual(old, got) {
				t.Fatalf("completed metric changed after %s: before=%+v after=%+v", boundary, old, got)
			}
			continue
		}
		// Closing a native page can hide it first. LCP legitimately reaches
		// its own hidden boundary before close; that is not a completed
		// CLS/INP lifecycle, which must remain censored here.
		if boundary == "context_closed" && old.Metric == "lcp" && got.State == "final" && got.Boundary == "hidden" && got.Available && got.Value != nil {
			continue
		}
		if got.State != "censored" || got.Boundary != boundary || got.Value != nil || got.Available || got.Qualified || got.UnavailableReason != "source_censored" || got.Revision <= old.Revision {
			t.Fatalf("unfinished measurement gained finality or lost its censor: before=%+v after=%+v", old, got)
		}
	}
	return after
}

func TestBrowserDocumentNativePageClosePreservesFinalAndCensorsPending(t *testing.T) {
	for _, tc := range []struct{ engine, profile string }{
		{"chromium", ""}, {"webkit", ""}, {"chromium", "pixel_8"}, {"webkit", "iphone_15_pro"},
	} {
		t.Run(tc.engine+"/"+tc.profile, func(t *testing.T) {
			b, _, before := documentFailureFixture(t, tc.engine, tc.profile)
			// This bypasses the managed finalizer: it is an external native page close,
			// not successful controller teardown and not a JavaScript pagehide fixture.
			if err := b.currentPage().Close(); err != nil {
				t.Fatal(err)
			}
			after := assertDocumentFailurePopulation(t, b, before, "context_closed")
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := assertDocumentFailurePopulation(t, b, before, "context_closed"); !reflect.DeepEqual(got, after) {
				t.Fatal("later teardown revised the native close population")
			}
		})
	}
}

func TestBrowserDocumentNativeRendererCrashPreservesFinalAndCensorsPending(t *testing.T) {
	b, _, before := documentFailureFixture(t, "chromium", "")
	page := b.currentPage()
	session, err := page.Context().NewCDPSession(page)
	if err != nil {
		t.Fatal(err)
	}
	crashed := make(chan struct{}, 1)
	page.OnCrash(func(playwright.Page) {
		select {
		case crashed <- struct{}{}:
		default:
		}
	})
	// Page.crash may not reply until the crashed page closes. Always close the
	// owned context and join the command worker, including assertion failures.
	joined := make(chan struct{})
	go func() { defer close(joined); _, _ = session.Send("Page.crash", nil) }()
	t.Cleanup(func() {
		_ = b.Teardown(context.Background())
		_ = session.Detach()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("native crash command worker did not join")
		}
	})
	select {
	case <-crashed:
	case <-time.After(10 * time.Second):
		t.Fatal("native renderer crash event not observed")
	}
	after := assertDocumentFailurePopulation(t, b, before, "crash")
	if err := b.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := assertDocumentFailurePopulation(t, b, before, "crash"); !reflect.DeepEqual(got, after) {
		t.Fatal("later teardown revised the native crash population")
	}
}

// Run in a separate test process: Browser.crash terminates the whole pooled
// Chromium process, including every context it owns. The fixture must never
// terminate a browser used by another test or an operator's browser.
func TestBrowserDocumentNativeBrowserCrashPreservesContextsAndReplacesPool(t *testing.T) {
	const childFlag = "APEX_DOCUMENT_BROWSER_CRASH_CHILD"
	if os.Getenv(childFlag) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-test.timeout=70s")
		// Bound pipe draining too: a timed-out child must not leave this test
		// waiting on a driver descendant that inherited an output handle.
		child.WaitDelay = 5 * time.Second
		child.Env = append(os.Environ(), childFlag+"=1")
		output, err := child.CombinedOutput()
		t.Logf("isolated native browser loss:\n%s", output)
		if err != nil {
			t.Fatalf("native browser loss subprocess: %v (deadline: %v)", err, ctx.Err())
		}
		if strings.Contains(string(output), "--- SKIP: "+t.Name()+" ") {
			t.Skip("native browser runtime unavailable in isolated subprocess")
		}
		if !strings.Contains(string(output), "--- PASS: "+t.Name()+" ") {
			t.Fatal("native browser subprocess exited without completing the fixture")
		}
		return
	}

	desktop, _, desktopBefore := documentFailureFixture(t, "chromium", "")
	mobile, _, mobileBefore := documentFailureFixture(t, "chromium", "pixel_8")
	native := desktop.currentBrowserContext().Browser()
	if native == nil || native != mobile.currentBrowserContext().Browser() {
		t.Fatal("fixture contexts must share the same owned browser process")
	}
	session, err := native.NewBrowserCDPSession()
	if err != nil {
		t.Fatal(err)
	}
	disconnected := make(chan struct{}, 1)
	native.OnDisconnected(func(playwright.Browser) {
		select {
		case disconnected <- struct{}{}:
		default:
		}
	})
	joined := make(chan struct{})
	// The command cannot reply normally because it kills its browser. The
	// driver can leave it pending even after disconnection; retain its worker
	// until the isolated driver stops after all recovery assertions.
	go func() { defer close(joined); _, _ = session.Send("Browser.crash", nil) }()
	t.Cleanup(func() {
		if !ShutdownPool(5 * time.Second) {
			t.Error("isolated browser pool did not close within its shutdown budget")
		}
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("browser crash command worker did not join")
		}
	})
	select {
	case <-disconnected:
	case <-time.After(10 * time.Second):
		t.Fatal("native browser disconnect event not observed")
	}
	if native.IsConnected() {
		t.Fatal("crashed native browser remains connected")
	}
	// Playwright reports process loss as native page/context closure. Do not
	// invent a renderer-crash boundary or successful document finalization.
	desktopAfter := assertDocumentFailurePopulation(t, desktop, desktopBefore, "context_closed")
	mobileAfter := assertDocumentFailurePopulation(t, mobile, mobileBefore, "context_closed")

	replacement, replacementCtx, _ := documentFailureFixture(t, "chromium", "")
	fresh := replacement.currentBrowserContext().Browser()
	if fresh == native || !fresh.IsConnected() {
		t.Fatal("the pool reused its disconnected browser instead of launching a replacement")
	}
	oldGenerations := map[string]bool{}
	for _, population := range []map[string]protocol.BrowserDocumentObservation{desktopBefore, mobileBefore} {
		for _, o := range population {
			oldGenerations[o.SourceGeneration] = true
		}
	}
	if len(oldGenerations) != 2 {
		t.Fatal("shared-browser contexts did not retain separate source generations")
	}
	freshEvidence := replacement.BrowserDocumentEvidence()
	if oldGenerations[freshEvidence.SourceGeneration] || freshEvidence.SourceGeneration == "" {
		t.Fatal("replacement browser adopted a lost source generation")
	}
	for _, o := range freshEvidence.Observations {
		if o.SourceGeneration != freshEvidence.SourceGeneration {
			t.Fatal("replacement browser mixed observations from another generation")
		}
	}
	for _, tc := range []struct {
		name          string
		browser       *Browser
		before, after map[string]protocol.BrowserDocumentObservation
	}{{"desktop", desktop, desktopBefore, desktopAfter}, {"pixel_8", mobile, mobileBefore, mobileAfter}} {
		if err := tc.browser.Teardown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := assertDocumentFailurePopulation(t, tc.browser, tc.before, "context_closed"); !reflect.DeepEqual(got, tc.after) {
			t.Fatalf("pool replacement or later teardown revised the lost %s population", tc.name)
		}
	}
	var title string
	if err := replacement.evaluatePageExpression(replacementCtx, "document.title", &title); err != nil || title != "failure evidence" || !fresh.IsConnected() {
		t.Fatalf("lost-context teardown damaged the replacement: title=%q err=%v connected=%t", title, err, fresh.IsConnected())
	}
	t.Log("native Chromium browser process loss: two contexts censored; final observations preserved; fresh pool replacement observed; old populations unchanged; replacement survives lost-context teardown")
}
