package engine

import (
	"math/rand"
	"testing"
)

type distributionPart struct {
	count  int64
	digest *TDigestSnapshot
	sketch *DurationSketchSnapshot
}

func distributionParts(rng *rand.Rand, n, size int) ([]distributionPart, *DurationSketch, *TDigest) {
	parts := make([]distributionPart, n)
	whole, digests := NewDurationSketch(), NewTDigest(200)
	for i := range parts {
		digest, sketch := NewTDigest(200), NewDurationSketch()
		for j := 0; j < size; j++ {
			v := 1e6 * (1 + 500*rng.Float64())
			digest.Add(v)
			sketch.Add(v)
			whole.Add(v)
		}
		digestSnapshot, sketchSnapshot := digest.Snapshot(), sketch.Snapshot()
		parts[i] = distributionPart{count: int64(size), digest: &digestSnapshot, sketch: &sketchSnapshot}
		digests.Merge(NewTDigestFromSnapshot(digestSnapshot))
	}
	return parts, whole, digests
}

func mergeDistributionParts(parts []distributionPart) (*DurationDistributionMerge, bool) {
	merge := NewDurationDistributionMerge()
	usable := true
	for _, part := range parts {
		usable = merge.Add(part.count, part.digest, part.sketch) && usable
	}
	return merge, usable
}

func TestDurationDistributionMergePrefersSketches(t *testing.T) {
	parts, whole, digests := distributionParts(rand.New(rand.NewSource(3)), 4, 500)

	t.Run("every part carries a sketch", func(t *testing.T) {
		merge, usable := mergeDistributionParts(parts)
		if !usable || merge.Kind() != DurationDistributionSketch || merge.Count() != whole.Count() {
			t.Fatalf("usable %v, kind %q, count %d", usable, merge.Kind(), merge.Count())
		}
		for _, q := range []float64{0, 0.5, 0.99, 1} {
			if got, want := merge.Quantile(q), whole.Quantile(q); got != want {
				t.Fatalf("q%v = %v, one sketch of every value %v", q, got, want)
			}
		}
		if digest, sketch := merge.Snapshots(); digest == nil || sketch == nil || int64(sketch.Count) != whole.Count() || digest.Count != whole.Count() {
			t.Fatalf("snapshots digest %v sketch %v", digest != nil, sketch != nil)
		}
	})

	t.Run("a part recorded before the sketch", func(t *testing.T) {
		older := append([]distributionPart(nil), parts...)
		older[2].sketch = nil
		merge, usable := mergeDistributionParts(older)
		if !usable || merge.Kind() != DurationDistributionTDigest || merge.Quantile(0.99) != digests.Quantile(0.99) {
			t.Fatalf("usable %v, kind %q, p99 %v; want the t-digests' %v", usable, merge.Kind(), merge.Quantile(0.99), digests.Quantile(0.99))
		}
		if digest, sketch := merge.Snapshots(); digest == nil || sketch != nil {
			t.Fatalf("snapshots digest %v sketch %v; want the t-digest alone", digest != nil, sketch != nil)
		}
	})

	t.Run("a part with neither", func(t *testing.T) {
		missing := append([]distributionPart(nil), parts...)
		missing[1].sketch, missing[1].digest = nil, nil
		merge, usable := mergeDistributionParts(missing)
		if usable || merge.Kind() != "" || merge.Quantile(0.99) != 0 {
			t.Fatalf("usable %v, kind %q, p99 %v", usable, merge.Kind(), merge.Quantile(0.99))
		}
		if digest, sketch := merge.Snapshots(); digest != nil || sketch != nil {
			t.Fatal("snapshots cover only some of the durations")
		}
	})

	t.Run("one part has only a sketch and another only a t-digest", func(t *testing.T) {
		mixed := append([]distributionPart(nil), parts...)
		mixed[0].digest, mixed[3].sketch = nil, nil
		merge, usable := mergeDistributionParts(mixed)
		if !usable || merge.Kind() != "" {
			t.Fatalf("usable %v, kind %q; neither form covers every duration", usable, merge.Kind())
		}
	})

	t.Run("a distribution of other durations", func(t *testing.T) {
		wrong := append([]distributionPart(nil), parts...)
		wrong[0].count++
		merge, usable := mergeDistributionParts(wrong)
		if usable || merge.Kind() != "" {
			t.Fatalf("usable %v, kind %q for distributions that miss one duration", usable, merge.Kind())
		}
	})

	t.Run("empty parts", func(t *testing.T) {
		merge := NewDurationDistributionMerge()
		if !merge.Add(0, nil, nil) || merge.Count() != 0 || merge.Kind() != "" {
			t.Fatalf("an empty part changed the merge: count %d, kind %q", merge.Count(), merge.Kind())
		}
	})
}
