//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeNormalizeSnapshotsMutableInputsBeforeHashAndStage(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000::BIGINT AS ts, 7::DOUBLE AS value")
	input := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "approved", Kind: analysis.KindGauge, Unit: "count"}}}}
	entered, release := make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var once sync.Once
	e.workerBeforeHash = func() { once.Do(func() { close(entered); <-release }) }
	type outcome struct {
		result NormalizeResult
		err    error
	}
	done := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { result, err := e.Normalize(ctx, input); done <- outcome{result, err} }()
	select {
	case <-entered:
	case got := <-done:
		t.Fatal("normalization returned before the admitted hash barrier", got.err)
	case <-ctx.Done():
		t.Fatal("normalization never reached admitted hash barrier")
	}
	input.Files[0] = "/unapproved/outside.parquet"
	input.Mapping.Timestamp[0] = "changed_timestamp"
	input.Mapping.Measures[0].Value[0] = "changed_value"
	input.Mapping.Measures[0].Name = "changed_name"
	unblock()
	var got outcome
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("normalization did not finish after barrier")
	}
	if got.err != nil || got.result.Accepted != 1 {
		t.Fatal("caller mutation changed approved operation", got.result, got.err)
	}
	e.workerBeforeHash = nil
	got.result = retainNativeFixtureOutputs(t, e, got.result)
	points, err := e.Series(ctx, got.result.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "approved", From: time.Unix(1700000000, 0), To: time.Unix(1700000001, 0), BucketSeconds: 1, Aggregation: "avg"})
	if err != nil || len(points) != 1 || points[0].Value == nil || *points[0].Value != 7 {
		t.Fatal("approved immutable mapping changed", points, err)
	}
}
