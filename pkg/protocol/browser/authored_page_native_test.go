package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserAuthoredPageNativeNavigation(t *testing.T) {
	for _, tc := range []struct{ engine, profile string }{{"chromium", ""}, {"chromium", "pixel_8"}, {"webkit", ""}, {"webkit", "iphone_15_pro"}} {
		t.Run(tc.engine+"/"+tc.profile, func(t *testing.T) {
			var requests atomic.Int64
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/redirect" {
					http.Redirect(w, r, "/final", http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><link rel="icon" href="data:,"><h1>Same page</h1>`)
			}))
			defer target.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b := &Browser{}
			if err := b.Init(ctx, map[string]any{"base_url": target.URL, "browser_engine": tc.engine, "browser_device_profile": tc.profile, "browser_headless": true, "browser_capture_document_metrics": true}); err != nil {
				requireBrowserRuntime(t, tc.engine, err)
			}
			t.Cleanup(func() { _ = b.Teardown(context.Background()) })
			definition := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
			var pages []*protocol.BrowserAuthoredPageBinding
			for _, path := range []string{"actions[0]", "actions[1]", "actions[0]"} {
				result := documentActualExecute(t, b, ctx, &protocol.Action{Name: "same", Type: "browser_navigate", Target: "/", BrowserAuthoredStep: protocol.NewBrowserOriginalAuthoredStep(definition, path)})
				raw, err := json.Marshal(result.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey])
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := protocol.DecodeBrowserAuthoredPageBinding(raw)
				if err != nil {
					t.Fatalf("receipt=%s: %v", raw, err)
				}
				if receipt.State != "bound" {
					t.Fatalf("native %s identity unavailable: %+v", tc.engine, receipt)
				}
				b.mu.Lock()
				matched := false
				for _, d := range b.evidence.documents {
					if d.documentID == receipt.DocumentID && d.lifecycleID == receipt.LifecycleID {
						matched = true
					}
				}
				b.mu.Unlock()
				if !matched {
					t.Fatal("receipt did not join observed document")
				}
				pages = append(pages, receipt)
			}
			if pages[0].PageID != pages[2].PageID || pages[0].PageID == pages[1].PageID || pages[0].DocumentID == pages[2].DocumentID {
				t.Fatal("authored and runtime page identities conflated")
			}
			redirected := documentActualExecute(t, b, ctx, &protocol.Action{Name: "redirect", Type: "browser_navigate", Target: "/redirect", BrowserAuthoredStep: protocol.NewBrowserOriginalAuthoredStep(definition, "actions[2]")})
			raw, _ := json.Marshal(redirected.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey])
			receipt, err := protocol.DecodeBrowserAuthoredPageBinding(raw)
			if err != nil || receipt.State != "bound" {
				t.Fatal("redirect response binding", string(raw), err)
			}
			if receipt.PageID == pages[0].PageID {
				t.Fatal("redirect definition collapsed into same-URL page")
			}
			// Old native requests and source generations cannot be rebound to
			// the current document, even when the target URL is unchanged.
			b.mu.Lock()
			var currentRequest, oldRequest playwright.Request
			var currentEvidence, oldEvidence protocol.BrowserResourceEvidence
			for request, identity := range b.evidence.requests {
				if identity.evidence.RequestID == receipt.RequestID {
					currentRequest, currentEvidence = request, identity.evidence
				}
				if identity.evidence.RequestID == pages[0].RequestID {
					oldRequest, oldEvidence = request, identity.evidence
				}
			}
			generation := b.evidence.generation
			b.mu.Unlock()
			if currentRequest == nil || oldRequest == nil {
				t.Fatal("missing fixture native request")
			}
			for _, invalid := range []struct {
				request    playwright.Request
				evidence   protocol.BrowserResourceEvidence
				generation string
				canceled   bool
			}{
				{oldRequest, oldEvidence, generation, false},
				{currentRequest, currentEvidence, strings.Repeat("f", 32), false},
				{currentRequest, currentEvidence, generation, true},
			} {
				probe := protocol.BrowserAuthoredPageBinding{Version: 1, State: "unavailable", Reason: "navigation_join_unavailable", ObservedAt: time.Now().UTC(), AuthoredStep: receipt.AuthoredStep.Clone()}
				probeCtx, probeCancel := context.WithCancel(ctx)
				if invalid.canceled {
					probeCancel()
				}
				b.bindNativeAuthoredPage(probeCtx, invalid.request, invalid.evidence, invalid.generation, &probe)
				probeCancel()
				if probe.State != "unavailable" || probe.PageID != "" || !probe.Valid() {
					t.Fatalf("stale/canceled observation bound: %+v", probe)
				}
			}
			fragment := documentActualExecute(t, b, ctx, &protocol.Action{Name: "fragment", Type: "browser_navigate", Target: "/final#fragment", BrowserAuthoredStep: protocol.NewBrowserOriginalAuthoredStep(definition, "actions[3]")})
			raw, _ = json.Marshal(fragment.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey])
			receipt, err = protocol.DecodeBrowserAuthoredPageBinding(raw)
			if err != nil || receipt.State != "unavailable" || receipt.Reason != "navigation_response_unavailable" {
				t.Fatal("same-document navigation invented binding", string(raw), err)
			}
			opened := documentActualExecute(t, b, ctx, &protocol.Action{Name: "new window", Type: "browser_open_window", Target: "/", BrowserAuthoredStep: protocol.NewBrowserOriginalAuthoredStep(definition, "actions[4]")})
			raw, _ = json.Marshal(opened.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey])
			receipt, err = protocol.DecodeBrowserAuthoredPageBinding(raw)
			if err != nil || receipt.State != "bound" || receipt.PageID == pages[0].PageID || receipt.DocumentID == pages[0].DocumentID {
				t.Fatal("explicit new-window binding", string(raw), err)
			}
			if requests.Load() != 6 {
				t.Fatalf("evidence added target traffic: %d", requests.Load())
			}
		})
	}
}
