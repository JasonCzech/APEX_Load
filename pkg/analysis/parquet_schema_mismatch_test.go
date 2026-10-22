package analysis

import (
	"errors"
	"reflect"
	"testing"
)

func TestMappedColumnsListsEveryReadColumnOnceInMappingOrder(t *testing.T) {
	mapping := ParquetMapping{
		Timestamp: ParquetColumnPath{"ts"},
		Identity: &ParquetIdentityMapping{
			Host:       ParquetColumnPath{"host"},
			Dimensions: []ParquetDimensionMapping{{Name: "region", Value: ParquetColumnPath{"labels", "region"}}},
		},
		Measures: []ParquetMeasure{
			{Value: ParquetColumnPath{"latency"}, Name: "latency", Kind: KindSummary, SampleCountColumn: ParquetColumnPath{"samples"}},
			{Value: ParquetColumnPath{"value"}, NameColumn: ParquetColumnPath{"metric"}, Kind: KindGauge},
			{Value: ParquetColumnPath{"latency"}, Name: "latency_copy", Kind: KindGauge},
			{Value: ParquetColumnPath{"total"}, Name: "requests", Kind: KindHistogram, Histogram: &ParquetHistogram{CountColumns: []ParquetColumnPath{{"le_10"}, {"le_inf"}}}},
		},
	}
	want := []ParquetColumnPath{{"ts"}, {"host"}, {"labels", "region"}, {"latency"}, {"samples"}, {"value"}, {"metric"}, {"total"}, {"le_10"}, {"le_inf"}}
	if got := mapping.MappedColumns(); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapped columns = %v, want %v", got, want)
	}
}

func TestSchemaMismatchNamesTheFileAndTheColumnItLacksOrRepeats(t *testing.T) {
	mapping := ParquetMapping{Timestamp: ParquetColumnPath{"ts"}, Measures: []ParquetMeasure{{Value: ParquetColumnPath{"stats", "latency"}, Name: "latency", Kind: KindGauge}}}
	schema := func(columns ...ParquetColumn) ParquetSchemaPreview { return ParquetSchemaPreview{Columns: columns} }
	ts := ParquetColumn{PathComponents: []string{"ts"}}
	latency := ParquetColumn{PathComponents: []string{"stats", "latency"}}

	if mismatch := mapping.SchemaMismatch(0, schema(ts, latency)); mismatch != nil {
		t.Fatalf("a matching file reported %v", mismatch)
	}
	absent := mapping.SchemaMismatch(2, schema(ts, ParquetColumn{PathComponents: []string{"stats", "latency_ms"}}))
	if absent == nil || *absent != (SchemaMismatchError{File: 2, Column: 1, Reason: SchemaMismatchAbsent}) {
		t.Fatalf("absent column = %#v", absent)
	}
	if got, want := absent.Describe("2026/b.parquet", mapping), `File "2026/b.parquet" does not match the mapping: it has no column "stats.latency".`; got != want {
		t.Fatalf("describe absent = %q, want %q", got, want)
	}
	repeated := mapping.SchemaMismatch(1, schema(ts, ParquetColumn{PathComponents: []string{"stats", "latency"}, Repeated: true}))
	if repeated == nil || *repeated != (SchemaMismatchError{File: 1, Column: 1, Reason: SchemaMismatchRepeated}) {
		t.Fatalf("repeated column = %#v", repeated)
	}
	if got, want := repeated.Describe("b.parquet", mapping), `File "b.parquet" does not match the mapping: column "stats.latency" is a list or repeated field, which cannot be mapped to a scalar measurement.`; got != want {
		t.Fatalf("describe repeated = %q, want %q", got, want)
	}
	// A list's elements sit below the mapped path; a group's fields do too.
	list := mapping.SchemaMismatch(1, schema(ts, ParquetColumn{PathComponents: []string{"stats", "latency", "list", "element"}, Repeated: true}))
	if list == nil || list.Reason != SchemaMismatchRepeated {
		t.Fatalf("list column = %#v", list)
	}
	group := mapping.SchemaMismatch(1, schema(ts, ParquetColumn{PathComponents: []string{"stats", "latency", "p50"}}, ParquetColumn{PathComponents: []string{"stats", "latency", "p99"}}))
	if group == nil || group.Reason != SchemaMismatchGroup {
		t.Fatalf("group column = %#v", group)
	}
	if got, want := group.Describe("b.parquet", mapping), `File "b.parquet" does not match the mapping: column "stats.latency" is a group of fields; map one of its fields instead.`; got != want {
		t.Fatalf("describe group = %q, want %q", got, want)
	}
	// A group with a list among its fields is still a group to pick from.
	mixed := mapping.SchemaMismatch(1, schema(ts, ParquetColumn{PathComponents: []string{"stats", "latency", "p50"}}, ParquetColumn{PathComponents: []string{"stats", "latency", "samples", "list", "element"}, Repeated: true}))
	if mixed == nil || mixed.Reason != SchemaMismatchGroup {
		t.Fatalf("mixed group column = %#v", mixed)
	}
	// A component holding a "." is shown escaped once, as in the schema preview.
	dotted := ParquetMapping{Timestamp: ParquetColumnPath{"ts"}, Measures: []ParquetMeasure{{Value: ParquetColumnPath{"stats.latency"}, Name: "latency", Kind: KindGauge}}}
	if got, want := dotted.SchemaMismatch(0, schema(ts, latency)).Describe("b.parquet", dotted), `File "b.parquet" does not match the mapping: it has no column "stats\.latency".`; got != want {
		t.Fatalf("describe dotted = %q, want %q", got, want)
	}
	// A quote or a line break in the mapping's own text cannot split the message.
	odd := ParquetMapping{Timestamp: ParquetColumnPath{"ts"}, Measures: []ParquetMeasure{{Value: ParquetColumnPath{"lat\"en\ncy"}, Name: "say \"hi\"\n", Kind: KindGauge}}}
	if got, want := odd.SchemaMismatch(0, schema(ts)).Describe("b.parquet", odd), `File "b.parquet" does not match the mapping: it has no column "lat\"en\u000acy".`; got != want {
		t.Fatalf("describe odd column = %q, want %q", got, want)
	}
	if got, want := (&RejectedRowsError{Count: 1, Rows: []int64{0}}).Describe("b.parquet", odd), `File "b.parquet" has 1 row whose measure "say \"hi\"\u000a" was rejected (first source rows, counting from 0: 0). Accept rejected measurements to import the rest, or correct the file.`; got != want {
		t.Fatalf("describe odd measure = %q, want %q", got, want)
	}
	if got, want := quotedName("p99\u2028ok\u202eé\U000E0001"), `"p99\u2028ok\u202eé\U000e0001"`; got != want {
		t.Fatalf("quoted separators and bidi controls = %q, want %q", got, want)
	}
	// A longer path that merely shares a prefix is not the mapped column.
	if mismatch := mapping.SchemaMismatch(0, schema(ts, latency, ParquetColumn{PathComponents: []string{"stats", "latency_ms"}})); mismatch != nil {
		t.Fatalf("a sibling column changed the match: %#v", mismatch)
	}
	var target *SchemaMismatchError
	if err := error(absent); !errors.As(err, &target) || target != absent {
		t.Fatal("a schema mismatch is not found by errors.As")
	}
}

func TestRejectedRowsErrorNamesTheFileMeasureAndFirstRows(t *testing.T) {
	mapping := ParquetMapping{Measures: []ParquetMeasure{{Value: ParquetColumnPath{"latency"}, Name: "latency_ms"}, {Value: ParquetColumnPath{"m", "value"}, NameColumn: ParquetColumnPath{"m", "name"}}}}
	several := &RejectedRowsError{File: 1, Measure: 0, Count: 12, Rows: []int64{4, 17, 52}}
	if got, want := several.Describe("b.parquet", mapping), `File "b.parquet" has 12 rows whose measure "latency_ms" was rejected (first source rows, counting from 0: 4, 17, 52). Accept rejected measurements to import the rest, or correct the file.`; got != want {
		t.Fatalf("describe = %q, want %q", got, want)
	}
	one := &RejectedRowsError{File: 0, Measure: 1, Count: 1, Rows: []int64{0}}
	if got, want := one.Describe("a.parquet", mapping), `File "a.parquet" has 1 row whose measure "m.value" was rejected (first source rows, counting from 0: 0). Accept rejected measurements to import the rest, or correct the file.`; got != want {
		t.Fatalf("describe one = %q, want %q", got, want)
	}
}
