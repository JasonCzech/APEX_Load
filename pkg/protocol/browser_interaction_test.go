package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func interactionFixture() BrowserInteraction {
	gen := strings.Repeat("a", 32)
	return BrowserInteraction{Version: BrowserInteractionVersion, SourceGeneration: gen, InteractionID: gen + "-i1", ActionID: gen + "-a1", DocumentID: gen + "-idoc-" + strings.Repeat("b", 32), FrameScope: "main_frame", Kind: "click", PointerType: "mouse", Points: []BrowserInteractionPoint{{X: 140, Y: 170}},
		ViewportWidth: 1000, ViewportHeight: 700, DeviceScaleFactor: 1, LayoutRevision: "0123456789abcdef", Route: "/orders/:id", Outcome: BrowserInteractionOutcome{Observed: InteractionOutcomeNotDeclared}, Provenance: InteractionProvenanceScripted, Success: true, CapturedAt: time.Now().UTC()}
}

func TestBrowserInteractionValidation(t *testing.T) {
	if !interactionFixture().Valid() {
		t.Fatal("valid interaction refused")
	}
	for name, mutate := range map[string]func(*BrowserInteraction){
		"kind":               func(i *BrowserInteraction) { i.Kind = "type_password" },
		"points on keyboard": func(i *BrowserInteraction) { i.Kind = "fill" },
		"target on click":    func(i *BrowserInteraction) { i.Target = &BrowserInteractionTarget{Role: "button"} },
		"role": func(i *BrowserInteraction) {
			i.Kind, i.Points, i.Target = "fill", nil, &BrowserInteractionTarget{Role: "Text Box"}
		},
		"label hash": func(i *BrowserInteraction) {
			i.Kind, i.Points, i.Target = "fill", nil, &BrowserInteractionTarget{Role: "textbox", LabelSHA256: "e-mail"}
		},
		"route query":    func(i *BrowserInteraction) { i.Route = "/orders?id=1" },
		"route relative": func(i *BrowserInteraction) { i.Route = "orders" },
		"provenance":     func(i *BrowserInteraction) { i.Provenance = "human" },
		"outcome":        func(i *BrowserInteraction) { i.Outcome = BrowserInteractionOutcome{Observed: InteractionOutcomeMet} },
		"undeclared deadline": func(i *BrowserInteraction) {
			i.Outcome = BrowserInteractionOutcome{Observed: InteractionOutcomeNotDeclared, DeadlineMS: 5}
		},
		"foreign identity": func(i *BrowserInteraction) { i.InteractionID = "other-i1" },
		"nan point":        func(i *BrowserInteraction) { i.Points[0].X = nan() },
		"too many points": func(i *BrowserInteraction) {
			i.Points = make([]BrowserInteractionPoint, 9)
		},
		"layout": func(i *BrowserInteraction) { i.LayoutRevision = "<div>" },
		"frame":  func(i *BrowserInteraction) { i.FrameScope = "frame" },
		"time":   func(i *BrowserInteraction) { i.CapturedAt = time.Time{} },
	} {
		i := interactionFixture()
		i.Points = append([]BrowserInteractionPoint(nil), i.Points...)
		mutate(&i)
		if i.Valid() {
			t.Errorf("%s admitted", name)
		}
	}
	keyboard := interactionFixture()
	keyboard.Kind, keyboard.Points, keyboard.PointerType = "fill", nil, ""
	keyboard.Target = &BrowserInteractionTarget{Role: "textbox", LabelSHA256: strings.Repeat("c", 64)}
	keyboard.Outcome = BrowserInteractionOutcome{Declared: "selector", DeadlineMS: 2000, Observed: InteractionOutcomeMissed}
	if !keyboard.Valid() {
		t.Fatal("keyboard interaction refused")
	}
}

func nan() float64 { var zero float64; return zero / zero }

func TestBrowserInteractionEvidenceRoundTripsStrictly(t *testing.T) {
	first, second := interactionFixture(), interactionFixture()
	second.InteractionID = second.SourceGeneration + "-i2"
	evidence := &BrowserInteractionEvidence{Version: BrowserInteractionVersion, SourceGeneration: first.SourceGeneration, Interactions: []BrowserInteraction{first, second}, Dropped: 3, Incomplete: true}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBrowserInteractionEvidence(raw)
	if err != nil || len(decoded.Interactions) != 2 || decoded.Dropped != 3 || !decoded.Incomplete {
		t.Fatalf("round trip %+v %v", decoded, err)
	}
	decoded.Interactions[0].Points[0].X = 1
	if evidence.Interactions[0].Points[0].X != 140 {
		t.Fatal("decoded evidence aliases its source")
	}
	for name, mutate := range map[string]func(*BrowserInteractionEvidence){
		"duplicate id":       func(e *BrowserInteractionEvidence) { e.Interactions[1].InteractionID = e.Interactions[0].InteractionID },
		"mixed generation":   func(e *BrowserInteractionEvidence) { e.SourceGeneration = strings.Repeat("c", 32) },
		"negative drops":     func(e *BrowserInteractionEvidence) { e.Dropped = -1 },
		"version":            func(e *BrowserInteractionEvidence) { e.Version = 2 },
		"invalid inner item": func(e *BrowserInteractionEvidence) { e.Interactions[0].Kind = "" },
	} {
		bad := *evidence
		bad.Interactions = append([]BrowserInteraction(nil), evidence.Interactions...)
		mutate(&bad)
		if bad.Clone() != nil {
			t.Errorf("%s admitted", name)
		}
	}
	if _, err := DecodeBrowserInteractionEvidence([]byte(`{"version":1,"source_generation":"` + first.SourceGeneration + `","interactions":[],"dropped":0,"incomplete":false,"extra":1}`)); err == nil {
		t.Fatal("unknown field admitted")
	}
}
