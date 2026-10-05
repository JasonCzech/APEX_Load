//go:build analytics_duckdb && cgo

package duckdb

import (
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// These private builders are used by the actual bounded child operation. Table
// comes only from nativeIdentityTable on approved immutable staged files; no SQL
// interface, identifier, expression or arbitrary path is exposed to callers.
func nativeSeriesFilter(q SeriesQuery) string {
	filter := "project_id=" + literal(q.ProjectID) + " AND dataset_id=" + literal(q.DatasetID) + " AND metric_name=" + literal(q.Metric) + " AND timestamp_utc >= TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano)) + " AND timestamp_utc < TIMESTAMPTZ " + literal(q.To.UTC().Format(time.RFC3339Nano))
	return filter + nativeIdentityFilter(q)
}
func nativeSeriesBoundsSQL(q SeriesQuery) string {
	return "SELECT TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano)) + ",TIMESTAMPTZ " + literal(q.To.UTC().Format(time.RFC3339Nano))
}
func nativeSeriesQualitySQL(table string, q SeriesQuery) string {
	admitted := "(quality='valid' AND value IS NOT NULL AND isfinite(value)) OR (quality='missing' AND value IS NULL)"
	return "SELECT count(*) FROM " + table + " WHERE " + nativeSeriesFilter(q) + " AND NOT coalesce((" + admitted + "),false)"
}
func nativeSeriesMetadataSQL(table string, q SeriesQuery) string {
	return "SELECT DISTINCT schema_version,kind,unit,identity_version,source,host,service,dimensions_json FROM " + table + " WHERE " + nativeSeriesFilter(q) + " LIMIT 2"
}
func nativeSeriesAggregationSQL(table string, q SeriesQuery, kind string) (string, error) {
	if err := q.Validate(); err != nil {
		return "", err
	}
	if err := analysis.ValidateScalarAggregation(kind, q.Aggregation); err != nil {
		return "", err
	}
	origin := "TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano))
	bucket := "(" + origin + " + floor(epoch(timestamp_utc-" + origin + ")/" + strconv.Itoa(q.BucketSeconds) + ")*" + strconv.Itoa(q.BucketSeconds) + "*INTERVAL '1 second')"
	agg := map[string]string{"avg": "avg(value)", "min": "min(value)", "max": "max(value)", "sum": "sum(value)", "count": "count(value)", "p50": "quantile_cont(value,0.5)", "p95": "quantile_cont(value,0.95)", "p99": "quantile_cont(value,0.99)"}[q.Aggregation]
	filter := nativeSeriesFilter(q)
	from := table + " WHERE " + filter
	if q.Aggregation == "rate" {
		// Preserve existing in-window-only counter semantics: duplicate instants,
		// resets and missing samples break the chain. No predecessor outside From.
		from = "(SELECT timestamp_utc, CASE WHEN value>=lag(value) OVER w AND timestamp_utc>lag(timestamp_utc) OVER w THEN (value-lag(value) OVER w)/epoch(timestamp_utc-lag(timestamp_utc) OVER w) ELSE NULL END AS value FROM (SELECT timestamp_utc, CASE WHEN count(*)=1 AND count(value)=1 THEN min(value) ELSE NULL END AS value FROM " + table + " WHERE " + filter + " GROUP BY timestamp_utc) WINDOW w AS (ORDER BY timestamp_utc))"
		agg = "CASE WHEN count(value)=count(*) THEN avg(value) ELSE NULL END"
	} else {
		// Any missing sample makes its complete bucket, including count, missing.
		agg = "CASE WHEN count(value)=count(*) THEN " + agg + " ELSE NULL END"
	}
	return "SELECT " + bucket + " AS bucket, " + agg + " FROM " + from + " GROUP BY bucket ORDER BY bucket LIMIT 10001", nil
}
