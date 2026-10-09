package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const e4Generation = "fedcba9876543210fedcba9876543210"

var e4Start = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// e4Tap is a scripted tap at (x, y) on one document, route and layout.
func e4Tap(n int, at time.Duration, x, y float64) protocol.BrowserInteraction {
	return protocol.BrowserInteraction{Version: protocol.BrowserInteractionVersion, SourceGeneration: e4Generation, InteractionID: fmt.Sprintf("%s-interaction-%d", e4Generation, n),
		ActionID: fmt.Sprintf("%s-action-%d", e4Generation, n), DocumentID: e4Generation + "-idoc-" + e4Generation, FrameScope: "main_frame", Kind: "tap", PointerType: "touch",
		Points: []protocol.BrowserInteractionPoint{{X: x, Y: y}}, ViewportWidth: 400, ViewportHeight: 800, DeviceScaleFactor: 2, LayoutRevision: "00000000000000aa", Route: "/cart",
		Outcome: protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared}, Provenance: protocol.InteractionProvenanceScripted, Success: true, CapturedAt: e4Start.Add(at)}
}

func analyzeE4(interactions ...protocol.BrowserInteraction) BrowserInteractionAnalysis {
	analyzer := newInteractionAnalyzer("run", map[string]browserGenerationCohort{e4Generation: {engine: "chromium", profile: "mobile"}})
	analyzer.add(&protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: e4Generation, Interactions: interactions})
	return analyzer.finish()
}

func e4Patterns(analysis BrowserInteractionAnalysis, kind string) []BrowserInteractionPattern {
	var out []BrowserInteractionPattern
	for _, p := range analysis.Patterns {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

func TestRepeatedScriptedTapsAreScriptedRepetition(t *testing.T) {
	analysis := analyzeE4(e4Tap(1, 0, 100, 100), e4Tap(2, 400*time.Millisecond, 110, 105), e4Tap(3, 900*time.Millisecond, 95, 120), e4Tap(4, 5*time.Second, 100, 100))
	patterns := e4Patterns(analysis, interactionPatternScriptedRepetition)
	if len(patterns) != 1 || patterns[0].Count != 3 || patterns[0].Provenance != protocol.InteractionProvenanceScripted || len(e4Patterns(analysis, interactionPatternRepeatedGesture)) != 0 {
		t.Fatalf("patterns = %+v", analysis.Patterns)
	}
	if analysis.Thresholds.RepeatMinGestures != 3 || analysis.Thresholds.RepeatWindowMs != 2000 || analysis.Thresholds.RepeatRadiusPx != 32 {
		t.Fatalf("thresholds = %+v", analysis.Thresholds)
	}
	// Only recorded (consented) gestures can be a repeated gesture.
	var recorded []protocol.BrowserInteraction
	for n, at := range []time.Duration{0, 300 * time.Millisecond, 600 * time.Millisecond} {
		tap := e4Tap(n+1, at, 100, 100)
		tap.Provenance = protocol.InteractionProvenanceRecorded
		recorded = append(recorded, tap)
	}
	if got := e4Patterns(analyzeE4(recorded...), interactionPatternRepeatedGesture); len(got) != 1 || got[0].Provenance != protocol.InteractionProvenanceRecorded {
		t.Fatalf("recorded repeat = %+v", got)
	}
	// One unknown gesture keeps the whole run from a frustration label.
	recorded[1].Provenance = protocol.InteractionProvenanceUnknown
	if got := analyzeE4(recorded...); len(e4Patterns(got, interactionPatternRepeatedGesture)) != 0 || len(e4Patterns(got, interactionPatternScriptedRepetition)) != 1 {
		t.Fatalf("mixed provenance = %+v", got.Patterns)
	}
	// Taps further apart than 32 px or 2 s are no repeat; a double click is
	// one declared gesture.
	far := analyzeE4(e4Tap(1, 0, 100, 100), e4Tap(2, 100*time.Millisecond, 160, 100), e4Tap(3, 200*time.Millisecond, 220, 100))
	slow := analyzeE4(e4Tap(1, 0, 100, 100), e4Tap(2, 1500*time.Millisecond, 100, 100), e4Tap(3, 3*time.Second, 100, 100))
	if len(far.Patterns) != 0 || len(slow.Patterns) != 0 {
		t.Fatalf("far %+v slow %+v", far.Patterns, slow.Patterns)
	}
}

func TestRetriesDeadInteractionsAndUnexpectedNavigation(t *testing.T) {
	failed := e4Tap(1, 0, 100, 100)
	failed.Success = false
	retry := e4Tap(2, 300*time.Millisecond, 100, 100)
	retry.ActionID = failed.ActionID
	third := e4Tap(3, 600*time.Millisecond, 100, 100)
	analysis := analyzeE4(failed, retry, third)
	if got := e4Patterns(analysis, interactionPatternRetry); len(got) != 1 || got[0].Count != 2 {
		t.Fatalf("retry = %+v", analysis.Patterns)
	}
	if len(e4Patterns(analysis, interactionPatternScriptedRepetition)) != 0 {
		t.Fatalf("retries counted as repetition: %+v", analysis.Patterns)
	}
	missed := e4Tap(4, time.Second, 10, 10)
	missed.Outcome = protocol.BrowserInteractionOutcome{Declared: "element_visible", DeadlineMS: 5000, Observed: protocol.InteractionOutcomeMissed}
	notReached := e4Tap(5, 2*time.Second, 10, 10)
	notReached.Outcome = protocol.BrowserInteractionOutcome{Declared: "element_visible", DeadlineMS: 5000, Observed: protocol.InteractionOutcomeNotReached}
	quiet := e4Tap(6, 3*time.Second, 10, 10)
	navigated := e4Tap(7, 4*time.Second, 10, 10)
	navigated.Points, navigated.UnavailableReason = nil, protocol.InteractionUnavailableDocumentChanged
	navigated.Outcome = protocol.BrowserInteractionOutcome{Declared: "dom_stable", DeadlineMS: 5000, Observed: protocol.InteractionOutcomeMet}
	loaded := navigated
	loaded.InteractionID, loaded.Outcome.Declared = e4Generation+"-interaction-8", "load"
	analysis = analyzeE4(missed, notReached, quiet, navigated, loaded)
	dead := e4Patterns(analysis, interactionPatternDeadInteraction)
	if len(dead) != 1 || dead[0].Declared != "element_visible" || dead[0].DeadlineMs != 5000 || dead[0].Interactions[0] != missed.InteractionID {
		t.Fatalf("dead = %+v", analysis.Patterns)
	}
	if nav := e4Patterns(analysis, interactionPatternUnexpectedNavigation); len(nav) != 1 || nav[0].Interactions[0] != navigated.InteractionID {
		t.Fatalf("unexpected navigation = %+v", analysis.Patterns)
	}
}

func TestHeatmapsKeepIncompatibleLayoutsApart(t *testing.T) {
	first := e4Tap(1, 0, 0, 0)
	second := e4Tap(2, 5*time.Second, 399, 799)
	otherLayout := e4Tap(3, 10*time.Second, 200, 400)
	otherLayout.LayoutRevision = "00000000000000bb"
	rotated := e4Tap(4, 15*time.Second, 200, 200)
	rotated.ViewportWidth, rotated.ViewportHeight = 800, 400
	analysis := analyzeE4(first, second, otherLayout, rotated)
	if len(analysis.Heatmaps) != 3 || analysis.Coverage.Mapped != 4 {
		t.Fatalf("heatmaps = %+v", analysis.Heatmaps)
	}
	var shared *BrowserInteractionHeatmap
	for index := range analysis.Heatmaps {
		if analysis.Heatmaps[index].Points == 2 {
			shared = &analysis.Heatmaps[index]
		}
	}
	if shared == nil || shared.Columns != 64 || shared.Rows != 64 || shared.Engine != "chromium" || shared.DeviceProfile != "mobile" || shared.Orientation != "portrait" || shared.Route != "/cart" {
		t.Fatalf("shared map = %+v", shared)
	}
	if len(shared.Cells) != 2 || shared.Cells[0] != [3]int{0, 0, 1} || shared.Cells[1] != [3]int{63, 63, 1} {
		t.Fatalf("cells = %+v", shared.Cells)
	}
	for _, m := range analysis.Heatmaps {
		if m.ViewportWidth == 800 && m.Orientation != "landscape" {
			t.Fatalf("rotated map = %+v", m)
		}
	}
}

func TestHeatmapsCountKeyboardTargetsAndExclusions(t *testing.T) {
	fill := e4Tap(1, 0, 0, 0)
	fill.Kind, fill.Points, fill.PointerType = "fill", nil, ""
	fill.Target = &protocol.BrowserInteractionTarget{Role: "textbox", LabelSHA256: strings.Repeat("a", 64)}
	outside := e4Tap(2, time.Second, 500, 10)
	noViewport := e4Tap(3, 2*time.Second, 10, 10)
	noViewport.ViewportWidth = 0
	framed := e4Tap(4, 3*time.Second, 10, 10)
	framed.Points, framed.UnavailableReason, framed.FrameScope = nil, protocol.InteractionUnavailableChildFrame, "child_frame"
	unknownCohort := e4Tap(5, 4*time.Second, 10, 10)
	unknownCohort.SourceGeneration = "00000000000000000000000000000000"
	analysis := analyzeE4(fill, outside, noViewport, framed, unknownCohort)
	excluded := analysis.Coverage.Excluded
	if excluded["outside_viewport"] != 1 || excluded["no_viewport"] != 1 || excluded[protocol.InteractionUnavailableChildFrame] != 1 || analysis.Coverage.Mapped != 2 || analysis.Coverage.Interactions != 5 {
		t.Fatalf("coverage = %+v", analysis.Coverage)
	}
	var keyboard, unknown *BrowserInteractionHeatmap
	for index := range analysis.Heatmaps {
		switch {
		case len(analysis.Heatmaps[index].Targets) > 0:
			keyboard = &analysis.Heatmaps[index]
		case analysis.Heatmaps[index].Engine == "unknown":
			unknown = &analysis.Heatmaps[index]
		}
	}
	if keyboard == nil || keyboard.Points != 0 || len(keyboard.Cells) != 0 || keyboard.Targets[0].Role != "textbox" || keyboard.Targets[0].Count != 1 {
		t.Fatalf("keyboard targets = %+v", keyboard)
	}
	if unknown == nil || unknown.DeviceProfile != "unknown" {
		t.Fatalf("a gesture of an unknown source generation = %+v", analysis.Heatmaps)
	}
}

func TestRunArchiveBrowserInteractionsEndpoint(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	record := server.runner.archives[current.ID]
	get := func() (int, BrowserInteractionAnalysis) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+current.ID+"/archive/browser-interactions", nil)
		request.SetPathValue("id", current.ID)
		server.handleGetRunArchiveBrowserInteractions(recorder, request)
		var analysis BrowserInteractionAnalysis
		_ = json.Unmarshal(recorder.Body.Bytes(), &analysis)
		return recorder.Code, analysis
	}
	if status, analysis := get(); status != http.StatusOK || analysis.Status != "not_captured" || analysis.Version != browserInteractionAnalysisVersion {
		t.Fatalf("no evidence = %d %+v", status, analysis)
	}
	evidence := &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: e4Generation,
		Interactions: []protocol.BrowserInteraction{e4Tap(1, 0, 100, 100), e4Tap(2, 300*time.Millisecond, 100, 100), e4Tap(3, 600*time.Millisecond, 100, 100)}, Dropped: 2}
	if evidence.Clone() == nil {
		t.Fatal("invalid fixture evidence")
	}
	line, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.ArchivePath, browserInteractionsFileName), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.BrowserInteractionsFile, manifest.BrowserInteractionRecordCount = browserInteractionsFileName, 1
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(record.ManifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	status, analysis := get()
	if status != http.StatusOK || analysis.Status != "ready" || analysis.Coverage.Interactions != 3 || analysis.Coverage.Dropped != 2 || len(e4Patterns(analysis, interactionPatternScriptedRepetition)) != 1 || len(analysis.Heatmaps) != 1 {
		t.Fatalf("analysis = %d %+v", status, analysis)
	}
	// An unknown run is not found.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/missing/archive/browser-interactions", nil)
	request.SetPathValue("id", "missing")
	server.handleGetRunArchiveBrowserInteractions(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing run = %d", recorder.Code)
	}
}
