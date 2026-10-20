//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Directory partition labels are not an admitted mapping source. Physical
// columns and explicit mapping constants retain their meaning at every stage.
func TestNativePartitionLabelsCannotOverridePhysicalColumnsOrMapping(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT 1700000040 AS ts, 7::DOUBLE AS value, 'physical' AS metric, 'agent' AS source, 'east' AS region")
			original := nativeAcceptanceDigest(t, path)
			dir := filepath.Join(e.root, "ts=1", "value=999", "metric=directory", "source=foreign", "region=west", "injected=1")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			nested := filepath.Join(dir, "input.parquet")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(nested, data, 0600); err != nil {
				t.Fatal(err)
			}
			for name, preview := range map[string]func(context.Context, string) (Preview, error){"native": e.childNativePreview, "contained": e.Preview} {
				got, err := preview(t.Context(), nested)
				if err != nil || len(got.Samples) != 1 || fmt.Sprint(got.Samples[0]["ts"]) != "1700000040" || fmt.Sprint(got.Samples[0]["value"]) != "7" {
					t.Fatalf("%s physical preview: %+v %v", name, got, err)
				}
				for _, column := range got.Schema.Columns {
					if column.Path == "injected" {
						t.Fatal("directory label invented a schema column")
					}
				}
			}
			mapping := Mapping{Version: version, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
				{Value: Column{"value"}, Name: "constant", Kind: analysis.KindGauge, Unit: "count"},
				{Value: Column{"value"}, NameColumn: Column{"metric"}, Kind: analysis.KindGauge, Unit: "count"},
			}}
			if version == 2 {
				mapping.Identity = &analysis.ParquetIdentityMapping{Source: Column{"source"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "region", Value: Column{"region"}}}}
			}
			normalized, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{nested}, Mapping: mapping})
			if err != nil || normalized.Accepted != 2 || normalized.Rejected != 0 {
				t.Fatalf("physical normalize: %+v %v", normalized, err)
			}
			normalized = nativeAcceptanceRetain(t, e, normalized, "precedence")
			for _, metric := range []string{"constant", "physical"} {
				q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: metric, IdentitySchemaVersion: version, From: time.Unix(1700000040, 0).UTC(), To: time.Unix(1700000100, 0).UTC(), BucketSeconds: 60, Aggregation: "sum"}
				if version == 2 {
					q.SourceID = "agent"
					q.Dimensions = map[string]string{"region": "east"}
				}
				points, err := e.Series(t.Context(), normalized.Files, q)
				if err != nil || len(points) != 1 || points[0].Value == nil || *points[0].Value != 7 {
					t.Fatalf("%s physical query: %+v %v", metric, points, err)
				}
			}
			if nativeAcceptanceDigest(t, path) != original || nativeAcceptanceDigest(t, nested) != original {
				t.Fatal("source bytes changed")
			}
			ambiguous := mapping
			ambiguous.Measures = append([]Measure(nil), mapping.Measures...)
			ambiguous.Measures[0].NameColumn = Column{"metric"}
			if ambiguous.ValidateDefinition() == nil {
				t.Fatal("constant and column silently selected precedence")
			}
		})
	}
}
