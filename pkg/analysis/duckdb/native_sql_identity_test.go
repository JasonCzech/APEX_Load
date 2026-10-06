//go:build analytics_duckdb && cgo

package duckdb

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type nativeSQLCaseSiblingFixture struct {
	Timestamp int64   `parquet:"name=ts, type=INT64"`
	Value     float64 `parquet:"name=value, type=DOUBLE"`
	Lower     int64   `parquet:"name=label, type=INT64"`
	Upper     int64   `parquet:"name=LABEL, type=INT64"`
	Suffix    int64   `parquet:"name=label_1, type=INT64"`
}
type nativeSQLCaseAncestorFixture struct {
	Timestamp int64   `parquet:"name=ts, type=INT64"`
	Value     float64 `parquet:"name=value, type=DOUBLE"`
	Lower     struct {
		Source string `parquet:"name=source, type=BYTE_ARRAY, convertedtype=UTF8"`
	} `parquet:"name=labels"`
	Upper struct {
		Source string `parquet:"name=source, type=BYTE_ARRAY, convertedtype=UTF8"`
	} `parquet:"name=LABELS"`
}
type nativeSQLCaseNestedFixture struct {
	Timestamp int64   `parquet:"name=ts, type=INT64"`
	Value     float64 `parquet:"name=value, type=DOUBLE"`
	Labels    struct {
		Lower string `parquet:"name=source, type=BYTE_ARRAY, convertedtype=UTF8"`
		Upper string `parquet:"name=SOURCE, type=BYTE_ARRAY, convertedtype=UTF8"`
	} `parquet:"name=labels"`
}
type nativeSQLNonASCIIFixture struct {
	Timestamp int64   `parquet:"name=ts, type=INT64"`
	Value     float64 `parquet:"name=value, type=DOUBLE"`
	Lower     string  `parquet:"name=label_á, type=BYTE_ARRAY, convertedtype=UTF8"`
	Upper     string  `parquet:"name=label_Á, type=BYTE_ARRAY, convertedtype=UTF8"`
}

func writeNativeSQLPhysicalFixture(t *testing.T, path string, row any) {
	t.Helper()
	file, err := local.NewLocalFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = file.Close()
		}
	})
	output, err := writer.NewParquetWriter(file, reflect.New(reflect.TypeOf(row)).Interface(), 1)
	if err != nil {
		t.Fatal(err)
	}
	// The pinned writer fills optional length/precision/scale with zero even
	// for fields to which they do not apply. Omit those invalid annotations
	// so preview admission tests the physical names, not writer defaults.
	// Footer and schema handler share these elements, preserving data pages.
	for _, element := range output.Footer.Schema {
		if element.Type == nil {
			continue
		}
		if *element.Type != parquet.Type_FIXED_LEN_BYTE_ARRAY {
			element.TypeLength = nil
		}
		decimal := element.ConvertedType != nil && *element.ConvertedType == parquet.ConvertedType_DECIMAL || element.LogicalType != nil && element.LogicalType.DECIMAL != nil
		if !decimal {
			element.Precision, element.Scale = nil, nil
		}
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = output.WriteStop()
		}
	})
	if err := output.Write(row); err != nil {
		t.Fatal(err)
	}
	if err := output.WriteStop(); err != nil {
		t.Fatal(err)
	}
	stopped = true
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
}

func TestNativePhysicalParquetCaseBindingCannotChangePreviewOrIdentity(t *testing.T) {
	ancestor := nativeSQLCaseAncestorFixture{Timestamp: 1700000000, Value: 7}
	ancestor.Lower.Source, ancestor.Upper.Source = "lower-owner", "upper-owner"
	nested := nativeSQLCaseNestedFixture{Timestamp: 1700000000, Value: 7}
	nested.Labels.Lower, nested.Labels.Upper = "lower-owner", "upper-owner"
	for _, tc := range []struct {
		name            string
		row             any
		lower, selected Column
		valid           bool
	}{
		{name: "top-level-and-generated-suffix", row: nativeSQLCaseSiblingFixture{Timestamp: 1700000000, Value: 7, Lower: 11, Upper: 99, Suffix: 123}, lower: Column{"label"}, selected: Column{"LABEL"}},
		{name: "nested-ancestor", row: ancestor, lower: Column{"labels", "source"}, selected: Column{"LABELS", "source"}},
		{name: "nested-scalar", row: nested, lower: Column{"labels", "source"}, selected: Column{"labels", "SOURCE"}},
		{name: "non-ASCII-case-is-distinct", row: nativeSQLNonASCIIFixture{Timestamp: 1700000000, Value: 7, Lower: "lower-owner", Upper: "upper-owner"}, lower: Column{"label_á"}, selected: Column{"label_Á"}, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, _ := nativeFixture(t, "SELECT 1700000000 AS ts, 7::DOUBLE AS value")
			path := filepath.Join(engine.root, "physical-case.parquet")
			writeNativeSQLPhysicalFixture(t, path, tc.row)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			schema, err := analysis.InspectParquet(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, column := range schema.Columns {
				if column.TypeLength != nil || column.Precision != nil || column.Scale != nil {
					t.Fatal("physical case fixture has unrelated type metadata", column)
				}
			}
			for _, expected := range []Column{tc.lower, tc.selected} {
				found := false
				for _, column := range schema.Columns {
					if reflect.DeepEqual(column.PathComponents, []string(expected)) {
						found = true
					}
				}
				if !found {
					t.Fatal("writer did not preserve exact physical case fixture", expected, schema.Columns)
				}
			}
			preview, previewErr := engine.Preview(t.Context(), path)
			mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: tc.selected}}
			result, normalizeErr := engine.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
			if !tc.valid {
				if previewErr == nil || len(preview.Samples) != 0 || normalizeErr == nil || result.Accepted != 0 || len(result.Files) != 0 {
					t.Fatal("ambiguous fields became preview or normalized evidence", preview, previewErr, result, normalizeErr)
				}
			} else {
				if previewErr != nil || len(preview.Samples) != 1 || normalizeErr != nil || result.Accepted != 1 {
					t.Fatal("distinct non-ASCII physical fields refused", preview, previewErr, result, normalizeErr)
				}
				result = retainNativeFixtureOutputs(t, engine, result)
				from := time.Unix(1700000000, 0).UTC()
				points, err := engine.Series(t.Context(), result.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", IdentitySchemaVersion: 2, SourceID: "upper-owner", From: from, To: from.Add(time.Second), BucketSeconds: 1, Aggregation: "avg"})
				if err != nil || len(points) != 1 || points[0].Source != "upper-owner" || points[0].Value == nil || *points[0].Value != 7 {
					t.Fatal("exact selected physical field changed", points, err)
				}
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatal("schema refusal or normalization changed original", err)
			}
		})
	}
}

func TestNativePinnedDuckDBIdentifierFoldingMatchesPhysicalAdmission(t *testing.T) {
	engine, _ := nativeFixture(t, "SELECT 1 AS value")
	if err := engine.withDB(t.Context(), func(ctx context.Context, db *sql.DB) error {
		var value int
		if err := db.QueryRowContext(ctx, `SELECT "LaBeL" FROM (SELECT 17 AS "label")`).Scan(&value); err != nil || value != 17 {
			t.Fatal("pinned native quoted ASCII folding changed", value, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT "label_Á" FROM (SELECT 17 AS "label_á")`).Scan(&value); err == nil {
			t.Fatal("pinned native non-ASCII folding changed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type nativeSQLCanonicalCaseFixture struct {
	Schema       string  `parquet:"name=schema_version, type=BYTE_ARRAY, convertedtype=UTF8"`
	Project      string  `parquet:"name=project_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	Dataset      string  `parquet:"name=dataset_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	Source       string  `parquet:"name=source, type=BYTE_ARRAY, convertedtype=UTF8"`
	ShadowSource string  `parquet:"name=SOURCE, type=BYTE_ARRAY, convertedtype=UTF8"`
	Timestamp    int64   `parquet:"name=timestamp_utc, type=INT64, convertedtype=TIMESTAMP_MILLIS"`
	Metric       string  `parquet:"name=metric_name, type=BYTE_ARRAY, convertedtype=UTF8"`
	Kind         string  `parquet:"name=kind, type=BYTE_ARRAY, convertedtype=UTF8"`
	Unit         string  `parquet:"name=unit, type=BYTE_ARRAY, convertedtype=UTF8"`
	Value        float64 `parquet:"name=value, type=DOUBLE"`
	Quality      string  `parquet:"name=quality, type=BYTE_ARRAY, convertedtype=UTF8"`
}

func TestNativeCanonicalParquetCaseAmbiguityCannotChangeQueryIdentity(t *testing.T) {
	engine, _ := nativeFixture(t, "SELECT 1 AS value")
	path := filepath.Join(engine.root, "canonical-case.parquet")
	writeNativeSQLPhysicalFixture(t, path, nativeSQLCanonicalCaseFixture{Schema: analysis.SchemaVersion, Project: "p", Dataset: "d", Source: "d", ShadowSource: "foreign-source", Timestamp: 1700000000000, Metric: "v", Kind: analysis.KindGauge, Unit: "count", Value: 7, Quality: analysis.QualityValid})
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := analysis.InspectParquet(path)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.ValidateParquetSQLSchema(schema) == nil {
		t.Fatal("physical writer did not retain ambiguous source fixture")
	}
	from := time.Unix(1700000000, 0).UTC()
	points, err := engine.Series(t.Context(), []string{path}, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", From: from, To: from.Add(time.Second), BucketSeconds: 1, Aggregation: "avg"})
	if err == nil || len(points) != 0 {
		t.Fatal("ambiguous canonical artifact produced an identity", points, err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatal("canonical refusal changed original artifact", err)
	}
}
