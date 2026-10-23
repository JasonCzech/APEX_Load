package storage

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// validateNativeByteQuery admits an archive selection of HTTP actions' body
// bytes (NA-C): no grouping, property or derivation, and at most an exact
// action filter; a comparison run is allowed.
func validateNativeByteQuery(q AnalyticsQuery) error {
	if len(q.Dimensions) != 0 || q.Overlay || q.PropertyName != "" || q.PropertyValue != "" || q.MaxSeries > 1 || q.MaxSeries < 0 || q.Derivation != nil {
		return errors.New("byte queries select HTTP actions without grouping, properties or derived operations")
	}
	for k, v := range q.Filters {
		if k != "action" || v == "" || len(v) > 256 {
			return errors.New("byte queries filter only by one exact action identity")
		}
	}
	if len(q.ComparisonRunID) > 128 {
		return errors.New("comparison run id exceeds limit")
	}
	if q.StartSeconds < 0 || q.EndSeconds <= q.StartSeconds || q.EndSeconds > 86400 || q.BucketSeconds < 1 || q.BucketSeconds > 3600 || (q.EndSeconds-q.StartSeconds+q.BucketSeconds-1)/q.BucketSeconds > 1000 {
		return errors.New("select a window within 24 hours and at most 1000 buckets")
	}
	return analysis.ValidateMetricAggregation(q.Metric, q.Population, q.Aggregation)
}
