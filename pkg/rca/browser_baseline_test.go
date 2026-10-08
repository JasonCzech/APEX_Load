package rca

import (
	"math"
	"strings"
	"testing"
	"time"
)

var browserBaselineStart = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// documentSeries is one browser document cohort's per-bucket p75 series,
// each bucket holding samples documents.
func documentSeries(id, metric, unit, family string, fields map[string]string, values []float64, samples int64) Series {
	s := Series{ID: id, Kind: SeriesBrowserDocument, Unit: unit, Observation: ObservationInterval, Bucket: time.Second,
		Evidence: EvidenceRef{Kind: "browser_document", Locator: "browser_documents.jsonl#" + id}, Document: &DocumentCohort{Metric: metric, Family: family, Fields: fields}}
	for i, v := range values {
		point := SeriesPoint{At: browserBaselineStart.Add(time.Duration(i) * time.Second), Value: v, Valid: !math.IsNaN(v), Count: samples}
		if !point.Valid {
			point.Value, point.Count = 0, 0
		}
		s.Points = append(s.Points, point)
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

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9*math.Max(1, math.Abs(b)) }

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
	if got.Current != 1400 || !near(got.BaselineMean, 1000) || !near(got.EffectFloor, 100) || got.ExpectedHigh <= got.BaselineMean || got.ExpectedLow >= got.BaselineMean || len(got.Evidence) != 2 {
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
	if got.State != BrowserBaselineWithinRange || math.Abs(got.Z) < 3 || !near(got.EffectFloor, 100) {
		t.Fatalf("below the effect floor = %+v", got)
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
