package duckdb

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/xitongsys/parquet-go/parquet"
)

func TestWorkerAggregateEncodingBoundsBeforeWholeMarshal(t *testing.T) {
	b := workerTestBootstrap(t)
	// Values share backing strings, keeping the fixture small while encoded
	// JSON would expand to hundreds of MiB despite per-field/count bounds.
	text := strings.Repeat("x", 8192)
	points := make([]analysis.MetricPoint, 10000)
	for i := range points {
		points[i].MetricName = text
		points[i].Source = text
	}
	f := workerFrame{Identity: b.Identity, Kind: "result", Result: &workerResult{Status: "ok", Points: &points}}
	if preflightWorkerValue(reflect.ValueOf(f), "", 0, new(int)) != nil {
		t.Fatal("fixture does not pass individual limits")
	}
	remaining := int64(workerMaxFrameBytes)
	if workerEncodedBytes(reflect.ValueOf(f), 0, &remaining) == nil {
		t.Fatal("aggregate encoded expansion admitted")
	}
	var destination bytes.Buffer
	if writeWorkerFrame(&destination, f) == nil || destination.Len() != 0 {
		t.Fatal("oversized output reached frame writer")
	}
	if _, err := f.clone(); err == nil {
		t.Fatal("clone allocated oversized object")
	}
	// HTML escaping must be charged, not only the unescaped string length.
	remaining = 10
	if workerEncodedString("&&", &remaining) == nil {
		t.Fatal("JSON escape expansion ignored")
	}
}

func workerReviewTwoMeasureFixture(t *testing.T) (workerBootstrap, workerRequest, workerResult) {
	b := workerTestBootstrap(t)
	b.Handles[0].Rows = nil
	b.Limits.OutputBytes = 4096
	b.Handles = append(b.Handles, workerHandle{Handle: strings.Repeat("4", 32), Role: "normalized", RelativeName: "part-0000-01.parquet", Bytes: 1000, InputHandle: b.Handles[0].Handle, Measure: 1}, workerHandle{Handle: strings.Repeat("5", 32), Role: "rejection", RelativeName: "reject-0000-01.parquet", Bytes: 1000, InputHandle: b.Handles[0].Handle, Measure: 1})
	b.Identity.ManifestDigest, _ = b.manifestDigest()
	r := workerTestRequest(b)
	r.Normalize.Mapping.Measures = append(r.Normalize.Mapping.Measures, Measure{Name: "other", Value: Column{"other"}, Kind: analysis.KindGauge, Unit: "ms"})
	r.Normalize.OutputHandles = append(r.Normalize.OutputHandles, b.Handles[3].Handle, b.Handles[4].Handle)
	v := workerTestNormalizeResult(b)
	v.Normalize.Accepted = 5
	v.Normalize.Outputs = append(v.Normalize.Outputs, workerOutput{Handle: b.Handles[3].Handle, SHA256: strings.Repeat("a", 64), Bytes: 100, Rows: 3})
	if v.validate(b, r) != nil {
		t.Fatal("coherent unknown-row fixture rejected")
	}
	return b, r, v
}
func TestWorkerUnknownRowsStillRequireEachMeasurePopulation(t *testing.T) {
	b, r, v := workerReviewTwoMeasureFixture(t)
	v.Normalize.Outputs[0].Rows = 0
	v.Normalize.Outputs[2].Rows = 5
	if v.validate(b, r) == nil {
		t.Fatal("slot totals 1 and 5 accepted for one three-row source")
	}
	b, r, v = workerReviewTwoMeasureFixture(t)
	v.Normalize.Rejections[0].Rows[0] = 42
	if v.validate(b, r) == nil {
		t.Fatal("rejection ordinal beyond inferred source rows accepted")
	}
}
func TestWorkerPreviewCellsMatchPhysicalAndLogicalColumn(t *testing.T) {
	number := "1"
	boolean := true
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, physical string
		logical        *parquet.LogicalType
		cell           workerCell
		want           bool
	}{
		{"integer bool", "INT64", nil, workerCell{Kind: "bool", Boolean: &boolean}, false},
		{"integer number", "INT64", nil, workerCell{Kind: "number", Number: &number}, true},
		{"integer time unsupported", "INT64", nil, workerCell{Kind: "timestamp", Timestamp: &now}, false},
		{"timestamp logical", "INT64", &parquet.LogicalType{TIMESTAMP: &parquet.TimestampType{}}, workerCell{Kind: "timestamp", Timestamp: &now}, true},
		{"date logical", "INT32", &parquet.LogicalType{DATE: &parquet.DateType{}}, workerCell{Kind: "timestamp", Timestamp: &now}, true},
		{"int96 time", "INT96", nil, workerCell{Kind: "timestamp", Timestamp: &now}, true},
		{"integer nonfinite", "INT32", nil, workerCell{Kind: "non_finite"}, false},
		{"float nonfinite", "DOUBLE", nil, workerCell{Kind: "non_finite"}, true},
		{"boolean numeric", "BOOLEAN", nil, workerCell{Kind: "number", Number: &number}, false},
		{"boolean", "BOOLEAN", nil, workerCell{Kind: "bool", Boolean: &boolean}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := workerCellMatchesColumn(test.cell, analysis.ParquetColumn{PhysicalType: test.physical, LogicalType: test.logical}); got != test.want {
				t.Fatal(got)
			}
		})
	}
}
func TestWorkerPortableNamesRejectWindowsAliasesAndDevices(t *testing.T) {
	for _, name := range []string{"input.parquet.", "nul.parquet", "con", "prn.log", "aux.parquet", "com1.parquet", "lpt9"} {
		b := workerTestBootstrap(t)
		b.Handles[1].RelativeName = name
		b.Identity.ManifestDigest, _ = b.manifestDigest()
		if b.validate() == nil {
			t.Fatal("nonportable file authority", name)
		}
	}
	for _, name := range []string{"part-0000.parquet", "console.parquet", "com0.parquet", "lpt10.parquet"} {
		if !workerPortableName(name) {
			t.Fatal("ordinary name rejected", name)
		}
	}
}

func TestWorkerPreviewDisplayPathPreservesEscapedPhysicalFieldIdentity(t *testing.T) {
	b := workerTestBootstrap(t)
	request := workerRequest{Preview: &workerPreviewOperation{InputHandle: b.Handles[0].Handle}}
	number := "1"
	for _, test := range []struct {
		parts []string
		path  string
		kind  string
	}{{[]string{"a.b"}, `a\.b`, "number"}, {[]string{`a\b`}, `a\\b`, "number"}, {[]string{"a", "b"}, "a.b", "redacted"}} {
		cell := workerCell{Kind: test.kind}
		if test.kind == "number" {
			cell.Number = &number
		}
		result := workerResult{Status: "ok", Preview: &workerPreviewResult{Schema: analysis.ParquetSchemaPreview{Rows: 3, FileSize: 100, Columns: []analysis.ParquetColumn{{Path: test.path, PathComponents: test.parts, PhysicalType: "INT64"}}}, Samples: [][]workerCell{{cell}}}}
		if result.validate(b, request) != nil {
			t.Fatal("valid schema field identity rejected", test.path)
		}
	}
	if workerParquetPath([]string{"a.b"}) == workerParquetPath([]string{"a", "b"}) {
		t.Fatal("scalar literal dot aliased nested identity")
	}
}

func TestWorkerPreviewAndMappingPreserveSpacedColumnNames(t *testing.T) {
	b := workerTestBootstrap(t)
	r := workerTestRequest(b)
	r.Normalize.Mapping.Timestamp = Column{" at "}
	r.Normalize.Mapping.Measures[0].Value = Column{" value "}
	if r.validate(b) != nil {
		t.Fatal("exact spaced mapping names rejected")
	}
	number := "1"
	request := workerRequest{Preview: &workerPreviewOperation{InputHandle: b.Handles[0].Handle}}
	for _, name := range []string{" value ", "\tvalue\t"} {
		result := workerResult{Status: "ok", Preview: &workerPreviewResult{Schema: analysis.ParquetSchemaPreview{Rows: 3, FileSize: 100, Columns: []analysis.ParquetColumn{{Path: name, PathComponents: []string{name}, PhysicalType: "INT64"}}}, Samples: [][]workerCell{{{Kind: "number", Number: &number}}}}}
		if result.validate(b, request) != nil {
			t.Fatal("exact field name rejected", name)
		}
		result.Preview.Schema.Columns[0].Path = strings.TrimSpace(name)
		if result.validate(b, request) == nil {
			t.Fatal("forged normalized field path accepted")
		}
	}
	for _, name := range []string{"", "value\n", "value\r", "value\x00"} {
		if workerColumnName(name, 256) {
			t.Fatal("invalid field name accepted")
		}
	}
}
