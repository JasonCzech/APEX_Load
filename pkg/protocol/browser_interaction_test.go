package protocol

import (
	"encoding/json"
	"fmt"
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

// A record past the byte bound splits into in-order records within it, each
// valid, with the drop count and incompleteness on the last.
func TestSplitBrowserInteractionEvidenceKeepsEveryInteractionWithinTheBound(t *testing.T) {
	base := interactionFixture()
	record := &BrowserInteractionEvidence{Version: BrowserInteractionVersion, SourceGeneration: base.SourceGeneration, Dropped: 3, Incomplete: true}
	for n := 0; ; n++ {
		interaction := base
		interaction.InteractionID = fmt.Sprintf("%s-i%d", base.SourceGeneration, n)
		interaction.Route = "/" + strings.Repeat("r", 250)
		record.Interactions = append(record.Interactions, interaction)
		if raw, _ := json.Marshal(record); len(raw) > MaxBrowserInteractionEvidenceBytes+20_000 {
			break
		}
	}
	if same := SplitBrowserInteractionEvidence(&BrowserInteractionEvidence{Version: record.Version, SourceGeneration: record.SourceGeneration, Interactions: record.Interactions[:2]}); len(same) != 1 || len(same[0].Interactions) != 2 {
		t.Fatalf("a record within the bound was split: %d", len(same))
	}
	parts := SplitBrowserInteractionEvidence(record)
	if len(parts) < 2 {
		t.Fatalf("parts %d", len(parts))
	}
	next := 0
	for k, part := range parts {
		raw, err := json.Marshal(part)
		if err != nil || len(raw) > MaxBrowserInteractionEvidenceBytes {
			t.Fatalf("part %d is %d bytes", k, len(raw))
		}
		if _, err := DecodeBrowserInteractionEvidence(raw); err != nil {
			t.Fatalf("part %d does not decode: %v", k, err)
		}
		for _, interaction := range part.Interactions {
			if interaction.InteractionID != record.Interactions[next].InteractionID {
				t.Fatalf("interaction %d out of order", next)
			}
			next++
		}
		last := k == len(parts)-1
		if last != (part.Dropped == 3 && part.Incomplete) || !last && (part.Dropped != 0 || part.Incomplete) {
			t.Fatalf("part %d dropped %d incomplete %v", k, part.Dropped, part.Incomplete)
		}
	}
	if next != len(record.Interactions) {
		t.Fatalf("split kept %d of %d interactions", next, len(record.Interactions))
	}
}

func TestBrowserInteractionV4OutcomesAndFrameChainContract(t *testing.T) {
	gen := strings.Repeat("a", 32)
	step := NewBrowserOriginalAuthoredStep(BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("b", 64), StructureSHA256: strings.Repeat("c", 64)}, "actions[1]")
	pageID := BrowserAuthoredPageID(step)
	start := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	obs := start.Add(50 * time.Millisecond)
	deadline := start.Add(1000 * time.Millisecond)
	captured := start.Add(120 * time.Millisecond)
	clock := &BrowserOutcomeClock{
		Version:    1,
		StartedAt:  start,
		DeadlineAt: deadline,
		ObservedAt: obs,
		BudgetMS:   1000,
		ElapsedMS:  50,
	}
	hop := BrowserInteractionFrameHop{
		BoundingX:      40,
		BoundingY:      30,
		BoundingWidth:  200,
		BoundingHeight: 100,
		OffsetWidth:    400,
		OffsetHeight:   200,
		ClientLeft:     4,
		ClientTop:      6,
		PaddingLeft:    2,
		PaddingTop:     4,
		ScrollX:        10,
		ScrollY:        20,
		Matrix:         []float64{0.5, 0, 0, 0.5, 10, 20},
	}
	if !hop.Valid() || !hop.Invertible2D() {
		t.Fatalf("valid hop rejected: %+v", hop)
	}
	mapped, ok := hop.ApplyToPoint(BrowserInteractionPoint{X: 100, Y: 50})
	// px = 4 + 2 + 100 = 106, py = 6 + 4 + 50 = 60; scaled by 0.5 -> (53, 30) + Bounding(40, 30) = (93, 60)
	if !ok || mapped.X != 93 || mapped.Y != 60 {
		t.Fatalf("ApplyToPoint = (%v, %v), ok=%t, want (93, 60)", mapped.X, mapped.Y, ok)
	}
	v4 := BrowserInteraction{
		Version:            BrowserInteractionVersion4,
		SourceGeneration:   gen,
		InteractionID:      gen + "-i1",
		ActionID:           gen + "-a1",
		DocumentID:         gen + "-d1",
		FrameScope:         "child_frame",
		Kind:               "download",
		PointerType:        "mouse",
		Points:             []BrowserInteractionPoint{{X: 100, Y: 50}},
		ViewportWidth:      1280,
		ViewportHeight:     720,
		DeviceScaleFactor:  1,
		LayoutRevision:     "0123456789abcdef",
		Route:              "/files",
		Provenance:         InteractionProvenanceScripted,
		Success:            true,
		CapturedAt:         captured,
		GeometryProvenance: InteractionGeometryProvenanceDispatch,
		AuthoredPageID:     pageID,
		RouteSeq:           2,
		FrameChain:         []BrowserInteractionFrameHop{hop},
		AuthoredStep:       step,
		Execution:          &BrowserActionExecution{Invocation: 1, Attempt: 1},
		Dispatch: &BrowserInteractionDispatch{
			At:           start,
			TimeOriginMS: float64(start.UnixMilli()),
			OffsetMS:     0,
			DocumentID:   gen + "-d1",
			LifecycleID:  gen + "-l1",
		},
		Outcomes: []BrowserInteractionOutcome{
			{Kind: InteractionOutcomeKindSync, Declared: "element_visible", DeadlineMS: 1000, Observed: InteractionOutcomeMet, Clock: clock.Clone()},
			{Kind: InteractionOutcomeKindDialog, Declared: "dialog", DeadlineMS: 1000, Observed: InteractionOutcomeMet, Clock: clock.Clone()},
			{Kind: InteractionOutcomeKindDownload, Declared: "download", DeadlineMS: 1000, Observed: InteractionOutcomeMet, Clock: clock.Clone()},
			{Kind: InteractionOutcomeKindAssertion, Declared: "element_visible", Observed: InteractionOutcomeMet, ObservedAt: &obs},
			{Kind: InteractionOutcomeKindNavigation, Declared: BrowserAuthoredPageAssociationSameDocumentRoute, Observed: InteractionOutcomeMet, ObservedAt: &obs, NavigationSequence: 2, AuthoredPageID: pageID, RouteSeq: 2},
		},
	}
	if !v4.Valid() {
		t.Fatalf("valid v4 interaction rejected: %+v", v4)
	}
	if v4.PrimaryOutcome().Kind != InteractionOutcomeKindSync || len(v4.DeclaredOutcomes()) != 5 {
		t.Fatalf("outcomes accessors: primary=%+v all=%+v", v4.PrimaryOutcome(), v4.DeclaredOutcomes())
	}
	evidence := &BrowserInteractionEvidence{Version: BrowserInteractionVersion4, SourceGeneration: gen, Interactions: []BrowserInteraction{v4}}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBrowserInteractionEvidence(raw)
	if err != nil || len(decoded.Interactions) != 1 || len(decoded.Interactions[0].Outcomes) != 5 || len(decoded.Interactions[0].FrameChain) != 1 {
		t.Fatalf("v4 decode failed: %+v err=%v", decoded, err)
	}
	// Reject non-zero legacy Outcome on v4, or Outcomes on v3, or unmapped child_frame with Dispatch != nil.
	badLegacy := v4
	badLegacy.Outcome = BrowserInteractionOutcome{Observed: InteractionOutcomeNotDeclared}
	if badLegacy.Valid() {
		t.Fatal("v4 accepted non-zero legacy Outcome field")
	}
	badSingular := v4
	singularHop := hop
	singularHop.Matrix = []float64{1, 0, 1, 0, 0, 0}
	badSingular.FrameChain = []BrowserInteractionFrameHop{singularHop}
	if badSingular.Valid() {
		t.Fatal("v4 accepted singular matrix on dispatch-qualified child frame")
	}
	badEmptyChain := v4
	badEmptyChain.FrameChain = nil
	if badEmptyChain.Valid() {
		t.Fatal("v4 accepted empty FrameChain on dispatch-qualified child frame")
	}
	badDepthChain := v4
	badDepthChain.FrameChain = make([]BrowserInteractionFrameHop, MaxBrowserInteractionFrameDepth+1)
	for idx := range badDepthChain.FrameChain {
		badDepthChain.FrameChain[idx] = hop
	}
	if badDepthChain.Valid() {
		t.Fatal("v4 accepted FrameChain exceeding MaxBrowserInteractionFrameDepth")
	}
	depthExceeded := v4
	depthExceeded.Dispatch = nil
	depthExceeded.Points = nil
	depthExceeded.PointerType = ""
	depthExceeded.FrameChain = nil
	depthExceeded.GeometryProvenance = InteractionGeometryProvenancePostAction
	depthExceeded.UnavailableReason = InteractionUnavailableFrameDepthExceeded
	depthExceeded.DispatchUnavailableReason = InteractionUnavailableFrameDepthExceeded
	if !depthExceeded.Valid() {
		t.Fatalf("valid frame_depth_exceeded v4 interaction rejected: %+v", depthExceeded)
	}
}
