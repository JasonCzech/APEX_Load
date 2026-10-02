package api

import (
	"context"
	"github.com/jasonczech/apexload/pkg/storage"
	"math"
	"testing"
	"time"
)

func TestNativeMergedRatesRequireContainedNonoverlappingWindows(t *testing.T) {
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	makeWindow := func(end, duration float64) *LiveMetrics {
		return &LiveMetrics{Timestamp: start.Add(time.Duration(end * float64(time.Second))), NativeEvidence: &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: duration, Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": {"checkout": nativeBucketMetric(10)}}}}
	}
	for _, tc := range []struct {
		name                             string
		history                          []*LiveMetrics
		bucketSeconds, endSeconds, index int
		coverage                         float64
		wantRate                         *float64
	}{
		{name: "boundary spanning", history: []*LiveMetrics{makeWindow(10, 10)}, bucketSeconds: 2, endSeconds: 10, index: 4, coverage: 1},
		{name: "overlapping", history: []*LiveMetrics{makeWindow(1, 1), makeWindow(1.5, 1)}, bucketSeconds: 2, endSeconds: 2, coverage: .75},
		{name: "overlapping despite full union", history: []*LiveMetrics{makeWindow(1.5, 1.5), makeWindow(2, 1)}, bucketSeconds: 2, endSeconds: 2, coverage: 1},
		{name: "adjacent", history: []*LiveMetrics{makeWindow(1, 1), makeWindow(2, 1)}, bucketSeconds: 2, endSeconds: 2, coverage: 1, wantRate: ratePointer(1)},
		{name: "gap", history: []*LiveMetrics{makeWindow(.5, .5), makeWindow(2, .5)}, bucketSeconds: 2, endSeconds: 2, coverage: .5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := nativeEvidenceQuery("live")
			q.LiveBucketMode = "merged"
			q.Metric = "observation_rate"
			q.Aggregation = "rate"
			q.BucketSeconds = tc.bucketSeconds
			q.EndSeconds = tc.endSeconds
			out, err := buildNativeLiveEvidence(context.Background(), start, tc.history, q)
			if err != nil || len(out.Series) != 1 {
				t.Fatalf("output %+v: %v", out, err)
			}
			point := out.Series[0].Points[tc.index]
			if point.Coverage == nil || math.Abs(*point.Coverage-tc.coverage) > 1e-9 || (point.Value == nil) != (tc.wantRate == nil) || point.Value != nil && *point.Value != *tc.wantRate {
				t.Fatalf("point %+v; expected coverage %v / rate %v", point, tc.coverage, tc.wantRate)
			}
			// Completion-cohort counts retain their existing approximate semantics.
			q.Metric = "observation_count"
			q.Aggregation = "count"
			out, err = buildNativeLiveEvidence(context.Background(), start, tc.history, q)
			if err != nil || out.Series[0].Points[tc.index].Value == nil || *out.Series[0].Points[tc.index].Value != float64(len(tc.history)) {
				t.Fatalf("non-rate cohort changed %+v: %v", out, err)
			}
		})
	}
}
func ratePointer(v float64) *float64 { return &v }

func TestNativeMergedRatesCheckCoveragePerSource(t *testing.T) {
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	frame := func(source string, sequence uint64, end, duration float64) *LiveMetrics {
		at := start.Add(time.Duration(end * float64(time.Second)))
		return &LiveMetrics{Timestamp: at, NativeEvidence: &storage.NativeIntervalEvidence{Publication: &storage.NativePublicationEvidence{SourceID: source, Sequence: sequence, ExecutionGeneration: 1, Timestamp: at, Version: 2, WindowSeconds: duration, ExpectedSources: []string{"a", "b"}, Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": {"checkout": nativeBucketMetric(10)}}}}}
	}
	for _, tc := range []struct {
		name     string
		history  []*LiveMetrics
		coverage float64
		rate     *float64
	}{
		{"different source windows may overlap", []*LiveMetrics{frame("a", 2, 2, 1), frame("b", 1, 2, 2), frame("a", 1, 1, 1)}, 1, ratePointer(1.5)},
		{"one partially covered source", []*LiveMetrics{frame("a", 1, 1, 1), frame("a", 2, 2, 1), frame("b", 1, 2, 1)}, .5, nil},
		{"missing expected source", []*LiveMetrics{frame("a", 1, 1, 1), frame("a", 2, 2, 1)}, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := nativeEvidenceQuery("live")
			q.LiveBucketMode = "merged"
			q.Metric = "observation_rate"
			q.Aggregation = "rate"
			q.BucketSeconds = 2
			q.EndSeconds = 2
			out, err := buildNativeLiveEvidence(context.Background(), start, tc.history, q)
			if err != nil || len(out.Series) != 1 {
				t.Fatalf("output %+v: %v", out, err)
			}
			point := out.Series[0].Points[0]
			if point.Coverage == nil || *point.Coverage != tc.coverage || (point.Value == nil) != (tc.rate == nil) || point.Value != nil && *point.Value != *tc.rate {
				t.Fatalf("point %+v; expected coverage %v / rate %v", point, tc.coverage, tc.rate)
			}
		})
	}
}

func TestNativeMergedRatesExactFractionalCoverage(t *testing.T) {
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	history := make([]*LiveMetrics, 10)
	for i := range history {
		history[i] = &LiveMetrics{Timestamp: start.Add(time.Duration(i+1) * 100 * time.Millisecond), NativeEvidence: &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: .1, Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": {"checkout": nativeBucketMetric(10)}}}}
	}
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode = "merged"
	q.Metric = "observation_rate"
	q.Aggregation = "rate"
	q.BucketSeconds = 1
	q.EndSeconds = 1
	out, err := buildNativeLiveEvidence(context.Background(), start, history, q)
	if err != nil || len(out.Series) != 1 {
		t.Fatalf("output %+v: %v", out, err)
	}
	point := out.Series[0].Points[0]
	if point.Coverage == nil || *point.Coverage != 1 || point.Value == nil || *point.Value != 10 {
		t.Fatalf("exact fractional tiling lost coverage: %+v", point)
	}
}

func TestNativeMergedRatesPreserveNanosecondWindowResolution(t *testing.T) {
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	first := 62812 * time.Nanosecond
	durations := []time.Duration{first, time.Second - first}
	history := make([]*LiveMetrics, 0, len(durations))
	at := start
	for _, duration := range durations {
		at = at.Add(duration)
		history = append(history, &LiveMetrics{Timestamp: at, NativeEvidence: &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: duration.Seconds(), Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": {"checkout": nativeBucketMetric(10)}}}})
	}
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode = "merged"
	q.Metric = "observation_rate"
	q.Aggregation = "rate"
	q.BucketSeconds = 1
	q.EndSeconds = 1
	out, err := buildNativeLiveEvidence(context.Background(), start, history, q)
	if err != nil || len(out.Series) != 1 {
		t.Fatalf("output %+v: %v", out, err)
	}
	point := out.Series[0].Points[0]
	if point.Coverage == nil || *point.Coverage != 1 || point.Value == nil || *point.Value != 2 {
		t.Fatalf("exact nanosecond tiling lost coverage: %+v", point)
	}
}
