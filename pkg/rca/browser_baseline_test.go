package rca

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

var browserBaselineStart = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// documentSeries is one browser document cohort's per-bucket p75 series,
// each bucket holding samples documents.
func documentSeries(id, metric, unit, family string, fields map[string]string, values []float64, samples int64) Series {
	s := Series{ID: id, Kind: SeriesBrowserDocument, Unit: unit, Observation: ObservationInterval, Bucket: time.Second,
		Evidence: EvidenceRef{Kind: "browser_document", Locator: "browser_documents.jsonl#" + id}, Document: &DocumentCohort{Metric: metric, Family: family, Fields: fields, Windows: map[time.Time]DocumentWindow{}}}
	for i, v := range values {
		point := SeriesPoint{At: browserBaselineStart.Add(time.Duration(i) * time.Second), Value: v, Valid: !math.IsNaN(v), Count: int32(samples)}
		if !point.Valid {
			point.Value, point.Count = 0, 0
		}
		s.Points = append(s.Points, point)
		s.Document.Windows[point.At.UTC()] = DocumentWindow{Start: point.At, End: point.At.Add(time.Millisecond)}
	}
	return s
}

// browserNoisy is n deterministic values around mean with a ± spread pattern.
func browserNoisy(n int, mean, spread float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + spread*float64((i%5)-2)
	}
	return out
}

func browserNear(a, b float64) bool { return math.Abs(a-b) < 1e-9*math.Max(1, math.Abs(b)) }

func browserBaselineInput(current, baseline []Series, qualification string) DetectionInput {
	level := LoadLevel{Start: browserBaselineStart, End: browserBaselineStart.Add(time.Minute), VUsers: 10}
	return DetectionInput{Levels: []LoadLevel{level}, Series: current,
		Baseline: &DetectionBaseline{TestID: "baseline", Qualification: qualification, Levels: []LoadLevel{level}, Series: baseline}}
}

var chromium141 = map[string]string{"browser_version": "141.0", "shaping_applied": "true"}

func TestBrowserBaselineFindsASeededRegression(t *testing.T) {
	baseline := documentSeries("browser_document/lcp/a/p75", "lcp", "ms", "lcp-family", chromium141, browserNoisy(40, 1000, 20), 3)
	current := documentSeries("browser_document/lcp/a/p75", "lcp", "ms", "lcp-family", chromium141, browserNoisy(20, 1400, 20), 3)
	result := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams)
	if result == nil || result.Status != DetectionComplete || len(result.Results) != 1 || result.Baseline.TestID != "baseline" {
		t.Fatalf("result = %+v", result)
	}
	got := result.Results[0]
	if got.State != BrowserBaselineAnomaly || got.Direction != DirectionIncrease || got.CurrentBuckets != 20 || got.CurrentSamples != 60 || got.BaselineBuckets != 40 || got.BaselineSamples != 120 {
		t.Fatalf("seeded regression = %+v", got)
	}
	if got.Current != 1400 || !browserNear(got.BaselineMean, 1000) || !browserNear(got.EffectFloor, 100) || got.ExpectedHigh <= got.BaselineMean || got.ExpectedLow >= got.BaselineMean || len(got.Evidence) != 2 {
		t.Fatalf("seeded regression evidence = %+v", got)
	}
	// The same cohort without the regression stays within range.
	quiet := documentSeries("browser_document/lcp/a/p75", "lcp", "ms", "lcp-family", chromium141, browserNoisy(20, 1010, 20), 3)
	if result := DetectBrowserBaseline(browserBaselineInput([]Series{quiet}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams); result.Results[0].State != BrowserBaselineWithinRange {
		t.Fatalf("noise = %+v", result.Results[0])
	}
}

func TestBrowserBaselineNeedsAPracticalEffect(t *testing.T) {
	// A very stable baseline makes a 30 ms change many sigmas, but below the
	// timing floor of max(50 ms, 10% of 1000 ms) it is no anomaly.
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 1), 1)
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1030, 0), 1)
	got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
	if got.State != BrowserBaselineWithinRange || math.Abs(got.Z) < 3 || !browserNear(got.EffectFloor, 100) {
		t.Fatalf("below the effect floor = %+v", got)
	}
	// Within range means inside the published range: it is as wide as the
	// effect floor when that exceeds three sigmas.
	if got.Current < got.ExpectedLow || got.Current > got.ExpectedHigh || !browserNear(got.ExpectedHigh, got.BaselineMean+100) {
		t.Fatalf("within range outside the expected range = %+v", got)
	}
	// Layout shift's floor is 0.02.
	baseline = documentSeries("cls", "cls", "score", "c", chromium141, browserNoisy(40, 0.05, 0.001), 1)
	current = documentSeries("cls", "cls", "score", "c", chromium141, browserNoisy(10, 0.06, 0), 1)
	got = DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
	if got.State != BrowserBaselineWithinRange || got.EffectFloor != 0.02 {
		t.Fatalf("CLS below its floor = %+v", got)
	}
	current = documentSeries("cls", "cls", "score", "c", chromium141, browserNoisy(10, 0.09, 0), 1)
	if got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams).Results[0]; got.State != BrowserBaselineAnomaly {
		t.Fatalf("CLS above its floor = %+v", got)
	}
}

// A run's pinned browser policy raises the effect floors and sets the
// baseline mode: none turns the comparison off, pinned_run compares only with
// its run, and a policy that could not be used leaves the defaults, said so.
func TestBrowserBaselineFollowsThePinnedBrowserPolicy(t *testing.T) {
	floor := func(v float64) *float64 { return &v }
	if params := BrowserBaselineParamsFor(&BrowserBaselinePolicy{TimingMs: floor(250), TimingRelative: floor(0.05), CLS: floor(0.1)}); params.TimingFloorMs != 250 || params.TimingFloorFraction != 0.10 || params.CLSFloor != 0.1 {
		t.Fatalf("policy floors only raise the defaults: %+v", params)
	}
	if params := BrowserBaselineParamsFor(&BrowserBaselinePolicy{TimingMs: floor(250), Unavailable: "missing"}); !reflect.DeepEqual(params, DefaultBrowserBaselineParams) {
		t.Fatalf("an unusable policy applied its floors: %+v", params)
	}
	// A 150 ms regression clears the default 100 ms floor but not a 250 ms one.
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 1), 1)
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1150, 0), 1)
	input := browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified)
	if got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results[0]; got.State != BrowserBaselineAnomaly {
		t.Fatalf("default floors = %+v", got)
	}
	input.BrowserPolicy = &BrowserBaselinePolicy{Ref: "web@2", TimingMs: floor(250), Mode: "latest_compatible"}
	result := DetectBrowserBaseline(input, BrowserBaselineParamsFor(input.BrowserPolicy))
	if result.Policy != "web@2" || result.Results[0].State != BrowserBaselineWithinRange || !browserNear(result.Results[0].EffectFloor, 250) {
		t.Fatalf("policy floor = %+v", result)
	}
	for _, tc := range []struct {
		policy BrowserBaselinePolicy
		status string
		code   string
	}{
		{BrowserBaselinePolicy{Ref: "web@2", Mode: "none"}, DetectionUnavailable, BrowserBaselinePolicyOff},
		{BrowserBaselinePolicy{Ref: "web@2", Mode: "pinned_run", RunID: "golden"}, DetectionUnavailable, BrowserBaselinePolicyPinnedRun},
		{BrowserBaselinePolicy{Ref: "web@2", Unavailable: "its revision was not found"}, DetectionPartial, BrowserBaselinePolicyUnavailable},
	} {
		input.BrowserPolicy = &tc.policy
		result := DetectBrowserBaseline(input, BrowserBaselineParamsFor(input.BrowserPolicy))
		if result.Status != tc.status || len(result.Limitations) == 0 || result.Limitations[0].Code != tc.code {
			t.Fatalf("%+v: %+v", tc.policy, result)
		}
		// A policy that could not be used is named in its limitation, never
		// as the policy whose floors applied.
		if (tc.policy.Unavailable != "") != (result.Policy == "") {
			t.Fatalf("%+v names policy %q", tc.policy, result.Policy)
		}
	}
	// pinned_run without a usable baseline is unavailable for that reason,
	// not because the analysis named another run.
	unqualified := input
	unqualified.Baseline = nil
	unqualified.BrowserPolicy = &BrowserBaselinePolicy{Ref: "web@2", Mode: "pinned_run", RunID: "golden"}
	if result := DetectBrowserBaseline(unqualified, BrowserBaselineParamsFor(unqualified.BrowserPolicy)); result.Status != DetectionUnavailable || result.Limitations[0].Code != BrowserBaselineNoBaselineRun {
		t.Fatalf("pinned run without a usable baseline = %+v", result)
	}
	// pinned_run compares when the analysis's baseline is that run.
	input.BrowserPolicy = &BrowserBaselinePolicy{Ref: "web@2", Mode: "pinned_run", RunID: "baseline"}
	if result := DetectBrowserBaseline(input, BrowserBaselineParamsFor(input.BrowserPolicy)); result.Status != DetectionComplete || len(result.Results) != 1 {
		t.Fatalf("pinned run is the baseline: %+v", result)
	}
}

func TestBrowserBaselineLeavesWhatItCannotJudgeUnscored(t *testing.T) {
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1500, 20), 1)
	tests := []struct {
		name     string
		baseline []Series
		reason   string
		detail   string
	}{
		{name: "short baseline", baseline: []Series{documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(29, 1000, 20), 1)}, reason: BrowserBaselineInsufficient, detail: "29 of the 30"},
		{name: "zero variance", baseline: []Series{documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 0), 1)}, reason: BrowserBaselineZeroVariance},
		{name: "shaping changed", baseline: []Series{documentSeries("lcp-other", "lcp", "ms", "f", map[string]string{"browser_version": "141.0", "shaping_applied": "false"}, browserNoisy(40, 1000, 20), 1)}, reason: BrowserBaselineCohortChanged, detail: "shaping_applied"},
		{name: "no baseline cohort", baseline: []Series{documentSeries("fcp", "fcp", "ms", "g", chromium141, browserNoisy(40, 1000, 20), 1)}, reason: BrowserBaselineNoBaselineCohort},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, test.baseline, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
			if got.State != BrowserBaselineUnscored || got.Reason != test.reason || !strings.Contains(got.Detail, test.detail) || got.Direction != "" {
				t.Fatalf("result = %+v", got)
			}
		})
	}
	// A baseline at another load has no same-load window.
	input := browserBaselineInput([]Series{current}, []Series{documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 1)}, BaselineQualified)
	input.Baseline.Levels[0].VUsers = 50
	if got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results[0]; got.Reason != BrowserBaselineNoSameLoadWindow {
		t.Fatalf("other load = %+v", got)
	}
}

func TestBrowserBaselineWithoutAQualifiedBaselineOrLevels(t *testing.T) {
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1500, 20), 1)
	for _, input := range []DetectionInput{
		{Levels: browserBaselineInput(nil, nil, "").Levels, Series: []Series{current}},
		browserBaselineInput([]Series{current}, nil, BaselineUnqualified),
	} {
		got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams)
		if got.Status != DetectionUnavailable || len(got.Results) != 0 || got.Limitations[0].Code != BrowserBaselineNoBaselineRun {
			t.Fatalf("no baseline = %+v", got)
		}
	}
	input := browserBaselineInput([]Series{current}, nil, BaselineQualified)
	input.Levels = nil
	if got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams); got.Status != DetectionUnavailable || got.Limitations[0].Code != BrowserBaselineLevelsUnavailable {
		t.Fatalf("no levels = %+v", got)
	}
	// A run without browser documents has no section.
	if got := DetectBrowserBaseline(DetectionInput{Levels: input.Levels}, DefaultBrowserBaselineParams); got != nil {
		t.Fatalf("no documents = %+v", got)
	}
}

func TestBrowserBaselineExcludesBucketsOutsideSteadyLoadAndCarriesSourceLoss(t *testing.T) {
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 1)
	baseline.Document.Incomplete = true
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(70, 1000, 20), 1)
	current.Document.Incomplete = true
	got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams)
	// The level spans the first minute: the last 10 buckets fall outside it.
	if got.Excluded[BrowserBaselineOutsideSteadyLoad] != 10 || got.Results[0].CurrentBuckets != 60 {
		t.Fatalf("outside steady load = %+v %+v", got.Excluded, got.Results[0])
	}
	if caveats := strings.Join(got.Results[0].Caveats, ","); caveats != BrowserBaselineSourceLoss+","+BrowserBaselineBaselineSourceLoss {
		t.Fatalf("caveats = %q", caveats)
	}
}

func TestBrowserBaselineKeepsAnomaliesFirstWithinItsBound(t *testing.T) {
	baseline := []Series{}
	current := []Series{}
	for _, id := range []string{"a", "b", "c"} {
		baseline = append(baseline, documentSeries(id, "lcp", "ms", id, chromium141, browserNoisy(40, 1000, 20), 1))
	}
	current = append(current,
		documentSeries("a", "lcp", "ms", "a", chromium141, browserNoisy(10, 1000, 20), 1),
		documentSeries("b", "lcp", "ms", "b", chromium141, browserNoisy(10, 2000, 20), 1),
		documentSeries("c", "lcp", "ms", "c", chromium141, browserNoisy(10, 1001, 20), 1))
	params := DefaultBrowserBaselineParams
	params.MaxResults = 2
	got := DetectBrowserBaseline(browserBaselineInput(current, baseline, BaselineQualified), params)
	if got.Status != DetectionPartial || len(got.Results) != 2 || got.Results[0].Series != "b" || got.Results[0].State != BrowserBaselineAnomaly || got.Limitations[0].Code != BrowserBaselineResultsTruncated {
		t.Fatalf("bounded = %+v", got)
	}
}

func TestAnalyzeAddsTheBrowserBaselineBesideDetection(t *testing.T) {
	baseline := documentSeries("browser_document/lcp/a/p75", "lcp", "ms", "lcp-family", chromium141, browserNoisy(40, 1000, 20), 2)
	current := documentSeries("browser_document/lcp/a/p75", "lcp", "ms", "lcp-family", chromium141, browserNoisy(20, 1600, 20), 2)
	assessment := Analyze(AnalysisInput{Detection: browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified)}, nil)
	if assessment.BrowserBaseline == nil || assessment.BrowserBaseline.Version != BrowserBaselineVersion || len(assessment.BrowserBaseline.Results) != 1 || assessment.BrowserBaseline.Results[0].State != BrowserBaselineAnomaly {
		t.Fatalf("browser baseline = %+v", assessment.BrowserBaseline)
	}
	if !strings.Contains(EngineVersion(), BrowserBaselineVersion) {
		t.Fatalf("engine version %q omits the detector", EngineVersion())
	}
	// A run without browser documents has no section.
	if assessment := Analyze(AnalysisInput{Detection: browserBaselineInput(nil, nil, BaselineQualified)}, nil); assessment.BrowserBaseline != nil {
		t.Fatalf("no documents = %+v", assessment.BrowserBaseline)
	}
}

// A partial baseline whose script or configuration changed is not the same
// authored page; other partial reasons still compare.
func TestBrowserBaselineRefusesABaselineWhoseScriptChanged(t *testing.T) {
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 1)
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1400, 20), 1)
	input := browserBaselineInput([]Series{current}, []Series{baseline}, BaselinePartial)
	input.Baseline.QualificationReasons = []string{ReasonPacingChanged, ReasonScriptChanged}
	result := DetectBrowserBaseline(input, DefaultBrowserBaselineParams)
	got := result.Results[0]
	if got.State != BrowserBaselineUnscored || got.Reason != BrowserBaselineCohortChanged || !strings.Contains(got.Detail, ReasonScriptChanged) || got.ExpectedHigh != 0 || len(got.Evidence) != 1 {
		t.Fatalf("script changed = %+v", got)
	}
	if len(result.Baseline.Reasons) != 2 || result.Baseline.Qualification != BaselinePartial {
		t.Fatalf("baseline ref = %+v", result.Baseline)
	}
	input.Baseline.QualificationReasons = []string{ReasonPacingChanged}
	if got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results[0]; got.State != BrowserBaselineAnomaly {
		t.Fatalf("pacing-only partial baseline = %+v", got)
	}
}

// Baseline document evidence that was not fully read makes a missing cohort
// unavailable, not absent.
func TestBrowserBaselineUnreadBaselineEvidenceIsNotAbsence(t *testing.T) {
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1400, 20), 1)
	input := browserBaselineInput([]Series{current}, nil, BaselineQualified)
	input.Baseline.DocumentsIncomplete = true
	result := DetectBrowserBaseline(input, DefaultBrowserBaselineParams)
	if result.Status != DetectionPartial || result.Results[0].Reason != BrowserBaselineEvidenceUnavailable || result.Limitations[0].Code != BrowserBaselineEvidenceUnavailable {
		t.Fatalf("unread baseline evidence = %+v", result)
	}
}

// The run's own document evidence that was not fully read makes the section
// partial; evidence it could not read at all makes it unavailable, never
// missing as if the run had no document evidence.
func TestBrowserBaselineRunEvidenceLossIsDisclosed(t *testing.T) {
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1400, 20), 1)
	input := browserBaselineInput([]Series{current}, nil, BaselineQualified)
	input.DocumentsIncomplete = true
	if result := DetectBrowserBaseline(input, DefaultBrowserBaselineParams); result.Status != DetectionPartial || result.Limitations[0].Code != BrowserBaselineRunEvidenceIncomplete {
		t.Fatalf("incomplete run evidence = %+v", result)
	}
	unreadable := browserBaselineInput(nil, nil, BaselineQualified)
	unreadable.DocumentsUnreadable = true
	if result := DetectBrowserBaseline(unreadable, DefaultBrowserBaselineParams); result == nil || result.Status != DetectionUnavailable || len(result.Results) != 0 || result.Limitations[0].Code != BrowserBaselineRunEvidenceUnavailable {
		t.Fatalf("unreadable run evidence = %+v", result)
	}
	// Every series left out by the budget is not a run without documents.
	cut := browserBaselineInput(nil, nil, BaselineQualified)
	cut.DocumentsIncomplete = true
	if result := DetectBrowserBaseline(cut, DefaultBrowserBaselineParams); result == nil || result.Status != DetectionUnavailable || result.Limitations[0].Code != BrowserBaselineRunEvidenceIncomplete {
		t.Fatalf("every series cut = %+v", result)
	}
	if result := DetectBrowserBaseline(browserBaselineInput(nil, nil, BaselineQualified), DefaultBrowserBaselineParams); result != nil {
		t.Fatalf("a run without document evidence = %+v", result)
	}
}

// A profile that returns to a load gives each steady level its own result.
func TestBrowserBaselineRepeatedLoadLevelsAreDistinct(t *testing.T) {
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 1)
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 1)
	input := browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified)
	first := LoadLevel{Start: browserBaselineStart, End: browserBaselineStart.Add(15 * time.Second), VUsers: 10}
	second := LoadLevel{Start: browserBaselineStart.Add(25 * time.Second), End: browserBaselineStart.Add(40 * time.Second), VUsers: 10}
	input.Levels = []LoadLevel{first, second}
	results := DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results
	if len(results) != 2 || results[0].ID == results[1].ID || results[0].LevelStart.Equal(results[1].LevelStart) || !strings.HasPrefix(results[0].ID, "browser_baseline-") {
		t.Fatalf("repeated levels = %+v", results)
	}
}

// A changed cohort names what differs from the closest baseline cohort.
func TestBrowserBaselineNamesTheClosestCohortsDifference(t *testing.T) {
	current := documentSeries("lcp-current", "lcp", "ms", "f", map[string]string{"browser_version": "141.0", "shaping_applied": "true"}, browserNoisy(10, 1400, 20), 1)
	candidates := []Series{
		documentSeries("lcp-a", "lcp", "ms", "f", map[string]string{"browser_version": "141.0", "shaping_applied": "false"}, browserNoisy(40, 1000, 20), 1),
		documentSeries("lcp-b", "lcp", "ms", "f", map[string]string{"browser_version": "142.0", "shaping_applied": "false"}, browserNoisy(40, 1000, 20), 1),
	}
	got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, candidates, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
	if got.Reason != BrowserBaselineCohortChanged || !strings.HasSuffix(got.Detail, "different shaping_applied") {
		t.Fatalf("closest cohort difference = %+v", got)
	}
}

// Every detection point stays 40 bytes on 64-bit hosts: document counts
// pack beside Valid instead of growing every series.
func TestSeriesPointStaysPacked(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(SeriesPoint{}) != 40 {
		t.Fatalf("SeriesPoint is %d bytes", unsafe.Sizeof(SeriesPoint{}))
	}
}

// A script change refuses only the cohorts of the group that ran it; a
// configuration change refuses every cohort.
func TestBrowserBaselineScriptChangeRefusesOnlyItsGroup(t *testing.T) {
	cohort := func(id, group string, values []float64) Series {
		s := documentSeries(id, "lcp", "ms", "f-"+group, chromium141, values, 1)
		s.Document.Group = group
		return s
	}
	current := []Series{cohort("checkout", "checkout_ui", browserNoisy(10, 1400, 20)), cohort("api", "api_load", browserNoisy(10, 1400, 20))}
	baseline := []Series{cohort("checkout", "checkout_ui", browserNoisy(40, 1000, 20)), cohort("api", "api_load", browserNoisy(40, 1000, 20))}
	input := browserBaselineInput(current, baseline, BaselinePartial)
	input.Baseline.QualificationReasons = []string{ReasonScriptChanged}
	input.Baseline.ScriptChangedGroups = map[string]bool{"api_load": true}
	states := map[string]BrowserBaselineResult{}
	for _, result := range DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results {
		states[result.Series] = result
	}
	if states["checkout"].State != BrowserBaselineAnomaly {
		t.Fatalf("the unchanged group was refused: %+v", states["checkout"])
	}
	if api := states["api"]; api.State != BrowserBaselineUnscored || !strings.Contains(api.Detail, `"api_load"`) {
		t.Fatalf("the changed group = %+v", api)
	}
	input.Baseline.QualificationReasons = []string{ReasonConfigChanged}
	input.Baseline.ScriptChangedGroups = map[string]bool{}
	for _, result := range DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results {
		if result.State != BrowserBaselineUnscored || !strings.Contains(result.Detail, ReasonConfigChanged) {
			t.Fatalf("a configuration change left %+v scored", result)
		}
	}
}

func TestScriptChangedGroups(t *testing.T) {
	current := WorkloadKey{Groups: map[string]WorkloadGroup{"same": {ScriptDigest: "a"}, "changed": {ScriptDigest: "b"}, "unrecorded": {}, "new": {ScriptDigest: "c"}}}
	baseline := WorkloadKey{Groups: map[string]WorkloadGroup{"same": {ScriptDigest: "a"}, "changed": {ScriptDigest: "x"}, "unrecorded": {ScriptDigest: "d"}}}
	got := ScriptChangedGroups(current, baseline)
	if len(got) != 2 || !got["changed"] || !got["unrecorded"] {
		t.Fatalf("changed groups = %v", got)
	}
}

// Every baseline level at the run's load contributes its buckets.
func TestBrowserBaselinePoolsBaselineLevelsAtOneLoad(t *testing.T) {
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 1)
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1400, 20), 1)
	input := browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified)
	// The baseline returned to the run's load: 20 buckets at each visit.
	input.Baseline.Levels = []LoadLevel{
		{Start: browserBaselineStart, End: browserBaselineStart.Add(20 * time.Second), VUsers: 10},
		{Start: browserBaselineStart.Add(20 * time.Second), End: browserBaselineStart.Add(40 * time.Second), VUsers: 10},
	}
	got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results[0]
	if got.BaselineBuckets < DefaultBrowserBaselineParams.MinBaselineBuckets || got.State != BrowserBaselineAnomaly {
		t.Fatalf("pooled baseline levels = %+v", got)
	}
}

func TestBrowserBaselineRequiresComparableBucketCadence(t *testing.T) {
	for _, tc := range []struct {
		name              string
		current, baseline time.Duration
		observation       string
		want              string
	}{
		{"equal", time.Second, time.Second, ObservationInterval, ""},
		{"changed", time.Second, 2 * time.Second, ObservationInterval, "cadence_changed"},
		{"current missing", 0, time.Second, ObservationInterval, "cadence_unavailable"},
		{"baseline missing", time.Second, 0, ObservationInterval, "cadence_unavailable"},
		{"negative", time.Second, -time.Second, ObservationInterval, "cadence_unavailable"},
		{"snapshot", time.Second, time.Second, ObservationSnapshot, "cadence_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(20, 1400, 20), 7)
			baseline := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(40, 1000, 20), 3)
			current.Bucket, baseline.Bucket, baseline.Observation = tc.current, tc.baseline, tc.observation
			got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams)
			if got == nil || len(got.Results) != 1 {
				t.Fatalf("result=%+v", got)
			}
			r := got.Results[0]
			if tc.want == "" {
				if r.State != BrowserBaselineAnomaly || r.Current != 1400 || r.CurrentSamples != 140 || r.BaselineSamples != 120 {
					t.Fatalf("equal cadence changed estimator: %+v", r)
				}
			} else if r.State != BrowserBaselineUnscored || r.Reason != tc.want || r.Z != 0 || r.ExpectedHigh != 0 || len(r.Evidence) != 1 {
				t.Fatalf("incompatible cadence scored: %+v", r)
			}
		})
	}
}

func TestBrowserBaselineChecksTupleEvenWhenSeriesIDMatches(t *testing.T) {
	for _, field := range []string{"runtime", "metric", "family", "group", "unit"} {
		t.Run(field, func(t *testing.T) {
			current := documentSeries("same-id", "lcp", "ms", "family", chromium141, browserNoisy(20, 1400, 20), 3)
			baseline := documentSeries("same-id", "lcp", "ms", "family", chromium141, browserNoisy(40, 1000, 20), 3)
			switch field {
			case "runtime":
				baseline.Document.Fields = map[string]string{"browser_version": "142.0", "shaping_applied": "true"}
			case "metric":
				baseline.Document.Metric = "fcp"
			case "family":
				baseline.Document.Family = "other-family"
			case "group":
				baseline.Document.Group = "other-group"
			case "unit":
				baseline.Unit = "seconds"
			}
			got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
			if got.State != BrowserBaselineUnscored || got.Reason != BrowserBaselineCohortChanged || got.Z != 0 || got.ExpectedHigh != 0 || len(got.Evidence) != 1 {
				t.Fatalf("different tuple scored: %+v", got)
			}
		})
	}
}

func TestBrowserBaselineRejectsAmbiguousBucketTimestamps(t *testing.T) {
	for _, side := range []string{"current", "baseline"} {
		for _, kind := range []string{"duplicate", "off grid", "zero timestamp"} {
			t.Run(side+"/"+kind, func(t *testing.T) {
				current := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(20, 1400, 20), 3)
				baseline := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(40, 1000, 20), 3)
				changed := &baseline
				if side == "current" {
					changed = &current
				}
				switch kind {
				case "duplicate":
					changed.Points = append(changed.Points, changed.Points[1])
				case "off grid":
					changed.Points[1].At = changed.Points[1].At.Add(time.Millisecond)
				case "zero timestamp":
					changed.Points[1].At = time.Time{}
				}
				got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams)
				if len(got.Results) != 1 {
					t.Fatalf("results: %+v", got)
				}
				r := got.Results[0]
				if r.State != BrowserBaselineUnscored || r.Reason != BrowserBaselineBucketGridInvalid || r.Z != 0 || r.ExpectedHigh != 0 {
					t.Fatalf("ambiguous grid scored: %+v", r)
				}
			})
		}
	}
}

func TestBrowserBaselineBucketGridPreservesMissingAndUnorderedEvidence(t *testing.T) {
	current := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(20, 1400, 20), 3)
	baseline := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(40, 1000, 20), 3)
	// A whole missing bucket is a gap, not an extra sample or an irregular grid.
	baseline.Points = append(baseline.Points[:10], baseline.Points[11:]...)
	baseline.Points[0], baseline.Points[20] = baseline.Points[20], baseline.Points[0]
	before := append([]SeriesPoint(nil), baseline.Points...)
	got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
	if got.State != BrowserBaselineAnomaly || got.BaselineBuckets != 39 || got.BaselineSamples != 117 {
		t.Fatalf("missing bucket semantics changed: %+v", got)
	}
	if !reflect.DeepEqual(before, baseline.Points) {
		t.Fatal("detector mutated caller evidence")
	}
	// Re-publication of five buckets must not satisfy the thirty-bucket minimum.
	baseline.Points = nil
	five := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(5, 1000, 20), 3)
	for i := 0; i < 8; i++ {
		baseline.Points = append(baseline.Points, five.Points...)
	}
	got = DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
	if got.State != BrowserBaselineUnscored || got.Reason != BrowserBaselineBucketGridInvalid {
		t.Fatalf("republication manufactured baseline: %+v", got)
	}
}

func TestBrowserBucketGridBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		points []SeriesPoint
		bucket time.Duration
		want   bool
	}{
		{"empty", nil, time.Second, true},
		{"no cadence", nil, 0, false},
		{"negative cadence", nil, -time.Second, false},
		{"independent origin", []SeriesPoint{{At: browserBaselineStart.Add(123 * time.Millisecond)}, {At: browserBaselineStart.Add(123*time.Millisecond + 2*time.Second)}}, time.Second, true},
		{"duration overflow", []SeriesPoint{{At: time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)}, {At: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}}, time.Nanosecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserBucketGridValid(tc.points, tc.bucket); got != tc.want {
				t.Fatalf("grid valid=%v want %v", got, tc.want)
			}
		})
	}
}

func TestBrowserBaselineRequiresWholeSourceWindowWithinSteadyLoad(t *testing.T) {
	for _, side := range []string{"current", "baseline"} {
		for _, kind := range []string{"missing", "zero start", "zero end", "reversed", "invalid constituent", "ramp", "after span", "wrong bucket", "equal endpoints", "exact span start"} {
			t.Run(side+"/"+kind, func(t *testing.T) {
				current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(20, 1400, 20), 3)
				baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 3)
				changed := &current
				reason := BrowserBaselineSourceWindowInvalid
				if side == "baseline" {
					changed, reason = &baseline, BrowserBaselineBaselineWindowInvalid
				}
				at := changed.Points[1].At.UTC()
				w := changed.Document.Windows[at]
				switch kind {
				case "missing":
					delete(changed.Document.Windows, at)
				case "zero start":
					w.Start = time.Time{}
				case "zero end":
					w.End = time.Time{}
				case "reversed":
					w.Start = w.End.Add(time.Second)
				case "invalid constituent":
					w.Invalid = true
				case "ramp":
					w.Start = browserBaselineStart.Add(-time.Nanosecond)
				case "after span":
					w.End = browserBaselineStart.Add(time.Minute + time.Nanosecond)
				case "wrong bucket":
					w.End = at.Add(time.Second)
				case "equal endpoints":
					w.Start = w.End
				case "exact span start":
					w.Start = browserBaselineStart
				}
				if kind != "missing" {
					changed.Document.Windows[at] = w
				}
				got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams).Results[0]
				if kind == "equal endpoints" || kind == "exact span start" {
					if got.State != BrowserBaselineAnomaly {
						t.Fatalf("valid window = %+v", got)
					}
				} else if got.State != BrowserBaselineUnscored || got.Reason != reason || got.Z != 0 || got.ExpectedHigh != 0 || len(got.Evidence) != 1 {
					t.Fatalf("unqualified window = %+v", got)
				}
				if got.CurrentBuckets != 20 || got.BaselineBuckets != 40 || got.CurrentSamples != 60 || got.BaselineSamples != 120 {
					t.Fatalf("lost retained population = %+v", got)
				}
			})
		}
	}
}

func TestBrowserBaselineSourceWindowCannotBridgePauseBetweenSameLoadSpans(t *testing.T) {
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(20, 1400, 20), 3)
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(60, 1000, 20), 3)
	input := browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified)
	input.Baseline.Levels = []LoadLevel{
		{Start: browserBaselineStart, End: browserBaselineStart.Add(20 * time.Second), VUsers: 10},
		{Start: browserBaselineStart.Add(30 * time.Second), End: browserBaselineStart.Add(time.Minute), VUsers: 10},
	}
	at := baseline.Points[35].At.UTC()
	baseline.Document.Windows[at] = DocumentWindow{Start: browserBaselineStart.Add(10 * time.Second), End: at.Add(time.Millisecond)}
	got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results[0]
	if got.State != BrowserBaselineUnscored || got.Reason != BrowserBaselineBaselineWindowInvalid || got.BaselineBuckets != 50 {
		t.Fatalf("cross-pause population = %+v", got)
	}
	// An invalid window outside the compared spans must not poison valid evidence.
	baseline.Document.Windows[at] = DocumentWindow{Start: at, End: at.Add(time.Millisecond)}
	baseline.Document.Windows[baseline.Points[25].At.UTC()] = DocumentWindow{Invalid: true}
	if got := DetectBrowserBaseline(input, DefaultBrowserBaselineParams).Results[0]; got.State != BrowserBaselineAnomaly {
		t.Fatalf("excluded pause bucket = %+v", got)
	}
}
