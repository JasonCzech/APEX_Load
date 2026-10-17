package engine

import (
	"errors"
	"fmt"
	"math"
)

// DurationSketch is a mergeable quantile sketch for durations with a bounded
// relative error (DDSketch-style logarithmic buckets). Every quantile it
// reports interpolates, as an exact percentile does, between two order
// statistics that are each within DurationSketchRelativeAccuracy, whatever
// the distribution, so a slow mode near a percentile's tail cannot be reported
// as a value from the empty gap between modes. Count, sum, minimum
// and maximum are exact. Merging adds bucket counts, so the result does not
// depend on the order in which sketches are merged.
//
// A sketch keeps at most DurationSketchMaxBuckets buckets. A distribution
// spanning a wider range folds its lowest buckets together (Collapsed reports
// it), which affects only the smallest values. A sketch is not safe for
// concurrent use.
type DurationSketch struct {
	bins      []uint64 // bins[i] counts bucket offset+i
	offset    int
	count     uint64
	zero      uint64 // values at or below durationSketchMinIndexable
	sum       float64
	min       float64
	max       float64
	collapsed bool
}

const (
	// DurationSketchRelativeAccuracy bounds the relative error of every
	// reported quantile.
	DurationSketchRelativeAccuracy = 0.005
	// DurationSketchMaxBuckets bounds a sketch's memory: 32 KiB of counts.
	DurationSketchMaxBuckets = 4096
	// DurationSketchSnapshotVersion identifies the snapshot layout.
	DurationSketchSnapshotVersion = 2

	durationSketchMinIndexable = 1e-9
)

var (
	durationSketchGamma    = (1 + DurationSketchRelativeAccuracy) / (1 - DurationSketchRelativeAccuracy)
	durationSketchLogGamma = math.Log(durationSketchGamma)
)

// NewDurationSketch returns an empty sketch.
func NewDurationSketch() *DurationSketch {
	return &DurationSketch{min: math.Inf(1), max: math.Inf(-1)}
}

// durationSketchIndex returns the bucket holding v: bucket i covers
// (gamma^(i-1), gamma^i].
func durationSketchIndex(v float64) int {
	return int(math.Ceil(math.Log(v) / durationSketchLogGamma))
}

// durationSketchValue is the value reported for bucket i, within the relative
// accuracy of every value the bucket holds.
func durationSketchValue(index int) float64 {
	return 2 * math.Pow(durationSketchGamma, float64(index)) / (1 + durationSketchGamma)
}

// Add records one duration. Negative values count as zero; NaN and infinite
// values are ignored.
func (s *DurationSketch) Add(v float64) {
	if s == nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	if v < 0 {
		v = 0
	}
	s.count++
	s.sum += v
	if v < s.min {
		s.min = v
	}
	if v > s.max {
		s.max = v
	}
	if v <= durationSketchMinIndexable {
		s.zero++
		return
	}
	s.addToBucket(durationSketchIndex(v), 1)
}

// addToBucket adds n to bucket index, growing the dense range and folding the
// lowest buckets together when the range would exceed the bucket limit. The
// dense range never holds more than DurationSketchMaxBuckets counts.
func (s *DurationSketch) addToBucket(index int, n uint64) {
	if len(s.bins) == 0 {
		s.offset = index
		s.bins = append(s.bins[:0], n)
		return
	}
	if index < s.offset {
		top := s.offset + len(s.bins) - 1
		if lowest := max(index, top-DurationSketchMaxBuckets+1); lowest < s.offset {
			grown := make([]uint64, s.offset-lowest, s.offset-lowest+len(s.bins))
			s.bins = append(grown, s.bins...)
			s.offset = lowest
		}
		if index < s.offset {
			index = s.offset
			s.collapsed = true
		}
		s.bins[index-s.offset] += n
		return
	}
	if lowest := index - DurationSketchMaxBuckets + 1; lowest > s.offset {
		s.foldBelow(lowest)
	}
	if need := index - s.offset + 1; need > len(s.bins) {
		if need > cap(s.bins) {
			grown := make([]uint64, len(s.bins), min(max(need, 2*cap(s.bins)), DurationSketchMaxBuckets))
			copy(grown, s.bins)
			s.bins = grown
		}
		s.bins = s.bins[:need]
	}
	s.bins[index-s.offset] += n
}

// foldBelow folds every bucket below lowest into bucket lowest, which becomes
// the first bucket of the range.
func (s *DurationSketch) foldBelow(lowest int) {
	foldCount := min(lowest-s.offset, len(s.bins))
	folded := uint64(0)
	for _, n := range s.bins[:foldCount] {
		folded += n
	}
	remaining := copy(s.bins, s.bins[foldCount:])
	s.bins = s.bins[:max(remaining, 1)]
	if remaining == 0 {
		s.bins[0] = 0
	}
	s.offset = lowest
	if folded > 0 {
		s.bins[0] += folded
		s.collapsed = true
	}
}

// Count returns how many values were added.
func (s *DurationSketch) Count() int64 {
	if s == nil {
		return 0
	}
	return int64(s.count)
}

// Sum returns the exact sum of the added values.
func (s *DurationSketch) Sum() float64 {
	if s == nil {
		return 0
	}
	return s.sum
}

// Min and Max return the exact extremes, or 0 for an empty sketch.
func (s *DurationSketch) Min() float64 {
	if s == nil || s.count == 0 {
		return 0
	}
	return s.min
}

func (s *DurationSketch) Max() float64 {
	if s == nil || s.count == 0 {
		return 0
	}
	return s.max
}

// Collapsed reports whether the lowest buckets were folded together to stay
// within the bucket limit, which coarsens only the smallest values.
func (s *DurationSketch) Collapsed() bool {
	return s != nil && s.collapsed
}

// Quantile returns the value at quantile q (0 to 1). Like an exact percentile
// it interpolates linearly between the two values bracketing rank q*(count-1);
// each of those is within the relative accuracy, and the first and last ranks
// are the exact minimum and maximum.
func (s *DurationSketch) Quantile(q float64) float64 {
	if s == nil || s.count == 0 {
		return 0
	}
	if math.IsNaN(q) || q <= 0 {
		return s.min
	}
	if q >= 1 {
		return s.max
	}
	rank := q * float64(s.count-1)
	lower := math.Floor(rank)
	low := s.valueAtRank(uint64(lower))
	if fraction := rank - lower; fraction > 0 {
		return low + fraction*(s.valueAtRank(uint64(lower)+1)-low)
	}
	return low
}

// valueAtRank returns the rank-th smallest value (0-based) within the relative
// accuracy.
func (s *DurationSketch) valueAtRank(rank uint64) float64 {
	switch {
	case rank == 0:
		return s.min
	case rank >= s.count-1:
		return s.max
	}
	seen := s.zero
	if seen > rank {
		return s.min
	}
	for i, n := range s.bins {
		seen += n
		if seen > rank {
			return math.Min(s.max, math.Max(s.min, durationSketchValue(s.offset+i)))
		}
	}
	return s.max
}

// Merge adds other's values to s. It is exact: merging in any order gives the
// same sketch.
func (s *DurationSketch) Merge(other *DurationSketch) {
	if s == nil || other == nil || other.count == 0 {
		return
	}
	s.count += other.count
	s.zero += other.zero
	s.sum += other.sum
	s.min = math.Min(s.min, other.min)
	s.max = math.Max(s.max, other.max)
	s.collapsed = s.collapsed || other.collapsed
	for i, n := range other.bins {
		if n > 0 {
			s.addToBucket(other.offset+i, n)
		}
	}
}

// Clone returns an independent copy.
func (s *DurationSketch) Clone() *DurationSketch {
	if s == nil {
		return nil
	}
	clone := *s
	clone.bins = append([]uint64(nil), s.bins...)
	return &clone
}

// DurationSketchBin is one non-empty bucket in a snapshot.
type DurationSketchBin struct {
	Index int    `json:"index"`
	Count uint64 `json:"count"`
}

// DurationSketchSnapshot is the portable form of a DurationSketch (version 2
// of the duration distribution format; version 1 is the t-digest).
type DurationSketchSnapshot struct {
	Version   int                 `json:"version"`
	Alpha     float64             `json:"alpha"`
	Count     uint64              `json:"count"`
	Sum       float64             `json:"sum"`
	Min       float64             `json:"min"`
	Max       float64             `json:"max"`
	ZeroCount uint64              `json:"zero_count"`
	Collapsed bool                `json:"collapsed,omitempty"`
	Bins      []DurationSketchBin `json:"bins"`
}

// Snapshot returns the sketch's portable form, listing non-empty buckets in
// index order.
func (s *DurationSketch) Snapshot() DurationSketchSnapshot {
	snapshot := DurationSketchSnapshot{Version: DurationSketchSnapshotVersion, Alpha: DurationSketchRelativeAccuracy, Bins: []DurationSketchBin{}}
	if s == nil || s.count == 0 {
		return snapshot
	}
	snapshot.Count, snapshot.Sum, snapshot.Min, snapshot.Max = s.count, s.sum, s.min, s.max
	snapshot.ZeroCount, snapshot.Collapsed = s.zero, s.collapsed
	for i, n := range s.bins {
		if n > 0 {
			snapshot.Bins = append(snapshot.Bins, DurationSketchBin{Index: s.offset + i, Count: n})
		}
	}
	return snapshot
}

// ErrInvalidDurationSketch marks a snapshot DurationSketchFromSnapshot refuses.
var ErrInvalidDurationSketch = errors.New("invalid duration sketch")

// DurationSketchFromSnapshot restores a sketch, refusing a snapshot that is
// malformed, from another format or accuracy, or wider than the bucket limit.
func DurationSketchFromSnapshot(snapshot DurationSketchSnapshot) (*DurationSketch, error) {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidDurationSketch, fmt.Sprintf(format, args...))
	}
	if snapshot.Version != DurationSketchSnapshotVersion {
		return nil, invalid("version %d, want %d", snapshot.Version, DurationSketchSnapshotVersion)
	}
	if snapshot.Alpha != DurationSketchRelativeAccuracy {
		return nil, invalid("relative accuracy %v, want %v", snapshot.Alpha, DurationSketchRelativeAccuracy)
	}
	sketch := NewDurationSketch()
	if snapshot.Count == 0 {
		if snapshot.ZeroCount != 0 || len(snapshot.Bins) != 0 || snapshot.Sum != 0 {
			return nil, invalid("empty sketch carries values")
		}
		return sketch, nil
	}
	for _, value := range []float64{snapshot.Sum, snapshot.Min, snapshot.Max} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, invalid("sum, min and max must be finite and non-negative")
		}
	}
	if snapshot.Min > snapshot.Max {
		return nil, invalid("min %v exceeds max %v", snapshot.Min, snapshot.Max)
	}
	if len(snapshot.Bins) > DurationSketchMaxBuckets {
		return nil, invalid("%d buckets exceed the limit of %d", len(snapshot.Bins), DurationSketchMaxBuckets)
	}
	total := snapshot.ZeroCount
	for i, bin := range snapshot.Bins {
		if bin.Count == 0 {
			return nil, invalid("bucket %d is empty", bin.Index)
		}
		if i > 0 && bin.Index <= snapshot.Bins[i-1].Index {
			return nil, invalid("buckets are not in increasing index order")
		}
		if total+bin.Count < total {
			return nil, invalid("bucket counts overflow")
		}
		total += bin.Count
	}
	if total != snapshot.Count {
		return nil, invalid("bucket counts total %d, want %d", total, snapshot.Count)
	}
	if n := len(snapshot.Bins); n > 0 && snapshot.Bins[n-1].Index-snapshot.Bins[0].Index >= DurationSketchMaxBuckets {
		return nil, invalid("buckets span more than %d indexes", DurationSketchMaxBuckets)
	}
	sketch.count, sketch.zero, sketch.sum = snapshot.Count, snapshot.ZeroCount, snapshot.Sum
	sketch.min, sketch.max, sketch.collapsed = snapshot.Min, snapshot.Max, snapshot.Collapsed
	for _, bin := range snapshot.Bins {
		sketch.addToBucket(bin.Index, bin.Count)
	}
	return sketch, nil
}

// durationDistribution records one window's durations in both forms for a
// release: the sketch, which reports percentiles, and the t-digest that
// controllers predating the sketch still merge.
type durationDistribution struct {
	digest *TDigest
	sketch *DurationSketch
}

func newDurationDistribution() *durationDistribution {
	return &durationDistribution{digest: NewTDigest(200), sketch: NewDurationSketch()}
}

func (d *durationDistribution) Add(v float64) {
	d.digest.Add(v)
	d.sketch.Add(v)
}

func (d *durationDistribution) Quantile(q float64) float64 {
	return d.sketch.Quantile(q)
}

// snapshots returns both portable forms.
func (d *durationDistribution) snapshots() (*TDigestSnapshot, *DurationSketchSnapshot) {
	digest, sketch := d.digest.Snapshot(), d.sketch.Snapshot()
	return &digest, &sketch
}

// CloneDurationSketchSnapshot returns an independent copy, or nil.
func CloneDurationSketchSnapshot(in *DurationSketchSnapshot) *DurationSketchSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	out.Bins = append([]DurationSketchBin(nil), in.Bins...)
	return &out
}

// DurationSketchForCount restores a peer's or a persisted sketch when it is
// valid and holds exactly count durations, and returns nil otherwise, so a
// caller merges only distributions that describe every observation.
func DurationSketchForCount(snapshot *DurationSketchSnapshot, count int64) *DurationSketch {
	if snapshot == nil || count <= 0 {
		return nil
	}
	sketch, err := DurationSketchFromSnapshot(*snapshot)
	if err != nil || sketch.Count() != count {
		return nil
	}
	return sketch
}
