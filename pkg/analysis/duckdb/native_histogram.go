//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Histogram measures normalize into distribution artifacts: one row per
// histogram window with its bounds (in the canonical unit) and per-bin counts.
// Cumulative histograms become deltas between consecutive samples of one
// series across every input file of the import; a bin that decreases is a
// counter reset and that window is missing.

var nativeDistributionColumns = []string{"schema_version", "project_id", "dataset_id", "identity_version", "source", "host", "service", "dimensions_json", "timestamp_utc", "metric_name", "kind", "unit", "quality", "missing_reason", "window_us", "source_window_us", "bounds", "counts"}

// nativeDoubleList spells bounds as an exact DOUBLE list literal.
func nativeDoubleList(values []float64) string {
	parts := make([]string, len(values))
	for i, v := range values {
		if math.IsInf(v, 1) {
			parts[i] = "'inf'::DOUBLE"
		} else {
			parts[i] = "CAST(" + literal(strconv.FormatFloat(v, 'g', -1, 64)) + " AS DOUBLE)"
		}
	}
	return "[" + strings.Join(parts, ",") + "]::DOUBLE[]"
}

// nativeFloat32List spells bounds rounded to float32 as a DOUBLE list, to
// match a FLOAT column exactly.
func nativeFloat32List(values []float64) string {
	rounded := make([]float64, len(values))
	for i, v := range values {
		rounded[i] = float64(float32(v))
	}
	return nativeDoubleList(rounded)
}

// nativeHistogramBounds returns the declared bounds and the same bounds in
// the canonical unit (seconds become milliseconds, and so on).
func nativeHistogramBounds(measure Measure) (declared, scaled []float64, unit string, err error) {
	if measure.Histogram == nil {
		return nil, nil, "", errors.New("histogram declaration missing")
	}
	if declared, err = measure.Histogram.ParseBounds(); err != nil {
		return nil, nil, "", err
	}
	var scale float64
	if unit, scale, err = unitScale(measure.Unit); err != nil {
		return nil, nil, "", err
	}
	scaled = make([]float64, len(declared))
	for i, bound := range declared {
		scaled[i] = bound * scale
	}
	return declared, scaled, unit, nil
}

// nativeCumulativeSlot is one input file's admitted samples of a cumulative
// histogram measure, emitted once every file has been read.
type nativeCumulativeSlot struct {
	file, measure int
	accepted      int64
}

func nativeCumulativeTable(j int) string { return fmt.Sprintf("histogram_cumulative_%02d", j) }

// nativeHistogramNormalize maps one histogram measure of one input file. A
// delta histogram is written at once. A cumulative histogram's samples are
// kept until every file is read, because a series' window can open in one
// file and close in the next; the returned slot is emitted by
// nativeHistogramEmitCumulative.
func nativeHistogramNormalize(ctx context.Context, db *sql.DB, request NormalizeRequest, i, j int, path, dir string, result *NormalizeResult) (*nativeCumulativeSlot, error) {
	measure := request.Mapping.Measures[j]
	h := measure.Histogram
	declared, scaled, unit, err := nativeHistogramBounds(measure)
	if err != nil {
		return nil, err
	}
	n := strconv.Itoa(len(declared))
	name := literal(measure.Name)
	if len(measure.NameColumn) > 0 {
		name = "try_cast(" + column(measure.NameColumn) + " AS VARCHAR)"
	}
	value := column(measure.Value)
	identityProjection, identityCanonical, identityValid := nativeIdentityProjection(request.Mapping, request.DatasetID)
	base := "timestamp_utc IS NOT NULL AND isfinite(timestamp_utc) AND timestamp_utc>=TIMESTAMPTZ '0001-01-01 00:00:00+00' AND timestamp_utc<TIMESTAMPTZ '10000-01-01 00:00:00+00' AND " + nativeRequiredIdentityText("metric_name", 256) + " AND " + identityValid
	from := " FROM read_parquet(" + literal(path) + ", hive_partitioning=false, file_row_number=true)"
	head := "SELECT " + identityProjection + ", file_row_number AS source_row, " + timestampExpression(request.Mapping) + " AS timestamp_utc, " + name + " AS metric_name"
	key := "PARTITION BY source, host, service, dimensions_json, timestamp_utc, metric_name"
	cumulativeBins := h.BucketCounts == analysis.HistogramLeCumulative
	var raw, canon, mapped, groups, longOrdered string
	if len(h.BoundColumn) > 0 {
		// Long table: one row per bound. The group of one timestamp, identity
		// and metric must hold exactly the declared bounds once each.
		// A FLOAT bound column holds the declared bounds rounded to float32,
		// so they are compared at that precision.
		raw = head + ", try_cast(" + column(h.BoundColumn) + " AS DOUBLE) AS bound, typeof(" + column(h.BoundColumn) + ")='FLOAT' AS bound_float, try_cast(" + value + " AS DOUBLE) AS bin_count, (" + value + " IS NULL) AS count_null" + from
		canon = "SELECT source_row, timestamp_utc, metric_name, bound, bin_count, count_null, coalesce((" + base + " AND bound IS NOT NULL AND list_contains(CASE WHEN bound_float THEN " + nativeFloat32List(declared) + " ELSE " + nativeDoubleList(declared) + " END, bound) AND (count_null OR (bin_count IS NOT NULL AND isfinite(bin_count) AND bin_count>=0))),false) AS row_ok, " + identityCanonical + " FROM histogram_raw"
		ordered := "true"
		if cumulativeBins {
			ordered = "count_null OR coalesce(bin_count>=lag(bin_count) OVER (" + key + " ORDER BY bound),true)"
		}
		// The group verdict is one aggregate per group: every row valid,
		// exactly the declared bounds once each (all are declared, so n
		// distinct of n rows is the declared set), all counts present or all
		// absent. It is joined back to its rows, never built per row.
		groupKey := "source, host, service, dimensions_json, timestamp_utc, metric_name"
		verdict := "SELECT " + groupKey + ", coalesce(bool_and(row_ok) AND count(*)=" + n + " AND count(DISTINCT bound)=" + n + " AND sum(count_null::INTEGER) IN (0," + n + ") AND bool_and(bins_ordered),false) AS group_ok FROM histogram_ordered GROUP BY " + groupKey
		longOrdered = ordered
		mapped = "SELECT o.*, coalesce(v.group_ok,false) AS row_valid FROM histogram_ordered o LEFT JOIN (" + verdict + ") v USING (" + groupKey + ")"
		groups = "SELECT identity_version, source, host, service, dimensions_json, timestamp_utc, metric_name, list(bin_count ORDER BY bound) AS counts, bool_and(count_null) AS missing FROM mapped WHERE row_valid GROUP BY ALL"
	} else {
		// Wide table: one row per histogram, the value holding the last bound.
		cells, nulls := []string{}, []string{}
		for _, path := range append(append([]Column{}, h.CountColumns...), measure.Value) {
			cells = append(cells, "try_cast("+column(path)+" AS DOUBLE)")
			nulls = append(nulls, "("+column(path)+" IS NULL)::INTEGER")
		}
		raw = head + ", [" + strings.Join(cells, ",") + "] AS counts, (" + strings.Join(nulls, "+") + ") AS nulls" + from
		ordered := ""
		if cumulativeBins {
			ordered = " AND coalesce(list_min(list_transform(range(2," + n + "+1), i -> counts[i]-counts[i-1])),0)>=0"
		}
		canon = "SELECT source_row, timestamp_utc, metric_name, counts, nulls, coalesce((" + base + " AND (nulls=" + n + " OR (nulls=0 AND list_count(counts)=" + n + " AND list_min(counts)>=0 AND isfinite(list_max(counts))" + ordered + "))),false) AS row_ok, " + identityCanonical + " FROM histogram_raw"
		unique := "true"
		if h.Temporality == analysis.HistogramCumulative {
			// Differencing needs one sample per series instant.
			unique = "count(*) OVER (" + key + ")=1"
		}
		mapped = "SELECT *, row_ok AND " + unique + " AS row_valid FROM histogram_canon"
		groups = "SELECT identity_version, source, host, service, dimensions_json, timestamp_utc, metric_name, counts, nulls=" + n + " AS missing FROM mapped WHERE row_valid"
	}
	for _, view := range [][2]string{{"histogram_raw", raw}, {"histogram_canon", canon}} {
		if _, err := db.ExecContext(ctx, "CREATE OR REPLACE TEMP VIEW "+view[0]+" AS "+view[1]); err != nil {
			return nil, err
		}
	}
	if len(h.BoundColumn) > 0 {
		if _, err := db.ExecContext(ctx, "CREATE OR REPLACE TEMP VIEW histogram_ordered AS SELECT *, "+longOrdered+" AS bins_ordered FROM histogram_canon"); err != nil {
			return nil, err
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE OR REPLACE TEMP VIEW mapped AS "+mapped); err != nil {
		return nil, err
	}
	accepted, err := nativeAdmitMappedRows(ctx, db, request, i, j, "row_valid", literal("invalid_histogram_row"), dir, result)
	if err != nil {
		return nil, err
	}
	bins := "counts"
	if cumulativeBins {
		bins = "list_transform(range(1," + n + "+1), i -> counts[i]-coalesce(counts[i-1],0))"
	}
	if h.Temporality == analysis.HistogramCumulative {
		samples := "SELECT CAST(" + strconv.Itoa(i) + " AS INTEGER) AS source_file, identity_version, source, host, service, dimensions_json, timestamp_utc, metric_name, " + bins + " AS bins FROM (" + groups + ") WHERE NOT missing"
		statement := "INSERT INTO " + nativeCumulativeTable(j) + " " + samples
		if i == 0 {
			statement = "CREATE OR REPLACE TEMP TABLE " + nativeCumulativeTable(j) + " AS " + samples
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return nil, err
		}
		return &nativeCumulativeSlot{file: i, measure: j, accepted: accepted}, nil
	}
	head = "SELECT " + literal(analysis.DistributionSchemaVersion) + " AS schema_version, " + literal(request.ProjectID) + " AS project_id, " + literal(request.DatasetID) + " AS dataset_id, identity_version, source, host, service, dimensions_json, "
	tail := ", metric_name, 'histogram' AS kind, " + literal(unit) + " AS unit, "
	window := strconv.FormatInt(int64(h.WindowSeconds)*1_000_000, 10)
	artifact := head + "timestamp_utc" + tail + "CASE WHEN missing THEN 'missing' ELSE 'valid' END AS quality, '' AS missing_reason, CAST(" + window + " AS BIGINT) AS window_us, CAST(" + window + " AS BIGINT) AS source_window_us, " + nativeDoubleList(scaled) + " AS bounds, CASE WHEN missing THEN NULL ELSE " + bins + " END AS counts FROM (" + groups + ")"
	return nil, nativeWriteDistribution(ctx, db, request, measure, artifact, filepath.Join(dir, fmt.Sprintf("part-%04d-%02d.parquet", i, j)), accepted, result)
}

// nativeHistogramEmitCumulative writes each input file's slot of a cumulative
// histogram measure: the windows whose closing sample came from that file,
// differenced against the previous sample of the series in any file. Each
// window runs from one present sample to the next; a series' first sample
// only opens the first window, and two samples at one instant open none.
func nativeHistogramEmitCumulative(ctx context.Context, db *sql.DB, request NormalizeRequest, slot nativeCumulativeSlot, dir string, result *NormalizeResult) error {
	measure := request.Mapping.Measures[slot.measure]
	declared, scaled, unit, err := nativeHistogramBounds(measure)
	if err != nil {
		return err
	}
	n := strconv.Itoa(len(declared))
	series := "PARTITION BY source, host, service, dimensions_json, metric_name ORDER BY timestamp_utc, source_file"
	deltas := "SELECT *, list_transform(range(1," + n + "+1), k -> bins[k]-prev[k]) AS delta, epoch_us(timestamp_utc)-epoch_us(prev_ts) AS span_us FROM (SELECT *, lag(bins) OVER (" + series + ") AS prev, lag(timestamp_utc) OVER (" + series + ") AS prev_ts FROM " + nativeCumulativeTable(slot.measure) + ") WHERE prev IS NOT NULL AND source_file=" + strconv.Itoa(slot.file) + " AND epoch_us(timestamp_utc)>epoch_us(prev_ts)"
	artifact := "SELECT " + literal(analysis.DistributionSchemaVersion) + " AS schema_version, " + literal(request.ProjectID) + " AS project_id, " + literal(request.DatasetID) + " AS dataset_id, identity_version, source, host, service, dimensions_json, prev_ts AS timestamp_utc, metric_name, 'histogram' AS kind, " + literal(unit) + " AS unit, CASE WHEN list_min(delta)<0 THEN 'missing' ELSE 'valid' END AS quality, CASE WHEN list_min(delta)<0 THEN " + literal(analysis.MissingHistogramCounterReset) + " ELSE '' END AS missing_reason, span_us AS window_us, CAST(0 AS BIGINT) AS source_window_us, " + nativeDoubleList(scaled) + " AS bounds, CASE WHEN list_min(delta)<0 THEN NULL ELSE delta END AS counts FROM (" + deltas + ")"
	return nativeWriteDistribution(ctx, db, request, measure, artifact, filepath.Join(dir, fmt.Sprintf("part-%04d-%02d.parquet", slot.file, slot.measure)), slot.accepted, result)
}

// nativeWriteDistribution copies one distribution artifact and records its
// partition with the source rows it was normalized from.
func nativeWriteDistribution(ctx context.Context, db *sql.DB, request NormalizeRequest, measure Measure, artifact, out string, accepted int64, result *NormalizeResult) error {
	if _, err := db.ExecContext(ctx, "COPY ("+artifact+") TO "+literal(out)+" (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 122880)"); err != nil {
		return err
	}
	if err := syncNormalizedFile(out); err != nil {
		return err
	}
	stats, err := nativeDistributionPartitionStats(ctx, db, out, request, measure, accepted)
	if err != nil {
		return err
	}
	result.Files = append(result.Files, out)
	result.Partitions = append(result.Partitions, NormalizedFilePartition{File: out, Stats: stats})
	return nil
}

// nativeDistributionValid admits one canonical distribution row with n bins.
func nativeDistributionValid(n int) string {
	size := strconv.Itoa(n)
	return "len(bounds)=" + size + " AND window_us>0 AND source_window_us>=0 AND ((quality='valid' AND missing_reason='' AND counts IS NOT NULL AND len(counts)=" + size + " AND list_count(counts)=" + size + " AND list_min(counts)>=0 AND isfinite(list_max(counts))) OR (quality='missing' AND counts IS NULL AND missing_reason IN (''," + literal(analysis.MissingHistogramCounterReset) + ")))"
}

func nativeDistributionPartitionStats(ctx context.Context, db *sql.DB, path string, request NormalizeRequest, measure Measure, sourceRows int64) (analysis.NormalizedPartitionStats, error) {
	_, scaled, unit, err := nativeHistogramBounds(measure)
	if err != nil {
		return analysis.NormalizedPartitionStats{}, errWorkerProtocol
	}
	stats := analysis.NormalizedPartitionStats{Version: analysis.PartitionManifestVersion, SchemaVersion: analysis.DistributionSchemaVersion, IdentityVersion: request.Mapping.Version, Kind: analysis.KindHistogram, Unit: unit, SourceRows: sourceRows}
	window := int64(0)
	if measure.Histogram.Temporality == analysis.HistogramDelta {
		window = int64(measure.Histogram.WindowSeconds) * 1_000_000
	}
	table, err := nativeDistributionTable([]string{path})
	if err != nil {
		return stats, err
	}
	valid := "schema_version=" + literal(analysis.DistributionSchemaVersion) + " AND project_id=" + literal(request.ProjectID) + " AND dataset_id=" + literal(request.DatasetID) + " AND identity_version=" + strconv.Itoa(request.Mapping.Version) + " AND kind='histogram' AND unit=" + literal(unit)
	valid += " AND timestamp_utc IS NOT NULL AND isfinite(timestamp_utc) AND timestamp_utc>=TIMESTAMPTZ '0001-01-01 00:00:00+00' AND timestamp_utc<TIMESTAMPTZ '10000-01-01 00:00:00+00' AND " + nativeRequiredIdentityText("metric_name", 256) + " AND " + nativeRequiredIdentityText("source", 256)
	valid += " AND host IS NOT NULL AND octet_length(encode(host))<=256 AND NOT contains(host,chr(0)) AND service IS NOT NULL AND octet_length(encode(service))<=256 AND NOT contains(service,chr(0)) AND dimensions_json IS NOT NULL AND octet_length(encode(dimensions_json))<=25000"
	valid += " AND bounds=" + nativeDoubleList(scaled) + " AND source_window_us=" + strconv.FormatInt(window, 10) + " AND " + nativeDistributionValid(len(scaled))
	if window > 0 {
		valid += " AND window_us=source_window_us"
	}
	if request.Mapping.Version == 1 {
		valid += " AND source=" + literal(request.DatasetID) + " AND host='' AND service='' AND dimensions_json='{}'"
	}
	return nativePartitionStatsScan(ctx, db, table, valid, stats)
}

// nativeClassifyArtifacts separates metric-point artifacts from distribution
// artifacts; a query reads each kind with its own table.
func nativeClassifyArtifacts(paths []string) (scalar, distribution []string, err error) {
	for _, path := range paths {
		schema, err := analysis.InspectParquet(path)
		if err != nil {
			return nil, nil, err
		}
		histogram := false
		for _, c := range schema.Columns {
			// List columns surface as nested leaves (bounds.list.element).
			if c.PathComponents[0] == "bounds" {
				histogram = true
			}
		}
		if histogram {
			distribution = append(distribution, path)
		} else {
			scalar = append(scalar, path)
		}
	}
	return scalar, distribution, nil
}

func nativeDistributionTable(paths []string) (string, error) {
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		schema, err := analysis.InspectParquet(path)
		if err != nil {
			return "", err
		}
		present := map[string]bool{}
		for _, c := range schema.Columns {
			present[c.PathComponents[0]] = true
		}
		for _, name := range nativeDistributionColumns {
			if !present[name] {
				return "", errors.New("distribution artifact lacks a canonical column")
			}
		}
		parts = append(parts, "SELECT "+strings.Join(nativeDistributionColumns, ", ")+" FROM read_parquet("+literal(path)+", hive_partitioning=false)")
	}
	if len(parts) == 0 {
		return "", errors.New("no distribution artifacts")
	}
	return "(" + strings.Join(parts, " UNION ALL ") + ")", nil
}

// nativeHistogramSeries answers a series request from distribution artifacts.
// found is false when no selected histogram row has the requested metric.
func nativeHistogramSeries(ctx context.Context, db *sql.DB, paths []string, q SeriesQuery) (points []analysis.MetricPoint, template analysis.MetricPoint, found bool, err error) {
	table, err := nativeDistributionTable(paths)
	if err != nil {
		return nil, template, false, err
	}
	filter := nativeSeriesFilter(q)
	var identities, boundSets int64
	if err := db.QueryRowContext(ctx, "SELECT count(DISTINCT (schema_version,kind,unit,identity_version,source,host,service,dimensions_json,source_window_us)), count(DISTINCT bounds) FROM "+table+" WHERE "+filter).Scan(&identities, &boundSets); err != nil {
		return nil, template, false, err
	}
	if identities == 0 {
		return nil, template, false, nil
	}
	if identities != 1 {
		return nil, template, true, errors.New("multiple histogram identities, units or windows require a narrower series selection")
	}
	if boundSets != 1 {
		return nil, template, true, fmt.Errorf("%w (%d bound sets)", ErrIncompatibleHistogramBounds, boundSets)
	}
	var schemaVersion, kind, unit, source, host, service, dimensionsJSON string
	var identityVersion int
	var sourceWindow int64
	var boundList []any
	if err := db.QueryRowContext(ctx, "SELECT schema_version,kind,unit,identity_version,source,host,service,dimensions_json,source_window_us,bounds FROM "+table+" WHERE "+filter+" LIMIT 1").Scan(&schemaVersion, &kind, &unit, &identityVersion, &source, &host, &service, &dimensionsJSON, &sourceWindow, &boundList); err != nil {
		return nil, template, true, err
	}
	bounds, err := nativeFloatList(boundList)
	if err != nil || len(bounds) < 1 || len(bounds) > analysis.MaxHistogramBounds {
		return nil, template, true, errors.New("invalid histogram bounds")
	}
	var invalid int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE "+filter+" AND NOT coalesce(("+nativeDistributionValid(len(bounds))+"),false)").Scan(&invalid); err != nil {
		return nil, template, true, err
	}
	queryIdentityVersion := q.IdentitySchemaVersion
	if queryIdentityVersion == 0 {
		queryIdentityVersion = 1
	}
	if invalid != 0 || schemaVersion != analysis.DistributionSchemaVersion || kind != analysis.KindHistogram || sourceWindow < 0 || sourceWindow%1_000_000 != 0 {
		return nil, template, true, fmt.Errorf("%w: selected distribution evidence is inconsistent", errWorkerProtocol)
	}
	if identityVersion != 1 && identityVersion != 2 || identityVersion != queryIdentityVersion {
		return nil, template, true, errors.New("normalized identity version requires explicit compatible query capability")
	}
	dimensions, err := analysis.DecodeNormalizedDimensions(dimensionsJSON)
	if err != nil {
		return nil, template, true, err
	}
	if !nativeSeriesIdentityMatches(q, analysis.MetricPoint{Source: source, Host: host, Service: service, Dimensions: dimensions}) {
		return nil, template, true, errors.New("normalized identity does not match selected query scope")
	}
	window := int(sourceWindow / 1_000_000)
	lineage, err := analysis.HistogramBucketDerivation(window, q.BucketSeconds, q.Aggregation)
	if err != nil {
		return nil, template, true, err
	}
	if q.Aggregation == "count" {
		unit = "count"
	}
	template = analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: q.ProjectID, DatasetID: q.DatasetID, Source: source, Host: host, Service: service, Dimensions: dimensions, MetricName: q.Metric, Kind: analysis.KindGauge, InputKind: analysis.KindHistogram, Derivation: lineage, Unit: unit, Quality: analysis.QualityMissing}
	rows, err := db.QueryContext(ctx, nativeHistogramMergeSQL(table, q, len(bounds)))
	if err != nil {
		return nil, template, true, err
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		var total, valid, distinct int64
		var crossing bool
		var reason string
		var merged []any
		if err := rows.Scan(&at, &total, &valid, &distinct, &crossing, &reason, &merged); err != nil {
			return nil, template, true, err
		}
		if len(points) >= analysis.MaxSeriesPoints {
			return nil, template, true, errors.New("analytical result exceeds point limit")
		}
		p := nativeSeriesPoint(template, at.UTC())
		switch {
		case valid < total:
			p.MissingReason = reason
		case distinct < total:
			p.MissingReason = analysis.MissingDuplicateHistogramRow
		case crossing:
			p.MissingReason = analysis.MissingPartialHistogramWindow
		default:
			counts, err := nativeFloatList(merged)
			if err != nil || len(counts) != len(bounds) {
				return nil, template, true, errors.New("invalid merged histogram counts")
			}
			value, bin, missingReason, err := analysis.HistogramValue(bounds, counts, q.Aggregation)
			if err != nil {
				return nil, template, true, err
			}
			if missingReason != "" {
				p.MissingReason = missingReason
			} else {
				p.Value, p.Quality, p.EstimateBin = &value, analysis.QualityValid, bin
			}
		}
		if err := p.Validate(); err != nil {
			return nil, template, true, err
		}
		points = append(points, p)
	}
	return points, template, true, rows.Err()
}

// nativeHistogramMergeSQL sums per-bin counts of every window starting in a
// bucket, with the counts needed to refuse duplicated or crossing windows and
// the reason of any missing window. A bucket is crossed when one of its
// windows ends past the bucket's end, or when a window from an earlier bucket
// (or from before the query start) ends inside it: either way part of the
// bucket's observations sit in a window it cannot split.
func nativeHistogramMergeSQL(table string, q SeriesQuery, bins int) string {
	origin := "TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano))
	seconds := strconv.Itoa(q.BucketSeconds)
	end := "timestamp_utc+window_us*INTERVAL '1 microsecond'"
	rows := "SELECT (" + origin + " + floor(epoch(timestamp_utc-" + origin + ")/" + seconds + ")*" + seconds + "*INTERVAL '1 second') AS bucket, timestamp_utc, window_us, quality, missing_reason, counts FROM " + table + " WHERE " + nativeSeriesFilter(q)
	leadIn := "(SELECT max(" + end + ") FROM " + table + " WHERE " + nativeSeriesLeadInFilter(q) + ")"
	stats := "SELECT bucket, count(*) AS n, count(*) FILTER(WHERE quality='valid') AS nv, count(DISTINCT timestamp_utc) AS nt, bool_or(" + end + " > bucket+" + seconds + "*INTERVAL '1 second') AS crossing, max(" + end + ") AS max_end, coalesce(min(missing_reason) FILTER(WHERE quality='missing'),'') AS reason FROM histogram_rows GROUP BY bucket"
	earlier := "SELECT *, coalesce(max(max_end) OVER (ORDER BY bucket ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING), " + leadIn + ") AS earlier_end FROM (" + stats + ")"
	sums := "SELECT bucket, list(total ORDER BY i) AS merged FROM (SELECT bucket, i, sum(counts[i]) AS total FROM histogram_rows, range(1," + strconv.Itoa(bins) + "+1) t(i) WHERE quality='valid' GROUP BY bucket, i) GROUP BY bucket"
	return "WITH histogram_rows AS (" + rows + ") SELECT s.bucket, s.n, s.nv, s.nt, s.crossing OR coalesce(s.earlier_end > s.bucket, false), s.reason, coalesce(m.merged, []::DOUBLE[]) FROM (" + earlier + ") s LEFT JOIN (" + sums + ") m USING (bucket) ORDER BY s.bucket LIMIT 10001"
}

func nativeFloatList(values []any) ([]float64, error) {
	out := make([]float64, len(values))
	for i, value := range values {
		v, ok := value.(float64)
		if !ok {
			return nil, errors.New("non-numeric histogram list element")
		}
		out[i] = v
	}
	return out, nil
}

// nativeSeriesPoint copies a series template to one bucket.
func nativeSeriesPoint(template analysis.MetricPoint, at time.Time) analysis.MetricPoint {
	p := template
	p.TimestampUTC = at
	p.Dimensions = maps.Clone(template.Dimensions)
	p.Derivation = append([]analysis.DerivationStep(nil), template.Derivation...)
	return p
}
