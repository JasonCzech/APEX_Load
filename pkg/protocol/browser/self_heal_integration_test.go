package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/selector"
)

// TestSelectorSelfHealFallsThroughRankedCandidates replays a click whose
// primary selector no longer exists (simulating app drift) and verifies the
// engine heals via the ranked fallback recorded at capture time, emitting
// structured evidence.
func TestSelectorSelfHealFallsThroughRankedCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
  <button id="checkout-v2" data-testid="checkout" onclick="document.title='clicked'">Checkout</button>
</body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = b.Teardown(context.Background()) }()

	navResult, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "open",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil || navResult.ErrorMessage != "" {
		t.Fatalf("navigate failed: err=%v msg=%s", err, navResult.ErrorMessage)
	}

	// Primary selector #checkout (stale) fails; ranked fallbacks carry the
	// data-testid hook that still resolves.
	action := &protocol.Action{
		Name:     "Click Checkout",
		Type:     "browser_click",
		Selector: "#checkout",
		WaitFor:  &protocol.WaitCondition{TimeoutMs: 1500},
		Metadata: map[string]any{
			selector.MetadataFallbacksKey: []any{
				map[string]any{"kind": "css", "value": ".gone-too", "confidence": 90.0},
				map[string]any{"kind": "css", "value": `[data-testid="checkout"]`, "confidence": 95.0, "reason": "test hook"},
			},
		},
	}
	result, err := b.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if result.ErrorMessage != "" {
		t.Fatalf("click was not healed: %s", result.ErrorMessage)
	}

	evidence, ok := result.ProtocolEvidence["selector_self_heal"].(map[string]any)
	if !ok {
		t.Fatalf("self-heal evidence missing: %+v", result.ProtocolEvidence)
	}
	if evidence["primary_selector"] != "#checkout" {
		t.Errorf("evidence primary = %v", evidence["primary_selector"])
	}
	if evidence["healed_selector"] != `[data-testid="checkout"]` {
		t.Errorf("evidence healed = %v", evidence["healed_selector"])
	}
	if evidence["confidence"] != 95 {
		t.Errorf("evidence confidence = %v", evidence["confidence"])
	}
	metrics, ok := result.ProtocolEvidence["selector_self_heal_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("self-heal metrics missing: %+v", result.ProtocolEvidence)
	}
	if metrics["attempts"] != 1 || metrics["cache_hit"] != false || metrics["success"] != true {
		t.Fatalf("self-heal metrics = %+v, want one non-cached successful attempt", metrics)
	}

	cached, err := b.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("cached execute returned error: %v", err)
	}
	if cached.ErrorMessage != "" {
		t.Fatalf("cached click was not healed: %s", cached.ErrorMessage)
	}
	cachedMetrics, ok := cached.ProtocolEvidence["selector_self_heal_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("cached self-heal metrics missing: %+v", cached.ProtocolEvidence)
	}
	if cachedMetrics["cache_hit"] != true || cachedMetrics["success"] != true {
		t.Fatalf("cached metrics = %+v, want cache_hit successful heal", cachedMetrics)
	}
}

// TestSelectorSelfHealDoesNotMaskRealFailures verifies that when no fallback
// resolves either, the original failure surfaces unchanged.
func TestSelectorSelfHealDoesNotMaskRealFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body><p>empty</p></body></html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = b.Teardown(context.Background()) }()

	if _, err := b.Execute(context.Background(), &protocol.Action{
		Name: "open", Type: "browser_navigate", Target: "/",
	}); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	action := &protocol.Action{
		Name:     "Click Missing",
		Type:     "browser_click",
		Selector: "#missing",
		WaitFor:  &protocol.WaitCondition{TimeoutMs: 1000},
		Metadata: map[string]any{
			selector.MetadataFallbacksKey: []any{
				map[string]any{"kind": "css", "value": "#also-missing", "confidence": 90.0},
			},
		},
	}
	result, _ := b.Execute(context.Background(), action)
	if result.ErrorMessage == "" && result.Success {
		t.Fatal("expected failure when no candidate resolves")
	}
	if _, healed := result.ProtocolEvidence["selector_self_heal"]; healed {
		t.Error("no heal evidence expected when all candidates fail")
	}
}
