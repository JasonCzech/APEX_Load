package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserInteractionDispatchV2UsesNativeEventAndGeometry(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><title>dispatch</title><style>body{height:3000px}button{position:absolute;top:100px;left:100px;width:100px;height:50px}</style>
<button id="go">Go</button><input id="edit" aria-label="first field">
<script>window.ground=[];document.querySelector('#go').addEventListener('pointerdown',e=>ground.push({origin:performance.timeOrigin,offset:e.timeStamp,x:e.clientX,y:e.clientY,sx:scrollX,sy:scrollY}));document.querySelector('#go').onclick=()=>{scrollTo(0,300);let el=document.createElement('section');el.style.cssText='height:200px;width:500px';document.body.appendChild(el)}</script>`)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b := &Browser{}
			if err := b.Init(ctx, map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_capture_document_metrics": true, "browser_capture_interactions": true, "browser_interaction_evidence_version": 2}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			t.Cleanup(func() { _ = b.Teardown(context.Background()) })
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
			step := protocol.NewBrowserAuthoredStep(strings.Repeat("c", 64), "actions[1]")
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "duplicate", Type: "browser_click", Selector: "#go", BrowserAuthoredStep: step, Sync: &protocol.BrowserSyncPolicy{Type: "duration", Value: "350", TimeoutMs: 2000}})
			batches := b.TakeBrowserInteractionEvidence()
			if len(batches) != 1 || len(batches[0].Interactions) != 1 {
				t.Fatalf("evidence %+v", batches)
			}
			i := batches[0].Interactions[0]
			if !i.Valid() || i.Version != 2 || i.Dispatch == nil || i.AuthoredStep == nil || i.AuthoredStep.StepID != step.StepID {
				t.Fatalf("dispatch identity %+v", i)
			}
			var ground []struct{ Origin, Offset, X, Y, SX, SY float64 }
			if err := b.evaluatePageExpression(ctx, "window.ground", &ground); err != nil || len(ground) != 1 {
				t.Fatalf("ground=%+v err=%v", ground, err)
			}
			g := ground[0]
			if math.Abs(i.Dispatch.OffsetMS-g.Offset) > 0.001 || i.Dispatch.TimeOriginMS != g.Origin || i.ScrollX != g.SX || i.ScrollY != g.SY || len(i.Points) == 0 || i.Points[0].X != g.X || i.Points[0].Y != g.Y {
				t.Fatalf("event differs: evidence=%+v ground=%+v", i, g)
			}
			if i.CapturedAt.Sub(i.Dispatch.At) < 300*time.Millisecond {
				t.Fatalf("post-wait capture collapsed into dispatch: %v", i.CapturedAt.Sub(i.Dispatch.At))
			}
			var postScroll float64
			if err := b.evaluatePageExpression(ctx, "scrollY", &postScroll); err != nil || postScroll == i.ScrollY {
				t.Fatalf("fixture did not move after dispatch: %v %v", postScroll, err)
			}
			joined := false
			for _, o := range b.BrowserDocumentEvidence().Observations {
				if o.DocumentID == i.Dispatch.DocumentID && o.LifecycleID == i.Dispatch.LifecycleID {
					joined = true
				}
			}
			if !joined {
				t.Fatal("dispatch does not join a retained document lifecycle")
			}
			documentActualExecute(t, b, ctx, &protocol.Action{Name: "duplicate", Type: "browser_fill", Selector: "#edit", Value: "value", BrowserAuthoredStep: protocol.NewBrowserAuthoredStep(strings.Repeat("c", 64), "actions[2]")})
			batches = b.TakeBrowserInteractionEvidence()
			if len(batches) != 1 || len(batches[0].Interactions) != 1 {
				t.Fatalf("fill evidence %+v", batches)
			}
			second := batches[0].Interactions[0]
			if second.AuthoredStep.StepID == i.AuthoredStep.StepID {
				t.Fatal("duplicate display names merged")
			}
			if second.Dispatch != nil && (second.Target == nil || second.Target.Role != "textbox") {
				t.Fatalf("observed keyboard target=%+v", second)
			}
		})
	}
}

func TestBrowserInteractionDispatchVersionValidation(t *testing.T) {
	for _, value := range []any{0, 3, -1, 1.5, "2", true, math.NaN()} {
		if err := (&Browser{}).applyConfig(map[string]any{"browser_interaction_evidence_version": value}); err == nil {
			t.Fatalf("accepted version %v", value)
		}
	}
	for _, value := range []any{1, 2, float64(1), float64(2)} {
		b := &Browser{}
		if err := b.applyConfig(map[string]any{"browser_interaction_evidence_version": value}); err != nil {
			t.Fatal(err)
		}
		if b.interactionVersion < 1 || b.interactionVersion > 2 {
			t.Fatalf("version=%d", b.interactionVersion)
		}
	}
}

func TestBrowserInteractionDispatchV2UnavailablePopulations(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		for _, tc := range []struct {
			name                    string
			touch, documents, frame bool
			reason                  string
		}{
			{"synthetic_touch", true, true, false, protocol.InteractionUnavailableSyntheticDispatch},
			{"untransformed_frame", false, true, true, protocol.InteractionUnavailableChildFrame},
			{"no_document_identity", false, false, false, protocol.InteractionDispatchDocumentUnavailable},
		} {
			t.Run(engine+"/"+tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					if r.URL.Path == "/frame" {
						fmt.Fprint(w, `<!doctype html><button id="go">Go</button>`)
						return
					}
					fmt.Fprint(w, `<!doctype html><button id="go">Go</button><iframe id="child" src="/frame"></iframe>`)
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				b := &Browser{}
				if err := b.Init(ctx, map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_capture_document_metrics": tc.documents, "browser_capture_interactions": true, "browser_interaction_evidence_version": 2, "browser_touch_mode": tc.touch}); err != nil {
					requireBrowserRuntime(t, engine, err)
				}
				t.Cleanup(func() { _ = b.Teardown(context.Background()) })
				documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_navigate", Target: "/"})
				action := &protocol.Action{Name: "same", Type: "browser_click", Selector: "#go", BrowserAuthoredStep: protocol.NewBrowserAuthoredStep(strings.Repeat("d", 64), "actions[1]"), BrowserActionExecution: &protocol.BrowserActionExecution{Invocation: 1, Attempt: 1}}
				if tc.frame {
					action.Params = map[string]any{"frame_selector": "#child"}
				}
				documentActualExecute(t, b, ctx, action)
				rows := b.TakeBrowserInteractionEvidence()
				if len(rows) != 1 || len(rows[0].Interactions) != 1 {
					t.Fatalf("rows %+v", rows)
				}
				i := rows[0].Interactions[0]
				if !i.Valid() || i.Dispatch != nil || i.DispatchUnavailableReason != tc.reason || i.AuthoredStep == nil || i.Execution == nil {
					t.Fatalf("unavailable population %+v", i)
				}
			})
		}
	}
}

func TestBrowserInteractionDispatchV2RejectsLateOrUnarmedRead(t *testing.T) {
	b := &Browser{interactionVersion: 2}
	generation := strings.Repeat("a", 32)
	action := &protocol.Action{BrowserAuthoredStep: protocol.NewBrowserAuthoredStep(strings.Repeat("b", 64), "actions[0]")}
	for _, tc := range []struct {
		name     string
		start    *browserInteractionStart
		actionID string
		reason   string
	}{
		{"failed arm", &browserInteractionStart{read: false}, generation + "-a1", protocol.InteractionUnavailablePageState},
		{"missing arm", nil, generation + "-a1", protocol.InteractionUnavailablePageState},
		{"late prior arm", &browserInteractionStart{read: true}, generation + "-a0", protocol.InteractionDispatchActionUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := protocol.BrowserInteraction{SourceGeneration: generation, ActionID: generation + "-a1", FrameScope: "main_frame", Kind: "click"}
			page := browserInteractionPage{Dispatch: &browserInteractionDispatchPage{Action: tc.actionID, Origin: 1700000000000, Offset: 10}}
			b.enrichBrowserInteractionDispatch(&i, action, page, tc.start)
			if i.Dispatch != nil || i.DispatchUnavailableReason != tc.reason {
				t.Fatalf("late or unarmed dispatch accepted %+v", i)
			}
		})
	}
}

func TestBrowserInteractionDispatchV2BoundsLabelWorkBeforeNormalization(t *testing.T) {
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, `<!doctype html><input type="checkbox" id="attribute" aria-label="%soutside bounded prefix"><input type="checkbox" id="tree"><label for="tree">%s</label>`, strings.Repeat(" ", 100000), strings.Repeat("<span>x</span>", 1000))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			b := &Browser{}
			if err := b.Init(ctx, map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_capture_document_metrics": true, "browser_capture_interactions": true, "browser_interaction_evidence_version": 2}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			t.Cleanup(func() { _ = b.Teardown(context.Background()) })
			documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_navigate", Target: "/"})
			for _, selector := range []string{"#attribute", "#tree"} {
				documentActualExecute(t, b, ctx, &protocol.Action{Type: "browser_check", Selector: selector})
			}
			records := b.TakeBrowserInteractionEvidence()
			if len(records) != 1 || len(records[0].Interactions) != 2 {
				t.Fatalf("records %+v", records)
			}
			first, second := records[0].Interactions[0], records[0].Interactions[1]
			if first.Dispatch == nil || first.Target == nil || first.Target.LabelSHA256 != "" {
				t.Fatalf("processed label after bounded prefix: %+v", first)
			}
			sum := sha256.Sum256([]byte(strings.Repeat("x", 32)))
			if second.Dispatch == nil || second.Target == nil || second.Target.LabelSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("did not bound label traversal to 64 nodes: %+v", second)
			}
		})
	}
}
