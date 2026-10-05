//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

var heavyJobs = make(chan struct{}, 1)
var errResourceLimit = errors.New("analytics cancelled to preserve controller memory or disk headroom")

func syncNormalizedFile(path string) error {
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func Available() bool         { return true }
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func column(path Column) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = "\"" + strings.ReplaceAll(p, "\"", "\"\"") + "\""
	}
	return strings.Join(parts, ".")
}

func (e *Engine) withDB(ctx context.Context, operation func(context.Context, *sql.DB) error) error {
	select {
	case heavyJobs <- struct{}{}:
		defer func() { <-heavyJobs }()
	default:
		return ErrBusy
	}
	if err := analyticsHeadroom(e.root); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	ctx, limitCancel := context.WithCancelCause(ctx)
	defer limitCancel(nil)
	spill, err := os.MkdirTemp(e.root, "spill-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(spill)
	threads := e.workerThreads
	if threads < 1 || threads > 2 {
		return errWorkerProtocol
	}
	db, err := sql.Open("duckdb", ":memory:?threads="+strconv.Itoa(threads)+"&autoload_known_extensions=false&autoinstall_known_extensions=false&allow_unsigned_extensions=false&allow_community_extensions=false")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, query := range []string{"SET memory_limit=" + literal(e.MemoryLimit), "SET max_temp_directory_size=" + literal(e.SpillLimit), "SET temp_directory=" + literal(spill), "SET allowed_directories=[" + literal(e.root+string(os.PathSeparator)) + "]", "SET enable_external_access=false", "SET preserve_insertion_order=false", "SET TimeZone='UTC'", "SET lock_configuration=true"} {
		if _, err = db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("configure analytics: %w", err)
		}
	}
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if analyticsHeadroom(e.root) != nil {
					limitCancel(errResourceLimit)
					return
				}
			}
		}
	}()
	err = operation(ctx, db)
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return err
}

func analyticsHeadroom(root string) error {
	if err := cgroupHeadroom("/sys/fs/cgroup"); err != nil {
		return err
	}
	free, err := diskspace.AvailablePath(root)
	if err != nil {
		return fmt.Errorf("cannot measure analytics disk headroom: %w", err)
	}
	if free < 512<<20 {
		return errResourceLimit
	}
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return fmt.Errorf("cannot measure controller RSS: %w", err)
	}
	rss, err := p.MemoryInfo()
	if err != nil {
		return fmt.Errorf("cannot measure controller RSS: %w", err)
	}
	if rss.RSS > 4<<30 {
		return errResourceLimit
	}
	vm, err := mem.VirtualMemory()
	if err != nil {
		return fmt.Errorf("cannot measure controller memory headroom: %w", err)
	}
	if vm.Available < 512<<20 {
		return errResourceLimit
	}
	return nil
}

func (e *Engine) childNativePreview(ctx context.Context, path string) (Preview, error) {
	path, err := e.authorizedPath(path)
	if err != nil {
		return Preview{}, err
	}
	schema, err := analysis.InspectParquet(path)
	if err != nil {
		return Preview{}, err
	}
	result := Preview{Schema: schema, Samples: []map[string]any{}}
	err = e.withDB(ctx, func(ctx context.Context, db *sql.DB) error {
		// Redact variable-width and nested values inside DuckDB, before the Go
		// driver could materialize an unbounded string, list, or struct cell.
		projection := make([]string, 0, len(schema.Columns))
		for _, c := range schema.Columns {
			expr := "'[redacted]'"
			if !c.Repeated && len(c.PathComponents) == 1 && !credential.SensitiveName(c.Path) {
				switch c.PhysicalType {
				case "BOOLEAN", "INT32", "INT64", "FLOAT", "DOUBLE", "INT96":
					expr = column(c.PathComponents)
				}
			}
			projection = append(projection, expr+" AS "+column([]string{c.Path}))
		}
		if len(projection) == 0 {
			return errors.New("Parquet has no scalar columns")
		}
		rows, err := db.QueryContext(ctx, "SELECT "+strings.Join(projection, ",")+" FROM read_parquet("+literal(path)+", hive_partitioning=false) LIMIT 20")
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range dest {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return err
			}
			sample := map[string]any{}
			for i, name := range cols {
				switch v := values[i].(type) {
				case nil:
					sample[name] = nil
				case bool, int8, int16, int32, int64, uint8, uint16, uint32, uint64:
					sample[name] = v
				case float32:
					if !math.IsInf(float64(v), 0) && !math.IsNaN(float64(v)) {
						sample[name] = v
					} else {
						sample[name] = "non-finite"
					}
				case float64:
					if !math.IsInf(v, 0) && !math.IsNaN(v) {
						sample[name] = v
					} else {
						sample[name] = "non-finite"
					}
				case time.Time:
					sample[name] = v.UTC()
				default:
					sample[name] = "[redacted]"
				}
			}
			result.Samples = append(result.Samples, sample)
		}
		return rows.Err()
	})
	return result, err
}

func timestampExpression(mapping Mapping) string {
	c := column(mapping.Timestamp)
	if mapping.TimestampEncoding == "timestamp_offset" {
		return "try_cast(" + c + " AS TIMESTAMPTZ)"
	}
	if mapping.TimestampEncoding == "timestamp" {
		// Offset-aware columns retain their absolute instant. Text with an
		// explicit offset requires the corresponding mapping rather than
		// silently dropping its offset during a TIMESTAMP cast.
		return "CASE WHEN typeof(" + c + ")='TIMESTAMP WITH TIME ZONE' THEN try_cast(" + c + " AS TIMESTAMPTZ) WHEN regexp_matches(try_cast(" + c + " AS VARCHAR), '(Z|[+-][0-9]{2}:?[0-9]{2})$') THEN NULL ELSE try_cast(" + c + " AS TIMESTAMP) AT TIME ZONE " + literal(mapping.Timezone) + " END"
	}
	scale := map[string]string{"unix_s": "1", "unix_ms": "1000", "unix_us": "1000000", "unix_ns": "1000000000"}[mapping.TimestampEncoding]
	return "TRY(to_timestamp(try_cast(" + c + " AS DOUBLE)/" + scale + "))"
}

func (e *Engine) childNativeNormalize(ctx context.Context, request NormalizeRequest) (result NormalizeResult, err error) {
	if request.ProjectID == "" || len(request.ProjectID) > 256 || request.DatasetID == "" || len(request.DatasetID) > 256 || len(request.Files) == 0 || len(request.Files) > 1024 || len(request.Files)*len(request.Mapping.Measures) > 1024 {
		return result, errors.New("invalid normalization identity or file count")
	}
	paths := make([]string, len(request.Files))
	for i, p := range request.Files {
		paths[i], err = e.authorizedPath(p)
		if err != nil {
			return result, err
		}
		schema, err := analysis.InspectParquet(paths[i])
		if err != nil {
			return result, err
		}
		if err = request.Mapping.Validate(schema); err != nil {
			return result, fmt.Errorf("file %d: %w", i, err)
		}
		for _, c := range schema.Columns {
			if len(c.PathComponents) == 1 && c.PathComponents[0] == "file_row_number" {
				return result, errors.New("reserved Parquet file_row_number column prevents stable rejection provenance")
			}
		}
		result.InputRows += schema.Rows
	}
	dir, err := os.MkdirTemp(e.root, "normalized-")
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	err = e.withDB(ctx, func(ctx context.Context, db *sql.DB) error {
		for i, path := range paths {
			for j, measure := range request.Mapping.Measures {
				unit, scale, _ := unitScale(measure.Unit)
				name := literal(measure.Name)
				if len(measure.NameColumn) > 0 {
					name = "try_cast(" + column(measure.NameColumn) + " AS VARCHAR)"
				}
				value := column(measure.Value)
				projection := "SELECT file_row_number AS source_row, " + timestampExpression(request.Mapping) + " AS timestamp_utc, " + name + " AS metric_name, try_cast(" + value + " AS DOUBLE)*" + strconv.FormatFloat(scale, 'g', -1, 64) + " AS value, (" + value + " IS NULL) AS was_null FROM read_parquet(" + literal(path) + ", hive_partitioning=false, file_row_number=true)"
				if _, err := db.ExecContext(ctx, "CREATE OR REPLACE TEMP VIEW mapped AS "+projection); err != nil {
					return err
				}
				valid := "timestamp_utc IS NOT NULL AND isfinite(timestamp_utc) AND timestamp_utc>=TIMESTAMPTZ '0001-01-01 00:00:00+00' AND timestamp_utc<TIMESTAMPTZ '10000-01-01 00:00:00+00' AND metric_name IS NOT NULL AND length(trim(metric_name))>=1 AND octet_length(encode(metric_name))<=256 AND (was_null OR (value IS NOT NULL AND isfinite(value)))"
				var accepted, rejected int64
				if err := db.QueryRowContext(ctx, "SELECT count(*) FILTER(WHERE "+valid+"), count(*) FILTER(WHERE NOT coalesce(("+valid+"),false)) FROM mapped").Scan(&accepted, &rejected); err != nil {
					return err
				}
				result.Accepted += accepted
				result.Rejected += rejected
				if rejected > 0 {
					summary := RejectionSummary{File: i, Measure: j, Count: rejected}
					rows, err := db.QueryContext(ctx, "SELECT source_row FROM mapped WHERE NOT coalesce(("+valid+"),false) ORDER BY source_row LIMIT 100")
					if err != nil {
						return err
					}
					for rows.Next() {
						var row int64
						if err = rows.Scan(&row); err != nil {
							rows.Close()
							return err
						}
						summary.Rows = append(summary.Rows, row)
					}
					err = rows.Err()
					rows.Close()
					if err != nil {
						return err
					}
					result.Rejections = append(result.Rejections, summary)
					if request.AllowRejected {
						out := filepath.Join(dir, fmt.Sprintf("reject-%04d-%02d.parquet", i, j))
						query := "COPY (SELECT source_row, " + strconv.Itoa(i) + " AS source_file, " + strconv.Itoa(j) + " AS mapped_measure, 'invalid_timestamp_name_or_value' AS reason FROM mapped WHERE NOT coalesce((" + valid + "),false)) TO " + literal(out) + " (FORMAT PARQUET, COMPRESSION ZSTD)"
						if _, err = db.ExecContext(ctx, query); err != nil {
							return err
						}
						if err = syncNormalizedFile(out); err != nil {
							return err
						}
						result.RejectionFiles = append(result.RejectionFiles, out)
					}
				}
				if rejected > 0 && !request.AllowRejected {
					return fmt.Errorf("file %d measure %d contains %d rejected rows; explicit acceptance required", i, j, rejected)
				}
				out := filepath.Join(dir, fmt.Sprintf("part-%04d-%02d.parquet", i, j))
				query := "COPY (SELECT " + literal(analysis.SchemaVersion) + " AS schema_version, " + literal(request.ProjectID) + " AS project_id, " + literal(request.DatasetID) + " AS dataset_id, " + literal(request.DatasetID) + " AS source, timestamp_utc, metric_name, " + literal(measure.Kind) + " AS kind, " + literal(unit) + " AS unit, value, CASE WHEN was_null THEN 'missing' ELSE 'valid' END AS quality FROM mapped WHERE " + valid + ") TO " + literal(out) + " (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 122880)"
				if _, err := db.ExecContext(ctx, query); err != nil {
					return err
				}
				if err = syncNormalizedFile(out); err != nil {
					return err
				}
				result.Files = append(result.Files, out)
			}
		}
		return nil
	})
	return result, err
}

func (e *Engine) childNativeSeries(ctx context.Context, files []string, q SeriesQuery) (points []analysis.MetricPoint, err error) {
	if err = q.Validate(); err != nil {
		return nil, err
	}
	if len(files) == 0 || len(files) > 1024 {
		return nil, errors.New("analytical file count exceeds limit")
	}
	paths := make([]string, len(files))
	for i, p := range files {
		path, err := e.authorizedPath(p)
		if err != nil {
			return nil, err
		}
		paths[i] = literal(path)
	}
	points = []analysis.MetricPoint{}
	err = e.withDB(ctx, func(ctx context.Context, db *sql.DB) error {
		table := "read_parquet([" + strings.Join(paths, ",") + "], hive_partitioning=false)"
		filter := "project_id=" + literal(q.ProjectID) + " AND dataset_id=" + literal(q.DatasetID) + " AND metric_name=" + literal(q.Metric) + " AND timestamp_utc >= TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano)) + " AND timestamp_utc < TIMESTAMPTZ " + literal(q.To.UTC().Format(time.RFC3339Nano))
		rows, err := db.QueryContext(ctx, "SELECT DISTINCT kind,unit FROM "+table+" WHERE "+filter+" LIMIT 2")
		if err != nil {
			return err
		}
		var kind, unit string
		count := 0
		for rows.Next() {
			count++
			if err := rows.Scan(&kind, &unit); err != nil {
				rows.Close()
				return err
			}
		}
		scanErr := rows.Err()
		rows.Close()
		if scanErr != nil {
			return scanErr
		}
		if count == 0 {
			return nil
		}
		if count != 1 {
			return errors.New("incompatible metric kinds or units prevent overlay")
		}
		if err := analysis.ValidateScalarAggregation(kind, q.Aggregation); err != nil {
			return err
		}
		lineage, err := analysis.ScalarBucketDerivation(analysis.MetricPoint{Kind: kind}, q.BucketSeconds, q.Aggregation)
		if err != nil {
			return err
		}
		origin := "TIMESTAMPTZ " + literal(q.From.UTC().Format(time.RFC3339Nano))
		bucket := "(" + origin + " + floor(epoch(timestamp_utc-" + origin + ")/" + strconv.Itoa(q.BucketSeconds) + ")*" + strconv.Itoa(q.BucketSeconds) + "*INTERVAL '1 second')"
		agg := map[string]string{"avg": "avg(value)", "min": "min(value)", "max": "max(value)", "sum": "sum(value)", "count": "count(value)", "p50": "quantile_cont(value,0.5)", "p95": "quantile_cont(value,0.95)", "p99": "quantile_cont(value,0.99)"}[q.Aggregation]
		from := table + " WHERE " + filter
		if q.Aggregation == "count" {
			unit = "count"
		}
		if q.Aggregation == "rate" {
			// Reset intervals and duplicate timestamps are explicitly missing.
			// Collapse duplicate instants to missing before differencing. Missing
			// samples break the chain, and any reset invalidates its entire bucket.
			from = "(SELECT timestamp_utc, CASE WHEN value>=lag(value) OVER w AND timestamp_utc>lag(timestamp_utc) OVER w THEN (value-lag(value) OVER w)/epoch(timestamp_utc-lag(timestamp_utc) OVER w) ELSE NULL END AS value FROM (SELECT timestamp_utc, CASE WHEN count(*)=1 AND count(value)=1 THEN min(value) ELSE NULL END AS value FROM " + table + " WHERE " + filter + " GROUP BY timestamp_utc) WINDOW w AS (ORDER BY timestamp_utc))"
			agg = "CASE WHEN count(value)=count(*) THEN avg(value) ELSE NULL END"
			unit += "/s"
		}
		if q.Aggregation != "rate" {
			// A partially missing bucket is incomplete evidence, including count.
			agg = "CASE WHEN count(value)=count(*) THEN " + agg + " ELSE NULL END"
		}
		rows, err = db.QueryContext(ctx, "SELECT "+bucket+" AS bucket, "+agg+" FROM "+from+" GROUP BY bucket ORDER BY bucket LIMIT 10001")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var at time.Time
			var value sql.NullFloat64
			if err := rows.Scan(&at, &value); err != nil {
				return err
			}
			if len(points) >= analysis.MaxSeriesPoints {
				return errors.New("analytical result exceeds point limit")
			}
			p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: q.ProjectID, DatasetID: q.DatasetID, Source: q.DatasetID, TimestampUTC: at.UTC(), MetricName: q.Metric, Kind: analysis.KindGauge, InputKind: kind, Derivation: append([]analysis.DerivationStep(nil), lineage...), Unit: unit, Quality: analysis.QualityMissing}
			if value.Valid {
				v := value.Float64
				p.Value = &v
				p.Quality = analysis.QualityValid
			}
			if err := p.Validate(); err != nil {
				return err
			}
			points = append(points, p)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Explicit empty buckets prevent charts from drawing across missing data.
		byTime := make(map[time.Time]analysis.MetricPoint, len(points))
		for _, p := range points {
			byTime[p.TimestampUTC] = p
		}
		points = points[:0]
		for at := q.From.UTC(); at.Before(q.To); at = at.Add(time.Duration(q.BucketSeconds) * time.Second) {
			p, ok := byTime[at]
			if !ok {
				p = analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: q.ProjectID, DatasetID: q.DatasetID, Source: q.DatasetID, TimestampUTC: at, MetricName: q.Metric, Kind: analysis.KindGauge, InputKind: kind, Derivation: append([]analysis.DerivationStep(nil), lineage...), Unit: unit, Quality: analysis.QualityMissing}
			}
			points = append(points, p)
		}
		return nil
	})
	return points, err
}
