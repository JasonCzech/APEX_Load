package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Browser document metrics (D2). Native panels and RCA read the final
// eligible measurement of each document lifecycle metric, once, at its
// latest retained state; provisional and censored measurements and progress
// revisions never enter a distribution. One series is one measurement tuple:
// values of different algorithms, units, lifecycle phases, frame scopes,
// engines, device profiles or runtime cohorts are never merged.

// browserDocumentFinal is one final eligible document measurement.
type browserDocumentFinal struct {
	tuple protocol.BrowserDocumentMetricSummary
	key   string
	at    time.Time
	value float64
}

// browserDocumentTupleLabel names a tuple for people: its measurement fields,
// scope and the cohort digest a query filters by.
func browserDocumentTupleLabel(m protocol.BrowserDocumentMetricSummary) string {
	parts := []string{m.Metric, m.Engine, "profile=" + m.DeviceProfile, m.FrameScope, m.LifecyclePhase}
	if m.Group != "" {
		parts = append(parts, "group="+m.Group)
	}
	if m.Region != "" {
		parts = append(parts, "region="+m.Region)
	}
	return strings.Join(append(parts, "cohort="+protocol.BrowserRuntimeCohortDigest(m.Cohort)), " · ")
}

// browserDocumentTupleDigest is a tuple key's SHA-256, hex.
func browserDocumentTupleDigest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// browserDocumentFinals splits an archive's latest document measurements of
// the given metrics into final eligible values and the reasons the others
// were left out (unqualified or unavailable finals, provisional, censored).
func browserDocumentFinals(accumulator *archiveBrowserDocumentAccumulator, metrics []string, keep func(protocol.BrowserDocumentObservation) bool) ([]browserDocumentFinal, map[string]int64) {
	var finals []browserDocumentFinal
	excluded := map[string]int64{}
	for _, id := range slices.Sorted(maps.Keys(accumulator.measurements)) {
		o := accumulator.measurements[id].observation
		if !slices.Contains(metrics, o.Metric) || !keep(o) {
			continue
		}
		switch {
		case o.State != "final":
			excluded[o.State]++
		case !o.Qualified || o.Value == nil:
			reason := "unqualified_final"
			if !o.Available {
				reason = "unavailable_final"
			}
			excluded[reason]++
		default:
			tuple := protocol.BrowserDocumentObservationTuple(o)
			finals = append(finals, browserDocumentFinal{tuple: tuple, key: protocol.BrowserDocumentMetricKey(tuple), at: o.WindowEnd, value: *o.Value})
		}
	}
	return finals, excluded
}

// browserDocumentFilterMatch applies a native query's tuple filters.
func browserDocumentFilterMatch(filters map[string]string, o protocol.BrowserDocumentObservation) bool {
	for name, want := range filters {
		var got string
		switch name {
		case "engine":
			got = o.Engine
		case "device_profile":
			got = o.DeviceProfile
		case "frame_scope":
			got = o.FrameScope
		case "lifecycle_phase":
			got = o.LifecyclePhase
		case "group":
			got = o.Group
		case "region":
			got = o.Region
		case "cohort":
			got = protocol.BrowserRuntimeCohortDigest(o.Cohort)
		default:
			return false
		}
		if got != want {
			return false
		}
	}
	return true
}

// browserDocumentAggregate is a final distribution's approximate percentile
// for a catalog aggregation; a bucket's Count is its final documents.
func browserDocumentAggregate(d protocol.BrowserMetricDistribution, aggregation string) (float64, bool) {
	q, known := map[string]float64{"p50": .50, "p75": .75, "p90": .90, "p95": .95, "p99": .99}[aggregation]
	if !known {
		return 0, false
	}
	return d.Quantile(q)
}

// buildNativeBrowserDocumentSeries reads one measurement tuple's final
// document distribution per bucket, by the time each measurement's window
// closed. A selection that spans more than one tuple is refused.
func buildNativeBrowserDocumentSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, q storage.AnalyticsQuery, result NativeArchiveSeries, from, to time.Time) (NativeArchiveSeries, error) {
	metric, ok := analysis.BrowserDocumentMetric(q.Metric)
	if !ok {
		return result, fmt.Errorf("%s is not a browser document metric", q.Metric)
	}
	accumulator, captured, err := readArchiveBrowserDocumentMeasurements(ctx, record, manifest)
	if err != nil {
		return result, err
	}
	if to.After(manifest.CompletedAt) {
		result.Warnings = append(result.Warnings, "The requested window extends past run completion.")
	}
	if accumulator == nil {
		if captured {
			result.Warnings = append(result.Warnings, "Browser document capture was requested but no document evidence was retained.")
		}
		result.Warnings = append(result.Warnings, "No browser document evidence; missing evidence is not zero.")
		return result, nil
	}
	finals, excluded := browserDocumentFinals(accumulator, []string{metric}, func(o protocol.BrowserDocumentObservation) bool {
		return !o.WindowEnd.Before(from) && o.WindowEnd.Before(to) && browserDocumentFilterMatch(q.Filters, o)
	})
	tuples := map[string]protocol.BrowserDocumentMetricSummary{}
	for _, f := range finals {
		tuples[f.key] = f.tuple
	}
	if len(tuples) > 1 {
		keys := slices.Sorted(maps.Keys(tuples))
		return result, fmt.Errorf("the selection spans %d browser document measurement tuples (first difference: %s); filter by engine, device_profile, frame_scope, lifecycle_phase, group, region or cohort", len(tuples), protocol.BrowserDocumentTupleMismatch(tuples[keys[0]], tuples[keys[1]]))
	}
	distributions := make([]protocol.BrowserMetricDistribution, len(result.Buckets))
	for _, f := range finals {
		i := int(f.at.Sub(from)/time.Second) / q.BucketSeconds
		if i >= 0 && i < len(distributions) {
			distributions[i].Add(f.value)
		}
	}
	for i := range result.Buckets {
		b := &result.Buckets[i]
		b.Count = int(distributions[i].Count)
		b.complete = !accumulator.incomplete && !b.To.After(manifest.CompletedAt)
		if value, ok := browserDocumentAggregate(distributions[i], q.Aggregation); ok {
			b.Value = &value
		}
	}
	for key, tuple := range tuples {
		result.Identities = append(result.Identities, browserDocumentTupleLabel(tuple))
		result.documentTuple = &tuple
		// Runs compare only the same measurement tuple.
		result.Fingerprint += "\x00" + browserDocumentTupleDigest(key)
	}
	if len(excluded) > 0 {
		result.Excluded = excluded
	}
	if len(finals) == 0 {
		result.Warnings = append(result.Warnings, "No final eligible document measurements match; missing evidence is not zero.")
	}
	if accumulator.incomplete {
		result.Warnings = append(result.Warnings, "Browser document evidence is incomplete; review its dropped observations before interpreting this selection.")
	}
	return result, nil
}
