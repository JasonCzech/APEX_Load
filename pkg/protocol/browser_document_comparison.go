package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
)

// BrowserDocumentComparison compares one final-document metric tuple of a
// run with a baseline run. Distributions are compared only when every field
// that decides what was measured matches: algorithm (and its digest), unit,
// lifecycle phase, frame scope, engine, device profile and runtime cohort,
// within the same group and region. Otherwise Reason names the first
// difference and no values are compared.
type BrowserDocumentComparison struct {
	// BaselineID names the baseline run when a caller compares one run with
	// several.
	BaselineID     string `json:"baseline_id,omitempty"`
	Metric         string `json:"metric"`
	Unit           string `json:"unit"`
	Group          string `json:"group,omitempty"`
	Region         string `json:"region,omitempty"`
	FrameScope     string `json:"frame_scope"`
	LifecyclePhase string `json:"lifecycle_phase"`
	Engine         string `json:"engine"`
	DeviceProfile  string `json:"device_profile"`
	Comparable     bool   `json:"comparable"`
	// Reason is empty when comparable: algorithm_mismatch, unit_mismatch,
	// lifecycle_phase_mismatch, frame_scope_mismatch, engine_mismatch,
	// device_profile_mismatch, cohort_mismatch, baseline_unmatched,
	// current_unmatched or insufficient_final_documents.
	Reason        string   `json:"reason,omitempty"`
	CurrentFinal  int64    `json:"current_final"`
	BaselineFinal int64    `json:"baseline_final"`
	CurrentP75    *float64 `json:"current_p75,omitempty"`
	BaselineP75   *float64 `json:"baseline_p75,omitempty"`
	DeltaP75      *float64 `json:"delta_p75,omitempty"`
}

// Quantile is the approximate q-quantile of a final-document distribution:
// the upper bound of the bucket holding it, capped by the observed maximum.
// An empty or invalid distribution has none.
func (d BrowserMetricDistribution) Quantile(q float64) (float64, bool) {
	if d.Count <= 0 || !d.Valid() || q < 0 || q > 1 || math.IsNaN(q) {
		return 0, false
	}
	rank := int64(math.Ceil(q * float64(d.Count)))
	rank = max(rank, 1)
	var seen int64
	for i, n := range d.Buckets {
		seen += n
		if seen >= rank {
			upper := 0.000001 * math.Pow(1.2, float64(i))
			return math.Max(d.Min, math.Min(upper, d.Max)), true
		}
	}
	return d.Max, true
}

// CompareBrowserDocumentSummaries pairs the two runs' final-document metric
// tuples (never progress snapshots: summaries count one eligible final
// observation per document lifecycle metric).
func CompareBrowserDocumentSummaries(current, baseline *BrowserDocumentSummary) []BrowserDocumentComparison {
	if current == nil && baseline == nil {
		return nil
	}
	var currentMetrics, baselineMetrics []BrowserDocumentMetricSummary
	if current != nil {
		currentMetrics = current.Metrics
	}
	if baseline != nil {
		baselineMetrics = baseline.Metrics
	}
	byKey := map[string]BrowserDocumentMetricSummary{}
	for _, m := range baselineMetrics {
		byKey[BrowserDocumentMetricKey(m)] = m
	}
	// used are baseline tuples compared exactly; paired are baseline tuples
	// already reported as a current tuple's mismatch.
	used, paired := map[string]bool{}, map[string]bool{}
	var out []BrowserDocumentComparison
	for _, m := range currentMetrics {
		row := browserDocumentComparisonRow(m)
		row.CurrentFinal = m.EligibleFinal
		key := BrowserDocumentMetricKey(m)
		if b, ok := byKey[key]; ok {
			used[key] = true
			row.BaselineFinal = b.EligibleFinal
			currentP75, currentOK := m.Distribution.Quantile(0.75)
			baselineP75, baselineOK := b.Distribution.Quantile(0.75)
			if !currentOK || !baselineOK {
				row.Reason = "insufficient_final_documents"
			} else {
				delta := currentP75 - baselineP75
				row.Comparable, row.CurrentP75, row.BaselineP75, row.DeltaP75 = true, &currentP75, &baselineP75, &delta
			}
			out = append(out, row)
			continue
		}
		row.Reason = "baseline_unmatched"
		if b, reason, found := closestBrowserDocumentTuple(m, baselineMetrics, used); found {
			row.Reason, row.BaselineFinal = reason, b.EligibleFinal
			paired[BrowserDocumentMetricKey(b)] = true
		}
		out = append(out, row)
	}
	for _, b := range baselineMetrics {
		key := BrowserDocumentMetricKey(b)
		if used[key] || paired[key] {
			continue
		}
		row := browserDocumentComparisonRow(b)
		row.Reason, row.BaselineFinal = "current_unmatched", b.EligibleFinal
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		if a.FrameScope != b.FrameScope {
			return a.FrameScope < b.FrameScope
		}
		return a.LifecyclePhase < b.LifecyclePhase
	})
	return out
}

// BrowserDocumentObservationTuple is the metric tuple an observation counts
// toward: every field that decides what was measured, and no counts.
func BrowserDocumentObservationTuple(o BrowserDocumentObservation) BrowserDocumentMetricSummary {
	return BrowserDocumentMetricSummary{Metric: o.Metric, Algorithm: o.Algorithm, AlgorithmSHA256: o.AlgorithmSHA256, Unit: o.Unit, Engine: o.Engine, DeviceProfile: o.DeviceProfile, Cohort: o.Cohort.Clone(), Group: o.Group, Region: o.Region, FrameScope: o.FrameScope, LifecyclePhase: o.LifecyclePhase}
}

// BrowserRuntimeCohortDigest names an actual runtime cohort compactly (the
// first 16 hex digits of its JSON's SHA-256), so a query can select one
// cohort; an unavailable cohort is "unavailable".
func BrowserRuntimeCohortDigest(c *BrowserRuntimeCohort) string {
	if c == nil {
		return "unavailable"
	}
	body, _ := json.Marshal(c)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8])
}

// BrowserDocumentTupleMismatch is the first field on which two tuples of the
// same metric measured different things (the reasons of
// BrowserDocumentComparison), then group_mismatch or region_mismatch; empty
// when they are the same tuple.
func BrowserDocumentTupleMismatch(a, b BrowserDocumentMetricSummary) string {
	if a.Metric != b.Metric {
		return "metric_mismatch"
	}
	for _, field := range browserDocumentFields {
		if field.value(a) != field.value(b) {
			return field.reason
		}
	}
	if a.Group != b.Group {
		return "group_mismatch"
	}
	if a.Region != b.Region {
		return "region_mismatch"
	}
	return ""
}

func browserDocumentComparisonRow(m BrowserDocumentMetricSummary) BrowserDocumentComparison {
	return BrowserDocumentComparison{Metric: m.Metric, Unit: m.Unit, Group: m.Group, Region: m.Region, FrameScope: m.FrameScope, LifecyclePhase: m.LifecyclePhase, Engine: m.Engine, DeviceProfile: m.DeviceProfile}
}

// browserDocumentFields are the fields that decide what a tuple measured, in
// the order a mismatch is reported.
var browserDocumentFields = []struct {
	reason string
	value  func(BrowserDocumentMetricSummary) string
}{
	{"algorithm_mismatch", func(m BrowserDocumentMetricSummary) string { return m.Algorithm + "\x00" + m.AlgorithmSHA256 }},
	{"unit_mismatch", func(m BrowserDocumentMetricSummary) string { return m.Unit }},
	{"lifecycle_phase_mismatch", func(m BrowserDocumentMetricSummary) string { return m.LifecyclePhase }},
	{"frame_scope_mismatch", func(m BrowserDocumentMetricSummary) string { return m.FrameScope }},
	{"engine_mismatch", func(m BrowserDocumentMetricSummary) string { return m.Engine }},
	{"device_profile_mismatch", func(m BrowserDocumentMetricSummary) string { return m.DeviceProfile }},
	{"cohort_mismatch", func(m BrowserDocumentMetricSummary) string { body, _ := json.Marshal(m.Cohort); return string(body) }},
}

// closestBrowserDocumentTuple is the unpaired baseline tuple of the same
// metric, group and region that agrees with m on the most measurement
// fields, and the first field on which they differ.
func closestBrowserDocumentTuple(m BrowserDocumentMetricSummary, baseline []BrowserDocumentMetricSummary, used map[string]bool) (BrowserDocumentMetricSummary, string, bool) {
	best, bestScore, reason := BrowserDocumentMetricSummary{}, -1, ""
	for _, b := range baseline {
		if b.Metric != m.Metric || b.Group != m.Group || b.Region != m.Region || used[BrowserDocumentMetricKey(b)] {
			continue
		}
		score := 0
		for _, field := range browserDocumentFields {
			if field.value(m) == field.value(b) {
				score++
			}
		}
		if first := BrowserDocumentTupleMismatch(m, b); first != "" && score > bestScore {
			best, bestScore, reason = b, score, first
		}
	}
	return best, reason, bestScore >= 0
}
