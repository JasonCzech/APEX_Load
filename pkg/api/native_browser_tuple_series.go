package api

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// NativeBrowserTupleSeries never pools algorithms, units, engines or cohorts.
// Version 1 bounds the output to 20 tuples and 20,000 buckets in total.
type NativeBrowserTupleSeries struct {
	Version int                  `json:"version"`
	Tuples  []NativeBrowserTuple `json:"tuples"`
}
type NativeBrowserTuple struct {
	ID           string                     `json:"id"`
	Label        string                     `json:"label"`
	CohortDigest string                     `json:"cohort_digest"`
	Measurement  NativeBrowserTupleIdentity `json:"measurement"`
	Buckets      []NativeArchiveBucket      `json:"buckets"`
}

// NativeBrowserTupleIdentity contains measurement identity only. It does not
// publish zero-valued summary counts beside nonempty tuple buckets.
type NativeBrowserTupleIdentity struct {
	Metric          string                         `json:"metric"`
	Algorithm       string                         `json:"algorithm"`
	AlgorithmSHA256 string                         `json:"algorithm_sha256"`
	Unit            string                         `json:"unit"`
	Engine          string                         `json:"engine"`
	Group           string                         `json:"group,omitempty"`
	Region          string                         `json:"region,omitempty"`
	DeviceProfile   string                         `json:"device_profile"`
	Cohort          *protocol.BrowserRuntimeCohort `json:"cohort"`
	FrameScope      string                         `json:"frame_scope"`
	LifecyclePhase  string                         `json:"lifecycle_phase"`
}

func nativeBrowserTupleIdentity(tuple protocol.BrowserDocumentMetricSummary) NativeBrowserTupleIdentity {
	return NativeBrowserTupleIdentity{Metric: tuple.Metric, Algorithm: tuple.Algorithm, AlgorithmSHA256: tuple.AlgorithmSHA256, Unit: tuple.Unit, Engine: tuple.Engine, Group: tuple.Group, Region: tuple.Region, DeviceProfile: tuple.DeviceProfile, Cohort: tuple.Cohort.Clone(), FrameScope: tuple.FrameScope, LifecyclePhase: tuple.LifecyclePhase}
}

type NativeBrowserTupleComparison struct {
	TupleID string `json:"tuple_id"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

func buildSeparateBrowserTuples(finals []browserDocumentFinal, tuples map[string]protocol.BrowserDocumentMetricSummary, excluded map[string]int64, incomplete bool, manifest *RunArchiveManifest, q storage.AnalyticsQuery, result NativeArchiveSeries, from time.Time) (NativeArchiveSeries, error) {
	limit := q.MaxSeries
	if limit == 0 {
		limit = 20
	}
	if len(tuples) > limit {
		return result, fmt.Errorf("browser document tuple limit exceeded (%d > %d); narrow the filters", len(tuples), limit)
	}
	template := result.Buckets
	if len(template)*len(tuples) > 20000 {
		return result, fmt.Errorf("browser document tuple bucket limit exceeded")
	}
	result.Buckets = []NativeArchiveBucket{}
	result.BrowserTuples = &NativeBrowserTupleSeries{Version: 1, Tuples: []NativeBrowserTuple{}}
	result.Excluded = excluded
	distributions := make(map[string][]protocol.BrowserMetricDistribution, len(tuples))
	for key := range tuples {
		distributions[key] = make([]protocol.BrowserMetricDistribution, len(template))
	}
	for _, f := range finals {
		i := int(f.at.Sub(from)/time.Second) / q.BucketSeconds
		if i >= 0 && i < len(template) {
			distributions[f.key][i].Add(f.value)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(tuples)) {
		tuple := tuples[key]
		buckets := append([]NativeArchiveBucket(nil), template...)
		for i := range buckets {
			buckets[i].Count = int(distributions[key][i].Count)
			buckets[i].complete = !incomplete && !buckets[i].To.After(archiveExecutionCompletedAt(manifest))
			if value, ok := browserDocumentAggregate(distributions[key][i], q.Aggregation); ok {
				buckets[i].Value = &value
			}
		}
		id := browserDocumentTupleDigest(key)
		label := browserDocumentTupleLabel(tuple)
		result.BrowserTuples.Tuples = append(result.BrowserTuples.Tuples, NativeBrowserTuple{ID: id, Label: label, CohortDigest: protocol.BrowserRuntimeCohortDigest(tuple.Cohort), Measurement: nativeBrowserTupleIdentity(tuple), Buckets: buckets})
		result.Identities = append(result.Identities, id)
	}
	if len(finals) == 0 {
		result.Warnings = append(result.Warnings, "No final eligible document measurements match; missing evidence is not zero.")
	}
	if incomplete {
		result.Warnings = append(result.Warnings, "Browser document evidence is incomplete; review its dropped observations before interpreting this selection.")
	}
	return result, nil
}

func compareNativeBrowserTuples(current, baseline NativeArchiveSeries) []NativeBrowserTupleComparison {
	left, right := map[string]bool{}, map[string]bool{}
	for _, t := range current.BrowserTuples.Tuples {
		left[t.ID] = true
	}
	for _, t := range baseline.BrowserTuples.Tuples {
		right[t.ID] = true
	}
	keys := maps.Clone(left)
	maps.Copy(keys, right)
	out := make([]NativeBrowserTupleComparison, 0, len(keys))
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		row := NativeBrowserTupleComparison{TupleID: key, Status: "unavailable"}
		switch {
		case !left[key]:
			row.Reason = "missing_current_tuple"
		case !right[key]:
			row.Reason = "missing_baseline_tuple"
		case current.Fingerprint == "" || current.Fingerprint != baseline.Fingerprint:
			row.Reason = "workload_version_mismatch"
		case len(current.Warnings) > 0 || len(baseline.Warnings) > 0 || len(current.Excluded) > 0 || len(baseline.Excluded) > 0:
			row.Reason = "incomplete_evidence"
		default:
			row.Status = "comparable"
		}
		out = append(out, row)
	}
	return out
}
