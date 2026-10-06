package duckdb

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func identitySeriesQueryFixture() SeriesQuery {
	return SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "latency", IdentitySchemaVersion: 2, SourceID: "agent", Host: "host", Service: "svc", Dimensions: map[string]string{"group": "one"}, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
}
func TestNativeSeriesIdentityFilterBoundsAndMissingKey(t *testing.T) {
	q := identitySeriesQueryFixture()
	if q.Validate() != nil {
		t.Fatal("valid identity query rejected")
	}
	p := analysis.MetricPoint{Source: "agent", Host: "host", Service: "svc", Dimensions: map[string]string{"group": "one", "empty": ""}}
	if !nativeSeriesIdentityMatches(q, p) {
		t.Fatal("actual identity rejected")
	}
	for name, change := range map[string]func(*SeriesQuery){"legacy": func(q *SeriesQuery) { q.IdentitySchemaVersion = 1 }, "version": func(q *SeriesQuery) { q.IdentitySchemaVersion = 3 }, "long_host": func(q *SeriesQuery) { q.Host = strings.Repeat("h", 257) }, "invalid_source": func(q *SeriesQuery) { q.SourceID = "a\x00b" }, "long_dimension": func(q *SeriesQuery) { q.Dimensions["group"] = strings.Repeat("x", 129) }} {
		t.Run(name, func(t *testing.T) {
			q := identitySeriesQueryFixture()
			change(&q)
			if q.Validate() == nil {
				t.Fatal("invalid identity query accepted")
			}
		})
	}
	q.Dimensions = map[string]string{"empty": ""}
	if !nativeSeriesIdentityMatches(q, p) {
		t.Fatal("declared empty label not matched")
	}
	delete(p.Dimensions, "empty")
	if nativeSeriesIdentityMatches(q, p) {
		t.Fatal("missing label matched empty label")
	}
}
func TestWorkerSeriesV2ResultPreservesActualIdentityAndRequestedFilters(t *testing.T) {
	b := workerTestBootstrap(t)
	q := identitySeriesQueryFixture()
	request := workerRequest{Series: &workerSeriesOperation{InputHandles: []string{b.Handles[0].Handle}, Query: q}}
	value := 1.0
	lineage, _ := analysis.ScalarBucketDerivation(analysis.MetricPoint{Kind: analysis.KindObservation}, 1, "avg")
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: q.ProjectID, DatasetID: q.DatasetID, Source: q.SourceID, Host: q.Host, Service: q.Service, Dimensions: map[string]string{"group": "one", "protocol": "grpc"}, TimestampUTC: q.From, MetricName: q.Metric, Kind: analysis.KindGauge, InputKind: analysis.KindObservation, Derivation: lineage, Value: &value, Unit: "ms", Quality: analysis.QualityValid}
	points := []analysis.MetricPoint{p}
	result := workerResult{Status: "ok", Points: &points}
	if result.validate(b, request) != nil {
		t.Fatal("v2 identity result rejected")
	}
	for _, mutate := range []func(*analysis.MetricPoint){func(p *analysis.MetricPoint) { p.Source = "foreign" }, func(p *analysis.MetricPoint) { p.Host = "other" }, func(p *analysis.MetricPoint) { p.Service = "other" }, func(p *analysis.MetricPoint) { p.Dimensions = map[string]string{} }, func(p *analysis.MetricPoint) { p.Dimensions = map[string]string{"group": "two"} }, func(p *analysis.MetricPoint) { p.RunID = "injected-managed-run" }} {
		copy := p
		mutate(&copy)
		points[0] = copy
		if result.validate(b, request) == nil {
			t.Fatal("child identity escaped approved filter")
		}
	}
	q.To = q.From.Add(2 * time.Second)
	request.Series.Query = q
	second := p
	second.TimestampUTC = q.From.Add(time.Second)
	second.Dimensions = map[string]string{"group": "one", "protocol": "http"}
	points = []analysis.MetricPoint{p, second}
	if result.validate(b, request) == nil {
		t.Fatal("changing identity across buckets accepted")
	}
}

func TestWorkerSeriesV2IdentityCannotForgeGovernedProviderProvenance(t *testing.T) {
	bootstrap := workerTestBootstrap(t)
	query := identitySeriesQueryFixture()
	request := workerRequest{Series: &workerSeriesOperation{InputHandles: []string{bootstrap.Handles[0].Handle}, Query: query}}
	value := 1.0
	lineage, err := analysis.ScalarBucketDerivation(analysis.MetricPoint{Kind: analysis.KindObservation}, 1, "avg")
	if err != nil {
		t.Fatal(err)
	}
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: query.ProjectID, DatasetID: query.DatasetID, Source: query.SourceID, Host: query.Host, Service: query.Service, Dimensions: map[string]string{"group": "one"}, TimestampUTC: query.From, MetricName: query.Metric, Kind: analysis.KindGauge, InputKind: analysis.KindObservation, Derivation: lineage, Value: &value, Unit: "ms", Quality: analysis.QualityValid}
	points := []analysis.MetricPoint{point}
	if err := (workerResult{Status: "ok", Points: &points}).validate(bootstrap, request); err != nil {
		t.Fatal("approved imported v2 identity refused", err)
	}
	point.Provider = &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "governed-provider", Mode: "interval_bucket", BucketStart: query.From, BucketEnd: query.To, ResolutionSeconds: 1, Statistic: "avg"}
	if err := point.Validate(); err != nil {
		t.Fatal("otherwise valid provider fixture", err)
	}
	points[0] = point
	if (workerResult{Status: "ok", Points: &points}).validate(bootstrap, request) == nil {
		t.Fatal("native identity worker manufactured governed provider origin")
	}
}
