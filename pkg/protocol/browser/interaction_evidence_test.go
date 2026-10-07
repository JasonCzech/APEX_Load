package browser

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserInteractionProvenanceNeedsRecorderConsent(t *testing.T) {
	for want, metadata := range map[string]map[string]any{
		protocol.InteractionProvenanceScripted: nil,
		protocol.InteractionProvenanceUnknown:  {"recorder_step_seq": 3},
		protocol.InteractionProvenanceRecorded: {"recorder_step_seq": 3, "interaction_consent": true},
	} {
		if got := browserInteractionProvenance(&protocol.Action{Metadata: metadata}); got != want {
			t.Errorf("metadata %v: %s, want %s", metadata, got, want)
		}
	}
	if got := browserInteractionProvenance(&protocol.Action{Metadata: map[string]any{"recorder_step_seq": 1, "interaction_consent": "yes"}}); got != protocol.InteractionProvenanceUnknown {
		t.Fatal("a non-boolean consent counted")
	}
}

func TestSanitizeBrowserRouteCollapsesIdentifiers(t *testing.T) {
	for in, want := range map[string]string{
		"/orders/12345/items": "/orders/:id/items",
		"/users/3f2504e0-4f89-11d3-9a0c-0305e82c3301/profile": "/users/:id/profile",
		"/session/deadbeefcafe":                               "/session/:id",
		"/search?q=secret#frag":                               "/search",
		"/a1b2c3/checkout":                                    "/:id/checkout",
		"/products/shoes":                                     "/products/shoes",
		"relative":                                            "",
		"/" + strings.Repeat("x", 80):                         "/:id",
	} {
		if got := sanitizeBrowserRoute(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestBrowserInteractionPointsFollowTheGestureKind(t *testing.T) {
	var page browserInteractionPage
	page.Events = append(page.Events,
		struct {
			T string  `json:"t"`
			X float64 `json:"x"`
			Y float64 `json:"y"`
			P string  `json:"p"`
		}{T: "pointerdown", X: 1, Y: 2, P: "touch"},
		struct {
			T string  `json:"t"`
			X float64 `json:"x"`
			Y float64 `json:"y"`
			P string  `json:"p"`
		}{T: "pointerup", X: 50, Y: 2, P: "touch"})
	if points, pointer := browserInteractionPoints("swipe", page); len(points) != 2 || points[1].X != 50 || pointer != "touch" {
		t.Fatalf("swipe %+v %s", points, pointer)
	}
	if points, _ := browserInteractionPoints("tap", page); len(points) != 1 || points[0].X != 1 {
		t.Fatalf("tap %+v", points)
	}
	if points, _ := browserInteractionPoints("hover", page); len(points) != 0 {
		t.Fatalf("hover without a move %+v", points)
	}
	page.Events[0].P = "laser"
	if _, pointer := browserInteractionPoints("tap", page); pointer != "" {
		t.Fatal("unknown pointer type kept")
	}
}

func TestBrowserInteractionBoundsAndGenerations(t *testing.T) {
	b := &Browser{}
	generation := strings.Repeat("a", 32)
	b.evidence.generation = generation
	interaction := func(document string) protocol.BrowserInteraction {
		return protocol.BrowserInteraction{Version: protocol.BrowserInteractionVersion, SourceGeneration: b.evidence.generation, DocumentID: document, FrameScope: "main_frame", Kind: "click", Provenance: protocol.InteractionProvenanceScripted, CapturedAt: time.Now().UTC(), Outcome: protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared}}
	}
	doc := generation + "-idoc-" + strings.Repeat("b", 32)
	for i := 0; i < protocol.MaxBrowserInteractionsPerDocument+2; i++ {
		b.recordBrowserInteraction(interaction(doc))
	}
	// A new browser context starts a new source generation.
	b.evidence.generation = strings.Repeat("c", 32)
	b.recordBrowserInteraction(interaction(b.evidence.generation + "-idoc-" + strings.Repeat("d", 32)))
	taken := b.TakeBrowserInteractionEvidence()
	if len(taken) != 2 || len(taken[1].Interactions) != 1 || taken[0].SourceGeneration == taken[1].SourceGeneration {
		t.Fatalf("taken %d records", len(taken))
	}
	// Whichever per-document bound binds first (64 KiB, then 1,024), the
	// document kept what fits and counted the rest as dropped.
	kept, bytes := len(taken[0].Interactions), 0
	for _, i := range taken[0].Interactions {
		encoded, _ := json.Marshal(i)
		bytes += len(encoded)
	}
	if kept == 0 || kept > protocol.MaxBrowserInteractionsPerDocument || bytes > protocol.MaxBrowserInteractionBytesPerDocument {
		t.Fatalf("kept %d interactions, %d bytes", kept, bytes)
	}
	if taken[0].Dropped != 0 || taken[1].Dropped != int64(protocol.MaxBrowserInteractionsPerDocument+2-kept) || !taken[1].Incomplete {
		t.Fatalf("drops %d/%d", taken[0].Dropped, taken[1].Dropped)
	}
	if len(b.TakeBrowserInteractionEvidence()) != 0 {
		t.Fatal("interactions handed over twice")
	}
}
