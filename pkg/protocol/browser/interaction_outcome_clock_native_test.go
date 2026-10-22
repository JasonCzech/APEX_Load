package browser

import (
	"context"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserOutcomeClockNativePrecedesSlowMotionAndCapture(t *testing.T) {
	for _, tc := range []struct {
		engine, profile string
		version         int
	}{{"chromium", "", 3}, {"chromium", "pixel_8", 3}, {"webkit", "", 3}, {"webkit", "iphone_15_pro", 3}, {"chromium", "", 2}} {
		t.Run(fmt.Sprintf("%s/%s/v%d", tc.engine, tc.profile, tc.version), func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><button id="go">Go</button>`)
			}))
			defer target.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b := &Browser{}
			if err := b.Init(ctx, map[string]any{"base_url": target.URL, "browser_engine": tc.engine, "browser_device_profile": tc.profile, "browser_headless": true, "browser_capture_document_metrics": true, "browser_capture_interactions": true, "browser_interaction_evidence_version": tc.version}); err != nil {
				requireBrowserRuntime(t, tc.engine, err)
			}
			t.Cleanup(func() { _ = b.Teardown(context.Background()) })
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
			b.slowMo = 250 * time.Millisecond
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "go", Type: "browser_click", Selector: "#go", BrowserAuthoredStep: protocol.NewBrowserAuthoredStep(strings.Repeat("a", 64), "actions[1]"), Sync: &protocol.BrowserSyncPolicy{Type: "duration", Value: "40", TimeoutMs: 1500}})
			batches := b.TakeBrowserInteractionEvidence()
			if len(batches) != 1 || batches[0] == nil || len(batches[0].Interactions) != 1 {
				t.Fatalf("missing interaction %+v", batches)
			}
			i := batches[0].Interactions[0]
			if !i.Valid() || i.Version != tc.version || i.Outcome.Observed != protocol.InteractionOutcomeMet {
				t.Fatal(i)
			}
			if tc.version == 2 {
				if i.Outcome.Clock != nil {
					t.Fatal("legacy wire includes new clock")
				}
				return
			}
			c := i.Outcome.Clock
			if !c.Valid() || c.BudgetMS != 1500 || c.ElapsedMS < 30 || i.CapturedAt.Sub(c.ObservedAt) < 200*time.Millisecond {
				t.Fatalf("wait/capture clocks collapsed: %+v captured=%v", c, i.CapturedAt)
			}
			if tc.profile == "" && i.Dispatch == nil {
				t.Fatal("v3 lost native dispatch instrumentation", i.DispatchUnavailableReason)
			}
			if tc.profile != "" && i.Dispatch != nil && i.UnavailableReason == protocol.InteractionUnavailableSyntheticDispatch {
				t.Fatal("synthetic mobile input became qualified")
			}
		})
	}
}
