package duckdb

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestWorkerSeriesBoundsAdmitOnlyCanonicalNativeCasts(t *testing.T) {
	for _, from := range []time.Time{time.Unix(1700000000, 123456000).UTC(), time.Unix(-4, 123456000).UTC()} {
		for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			q := SeriesQuery{From: from.Add(delta), To: from.Add(time.Second + delta)}
			expected := workerSeriesBounds{From: q.From.UTC().Truncate(time.Microsecond), To: q.To.UTC().Truncate(time.Microsecond)}
			got, err := workerAdmitSeriesBounds(q, &expected)
			if err != nil || !got.From.Equal(expected.From) || !got.To.Equal(expected.To) {
				t.Fatal("actual canonical cast refused", got, err)
			}
			_, err = workerAdmitSeriesBounds(q, nil)
			if (delta == 0) != (err == nil) {
				t.Fatal("proof absence did not require canonical endpoints", delta, err)
			}
			for name, mutate := range map[string]func(*workerSeriesBounds){
				"submicro-from": func(b *workerSeriesBounds) { b.From = b.From.Add(time.Nanosecond) },
				"submicro-to":   func(b *workerSeriesBounds) { b.To = b.To.Add(time.Nanosecond) },
				"earlier-from":  func(b *workerSeriesBounds) { b.From = b.From.Add(-time.Microsecond) },
				"later-from":    func(b *workerSeriesBounds) { b.From = b.From.Add(time.Microsecond) },
				"earlier-to":    func(b *workerSeriesBounds) { b.To = b.To.Add(-time.Microsecond) },
				"later-to":      func(b *workerSeriesBounds) { b.To = b.To.Add(time.Microsecond) },
				"non-UTC":       func(b *workerSeriesBounds) { b.From = b.From.In(time.FixedZone("zero-alias", 0)) },
				"wrong-offset":  func(b *workerSeriesBounds) { b.To = b.To.Add(time.Hour) },
			} {
				t.Run(name, func(t *testing.T) {
					forged := expected
					mutate(&forged)
					if _, err := workerAdmitSeriesBounds(q, &forged); err == nil {
						t.Fatal("forged cast accepted", name)
					}
				})
			}
		}
	}
}

func TestWorkerSeriesBoundsProofRetainsBucketAdmissionAndOperationScope(t *testing.T) {
	b := workerTestBootstrap(t)
	from := time.Unix(-4, 123456001).UTC()
	q := SeriesQuery{ProjectID: "project", DatasetID: "dataset", Metric: "latency", SourceID: "dataset", From: from, To: from.Add(time.Second), BucketSeconds: 1, Aggregation: "avg"}
	request := workerRequest{Series: &workerSeriesOperation{InputHandles: []string{b.Handles[0].Handle}, Query: q}}
	bounds := workerSeriesBounds{From: from.Truncate(time.Microsecond), To: from.Add(time.Second).Truncate(time.Microsecond)}
	value := 7.0
	lineage, _ := analysis.ScalarBucketDerivation(analysis.MetricPoint{Kind: analysis.KindObservation}, 1, "avg")
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: q.ProjectID, DatasetID: q.DatasetID, Source: q.DatasetID, TimestampUTC: bounds.From, MetricName: q.Metric, Kind: analysis.KindGauge, InputKind: analysis.KindObservation, Derivation: lineage, Value: &value, Unit: "ms", Quality: analysis.QualityValid}
	points := []analysis.MetricPoint{point}
	result := workerResult{Status: "ok", Points: &points, SeriesBounds: &bounds}
	if result.validate(b, request) != nil {
		t.Fatal("canonical point outside raw ns window was refused")
	}
	frame := workerFrame{Identity: b.Identity, Kind: "result", Result: &result}
	body, err := marshalWorkerJSON(frame)
	if err != nil {
		t.Fatal(err)
	}
	var decoded workerFrame
	if err = decodeWorkerJSON(body, &decoded); err != nil || decoded.Result.validate(b, request) != nil {
		t.Fatal("canonical cast proof failed strict IPC round trip", err)
	}
	unproven := result
	unproven.SeriesBounds = nil
	if unproven.validate(b, request) == nil {
		t.Fatal("unproven raw submicro window admitted")
	}
	offGrid := point
	offGrid.TimestampUTC = q.From
	offPoints := []analysis.MetricPoint{offGrid}
	offResult := result
	offResult.Points = &offPoints
	if offResult.validate(b, request) == nil {
		t.Fatal("raw nanosecond bucket admitted despite canonical cast proof")
	}
	normalize := workerTestNormalizeResult(b)
	normalize.SeriesBounds = &bounds
	if normalize.validate(b, workerTestRequest(b)) == nil {
		t.Fatal("normalization manufactured series cast proof")
	}
	preview := workerResult{Status: "ok", Preview: &workerPreviewResult{}, SeriesBounds: &bounds}
	if preview.validate(b, workerRequest{Preview: &workerPreviewOperation{InputHandle: b.Handles[0].Handle}}) == nil {
		t.Fatal("preview manufactured series cast proof")
	}
	failed := workerResult{Status: "error", Code: "internal", SeriesBounds: &bounds}
	if failed.validate(b, request) == nil {
		t.Fatal("failed operation carried authoritative cast proof")
	}
	emptyPoints := []analysis.MetricPoint{}
	emptyQuery := q
	emptyQuery.To = emptyQuery.From.Add(time.Nanosecond)
	collapsed := workerSeriesBounds{From: bounds.From, To: bounds.From}
	emptyResult := workerResult{Status: "ok", Points: &emptyPoints, SeriesBounds: &collapsed}
	emptyRequest := workerRequest{Series: &workerSeriesOperation{InputHandles: request.Series.InputHandles, Query: emptyQuery}}
	if emptyResult.validate(b, emptyRequest) != nil {
		t.Fatal("canonical collapsed window refused instead of returning empty")
	}
}
