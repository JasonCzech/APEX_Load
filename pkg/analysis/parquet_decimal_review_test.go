package analysis

import (
	"fmt"
	"testing"

	"github.com/xitongsys/parquet-go/parquet"
)

func TestParquetDecimalReviewDistinguishesPotentialLossFromRows(t *testing.T) {
	for _, tc := range []struct {
		precision, scale int32
		want             string
	}{{15, 0, ""}, {16, 0, "decimal_digits_may_exceed_float64_integer_precision"}, {38, 2, "fractional_decimal_may_round_to_float64"}, {2, 1, "fractional_decimal_may_round_to_float64"}} {
		t.Run(fmt.Sprintf("%d,%d", tc.precision, tc.scale), func(t *testing.T) {
			m := ParquetMapping{Measures: []ParquetMeasure{{Value: ParquetColumnPath{"amount"}}}}
			s := ParquetSchemaPreview{Columns: []ParquetColumn{{PathComponents: []string{"amount"}, ConvertedType: "DECIMAL", Precision: &tc.precision, Scale: &tc.scale}}}
			risks, err := ParquetDecimalRisks(m, s)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(risks) != 0 {
					t.Fatal(risks)
				}
				return
			}
			if len(risks) != 1 || risks[0].Reason != tc.want || risks[0].Precision != tc.precision || risks[0].Scale != tc.scale {
				t.Fatal(risks)
			}
			risks[0].Column[0] = "mutated"
			if s.Columns[0].PathComponents[0] != "amount" || m.Measures[0].Value[0] != "amount" {
				t.Fatal("disclosure aliases input")
			}
		})
	}
}
func TestParquetDecimalReviewUsesMappedNumericRolesAndRejectsUnknownMetadata(t *testing.T) {
	precision, scale := int32(38), int32(2)
	c := ParquetColumn{PathComponents: []string{"amount"}, LogicalType: &parquet.LogicalType{DECIMAL: &parquet.DecimalType{Precision: precision, Scale: scale}}}
	m := ParquetMapping{Measures: []ParquetMeasure{{Value: ParquetColumnPath{"amount"}, SampleSumColumn: ParquetColumnPath{"amount"}}}}
	s := ParquetSchemaPreview{Columns: []ParquetColumn{c}}
	risks, err := ParquetDecimalRisks(m, s)
	if err != nil || len(risks) != 1 {
		t.Fatal(risks, err)
	}
	m.Measures[0].Value = ParquetColumnPath{"other"}
	m.Measures[0].SampleSumColumn = nil
	s.Columns = append(s.Columns, ParquetColumn{PathComponents: []string{"other"}, PhysicalType: "DOUBLE"})
	risks, err = ParquetDecimalRisks(m, s)
	if err != nil || len(risks) != 0 {
		t.Fatal("unmapped identity became numeric risk", risks, err)
	}
	m.Measures[0].Value = ParquetColumnPath{"amount"}
	bad := int32(37)
	s.Columns[0].Precision = &bad
	if _, err = ParquetDecimalRisks(m, s); err == nil {
		t.Fatal("conflicting precision accepted")
	}
	s.Columns[0] = ParquetColumn{PathComponents: []string{"amount"}, ConvertedType: "DECIMAL"}
	if _, err = ParquetDecimalRisks(m, s); err == nil {
		t.Fatal("missing precision silently ignored")
	}
	if got, err := ParquetDecimalRisks(ParquetMapping{}, ParquetSchemaPreview{}); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestParquetDecimalReviewRejectsBoundsAndMalformedSchema(t *testing.T) {
	precision, scale := int32(38), int32(2)
	for _, count := range []int{64, 65} {
		mapping := ParquetMapping{Measures: []ParquetMeasure{{Value: ParquetColumnPath{"value0"}, Histogram: &ParquetHistogram{}}}}
		schema := ParquetSchemaPreview{}
		for i := 0; i < count; i++ {
			path := ParquetColumnPath{fmt.Sprintf("value%d", i)}
			mapping.Measures[0].Histogram.CountColumns = append(mapping.Measures[0].Histogram.CountColumns, path)
			schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: path, ConvertedType: "DECIMAL", Precision: &precision, Scale: &scale})
		}
		risks, err := ParquetDecimalRisks(mapping, schema)
		if count == 64 && (err != nil || len(risks) != 64) || count == 65 && (err == nil || risks != nil) {
			t.Fatal(count, len(risks), err)
		}
	}
	mapping := ParquetMapping{Measures: []ParquetMeasure{{Value: ParquetColumnPath{"value"}}}}
	col := ParquetColumn{PathComponents: []string{"value"}, ConvertedType: "DECIMAL", Precision: &precision, Scale: &scale}
	for _, columns := range [][]ParquetColumn{{}, {col, col}, {{PathComponents: []string{"other"}, PhysicalType: "DOUBLE"}}} {
		if risks, err := ParquetDecimalRisks(mapping, ParquetSchemaPreview{Columns: columns}); err == nil || risks != nil {
			t.Fatal("malformed or absent column admitted", risks, err)
		}
	}
	mapping.Measures[0].Histogram = &ParquetHistogram{CountColumns: make([]ParquetColumnPath, MaxHistogramBounds+1)}
	if _, err := ParquetDecimalRisks(mapping, ParquetSchemaPreview{Columns: []ParquetColumn{col}}); err == nil {
		t.Fatal("histogram population copied before refusal")
	}
}

func TestParquetDecimalReviewIncludesHistogramBoundaryConversion(t *testing.T) {
	precision, scale := int32(38), int32(2)
	mapping := ParquetMapping{Measures: []ParquetMeasure{{Value: ParquetColumnPath{"count"}, Histogram: &ParquetHistogram{BoundColumn: ParquetColumnPath{"upper"}}}}}
	schema := ParquetSchemaPreview{Columns: []ParquetColumn{{PathComponents: []string{"count"}, PhysicalType: "INT64"}, {PathComponents: []string{"upper"}, ConvertedType: "DECIMAL", Precision: &precision, Scale: &scale}}}
	risks, err := ParquetDecimalRisks(mapping, schema)
	if err != nil || len(risks) != 1 || len(risks[0].Column) != 1 || risks[0].Column[0] != "upper" {
		t.Fatalf("numeric histogram boundary omitted: %+v %v", risks, err)
	}
}
