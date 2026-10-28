package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
		InteractionID:      e4Generation + "-i" + strconv.Itoa(n),
		ActionID:           e4Generation + "-a" + strconv.Itoa(n),
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
	if got.Version != browserInteractionAnalysisVersion4 {
		t.Fatalf("version = %q, want %q", got.Version, browserInteractionAnalysisVersion4)
	}
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

func TestBrowserInteractionPatternsV4CombinedQualificationAndReportFormats(t *testing.T) {
	def := protocol.BrowserAuthoredDefinition{
		Version:         1,
		ScriptSHA256:    strings.Repeat("a", 64),
		StructureSHA256: strings.Repeat("b", 64),
	}
	stepA := protocol.NewBrowserOriginalAuthoredStep(def, "actions[1]")
	stepB := protocol.NewBrowserOriginalAuthoredStep(def, "actions[2]")
	stepRetry := protocol.NewBrowserOriginalAuthoredStep(def, "actions[3]")
	stepMultiDead := protocol.NewBrowserOriginalAuthoredStep(def, "actions[4]")
	stepUnexpectedNav := protocol.NewBrowserOriginalAuthoredStep(def, "actions[5]")

	// 1. Duplicate ActionName on distinct AuthoredStep.StepID, or frame-scope/frame-chain mismatches,
	// never merge into a repeat pattern.
	dup1 := v4InteractionFixture(1, 0)
	dup1.ActionName = "Click Buy"
	dup1.AuthoredStep = stepA
	dup2 := v4InteractionFixture(2, 400*time.Millisecond)
	dup2.ActionName = "Click Buy"
	dup2.AuthoredStep = stepB
	dup3 := v4InteractionFixture(3, 800*time.Millisecond)
	dup3.ActionName = "Click Buy"
	dup3.AuthoredStep = stepA
	if got := analyzeV3(dup1, dup2, dup3); len(got.Patterns) != 0 {
		t.Fatalf("duplicate ActionName with distinct StepID merged into pattern: %+v", got.Patterns)
	}
	frameHop1 := protocol.BrowserInteractionFrameHop{BoundingX: 0, BoundingY: 0, BoundingWidth: 100, BoundingHeight: 100, OffsetWidth: 100, OffsetHeight: 100, Matrix: []float64{1, 0, 0, 1, 0, 0}}
	frameHop2 := frameHop1
	frameHop2.BoundingY = 50
	fm1 := v4InteractionFixture(4, 0)
	fm1.FrameScope = "child_frame"
	fm1.FrameChain = []protocol.BrowserInteractionFrameHop{frameHop1}
	fm2 := v4InteractionFixture(5, 300*time.Millisecond)
	fm2.FrameScope = "child_frame"
	fm2.FrameChain = []protocol.BrowserInteractionFrameHop{frameHop2}
	fm3 := v4InteractionFixture(6, 600*time.Millisecond)
	fm3.FrameScope = "child_frame"
	fm3.FrameChain = []protocol.BrowserInteractionFrameHop{frameHop1}
	if got := analyzeV3(fm1, fm2, fm3); len(got.Patterns) != 0 {
		t.Fatalf("frame-chain mismatch merged into repeat pattern: %+v", got.Patterns)
	}

	// 2. Non-consecutive retry attempt numbers (Attempt 1 + Attempt 3 with Attempt 2 dropped,
	// plus an interleaved unrelated gesture) group into one retry pattern and never count as repetition.
	retry1 := v4InteractionFixture(10, 10*time.Second)
	retry1.ActionName = "Submit Order"
	retry1.AuthoredStep = stepRetry
	retry1.Execution = &protocol.BrowserActionExecution{Invocation: 10, Attempt: 1}
	retry1.Success = false
	interleaved := v4InteractionFixture(11, 10200*time.Millisecond)
	interleaved.ActionName = "Other Step"
	interleaved.AuthoredStep = stepB
	retry3 := v4InteractionFixture(12, 10500*time.Millisecond)
	retry3.ActionName = "Submit Order"
	retry3.AuthoredStep = stepRetry
	retry3.Execution = &protocol.BrowserActionExecution{Invocation: 10, Attempt: 3}
	retry3.Success = true
	retryFollowup := v4InteractionFixture(13, 10800*time.Millisecond)
	retryFollowup.ActionName = "Submit Order"
	retryFollowup.AuthoredStep = stepRetry
	retryFollowup.Execution = &protocol.BrowserActionExecution{Invocation: 11, Attempt: 1}

	// 3. Delayed post-action waits (CapturedAt minutes/hours apart) do not shift Dispatch.At repeat windows,
	// and "recorded" provenance produces repeated_gesture whereas "unknown" produces scripted_repetition.
	rec1 := v4InteractionFixture(20, 20*time.Second)
	rec1.ActionName = "Tap Checkout"
	rec1.AuthoredStep = stepA
	rec1.Provenance = protocol.InteractionProvenanceRecorded
	rec1.CapturedAt = e4Start.Add(10 * time.Minute)
	rec2 := v4InteractionFixture(21, 20500*time.Millisecond)
	rec2.ActionName = "Tap Checkout"
	rec2.AuthoredStep = stepA
	rec2.Provenance = protocol.InteractionProvenanceRecorded
	rec2.CapturedAt = e4Start.Add(20 * time.Minute)
	rec3 := v4InteractionFixture(22, 21*time.Second)
	rec3.ActionName = "Tap Checkout"
	rec3.AuthoredStep = stepA
	rec3.Provenance = protocol.InteractionProvenanceRecorded
	rec3.CapturedAt = e4Start.Add(30 * time.Minute)

	unk1 := v4InteractionFixture(30, 30*time.Second)
	unk1.ActionName = "Tap Promo"
	unk1.AuthoredStep = stepB
	unk1.Provenance = protocol.InteractionProvenanceUnknown
	unk2 := v4InteractionFixture(31, 30400*time.Millisecond)
	unk2.ActionName = "Tap Promo"
	unk2.AuthoredStep = stepB
	unk2.Provenance = protocol.InteractionProvenanceUnknown
	unk3 := v4InteractionFixture(32, 30800*time.Millisecond)
	unk3.ActionName = "Tap Promo"
	unk3.AuthoredStep = stepB
	unk3.Provenance = protocol.InteractionProvenanceUnknown

	// 4. Multi-outcome misses on a single v4 interaction emit separate dead_interaction patterns with distinct IDs,
	// while a missed non-page-response sync (dom_stable) does not emit a dead interaction.
	multiDead := v4InteractionFixture(40, 40*time.Second)
	multiDead.ActionName = "Confirm Modal"
	multiDead.AuthoredStep = stepMultiDead
	dStart := multiDead.Dispatch.At
	dAssertionObs := dStart.Add(2000 * time.Millisecond)
	multiDead.CapturedAt = dStart.Add(3 * time.Second)
	multiDead.Outcomes = []protocol.BrowserInteractionOutcome{
		{
			Kind:       protocol.InteractionOutcomeKindDialog,
			Declared:   "dialog",
			DeadlineMS: 1200,
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "deadline_missed",
			Clock: &protocol.BrowserOutcomeClock{
				Version: 1, StartedAt: dStart, DeadlineAt: dStart.Add(1200 * time.Millisecond), ObservedAt: dStart.Add(1200 * time.Millisecond), BudgetMS: 1200, ElapsedMS: 1200,
			},
		},
		{
			Kind:       protocol.InteractionOutcomeKindAssertion,
			Declared:   "visible",
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "assertion_failed",
			ObservedAt: &dAssertionObs,
		},
		{
			Kind:       protocol.InteractionOutcomeKindSync,
			Declared:   "dom_stable",
			DeadlineMS: 2500,
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "deadline_missed",
			Clock: &protocol.BrowserOutcomeClock{
				Version: 1, StartedAt: dStart, DeadlineAt: dStart.Add(2500 * time.Millisecond), ObservedAt: dStart.Add(2500 * time.Millisecond), BudgetMS: 2500, ElapsedMS: 2500,
			},
		},
	}
	if !multiDead.Valid() {
		t.Fatal("valid multiDead rejected")
	}

	// 5. Navigated v4 interaction with missed assertion emits unexpected_navigation, never dead_interaction.
	unexpectedNav := v4InteractionFixture(50, 50*time.Second)
	unexpectedNav.ActionName = "Stay On Cart"
	unexpectedNav.AuthoredStep = stepUnexpectedNav
	nStart := unexpectedNav.Dispatch.At
	nNavObs := nStart.Add(300 * time.Millisecond)
	nAssertObs := nStart.Add(1500 * time.Millisecond)
	unexpectedNav.CapturedAt = nStart.Add(2 * time.Second)
	unexpectedNav.Outcomes = []protocol.BrowserInteractionOutcome{
		{
			Kind:               protocol.InteractionOutcomeKindNavigation,
			Declared:           "load",
			Observed:           protocol.InteractionOutcomeMet,
			ObservedAt:         &nNavObs,
			NavigationSequence: 1,
		},
		{
			Kind:       protocol.InteractionOutcomeKindAssertion,
			Declared:   "visible",
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "assertion_failed",
			ObservedAt: &nAssertObs,
		},
	}
	if !unexpectedNav.Valid() {
		t.Fatal("valid unexpectedNav rejected")
	}

	// 6. Scroll, layout, child-frame transform, and unknown cohort separation.
	scrolled := v4InteractionFixture(60, 60*time.Second)
	scrolled.ScrollY = 480
	childMapped := v4InteractionFixture(61, 61*time.Second)
	childMapped.FrameScope = "child_frame"
	childMapped.Points = []protocol.BrowserInteractionPoint{{X: 20, Y: 30}}
	childMapped.FrameChain = []protocol.BrowserInteractionFrameHop{{
		BoundingX: 10, BoundingY: 20, BoundingWidth: 100, BoundingHeight: 100, OffsetWidth: 100, OffsetHeight: 100, Matrix: []float64{1, 0, 0, 1, 0, 0},
	}}
	unknownCohort := v4InteractionFixture(62, 62*time.Second)
	unknownCohort.SourceGeneration = strings.Repeat("f", 32)
	unknownCohort.InteractionID = unknownCohort.SourceGeneration + "-i62"
	unknownCohort.ActionID = unknownCohort.SourceGeneration + "-a62"
	unknownCohort.DocumentID = unknownCohort.SourceGeneration + "-doc-1"
	unknownCohort.Dispatch.DocumentID = unknownCohort.DocumentID
	unknownCohort.Dispatch.LifecycleID = unknownCohort.SourceGeneration + "-life-1"

	got := analyzeV3(retry1, interleaved, retry3, retryFollowup, rec1, rec2, rec3, unk1, unk2, unk3, multiDead, unexpectedNav, scrolled, childMapped, unknownCohort)
	if got.Version != browserInteractionAnalysisVersion4 {
		t.Fatalf("version = %q, want %q", got.Version, browserInteractionAnalysisVersion4)
	}
	if got.Coverage.MappedViaTransform != 1 || got.Coverage.PatternExcluded["engine_profile_unknown"] != 1 || got.Coverage.Excluded["engine_profile_unknown"] != 1 {
		t.Fatalf("coverage = %+v", got.Coverage)
	}

	retries := e4Patterns(got, interactionPatternRetry)
	if len(retries) != 1 || retries[0].Count != 2 || retries[0].AuthoredStep.StepID != stepRetry.StepID {
		t.Fatalf("retries = %+v", retries)
	}
	repeated := e4Patterns(got, interactionPatternRepeatedGesture)
	if len(repeated) != 1 || repeated[0].Provenance != protocol.InteractionProvenanceRecorded || !repeated[0].Start.Equal(rec1.Dispatch.At) || !repeated[0].End.Equal(rec3.Dispatch.At) {
		t.Fatalf("repeated_gesture = %+v", repeated)
	}
	scripted := e4Patterns(got, interactionPatternScriptedRepetition)
	if len(scripted) != 1 || scripted[0].Provenance != protocol.InteractionProvenanceUnknown || scripted[0].AuthoredStep.StepID != stepB.StepID {
		t.Fatalf("scripted_repetition = %+v", scripted)
	}
	dead := e4Patterns(got, interactionPatternDeadInteraction)
	if len(dead) != 2 || dead[0].ID == dead[1].ID || dead[0].OutcomeKind != protocol.InteractionOutcomeKindDialog || dead[0].OutcomeIndex != 0 || dead[1].OutcomeKind != protocol.InteractionOutcomeKindAssertion || dead[1].OutcomeIndex != 1 {
		t.Fatalf("dead_interaction = %+v", dead)
	}
	unexp := e4Patterns(got, interactionPatternUnexpectedNavigation)
	if len(unexp) != 1 || unexp[0].OutcomeKind != protocol.InteractionOutcomeKindAssertion || unexp[0].OutcomeIndex != 1 || unexp[0].Declared != "visible" {
		t.Fatalf("unexpected_navigation = %+v", unexp)
	}

	// Verify HTML, PDF, and CSV report formats surface browser_interaction_patterns.v4, mapped_via_transform, and outcome metadata.
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	report.BrowserIntelligence.Interactions = &got
	rendered := renderBrowserIntelligenceSections(t, report)[reportSectionInteractionMap]
	for index, format := range []string{"HTML", "PDF", "CSV"} {
		for _, want := range []string{
			"browser_interaction_patterns.v4",
			"Pattern-eligible gestures: 14.",
			"Mapped via child-frame transform: 1.",
			"Excluded from patterns: 1 engine_profile_unknown.",
			"outcome dialog[0]",
			"outcome assertion[1]",
			stepMultiDead.StepID,
		} {
			if !strings.Contains(rendered[index], want) {
				t.Fatalf("%s lacks %q:\n%s", format, want, rendered[index])
			}
		}
	}
}

func TestBrowserInteractionAnalysisSevenBoundsAndCSVReportParity(t *testing.T) {
	origRetained := maxRetainedInteractions
	origPatterns := maxInteractionPatterns
	origMembers := maxInteractionPatternMembers
	origHeatmaps := maxInteractionHeatmaps
	origTargets := maxHeatmapTargets
	origRetainedTargets := maxRetainedHeatmapTargets
	origCohorts := maxInteractionGenerationCohorts
	t.Cleanup(func() {
		maxRetainedInteractions = origRetained
		maxInteractionPatterns = origPatterns
		maxInteractionPatternMembers = origMembers
		maxInteractionHeatmaps = origHeatmaps
		maxHeatmapTargets = origTargets
		maxRetainedHeatmapTargets = origRetainedTargets
		maxInteractionGenerationCohorts = origCohorts
	})

	// 1. Bound 1 (maxRetainedInteractions) & Bound 4 (maxInteractionPatternMembers):
	// StreamedInteractions and heatmaps continue past maxRetainedInteractions, while PatternRetained and PatternTruncated
	// account the retained pattern sample separately. A repeat run exceeding maxInteractionPatternMembers sets MembersTruncated.
	maxRetainedInteractions = 6
	maxInteractionPatternMembers = 3
	g1 := v4InteractionFixture(1, 0)
	g2 := v4InteractionFixture(2, 100*time.Millisecond)
	g3 := v4InteractionFixture(3, 200*time.Millisecond)
	g4 := v4InteractionFixture(4, 300*time.Millisecond)
	g5 := v4InteractionFixture(5, 400*time.Millisecond)
	g6 := v4InteractionFixture(6, 5*time.Second)
	g6.ScrollY = 100
	g7 := v4InteractionFixture(7, 6*time.Second)
	g7.ScrollY = 200
	g8 := v4InteractionFixture(8, 7*time.Second)
	g8.ScrollY = 300
	gotRetained := analyzeV3(g1, g2, g3, g4, g5, g6, g7, g8)
	if gotRetained.Coverage.Interactions != 8 || gotRetained.Coverage.StreamedInteractions != 8 || gotRetained.Coverage.PatternRetained != 6 || gotRetained.Coverage.PatternTruncated != 2 || gotRetained.Coverage.Mapped != 8 {
		t.Fatalf("retained coverage = %+v", gotRetained.Coverage)
	}
	if len(gotRetained.Heatmaps) != 4 {
		t.Fatalf("heatmaps after retained cap = %d, want 4", len(gotRetained.Heatmaps))
	}
	if len(gotRetained.Patterns) != 1 || gotRetained.Patterns[0].Count != 5 || len(gotRetained.Patterns[0].Interactions) != 3 || !gotRetained.Patterns[0].MembersTruncated {
		t.Fatalf("member-truncated pattern = %+v", gotRetained.Patterns)
	}

	// 2. Bound 3 (maxInteractionPatterns): High-signal patterns (dead_interaction, unexpected_navigation, retry, repeated_gesture)
	// are prioritized ahead of scripted_repetition when truncating to maxInteractionPatterns, while preserving chronological order.
	maxRetainedInteractions = origRetained
	maxInteractionPatternMembers = origMembers
	maxInteractionPatterns = 2
	sr1 := v4InteractionFixture(10, 0)
	sr2 := v4InteractionFixture(11, 100*time.Millisecond)
	sr3 := v4InteractionFixture(12, 200*time.Millisecond)
	sr4 := v4InteractionFixture(13, 5*time.Second)
	sr5 := v4InteractionFixture(14, 5100*time.Millisecond)
	sr6 := v4InteractionFixture(15, 5200*time.Millisecond)
	deadLate := v4InteractionFixture(16, 10*time.Second)
	dStart := deadLate.Dispatch.At
	deadLate.CapturedAt = dStart.Add(2 * time.Second)
	deadLate.Outcomes = []protocol.BrowserInteractionOutcome{{
		Kind: protocol.InteractionOutcomeKindDialog, Declared: "dialog", DeadlineMS: 1000, Observed: protocol.InteractionOutcomeMissed, Reason: "deadline_missed",
		Clock: &protocol.BrowserOutcomeClock{Version: 1, StartedAt: dStart, DeadlineAt: dStart.Add(time.Second), ObservedAt: dStart.Add(time.Second), BudgetMS: 1000, ElapsedMS: 1000},
	}}
	gotPrioritized := analyzeV3(sr1, sr2, sr3, sr4, sr5, sr6, deadLate)
	if len(gotPrioritized.Patterns) != 2 || gotPrioritized.Patterns[0].Kind != interactionPatternScriptedRepetition || gotPrioritized.Patterns[1].Kind != interactionPatternDeadInteraction {
		t.Fatalf("prioritized patterns = %+v", gotPrioritized.Patterns)
	}
	if !strings.Contains(strings.Join(gotPrioritized.Limitations, " "), "patterns_truncated: kept 2 of 3") {
		t.Fatalf("limitations missing patterns_truncated: %+v", gotPrioritized.Limitations)
	}

	// 3. Bound 5 (maxInteractionHeatmaps) & Bound 6 (maxHeatmapTargets + maxRetainedHeatmapTargets):
	// Keyboard maps count target gestures in report views and enforce both target bounds.
	maxInteractionPatterns = origPatterns
	maxInteractionHeatmaps = 2
	maxHeatmapTargets = 2
	maxRetainedHeatmapTargets = 3
	var kbdRows []protocol.BrowserInteraction
	for idx, role := range []string{"textbox", "combobox", "searchbox", "spinbutton"} {
		k := v4InteractionFixture(20+idx, time.Duration(idx)*time.Second)
		k.Kind = "fill"
		k.Points = nil
		k.PointerType = ""
		k.Target = &protocol.BrowserInteractionTarget{Role: role, LabelSHA256: strings.Repeat(fmt.Sprintf("%x", idx+1), 64)}
		kbdRows = append(kbdRows, k)
	}
	// Duplicate "textbox" so it sorts first with Count=2.
	dupTextbox := kbdRows[0]
	dupTextbox.InteractionID = e4Generation + "-i29"
	dupTextbox.ActionID = e4Generation + "-a29"
	dupTextbox.Execution = &protocol.BrowserActionExecution{Invocation: 29, Attempt: 1}
	kbdRows = append(kbdRows, dupTextbox)
	gotKbd := analyzeV3(kbdRows...)
	if len(gotKbd.Heatmaps) != 1 || len(gotKbd.Heatmaps[0].Targets) != 2 || gotKbd.Heatmaps[0].Targets[0].Role != "textbox" || gotKbd.Heatmaps[0].Targets[0].Count != 2 || gotKbd.Coverage.Excluded["keyboard_targets_truncated"] != 1 {
		t.Fatalf("keyboard targets heatmap = %+v, coverage = %+v", gotKbd.Heatmaps, gotKbd.Coverage)
	}

	// 4. Bound 2 (maxInteractionGenerationCohorts) & Bound 7 (maxInteractionDocumentJoins):
	cohorts := browserGenerationCohorts{byGeneration: map[string]browserGenerationCohort{}, bound: 1, joinBound: 1}
	genA := strings.Repeat("1", 32)
	genB := strings.Repeat("2", 32)
	cohorts.add(&protocol.BrowserDocumentEvidence{SourceGeneration: genA, Observations: []protocol.BrowserDocumentObservation{
		{SourceGeneration: genA, DocumentID: genA + "-d1", LifecycleID: genA + "-l1", Engine: "chromium", DeviceProfile: "desktop"},
		{SourceGeneration: genA, DocumentID: genA + "-d1", LifecycleID: genA + "-l2", Engine: "chromium", DeviceProfile: "desktop"},
	}})
	cohorts.add(&protocol.BrowserDocumentEvidence{SourceGeneration: genB, Observations: []protocol.BrowserDocumentObservation{
		{SourceGeneration: genB, DocumentID: genB + "-d1", LifecycleID: genB + "-l1", Engine: "webkit", DeviceProfile: "mobile"},
	}})
	if !cohorts.truncated || !cohorts.joinsTruncated || len(cohorts.byGeneration) != 1 || len(cohorts.joins) != 1 {
		t.Fatalf("cohorts/joins bounds not enforced: truncated=%v joinsTruncated=%v lenCohorts=%d lenJoins=%d", cohorts.truncated, cohorts.joinsTruncated, len(cohorts.byGeneration), len(cohorts.joins))
	}

	// 5. Report parity (HTML/PDF/CSV) for keyboard targets, member truncation, and pattern-sample truncation.
	maxInteractionHeatmaps = origHeatmaps
	maxHeatmapTargets = origTargets
	maxRetainedHeatmapTargets = origRetainedTargets
	gotRetained.Heatmaps = append(gotRetained.Heatmaps, gotKbd.Heatmaps[0])
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	report.BrowserIntelligence.Interactions = &gotRetained
	mapView := (&runArchiveHTMLReportData{Report: report}).InteractionMapView()
	kbdRow := mapView.Heatmaps[len(mapView.Heatmaps)-1]
	if kbdRow.Gestures != "3" {
		t.Fatalf("keyboard heatmap Gestures = %q, want %q", kbdRow.Gestures, "3")
	}
	rendered := renderBrowserIntelligenceSections(t, report)[reportSectionInteractionMap]
	for index, format := range []string{"HTML", "PDF", "CSV"} {
		for _, want := range []string{
			"Pattern sample retained 6 of 8 streamed interactions (2 beyond pattern sample).",
			"3 of 5 interaction IDs listed",
			"textbox (111111111111): 2",
			"1 keyboard gestures excluded after the first 3 distinct targets",
			"kept the 2 most used of 3 retained keyboard targets",
		} {
			if !strings.Contains(rendered[index], want) {
				t.Fatalf("%s lacks %q:\n%s", format, want, rendered[index])
			}
		}
	}

	// 6. Endpoint ?format=csv export and formula-injection escaping.
	server, store := baselineTestServer(t)
	record := e1Archive(t, server, store, "e4-csv-export", nil, e1Document{id: 1, metric: "lcp", engine: "chromium", device: "mobile", value: 1000, qualified: true})
	evil := v4InteractionFixture(1, 0)
	evil.SourceGeneration = d2Generation
	evil.InteractionID = d2Generation + "-i1"
	evil.ActionID = d2Generation + "-a1"
	evil.DocumentID = d2Generation + "-document-1"
	evil.Dispatch.DocumentID = evil.DocumentID
	evil.Dispatch.LifecycleID = d2Generation + "-lifecycle-1"
	evil.ActionName = "=cmd|' /C calc'!A0"
	evilStart := evil.Dispatch.At
	evil.CapturedAt = evilStart.Add(2 * time.Second)
	evil.Outcomes = []protocol.BrowserInteractionOutcome{{
		Kind: protocol.InteractionOutcomeKindDialog, Declared: "dialog", DeadlineMS: 1000, Observed: protocol.InteractionOutcomeMissed, Reason: "deadline_missed",
		Clock: &protocol.BrowserOutcomeClock{Version: 1, StartedAt: evilStart, DeadlineAt: evilStart.Add(time.Second), ObservedAt: evilStart.Add(time.Second), BudgetMS: 1000, ElapsedMS: 1000},
	}}
	line, err := json.Marshal(&protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion4, SourceGeneration: d2Generation, Interactions: []protocol.BrowserInteraction{evil}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.ArchivePath, browserInteractionsFileName), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	rawManifest, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.BrowserInteractionsFile, manifest.BrowserInteractionRecordCount = browserInteractionsFileName, 1
	rawManifest, _ = json.Marshal(manifest)
	if err := os.WriteFile(record.ManifestPath, rawManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(rawManifest)

	csvRec := httptest.NewRecorder()
	csvReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/e4-csv-export/archive/browser-interactions?format=csv", nil)
	csvReq.SetPathValue("id", "e4-csv-export")
	server.handleGetRunArchiveBrowserInteractions(csvRec, csvReq)
	if csvRec.Code != http.StatusOK || !strings.HasPrefix(csvRec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("csv export = %d %s %s", csvRec.Code, csvRec.Header().Get("Content-Type"), csvRec.Body.String())
	}
	csvBody := csvRec.Body.String()
	for _, want := range []string{
		"section,key,field,value",
		"coverage,,version,browser_interaction_patterns.v4",
		"coverage,,streamed_interactions,1",
		"coverage,,pattern_retained,1",
		"action_name,'=cmd|' /C calc'!A0",
	} {
		if !strings.Contains(csvBody, want) {
			t.Fatalf("csv body lacks %q:\n%s", want, csvBody)
		}
	}

	badRec := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/e4-csv-export/archive/browser-interactions?format=xml", nil)
	badReq.SetPathValue("id", "e4-csv-export")
	server.handleGetRunArchiveBrowserInteractions(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported format = %d", badRec.Code)
	}
}
