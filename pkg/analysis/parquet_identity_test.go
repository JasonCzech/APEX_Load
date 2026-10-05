package analysis

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func identityMappingFixture() (ParquetMapping, ParquetSchemaPreview) {
	mapping := ParquetMapping{Version: 2, Timestamp: ParquetColumnPath{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []ParquetMeasure{{Value: ParquetColumnPath{"value"}, Name: "v", Kind: KindGauge, Unit: "count"}}, Identity: &ParquetIdentityMapping{Source: ParquetColumnPath{"nested", "source"}, Host: ParquetColumnPath{"host"}, Service: ParquetColumnPath{"service"}, Dimensions: []ParquetDimensionMapping{{Name: "group", Value: ParquetColumnPath{"group"}}}}}
	schema := ParquetSchemaPreview{Columns: []ParquetColumn{{PathComponents: ParquetColumnPath{"ts"}}, {PathComponents: ParquetColumnPath{"value"}}, {PathComponents: ParquetColumnPath{"nested", "source"}}, {PathComponents: ParquetColumnPath{"host"}}, {PathComponents: ParquetColumnPath{"service"}}, {PathComponents: ParquetColumnPath{"group"}}}}
	return mapping, schema
}
func TestParquetIdentityMappingVersionBoundsAndSchema(t *testing.T) {
	mapping, schema := identityMappingFixture()
	if err := mapping.Validate(schema); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ParquetMapping, *ParquetSchemaPreview){
		"legacy_identity": func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Version = 1 },
		"unknown_version": func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Version = 3 },
		"duplicate_name": func(m *ParquetMapping, _ *ParquetSchemaPreview) {
			m.Identity.Dimensions = append(m.Identity.Dimensions, m.Identity.Dimensions[0])
		},
		"missing_column":  func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Identity.Host = ParquetColumnPath{"missing"} },
		"repeated_column": func(_ *ParquetMapping, s *ParquetSchemaPreview) { s.Columns[2].Repeated = true },
		"path_depth":      func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Identity.Source = make(ParquetColumnPath, 17) },
		"path_nul": func(m *ParquetMapping, s *ParquetSchemaPreview) {
			m.Identity.Source = ParquetColumnPath{"a\x00b"}
			s.Columns = append(s.Columns, ParquetColumn{PathComponents: m.Identity.Source})
		},
		"path_utf8": func(m *ParquetMapping, s *ParquetSchemaPreview) {
			m.Identity.Source = ParquetColumnPath{string([]byte{255})}
			s.Columns = append(s.Columns, ParquetColumn{PathComponents: m.Identity.Source})
		},
		"metric_nul":  func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Measures[0].Name = "a\x00b" },
		"metric_utf8": func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Measures[0].Name = string([]byte{255}) },
		"name_bound": func(m *ParquetMapping, _ *ParquetSchemaPreview) {
			m.Identity.Dimensions[0].Name = strings.Repeat("x", 129)
		},
		"name_nul":  func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Identity.Dimensions[0].Name = "label\x00suffix" },
		"name_utf8": func(m *ParquetMapping, _ *ParquetSchemaPreview) { m.Identity.Dimensions[0].Name = string([]byte{255}) },
		"population": func(m *ParquetMapping, _ *ParquetSchemaPreview) {
			m.Identity.Dimensions = make([]ParquetDimensionMapping, 17)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, s := identityMappingFixture()
			change(&m, &s)
			if m.Validate(s) == nil {
				t.Fatal("invalid identity mapping accepted")
			}
		})
	}
	mapping.Version = 1
	mapping.Identity = nil
	if err := mapping.Validate(schema); err != nil {
		t.Fatal("legacy mapping unavailable", err)
	}
}
func TestNormalizedDimensionsExactBoundsUnicodeAndOwnership(t *testing.T) {
	values := map[string]string{}
	for i := 0; i < 16; i++ {
		values[fmt.Sprintf("%03d", i)+strings.Repeat("k", 125)] = strings.Repeat("v", 128)
	}
	body, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeNormalizedDimensions(string(body))
	if err != nil || len(decoded) != 16 {
		t.Fatal("exact dimensions rejected", err)
	}
	decoded["new"] = "owned"
	if len(values) != 16 {
		t.Fatal("decoder borrowed caller map")
	}
	valid, err := DecodeNormalizedDimensions(`{"😀":"\ud83d\ude00","empty":"","a/b~c":"replacement �"}`)
	if err != nil || valid["😀"] != "😀" || valid["a/b~c"] != "replacement �" {
		t.Fatal("valid Unicode/empty dimension lost", valid, err)
	}
	for _, invalid := range []string{`{"a":"1","a":"2"}`, `{"a":null}`, `{"a":1}`, `{"a":{}}`, `[]`, `{} {}`, `{"":"x"}`, `{"a":"\ud800"}`, `{"a":"\udc00"}`, `{"a":"\ud800x"}`, `{"a":"\u0000"}`, `{"a":"` + strings.Repeat("x", 129) + `"}`, `{"` + strings.Repeat("k", 129) + `":"x"}`, string([]byte{'{', '"', 'a', '"', ':', '"', 255, '"', '}'}), strings.Repeat("x", maxNormalizedDimensionsJSONBytes+1)} {
		if _, err := DecodeNormalizedDimensions(invalid); err == nil {
			t.Fatalf("invalid dimension object accepted: %q", invalid[:min(len(invalid), 100)])
		}
	}
	values["overflow"] = "x"
	body, _ = json.Marshal(values)
	if _, err := DecodeNormalizedDimensions(string(body)); err == nil {
		t.Fatal("17 dimensions accepted")
	}
}

func TestParquetIdentitySavedDefinitionBoundsAndActualSchemaSeparation(t *testing.T) {
	mapping, schema := identityMappingFixture()
	if err := mapping.ValidateDefinition(); err != nil {
		t.Fatal("identity definition failed without source file", err)
	}
	schema.Columns = schema.Columns[:2]
	if mapping.Validate(schema) == nil {
		t.Fatal("saved definition substituted for actual file schema")
	}
	for _, population := range []int{17, 10000} {
		mapping, _ = identityMappingFixture()
		mapping.Identity.Dimensions = make([]ParquetDimensionMapping, population)
		if mapping.ValidateDefinition() == nil {
			t.Fatal("saved mapping dimension population accepted", population)
		}
	}
	mapping, _ = identityMappingFixture()
	mapping.Measures = make([]ParquetMeasure, 65)
	if mapping.ValidateDefinition() == nil {
		t.Fatal("saved mapping measures escaped count bound")
	}
	mapping, _ = identityMappingFixture()
	mapping.Identity.Source = make(ParquetColumnPath, 17)
	if mapping.ValidateDefinition() == nil {
		t.Fatal("saved mapping path escaped depth bound")
	}
}
