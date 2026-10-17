//go:build analytics_duckdb && cgo

package duckdb

import (
	"strconv"
	"time"
)

// nativeWindowAdmissionSQL annotates whole source windows before either summary
// statistics or histogram bins are merged. All SQL fragments are private
// projections/filters over an already validated canonical table.
//
// Overlapping windows cannot be added without counting observations twice. A
// window crossing either edge of a bucket cannot be split exactly. The first
// row in each bucket also checks carryover that ends before that row starts;
// later adjacent rows in the same bucket are not carryover. Keep the maximum
// earlier end, rather than only the immediate predecessor, for nested windows.
func nativeWindowAdmissionSQL(table string, q SeriesQuery, filter, leadInFilter, windowEnd, columns string) string {
	origin := "TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano))
	queryEnd := "TIMESTAMPTZ " + literal(q.To.UTC().Format(time.RFC3339Nano))
	seconds := strconv.Itoa(q.BucketSeconds)
	bucket := "(" + origin + " + floor(epoch(timestamp_utc-" + origin + ")/" + seconds + ")*" + seconds + "*INTERVAL '1 second')"
	rows := "SELECT " + bucket + " AS bucket, timestamp_utc, " + windowEnd + " AS window_end, " + columns + " FROM " + table + " WHERE " + filter
	leadIn := "(SELECT max(" + windowEnd + ") FROM " + table + " WHERE " + leadInFilter + ")"
	ordered := "SELECT *, greatest(max(window_end) OVER (ORDER BY timestamp_utc ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING), " + leadIn + ") AS previous_end, row_number() OVER (PARTITION BY bucket ORDER BY timestamp_utc) AS bucket_ordinal FROM (" + rows + ")"
	effectiveEnd := "least(bucket+" + seconds + "*INTERVAL '1 second', " + queryEnd + ")"
	return "SELECT *, window_end > " + effectiveEnd + " OR coalesce(previous_end > timestamp_utc OR (bucket_ordinal=1 AND previous_end > bucket),false) AS partial_window FROM (" + ordered + ")"
}

// A query may omit identity filters when its in-range rows have one identity.
// Lead-in evidence must still belong to that exact validated series.
func nativeWindowIdentityFilter(source, host, service, dimensionsJSON string) string {
	return "source=" + literal(source) + " AND host=" + literal(host) + " AND service=" + literal(service) + " AND dimensions_json=" + literal(dimensionsJSON)
}
