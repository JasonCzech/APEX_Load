//go:build analytics_duckdb && cgo

package duckdb

import (
	"errors"
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

// nativeSeriesSelectionFilter also selects one summary statistic: the
// aggregation names it, and rows without a declaration match any aggregation.
func nativeSeriesSelectionFilter(q SeriesQuery) string {
	return nativeSeriesFilter(q) + " AND summary_statistic IN (''," + literal(q.Aggregation) + ")"
}
func nativeSeriesOtherStatisticsSQL(table string, q SeriesQuery) string {
	return "SELECT count(*) FROM (SELECT 1 FROM " + table + " WHERE " + nativeSeriesFilter(q) + " AND summary_statistic NOT IN (''," + literal(q.Aggregation) + ") LIMIT 1)"
}
func nativeSeriesBoundsSQL(q SeriesQuery) string {
	return "SELECT TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano)) + ",TIMESTAMPTZ " + literal(q.To.UTC().Format(time.RFC3339Nano))
}
func nativeSeriesQualitySQL(table string, q SeriesQuery) string {
	admitted := "((quality='valid' AND value IS NOT NULL AND isfinite(value)) OR (quality='missing' AND value IS NULL)) AND " + nativeSummarySamplesValid
	return "SELECT count(*) FROM " + table + " WHERE " + nativeSeriesSelectionFilter(q) + " AND NOT coalesce((" + admitted + "),false)"
}
func nativeSeriesMetadataSQL(table string, q SeriesQuery) string {
	return "SELECT DISTINCT schema_version,kind,unit,identity_version,source,host,service,dimensions_json,summary_statistic,summary_window_seconds FROM " + table + " WHERE " + nativeSeriesSelectionFilter(q) + " LIMIT 2"
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
	filter := nativeSeriesSelectionFilter(q)
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

// nativeSummaryAggregationSQL re-buckets whole windows of one declared
// summary statistic. Counts and sums add; extrema take the extreme; averages
// weight by sample counts (exact sums when present); percentiles pass through
// only when a bucket holds a single window. Any missing sample, duplicated
// window or window crossing the bucket end makes the bucket missing, and the
// third column names the reason when the input existed.
func nativeSummaryAggregationSQL(table string, q SeriesQuery, statistic string, windowSeconds int) (string, error) {
	if err := q.Validate(); err != nil {
		return "", err
	}
	if err := analysis.ValidateSummaryAggregation(statistic, windowSeconds, q.BucketSeconds, q.Aggregation); err != nil {
		return "", err
	}
	origin := "TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano))
	bucketSeconds := strconv.Itoa(q.BucketSeconds)
	bucket := "(" + origin + " + floor(epoch(timestamp_utc-" + origin + ")/" + bucketSeconds + ")*" + bucketSeconds + "*INTERVAL '1 second')"
	rows := "SELECT " + bucket + " AS bucket, timestamp_utc, value, summary_sample_count, summary_sample_sum FROM " + table + " WHERE " + nativeSeriesSelectionFilter(q)
	grouped := "SELECT bucket, count(*) AS n, count(value) AS nv, count(DISTINCT timestamp_utc) AS nt, bool_or(timestamp_utc+" + strconv.Itoa(windowSeconds) + "*INTERVAL '1 second' > bucket+" + bucketSeconds + "*INTERVAL '1 second') AS partial, sum(value) AS vsum, min(value) AS vmin, max(value) AS vmax, count(summary_sample_count) AS nc, sum(summary_sample_count) AS csum, count(summary_sample_sum) AS ns, sum(summary_sample_sum) AS ssum, sum(value*summary_sample_count) AS wsum FROM (" + rows + ") GROUP BY bucket"
	value, reason := "", "''"
	switch {
	case statistic == "count" || statistic == "sum":
		value = "vsum"
	case statistic == "min":
		value = "vmin"
	case statistic == "max":
		value = "vmax"
	case statistic == "avg":
		value = "CASE WHEN n=1 THEN vmin WHEN nc=n AND csum>0 THEN (CASE WHEN ns=n THEN ssum ELSE wsum END)/csum ELSE NULL END"
		reason = "CASE WHEN n>1 AND nc<n THEN " + literal(analysis.MissingAverageWithoutCounts) + " ELSE '' END"
	case analysis.SummaryPercentile(statistic):
		value = "CASE WHEN n=1 THEN vmin ELSE NULL END"
		reason = "CASE WHEN n>1 THEN " + literal(analysis.MissingPercentileNotMergeable) + " ELSE '' END"
	default:
		return "", errors.New("unsupported summary statistic")
	}
	return "SELECT bucket, CASE WHEN nv<n OR nt<n OR partial THEN NULL ELSE " + value + " END, CASE WHEN nv<n THEN '' WHEN nt<n THEN " + literal(analysis.MissingDuplicateSummaryWindow) + " WHEN partial THEN " + literal(analysis.MissingPartialSummaryWindow) + " ELSE " + reason + " END FROM (" + grouped + ") ORDER BY bucket LIMIT 10001", nil
}
