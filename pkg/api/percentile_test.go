package api

import (
	"math"
	"testing"
)

func TestPercentileFromSortedSamplesInterpolatesSparseBuckets(t *testing.T) {
	samples := []float64{100, 300}

	tests := []struct {
		name string
		q    float64
		want float64
	}{
		{name: "p50", q: 0.50, want: 200},
		{name: "p90", q: 0.90, want: 280},
		{name: "p95", q: 0.95, want: 290},
		{name: "p99", q: 0.99, want: 298},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := percentileFromSortedSamples(samples, tt.q)
			if math.Abs(got-tt.want) > 0.000001 {
				t.Fatalf("percentileFromSortedSamples(%v, %.2f) = %v, want %v", samples, tt.q, got, tt.want)
			}
		})
	}
}

func TestPercentileFromSortedSamplesBoundsAndDegenerateInputs(t *testing.T) {
	tests := []struct {
		name    string
		samples []float64
		q       float64
		want    float64
	}{
		{name: "empty", samples: nil, q: 0.95, want: 0},
		{name: "lower bound", samples: []float64{10, 20, 30}, q: 0, want: 10},
		{name: "upper bound", samples: []float64{10, 20, 30}, q: 1, want: 30},
		{name: "nan uses lower bound", samples: []float64{10, 20, 30}, q: math.NaN(), want: 10},
		{name: "single sample", samples: []float64{42}, q: 0.99, want: 42},
		{name: "identical samples", samples: []float64{25, 25, 25}, q: 0.95, want: 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := percentileFromSortedSamples(tt.samples, tt.q)
			if math.Abs(got-tt.want) > 0.000001 {
				t.Fatalf("percentileFromSortedSamples(%v, %v) = %v, want %v", tt.samples, tt.q, got, tt.want)
			}
		})
	}
}

func TestArchiveAnalysisPercentileUsesInterpolatedHelper(t *testing.T) {
	samples := []float64{100, 300}

	if got := archiveAnalysisPercentile(samples, 0.95); math.Abs(got-290) > 0.000001 {
		t.Fatalf("archiveAnalysisPercentile() = %v, want 290", got)
	}
}

func TestDistributedPercentileValueUsesInterpolatedHelper(t *testing.T) {
	samples := []float64{100, 300}

	if got := percentileValue(samples, 0.95); math.Abs(got-290) > 0.000001 {
		t.Fatalf("percentileValue() = %v, want 290", got)
	}
}
