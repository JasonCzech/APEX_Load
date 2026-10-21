package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func v3Interaction(n int, dispatch time.Duration) protocol.BrowserInteraction {
	i := e4Tap(n, 10*time.Second, 100, 100)
	i.Version = 2
	i.Kind = "click"
	i.ActionName = "same"
	i.AuthoredStep = protocol.NewBrowserAuthoredStep(strings.Repeat("a", 64), "actions[0]")
	i.Execution = &protocol.BrowserActionExecution{Invocation: uint64(n), Attempt: 1}
	i.Dispatch = &protocol.BrowserInteractionDispatch{At: e4Start.Add(dispatch), TimeOriginMS: float64(e4Start.UnixMilli()), OffsetMS: float64(dispatch / time.Millisecond), DocumentID: e4Generation + "-doc-1", LifecycleID: e4Generation + "-life-1"}
	i.DocumentID = i.Dispatch.DocumentID
	if !i.Valid() {
		panic("invalid v3 fixture")
	}
	return i
}
func analyzeV3(rows ...protocol.BrowserInteraction) BrowserInteractionAnalysis {
	a := newInteractionAnalyzer("run", map[string]browserGenerationCohort{e4Generation: {engine: "chromium", profile: "mobile"}})
	a.documentJoins = map[string]bool{}
	for _, i := range rows {
		if i.Dispatch != nil {
			a.documentJoins[interactionDocumentJoin(i.SourceGeneration, i.Dispatch.DocumentID, i.Dispatch.LifecycleID)] = true
		}
	}
	for _, i := range rows {
		a.add(&protocol.BrowserInteractionEvidence{Version: i.Version, SourceGeneration: i.SourceGeneration, Interactions: []protocol.BrowserInteraction{i}})
	}
	return a.finish()
}
func TestBrowserInteractionV3UsesDispatchAndAuthoredIdentity(t *testing.T) {
	a, b, c := v3Interaction(1, 0), v3Interaction(2, 500*time.Millisecond), v3Interaction(3, time.Second)
	a.CapturedAt = e4Start.Add(time.Minute)
	b.CapturedAt = e4Start.Add(time.Hour)
	got := analyzeV3(c, a, b)
	p := e4Patterns(got, interactionPatternScriptedRepetition)
	if got.Version != browserInteractionAnalysisVersion3 || len(p) != 1 || p[0].Count != 3 || !p[0].Start.Equal(a.Dispatch.At) || !p[0].End.Equal(c.Dispatch.At) || p[0].AuthoredStep.StepID != a.AuthoredStep.StepID {
		t.Fatalf("analysis %+v", got)
	}
	b.AuthoredStep = protocol.NewBrowserAuthoredStep(strings.Repeat("a", 64), "actions[1]")
	if len(analyzeV3(a, b, c).Patterns) != 0 {
		t.Fatal("duplicate names merged steps")
	}
	b.AuthoredStep = a.AuthoredStep.Clone()
	b.Dispatch.At = e4Start.Add(4 * time.Second)
	b.Dispatch.OffsetMS = 4000
	if len(analyzeV3(a, b, c).Patterns) != 0 {
		t.Fatal("post-action clock substituted")
	}
}
func TestBrowserInteractionV3IntendedRetryAndMissingAttempt(t *testing.T) {
	a, b, c := v3Interaction(1, 0), v3Interaction(2, time.Second), v3Interaction(3, 1500*time.Millisecond)
	a.Success = false
	b.Execution.Invocation = 1
	b.Execution.Attempt = 2
	got := analyzeV3(a, b, c)
	if len(e4Patterns(got, interactionPatternRetry)) != 1 || len(e4Patterns(got, interactionPatternScriptedRepetition)) != 0 {
		t.Fatalf("patterns %+v", got.Patterns)
	}
	d := v3Interaction(4, 1700*time.Millisecond)
	if len(e4Patterns(analyzeV3(b, c, d), interactionPatternScriptedRepetition)) != 0 {
		t.Fatal("missing first retry became repetition")
	}
	b.Execution.Invocation = 2
	b.Execution.Attempt = 1
	if len(e4Patterns(analyzeV3(a, b, c), interactionPatternRetry)) != 0 {
		t.Fatal("new invocation became retry")
	}
}
func TestBrowserInteractionV3MixedAndUnavailablePopulations(t *testing.T) {
	current := v3Interaction(1, 0)
	legacy := e4Tap(2, 0, 100, 100)
	unavailable := v3Interaction(3, time.Second)
	unavailable.Dispatch = nil
	unavailable.DispatchUnavailableReason = "not_observed"
	missing := v3Interaction(4, time.Second)
	missing.AuthoredStep = nil
	missing.Execution = nil
	got := analyzeV3(current, legacy, unavailable, missing)
	if got.Coverage.PatternEligible != 1 || got.Coverage.PatternExcluded["legacy_identity_unavailable"] != 1 || got.Coverage.PatternExcluded["dispatch_unavailable"] != 1 || got.Coverage.PatternExcluded["authored_step_unavailable"] != 1 || got.Coverage.Excluded["dispatch_unavailable"] != 1 {
		t.Fatalf("coverage %+v", got.Coverage)
	}
	if len(got.Heatmaps) != 2 || got.Heatmaps[0].Observation == got.Heatmaps[1].Observation {
		t.Fatalf("mixed map %+v", got.Heatmaps)
	}
	if got := analyzeV3(legacy); got.Version != browserInteractionAnalysisVersion || got.Coverage.PatternExcluded != nil {
		t.Fatalf("legacy changed %+v", got)
	}
}

func TestBrowserInteractionV3UnknownCohortCannotQualify(t *testing.T) {
	a := newInteractionAnalyzer("unknown", nil)
	i := v3Interaction(1, 0)
	a.add(&protocol.BrowserInteractionEvidence{Version: 2, SourceGeneration: i.SourceGeneration, Interactions: []protocol.BrowserInteraction{i}})
	got := a.finish()
	if got.Coverage.PatternEligible != 0 || got.Coverage.PatternExcluded["engine_profile_unknown"] != 1 || len(got.Heatmaps) != 0 {
		t.Fatalf("unknown cohort qualified %+v", got)
	}
}

func TestBrowserInteractionV3RequiresRetainedDocumentLifecycle(t *testing.T) {
	i := v3Interaction(1, 0)
	for _, life := range []string{"", e4Generation + "-another-lifecycle"} {
		a := newInteractionAnalyzer("run", map[string]browserGenerationCohort{e4Generation: {engine: "chromium"}})
		a.documentJoins = map[string]bool{interactionDocumentJoin(i.SourceGeneration, i.DocumentID, life): true}
		a.add(&protocol.BrowserInteractionEvidence{Version: 2, SourceGeneration: i.SourceGeneration, Interactions: []protocol.BrowserInteraction{i}})
		got := a.finish()
		if got.Coverage.PatternEligible != 0 || got.Coverage.PatternExcluded["document_lifecycle_unavailable"] != 1 {
			t.Fatalf("unretained lifecycle qualified %+v", got)
		}
	}
}
func TestBrowserInteractionV3DocumentJoinBoundCountsLifecycles(t *testing.T) {
	c := browserGenerationCohorts{byGeneration: map[string]browserGenerationCohort{}, bound: 10, joinBound: 2}
	row := protocol.BrowserDocumentObservation{SourceGeneration: e4Generation, DocumentID: e4Generation + "-d", LifecycleID: e4Generation + "-l1", Engine: "chromium"}
	for range 5 {
		c.add(&protocol.BrowserDocumentEvidence{Observations: []protocol.BrowserDocumentObservation{row}})
	}
	if len(c.joins) != 1 {
		t.Fatal("metric revisions counted as lifecycles")
	}
	row.LifecycleID = e4Generation + "-l2"
	c.add(&protocol.BrowserDocumentEvidence{Observations: []protocol.BrowserDocumentObservation{row}})
	if len(c.joins) != 2 || c.joinsTruncated {
		t.Fatal("exact bound rejected")
	}
	row.LifecycleID = e4Generation + "-l3"
	c.add(&protocol.BrowserDocumentEvidence{Observations: []protocol.BrowserDocumentObservation{row}})
	if len(c.joins) != 2 || !c.joinsTruncated {
		t.Fatal("join bound not enforced")
	}
}
