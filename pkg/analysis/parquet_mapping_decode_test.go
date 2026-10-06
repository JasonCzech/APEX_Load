package analysis

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const strictParquetDefinitionFixture = `{"version":2,"timestamp":["ts"],"timestamp_encoding":"unix_s","timezone":"UTC","measures":[{"value":["v"],"name":"v","kind":"gauge","unit":"count"}],"identity":{"source":["nested","source"],"dimensions":[{"name":"a/b~c","value":["label"]}]}}`

func TestDecodeParquetMappingDefinitionRejectsUnsupportedAmbiguousAndMalformedFields(t *testing.T) {
	valid, err := DecodeParquetMappingDefinition([]byte(strictParquetDefinitionFixture))
	if err != nil || valid.Identity.Source[0] != "nested" || valid.Identity.Dimensions[0].Name != "a/b~c" {
		t.Fatal("valid authored mapping lost identity", valid, err)
	}
	for name, body := range map[string]string{
		"unknown_root":        strings.Replace(strictParquetDefinitionFixture, `"version":2`, `"version":2,"summary_only":true`, 1),
		"unknown_identity":    strings.Replace(strictParquetDefinitionFixture, `"source":[`, `"scope":"run","source":[`, 1),
		"unknown_measure":     strings.Replace(strictParquetDefinitionFixture, `"kind":"gauge"`, `"kind":"gauge","statistic":"p95"`, 1),
		"unknown_dimension":   strings.Replace(strictParquetDefinitionFixture, `"name":"a/b~c"`, `"name":"a/b~c","case_insensitive":true`, 1),
		"duplicate_root":      strings.Replace(strictParquetDefinitionFixture, `"version":2`, `"version":1,"version":2`, 1),
		"escaped_duplicate":   strings.Replace(strictParquetDefinitionFixture, `"version":2`, `"version":1,"\u0076ersion":2`, 1),
		"duplicate_identity":  strings.Replace(strictParquetDefinitionFixture, `"source":["nested","source"]`, `"source":["old"],"source":["nested","source"]`, 1),
		"duplicate_measure":   strings.Replace(strictParquetDefinitionFixture, `"kind":"gauge"`, `"kind":"counter","kind":"gauge"`, 1),
		"duplicate_dimension": strings.Replace(strictParquetDefinitionFixture, `"name":"a/b~c"`, `"name":"old","name":"a/b~c"`, 1),
		"case_alias":          strings.Replace(strictParquetDefinitionFixture, `"version":2`, `"Version":2`, 1),
		"trailing":            strictParquetDefinitionFixture + ` {}`,
		"null_identity":       strings.Replace(strictParquetDefinitionFixture, `"identity":{"source":["nested","source"],"dimensions":[{"name":"a/b~c","value":["label"]}]}`, `"identity":null`, 1),
		"path_object":         strings.Replace(strictParquetDefinitionFixture, `["nested","source"]`, `{"nested":"source"}`, 1),
		"surrogate":           strings.Replace(strictParquetDefinitionFixture, `a/b~c`, `\ud800`, 1),
		"nul":                 strings.Replace(strictParquetDefinitionFixture, `a/b~c`, `a\u0000b`, 1),
		"scalar_over":         strings.Replace(strictParquetDefinitionFixture, `a/b~c`, strings.Repeat("x", 129), 1),
		"fractional_version":  strings.Replace(strictParquetDefinitionFixture, `"version":2`, `"version":2.0`, 1),
		"raw_limit":           strings.Repeat(" ", maxParquetMappingDefinitionBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			out, err := DecodeParquetMappingDefinition([]byte(body))
			if err == nil || out.Version != 0 || out.Identity != nil || out.Measures != nil {
				t.Fatal("unsupported mapping became a partially accepted typed definition", out, err)
			}
		})
	}
	legacy := strings.Replace(strictParquetDefinitionFixture, `"version":2`, `"version":1`, 1)
	legacy = legacy[:strings.Index(legacy, `,"identity"`)] + `}`
	if _, err := DecodeParquetMappingDefinition([]byte(legacy)); err != nil {
		t.Fatal("valid legacy new-write mapping rejected", err)
	}
}

func TestDecodeParquetMappingDefinitionExactCollectionAndScalarBounds(t *testing.T) {
	m, err := DecodeParquetMappingDefinition([]byte(strictParquetDefinitionFixture))
	if err != nil {
		t.Fatal(err)
	}
	m.Timestamp = make(ParquetColumnPath, 16)
	for i := range m.Timestamp {
		m.Timestamp[i] = strings.Repeat("p", 256)
	}
	m.Measures = make([]ParquetMeasure, 64)
	for i := range m.Measures {
		m.Measures[i] = ParquetMeasure{Value: []string{"v"}, Name: fmt.Sprintf("m%d", i), Kind: KindGauge, Unit: "count"}
	}
	m.Identity.Dimensions = make([]ParquetDimensionMapping, 16)
	for i := range m.Identity.Dimensions {
		m.Identity.Dimensions[i] = ParquetDimensionMapping{Name: fmt.Sprintf("%03d", i) + strings.Repeat("n", 125), Value: []string{"label"}}
	}
	body, _ := json.Marshal(m)
	if _, err := DecodeParquetMappingDefinition(body); err != nil {
		t.Fatal("exact collection/scalar bounds rejected", err)
	}
	for name, change := range map[string]func(*ParquetMapping){
		"path":    func(m *ParquetMapping) { m.Timestamp = append(m.Timestamp, "extra") },
		"measure": func(m *ParquetMapping) { m.Measures = append(m.Measures, m.Measures[0]) },
		"dimension": func(m *ParquetMapping) {
			m.Identity.Dimensions = append(m.Identity.Dimensions, m.Identity.Dimensions[0])
		},
		"path_scalar": func(m *ParquetMapping) { m.Timestamp[0] += "x" },
	} {
		t.Run(name, func(t *testing.T) {
			var next ParquetMapping
			if json.Unmarshal(body, &next) != nil {
				t.Fatal("fixture decode")
			}
			change(&next)
			invalid, _ := json.Marshal(next)
			if _, err := DecodeParquetMappingDefinition(invalid); err == nil {
				t.Fatal("collection/scalar bound exceeded")
			}
		})
	}
}
