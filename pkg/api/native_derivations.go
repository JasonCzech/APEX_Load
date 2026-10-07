package api

import (
	"errors"
	"slices"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Executable derivations on native queries (B4). The query's buckets become
// an analysis.DerivationSeries and the catalog's typed functions evaluate
// the derivation; each bucket keeps its value or the reason it is unscored.

// nativeCountMetrics are measured per bucket from counts: a complete bucket
// with no events is a measured zero, not a gap.
var nativeCountMetrics = []string{"observation_count", "observation_rate", "error_observation_rate", "success_observation_rate", "message_count"}

// nativeDerivationBucket is one native bucket's derivation input.
func nativeDerivationBucket(q storage.AnalyticsQuery, b analysis.DerivationBucket) analysis.DerivationBucket {
	if b.Value == nil && b.Complete && b.Count == 0 && slices.Contains(nativeCountMetrics, q.Metric) {
		zero := 0.0
		b.Value = &zero
	}
	return b
}

// applyNativeDerivation evaluates q's derivation over buckets (and over a
// baseline run's buckets on the same grid, for baseline derivations).
func applyNativeDerivation(project string, q storage.AnalyticsQuery, buckets, baseline []analysis.DerivationBucket) (analysis.DerivedSeries, error) {
	if q.Derivation == nil {
		return analysis.DerivedSeries{}, errors.New("no derivation requested")
	}
	unit, _, err := analysis.NormalizeUnit(nativeMetricUnit(q))
	if err != nil {
		return analysis.DerivedSeries{}, err
	}
	series := analysis.DerivationSeries{ProjectID: normalizeProjectID(project), Source: "apexload", MetricName: q.Metric, Population: q.Population, Kind: analysis.KindObservation, Unit: unit}
	for _, b := range buckets {
		series.Buckets = append(series.Buckets, nativeDerivationBucket(q, b))
	}
	if baseline == nil {
		return analysis.ApplyQueryDerivation(series, *q.Derivation, nil)
	}
	reference := series
	reference.Buckets = nil
	for _, b := range baseline {
		reference.Buckets = append(reference.Buckets, nativeDerivationBucket(q, b))
	}
	return analysis.ApplyQueryDerivation(series, *q.Derivation, &reference)
}

// applyNativeEvidenceDerivation replaces each evidence series' values with
// its derived values on the same buckets.
func applyNativeEvidenceDerivation(project string, q storage.AnalyticsQuery, out *NativeEvidenceResponse) error {
	if q.Derivation == nil {
		return nil
	}
	for i := range out.Series {
		series := &out.Series[i]
		buckets := make([]analysis.DerivationBucket, len(series.Points))
		for j, p := range series.Points {
			buckets[j] = analysis.DerivationBucket{From: p.From, To: p.To, Value: p.Value, Count: p.Count, Subset: p.failures, Complete: p.complete}
		}
		derived, err := applyNativeDerivation(project, q, buckets, nil)
		if err != nil {
			return err
		}
		series.Unit = derived.Unit
		for j := range series.Points {
			series.Points[j].Value, series.Points[j].Reason = derived.Buckets[j].Value, derived.Buckets[j].Reason
		}
	}
	// Responses name the catalog operation, never the alias asked for.
	derivation := *q.Derivation
	derivation.Operation = analysis.CanonicalDerivation(derivation.Operation)
	out.Derivation = &derivation
	return nil
}

// nativeArchiveDerivationBuckets is an archive series as derivation input.
func nativeArchiveDerivationBuckets(series NativeArchiveSeries) []analysis.DerivationBucket {
	buckets := make([]analysis.DerivationBucket, len(series.Buckets))
	for i, b := range series.Buckets {
		buckets[i] = analysis.DerivationBucket{From: b.From, To: b.To, Value: b.Value, Count: int64(b.Count), Subset: b.subset, Complete: b.complete}
	}
	return buckets
}

// applyNativeArchiveDerivation derives the current series; a baseline
// derivation compares only a comparable baseline run, bucket by bucket.
func applyNativeArchiveDerivation(project string, q storage.AnalyticsQuery, response *NativeArchiveResponse) error {
	if q.Derivation == nil {
		return nil
	}
	var baseline []analysis.DerivationBucket
	if response.Baseline != nil {
		baseline = nativeArchiveDerivationBuckets(*response.Baseline)
	}
	derived, err := applyNativeDerivation(project, q, nativeArchiveDerivationBuckets(response.Current), baseline)
	if err != nil {
		return err
	}
	baselineDerivation := analysis.CanonicalDerivation(q.Derivation.Operation) == "baseline_delta" || analysis.CanonicalDerivation(q.Derivation.Operation) == "baseline_percent_delta"
	response.Current.Unit = derived.Unit
	for i := range response.Current.Buckets {
		value, reason := derived.Buckets[i].Value, derived.Buckets[i].Reason
		if baselineDerivation && !response.Comparable {
			value, reason = nil, "baseline_not_comparable"
		}
		response.Current.Buckets[i].Value, response.Current.Buckets[i].Reason = value, reason
	}
	derivation := *q.Derivation
	derivation.Operation = analysis.CanonicalDerivation(derivation.Operation)
	response.Derivation = &derivation
	return nil
}
