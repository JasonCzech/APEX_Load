//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeIdentityMappingSnapshotsBeforeHashBarrier(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000 AS ts, 7::DOUBLE AS value, 'agent' AS source, 'host' AS host, 'service' AS service, 'one' AS label")
	input := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: Column{"source"}, Host: Column{"host"}, Service: Column{"service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "group", Value: Column{"label"}}}}}}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	e.workerBeforeHash = func() { once.Do(func() { close(entered); <-release }) }
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	type outcome struct {
		result NormalizeResult
		err    error
	}
	done := make(chan outcome, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		unblock()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("exact normalization caller remains unjoined after fixture release")
		}
	})
	go func() {
		defer close(joined)
		result, err := e.Normalize(ctx, input)
		done <- outcome{result, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("native identity hash barrier not reached")
	}
	input.Files[0] = "/outside/unapproved.parquet"
	input.Mapping.Identity.Source[0] = "changed_source"
	input.Mapping.Identity.Host[0] = "changed_host"
	input.Mapping.Identity.Service[0] = "changed_service"
	input.Mapping.Identity.Dimensions[0].Name = "changed_group"
	input.Mapping.Identity.Dimensions[0].Value[0] = "changed_label"
	unblock()
	var got outcome
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("native identity normalization did not return")
	}
	if got.err != nil || got.result.Accepted != 1 {
		t.Fatal("identity mapping borrowed caller input", got.result, got.err)
	}
	e.workerBeforeHash = nil
	got.result = retainNativeFixtureOutputs(t, e, got.result)
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", IdentitySchemaVersion: 2, SourceID: "agent", Host: "host", Service: "service", Dimensions: map[string]string{"group": "one"}, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
	points, err := e.Series(ctx, got.result.Files, q)
	if err != nil || len(points) != 1 || points[0].Source != "agent" || points[0].Host != "host" || points[0].Service != "service" || points[0].Dimensions["group"] != "one" {
		t.Fatal("snapshot did not preserve exact identity", points, err)
	}
}

func TestNativeSeriesFiltersSnapshotBeforeHashBarrier(t *testing.T) {
	e, path := nativeFixture(t, "SELECT "+literal(analysis.SchemaVersion)+" AS schema_version, 'p' AS project_id, 'd' AS dataset_id, 'agent' AS source, 'host' AS host, 'service' AS service, 2 AS identity_version, '{\"group\":\"one\"}' AS dimensions_json, to_timestamp(1700000000) AS timestamp_utc, 'v' AS metric_name, 'gauge' AS kind, 'count' AS unit, 7::DOUBLE AS value, 'valid' AS quality")
	files := []string{path}
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", IdentitySchemaVersion: 2, SourceID: "agent", Host: "host", Service: "service", Dimensions: map[string]string{"group": "one"}, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	e.workerBeforeHash = func() { once.Do(func() { close(entered); <-release }) }
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	type outcome struct {
		points []analysis.MetricPoint
		err    error
	}
	done := make(chan outcome, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		unblock()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("exact series caller remains unjoined after fixture release")
		}
	})
	go func() {
		defer close(joined)
		points, err := e.Series(ctx, files, q)
		done <- outcome{points, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("series hash barrier not reached")
	}
	files[0] = "/outside/unapproved.parquet"
	q.Dimensions["group"] = "changed"
	unblock()
	var got outcome
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("series did not return")
	}
	if got.err != nil || len(got.points) != 1 || got.points[0].Value == nil || *got.points[0].Value != 7 || got.points[0].Dimensions["group"] != "one" {
		t.Fatal("series filters borrowed caller input", got.points, got.err)
	}
}
