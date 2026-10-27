package rca

import (
	"reflect"
	"testing"
	"time"
)

func TestBrowserBaselineRequiresClockAndCoverageOnBothSides(t *testing.T) {
	for _, tc := range []struct {
		name, clock, coverage, want string
		baseline                    bool
	}{
		{"current absent", "", "", BrowserClockUnavailable, false},
		{"current unknown clock", "unknown", BrowserEvidenceQualified, BrowserClockUnavailable, false},
		{"current missing coverage", BrowserEvidenceQualified, "", BrowserSourceCoverageUnavailable, false},
		{"baseline absent", "", "", BrowserBaselineClockUnavailable, true},
		{"baseline missing coverage", BrowserEvidenceQualified, "unavailable", BrowserBaselineSourceCoverageUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(20, 1400, 20), 3)
			baseline := documentSeries("lcp", "lcp", "ms", "family", chromium141, browserNoisy(40, 1000, 20), 3)
			doc := current.Document
			if tc.baseline {
				doc = baseline.Document
			}
			doc.ClockQualification, doc.SourceCoverageQualification = tc.clock, tc.coverage
			before := append([]SeriesPoint(nil), current.Points...)
			got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams)
			if got == nil || got.Status != DetectionPartial || len(got.Results) != 1 {
				t.Fatalf("qualification result: %+v", got)
			}
			r := got.Results[0]
			if r.State != BrowserBaselineUnscored || r.Reason != tc.want || r.Z != 0 || r.ExpectedHigh != 0 || r.ExpectedLow != 0 || r.CurrentSamples != 60 || r.BaselineSamples != 120 {
				t.Fatalf("unqualified result: %+v", r)
			}
			found := false
			for _, l := range got.Limitations {
				found = found || l.Code == tc.want
			}
			if !found || !reflect.DeepEqual(before, current.Points) {
				t.Fatalf("missing limitation or changed evidence: %+v", got)
			}
		})
	}
}

func TestBrowserQualificationAlsoGuardsInRunDetection(t *testing.T) {
	values := browserNoisy(60, 1000, 20)
	for i := 40; i < len(values); i++ {
		values[i] += 1500
	}
	browser := documentSeries("lcp", "lcp", "ms", "family", chromium141, values, 3)
	browser.Document.ClockQualification, browser.Document.SourceCoverageQualification = "", ""
	http := browser
	http.ID, http.Kind, http.Document = "http", SeriesLatency, nil
	input := browserBaselineInput([]Series{browser, http}, nil, BaselineUnqualified)
	anomalies, detection, limits := Detect(input, DefaultDetectParams, nil)
	if detection.SeriesEvaluated != 1 || detection.SeriesExcluded != 1 || detection.Status != DetectionPartial {
		t.Fatalf("detection: %+v", detection)
	}
	for _, a := range anomalies {
		if a.Series == browser.ID {
			t.Fatalf("unqualified browser anomaly: %+v", a)
		}
	}
	for _, s := range detection.Spikes {
		if s.Series == browser.ID {
			t.Fatalf("unqualified browser spike: %+v", s)
		}
	}
	reasons := map[string]bool{}
	for _, l := range limits {
		reasons[l.Code] = true
	}
	if !reasons[BrowserClockUnavailable] || !reasons[BrowserSourceCoverageUnavailable] {
		t.Fatalf("qualification disclosure: %+v", limits)
	}
	onlyHTTP := input
	onlyHTTP.Series = []Series{http}
	expected, expectedDetection, _ := Detect(onlyHTTP, DefaultDetectParams, nil)
	if !reflect.DeepEqual(anomalies, expected) || !reflect.DeepEqual(detection.Spikes, expectedDetection.Spikes) {
		t.Fatal("non-browser detection changed")
	}
}

func TestBrowserTimelineOnsetQualificationAndInvalidBucketFiltering(t *testing.T) {
	if EngineVersionBase != "apexload.rca.engine.v8" {
		t.Fatalf("EngineVersionBase = %q, want apexload.rca.engine.v8", EngineVersionBase)
	}
	values := browserNoisy(60, 1000, 10)
	// Inject a massive outlier into bucket 10, marked invalid in Document.Windows.
	values[10] = 99999
	for i := 40; i < len(values); i++ {
		values[i] += 1500
	}
	browser := documentSeries("browser_document/lcp/a/p75", "lcp", "ms", "family", chromium141, values, 3)
	browser.Document.ClockUncertainty = 15 * time.Millisecond
	invalidAt := browser.Points[10].At.UTC()
	invalidWin := browser.Document.Windows[invalidAt]
	invalidWin.Invalid = true
	invalidWin.InvalidReason = "observation_window_exceeds_bucket"
	browser.Document.Windows[invalidAt] = invalidWin

	baseline := documentSeries("browser_document/lcp/a/p75", "lcp", "ms", "family", chromium141, browserNoisy(60, 1000, 10), 3)
	input := browserBaselineInput([]Series{browser}, []Series{baseline}, BaselineQualified)
	anomalies, detection, limits := Detect(input, DefaultDetectParams, nil)
	if detection.SeriesEvaluated != 1 || detection.SeriesExcluded != 0 || detection.Status != DetectionComplete || len(limits) != 0 {
		t.Fatalf("unexpected detection summary: %+v limits=%+v", detection, limits)
	}
	if len(detection.Spikes) != 0 {
		t.Fatalf("invalid bucket 10 leaked into spikes: %+v", detection.Spikes)
	}
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 in-run ScopeTime anomaly, got %+v", anomalies)
	}
	got := anomalies[0]
	expectedOnset := browser.Points[40].At.UTC()
	if got.Series != browser.ID || got.Scope != ScopeTime || got.Baseline.Qualification != BaselineSameRun || got.Baseline.TestID != "" {
		t.Fatalf("unexpected anomaly scope/baseline: %+v", got)
	}
	if !got.Onset.Equal(expectedOnset) {
		t.Fatalf("onset = %v, want %v (invalid bucket 10 must be ignored)", got.Onset, expectedOnset)
	}
	if got.OnsetUncertainty < browser.Bucket+browser.Document.ClockUncertainty {
		t.Fatalf("OnsetUncertainty = %v, want >= %v", got.OnsetUncertainty, browser.Bucket+browser.Document.ClockUncertainty)
	}

	// Non-sub-bucket clock uncertainty (>= Bucket) or missing TimelineQualification must be excluded.
	unqualified := browser
	unqualifiedDoc := *browser.Document
	unqualifiedDoc.ClockUncertainty = browser.Bucket
	unqualified.Document = &unqualifiedDoc
	unqAnomalies, unqDetection, unqLimits := Detect(browserBaselineInput([]Series{unqualified}, nil, BaselineUnqualified), DefaultDetectParams, nil)
	if len(unqAnomalies) != 0 || unqDetection.SeriesEvaluated != 0 || unqDetection.SeriesExcluded != 1 {
		t.Fatalf("expected >= bucket uncertainty to be excluded, got %+v %+v", unqAnomalies, unqDetection)
	}
	foundTimelineLimit := false
	for _, l := range unqLimits {
		if l.Code == BrowserTimelineUnavailable {
			foundTimelineLimit = true
		}
	}
	if !foundTimelineLimit {
		t.Fatalf("expected %s limitation, got %+v", BrowserTimelineUnavailable, unqLimits)
	}

	// Incomplete document evidence or invalid bucket grid must also be excluded.
	incompleteSeries := browser
	incompleteDoc := *browser.Document
	incompleteDoc.Incomplete = true
	incompleteSeries.Document = &incompleteDoc
	incAnomalies, incDetection, incLimits := Detect(browserBaselineInput([]Series{incompleteSeries}, nil, BaselineUnqualified), DefaultDetectParams, nil)
	if len(incAnomalies) != 0 || incDetection.SeriesEvaluated != 0 || incDetection.SeriesExcluded != 1 || len(incLimits) == 0 {
		t.Fatalf("expected incomplete series to be excluded, got %+v %+v %+v", incAnomalies, incDetection, incLimits)
	}

	// Mixed monotonic spans across a mid-level pause must not bridge or score.
	bridged := documentSeries("browser_document/lcp/bridged/p75", "lcp", "ms", "family", chromium141, values, 3)
	for i, p := range bridged.Points {
		at := p.At.UTC()
		w := bridged.Document.Windows[at]
		if i == 10 {
			w.Invalid = true
		} else {
			w.MonotonicVerified = true
			w.MonotonicVUsers = 10
			if i < 10 {
				w.MonotonicSpanID = "span-1"
			} else {
				w.MonotonicSpanID = "span-2"
			}
		}
		bridged.Document.Windows[at] = w
	}
	bridgedAnomalies, bridgedDetection, _ := Detect(browserBaselineInput([]Series{bridged}, nil, BaselineUnqualified), DefaultDetectParams, nil)
	if len(bridgedAnomalies) != 0 || len(bridgedDetection.Spikes) != 0 {
		t.Fatalf("mixed monotonic spans across pause produced anomalies/spikes: %+v %+v", bridgedAnomalies, bridgedDetection.Spikes)
	}
}
