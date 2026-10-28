package browser

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type groundTruthRecord struct {
	Kind                string   `json:"kind"`
	GroupID             string   `json:"group_id,omitempty"`
	TargetType          string   `json:"target_type,omitempty"`
	FrameScope          string   `json:"frame_scope,omitempty"`
	IsTrusted           bool     `json:"is_trusted,omitempty"`
	SeededDelayMS       int      `json:"seeded_delay_ms,omitempty"`
	AtMS                float64  `json:"at_ms,omitempty"`
	InteractionID       int64    `json:"interaction_id,omitempty"`
	DurationMS          float64  `json:"duration_ms,omitempty"`
	StartTimeMS         float64  `json:"start_time_ms,omitempty"`
	Name                string   `json:"name,omitempty"`
	Value               float64  `json:"value,omitempty"`
	HadRecentInput      bool     `json:"had_recent_input,omitempty"`
	Path                string   `json:"path,omitempty"`
	Payload             string   `json:"payload,omitempty"`
	InteractionCount    int      `json:"interaction_count,omitempty"`
	DistinctGroups      int      `json:"distinct_groups,omitempty"`
	SupportedEntryTypes []string `json:"supported_entry_types,omitempty"`
}

func TestBrowserDocumentActualGroundTruthInteractionGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser ground-truth interaction groups integration test in -short mode")
	}

	fixtureDir := filepath.Join("testdata", "interaction_groups")
	indexHTML, err := os.ReadFile(filepath.Join(fixtureDir, "index.html"))
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	frameHTML, err := os.ReadFile(filepath.Join(fixtureDir, "frame.html"))
	if err != nil {
		t.Fatalf("read frame.html: %v", err)
	}
	appCSS, err := os.ReadFile(filepath.Join(fixtureDir, "app.css"))
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	swJS, err := os.ReadFile(filepath.Join(fixtureDir, "sw.js"))
	if err != nil {
		t.Fatalf("read sw.js: %v", err)
	}

	cohorts := []struct {
		name    string
		engine  string
		profile string
	}{
		{name: "chromium_desktop", engine: "chromium", profile: ""},
		{name: "chromium_pixel_8", engine: "chromium", profile: "pixel_8"},
		{name: "webkit_desktop", engine: "webkit", profile: ""},
		{name: "webkit_iphone_15_pro", engine: "webkit", profile: "iphone_15_pro"},
	}

	for _, cohort := range cohorts {
		t.Run(cohort.name, func(t *testing.T) {
			var reqMu sync.Mutex
			requestCounts := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reqMu.Lock()
				requestCounts[r.URL.Path]++
				reqMu.Unlock()
				switch {
				case r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/launch/"):
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = w.Write(indexHTML)
				case r.URL.Path == "/frame.html":
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = w.Write(frameHTML)
				case r.URL.Path == "/app.css":
					w.Header().Set("Cache-Control", "public, max-age=600")
					w.Header().Set("Content-Type", "text/css; charset=utf-8")
					_, _ = w.Write(appCSS)
				case r.URL.Path == "/sw.js":
					w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
					_, _ = w.Write(swJS)
				case r.URL.Path == "/sw-cached.json":
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					_, _ = w.Write([]byte(`{"cached_by":"network_fallback","ok":false}`))
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()

			b := &Browser{}
			cfg := map[string]any{
				"base_url":                         server.URL,
				"browser_engine":                   cohort.engine,
				"browser_headless":                 true,
				"browser_capture_document_metrics": true,
				"browser_capture_interactions":     true,
				"browser_runtime_new_user_policy":  "persistent_context",
			}
			if cohort.profile != "" {
				cfg["browser_device_profile"] = cohort.profile
			} else {
				cfg["browser_viewport_width"] = 1280
				cfg["browser_viewport_height"] = 900
			}
			if err := b.Init(ctx, cfg); err != nil {
				requireBrowserRuntime(t, cohort.engine, err)
			}
			t.Cleanup(func() { _ = b.Teardown(context.Background()) })

			secret := "secretTokenValueAlphaOmega"
			b.SetRuntimeTextRedactor(func(v string) string {
				return strings.ReplaceAll(v, secret, "[redacted]")
			})

			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:   "navigate fixture",
				Type:   "browser_navigate",
				Target: "/launch/" + secret + "?secret=" + secret,
			})
			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:     "wait fixture ready",
				Type:     "browser_wait",
				Selector: "#fixture-status",
				WaitFor: &protocol.WaitCondition{
					Type:      "text",
					Value:     "ready_for_interactions",
					TimeoutMs: 15000,
				},
			})

			actionType := "browser_click"
			if cohort.profile != "" {
				actionType = "browser_tap"
			}
			for i := 1; i <= 50; i++ {
				var selector string
				switch {
				case i <= 40:
					selector = fmt.Sprintf("#grp-btn-%d", i)
				case i <= 45:
					selector = fmt.Sprintf("#grp-link-%d", i)
				default:
					selector = fmt.Sprintf("#grp-input-%d", i)
				}
				action := &protocol.Action{
					Name:     fmt.Sprintf("interact_%d", i),
					Type:     actionType,
					Selector: selector,
				}
				// Execute representative button, link, and input interactions via
				// b.Execute to verify action-level interaction evidence capture, and
				// dispatch the remaining trusted browser events directly to avoid 46
				// redundant full-DOM summary walks per cohort under -race.
				if i <= 2 || i == 41 || i == 50 {
					documentActualExecute(t, b, ctx, action)
					continue
				}
				var err error
				if b.touchMode {
					err = b.dispatchTap(ctx, action, selector)
				} else {
					err = b.clickAction(ctx, action, selector)
				}
				if err != nil {
					t.Fatalf("%s: %v", action.Name, err)
				}
			}

			for _, frameBtn := range []string{"#iframe-btn-51", "#iframe-btn-52"} {
				documentActualExecute(t, b, ctx, &protocol.Action{
					Name:     "interact_" + frameBtn,
					Type:     actionType,
					Selector: frameBtn,
					Params:   map[string]any{"frame_selector": "#child-frame"},
				})
			}

			documentActualExecute(t, b, ctx, &protocol.Action{
				Name:     "wait interactions settled",
				Type:     "browser_wait",
				Selector: "#fixture-status",
				WaitFor: &protocol.WaitCondition{
					Type:      "text",
					Value:     "interactions_settled",
					TimeoutMs: 10000,
				},
			})

			// Read window.__apexGroundTruth exactly once prior to teardown.
			var groundTruth []groundTruthRecord
			if err := b.evaluatePageExpression(ctx, `window.__apexGroundTruth`, &groundTruth); err != nil {
				t.Fatalf("read window.__apexGroundTruth: %v", err)
			}
			if len(groundTruth) == 0 {
				t.Fatal("window.__apexGroundTruth is empty")
			}

			seenGroups := map[string]bool{}
			var swPayload string
			var summary *groundTruthRecord
			groupedEvents := map[int64]float64{}
			var shifts []groundTruthRecord
			for i := range groundTruth {
				rec := groundTruth[i]
				switch rec.Kind {
				case "interaction":
					if !rec.IsTrusted {
						t.Fatalf("group %s dispatched untrusted interaction in cohort %s: %+v", rec.GroupID, cohort.name, rec)
					}
					seenGroups[rec.GroupID] = true
				case "service_worker":
					swPayload = rec.Payload
				case "event_timing":
					if rec.InteractionID != 0 {
						groupedEvents[rec.InteractionID] = max(groupedEvents[rec.InteractionID], rec.DurationMS)
					}
				case "layout_shift":
					if !rec.HadRecentInput {
						shifts = append(shifts, rec)
					}
				case "summary":
					r := rec
					summary = &r
				}
			}
			if len(seenGroups) != 52 {
				t.Fatalf("distinct trusted interaction groups = %d, want 52", len(seenGroups))
			}
			if summary == nil || summary.DistinctGroups != 52 {
				t.Fatalf("summary record missing or incomplete: %+v", summary)
			}
			if !strings.Contains(swPayload, `"cached_by":"service_worker"`) {
				t.Fatalf("service worker did not serve /sw-cached.json: %q", swPayload)
			}

			networkEntries := b.networkEntriesSince(0)
			if err := b.Teardown(ctx); err != nil {
				t.Fatalf("Teardown: %v", err)
			}
			interactions := b.TakeBrowserInteractionEvidence()

			// Verify zero unapproved /sidecar HTTP requests and SW interception.
			reqMu.Lock()
			counts := make(map[string]int, len(requestCounts))
			for k, v := range requestCounts {
				counts[k] = v
			}
			reqMu.Unlock()
			if counts["/sw-cached.json"] != 0 {
				t.Fatalf("/sw-cached.json hit target server %d times, want 0 (service-worker answered)", counts["/sw-cached.json"])
			}
			for path := range counts {
				switch {
				case strings.HasPrefix(path, "/launch/"), path == "/frame.html", path == "/app.css", path == "/sw.js", path == "/favicon.ico":
				default:
					t.Fatalf("unexpected target HTTP request path %q in %v", path, counts)
				}
			}

			// Verify redaction across interaction routes and resource origins.
			for _, batch := range interactions {
				for _, item := range batch.Interactions {
					if strings.Contains(item.Route, secret) {
						t.Fatalf("interaction route leaked secret: %q", item.Route)
					}
				}
			}
			for _, entry := range networkEntries {
				if entry.BrowserEvidence != nil && strings.Contains(entry.BrowserEvidence.Origin, secret) {
					t.Fatalf("resource origin leaked secret: %q", entry.BrowserEvidence.Origin)
				}
			}

			// Verify single final document population (no duplicate finals or leftover provisionals).
			docEvidence := b.BrowserDocumentEvidence()
			if docEvidence == nil || docEvidence.Clone() == nil {
				t.Fatal("BrowserDocumentEvidence is nil or invalid")
			}
			seenMeasurements := map[string]bool{}
			for _, obs := range docEvidence.Observations {
				if obs.State == "provisional" {
					t.Fatalf("leftover provisional observation after teardown: %+v", obs)
				}
				if seenMeasurements[obs.MeasurementID] {
					t.Fatalf("duplicate publication for measurement_id %q", obs.MeasurementID)
				}
				seenMeasurements[obs.MeasurementID] = true
			}

			inpMain := documentActualMetric(t, docEvidence, "inp", "main_frame")
			clsMain := documentActualMetric(t, docEvidence, "cls", "main_frame")

			supportsINP := slices.Contains(summary.SupportedEntryTypes, "event")
			supportsCLS := slices.Contains(summary.SupportedEntryTypes, "layout-shift")

			if supportsINP {
				// 1. INP ground-truth rank verification (floor(N/50) = 1 at N >= 50).
				if summary.InteractionCount < 50 {
					t.Fatalf("%s interactionCount = %d, want >= 50", cohort.name, summary.InteractionCount)
				}
				values := make([]float64, 0, len(groupedEvents))
				for _, dur := range groupedEvents {
					values = append(values, dur)
				}
				sort.Sort(sort.Reverse(sort.Float64Slice(values)))
				if len(values) < 3 {
					t.Fatalf("expected >= 3 distinct slow/threshold event timing interactions, got %v", values)
				}
				rank := min(len(values)-1, summary.InteractionCount/50)
				if rank != 1 {
					t.Fatalf("expected rank index 1 for interactionCount=%d, got %d", summary.InteractionCount, rank)
				}
				expectedINP := values[rank]
				if values[0] <= expectedINP {
					t.Fatalf("top outlier (%g) is not strictly greater than rank-1 outlier (%g): %v", values[0], expectedINP, values[:3])
				}
				var matchedINP bool
				for _, o := range inpMain {
					if o.State == "final" && o.Available && o.InteractionCount >= 50 {
						matchedINP = true
						wantQualified := o.Cohort != nil && o.Cohort.Eligible()
						if o.Qualified != wantQualified || o.Value == nil || *o.Value != expectedINP {
							t.Fatalf("%s INP mismatch: got=%+v want=%g wantQualified=%v (top3=%v count=%d)", cohort.name, o, expectedINP, wantQualified, values[:3], summary.InteractionCount)
						}
					}
				}
				if !matchedINP {
					t.Fatalf("no final available main_frame INP observation with interaction_count >= 50 in %+v", inpMain)
				}
			} else {
				var finals int
				for _, o := range inpMain {
					if o.State == "final" {
						finals++
						if o.Available || o.Qualified || o.Value != nil || o.UnavailableReason != "unsupported" {
							t.Fatalf("%s inp invented a value or wrong reason: %+v", cohort.name, o)
						}
					}
				}
				if finals == 0 {
					t.Fatalf("%s missing final observation for inp", cohort.name)
				}
			}

			if supportsCLS {
				// 2. CLS session-window verification.
				if len(shifts) < 3 {
					t.Fatalf("%s layout shifts = %d, want >= 3", cohort.name, len(shifts))
				}
				var expectedCLS, sumCLS, sessionCLS, startCLS, lastCLS float64
				for i, s := range shifts {
					sumCLS += s.Value
					if i > 0 && s.StartTimeMS-lastCLS < 1000 && s.StartTimeMS-startCLS < 5000 {
						sessionCLS += s.Value
					} else {
						sessionCLS = s.Value
						startCLS = s.StartTimeMS
					}
					lastCLS = s.StartTimeMS
					expectedCLS = max(expectedCLS, sessionCLS)
				}
				if expectedCLS <= 0 || sumCLS <= expectedCLS {
					t.Fatalf("CLS session windows did not separate max window (%g) from total sum (%g): shifts=%+v", expectedCLS, sumCLS, shifts)
				}
				var matchedCLS bool
				for _, o := range clsMain {
					if o.State == "final" && o.Available {
						matchedCLS = true
						wantQualified := o.Cohort != nil && o.Cohort.Eligible()
						if o.Qualified != wantQualified || o.Value == nil || math.Abs(*o.Value-expectedCLS) > 1e-6 {
							t.Fatalf("%s CLS mismatch: got=%+v want=%g sum=%g", cohort.name, o, expectedCLS, sumCLS)
						}
					}
				}
				if !matchedCLS {
					t.Fatalf("no final available main_frame CLS observation in %+v", clsMain)
				}
			} else {
				var finals int
				for _, o := range clsMain {
					if o.State == "final" {
						finals++
						if o.Available || o.Qualified || o.Value != nil || o.UnavailableReason != "unsupported" {
							t.Fatalf("%s cls invented a value or wrong reason: %+v", cohort.name, o)
						}
					}
				}
				if finals == 0 {
					t.Fatalf("%s missing final observation for cls", cohort.name)
				}
			}

			if cohort.engine == "chromium" {
				// 3. Per-response CDP service-worker and HTTP cache provenance.
				swEntries := entriesFor(networkEntries, "/sw-cached.json")
				if len(swEntries) == 0 {
					t.Fatal("missing network entry for /sw-cached.json")
				}
				swEv := swEntries[0].BrowserEvidence
				if swEv == nil || swEv.ServiceWorker == nil || !*swEv.ServiceWorker || swEv.ServiceWorkerUnavailableReason != "" || swEv.Protocol != "" || swEv.ProtocolUnavailableReason != "service_worker" || swEv.DocumentID == "" || swEv.DocumentAssociation != "cdp_loader_join" {
					t.Fatalf("chromium /sw-cached.json provenance mismatch: %+v", swEv)
				}

				cssEntries := entriesFor(networkEntries, "/app.css")
				if len(cssEntries) < 2 {
					t.Fatalf("expected >= 2 /app.css entries (main + child frame), got %d", len(cssEntries))
				}
				firstCSS := cssEntries[0].BrowserEvidence
				secondCSS := cssEntries[len(cssEntries)-1].BrowserEvidence
				if firstCSS == nil || firstCSS.Cache == nil || *firstCSS.Cache {
					t.Fatalf("first /app.css load should not be cached: %+v", firstCSS)
				}
				if secondCSS == nil || secondCSS.Cache == nil || !*secondCSS.Cache || secondCSS.CacheUnavailableReason != "" || secondCSS.Protocol != "" || secondCSS.ProtocolUnavailableReason != "cached" {
					t.Fatalf("second /app.css load should report CDP cache hit: %+v", secondCSS)
				}
			} else {
				if supportsCLS {
					t.Fatalf("expected webkit to lack layout-shift support, got %v", summary.SupportedEntryTypes)
				}
				// WebKit unproved cache cases remain explicitly unavailable ("not_exposed").
				for _, entry := range networkEntries {
					if entry.BrowserEvidence == nil {
						continue
					}
					if entry.BrowserEvidence.Cache != nil || entry.BrowserEvidence.CacheUnavailableReason != "not_exposed" {
						t.Fatalf("webkit resource invented cache provenance: %+v", entry.BrowserEvidence)
					}
				}
			}
		})
	}
}
