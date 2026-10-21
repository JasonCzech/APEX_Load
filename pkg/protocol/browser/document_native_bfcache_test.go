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
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// A separate process owns this alternate launch. The normal pinned driver
// disables BFCache and uses headless shell. This fixture uses bundled full
// Chromium and removes that flag; it must never qualify
// the default launch configuration or replace a synthetic event with a claim.
func TestBrowserDocumentNativeBFCacheRestore(t *testing.T) {
	const childFlag = "APEX_DOCUMENT_NATIVE_BFCACHE_CHILD"
	if os.Getenv(childFlag) != "1" {
		for _, profile := range []string{"", "pixel_8"} {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-test.timeout=70s")
			child.Env = append(os.Environ(), childFlag+"=1", "APEX_DOCUMENT_NATIVE_BFCACHE_PROFILE="+profile)
			child.WaitDelay = 5 * time.Second
			output, err := child.CombinedOutput()
			t.Logf("isolated BFCache fixture profile=%q:\n%s", profile, output)
			if err != nil {
				t.Fatalf("BFCache fixture: %v deadline=%v", err, ctx.Err())
			}
			if strings.Contains(string(output), "--- SKIP: "+t.Name()+" ") {
				t.Skip("native Chromium unavailable")
			}
			if !strings.Contains(string(output), "--- PASS: "+t.Name()+" ") {
				t.Fatal("fixture did not complete")
			}
		}
		return
	}
	profile := os.Getenv("APEX_DOCUMENT_NATIVE_BFCACHE_PROFILE")
	if profile != "" && profile != "pixel_8" {
		t.Fatal("unknown fixture profile")
	}
	t.Setenv("APEXLOAD_BROWSER_EXECUTABLE", "")
	pw, err := playwright.Run()
	if err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	opts := chromiumLaunchOptions(true, "")
	opts.IgnoreDefaultArgs = []string{"--disable-back-forward-cache"}
	opts.Channel = playwright.String("chromium")
	native, err := pw.Chromium.Launch(opts)
	if err != nil {
		_ = pw.Stop()
		requireBrowserRuntime(t, "chromium", err)
	}
	globalMutex.Lock()
	globalPlaywright = pw
	globalBrowsers[browserPoolKey("chromium", true, browserQUICAuto)] = native
	globalMutex.Unlock()
	t.Cleanup(func() {
		if !ShutdownPool(5 * time.Second) {
			t.Error("alternate launch pool did not shut down")
		}
	})
	var initialRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "max-age=3600")
		if r.URL.Path == "/first" {
			initialRequests.Add(1)
		}
		fmt.Fprint(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="icon" href="data:,"><h1>Native lifecycle</h1><script>window.marker=crypto.randomUUID();window.shows=[];addEventListener('pageshow',e=>shows.push({persisted:e.persisted,trusted:e.isTrusted}));</script>`)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	b := &Browser{}
	config := map[string]any{"base_url": server.URL, "browser_engine": "chromium", "browser_headless": true, "browser_capture_document_metrics": true, "browser_runtime_new_user_policy": "persistent_context"}
	if profile == "" {
		config["browser_viewport_width"], config["browser_viewport_height"] = 1280, 900
	} else {
		config["browser_device_profile"] = profile
	}
	if err := b.Init(ctx, config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	b.mu.Lock()
	cohort := b.evidence.cohort.Clone()
	b.mu.Unlock()
	if cohort == nil || !cohort.Valid() {
		t.Fatal("native cohort unavailable")
	}
	t.Logf("alternate native BFCache cohort engine=chromium profile=%q browser=%s platform=%s viewport=%dx%d dpr=%g channel=chromium", profile, native.Version(), cohort.Platform, cohort.ViewportWidth, cohort.ViewportHeight, cohort.DeviceScaleFactor)
	cdp, err := b.currentBrowserContext().NewCDPSession(b.currentPage())
	if err != nil {
		t.Fatal(err)
	}
	defer cdp.Detach()
	refusals := make(chan map[string]any, 4)
	cdp.On("Page.backForwardCacheNotUsed", func(row map[string]any) {
		select {
		case refusals <- row:
		default:
		}
	})
	if _, err := cdp.Send("Page.enable", nil); err != nil {
		t.Fatal(err)
	}
	documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_navigate", Target: "/first"})
	var before struct {
		Marker        string
		Origin        float64
		Width, Height int
		DPR           float64
	}
	if err := b.evaluatePageExpression(ctx, `({marker,origin:performance.timeOrigin,width:innerWidth,height:innerHeight,dpr:devicePixelRatio})`, &before); err != nil {
		t.Fatal(err)
	}
	if before.Width != cohort.ViewportWidth || before.Height != cohort.ViewportHeight || before.DPR != cohort.DeviceScaleFactor {
		t.Fatalf("page differs from configured cohort: %+v", before)
	}
	var original protocol.BrowserDocumentObservation
	documentActualWait(t, func() bool {
		for _, o := range b.BrowserDocumentEvidence().Observations {
			if o.Metric == "fcp" && o.FrameScope == "main_frame" && o.Available && o.State == "final" {
				original = o
				return true
			}
		}
		return false
	})
	documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_navigate", Target: "/second"})
	if _, err := b.currentPage().GoBack(playwright.PageGoBackOptions{WaitUntil: playwright.WaitUntilStateCommit, Timeout: playwright.Float(10000)}); err != nil {
		t.Fatal(err)
	}
	var restored struct {
		Marker string
		Origin float64
		Shows  []struct{ Persisted, Trusted bool }
	}
	documentActualWait(t, func() bool {
		if err := b.evaluatePageExpression(ctx, `({marker,origin:performance.timeOrigin,shows})`, &restored); err != nil {
			t.Fatal(err)
		}
		return len(restored.Shows) > 1 || restored.Marker != before.Marker
	})
	if restored.Marker != before.Marker || restored.Origin != before.Origin || len(restored.Shows) != 2 || !restored.Shows[1].Persisted || !restored.Shows[1].Trusted || initialRequests.Load() != 1 {
		select {
		case row := <-refusals:
			t.Logf("native CDP BFCache refusal: %+v", row)
		case <-time.After(time.Second):
			t.Log("no native CDP refusal event")
		}
		var reasons any
		_ = b.evaluatePageExpression(ctx, `performance.getEntriesByType("navigation")[0]?.notRestoredReasons?.toJSON()`, &reasons)
		t.Logf("native BFCache refusal reasons: %+v", reasons)
		t.Fatalf("no genuine native BFCache restore: before=%+v after=%+v first_requests=%d", before, restored, initialRequests.Load())
	}
	documentActualWait(t, func() bool {
		for _, o := range b.BrowserDocumentEvidence().Observations {
			if o.DocumentID == original.DocumentID && o.LifecyclePhase == "bfcache_restore" && o.Metric == "fcp" && o.Available {
				return true
			}
		}
		return false
	})
	if err := b.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	lifecycles := map[string]bool{}
	foundOriginal := false
	for _, o := range b.BrowserDocumentEvidence().Observations {
		if o.DocumentID != original.DocumentID {
			continue
		}
		if seen[o.MeasurementID] {
			t.Fatal("duplicated lifecycle measurement")
		}
		seen[o.MeasurementID] = true
		lifecycles[o.LifecycleID] = true
		if o.MeasurementID == original.MeasurementID {
			foundOriginal = true
			if !reflect.DeepEqual(original, o) {
				t.Fatal("restore revised a completed FCP observation")
			}
		}
		if o.State == "provisional" {
			t.Fatal("teardown left a provisional measurement")
		}
		if o.LifecyclePhase == "bfcache_restore" && o.LifecycleID == original.LifecycleID {
			t.Fatal("restore reused initial lifecycle")
		}
	}
	if !foundOriginal || len(lifecycles) != 2 {
		t.Fatalf("original=%t lifecycles=%d", foundOriginal, len(lifecycles))
	}
	t.Logf("native BFCache verified with trusted persisted pageshow, preserved heap marker/time origin, one initial target request; browser=%s; alternate launch uses bundled full Chromium channel and removes --disable-back-forward-cache", native.Version())
}
