//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeIdentityRetentionFilteringAndCounterSeparation(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000+floor(i/2)::BIGINT AS ts, CASE i WHEN 0 THEN 10 WHEN 1 THEN 100 WHEN 2 THEN 14 ELSE 200 END::DOUBLE AS value, struct_pack(source := 'agent', host := CASE WHEN i%2=0 THEN 'host-a' ELSE 'host-b' END, service := 'api', dimension := 'value''quoted') AS labels FROM range(4) t(i)")
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "counter", Kind: analysis.KindCounter, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: Column{"labels", "source"}, Host: Column{"labels", "host"}, Service: Column{"labels", "service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "a/b~\"c", Value: Column{"labels", "dimension"}}}}}
	normalized, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil || normalized.Accepted != 4 {
		t.Fatal(normalized, err)
	}
	normalized = retainNativeFixtureOutputs(t, e, normalized)
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "counter", IdentitySchemaVersion: 2, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000003, 0).UTC(), BucketSeconds: 1, Aggregation: "rate"}
	if _, err = e.Series(t.Context(), normalized.Files, q); err == nil {
		t.Fatal("heterogeneous counter owners silently merged")
	}
	q.SourceID, q.Host, q.Service = "agent", "host-a", "api"
	q.Dimensions = map[string]string{"a/b~\"c": "value'quoted"}
	points, err := e.Series(t.Context(), normalized.Files, q)
	if err != nil || len(points) != 3 || points[1].Value == nil || *points[1].Value != 4 {
		t.Fatal("filtered counter separation", points, err)
	}
	for _, p := range points {
		if p.Source != "agent" || p.Host != "host-a" || p.Service != "api" || p.Dimensions["a/b~\"c"] != "value'quoted" {
			t.Fatal("identity lost in observed/missing bucket", p)
		}
	}
	points[0].Dimensions["a/b~\"c"] = "mutated"
	if points[1].Dimensions["a/b~\"c"] != "value'quoted" {
		t.Fatal("missing/observed bucket dimensions alias")
	}
	q.Dimensions["a/b~\"c"] = "value'quoted' OR 1=1 --"
	points, err = e.Series(t.Context(), normalized.Files, q)
	if err != nil || len(points) != 0 {
		t.Fatal("dimension filter escaped literal scope", points, err)
	}
	q.IdentitySchemaVersion = 1
	q.SourceID, q.Host, q.Service = "", "", ""
	q.Dimensions = nil
	if _, err = e.Series(t.Context(), normalized.Files, q); err == nil {
		t.Fatal("v2 labels lost behind legacy query")
	}
}
func TestNativeIdentityInvalidRowsRemainRejectedProvenance(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000+i AS ts, 1::DOUBLE AS value, CASE i WHEN 0 THEN 'ok' WHEN 1 THEN NULL ELSE repeat('x',129) END AS label FROM range(3) t(i)")
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Dimensions: []analysis.ParquetDimensionMapping{{Name: "label", Value: Column{"label"}}}}}
	request := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping}
	if _, err := e.Normalize(t.Context(), request); err == nil {
		t.Fatal("invalid labels silently dropped")
	}
	request.AllowRejected = true
	result, err := e.Normalize(t.Context(), request)
	if err != nil || result.Accepted != 1 || result.Rejected != 2 || len(result.Rejections) != 1 || len(result.Rejections[0].Rows) != 2 || result.Rejections[0].Rows[0] != 1 || result.Rejections[0].Rows[1] != 2 {
		t.Fatal("identity rejected-row provenance lost", result, err)
	}
}
func TestNativeLegacyCanonicalArtifactIdentityAdapter(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 'apexload.metric_point.v1' AS schema_version, 'p' AS project_id, 'd' AS dataset_id, 'd' AS source, to_timestamp(1700000000) AS timestamp_utc, 'v' AS metric_name, 'gauge' AS kind, 'count' AS unit, 1::DOUBLE AS value, 'valid' AS quality")
	// Use the shared schema constant through a generated fixture expression; the
	// actual native query inspects the old physical file lacking identity columns.
	if analysis.SchemaVersion != "apexload.metric_point.v1" {
		t.Fatal("fixture schema constant requires update")
	}
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000002, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
	points, err := e.Series(context.Background(), []string{path}, q)
	if err != nil || len(points) != 2 || points[0].Value == nil || points[0].Source != "d" || points[0].Host != "" || len(points[0].Dimensions) != 0 {
		t.Fatal("historical canonical file incompatible", points, err)
	}
}
func TestNativeIdentitySQLOnlyQuotesApprovedLabelsAndPaths(t *testing.T) {
	q := SeriesQuery{Dimensions: map[string]string{"a/b~c": "x' OR 1=1 --"}}
	filter := nativeIdentityFilter(q)
	if !strings.Contains(filter, "'/a~1b~0c'") || !strings.Contains(filter, "'x'' OR 1=1 --'") {
		t.Fatal("literal/pointer grammar lost", filter)
	}
}

func TestNativeCanonicalQualityCannotBecomeHealthyAggregation(t *testing.T) {
	for name, row := range map[string]string{
		"stale_value":        "'stale', 7::DOUBLE",
		"clock_skewed_value": "'clock_skewed', 7::DOUBLE",
		"dropped_value":      "'dropped', 7::DOUBLE",
		"dropped_null":       "'dropped', NULL::DOUBLE",
		"unknown_value":      "'unknown', 7::DOUBLE",
		"unknown_null":       "'unknown', NULL::DOUBLE",
		"null_quality":       "NULL::VARCHAR, 7::DOUBLE",
		"missing_value":      "'missing', 7::DOUBLE",
		"valid_null":         "'valid', NULL::DOUBLE",
		"valid_nan":          "'valid', 'NaN'::DOUBLE",
		"valid_infinity":     "'valid', 'Infinity'::DOUBLE",
	} {
		t.Run(name, func(t *testing.T) {
			// Include a valid sample in the same bucket. Filtering the bad row
			// would fabricate a healthy average and must not satisfy this test.
			base := "SELECT " + literal(analysis.SchemaVersion) + " AS schema_version, 'p' AS project_id, 'd' AS dataset_id, 'd' AS source, to_timestamp(1700000000) AS timestamp_utc, 'v' AS metric_name, 'counter' AS kind, 'count' AS unit, q AS quality, v AS value FROM (SELECT 'valid' AS q, 1::DOUBLE AS v UNION ALL SELECT " + row + ")"
			e, path := nativeFixture(t, base)
			for _, aggregation := range []string{"avg", "count", "rate"} {
				q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000002, 0).UTC(), BucketSeconds: 1, Aggregation: aggregation}
				points, err := e.childNativeSeries(t.Context(), []string{path}, q)
				if err == nil || len(points) != 0 || !strings.Contains(err.Error(), "quality/value") {
					t.Fatal("unqualified canonical quality became a result", aggregation, points, err)
				}
				if aggregation == "avg" {
					points, err = e.Series(t.Context(), []string{path}, q)
					if err == nil || len(points) != 0 {
						t.Fatal("public worker returned unqualified evidence", points, err)
					}
				}
			}
		})
	}
}
func TestNativeCanonicalMissingQualityRemainsMissing(t *testing.T) {
	e, path := nativeFixture(t, "SELECT "+literal(analysis.SchemaVersion)+" AS schema_version, 'p' AS project_id, 'd' AS dataset_id, 'd' AS source, to_timestamp(1700000000) AS timestamp_utc, 'v' AS metric_name, 'gauge' AS kind, 'count' AS unit, q AS quality, v AS value FROM (SELECT 'valid' AS q, 1::DOUBLE AS v UNION ALL SELECT 'missing', NULL::DOUBLE)")
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000002, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
	points, err := e.Series(t.Context(), []string{path}, q)
	if err != nil || len(points) != 2 || points[0].Value != nil || points[0].Quality != analysis.QualityMissing || points[1].Value != nil || points[1].Quality != analysis.QualityMissing {
		t.Fatal("partially missing bucket became healthy", points, err)
	}
}
func TestNativeIdentityNULAndUTF8ScalarAdmission(t *testing.T) {
	for _, field := range []string{"source", "host", "service", "dimension", "metric"} {
		t.Run(field, func(t *testing.T) {
			columns := map[string]string{"source": "'agent'", "host": "'host'", "service": "'svc'", "dimension": "'😀'", "metric": "'v'"}
			columns[field] = "'a'||chr(0)||'b'"
			query := "SELECT 1700000000 AS ts, 1::DOUBLE AS value"
			for _, column := range []string{"source", "host", "service", "dimension", "metric"} {
				query += ", " + columns[column] + " AS " + column
			}
			e, path := nativeFixture(t, query)
			mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, NameColumn: Column{"metric"}, Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: Column{"source"}, Host: Column{"host"}, Service: Column{"service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "😀", Value: Column{"dimension"}}}}}
			request := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping, AllowRejected: true}
			result, err := e.Normalize(t.Context(), request)
			if err != nil || result.Accepted != 0 || result.Rejected != 1 {
				t.Fatal("NUL scalar admitted", field, result, err)
			}
		})
	}
}

func TestNativeIdentityRequiredSourceUnicodeSpaceAndValidLabels(t *testing.T) {
	for _, source := range []string{"agent😀", "\t\n", "\u0085\u00a0\u2003\u3000"} {
		t.Run(source, func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT 1700000000 AS ts, 1::DOUBLE AS value, "+literal(source)+" AS source, '😀' AS label")
			mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: Column{"source"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "😀", Value: Column{"label"}}}}}
			result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping, AllowRejected: true})
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(source) == "" {
				if result.Accepted != 0 || result.Rejected != 1 {
					t.Fatal("blank Unicode source accepted", result)
				}
				return
			}
			if result.Accepted != 1 || result.Rejected != 0 {
				t.Fatal("valid Unicode source rejected", result)
			}
			result = retainNativeFixtureOutputs(t, e, result)
			q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", IdentitySchemaVersion: 2, SourceID: source, Dimensions: map[string]string{"😀": "😀"}, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
			points, err := e.Series(t.Context(), result.Files, q)
			if err != nil || len(points) != 1 || points[0].Source != source || points[0].Dimensions["😀"] != "😀" {
				t.Fatal("valid Unicode identity changed", points, err)
			}
		})
	}
}

func TestNativeMappedMetricNameUnicodeScalarAdmission(t *testing.T) {
	for _, name := range []string{"latency😀", "\t\n", "\u00a0\u2000"} {
		t.Run(name, func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT 1700000000 AS ts, 1::DOUBLE AS value, "+literal(name)+" AS metric")
			mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, NameColumn: Column{"metric"}, Kind: analysis.KindGauge, Unit: "count"}}}
			result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping, AllowRejected: true})
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(name) == "" {
				if result.Accepted != 0 || result.Rejected != 1 {
					t.Fatal("Unicode blank metric normalized", result)
				}
				return
			}
			if result.Accepted != 1 || result.Rejected != 0 {
				t.Fatal("valid Unicode metric rejected", result)
			}
			result = retainNativeFixtureOutputs(t, e, result)
			q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: name, IdentitySchemaVersion: 2, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
			points, err := e.Series(t.Context(), result.Files, q)
			if err != nil || len(points) != 1 || points[0].MetricName != name {
				t.Fatal("valid Unicode metric query disagrees with normalization", points, err)
			}
		})
	}
}

// Even an all-rejected normalization owns its exact child/output/monitor parent
// until ReleaseOutputs joins it. Each independent fixture must cross that same
// physical boundary before another heavy native attempt is admitted.
func TestNativeIdentityAllRejectedOutputOwnerJoinsBeforeNextAdmission(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000 AS ts, 1::DOUBLE AS value, '' AS source")
	request := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, AllowRejected: true, Mapping: Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: Column{"source"}}}}
	result, err := e.Normalize(t.Context(), request)
	if err != nil || result.Accepted != 0 || result.Rejected != 1 {
		t.Fatal("fixture must own a completed all-rejected result", result, err)
	}
	e.workerMu.Lock()
	retained := e.workerResult
	e.workerMu.Unlock()
	if retained == nil || retained.attempt == nil || retained.attempt.privatePipeline == nil {
		t.Fatal("exact completed native parent missing")
	}
	pipeline := retained.attempt.privatePipeline
	select {
	case <-pipeline.Done():
		t.Fatal("result returned before exact output parent retention")
	default:
	}
	if _, err = e.Normalize(t.Context(), request); err == nil {
		t.Fatal("another normalization crossed retained physical owner")
	}
	if err = e.ReleaseOutputs(); err != nil {
		t.Fatal("exact output parent release", err)
	}
	select {
	case <-pipeline.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("released exact parent did not join")
	}
	if NativeBusy() {
		t.Fatal("joined parent retained heavy admission")
	}
	if _, err = e.Normalize(t.Context(), request); err != nil {
		t.Fatal("new normalization refused after exact parent join", err)
	}
}

func TestNativeIdentityQueryVersionCannotDowngradeCanonicalArtifacts(t *testing.T) {
	for _, artifact := range []struct{ name, identity string }{
		{name: "historical_missing_columns"},
		{name: "explicit_canonical_v1", identity: ", 1 AS identity_version, '' AS host, '' AS service, '{}' AS dimensions_json"},
	} {
		t.Run(artifact.name, func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT "+literal(analysis.SchemaVersion)+" AS schema_version, 'p' AS project_id, 'd' AS dataset_id, 'd' AS source, to_timestamp(1700000000) AS timestamp_utc, 'v' AS metric_name, 'gauge' AS kind, 'count' AS unit, 1::DOUBLE AS value, 'valid' AS quality"+artifact.identity)
			q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000002, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
			for _, version := range []int{0, 1} {
				q.IdentitySchemaVersion = version
				points, err := e.Series(t.Context(), []string{path}, q)
				if err != nil || len(points) != 2 || points[0].Value == nil || *points[0].Value != 1 {
					t.Fatal("historical query compatibility lost", version, points, err)
				}
			}
			q.IdentitySchemaVersion = 2
			points, err := e.Series(t.Context(), []string{path}, q)
			if !errors.Is(err, errWorkerProtocol) || len(points) != 0 {
				t.Fatal("explicit v2 query silently downgraded to canonical v1", points, err)
			}
		})
	}
}
