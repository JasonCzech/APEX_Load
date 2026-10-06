package browser

import (
	"context"
	playwright "github.com/mxschmitt/playwright-go"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHAROwnershipNativeOnCloseRepeatCannotBlockOwnedFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("native callback ownership requires installed browsers")
	}
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			var requests, callbacks atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<!doctype html><title>callback ownership fixture</title>"))
			}))
			defer server.Close()
			// This fixture owns its driver. A regression can close only this driver's
			// stdin/Wait to unblock outstanding acknowledgments; the pool is untouched.
			pw, err := playwright.Run()
			if err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			var stopOnce sync.Once
			var stopErr error
			stop := func() { stopOnce.Do(func() { stopErr = pw.Stop() }) }
			defer stop()
			var nativeBrowser playwright.Browser
			if engine == "webkit" {
				nativeBrowser, err = pw.WebKit.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
			} else {
				nativeBrowser, err = pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
			}
			if err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			b := &Browser{harDir: t.TempDir(), harCapturePolicy: HARCaptureAlways}
			options := playwright.BrowserNewContextOptions{}
			if err := b.applyHARCaptureOptions(&options); err != nil {
				t.Fatal(err)
			}
			owner := b.harOwner
			var callbackDone chan struct{}
			defer func() {
				// Stop is called once on this exclusively owned driver, after ordinary
				// completion or while its exact dispatcher callback is blocked. It joins
				// the actual Node process before retained raw descriptors are released.
				stop()
				if stopErr != nil {
					t.Error("owned driver stop did not join", stopErr)
					return
				}
				if callbackDone != nil {
					select {
					case <-callbackDone:
					case <-time.After(3 * time.Second):
						t.Error("owned close callback did not join")
						return
					}
				}
				_ = b.Teardown(context.Background())
				_, _ = owner.waitFinish(context.Background())
				_ = owner.closeDescriptors()
				owner.releaseReservation()
			}()
			owner.beginWriter()
			native, err := nativeBrowser.NewContext(options)
			if err != nil {
				t.Fatal(err)
			}
			owner.bindWriter(func() error { return closeOwnedHARBrowserContext(native) })
			b.setBrowserContext(native)
			page, err := native.NewPage()
			if err != nil {
				t.Fatal(err)
			}
			b.pwPage = page
			if requests.Load() != 0 {
				t.Fatal("context admission generated target traffic")
			}
			if _, err := page.Goto(server.URL); err != nil {
				t.Fatal(err)
			}
			callbackDone = make(chan struct{})
			var callbackErr error
			native.OnClose(func(ctx playwright.BrowserContext) {
				callbacks.Add(1)
				callbackErr = closeOwnedHARBrowserContext(ctx)
				close(callbackDone)
			})
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal("dispatcher repeat prevented original actual flush/join", err)
			}
			select {
			case <-callbackDone:
			case <-time.After(time.Second):
				t.Fatal("native close callback was never joined")
			}
			if callbackErr == nil || callbacks.Load() != 1 {
				t.Fatal("unfinished native callback claimed a completed receipt", callbackErr, callbacks.Load())
			}
			if !owner.retiredSuccessfully() || b.lastHARArtifactPath == "" || requests.Load() != 1 {
				t.Fatal("first owner was poisoned or target counter changed", requests.Load())
			}
			if err := closeOwnedHARBrowserContext(native); err != nil {
				t.Fatal("completed native receipt was not retained", err)
			}
			if _, err := os.Stat(filepath.Clean(owner.path)); !os.IsNotExist(err) {
				t.Fatal("joined first owner retained raw output", err)
			}
		})
	}
}
