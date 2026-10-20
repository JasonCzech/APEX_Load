package storage

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func validateNativeJavaHTTPQuery(q AnalyticsQuery) error {
	if len(q.Dimensions) != 0 || q.Overlay || q.PropertyName != "" || q.PropertyValue != "" || q.MaxSeries > 1 || q.MaxSeries < 0 || q.Derivation != nil {
		return errors.New("Java representation queries select one actual action without grouping, properties or derived scalar operations")
	}
	if len(q.Filters) != 1 || q.Filters["action"] == "" || len(q.Filters["action"]) > 256 {
		return errors.New("Java representation queries require one exact action identity")
	}
	if len(q.ComparisonRunID) > 128 {
		return errors.New("comparison run id exceeds limit")
	}
	if q.StartSeconds < 0 || q.EndSeconds <= q.StartSeconds || q.EndSeconds > 86400 || q.BucketSeconds < 1 || q.BucketSeconds > 3600 || (q.EndSeconds-q.StartSeconds+q.BucketSeconds-1)/q.BucketSeconds > 1000 {
		return errors.New("select a window within 24 hours and at most 1000 buckets")
	}
	return analysis.ValidateMetricAggregation(q.Metric, q.Population, q.Aggregation)
}
