package engine

import (
	"math"
	"math/rand"
	"testing"
)

func TestTDigest_Empty(t *testing.T) {
	td := NewTDigest(100)
	if td.Count() != 0 {
		t.Errorf("Count = %d, want 0", td.Count())
	}
	if td.Quantile(0.5) != 0 {
		t.Errorf("Quantile(0.5) = %v, want 0", td.Quantile(0.5))
	}
}

func TestTDigest_SingleValue(t *testing.T) {
	td := NewTDigest(100)
	td.Add(42)

	if td.Count() != 1 {
		t.Errorf("Count = %d, want 1", td.Count())
	}
	got := td.Quantile(0.5)
	if got != 42 {
		t.Errorf("Quantile(0.5) = %v, want 42", got)
	}
}

func TestTDigest_MinMax(t *testing.T) {
	td := NewTDigest(100)
	for i := 1; i <= 100; i++ {
		td.Add(float64(i))
	}

	if td.Quantile(0) != 1 {
		t.Errorf("Quantile(0) = %v, want 1 (min)", td.Quantile(0))
	}
	if td.Quantile(1) != 100 {
		t.Errorf("Quantile(1) = %v, want 100 (max)", td.Quantile(1))
	}
}

func TestTDigest_Uniform_Percentiles(t *testing.T) {
	td := NewTDigest(200)

	n := 10000
	for i := 1; i <= n; i++ {
		td.Add(float64(i))
	}

	tests := []struct {
		q         float64
		expected  float64
		tolerance float64
	}{
		{0.50, 5000, 150},
		{0.90, 9000, 150},
		{0.95, 9500, 150},
		{0.99, 9900, 150},
	}

	for _, tt := range tests {
		got := td.Quantile(tt.q)
		if math.Abs(got-tt.expected) > tt.tolerance {
			t.Errorf("Quantile(%.2f) = %.1f, want ~%.0f (±%.0f)", tt.q, got, tt.expected, tt.tolerance)
		}
	}
}

func TestTDigest_Gaussian_Percentiles(t *testing.T) {
	td := NewTDigest(200)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 100000; i++ {
		td.Add(rng.NormFloat64()*100 + 500)
	}

	// For N(500, 100): P50 ≈ 500, P90 ≈ 628, P99 ≈ 733
	p50 := td.Quantile(0.50)
	if math.Abs(p50-500) > 5 {
		t.Errorf("P50 = %.1f, want ~500", p50)
	}

	p90 := td.Quantile(0.90)
	if math.Abs(p90-628) > 10 {
		t.Errorf("P90 = %.1f, want ~628", p90)
	}

	p99 := td.Quantile(0.99)
	if math.Abs(p99-733) > 15 {
		t.Errorf("P99 = %.1f, want ~733", p99)
	}
}

func TestTDigest_Merge(t *testing.T) {
	td1 := NewTDigest(200)
	td2 := NewTDigest(200)

	for i := 1; i <= 500; i++ {
		td1.Add(float64(i))
	}
	for i := 501; i <= 1000; i++ {
		td2.Add(float64(i))
	}

	td1.Merge(td2)

	if td1.Count() != 1000 {
		t.Errorf("merged Count = %d, want 1000", td1.Count())
	}

	p50 := td1.Quantile(0.50)
	if math.Abs(p50-500) > 30 {
		t.Errorf("merged P50 = %.1f, want ~500", p50)
	}

	p99 := td1.Quantile(0.99)
	if math.Abs(p99-990) > 30 {
		t.Errorf("merged P99 = %.1f, want ~990", p99)
	}
}

func TestTDigest_Merge_Empty(t *testing.T) {
	td := NewTDigest(100)
	td.Add(1)
	td.Add(2)

	td.Merge(nil)
	td.Merge(NewTDigest(100))

	if td.Count() != 2 {
		t.Errorf("Count = %d after merging empty, want 2", td.Count())
	}
}

func TestTDigest_SnapshotRoundTripAndMerge(t *testing.T) {
	left := NewTDigest(200)
	right := NewTDigest(200)
	for i := 1; i <= 1000; i++ {
		left.Add(float64(i))
	}
	for i := 1001; i <= 2000; i++ {
		right.Add(float64(i))
	}

	roundTrip := NewTDigestFromSnapshot(left.Snapshot())
	if roundTrip.Count() != left.Count() {
		t.Fatalf("roundtrip count = %d, want %d", roundTrip.Count(), left.Count())
	}
	if math.Abs(roundTrip.Quantile(0.95)-left.Quantile(0.95)) > 0.001 {
		t.Fatalf("roundtrip p95 = %v, want %v", roundTrip.Quantile(0.95), left.Quantile(0.95))
	}

	roundTrip.Merge(NewTDigestFromSnapshot(right.Snapshot()))
	if roundTrip.Count() != 2000 {
		t.Fatalf("merged snapshot count = %d, want 2000", roundTrip.Count())
	}
	if p99 := roundTrip.Quantile(0.99); math.Abs(p99-1980) > 60 {
		t.Fatalf("merged snapshot p99 = %.1f, want about 1980", p99)
	}
}

func TestTDigest_Reset(t *testing.T) {
	td := NewTDigest(100)
	for i := 0; i < 100; i++ {
		td.Add(float64(i))
	}

	td.Reset()

	if td.Count() != 0 {
		t.Errorf("Count after reset = %d, want 0", td.Count())
	}
	if td.Quantile(0.5) != 0 {
		t.Errorf("Quantile after reset = %v, want 0", td.Quantile(0.5))
	}
}

func TestTDigest_DefaultCompression(t *testing.T) {
	td := NewTDigest(0)
	if td.compression != 200 {
		t.Errorf("default compression = %v, want 200", td.compression)
	}
}

func TestTDigest_BoundedMemory(t *testing.T) {
	td := NewTDigest(100)

	for i := 0; i < 1000000; i++ {
		td.Add(rand.Float64() * 10000)
	}

	td.flushBuffer()
	// With k1 scale function, centroid count is bounded at ~O(compression * log(n/compression))
	if len(td.centroids) > 2000 {
		t.Errorf("centroid count %d exceeds expected bound for compression=100 with 1M values", len(td.centroids))
	}
}

func TestTDigest_IdenticalValues(t *testing.T) {
	td := NewTDigest(100)
	for i := 0; i < 1000; i++ {
		td.Add(42)
	}

	got := td.Quantile(0.5)
	if got != 42 {
		t.Errorf("P50 of constant values = %v, want 42", got)
	}
	got = td.Quantile(0.99)
	if got != 42 {
		t.Errorf("P99 of constant values = %v, want 42", got)
	}
}

func TestTDigest_TwoValues(t *testing.T) {
	td := NewTDigest(100)
	td.Add(10)
	td.Add(20)

	p50 := td.Quantile(0.5)
	if p50 < 10 || p50 > 20 {
		t.Errorf("P50 of [10,20] = %v, should be between 10 and 20", p50)
	}
}

func TestTDigest_MonotonicQuantiles(t *testing.T) {
	td := NewTDigest(200)
	for i := 0; i < 10000; i++ {
		td.Add(rand.Float64() * 1000)
	}

	prev := td.Quantile(0)
	for q := 0.01; q <= 1.0; q += 0.01 {
		curr := td.Quantile(q)
		if curr < prev-0.001 {
			t.Fatalf("quantiles not monotonic: Q(%.2f)=%.3f < Q(%.2f)=%.3f", q, curr, q-0.01, prev)
		}
		prev = curr
	}
}

func BenchmarkTDigest_Add(b *testing.B) {
	td := NewTDigest(200)
	for i := 0; i < b.N; i++ {
		td.Add(float64(i))
	}
}

func BenchmarkTDigest_Quantile(b *testing.B) {
	td := NewTDigest(200)
	for i := 0; i < 100000; i++ {
		td.Add(rand.Float64() * 10000)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		td.Quantile(0.99)
	}
}

func BenchmarkTDigest_Merge(b *testing.B) {
	for i := 0; i < b.N; i++ {
		td1 := NewTDigest(200)
		td2 := NewTDigest(200)
		for j := 0; j < 1000; j++ {
			td1.Add(float64(j))
			td2.Add(float64(j + 1000))
		}
		td1.Merge(td2)
	}
}
