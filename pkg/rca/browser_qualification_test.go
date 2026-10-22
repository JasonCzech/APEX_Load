package rca

import (
	"reflect"
	"testing"
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
