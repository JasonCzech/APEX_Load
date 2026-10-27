package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func v4InteractionFixture(n int, dispatch time.Duration) protocol.BrowserInteraction {
	start := e4Start.Add(dispatch)
	obs := start.Add(40 * time.Millisecond)
	deadline := start.Add(1000 * time.Millisecond)
	captured := start.Add(80 * time.Millisecond)
	step := protocol.NewBrowserOriginalAuthoredStep(protocol.BrowserAuthoredDefinition{
		Version:         1,
		ScriptSHA256:    strings.Repeat("a", 64),
		StructureSHA256: strings.Repeat("b", 64),
	}, "actions[1]")
	i := protocol.BrowserInteraction{
		Version:            protocol.BrowserInteractionVersion4,
		SourceGeneration:   e4Generation,
		InteractionID:      e4Generation + "-i" + string(rune('0'+n)),
		ActionID:           e4Generation + "-a" + string(rune('0'+n)),
		DocumentID:         e4Generation + "-doc-1",
		FrameScope:         "main_frame",
		Kind:               "click",
		PointerType:        "mouse",
		Points:             []protocol.BrowserInteractionPoint{{X: 100, Y: 100}},
		ViewportWidth:      400,
		ViewportHeight:     800,
		DeviceScaleFactor:  2,
		LayoutRevision:     "00000000000000aa",
		Route:              "/cart",
		Provenance:         protocol.InteractionProvenanceScripted,
		Success:            true,
		CapturedAt:         captured,
		GeometryProvenance: protocol.InteractionGeometryProvenanceDispatch,
		AuthoredPageID:     protocol.BrowserAuthoredPageID(step),
		RouteSeq:           1,
		AuthoredStep:       step,
		Execution:          &protocol.BrowserActionExecution{Invocation: uint64(n), Attempt: 1},
		Dispatch: &protocol.BrowserInteractionDispatch{
			At:           start,
			TimeOriginMS: float64(e4Start.UnixMilli()),
			OffsetMS:     float64(dispatch / time.Millisecond),
			DocumentID:   e4Generation + "-doc-1",
			LifecycleID:  e4Generation + "-life-1",
		},
		Outcomes: []protocol.BrowserInteractionOutcome{{
			Kind:       protocol.InteractionOutcomeKindSync,
			Declared:   "element_visible",
			DeadlineMS: 1000,
			Observed:   protocol.InteractionOutcomeMet,
			Clock: &protocol.BrowserOutcomeClock{
				Version:    1,
				StartedAt:  start,
				DeadlineAt: deadline,
				ObservedAt: obs,
				BudgetMS:   1000,
				ElapsedMS:  40,
			},
		}},
	}
	if !i.Valid() {
		panic("invalid v4 fixture")
	}
	return i
}

func TestBrowserInteractionV4ChildFrameMappingAndExclusions(t *testing.T) {
	mappedChild := v4InteractionFixture(1, 0)
	mappedChild.FrameScope = "child_frame"
	mappedChild.Points = []protocol.BrowserInteractionPoint{{X: 40, Y: 20}}
	mappedChild.FrameChain = []protocol.BrowserInteractionFrameHop{{
		BoundingX:      50,
		BoundingY:      100,
		BoundingWidth:  200,
		BoundingHeight: 200,
		OffsetWidth:    200,
		OffsetHeight:   200,
		ClientLeft:     5,
		ClientTop:      5,
		PaddingLeft:    5,
		PaddingTop:     5,
		Matrix:         []float64{1, 0, 0, 1, 0, 0},
	}}
	if !mappedChild.Valid() {
		t.Fatal("valid mappedChild rejected")
	}

	xoriginChild := v4InteractionFixture(2, time.Second)
	xoriginChild.FrameScope = "child_frame"
	xoriginChild.Points = nil
	xoriginChild.PointerType = ""
	xoriginChild.Dispatch = nil
	xoriginChild.GeometryProvenance = protocol.InteractionGeometryProvenancePostAction
	xoriginChild.UnavailableReason = protocol.InteractionUnavailableCrossOriginAncestry
	xoriginChild.DispatchUnavailableReason = protocol.InteractionUnavailableCrossOriginAncestry
	xoriginChild.FrameChain = []protocol.BrowserInteractionFrameHop{{CrossOrigin: true}}
	if !xoriginChild.Valid() {
		t.Fatal("valid xoriginChild rejected")
	}

	non2DChild := v4InteractionFixture(3, 2*time.Second)
	non2DChild.FrameScope = "child_frame"
	non2DChild.Points = nil
	non2DChild.PointerType = ""
	non2DChild.Dispatch = nil
	non2DChild.GeometryProvenance = protocol.InteractionGeometryProvenancePostAction
	non2DChild.UnavailableReason = protocol.InteractionUnavailableNonAxisAlignedOr3DTransform
	non2DChild.DispatchUnavailableReason = protocol.InteractionUnavailableNonAxisAlignedOr3DTransform
	non2DChild.FrameChain = []protocol.BrowserInteractionFrameHop{{
		BoundingWidth:  100,
		BoundingHeight: 100,
		OffsetWidth:    100,
		OffsetHeight:   100,
		Non2DTransform: true,
	}}
	if !non2DChild.Valid() {
		t.Fatal("valid non2DChild rejected")
	}

	postActionMain := v4InteractionFixture(4, 3*time.Second)
	postActionMain.Dispatch = nil
	postActionMain.DispatchUnavailableReason = "not_observed"
	postActionMain.GeometryProvenance = protocol.InteractionGeometryProvenancePostAction
	if !postActionMain.Valid() {
		t.Fatal("valid postActionMain rejected")
	}

	missedDialog := v4InteractionFixture(5, 4*time.Second)
	start := missedDialog.Dispatch.At
	deadline := start.Add(1500 * time.Millisecond)
	missedDialog.CapturedAt = deadline.Add(10 * time.Millisecond)
	missedDialog.Outcomes = []protocol.BrowserInteractionOutcome{{
		Kind:       protocol.InteractionOutcomeKindDialog,
		Declared:   "dialog",
		DeadlineMS: 1500,
		Observed:   protocol.InteractionOutcomeMissed,
		Reason:     "deadline_missed",
		Clock: &protocol.BrowserOutcomeClock{
			Version:    1,
			StartedAt:  start,
			DeadlineAt: deadline,
			ObservedAt: deadline,
			BudgetMS:   1500,
			ElapsedMS:  1500,
		},
	}}
	if !missedDialog.Valid() {
		t.Fatal("valid missedDialog rejected")
	}

	depthExceededChild := v4InteractionFixture(6, 5*time.Second)
	depthExceededChild.FrameScope = "child_frame"
	depthExceededChild.Points = nil
	depthExceededChild.PointerType = ""
	depthExceededChild.Dispatch = nil
	depthExceededChild.GeometryProvenance = protocol.InteractionGeometryProvenancePostAction
	depthExceededChild.UnavailableReason = protocol.InteractionUnavailableFrameDepthExceeded
	depthExceededChild.DispatchUnavailableReason = protocol.InteractionUnavailableFrameDepthExceeded
	if !depthExceededChild.Valid() {
		t.Fatal("valid depthExceededChild rejected")
	}

	got := analyzeV3(mappedChild, xoriginChild, non2DChild, postActionMain, missedDialog, depthExceededChild)
	if mappedChild.Points[0].X != 40 || mappedChild.Points[0].Y != 20 {
		t.Fatalf("mapInteraction mutated caller Points slice in-place: %+v", mappedChild.Points)
	}
	if got.Coverage.Mapped != 2 || got.Coverage.MappedViaTransform != 1 {
		t.Fatalf("coverage mapped=%d mapped_via_transform=%d: %+v", got.Coverage.Mapped, got.Coverage.MappedViaTransform, got.Coverage)
	}
	if got.Coverage.Excluded[protocol.InteractionUnavailableCrossOriginAncestry] != 1 ||
		got.Coverage.Excluded[protocol.InteractionUnavailableNonAxisAlignedOr3DTransform] != 1 ||
		got.Coverage.Excluded[protocol.InteractionUnavailableFrameDepthExceeded] != 1 ||
		got.Coverage.Excluded["post_action_geometry"] != 1 {
		t.Fatalf("excluded = %+v", got.Coverage.Excluded)
	}
	dead := e4Patterns(got, interactionPatternDeadInteraction)
	if len(dead) != 1 || dead[0].Declared != "dialog" || dead[0].DeadlineMs != 1500 {
		t.Fatalf("dead patterns = %+v", got.Patterns)
	}
	// Verify mappedChild point (40,20) + border/padding (10,10) + bounding (50,100) = (100, 130) mapped into cell [row=130*64/800=10, col=100*64/400=16].
	if len(got.Heatmaps) != 1 {
		t.Fatalf("heatmaps = %+v", got.Heatmaps)
	}
	foundTransformedCell := false
	for _, cell := range got.Heatmaps[0].Cells {
		if cell[0] == 10 && cell[1] == 16 && cell[2] == 1 {
			foundTransformedCell = true
		}
	}
	if !foundTransformedCell {
		t.Fatalf("heatmap cells missing transformed child-frame cell [10, 16]: %+v", got.Heatmaps[0].Cells)
	}
}

func TestArchiveAuthoredPageAccumulatorRetainsDistinctRouteSeqs(t *testing.T) {
	acc := newArchiveAuthoredPageAccumulator()
	gen := strings.Repeat("c", 32)
	def := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	step0 := protocol.NewBrowserOriginalAuthoredStep(def, "actions[0]")
	step1 := protocol.NewBrowserOriginalAuthoredStep(def, "actions[1]")
	step2 := protocol.NewBrowserOriginalAuthoredStep(def, "actions[2]")
	now := time.Now().UTC()

	b0 := &protocol.BrowserAuthoredPageBinding{
		Version:            protocol.BrowserAuthoredPageVersion3,
		State:              "bound",
		ObservedAt:         now,
		AuthoredStep:       step0,
		PageID:             protocol.BrowserAuthoredPageID(step0),
		SourceGeneration:   gen,
		RequestID:          gen + "-r1",
		DocumentID:         gen + "-d1",
		LifecycleID:        gen + "-l1",
		Association:        protocol.BrowserAuthoredPageAssociationRedirectChain,
		NavigationSequence: 1,
		RouteSeq:           0,
	}
	b1 := &protocol.BrowserAuthoredPageBinding{
		Version:            protocol.BrowserAuthoredPageVersion3,
		State:              "bound",
		ObservedAt:         now.Add(10 * time.Millisecond),
		AuthoredStep:       step1,
		PageID:             protocol.BrowserAuthoredPageID(step1),
		SourceGeneration:   gen,
		DocumentID:         gen + "-d1",
		LifecycleID:        gen + "-l1",
		Association:        protocol.BrowserAuthoredPageAssociationSameDocumentRoute,
		NavigationSequence: 2,
		RouteSeq:           1,
	}
	b2 := &protocol.BrowserAuthoredPageBinding{
		Version:            protocol.BrowserAuthoredPageVersion3,
		State:              "bound",
		ObservedAt:         now.Add(20 * time.Millisecond),
		AuthoredStep:       step2,
		PageID:             protocol.BrowserAuthoredPageID(step2),
		SourceGeneration:   gen,
		DocumentID:         gen + "-d1",
		LifecycleID:        gen + "-l1",
		Association:        protocol.BrowserAuthoredPageAssociationSameDocumentRoute,
		NavigationSequence: 3,
		RouteSeq:           2,
	}

	sources := map[string]protocol.BrowserAuthoredDefinition{"browser": def}
	for _, b := range []*protocol.BrowserAuthoredPageBinding{b0, b1, b2} {
		acc.merge(TransactionEvent{
			Group:            "browser",
			ProtocolEvidence: map[string]any{protocol.BrowserAuthoredPageEvidenceKey: b},
		}, sources)
	}
	report := acc.result()
	if report == nil || report.Incomplete || report.ReceiptRecords != 3 || len(report.Bindings) != 3 {
		t.Fatalf("authored page report = %+v", report)
	}
}
