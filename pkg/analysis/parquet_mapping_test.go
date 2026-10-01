package analysis

import "testing"

func TestParquetMappingScalarPathsAndSemantics(t *testing.T) {
	schema := ParquetSchemaPreview{Columns: []ParquetColumn{
		{PathComponents: []string{"ts"}}, {PathComponents: []string{"nested", "value"}}, {PathComponents: []string{"metric"}}, {PathComponents: []string{"list"}, Repeated: true},
	}}
	for _, tc := range []struct {
		name   string
		mutate func(*ParquetMapping)
		valid  bool
	}{
		{"nested scalar", func(*ParquetMapping) {}, true},
		{"long table", func(m *ParquetMapping) { m.Measures[0].Name = ""; m.Measures[0].NameColumn = []string{"metric"} }, true},
		{"timezone required", func(m *ParquetMapping) { m.Timezone = "" }, false},
		{"unknown timezone", func(m *ParquetMapping) { m.Timezone = "Unknown/Zone" }, false},
		{"repeated", func(m *ParquetMapping) { m.Measures[0].Value = []string{"list"} }, false},
		{"missing", func(m *ParquetMapping) { m.Timestamp = []string{"missing"} }, false},
		{"not expression", func(m *ParquetMapping) { m.Timestamp = []string{"ts; SELECT 1"} }, false},
		{"duplicate wide", func(m *ParquetMapping) { m.Measures = append(m.Measures, m.Measures[0]) }, false},
		{"duplicate long", func(m *ParquetMapping) {
			m.Measures[0].Name = ""
			m.Measures[0].NameColumn = []string{"metric"}
			m.Measures = append(m.Measures, m.Measures[0])
		}, false},
		{"unknown unit", func(m *ParquetMapping) { m.Measures[0].Unit = "seconds-ish" }, false},
		{"histogram scalar", func(m *ParquetMapping) { m.Measures[0].Kind = KindHistogram }, false},
		{"summary remains summary", func(m *ParquetMapping) { m.Measures[0].Kind = KindSummary }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_ms", Timezone: "UTC", Measures: []ParquetMeasure{{Value: []string{"nested", "value"}, Name: "latency", Kind: KindObservation, Unit: "ms"}}}
			tc.mutate(&m)
			if err := m.Validate(schema); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
