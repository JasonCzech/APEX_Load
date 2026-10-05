package analysis

import (
	"strings"
	"testing"
)

func TestParquetSQLSchemaPreservesExactPhysicalNames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths [][]string
		valid bool
	}{
		{name: "distinct", paths: [][]string{{"ts"}, {"labels", "source"}, {"labels", "host"}}, valid: true},
		{name: "shared exact paths", paths: [][]string{{"ts"}, {"labels", "source"}, {"labels", "source"}}, valid: true},
		{name: "top-level", paths: [][]string{{"ts"}, {"host"}, {"HOST"}}},
		{name: "nested ancestor", paths: [][]string{{"labels", "source"}, {"LABELS", "host"}}},
		{name: "nested scalar", paths: [][]string{{"labels", "source"}, {"labels", "SOURCE"}}},
		{name: "generated suffix must not be guessed", paths: [][]string{{"label"}, {"LABEL"}, {"label_1"}}},
		{name: "exact parents keep independent children", paths: [][]string{{"left", "source"}, {"right", "SOURCE"}}, valid: true},
		{name: "non-ASCII case remains exact", paths: [][]string{{"label_á"}, {"label_Á"}}, valid: true},
		{name: "separator-containing names remain exact", paths: [][]string{{"a.b", "c"}, {"a", "b.c"}}, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := ParquetSchemaPreview{}
			for _, path := range tc.paths {
				schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: path})
			}
			if err := ValidateParquetSQLSchema(schema); (err == nil) != tc.valid {
				t.Fatal("SQL binding identity differs", tc.valid, err)
			}
		})
	}
}

func TestParquetSQLSchemaBoundsPrecedeBinding(t *testing.T) {
	for _, schema := range []ParquetSchemaPreview{
		{},
		{Columns: make([]ParquetColumn, maxParquetColumns+1)},
		{Columns: []ParquetColumn{{PathComponents: make([]string, maxParquetNesting+1)}}},
		{Columns: []ParquetColumn{{PathComponents: []string{""}}}},
		{Columns: []ParquetColumn{{PathComponents: []string{strings.Repeat("x", 257)}}}},
		{Columns: []ParquetColumn{{PathComponents: []string{"a\x00b"}}}},
		{Columns: []ParquetColumn{{PathComponents: []string{string([]byte{0xff})}}}},
	} {
		if ValidateParquetSQLSchema(schema) == nil {
			t.Fatal("invalid physical schema admitted")
		}
	}
}

func TestParquetMappingRefusesCaseAmbiguityForEveryProjectedRole(t *testing.T) {
	base := ParquetMapping{Version: 2, Timestamp: ParquetColumnPath{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []ParquetMeasure{{Value: ParquetColumnPath{"value"}, Name: "v", Kind: KindGauge, Unit: "count"}}, Identity: &ParquetIdentityMapping{Source: ParquetColumnPath{"labels", "source"}}}
	for _, role := range []string{"timestamp", "value", "name_column", "identity", "unselected-renaming"} {
		t.Run(role, func(t *testing.T) {
			mapping := base
			mapping.Measures = append([]ParquetMeasure(nil), base.Measures...)
			schema := ParquetSchemaPreview{Columns: []ParquetColumn{{PathComponents: []string{"ts"}}, {PathComponents: []string{"value"}}, {PathComponents: []string{"labels", "source"}}}}
			var other ParquetColumnPath
			switch role {
			case "timestamp":
				other = ParquetColumnPath{"TS"}
			case "value":
				other = ParquetColumnPath{"VALUE"}
			case "name_column":
				mapping.Measures[0].Name = ""
				mapping.Measures[0].NameColumn = ParquetColumnPath{"metric"}
				schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: ParquetColumnPath{"metric"}})
				other = ParquetColumnPath{"METRIC"}
			case "identity":
				other = ParquetColumnPath{"LABELS", "source"}
			case "unselected-renaming":
				schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: ParquetColumnPath{"label"}})
				other = ParquetColumnPath{"LABEL"}
			}
			schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: other})
			if mapping.Validate(schema) == nil {
				t.Fatal("case-ambiguous physical source reached normalization")
			}
		})
	}
}
