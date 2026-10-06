package browser

import (
	"context"
	"errors"
	playwright "github.com/mxschmitt/playwright-go"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHAROwnershipNativePriorCloseNeverCreatesJoinedProof(t *testing.T) {
	if testing.Short() {
		t.Skip("native HAR owned-close fixture requires installed browsers")
	}
	for _, engine := range []string{"chromium", "webkit"} {
		for _, mode := range []string{"inflight", "completed"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte("<!doctype html><title>owned close fixture</title>"))
				}))
				defer server.Close()
				b := &Browser{}
				if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_har_capture": "always", "browser_har_dir": t.TempDir()}); err != nil {
					requireBrowserRuntime(t, engine, err)
				}
				o, native := b.harOwner, b.pwContext
				if o == nil || native == nil {
					t.Fatal("native writer owner unavailable")
				}
				if requests.Load() != 0 {
					t.Fatal("context creation generated target traffic")
				}
				gate, dispatchEntered := make(chan struct{}), make(chan struct{})
				var releaseOnce, enteredOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(gate) }) }
				navigationDone, externalDone := make(chan struct{}), make(chan struct{})
				var navigationErr, externalErr error
				navigationStarted, externalStarted := false, false
				defer func() {
					release()
					if navigationStarted {
						select {
						case <-navigationDone:
						case <-time.After(3 * time.Second):
							t.Error("exact navigation worker did not join")
							return
						}
					}
					if externalStarted {
						select {
						case <-externalDone:
						case <-time.After(3 * time.Second):
							t.Error("exact external close worker did not join")
							return
						}
					}
					_ = b.Teardown(context.Background())
					// Only this fixture knows the separately owned actual Close joined. It
					// may release descriptors; production keeps unknown ownership charged.
					_ = o.closeDescriptors()
					o.releaseReservation()
				}()
				if mode == "inflight" {
					// Go SDK dispatch callbacks run synchronously. Holding this exact event
					// prevents the separate Close from receiving its Dispose acknowledgment
					// before HAR export, without altering/stopping a shared native driver.
					native.OnRequestFinished(func(request playwright.Request) {
						if strings.HasPrefix(request.URL(), server.URL) {
							enteredOnce.Do(func() { close(dispatchEntered) })
							<-gate
						}
					})
				}
				navigationStarted = true
				go func() { defer close(navigationDone); _, navigationErr = b.pwPage.Goto(server.URL) }()
				if mode == "inflight" {
					select {
					case <-dispatchEntered:
					case <-time.After(3 * time.Second):
						t.Fatal("controlled dispatch event absent")
					}
				} else {
					select {
					case <-navigationDone:
					case <-time.After(3 * time.Second):
						t.Fatal("exact navigation did not join")
					}
					if navigationErr != nil {
						t.Fatal(navigationErr)
					}
				}
				externalStarted = true
				go func() { defer close(externalDone); externalErr = native.Close() }()
				deadline := time.NewTimer(3 * time.Second)
				defer deadline.Stop()
				for !native.IsClosed() {
					select {
					case <-deadline.C:
						t.Fatal("actual SDK close did not enter")
					case <-time.After(time.Millisecond):
					}
				}
				if mode == "inflight" {
					select {
					case <-externalDone:
						t.Fatal("held native close unexpectedly joined before dispatch release")
					default:
					}
				} else {
					select {
					case <-externalDone:
					case <-time.After(3 * time.Second):
						t.Fatal("external completed close did not join")
					}
					if externalErr != nil {
						t.Fatal(externalErr)
					}
				}
				o.startClose()
				select {
				case <-o.closeDone:
				case <-time.After(3 * time.Second):
					t.Fatal("capture close result unavailable")
				}
				o.mu.Lock()
				closeErr := o.closeErr
				o.mu.Unlock()
				_, proofErr := o.root.Stat("writer-joined.json")
				if !errors.Is(closeErr, errHAROwnershipUnproven) || !os.IsNotExist(proofErr) {
					t.Fatal("prior/in-flight native Close produced a false writer-joined proof", closeErr, proofErr)
				}
				if o.retiredSuccessfully() || len(PlanRuntimeHARWorkingCleanup(b.harDir, 128)) != 0 || requests.Load() != 1 {
					t.Fatal("unowned close retired evidence or added target traffic")
				}
				release()
				select {
				case <-externalDone:
				case <-time.After(3 * time.Second):
					t.Fatal("released exact close did not physically join")
				}
				select {
				case <-navigationDone:
				case <-time.After(3 * time.Second):
					t.Fatal("released navigation did not join")
				}
				if externalErr != nil || navigationErr != nil {
					t.Fatal("owned fixture callbacks failed", externalErr, navigationErr)
				}
				if err := b.Teardown(context.Background()); !errors.Is(err, errHAROwnershipUnproven) {
					t.Fatal("failed close receipt was later erased", err)
				}
			})
		}
	}
}
