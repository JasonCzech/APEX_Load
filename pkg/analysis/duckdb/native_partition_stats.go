//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Summaries come from the just-written physical canonical file, including all
// explicit missing samples. This is a fixed aggregation, not a DISTINCT label
// inventory or caller-provided SQL. Optional constants are omitted when mixed.
func nativePartitionStats(ctx context.Context, db *sql.DB, path string, request NormalizeRequest, measure Measure) (analysis.NormalizedPartitionStats, error) {
	unit, _, err := unitScale(measure.Unit)
	if err != nil {
		return analysis.NormalizedPartitionStats{}, errWorkerProtocol
	}
	stats := analysis.NormalizedPartitionStats{Version: analysis.PartitionManifestVersion, SchemaVersion: analysis.SchemaVersion, IdentityVersion: request.Mapping.Version, Kind: measure.Kind, Unit: unit}
	table, err := nativeIdentityTable([]string{path})
	if err != nil {
		return stats, err
	}
	valid := "schema_version=" + literal(analysis.SchemaVersion) + " AND project_id=" + literal(request.ProjectID) + " AND dataset_id=" + literal(request.DatasetID) + " AND identity_version=" + strconv.Itoa(request.Mapping.Version) + " AND kind=" + literal(measure.Kind) + " AND unit=" + literal(unit)
	valid += " AND timestamp_utc IS NOT NULL AND isfinite(timestamp_utc) AND timestamp_utc>=TIMESTAMPTZ '0001-01-01 00:00:00+00' AND timestamp_utc<TIMESTAMPTZ '10000-01-01 00:00:00+00' AND " + nativeRequiredIdentityText("metric_name", 256) + " AND " + nativeRequiredIdentityText("source", 256)
	valid += " AND host IS NOT NULL AND octet_length(encode(host))<=256 AND NOT contains(host,chr(0)) AND service IS NOT NULL AND octet_length(encode(service))<=256 AND NOT contains(service,chr(0)) AND dimensions_json IS NOT NULL AND octet_length(encode(dimensions_json))<=25000"
	valid += " AND ((quality='valid' AND value IS NOT NULL AND isfinite(value)) OR (quality='missing' AND value IS NULL))"
	statistic, window := "", 0
	if measure.Kind == analysis.KindSummary {
		statistic, window = measure.Statistic, measure.WindowSeconds
	}
	valid += " AND summary_statistic=" + literal(statistic) + " AND summary_window_seconds=" + strconv.Itoa(window) + " AND " + nativeSummarySamplesValid
	if request.Mapping.Version == 1 {
		valid += " AND source=" + literal(request.DatasetID) + " AND host='' AND service='' AND dimensions_json='{}'"
	}
	return nativePartitionStatsScan(ctx, db, table, valid, stats)
}

// nativePartitionStatsScan fills row counts, time range and any constant
// metric and identity from a just-written artifact. Every row must pass valid.
func nativePartitionStatsScan(ctx context.Context, db *sql.DB, table, valid string, stats analysis.NormalizedPartitionStats) (analysis.NormalizedPartitionStats, error) {
	query := "SELECT count(*),count(*) FILTER(WHERE quality='valid'),count(*) FILTER(WHERE quality='missing'),count(*) FILTER(WHERE NOT coalesce((" + valid + "),false)),min(timestamp_utc),max(timestamp_utc),min(metric_name),max(metric_name),min(source),max(source),min(host),max(host),min(service),max(service),min(dimensions_json),max(dimensions_json) FROM " + table
	var invalid int64
	var first, last sql.NullTime
	var metricMin, metricMax, sourceMin, sourceMax, hostMin, hostMax, serviceMin, serviceMax, dimsMin, dimsMax sql.NullString
	if err := db.QueryRowContext(ctx, query).Scan(&stats.Rows, &stats.Valid, &stats.Missing, &invalid, &first, &last, &metricMin, &metricMax, &sourceMin, &sourceMax, &hostMin, &hostMax, &serviceMin, &serviceMax, &dimsMin, &dimsMax); err != nil {
		return stats, err
	}
	if invalid != 0 {
		return stats, errWorkerProtocol
	}
	if stats.Rows > 0 {
		if !first.Valid || !last.Valid {
			return stats, errWorkerProtocol
		}
		minTime, maxTime := first.Time.UTC(), last.Time.UTC()
		stats.MinTime, stats.MaxTime = &minTime, &maxTime
		constant := func(a, b sql.NullString) bool { return a.Valid && b.Valid && a.String == b.String }
		if constant(metricMin, metricMax) {
			metric := metricMin.String
			stats.Metric = &metric
		}
		if constant(sourceMin, sourceMax) && constant(hostMin, hostMax) && constant(serviceMin, serviceMax) && constant(dimsMin, dimsMax) {
			dimensions, err := analysis.DecodeNormalizedDimensions(dimsMin.String)
			if err != nil {
				return stats, errWorkerProtocol
			}
			stats.Identity = &analysis.NormalizedPartitionIdentity{Source: sourceMin.String, Host: hostMin.String, Service: serviceMin.String, Dimensions: dimensions}
		}
	}
	if stats.Validate() != nil {
		return stats, errWorkerProtocol
	}
	return stats, nil
}
