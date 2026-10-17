package engine

// DurationDistributionKind names the distribution a merged percentile comes
// from.
type DurationDistributionKind string

const (
	// DurationDistributionSketch is a merge of duration sketches: each
	// percentile is within DurationSketchRelativeAccuracy of its rank's value.
	DurationDistributionSketch DurationDistributionKind = "sketch"
	// DurationDistributionTDigest is a merge of t-digests, from evidence that
	// predates the sketch; its percentiles carry no error bound.
	DurationDistributionTDigest DurationDistributionKind = "tdigest"
)

// DurationDistributionMerge merges distributions that each describe a known
// number of durations. It merges their sketches while every part carries one
// that covers it, and otherwise their t-digests, so evidence recorded before
// the sketch still merges.
type DurationDistributionMerge struct {
	count              int64
	sketch             *DurationSketch
	digest             *TDigest
	sketched, digested bool
}

func NewDurationDistributionMerge() *DurationDistributionMerge {
	return &DurationDistributionMerge{sketch: NewDurationSketch(), sketched: true, digested: true}
}

// Add merges one part holding count durations and reports whether the part
// carried a distribution that covers them.
func (m *DurationDistributionMerge) Add(count int64, digest *TDigestSnapshot, sketch *DurationSketchSnapshot) bool {
	if count <= 0 {
		return true
	}
	m.count += count
	usable := false
	if part := DurationSketchForCount(sketch, count); part != nil {
		m.sketch.Merge(part)
		usable = true
	} else {
		m.sketched = false
	}
	if ValidateDurationDigest(digest, count) == nil {
		if m.digest == nil {
			m.digest = NewTDigest(digest.Compression)
		}
		m.digest.Merge(NewTDigestFromSnapshot(*digest))
		usable = true
	} else {
		m.digested = false
	}
	return usable
}

// Count is the number of durations merged.
func (m *DurationDistributionMerge) Count() int64 {
	if m == nil {
		return 0
	}
	return m.count
}

// Kind reports the distribution that covers every merged duration: the
// sketch when every part carried one, else the t-digest when every part
// carried one, else "".
func (m *DurationDistributionMerge) Kind() DurationDistributionKind {
	switch {
	case m == nil || m.count == 0:
		return ""
	case m.sketched && m.sketch.Count() == m.count:
		return DurationDistributionSketch
	case m.digested && m.digest != nil && m.digest.Count() == m.count:
		return DurationDistributionTDigest
	}
	return ""
}

// Quantile returns quantile q from the distribution Kind names, or 0 when no
// distribution covers every merged duration.
func (m *DurationDistributionMerge) Quantile(q float64) float64 {
	switch m.Kind() {
	case DurationDistributionSketch:
		return m.sketch.Quantile(q)
	case DurationDistributionTDigest:
		return m.digest.Quantile(q)
	}
	return 0
}

// Snapshots returns each merged form that covers every duration, or nil for
// a form some part lacked.
func (m *DurationDistributionMerge) Snapshots() (*TDigestSnapshot, *DurationSketchSnapshot) {
	var digest *TDigestSnapshot
	var sketch *DurationSketchSnapshot
	if m == nil || m.count == 0 {
		return nil, nil
	}
	if m.digested && m.digest != nil && m.digest.Count() == m.count {
		snapshot := m.digest.Snapshot()
		digest = &snapshot
	}
	if m.sketched && m.sketch.Count() == m.count {
		snapshot := m.sketch.Snapshot()
		sketch = &snapshot
	}
	return digest, sketch
}
