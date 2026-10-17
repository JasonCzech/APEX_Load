package engine

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// sketchBracketError reports how far a sketch quantile lies outside the
// relative-accuracy band around the two order statistics that bracket rank
// q*(n-1); zero means the sketch met its guarantee.
func sketchBracketError(sorted []float64, q, got float64) float64 {
	rank := q * float64(len(sorted)-1)
	lo, hi := sorted[int(math.Floor(rank))], sorted[int(math.Ceil(rank))]
	const slack = 1e-9
	low, high := lo*(1-DurationSketchRelativeAccuracy-slack), hi*(1+DurationSketchRelativeAccuracy+slack)
	switch {
	case got < low:
		return (low - got) / math.Max(lo, 1e-12)
	case got > high:
		return (got - high) / math.Max(hi, 1e-12)
	}
	return 0
}

type sketchDistribution struct {
	name string
	draw func(*rand.Rand) float64
}

func sketchDistributions() []sketchDistribution {
	return []sketchDistribution{
		{"lognormal", func(r *rand.Rand) float64 { return math.Exp(math.Log(120) + 0.6*r.NormFloat64()) }},
		{"bimodal 95/5", func(r *rand.Rand) float64 {
			if r.Float64() < 0.05 {
				return math.Max(1, 800+80*r.NormFloat64())
			}
			return math.Max(1, 50+5*r.NormFloat64())
		}},
		{"1% timeouts at 30 s", func(r *rand.Rand) float64 {
			if r.Float64() < 0.01 {
				return 30000 + r.Float64()*50
			}
			return math.Exp(math.Log(120) + 0.3*r.NormFloat64())
		}},
		{"uniform", func(r *rand.Rand) float64 { return 1 + 999*r.Float64() }},
		{"millisecond ties", func(r *rand.Rand) float64 { return float64(1 + r.Intn(20)) }},
	}
}

func TestDurationSketchQuantilesStayWithinRelativeAccuracy(t *testing.T) {
	sizes := []int{1_000, 100_000, 1_000_000}
	if testing.Short() {
		sizes = sizes[:2]
	}
	for _, distribution := range sketchDistributions() {
		for _, n := range sizes {
			r := rand.New(rand.NewSource(int64(n)))
			sketch := NewDurationSketch()
			values := make([]float64, n)
			for i := range values {
				values[i] = distribution.draw(r)
				sketch.Add(values[i])
			}
			sort.Float64s(values)
			for _, q := range []float64{0.5, 0.9, 0.95, 0.99, 0.999} {
				if err := sketchBracketError(values, q, sketch.Quantile(q)); err > 0 {
					t.Errorf("%s n=%d p%v = %v, outside the accuracy band by %.3g", distribution.name, n, q*100, sketch.Quantile(q), err)
				}
			}
			if sketch.Count() != int64(n) || sketch.Min() != values[0] || sketch.Max() != values[n-1] {
				t.Errorf("%s n=%d: count/min/max = %d/%v/%v", distribution.name, n, sketch.Count(), sketch.Min(), sketch.Max())
			}
		}
	}
}

func TestDurationSketchHasNoModeBoundaryError(t *testing.T) {
	// The share of 30-second timeouts sweeps across p99's tail, where the
	// t-digest interpolates into the empty gap between the modes.
	const runs, samplesPerRun = 200, 60_000
	digestMisses := 0
	for run := 0; run < runs; run++ {
		r := rand.New(rand.NewSource(int64(5000 + run)))
		share := 0.0095 + 0.001*float64(run)/runs
		sketch, digest := NewDurationSketch(), NewTDigest(200)
		values := make([]float64, samplesPerRun)
		for i := range values {
			value := math.Exp(math.Log(120) + 0.3*r.NormFloat64())
			if r.Float64() < share {
				value = 30000 + r.Float64()*50
			}
			values[i] = value
			sketch.Add(value)
			digest.Add(value)
		}
		sort.Float64s(values)
		if err := sketchBracketError(values, 0.99, sketch.Quantile(0.99)); err > 0.01 {
			t.Fatalf("run %d (timeouts %.3f%%): sketch p99 %v is %.1f%% outside its band", run, 100*share, sketch.Quantile(0.99), 100*err)
		}
		if sketchBracketError(values, 0.99, digest.Quantile(0.99)) > 0.01 {
			digestMisses++
		}
	}
	t.Logf("t-digest (compression 200) missed p99 by more than 1%% in %d of %d runs; the sketch in none", digestMisses, runs)
}

func TestDurationSketchMergeIsExactAndOrderIndependent(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	whole := NewDurationSketch()
	parts := make([]*DurationSketch, 10)
	for generator := range parts {
		parts[generator] = NewDurationSketch()
		scale := 1.0
		if generator == 0 {
			scale = 10 // one slow generator
		}
		for i := 0; i < 20_000; i++ {
			value := scale * math.Exp(math.Log(80)+0.5*r.NormFloat64())
			parts[generator].Add(value)
			whole.Add(value)
		}
	}
	forward, reverse, restored := NewDurationSketch(), NewDurationSketch(), NewDurationSketch()
	for i := range parts {
		forward.Merge(parts[i])
		reverse.Merge(parts[len(parts)-1-i])
		encoded, err := json.Marshal(parts[i].Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		var snapshot DurationSketchSnapshot
		if err := json.Unmarshal(encoded, &snapshot); err != nil {
			t.Fatal(err)
		}
		decoded, err := DurationSketchFromSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		restored.Merge(decoded)
	}
	for _, q := range []float64{0.5, 0.9, 0.95, 0.99, 0.999} {
		want := whole.Quantile(q)
		for name, merged := range map[string]*DurationSketch{"forward": forward, "reverse": reverse, "JSON round trip": restored} {
			if got := merged.Quantile(q); got != want {
				t.Fatalf("%s merge p%v = %v, want the single sketch's %v", name, q*100, got, want)
			}
		}
	}
	if forward.Count() != whole.Count() || math.Abs(forward.Sum()-whole.Sum()) > 1e-6*whole.Sum() {
		t.Fatalf("merged count/sum = %d/%v, want %d/%v", forward.Count(), forward.Sum(), whole.Count(), whole.Sum())
	}
}

func TestDurationSketchStaysBoundedAtTenMillionSamples(t *testing.T) {
	if testing.Short() {
		t.Skip("ten million samples")
	}
	r := rand.New(rand.NewSource(3))
	sketch := NewDurationSketch()
	const n = 10_000_000
	for i := 0; i < n; i++ {
		sketch.Add(math.Exp(math.Log(150) + 0.8*r.NormFloat64()))
	}
	snapshot := sketch.Snapshot()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if sketch.Count() != n || snapshot.Count != n || len(snapshot.Bins) > DurationSketchMaxBuckets || cap(sketch.bins) > DurationSketchMaxBuckets {
		t.Fatalf("count %d, %d buckets, capacity %d", sketch.Count(), len(snapshot.Bins), cap(sketch.bins))
	}
	if len(encoded) > 64<<10 {
		t.Fatalf("snapshot is %d bytes", len(encoded))
	}
	if _, err := DurationSketchFromSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestDurationSketchFoldsOnlyItsLowestBucketsWhenTheRangeIsHuge(t *testing.T) {
	values := []float64{1e-6, 1e-3, 1, 1e12, 2e12, 3e12}
	sketch := NewDurationSketch()
	for _, value := range values {
		sketch.Add(value)
	}
	if !sketch.Collapsed() || len(sketch.bins) > DurationSketchMaxBuckets || cap(sketch.bins) > DurationSketchMaxBuckets {
		t.Fatalf("collapsed=%v buckets=%d capacity=%d", sketch.Collapsed(), len(sketch.bins), cap(sketch.bins))
	}
	if got := sketch.Quantile(1); got != 3e12 {
		t.Fatalf("max = %v", got)
	}
	// High quantiles keep their accuracy; only the folded low end coarsens.
	for _, q := range []float64{0.7, 0.9} {
		if err := sketchBracketError(values, q, sketch.Quantile(q)); err > 0 {
			t.Fatalf("p%v = %v, outside the accuracy band by %.3g", q*100, sketch.Quantile(q), err)
		}
	}
	if sketch.Count() != 6 || sketch.Min() != 1e-6 {
		t.Fatalf("count %d min %v", sketch.Count(), sketch.Min())
	}
	// A value far above every existing bucket folds the whole old range.
	jump := NewDurationSketch()
	jump.Add(1e-6)
	jump.Add(1e30)
	if !jump.Collapsed() || jump.Count() != 2 || jump.Quantile(1) != 1e30 {
		t.Fatalf("jump: collapsed=%v count=%d max=%v", jump.Collapsed(), jump.Count(), jump.Quantile(1))
	}
}

func TestDurationSketchEdgeValues(t *testing.T) {
	sketch := NewDurationSketch()
	if sketch.Quantile(0.5) != 0 || sketch.Count() != 0 {
		t.Fatal("empty sketch must report zero")
	}
	for _, value := range []float64{0, -5, math.NaN(), math.Inf(1), 0, 10} {
		sketch.Add(value)
	}
	if sketch.Count() != 4 || sketch.Min() != 0 || sketch.Max() != 10 || sketch.Quantile(0.5) != 0 {
		t.Fatalf("count=%d min=%v max=%v p50=%v", sketch.Count(), sketch.Min(), sketch.Max(), sketch.Quantile(0.5))
	}
}

func TestDurationSketchSnapshotValidation(t *testing.T) {
	sketch := NewDurationSketch()
	for _, value := range []float64{0, 1, 2, 3, 400} {
		sketch.Add(value)
	}
	valid := sketch.Snapshot()
	if restored, err := DurationSketchFromSnapshot(valid); err != nil || restored.Quantile(0.5) != sketch.Quantile(0.5) {
		t.Fatalf("round trip: %v", err)
	}
	for name, mutate := range map[string]func(*DurationSketchSnapshot){
		"t-digest version":  func(s *DurationSketchSnapshot) { s.Version = 1 },
		"other accuracy":    func(s *DurationSketchSnapshot) { s.Alpha = 0.01 },
		"count mismatch":    func(s *DurationSketchSnapshot) { s.Count++ },
		"empty bucket":      func(s *DurationSketchSnapshot) { s.Bins[0].Count = 0 },
		"unordered buckets": func(s *DurationSketchSnapshot) { s.Bins[0], s.Bins[1] = s.Bins[1], s.Bins[0] },
		"span too wide": func(s *DurationSketchSnapshot) {
			s.Bins[len(s.Bins)-1].Index = s.Bins[0].Index + DurationSketchMaxBuckets
		},
		"min above max":      func(s *DurationSketchSnapshot) { s.Min = s.Max + 1 },
		"negative sum":       func(s *DurationSketchSnapshot) { s.Sum = -1 },
		"empty with buckets": func(s *DurationSketchSnapshot) { s.Count, s.ZeroCount = 0, 0 },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := valid
			snapshot.Bins = append([]DurationSketchBin(nil), valid.Bins...)
			mutate(&snapshot)
			if _, err := DurationSketchFromSnapshot(snapshot); !errors.Is(err, ErrInvalidDurationSketch) {
				t.Fatalf("error = %v, want an invalid-sketch error", err)
			}
		})
	}
}

func BenchmarkDurationSketchAdd(b *testing.B) {
	values := benchmarkDurations(4096)
	sketch := NewDurationSketch()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sketch.Add(values[i&4095])
	}
}

func BenchmarkTDigestAddForComparison(b *testing.B) {
	values := benchmarkDurations(4096)
	digest := NewTDigest(100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		digest.Add(values[i&4095])
	}
}

func benchmarkDurations(n int) []float64 {
	r := rand.New(rand.NewSource(1))
	values := make([]float64, n)
	for i := range values {
		values[i] = math.Exp(math.Log(120) + 0.6*r.NormFloat64())
	}
	return values
}
