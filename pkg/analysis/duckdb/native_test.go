//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func nativeFixture(t *testing.T, query string) (*Engine, string) {
	t.Helper()
	e, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Fixture-only SQL uses an explicit approved single-thread configuration.
	// Production operations obtain the thread count from the owned bootstrap.
	e.workerThreads = 1
	e.MemoryLimit = nativeFixtureMemoryLimit
	e.SpillLimit = "256MiB"
	e.Timeout = 30 * time.Second
	t.Cleanup(func() {
		if err := e.ReleaseOutputs(); err != nil {
			t.Errorf("native physical output drain: %v", err)
		}
	})
	path := filepath.Join(e.root, "input.parquet")
	if err = e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, "COPY ("+query+") TO "+literal(path)+" (FORMAT PARQUET)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return e, path
}

func TestNativeNormalizePreviewQueryAndIsolation(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000000+i*1000 AS ts, (i+1)::DOUBLE/1000 AS latency, 'secret' AS credential FROM range(10) t(i)")
	preview, err := e.Preview(context.Background(), path)
	if err != nil || preview.Schema.Rows != 10 || len(preview.Samples) != 10 {
		t.Fatalf("preview %+v %v", preview, err)
	}
	if preview.Samples[0]["credential"] != "[redacted]" {
		t.Fatal("text preview disclosed input")
	}
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []Measure{{Value: Column{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "s"}}}
	normalized, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil || normalized.Accepted != 10 || normalized.Rejected != 0 {
		t.Fatalf("normalize %+v %v", normalized, err)
	}
	normalized = retainNativeFixtureOutputs(t, e, normalized)
	query := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "latency", From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000010000), BucketSeconds: 10, Aggregation: "p95"}
	points, err := e.Series(context.Background(), normalized.Files, query)
	if err != nil || len(points) != 1 || points[0].Value == nil || *points[0].Value < 9 || *points[0].Value > 10 || points[0].Unit != "ms" {
		t.Fatalf("series %+v %v", points, err)
	}
	query.ProjectID = "foreign"
	points, err = e.Series(context.Background(), normalized.Files, query)
	if err != nil || len(points) != 0 {
		t.Fatalf("foreign project %+v %v", points, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Preview(context.Background(), outside); err == nil {
		t.Fatal("outside path accepted")
	}
}

func TestNativeRejectsInvalidRowsAndSummaryPercentiles(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000000::BIGINT AS ts, 'bad' AS value UNION ALL SELECT 1700000001000, '4'")
	request := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "p95", Kind: analysis.KindSummary, Unit: "ms"}}}}
	if _, err := e.Normalize(context.Background(), request); err == nil {
		t.Fatal("invalid row silently discarded")
	}
	request.AllowRejected = true
	result, err := e.Normalize(context.Background(), request)
	if err != nil || result.Accepted != 1 || result.Rejected != 1 {
		t.Fatalf("explicit rejections %+v %v", result, err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "p95", From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000010000), BucketSeconds: 10, Aggregation: "p95"}
	if _, err := e.Series(context.Background(), result.Files, q); err == nil {
		t.Fatal("percentile derived from summary")
	}
}

func TestNativeCancellationAndFileRestrictions(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1 AS ts, 2 AS value")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Preview(ctx, path); err == nil {
		t.Fatal("cancelled operation succeeded")
	}
	err := e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "SET enable_external_access=true"); err == nil {
			t.Error("configuration was mutable")
		}
		if _, err := db.ExecContext(ctx, "SELECT * FROM read_csv('/etc/passwd')"); err == nil {
			t.Error("external filesystem was readable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeTimestampOffsetsAndMissingCounterIntervals(t *testing.T) {
	t.Run("offset", func(t *testing.T) {
		e, path := nativeFixture(t, "SELECT '2026-01-01T12:00:00-05:00' AS ts, 1::DOUBLE AS value")
		m := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}}
		if _, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m}); err == nil {
			t.Fatal("offset text silently treated as local wall time")
		}
		m.TimestampEncoding = "timestamp_offset"
		r, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m})
		if err != nil {
			t.Fatal(err)
		}
		r = retainNativeFixtureOutputs(t, e, r)
		at := time.Date(2026, 1, 1, 17, 0, 0, 0, time.UTC)
		points, err := e.Series(context.Background(), r.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: at, To: at.Add(time.Minute), BucketSeconds: 1, Aggregation: "avg"})
		if err != nil || len(points) != 60 || !points[0].TimestampUTC.Equal(at) {
			t.Fatalf("offset conversion: %+v %v", points, err)
		}
	})
	t.Run("counter", func(t *testing.T) {
		e, path := nativeFixture(t, "SELECT 1700000000+i AS ts, CASE i WHEN 0 THEN 2 WHEN 1 THEN 4 WHEN 2 THEN NULL WHEN 3 THEN 8 WHEN 4 THEN 1 WHEN 5 THEN 3 END::DOUBLE AS value FROM range(6) t(i)")
		m := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindCounter, Unit: "count"}}}
		r, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m})
		if err != nil {
			t.Fatal(err)
		}
		r = retainNativeFixtureOutputs(t, e, r)
		points, err := e.Series(context.Background(), r.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: time.Unix(1700000000, 0), To: time.Unix(1700000006, 0), BucketSeconds: 1, Aggregation: "rate"})
		if err != nil || len(points) != 6 {
			t.Fatalf("rates: %+v %v", points, err)
		}
		for i, p := range points {
			valid := i == 1 || i == 5
			if valid && (p.Value == nil || *p.Value != 2) || !valid && (p.Value != nil || p.Quality != analysis.QualityMissing) {
				t.Errorf("counter[%d]=%+v", i, p)
			}
		}
	})
}

func TestNativeRejectionProvenanceAndNullBuckets(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000+i AS ts, CASE i WHEN 0 THEN 'bad' WHEN 1 THEN NULL ELSE '4' END AS value, 123456::BIGINT AS password FROM range(3) t(i)")
	preview, err := e.Preview(context.Background(), path)
	if err != nil || preview.Samples[0]["password"] != "[redacted]" {
		t.Fatalf("numeric credential: %+v %v", preview, err)
	}
	request := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, AllowRejected: true, Mapping: Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}}}
	r, err := e.Normalize(context.Background(), request)
	if err != nil || len(r.Rejections) != 1 || len(r.RejectionFiles) != 1 || r.Rejections[0].Count != 1 || len(r.Rejections[0].Rows) != 1 || r.Rejections[0].Rows[0] != 0 {
		t.Fatalf("provenance: %+v %v", r, err)
	}
	r = retainNativeFixtureOutputs(t, e, r)
	err = e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		var row int64
		if err := db.QueryRowContext(ctx, "SELECT source_row FROM read_parquet("+literal(r.RejectionFiles[0])+")").Scan(&row); err != nil {
			return err
		}
		if row != 0 {
			t.Errorf("rejected row %d", row)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Unix(1700000000, 0)
	for _, aggregation := range []string{"avg", "count"} {
		p, err := e.Series(context.Background(), r.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: from, To: from.Add(4 * time.Second), BucketSeconds: 1, Aggregation: aggregation})
		if err != nil || len(p) != 4 {
			t.Fatalf("buckets: %+v %v", p, err)
		}
		for i, v := range p {
			if i != 2 && (v.Value != nil || v.Quality != analysis.QualityMissing) || i == 2 && v.Value == nil {
				t.Errorf("%s bucket %d: %+v", aggregation, i, v)
			}
		}
	}
}

func TestNativeSeriesPreservesBucketStatisticIdentity(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000000 AS ts, 1::DOUBLE AS value UNION ALL SELECT 1700000000100, 101::DOUBLE")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}}}
	normalized, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	normalized = retainNativeFixtureOutputs(t, e, normalized)
	for _, aggregate := range []string{"avg", "p95"} {
		q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "latency", From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000002000), BucketSeconds: 1, Aggregation: aggregate}
		points, err := e.Series(context.Background(), normalized.Files, q)
		if err != nil || len(points) != 2 {
			t.Fatalf("series=%+v err=%v", points, err)
		}
		for _, point := range points {
			if len(point.Derivation) != 1 || point.Derivation[0].Operation != aggregate || point.Derivation[0].BucketSeconds != 1 {
				t.Fatalf("statistic identity lost: %+v", point)
			}
		}
		points[0].Derivation[0].Operation = "mutated"
		if points[1].Derivation[0].Operation != aggregate {
			t.Fatal("missing bucket lineage aliases observed bucket")
		}
	}
}

func TestNativeCounterRatesRemainEpochUnqualified(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000000 AS ts, 10::DOUBLE AS value UNION ALL SELECT 1700000001000, 100::DOUBLE UNION ALL SELECT 1700000002000, 105::DOUBLE")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "requests", Kind: analysis.KindCounter, Unit: "count"}}}
	normalized, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	normalized = retainNativeFixtureOutputs(t, e, normalized)
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "requests", From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000003000), BucketSeconds: 1, Aggregation: "rate"}
	points, err := e.Series(context.Background(), normalized.Files, q)
	if err != nil || len(points) != 3 || points[1].Value == nil || *points[1].Value != 90 {
		t.Fatalf("legacy native rate=%+v error=%v", points, err)
	}
	for _, point := range points {
		if len(point.Derivation) != 2 || point.Derivation[0].Operation != "legacy_derived" || point.Derivation[0].Algorithm != "unknown_counter_epoch" {
			t.Fatalf("native counter epoch silently qualified: %+v", point)
		}
		point.Population, point.MetricVersion = analysis.PopulationInfrastructure, "requests.v1"
		got, err := analysis.EvaluateBaselineDelta(point, point, false)
		if err != nil || got.Value != nil || got.Reason != "unknown_measurement_semantics" {
			t.Fatalf("native raw counter rate qualified: %+v error=%v", got, err)
		}
	}
}

// Tests retain immutable fixture copies through verified reader descriptors,
// then physically release the producer before entering a later Series worker.
func retainNativeFixtureOutputs(t *testing.T, e *Engine, result NormalizeResult) NormalizeResult {
	t.Helper()
	copySet := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, path := range paths {
			reader, err := e.OpenOutput(path)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(e.root, "fixture-"+filepath.Base(path))
			file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				reader.Close()
				t.Fatal(err)
			}
			_, copyErr := io.Copy(file, reader)
			syncErr := file.Sync()
			fileCloseErr, readerCloseErr := file.Close(), reader.Close()
			if copyErr != nil || syncErr != nil || fileCloseErr != nil || readerCloseErr != nil {
				t.Fatal("fixture output physical copy", copyErr, syncErr, fileCloseErr, readerCloseErr)
			}
			out = append(out, destination)
		}
		return out
	}
	result.Files = copySet(result.Files)
	result.RejectionFiles = copySet(result.RejectionFiles)
	if err := e.ReleaseOutputs(); err != nil {
		t.Fatal(err)
	}
	return result
}

// This mirrors API geometry: the authorized source is copied into a registered
// staging directory, and a real worker's immutable executable/input/output lives
// below it. The parent watchdog must observe the full admitted geometry.
func TestNativeRegisteredStagingAndActualWorkerSnapshotShareApprovedDiskGeometry(t *testing.T) {
	fixture, original := nativeFixture(t, "SELECT 1700000000000 AS ts, 1::DOUBLE AS value")
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := workerParseBudget(nativeFixtureMemoryLimit, 2<<30)
	if err != nil {
		t.Fatal(err)
	}
	ctx, pipeline, err := ReserveNativePipeline(t.Context(), fixture.root, PipelinePlan{InputBytes: info.Size(), OutputBytes: 12, StagingBytes: info.Size(), MemoryBytes: memory, SpillBytes: 64 << 20, Runtime: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pipeline.Release()
		select {
		case <-pipeline.Done():
		case <-time.After(5 * time.Second):
			t.Error("registered parent did not join actual worker/staging cleanup")
		}
	})
	dir, err := pipeline.CreateStagingDirectory(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := pipeline.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(original)
	if err != nil {
		lease.Joined()
		t.Fatal(err)
	}
	lease.TrackOwner(source)
	staged := filepath.Join(dir, "input.parquet")
	out, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = lease.CloseOwner(source)
		lease.Joined()
		t.Fatal(err)
	}
	lease.TrackOwner(out)
	_, copyErr := io.Copy(out, source)
	closeErr := errors.Join(out.Sync(), lease.CloseOwner(out), lease.CloseOwner(source))
	lease.Joined()
	if err = errors.Join(copyErr, closeErr); err != nil {
		t.Fatal(err)
	}
	engine, err := NewForNativePipeline(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	attempt, request, err := engine.prepareWorkerBounded(ctx, []string{staged}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerFinishAttempt(attempt) })
	// Produce the actual verified private executable once, then pass that exact
	// launch to the controlled supervisor fixture rather than copying it twice.
	launch, err := workerProductionLaunch(attempt)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := attempt.root.Stat(workerExecutableSnapshotName())
	if err != nil || snapshot.Size() <= info.Size() {
		t.Fatal("real executable did not reproduce nested staging geometry", err)
	}
	probe, err := pipeline.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	quota, err := probe.pipelineDiskBytes()
	if err == nil {
		err = workerObservePipelineResources(probe, fixture.root, []string{dir}, quota, true)
	}
	probe.Joined()
	if err != nil || pipeline.Err() != nil {
		t.Fatal("real approved nested worker bytes exceeded staging-only cap", err, pipeline.Err())
	}
	options := engine.workerOptions()
	options.testLaunch = &launch
	result, err := runWorkerParent(attempt, request, options)
	if err != nil {
		t.Fatal("actual child failed combined parent geometry", err)
	}
	if result.result.Preview == nil || result.result.Preview.Schema.Rows != 1 {
		t.Fatal("real child preview did not reconcile", result.result)
	}
	if err = result.release(); err != nil {
		t.Fatal("actual child output/reap did not join", err)
	}
}

// A counter read for a controller-side counter derivation is each bucket's
// highest reading; without the flag a counter still needs rate conversion.
func TestNativeCounterReadingsForDerivations(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000000 AS ts, 10::DOUBLE AS value UNION ALL SELECT 1700000000500, 40::DOUBLE UNION ALL SELECT 1700000001000, 100::DOUBLE UNION ALL SELECT 1700000002000, 105::DOUBLE")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "requests", Kind: analysis.KindCounter, Unit: "count"}}}
	normalized, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	normalized = retainNativeFixtureOutputs(t, e, normalized)
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "requests", From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000003000), BucketSeconds: 1, Aggregation: "max"}
	if _, err := e.Series(context.Background(), normalized.Files, q); err == nil {
		t.Fatal("a display query read a counter's raw readings")
	}
	q.CounterReadings = true
	points, err := e.Series(context.Background(), normalized.Files, q)
	if err != nil || len(points) != 3 || points[0].Value == nil || *points[0].Value != 40 || points[2].Value == nil || *points[2].Value != 105 {
		t.Fatalf("counter readings=%+v error=%v", points, err)
	}
	for _, point := range points {
		if point.Kind != analysis.KindGauge || point.InputKind != analysis.KindCounter || point.Unit != "count" || point.Derivation[len(point.Derivation)-1].Algorithm != "counter_bucket_reading.v2" {
			t.Fatalf("counter reading point %+v", point)
		}
	}
}

// A counter reading that falls (a reset inside a bucket, or across from the
// previous bucket) makes its bucket missing rather than reading the
// pre-reset peak; one instant with two readings is a duplicate.
func TestNativeCounterReadingsMarkResetsAndDuplicates(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000000 AS ts, 990::DOUBLE AS value UNION ALL SELECT 1700000001000, 1000::DOUBLE UNION ALL SELECT 1700000001300, 1050::DOUBLE UNION ALL SELECT 1700000001500, 3::DOUBLE UNION ALL SELECT 1700000001700, 20::DOUBLE UNION ALL SELECT 1700000002000, 1200::DOUBLE UNION ALL SELECT 1700000003000, 1300::DOUBLE UNION ALL SELECT 1700000003000, 1310::DOUBLE UNION ALL SELECT 1700000004000, 5::DOUBLE UNION ALL SELECT 1700000005000, 9::DOUBLE")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "requests", Kind: analysis.KindCounter, Unit: "count"}}}
	normalized, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	normalized = retainNativeFixtureOutputs(t, e, normalized)
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "requests", From: time.UnixMilli(1700000000000), To: time.UnixMilli(1700000006000), BucketSeconds: 1, Aggregation: "max", CounterReadings: true}
	points, err := e.Series(context.Background(), normalized.Files, q)
	if err != nil || len(points) != 6 {
		t.Fatalf("counter readings=%+v error=%v", points, err)
	}
	want := []struct {
		value  float64
		reason string
	}{{990, ""}, {0, analysis.MissingCounterReset}, {1200, ""}, {0, analysis.MissingDuplicateCounterInstant}, {0, analysis.MissingCounterReset}, {9, ""}}
	for i, expected := range want {
		point := points[i]
		if expected.reason != "" {
			if point.Value != nil || point.MissingReason != expected.reason {
				t.Fatalf("bucket %d = %+v, want missing %s", i, point, expected.reason)
			}
			continue
		}
		if point.Value == nil || *point.Value != expected.value || point.MissingReason != "" {
			t.Fatalf("bucket %d = %+v, want %v", i, point, expected.value)
		}
	}
}
