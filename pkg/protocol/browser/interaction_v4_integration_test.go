package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserInteractionV4AndAuthoredPageV3NativeIntegration(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			xoriginServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><style>body{margin:0}#xbtn{width:80px;height:40px}</style><button id="xbtn">XOrigin</button>`)
			}))
			t.Cleanup(xoriginServer.Close)

			var popupServed atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/redirect":
					http.Redirect(w, r, "/app", http.StatusFound)
				case "/download.txt":
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("Content-Disposition", `attachment; filename="report.txt"`)
					fmt.Fprint(w, "download payload")
				case "/popup":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<!doctype html><h1>Popup Window</h1><button id="popup-btn">Popup Go</button><script>fetch('/popup-loaded',{method:'POST'})</script>`)
				case "/popup-loaded":
					popupServed.Store(true)
					w.WriteHeader(http.StatusNoContent)
				case "/popup-ready":
					w.Header().Set("Content-Type", "text/plain")
					if popupServed.Load() {
						fmt.Fprint(w, "1")
					} else {
						fmt.Fprint(w, "0")
					}
				case "/frame2d":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<!doctype html><style>html,body{margin:0;padding:0}#fbtn{position:absolute;left:20px;top:10px;width:80px;height:40px}</style><button id="fbtn">Frame 2D</button>`)
				case "/frame3d":
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<!doctype html><style>html,body{margin:0;padding:0}#f3dbtn{width:80px;height:40px}</style><button id="f3dbtn">Frame 3D</button>`)
				default:
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprintf(w, `<!doctype html>
<title>App Home</title>
<style>
  html, body { margin: 0; padding: 0; }
  button, a { display: inline-block; width: 100px; height: 36px; }
  #child2d {
    position: absolute;
    left: 40px;
    top: 220px;
    width: 200px;
    height: 120px;
    border: 4px solid #000;
    padding: 6px;
    transform: translate(10px, 20px) scale(0.5);
    transform-origin: 0 0;
  }
  #child3d {
    position: absolute;
    left: 300px;
    top: 220px;
    width: 160px;
    height: 100px;
    transform: perspective(100px) rotateX(30deg);
  }
  #xorigin {
    position: absolute;
    left: 520px;
    top: 220px;
    width: 160px;
    height: 100px;
  }
</style>
<h1 id="heading">Ready</h1>
<button id="spa-route" onclick="history.pushState({step:1}, '', '/app/orders'); document.querySelector('#heading').textContent='Orders'">Orders</button>
<button id="alert-btn" onclick="alert('confirmed'); document.querySelector('#heading').textContent='Alerted'">Alert</button>
<a id="dl-link" href="/download.txt" download="report.txt">Download</a>
<a id="popup-link" href="/popup" target="_blank" onclick="const poll=()=>{fetch('/popup-ready').then(r=>r.text()).then(t=>{if(t==='1')setTimeout(()=>{const d=document.createElement('div');d.id='popup-opened';d.textContent='opened';document.body.appendChild(d)},50);else setTimeout(poll,20)})};setTimeout(poll,20)">Open Popup</a>
<iframe id="child2d" src="/frame2d"></iframe>
<iframe id="child3d" src="/frame3d"></iframe>
<iframe id="xorigin" src=%q></iframe>
`, xoriginServer.URL)
				}
			}))
			t.Cleanup(server.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			b := &Browser{}
			t.Setenv("APEXLOAD_EXPORT_DIR", t.TempDir())
			if err := b.Init(ctx, map[string]any{
				"base_url":                             server.URL,
				"browser_engine":                       engine,
				"browser_headless":                     true,
				"browser_capture_document_metrics":     true,
				"browser_capture_interactions":         true,
				"browser_interaction_evidence_version": 4,
				"browser_authored_page_version":        3,
			}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			t.Cleanup(func() { _ = b.Teardown(context.Background()) })

			def := protocol.BrowserAuthoredDefinition{
				Version:         1,
				ScriptSHA256:    strings.Repeat("a", 64),
				StructureSHA256: strings.Repeat("b", 64),
			}
			step := func(idx int) *protocol.BrowserAuthoredStep {
				return protocol.NewBrowserOriginalAuthoredStep(def, fmt.Sprintf("actions[%d]", idx))
			}
			exec := func(inv uint64) *protocol.BrowserActionExecution {
				return &protocol.BrowserActionExecution{Invocation: inv, Attempt: 1}
			}
			decodeReceipt := func(res *protocol.Result) *protocol.BrowserAuthoredPageBinding {
				t.Helper()
				raw, err := json.Marshal(res.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey])
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := protocol.DecodeBrowserAuthoredPageBinding(raw)
				if err != nil {
					t.Fatalf("decode receipt %s: %v", string(raw), err)
				}
				return receipt
			}

			// 1. Navigate via redirect chain (/redirect -> /app).
			resRedirect := documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                "nav_redirect",
				Type:                "browser_navigate",
				Target:              "/redirect",
				BrowserAuthoredStep: step(0),
			})
			receiptRedirect := decodeReceipt(resRedirect)
			if receiptRedirect.State != "bound" || receiptRedirect.Version != 3 || receiptRedirect.Association != protocol.BrowserAuthoredPageAssociationRedirectChain {
				t.Fatalf("redirect receipt = %+v", receiptRedirect)
			}

			// 2. Click button that triggers history.pushState + inline assertion + sync wait.
			resSPA := documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                   "spa_orders",
				Type:                   "browser_click",
				Selector:               "#spa-route",
				BrowserAuthoredStep:    step(1),
				BrowserActionExecution: exec(1),
				Sync:                   &protocol.BrowserSyncPolicy{Type: "element_visible", Value: "#heading", TimeoutMs: 2000},
				Assertions: []protocol.Assertion{
					{Type: "element_text_equals", Value: "#heading", Expected: "Orders"},
					{Type: "url_contains", Value: "/app/orders"},
				},
			})
			receiptSPA := decodeReceipt(resSPA)
			if receiptSPA.State != "bound" || receiptSPA.Version != 3 || receiptSPA.Association != protocol.BrowserAuthoredPageAssociationSameDocumentRoute || receiptSPA.RouteSeq != 1 {
				t.Fatalf("spa pushState receipt = %+v", receiptSPA)
			}

			// 3. Same-document hash navigation via browser_navigate -> RouteSeq increments to 2.
			resHash := documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                "nav_hash",
				Type:                "browser_navigate",
				Target:              "/app/orders#details",
				BrowserAuthoredStep: step(2),
			})
			receiptHash := decodeReceipt(resHash)
			if receiptHash.State != "bound" || receiptHash.Version != 3 || receiptHash.Association != protocol.BrowserAuthoredPageAssociationSameDocumentRoute || receiptHash.RouteSeq != 2 {
				t.Fatalf("hash navigate receipt = %+v", receiptHash)
			}

			// 4. History back and forward traversal -> bound with history_traversal.
			resBack := documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                "go_back",
				Type:                "browser_back",
				BrowserAuthoredStep: step(3),
			})
			receiptBack := decodeReceipt(resBack)
			if receiptBack.State != "bound" || receiptBack.Version != 3 || receiptBack.Association != protocol.BrowserAuthoredPageAssociationHistoryTraversal || receiptBack.RouteSeq <= receiptHash.RouteSeq {
				t.Fatalf("history back receipt = %+v", receiptBack)
			}
			resForward := documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                "go_forward",
				Type:                "browser_forward",
				BrowserAuthoredStep: step(4),
			})
			receiptForward := decodeReceipt(resForward)
			if receiptForward.State != "bound" || receiptForward.Version != 3 || receiptForward.Association != protocol.BrowserAuthoredPageAssociationHistoryTraversal || receiptForward.RouteSeq <= receiptBack.RouteSeq {
				t.Fatalf("history forward receipt = %+v", receiptForward)
			}

			// 5. Arm dialog handler and click #alert-btn -> dialog outcome met.
			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:   "arm_dialog",
				Type:   "browser_handle_dialog",
				Params: map[string]any{"action": "accept", "expected_message": "confirmed", "timeout_ms": 2000},
			})
			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                   "trigger_dialog",
				Type:                   "browser_click",
				Selector:               "#alert-btn",
				BrowserAuthoredStep:    step(5),
				BrowserActionExecution: exec(2),
			})

			// 6. Trigger file download via browser_download -> download outcome met + download kind interaction.
			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                   "download_report",
				Type:                   "browser_download",
				Selector:               "#dl-link",
				Params:                 map[string]any{"filename_contains": "report.txt", "save_as": "report.txt", "timeout_ms": 5000},
				BrowserAuthoredStep:    step(6),
				BrowserActionExecution: exec(3),
			})

			// 7. Click inside 2D-transformed child iframe (#child2d) -> FrameScope="child_frame", GeometryProvenance="dispatch", FrameChain invertible.
			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                   "click_frame2d",
				Type:                   "browser_click",
				Selector:               "#fbtn",
				Params:                 map[string]any{"frame_selector": "#child2d"},
				BrowserAuthoredStep:    step(7),
				BrowserActionExecution: exec(4),
			})

			// 8. Click inside 3D-transformed child iframe (#child3d) -> non_axis_aligned_or_3d_transform.
			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                   "click_frame3d",
				Type:                   "browser_click",
				Selector:               "#f3dbtn",
				Params:                 map[string]any{"frame_selector": "#child3d"},
				BrowserAuthoredStep:    step(8),
				BrowserActionExecution: exec(5),
			})

			// 9. Click inside cross-origin child iframe (#xorigin) -> cross_origin_ancestry.
			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                   "click_xorigin",
				Type:                   "browser_click",
				Selector:               "#xbtn",
				Params:                 map[string]any{"frame_selector": "#xorigin"},
				BrowserAuthoredStep:    step(9),
				BrowserActionExecution: exec(6),
			})

			// 10. Click popup link (#popup-link) -> opened_by_action receipt & navigation outcome.
			resPopup := documentActualExecute(t, b, ctx, &protocol.Action{
				Name:                   "open_popup_click",
				Type:                   "browser_click",
				Selector:               "#popup-link",
				Sync:                   &protocol.BrowserSyncPolicy{Type: "element_visible", Value: "#popup-opened", TimeoutMs: 5000},
				BrowserAuthoredStep:    step(10),
				BrowserActionExecution: exec(7),
			})
			receiptPopup := decodeReceipt(resPopup)
			if receiptPopup.State != "bound" || receiptPopup.Version != 3 || receiptPopup.Association != protocol.BrowserAuthoredPageAssociationOpenedByAction {
				t.Fatalf("popup receipt = %+v", receiptPopup)
			}

			batches := b.TakeBrowserInteractionEvidence()
			if len(batches) == 0 {
				t.Fatal("no interaction evidence batches")
			}
			byName := map[string]protocol.BrowserInteraction{}
			for _, batch := range batches {
				if batch.Version != protocol.BrowserInteractionVersion4 {
					t.Fatalf("batch version = %d, want 4", batch.Version)
				}
				for _, inter := range batch.Interactions {
					if !inter.Valid() {
						t.Fatalf("invalid v4 interaction %q: %+v", inter.ActionName, inter)
					}
					byName[inter.ActionName] = inter
				}
			}

			// Verify spa_orders outcomes: sync, 2 assertions, and navigation.
			spaInter, ok := byName["spa_orders"]
			if !ok {
				t.Fatalf("missing spa_orders interaction in %+v", byName)
			}
			if spaInter.AuthoredPageID != receiptRedirect.PageID || spaInter.RouteSeq != 0 {
				t.Fatalf("spa_orders pre-action page binding = (%q, %d), want (%q, 0)", spaInter.AuthoredPageID, spaInter.RouteSeq, receiptRedirect.PageID)
			}
			if len(spaInter.Outcomes) != 4 {
				t.Fatalf("spa_orders outcomes = %+v, want 4 (sync + 2 assertions + navigation)", spaInter.Outcomes)
			}
			if spaInter.Outcomes[0].Kind != protocol.InteractionOutcomeKindSync || spaInter.Outcomes[0].Observed != protocol.InteractionOutcomeMet || !spaInter.Outcomes[0].Clock.Valid() {
				t.Fatalf("spa_orders sync outcome = %+v", spaInter.Outcomes[0])
			}
			if spaInter.Outcomes[1].Kind != protocol.InteractionOutcomeKindAssertion || spaInter.Outcomes[1].Declared != "element_text_equals" || spaInter.Outcomes[1].Observed != protocol.InteractionOutcomeMet {
				t.Fatalf("spa_orders assertion[0] = %+v", spaInter.Outcomes[1])
			}
			if spaInter.Outcomes[2].Kind != protocol.InteractionOutcomeKindAssertion || spaInter.Outcomes[2].Declared != "url_contains" || spaInter.Outcomes[2].Observed != protocol.InteractionOutcomeMet {
				t.Fatalf("spa_orders assertion[1] = %+v", spaInter.Outcomes[2])
			}
			if spaInter.Outcomes[3].Kind != protocol.InteractionOutcomeKindNavigation || spaInter.Outcomes[3].Declared != protocol.BrowserAuthoredPageAssociationSameDocumentRoute || spaInter.Outcomes[3].AuthoredPageID != receiptSPA.PageID || spaInter.Outcomes[3].RouteSeq != 1 {
				t.Fatalf("spa_orders navigation outcome = %+v", spaInter.Outcomes[3])
			}

			// Verify trigger_dialog has bound AuthoredPageID from receiptForward and dialog outcome met.
			dlgInter, ok := byName["trigger_dialog"]
			if !ok {
				t.Fatal("missing trigger_dialog interaction")
			}
			if dlgInter.AuthoredPageID != receiptForward.PageID || dlgInter.RouteSeq != receiptForward.RouteSeq {
				t.Fatalf("trigger_dialog page = (%q, %d), want (%q, %d)", dlgInter.AuthoredPageID, dlgInter.RouteSeq, receiptForward.PageID, receiptForward.RouteSeq)
			}
			foundDialog := false
			for _, o := range dlgInter.Outcomes {
				if o.Kind == protocol.InteractionOutcomeKindDialog && o.Observed == protocol.InteractionOutcomeMet && o.Clock.Valid() {
					foundDialog = true
				}
			}
			if !foundDialog {
				t.Fatalf("trigger_dialog outcomes missing met dialog outcome: %+v", dlgInter.Outcomes)
			}

			// Verify download_report has Kind="download" and met download outcome.
			dlInter, ok := byName["download_report"]
			if !ok || dlInter.Kind != "download" {
				t.Fatalf("download_report interaction = %+v", dlInter)
			}
			foundDownload := false
			for _, o := range dlInter.Outcomes {
				if o.Kind == protocol.InteractionOutcomeKindDownload && o.Observed == protocol.InteractionOutcomeMet && o.Clock.Valid() {
					foundDownload = true
				}
			}
			if !foundDownload {
				t.Fatalf("download_report outcomes missing met download outcome: %+v", dlInter.Outcomes)
			}

			// Verify 2D child frame interaction.
			f2d, ok := byName["click_frame2d"]
			if !ok {
				t.Fatal("missing click_frame2d interaction")
			}
			if f2d.FrameScope != "child_frame" || f2d.UnavailableReason != "" || f2d.Dispatch == nil || f2d.GeometryProvenance != protocol.InteractionGeometryProvenanceDispatch || len(f2d.FrameChain) != 1 || len(f2d.Points) == 0 {
				t.Fatalf("click_frame2d = %+v", f2d)
			}
			mappedPt, ok := f2d.FrameChain[0].ApplyToPoint(f2d.Points[0])
			// #child2d has left:40, top:220, translate(10,20) -> BoundingX=50, BoundingY=240; border=4, padding=6; scale(0.5); #fbtn center is roughly (60, 30) in child frame -> px=70, py=40 -> scaled by 0.5=(35,20) + (50,240) = (85, 260).
			if !ok || math.Abs(mappedPt.X-85) > 25 || math.Abs(mappedPt.Y-260) > 25 {
				t.Fatalf("click_frame2d mapped point = (%v, %v), raw = (%v, %v), hop = %+v", mappedPt.X, mappedPt.Y, f2d.Points[0].X, f2d.Points[0].Y, f2d.FrameChain[0])
			}

			// Verify 3D child frame exclusion.
			f3d, ok := byName["click_frame3d"]
			if !ok {
				t.Fatal("missing click_frame3d interaction")
			}
			if f3d.FrameScope != "child_frame" || f3d.UnavailableReason != protocol.InteractionUnavailableNonAxisAlignedOr3DTransform || f3d.Dispatch != nil || len(f3d.Points) != 0 {
				t.Fatalf("click_frame3d = %+v", f3d)
			}

			// Verify cross-origin child frame exclusion.
			fxo, ok := byName["click_xorigin"]
			if !ok {
				t.Fatal("missing click_xorigin interaction")
			}
			if fxo.FrameScope != "child_frame" || fxo.UnavailableReason != protocol.InteractionUnavailableCrossOriginAncestry || fxo.Dispatch != nil || len(fxo.Points) != 0 {
				t.Fatalf("click_xorigin = %+v", fxo)
			}
		})
	}
}
