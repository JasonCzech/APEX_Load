package analysis

import (
	"context"
	"encoding/binary"
	"strings"

	"github.com/apache/thrift/lib/go/thrift"
	"os"
	"path/filepath"
	"testing"

	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type schemaFixture struct {
	Timestamp int64   `parquet:"name=timestamp_utc, type=INT64"`
	Latency   float64 `parquet:"name=latency_ms, type=DOUBLE"`
}

func TestInspectParquetReadsSchemaWithoutRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.parquet")
	file, err := local.NewLocalFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := writer.NewParquetWriter(file, new(schemaFixture), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(schemaFixture{Timestamp: 1, Latency: 2.5}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteStop(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	preview, err := InspectParquet(path)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Rows != 1 || preview.FileSize < 12 || len(preview.Columns) != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Columns[0].Path != "timestamp_utc" || preview.Columns[0].PhysicalType != "INT64" ||
		preview.Columns[1].Path != "latency_ms" || preview.Columns[1].PhysicalType != "DOUBLE" {
		t.Fatalf("columns = %+v", preview.Columns)
	}
}

func TestInspectParquetRejectsBadHeaderAndOversizeFooter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.parquet")
	if err := os.WriteFile(path, []byte("not parquet"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectParquet(path); err == nil {
		t.Fatal("invalid header accepted")
	}
	data := make([]byte, 16)
	copy(data[:4], []byte("PAR1"))
	binary.LittleEndian.PutUint32(data[8:12], maxParquetFooterBytes+1)
	copy(data[12:], []byte("PAR1"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectParquet(path); err == nil {
		t.Fatal("oversized footer accepted")
	}
}

func TestFlattenParquetSchemaRejectsMalformedTree(t *testing.T) {
	children := int32(2)
	leafType := parquet.Type_INT64
	root := &parquet.SchemaElement{Name: "root", NumChildren: &children}
	leaf := &parquet.SchemaElement{Name: "value", Type: &leafType}
	if _, err := flattenParquetSchema([]*parquet.SchemaElement{root, leaf}); err == nil {
		t.Fatal("missing child accepted")
	}
	if _, err := flattenParquetSchema([]*parquet.SchemaElement{{Name: "root"}}); err == nil {
		t.Fatal("empty root accepted")
	}
}

func writeFooterFixture(t *testing.T, footer []byte) string {
	t.Helper()
	data := append([]byte("PAR1"), footer...)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(footer)))
	data = append(data, []byte("PAR1")...)
	path := filepath.Join(t.TempDir(), "footer.parquet")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParquetMetadataLimitsBeforeAllocation(t *testing.T) {
	for _, count := range []uint64{maxParquetSchemaElements + 1, 1 << 30} {
		// FileMetaData.schema (field 2), list<struct>, claimed count, no payload.
		footer := binary.AppendUvarint([]byte{0x29, 0xfc}, count)
		if _, err := InspectParquet(writeFooterFixture(t, footer)); err == nil {
			t.Fatal("oversized metadata collection accepted")
		}
	}
	ctx := context.Background()
	transport := thrift.NewTMemoryBuffer()
	p := thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{})
	for i := 0; i < maxParquetMetadataDepth+1; i++ {
		_ = p.WriteStructBegin(ctx, "nested")
		_ = p.WriteFieldBegin(ctx, "unknown", thrift.STRUCT, 20)
	}
	if _, err := InspectParquet(writeFooterFixture(t, transport.Bytes())); err == nil || !strings.Contains(err.Error(), "nesting limit") {
		t.Fatalf("nested metadata: %v", err)
	}
	for _, footer := range [][]byte{{}, {0x29, 0x1c}, {0}} {
		if _, err := InspectParquet(writeFooterFixture(t, footer)); err == nil {
			t.Fatal("truncated footer accepted")
		}
	}
}

func TestParquetSchemaPreservesLogicalTypesAndNestedIdentity(t *testing.T) {
	children, one := int32(2), int32(1)
	physical := parquet.Type_INT64
	optional, repeated := parquet.FieldRepetitionType_OPTIONAL, parquet.FieldRepetitionType_REPEATED
	logical := &parquet.LogicalType{TIMESTAMP: &parquet.TimestampType{IsAdjustedToUTC: true, Unit: &parquet.TimeUnit{MICROS: &parquet.MicroSeconds{}}}}
	elements := []*parquet.SchemaElement{
		{Name: "root", NumChildren: &children},
		{Name: "a.b", Type: &physical, LogicalType: logical, RepetitionType: &optional},
		{Name: "a", NumChildren: &one, RepetitionType: &repeated},
		{Name: "b", Type: &physical, RepetitionType: &optional},
	}
	columns, err := flattenParquetSchema(elements)
	if err != nil {
		t.Fatal(err)
	}
	if columns[0].Path == columns[1].Path || len(columns[0].PathComponents) != 1 || len(columns[1].PathComponents) != 2 {
		t.Fatalf("ambiguous paths: %+v", columns)
	}
	if columns[0].LogicalType.TIMESTAMP.Unit.MICROS == nil || !columns[0].LogicalType.TIMESTAMP.IsAdjustedToUTC {
		t.Fatal("timestamp semantics lost")
	}
	if !columns[1].Repeated || !columns[1].Nullable {
		t.Fatal("ancestor repetition lost")
	}
	elements[2].Name = "a.b"
	if _, err := flattenParquetSchema(elements); err == nil {
		t.Fatal("duplicate field accepted")
	}
}
