package api

import (
	"context"
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
	"github.com/jasonczech/apexload/pkg/storage"
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
	navigated.Outcome = protocol.BrowserInteractionOutcome{Declared: "element_visible", DeadlineMS: 5000, Observed: protocol.InteractionOutcomeMissed}
	loaded := navigated
	loaded.InteractionID, loaded.Outcome.Declared = e4Generation+"-interaction-8", "load"
	// A recorded step that navigates and waits for the next page's element
	// (met), or for a stable DOM, is an ordinary navigation.
	recorded := navigated
	recorded.InteractionID, recorded.Outcome.Observed = e4Generation+"-interaction-9", protocol.InteractionOutcomeMet
	settled := navigated
	settled.InteractionID, settled.Outcome = e4Generation+"-interaction-10", protocol.BrowserInteractionOutcome{Declared: "dom_stable", DeadlineMS: 5000, Observed: protocol.InteractionOutcomeMissed}
	analysis = analyzeE4(missed, notReached, quiet, navigated, loaded, recorded, settled)
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
	if excluded["outside_viewport"] != 1 || excluded["no_viewport"] != 1 || excluded[protocol.InteractionUnavailableChildFrame] != 1 || analysis.Coverage.Mapped != 1 || excluded["engine_profile_unknown"] != 1 || analysis.Coverage.Interactions != 5 {
		t.Fatalf("coverage = %+v", analysis.Coverage)
	}
	var keyboard *BrowserInteractionHeatmap
	for index := range analysis.Heatmaps {
		switch {
		case len(analysis.Heatmaps[index].Targets) > 0:
			keyboard = &analysis.Heatmaps[index]
		}
	}
	if keyboard == nil || keyboard.Points != 0 || len(keyboard.Cells) != 0 || keyboard.Targets[0].Role != "textbox" || keyboard.Targets[0].Count != 1 {
		t.Fatalf("keyboard targets = %+v", keyboard)
	}
	if len(analysis.Heatmaps) != 1 {
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
	// A run without an archive is not found, as on the sibling archive
	// routes; one whose archive is not yet finalized has no analysis yet.
	if err := store.SaveTest(&storage.TestRecord{ID: "e4-no-archive", ProjectID: storage.DefaultProjectID, Name: "no archive", Status: "COMPLETE", StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tests/e4-no-archive/archive/browser-interactions", nil)
	missingRequest.SetPathValue("id", "e4-no-archive")
	server.handleGetRunArchiveBrowserInteractions(missing, missingRequest)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "archive not found") {
		t.Fatalf("missing archive = %d %s", missing.Code, missing.Body.String())
	}
	if status, _ := get(); status != http.StatusConflict {
		t.Fatalf("unfinalized archive = %d", status)
	}
	finalized, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(finalized)
	if status, analysis := get(); status != http.StatusOK || analysis.Status != "not_captured" || analysis.Version != browserInteractionAnalysisVersion {
		t.Fatalf("no evidence = %d %+v", status, analysis)
	}
	// A cancelled request is unavailable, never a panic or a server error.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := httptest.NewRecorder()
	cancelledRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+current.ID+"/archive/browser-interactions", nil).WithContext(cancelledCtx)
	cancelledRequest.SetPathValue("id", current.ID)
	server.handleGetRunArchiveBrowserInteractions(cancelled, cancelledRequest)
	if cancelled.Code != http.StatusServiceUnavailable {
		t.Fatalf("cancelled request = %d %s", cancelled.Code, cancelled.Body.String())
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
	record.ManifestSHA256 = sha256BytesHex(raw)
	status, analysis := get()
	if status != http.StatusOK || analysis.Status != "ready" || analysis.Coverage.Interactions != 3 || analysis.Coverage.Dropped != 2 || len(e4Patterns(analysis, interactionPatternScriptedRepetition)) != 1 || len(analysis.Heatmaps) != 0 || analysis.Coverage.Excluded["engine_profile_unknown"] != 3 {
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

// e4Captured is a tap as capture records it: dispatched synthetically, so
// without points, on an authored action.
func e4Captured(n int, at time.Duration, name string) protocol.BrowserInteraction {
	tap := e4Tap(n, at, 0, 0)
	tap.Points, tap.UnavailableReason, tap.ActionName = nil, protocol.InteractionUnavailableSyntheticDispatch, name
	return tap
}

func TestCapturedTapsRepeatOnOneActionWithoutPoints(t *testing.T) {
	analysis := analyzeE4(e4Captured(1, 0, "Add"), e4Captured(2, 300*time.Millisecond, "Add"), e4Captured(3, 700*time.Millisecond, "Add"), e4Captured(4, 800*time.Millisecond, "Remove"))
	patterns := e4Patterns(analysis, interactionPatternScriptedRepetition)
	if len(patterns) != 1 || patterns[0].Count != 3 || !strings.Contains(patterns[0].Detail, "one action name") {
		t.Fatalf("captured taps = %+v", analysis.Patterns)
	}
	// Synthetic taps are counted as not mapped, never as points.
	if analysis.Coverage.Excluded[protocol.InteractionUnavailableSyntheticDispatch] != 4 || len(analysis.Heatmaps) != 0 {
		t.Fatalf("coverage = %+v", analysis.Coverage)
	}
}

func TestEngineRetriesAreOneRetryNotRepetition(t *testing.T) {
	// Each engine retry is a new execution with its own action ID.
	click := func(n int, at time.Duration, success bool) protocol.BrowserInteraction {
		c := e4Tap(n, at, 100, 100)
		c.Kind, c.PointerType, c.ActionName, c.Success = "click", "mouse", "Pay", success
		return c
	}
	analysis := analyzeE4(click(1, 0, false), click(2, 500*time.Millisecond, false), click(3, time.Second, true))
	if retries := e4Patterns(analysis, interactionPatternRetry); len(retries) != 1 || retries[0].Count != 3 {
		t.Fatalf("retries = %+v", analysis.Patterns)
	}
	if len(e4Patterns(analysis, interactionPatternScriptedRepetition)) != 0 {
		t.Fatalf("retries counted as repetition: %+v", analysis.Patterns)
	}
	// Three successful executions of one step are a repetition, not a retry.
	looped := analyzeE4(click(1, 0, true), click(2, 500*time.Millisecond, true), click(3, time.Second, true))
	if len(e4Patterns(looped, interactionPatternRetry)) != 0 || len(e4Patterns(looped, interactionPatternScriptedRepetition)) != 1 {
		t.Fatalf("looped clicks = %+v", looped.Patterns)
	}
}

func TestDeadInteractionsNeedAPageResponseOutcome(t *testing.T) {
	missed := func(n int, declared string) protocol.BrowserInteraction {
		tap := e4Tap(n, time.Duration(n)*3*time.Second, 10, 10)
		tap.Outcome = protocol.BrowserInteractionOutcome{Declared: declared, DeadlineMS: 2000, Observed: protocol.InteractionOutcomeMissed}
		return tap
	}
	analysis := analyzeE4(missed(1, "dom_stable"), missed(2, "network_idle"), missed(3, "duration"), missed(4, "load"), missed(5, "element_visible"))
	dead := e4Patterns(analysis, interactionPatternDeadInteraction)
	if len(dead) != 2 || dead[0].Declared != "load" || dead[1].Declared != "element_visible" {
		t.Fatalf("dead interactions = %+v", analysis.Patterns)
	}
	// A gesture that navigated away and missed its in-page outcome is an
	// unexpected navigation, never a dead interaction.
	navigated := missed(6, "element_visible")
	navigated.Points, navigated.UnavailableReason = nil, protocol.InteractionUnavailableDocumentChanged
	analysis = analyzeE4(navigated)
	if len(e4Patterns(analysis, interactionPatternDeadInteraction)) != 0 || len(e4Patterns(analysis, interactionPatternUnexpectedNavigation)) != 1 {
		t.Fatalf("navigated gesture = %+v", analysis.Patterns)
	}
}

func TestRepetitionCountsClicksAndTapsOnly(t *testing.T) {
	gesture := func(n int, at time.Duration, kind string) protocol.BrowserInteraction {
		g := e4Tap(n, at, 100, 100)
		g.Kind = kind
		return g
	}
	if got := analyzeE4(gesture(1, 0, "hover"), gesture(2, 300*time.Millisecond, "hover"), gesture(3, 600*time.Millisecond, "click")); len(got.Patterns) != 0 {
		t.Fatalf("hovers counted: %+v", got.Patterns)
	}
	if got := analyzeE4(gesture(1, 0, "dblclick"), gesture(2, 300*time.Millisecond, "dblclick"), gesture(3, 600*time.Millisecond, "dblclick")); len(e4Patterns(got, interactionPatternScriptedRepetition)) != 1 {
		t.Fatalf("double clicks = %+v", got.Patterns)
	}
}

func TestHeatmapsHoldOnlyMappedGesturesAndStateTheirSpace(t *testing.T) {
	outside := e4Tap(1, 0, 500, 10)
	if got := analyzeE4(outside); len(got.Heatmaps) != 0 || got.Coverage.Excluded["outside_viewport"] != 1 {
		t.Fatalf("an out-of-viewport gesture made a map: %+v", got.Heatmaps)
	}
	scrolled := e4Tap(2, 5*time.Second, 10, 10)
	scrolled.ScrollY = 1200
	sharper := e4Tap(3, 10*time.Second, 10, 10)
	sharper.DeviceScaleFactor = 3
	got := analyzeE4(e4Tap(1, 0, 10, 10), scrolled, sharper)
	if len(got.Heatmaps) != 3 {
		t.Fatalf("device pixel ratios or scroll offsets share a map: %+v", got.Heatmaps)
	}
	for _, m := range got.Heatmaps {
		if m.CoordinateSpace != "viewport_css_px" || m.Scroll == nil {
			t.Fatalf("map space = %+v", m)
		}
		if m.Scroll.MinY != m.Scroll.MaxY || m.Scroll.MinX != m.Scroll.MaxX {
			t.Fatalf("scroll range = %+v", m.Scroll)
		}
	}
	// Gestures of an unknown engine and profile are disclosed.
	unknown := e4Tap(4, 15*time.Second, 10, 10)
	unknown.SourceGeneration = "00000000000000000000000000000000"
	if got := analyzeE4(unknown); len(got.Limitations) != 1 || !strings.HasPrefix(got.Limitations[0], "engine_profile_unknown") {
		t.Fatalf("limitations = %q", got.Limitations)
	}
}

// Every document record names its generation's engine and profile, also
// past the bounded measurement population documents are summarized from; past
// the generation bound a new generation is left out, and its gestures are excluded
// as unknown with the reason the handler gives, not the default no-evidence one.
func TestBrowserGenerationCohortsReadEveryRecord(t *testing.T) {
	const late = "11111111111111111111111111111111"
	early := &protocol.BrowserDocumentEvidence{SourceGeneration: e4Generation}
	for i := 0; i <= maxArchiveBrowserDocumentMeasurements; i++ {
		early.Observations = append(early.Observations, protocol.BrowserDocumentObservation{SourceGeneration: e4Generation, MeasurementID: fmt.Sprintf("m-%d", i), Engine: "chromium", DeviceProfile: "desktop"})
	}
	later := &protocol.BrowserDocumentEvidence{SourceGeneration: late, Observations: []protocol.BrowserDocumentObservation{{SourceGeneration: late, MeasurementID: "m-0", Engine: "webkit", DeviceProfile: "iphone_15_pro"}}}
	accumulator := &archiveBrowserDocumentAccumulator{}
	cohorts := browserGenerationCohorts{byGeneration: map[string]browserGenerationCohort{}, bound: maxInteractionGenerationCohorts}
	for _, evidence := range []*protocol.BrowserDocumentEvidence{early, later} {
		accumulator.add(evidence)
		cohorts.add(evidence)
	}
	if _, kept := accumulator.measurements[late+"\x00m-0"]; kept {
		t.Fatal("fixture: the measurement population kept the late generation")
	}
	if got := cohorts.byGeneration[late]; got != (browserGenerationCohort{engine: "webkit", profile: "iphone_15_pro"}) || cohorts.truncated {
		t.Fatalf("late generation = %+v truncated %t", got, cohorts.truncated)
	}
	bounded := browserGenerationCohorts{byGeneration: map[string]browserGenerationCohort{}, bound: 1}
	bounded.add(early)
	bounded.add(later)
	if _, ok := bounded.byGeneration[late]; ok || !bounded.truncated {
		t.Fatalf("bounded cohorts = %+v truncated %t", bounded.byGeneration, bounded.truncated)
	}
	analyzer := newInteractionAnalyzer("run", bounded.byGeneration)
	analyzer.unknownReason = "past the kept contexts"
	tap := e4Tap(1, 0, 10, 10)
	tap.SourceGeneration = late
	analyzer.add(&protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: late, Interactions: []protocol.BrowserInteraction{tap}})
	analysis := analyzer.finish()
	if len(analysis.Heatmaps) != 0 || analysis.Coverage.Excluded["engine_profile_unknown"] != 1 {
		t.Fatalf("unknown cohort was mapped: %+v", analysis)
	}
	if len(analysis.Limitations) != 1 || !strings.HasSuffix(analysis.Limitations[0], "(past the kept contexts)") {
		t.Fatalf("limitations = %q", analysis.Limitations)
	}
}

// Unknown contexts are excluded independently of the map bound; a known
// new cohort past the bound is counted as heatmaps_truncated.
func TestInteractionHeatmapBoundAndUnknownCohorts(t *testing.T) {
	var gestures []protocol.BrowserInteraction
	for n := 0; n < maxInteractionHeatmaps+1; n++ {
		tap := e4Tap(n+1, time.Duration(n)*5*time.Second, 10, 10)
		tap.ScrollY = float64(n)
		gestures = append(gestures, tap)
	}
	unknown := e4Tap(maxInteractionHeatmaps+2, time.Hour, 10, 10)
	unknown.SourceGeneration = "00000000000000000000000000000000"
	// An existing cohort remains admissible after the map limit.
	gestures = append(gestures, unknown, e4Tap(maxInteractionHeatmaps+3, 2*time.Hour, 10, 10))
	analysis := analyzeE4(gestures...)
	if len(analysis.Heatmaps) != maxInteractionHeatmaps || analysis.Coverage.Mapped != int64(maxInteractionHeatmaps+1) || analysis.Coverage.Excluded["heatmaps_truncated"] != 1 || analysis.Coverage.Excluded["engine_profile_unknown"] != 1 {
		t.Fatalf("heatmaps %d, coverage %+v", len(analysis.Heatmaps), analysis.Coverage)
	}
}

// A ready analysis without any pattern still encodes lists, never null.
func TestInteractionAnalysisEncodesEmptyLists(t *testing.T) {
	raw, err := json.Marshal(analyzeE4(e4Tap(1, 0, 10, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"patterns":[]`) || strings.Contains(string(raw), `"heatmaps":null`) {
		t.Fatalf("encoded analysis = %s", raw)
	}
}

// The endpoint joins engines and profiles from document evidence, reports
// patterns per document, and says when captured evidence never reached the
// archive.
func TestRunArchiveBrowserInteractionsJoinsDocumentsAndDisclosesLoss(t *testing.T) {
	server, store := baselineTestServer(t)
	record := e1Archive(t, server, store, "e4-documents", nil, e1Document{id: 1, metric: "lcp", engine: "webkit", device: "iphone_15_pro", value: 1000, qualified: true})
	retarget := func(i protocol.BrowserInteraction, document string) protocol.BrowserInteraction {
		i.SourceGeneration = d2Generation
		i.InteractionID = strings.ReplaceAll(i.InteractionID, e4Generation, d2Generation)
		i.ActionID = strings.ReplaceAll(i.ActionID, e4Generation, d2Generation)
		i.DocumentID = d2Generation + "-idoc-" + document
		return i
	}
	// Two taps on one document and one on another are no repetition.
	interactions := []protocol.BrowserInteraction{retarget(e4Tap(1, 0, 10, 10), "a"), retarget(e4Tap(2, 200*time.Millisecond, 10, 10), "a"), retarget(e4Tap(3, 400*time.Millisecond, 10, 10), "b")}
	evidence := &protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: d2Generation, Interactions: interactions}
	if evidence.Clone() == nil {
		t.Fatal("invalid fixture evidence")
	}
	line, _ := json.Marshal(evidence)
	if err := os.WriteFile(filepath.Join(record.ArchivePath, browserInteractionsFileName), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	rewrite := func(change func(*RunArchiveManifest)) {
		t.Helper()
		raw, _ := os.ReadFile(record.ManifestPath)
		var manifest RunArchiveManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		change(&manifest)
		raw, _ = json.Marshal(manifest)
		if err := os.WriteFile(record.ManifestPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		record.ManifestSHA256 = sha256BytesHex(raw)
	}
	rewrite(func(m *RunArchiveManifest) {
		m.BrowserInteractionsFile, m.BrowserInteractionRecordCount, m.BrowserInteractionsDropped, m.BrowserInteractionsIncomplete = browserInteractionsFileName, 1, 4, true
	})
	analysis, err := server.runArchiveBrowserInteractions(context.Background(), "e4-documents")
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Patterns) != 0 || len(analysis.Heatmaps) != 1 || analysis.Heatmaps[0].Engine != "webkit" || analysis.Heatmaps[0].DeviceProfile != "iphone_15_pro" {
		t.Fatalf("joined analysis = %+v", analysis)
	}
	// The archive's own account of losses (budget refusals, torn sidecars)
	// is counted.
	if analysis.Coverage.Dropped != 4 || !analysis.Coverage.Incomplete {
		t.Fatalf("coverage = %+v", analysis.Coverage)
	}
	// Past the context bound a gesture's context may have been left out or
	// may have kept no documents; the analysis says both and why.
	bound := maxInteractionGenerationCohorts
	maxInteractionGenerationCohorts = 0
	analysis, err = server.runArchiveBrowserInteractions(context.Background(), "e4-documents")
	maxInteractionGenerationCohorts = bound
	if err != nil || len(analysis.Heatmaps) != 0 || analysis.Coverage.Excluded["engine_profile_unknown"] != int64(len(interactions)) || len(analysis.Limitations) != 2 || !strings.HasPrefix(analysis.Limitations[0], "document_evidence_truncated") ||
		!strings.Contains(strings.Join(analysis.Limitations, " "), "kept no consistent engine/profile document evidence for these gestures, or their browser context is past the first 0") {
		t.Fatalf("truncated cohorts = %+v %v", analysis, err)
	}
	// A read that fails after valid records (here the record count) names
	// no engine from them.
	rewrite(func(m *RunArchiveManifest) { m.BrowserDocumentRecordCount++ })
	analysis, err = server.runArchiveBrowserInteractions(context.Background(), "e4-documents")
	if err != nil || len(analysis.Heatmaps) != 0 || analysis.Coverage.Excluded["engine_profile_unknown"] != int64(len(interactions)) || len(analysis.Limitations) == 0 || !strings.HasPrefix(analysis.Limitations[0], "document_evidence_unreadable") {
		t.Fatalf("partially read documents = %+v %v", analysis, err)
	}
	rewrite(func(m *RunArchiveManifest) { m.BrowserDocumentRecordCount-- })
	// Captured evidence that never reached the archive is unavailable, not
	// "not captured".
	rewrite(func(m *RunArchiveManifest) { m.BrowserInteractionsFile = "" })
	if analysis, err = server.runArchiveBrowserInteractions(context.Background(), "e4-documents"); err != nil || analysis.Status != "unavailable" || analysis.Coverage.Dropped != 4 {
		t.Fatalf("lost evidence = %+v %v", analysis, err)
	}
}

// Unreadable document evidence leaves engines and profiles unknown and says
// so; without interaction evidence the run is plainly not captured. An
// unreadable interaction sidecar is a server error.
func TestRunArchiveBrowserInteractionsUnreadableEvidence(t *testing.T) {
	server, store := baselineTestServer(t)
	record := e1Archive(t, server, store, "e4-unreadable", nil, e1Document{id: 1, metric: "lcp", engine: "webkit", device: "iphone_15_pro", value: 1000, qualified: true})
	rewrite := func(change func(*RunArchiveManifest)) {
		t.Helper()
		raw, _ := os.ReadFile(record.ManifestPath)
		var manifest RunArchiveManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		change(&manifest)
		raw, _ = json.Marshal(manifest)
		if err := os.WriteFile(record.ManifestPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		record.ManifestSHA256 = sha256BytesHex(raw)
	}
	if err := os.WriteFile(filepath.Join(record.ArchivePath, browserDocumentsFileName), []byte("not document evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rewrite(func(*RunArchiveManifest) {})
	if analysis, err := server.runArchiveBrowserInteractions(context.Background(), "e4-unreadable"); err != nil || analysis.Status != "not_captured" || len(analysis.Limitations) != 0 {
		t.Fatalf("no interactions with unreadable documents = %+v %v", analysis, err)
	}
	tap := e4Tap(1, 0, 10, 10)
	tap.SourceGeneration = d2Generation
	tap.InteractionID = strings.ReplaceAll(tap.InteractionID, e4Generation, d2Generation)
	tap.ActionID = strings.ReplaceAll(tap.ActionID, e4Generation, d2Generation)
	tap.DocumentID = d2Generation + "-idoc-a"
	line, _ := json.Marshal(&protocol.BrowserInteractionEvidence{Version: protocol.BrowserInteractionVersion, SourceGeneration: d2Generation, Interactions: []protocol.BrowserInteraction{tap}})
	interactions := filepath.Join(record.ArchivePath, browserInteractionsFileName)
	if err := os.WriteFile(interactions, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	rewrite(func(m *RunArchiveManifest) {
		m.BrowserInteractionsFile, m.BrowserInteractionRecordCount = browserInteractionsFileName, 1
	})
	analysis, err := server.runArchiveBrowserInteractions(context.Background(), "e4-unreadable")
	if err != nil || len(analysis.Limitations) == 0 || !strings.HasPrefix(analysis.Limitations[0], "document_evidence_unreadable") || len(analysis.Heatmaps) != 0 || analysis.Coverage.Excluded["engine_profile_unknown"] != 1 || !strings.Contains(strings.Join(analysis.Limitations, " "), "document evidence could not be read") {
		t.Fatalf("unreadable documents = %+v %v", analysis, err)
	}
	if err := os.WriteFile(interactions, []byte("not interaction evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/e4-unreadable/archive/browser-interactions", nil)
	request.SetPathValue("id", "e4-unreadable")
	server.handleGetRunArchiveBrowserInteractions(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unreadable interaction sidecar = %d %s", recorder.Code, recorder.Body.String())
	}
}

// Drags, swipes and long presses never repeat; only clicks and taps do.
func TestRepetitionLeavesOutNonClickGestures(t *testing.T) {
	for _, kind := range []string{"drag", "swipe", "long_press", "hover"} {
		gestures := make([]protocol.BrowserInteraction, 3)
		for n := range gestures {
			gestures[n] = e4Tap(n+1, time.Duration(n)*300*time.Millisecond, 100, 100)
			gestures[n].Kind = kind
		}
		if got := analyzeE4(gestures...); len(got.Patterns) != 0 {
			t.Fatalf("%s repeated: %+v", kind, got.Patterns)
		}
	}
}

func TestV4SameDocumentRouteAndMultiOutcomeNavigatedDeadInteractions(t *testing.T) {
	routeClick := v4InteractionFixture(1, 0)
	rStart := routeClick.Dispatch.At
	rNavObs := rStart.Add(20 * time.Millisecond)
	routeClick.CapturedAt = rStart.Add(2 * time.Second)
	routeClick.Outcomes = []protocol.BrowserInteractionOutcome{
		{
			Kind:               protocol.InteractionOutcomeKindNavigation,
			Declared:           "same_document_route",
			Observed:           protocol.InteractionOutcomeMet,
			ObservedAt:         &rNavObs,
			NavigationSequence: 1,
			AuthoredPageID:     routeClick.AuthoredPageID,
			RouteSeq:           1,
		},
		{
			Kind:       protocol.InteractionOutcomeKindSync,
			Declared:   "element_visible",
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "deadline_missed",
			DeadlineMS: 1000,
			Clock: &protocol.BrowserOutcomeClock{
				Version: 1, StartedAt: rStart, DeadlineAt: rStart.Add(time.Second), ObservedAt: rStart.Add(time.Second), BudgetMS: 1000, ElapsedMS: 1000,
			},
		},
	}
	if !routeClick.Valid() {
		t.Fatal("routeClick invalid")
	}
	routeGot := analyzeV3(routeClick)
	if len(routeGot.Patterns) != 1 || routeGot.Patterns[0].Kind != interactionPatternDeadInteraction {
		t.Fatalf("expected same_document_route missed element_visible to classify as dead_interaction, got %+v", routeGot.Patterns)
	}

	navDownloadClick := v4InteractionFixture(2, time.Second)
	nStart := navDownloadClick.Dispatch.At
	navDownloadClick.Points = nil
	navDownloadClick.UnavailableReason = protocol.InteractionUnavailableDocumentChanged
	navDownloadClick.CapturedAt = nStart.Add(2 * time.Second)
	navDownloadClick.Outcomes = []protocol.BrowserInteractionOutcome{
		{
			Kind:       protocol.InteractionOutcomeKindDownload,
			Declared:   "download",
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "deadline_missed",
			DeadlineMS: 1000,
			Clock: &protocol.BrowserOutcomeClock{
				Version: 1, StartedAt: nStart, DeadlineAt: nStart.Add(time.Second), ObservedAt: nStart.Add(time.Second), BudgetMS: 1000, ElapsedMS: 1000,
			},
		},
	}
	if !navDownloadClick.Valid() {
		t.Fatal("navDownloadClick invalid")
	}
	navGot := analyzeV3(navDownloadClick)
	if len(navGot.Patterns) != 1 || navGot.Patterns[0].Kind != interactionPatternDeadInteraction {
		t.Fatalf("expected missed download on document change to still emit dead_interaction, got %+v", navGot.Patterns)
	}
}
