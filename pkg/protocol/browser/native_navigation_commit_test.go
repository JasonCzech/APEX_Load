package browser

import (
	"context"
	"fmt"
	playwright "github.com/mxschmitt/playwright-go"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserNativeNavigationCommitIdentity(t *testing.T) {
	for _, tc := range []struct{ engine, profile string }{{"chromium", ""}, {"chromium", "pixel_8"}, {"webkit", ""}, {"webkit", "iphone_15_pro"}} {
		t.Run(tc.engine+"/"+tc.profile, func(t *testing.T) {
			var requests atomic.Int64
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Cache-Control", "no-store")
				if r.URL.Path == "/redirect" {
					http.Redirect(w, r, "/final", http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><link rel="icon" href="data:,"><h1>Native document</h1>`)
			}))
			defer target.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b := &Browser{}
			if err := b.Init(ctx, map[string]any{"browser_engine": tc.engine, "browser_device_profile": tc.profile, "browser_headless": true, "browser_capture_document_metrics": true}); err != nil {
				requireBrowserRuntime(t, tc.engine, err)
			}
			t.Cleanup(func() { _ = b.Teardown(context.Background()) })
			page := b.currentPage()
			var last playwright.APEXNavigationCommit
			for _, path := range []string{"/", "/", "/redirect"} {
				response, err := page.Goto(target.URL+path, playwright.PageGotoOptions{Timeout: playwright.Float(5000)})
				if err != nil || response == nil {
					t.Fatal("navigation", err)
				}
				before := playwright.APEXFrameNavigationCommit(page.MainFrame())
				if before.Request != response.Request() || before.Sequence <= last.Sequence {
					t.Fatalf("native response not bound to observed commit: %+v", before)
				}
				if last.Request != nil && before.Request == last.Request {
					t.Fatal("new request reused prior identity")
				}
				b.mu.Lock()
				finalizer := b.evidence.finalizer
				b.mu.Unlock()
				identity, err := page.Evaluate(fmt.Sprintf(`() => globalThis[%q]?.identity?.()`, finalizer))
				if err != nil {
					t.Fatal(err)
				}
				after := playwright.APEXFrameNavigationCommit(page.MainFrame())
				if after.Sequence != before.Sequence || after.Request != before.Request {
					t.Fatal("commit changed while observing DOM")
				}
				object, ok := identity.(map[string]any)
				if !ok || object["document_token"] == "" || object["document_token"] == nil || object["lifecycle_token"] == "" || object["lifecycle_token"] == nil {
					t.Fatalf("missing actual document identity: %#v", identity)
				}
				last = after
			}
			response, err := page.Goto(target.URL+"/final#fragment", playwright.PageGotoOptions{Timeout: playwright.Float(5000)})
			if err != nil || response != nil {
				t.Fatal("same-document response", err)
			}
			same := playwright.APEXFrameNavigationCommit(page.MainFrame())
			if same.Sequence <= last.Sequence || same.Request != nil {
				t.Fatal("same-document event retained new-document proof", same)
			}
			if requests.Load() != 4 {
				t.Fatalf("capture added target traffic: %d", requests.Load())
			}
		})
	}
}
