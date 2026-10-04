package api

import (
	"testing"
	"time"
)

// oracleCorrelationFixture builds one-minute interval summaries whose P95
// rises with the index, and wait-time samples placed by sampleAt.
func oracleCorrelationFixture(intervals int, samples []int, sampleAt func(index int) time.Time) ([]ResourceSample, []IntervalSummaryRecord) {
	start := time.Date(2026, time.October, 4, 10, 0, 0, 0, time.UTC)
	summaries := make([]IntervalSummaryRecord, 0, intervals)
	for i := 0; i < intervals; i++ {
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i+1) * time.Minute), SampleWindowSeconds: 60, P95Ms: 100 + 20*float64(i)})
	}
	resources := make([]ResourceSample, 0, len(samples))
	for _, i := range samples {
		at := start.Add(time.Duration(i)*time.Minute + 30*time.Second)
		if sampleAt != nil {
			at = sampleAt(i)
		}
		resources = append(resources, ResourceSample{Timestamp: at, External: map[string]float64{"oracle.db.wait_time_ms": 50 + 10*float64(i)}})
	}
	return resources, summaries
}

func indexes(from, to int) []int {
	out := []int{}
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	return out
}

func TestOracleCorrelationPairsOnlyContainingIntervals(t *testing.T) {
	metric := []string{"oracle.db.wait_time_ms"}
	tests := []struct {
		name      string
		intervals int
		samples   []int
		sampleAt  func(int) time.Time
		wantPairs int64
	}{
		{name: "ten contained samples score", intervals: 12, samples: indexes(0, 10), wantPairs: 10},
		{name: "nine pairs are not enough", intervals: 12, samples: indexes(0, 9)},
		{
			// The previous nearest-at-any-distance rule paired these with the
			// last interval; samples outside every window must stay unpaired.
			name: "samples an hour after the run are rejected", intervals: 12, samples: indexes(0, 12),
			sampleAt: func(i int) time.Time {
				return time.Date(2026, time.October, 4, 11, 13, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
			},
		},
		{
			// Eleven paired intervals across a span of thirty-one is 35% coverage.
			name: "sparse coverage across a long span is rejected", intervals: 40, samples: append(indexes(0, 10), 30),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources, summaries := oracleCorrelationFixture(tt.intervals, tt.samples, tt.sampleAt)
			correlations := buildOracleDBMetricCorrelations(resources, summaries, metric)
			if tt.wantPairs == 0 {
				if len(correlations) != 0 {
					t.Fatalf("correlations = %#v, want none", correlations)
				}
				return
			}
			if len(correlations) != 1 || correlations[0].Samples != tt.wantPairs || correlations[0].Strength != "strong" {
				t.Fatalf("correlations = %#v, want one strong correlation over %d pairs", correlations, tt.wantPairs)
			}
		})
	}
}

func TestOracleP95WindowsSkipUnavailablePercentilesAndDerivePlainSpacing(t *testing.T) {
	start := time.Date(2026, time.October, 4, 10, 0, 0, 0, time.UTC)
	windows := oracleP95Windows([]IntervalSummaryRecord{
		{Timestamp: start.Add(2 * time.Minute), P95Ms: 120},
		{Timestamp: start.Add(time.Minute), P95Ms: 110},
		{Timestamp: start.Add(3 * time.Minute), P95Ms: 999, PercentileQuality: archivePercentileQualityUnavailable},
		{Timestamp: start.Add(4 * time.Minute), P95Ms: 140},
	})
	if len(windows) != 3 {
		t.Fatalf("windows = %#v, want three percentile-bearing windows", windows)
	}
	if !windows[0].start.Equal(start) || !windows[0].end.Equal(start.Add(time.Minute)) {
		t.Fatalf("first window = %#v, want it to borrow the next one-minute spacing", windows[0])
	}
	if !windows[2].start.Equal(start.Add(2*time.Minute)) || windows[2].p95 != 140 {
		t.Fatalf("last window = %#v, want it to span back to the previous available summary", windows[2])
	}
}
