package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const interactionFixturePage = `<!doctype html><title>interactions</title>
<style>body{margin:0;height:3000px}#go{position:absolute;left:100px;top:150px;width:80px;height:40px}#email{position:absolute;left:100px;top:260px;width:200px}#over{position:absolute;left:400px;top:150px;width:60px;height:60px}</style>
<button id="go" onclick="const d=document.createElement('section');d.style.cssText='position:absolute;left:10px;top:500px;width:300px;height:300px';document.body.appendChild(d)">Go</button>
<label for="email">E-mail 2</label><input id="email" name="email">
<div id="over">hover</div>`

func interactionFixtureServer(t *testing.T) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, interactionFixturePage)
	}))
	t.Cleanup(server.Close)
	return server, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for k, v := range requests {
			out[k] = v
		}
		return out
	}
}

func interactionFixtureBrowser(t *testing.T, engine, baseURL string, capture bool) (*Browser, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	b := &Browser{}
	if err := b.Init(ctx, map[string]any{"base_url": baseURL, "browser_engine": engine, "browser_headless": true, "browser_capture_interactions": capture, "browser_viewport_width": 1000, "browser_viewport_height": 700}); err != nil {
		requireBrowserRuntime(t, engine, err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	return b, ctx
}

func TestBrowserActualInteractionsMatchDispatchedGroundTruth(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server, requests := interactionFixtureServer(t)
			b, ctx := interactionFixtureBrowser(t, engine, server.URL, true)
			run := func(action *protocol.Action) {
				t.Helper()
				result, err := b.Execute(ctx, action)
				if err != nil || result == nil || !result.Success {
					t.Fatalf("%s failed: %v %+v", action.Type, err, result)
				}
			}
			run(&protocol.Action{Name: "open order", Type: "browser_navigate", Target: "/orders/12345/items?coupon=SECRET#top"})
			box, err := b.currentPage().Locator("#go").BoundingBox()
			if err != nil || box == nil {
				t.Fatal("ground truth box", err)
			}
			hoverBox, _ := b.currentPage().Locator("#over").BoundingBox()
			run(&protocol.Action{Name: "hover", Type: "browser_hover", Selector: "#over"})
			// A page's own synthetic pointer events are never recorded.
			var dispatched bool
			if err := b.evaluatePageExpression(ctx, `document.querySelector('#go').dispatchEvent(new PointerEvent('pointerdown', {clientX: 5, clientY: 5, bubbles: true}))`, &dispatched); err != nil {
				t.Fatal(err)
			}
			run(&protocol.Action{Name: "go", Type: "browser_click", Selector: "#go", WaitFor: &protocol.WaitCondition{Type: "selector", Value: "section", TimeoutMs: 2000}})
			run(&protocol.Action{Name: "go twice", Type: "browser_dblclick", Selector: "#go"})
			run(&protocol.Action{Name: "email", Type: "browser_fill", Selector: "#email", Value: "user@example.com"})
			taken := b.TakeBrowserInteractionEvidence()
			if len(taken) != 1 || len(taken[0].Interactions) != 4 {
				t.Fatalf("interactions %+v", taken)
			}
			byKind := map[string]protocol.BrowserInteraction{}
			for _, interaction := range taken[0].Interactions {
				byKind[interaction.Kind] = interaction
			}
			cx, cy := box.X+box.Width/2, box.Y+box.Height/2
			near := func(p protocol.BrowserInteractionPoint, x, y float64) bool {
				return math.Abs(p.X-x) <= 1 && math.Abs(p.Y-y) <= 1
			}
			click := byKind["click"]
			if len(click.Points) != 1 || !near(click.Points[0], cx, cy) || click.PointerType != "mouse" || click.Target != nil {
				t.Fatalf("click points %+v want (%g,%g)", click, cx, cy)
			}
			if click.Route != "/orders/:id/items" || click.ViewportWidth != 1000 || click.ViewportHeight != 700 || click.DeviceScaleFactor <= 0 || click.Provenance != protocol.InteractionProvenanceScripted {
				t.Fatalf("click context %+v", click)
			}
			if click.Outcome.Declared != "selector" || click.Outcome.DeadlineMS != 2000 || click.Outcome.Observed != protocol.InteractionOutcomeMet {
				t.Fatalf("click outcome %+v", click.Outcome)
			}
			dbl := byKind["dblclick"]
			if len(dbl.Points) != 2 || !near(dbl.Points[1], cx, cy) {
				t.Fatalf("dblclick points %+v", dbl.Points)
			}
			// The click added a section: the layout changed; nothing else did.
			if dbl.LayoutRevision == byKind["hover"].LayoutRevision || dbl.LayoutRevision != byKind["fill"].LayoutRevision || len(dbl.LayoutRevision) != 16 {
				t.Fatalf("layout revisions hover %s click %s dbl %s fill %s", byKind["hover"].LayoutRevision, click.LayoutRevision, dbl.LayoutRevision, byKind["fill"].LayoutRevision)
			}
			hover := byKind["hover"]
			if len(hover.Points) != 1 || hover.Points[0].X < hoverBox.X || hover.Points[0].X > hoverBox.X+hoverBox.Width || hover.Points[0].Y < hoverBox.Y || hover.Points[0].Y > hoverBox.Y+hoverBox.Height {
				t.Fatalf("hover point %+v box %+v", hover.Points, hoverBox)
			}
			fill := byKind["fill"]
			label := sha256.Sum256([]byte("e-mail #"))
			if len(fill.Points) != 0 || fill.Target == nil || fill.Target.Role != "textbox" || fill.Target.LabelSHA256 != hex.EncodeToString(label[:]) {
				t.Fatalf("fill target %+v", fill.Target)
			}
			if fill.DocumentID == "" || fill.DocumentID != click.DocumentID || fill.ActionID == click.ActionID {
				t.Fatalf("identities %s %s %s %s", fill.DocumentID, click.DocumentID, fill.ActionID, click.ActionID)
			}
			// Input values are never recorded.
			encoded, _ := json.Marshal(taken)
			if strings.Contains(string(encoded), "user@example.com") || strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), "E-mail") {
				t.Fatalf("interaction evidence leaked page content: %s", encoded)
			}
			// Capture added no target traffic: one page load only.
			if got := requests(); len(got) != 1 || got["/orders/12345/items"] != 1 {
				t.Fatalf("requests %v", got)
			}
		})
	}
}

func TestBrowserActualInteractionCaptureIsOptIn(t *testing.T) {
	server, requests := interactionFixtureServer(t)
	b, ctx := interactionFixtureBrowser(t, "chromium", server.URL, false)
	if _, err := b.Execute(ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Execute(ctx, &protocol.Action{Name: "go", Type: "browser_click", Selector: "#go"}); err != nil {
		t.Fatal(err)
	}
	var present bool
	_ = b.evaluatePageExpression(ctx, `Object.getOwnPropertyNames(globalThis).some(n => n.startsWith('__apexInteraction_'))`, &present)
	if len(b.TakeBrowserInteractionEvidence()) != 0 || present {
		t.Fatal("interactions recorded without opt-in")
	}
	if got := requests(); got["/"] != 1 {
		t.Fatalf("requests %v", got)
	}
}
