package analysis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParquetProjectedDisclosureRefusesAllSelectedProtectedSourceRoles(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, role := range []string{"timestamp", "value", "name_column"} {
			for _, path := range []ParquetColumnPath{{"password"}, {"api", "key"}, {"credentials", "client_secret"}, {"auth%2Eprivate_key"}} {
				t.Run(strings.Join(path, "/")+"/"+role+string(rune('0'+version)), func(t *testing.T) {
					m, schema := identityMappingFixture()
					m.Version, m.Identity = version, nil
					schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: path})
					switch role {
					case "timestamp":
						m.Timestamp = path
					case "value":
						m.Measures[0].Value = path
					case "name_column":
						m.Measures[0].Name = ""
						m.Measures[0].NameColumn = path
					}
					before, _ := json.Marshal(m)
					if m.ValidateDisclosure() == nil || m.ValidateDefinition() == nil || m.Validate(schema) == nil {
						t.Fatal("protected selected source path became public evidence", role)
					}
					out, err := DecodeParquetMappingDefinition(before)
					if err == nil || len(out.Measures) != 0 {
						t.Fatal("closed decoder published protected mapping", out, err)
					}
					after, _ := json.Marshal(m)
					if string(after) != string(before) {
						t.Fatal("refusal rewrote mapping")
					}
				})
			}
		}
	}
}

func TestParquetProjectedDisclosureKeepsOrdinaryPathsAndOutputNames(t *testing.T) {
	m, schema := identityMappingFixture()
	m.Version, m.Identity = 1, nil
	m.Measures[0].Name = "password"
	before, _ := json.Marshal(m)
	if err := m.Validate(schema); err != nil {
		t.Fatal("constant output name became an input credential path", err)
	}
	out, err := DecodeParquetMappingDefinition(before)
	if err != nil || !reflect.DeepEqual(out, m) {
		t.Fatal("valid legacy mapping changed", out, err)
	}
	for _, path := range []ParquetColumnPath{{"tokenizer"}, {"author"}, {"monkey"}, {"metadata", "api_operation"}} {
		if err := ValidateParquetProjectedColumn(path); err != nil {
			t.Fatal("substring lookalike refused", path, err)
		}
	}
}

func TestParquetProjectedDisclosureBoundsBeforeClassification(t *testing.T) {
	oversized := ParquetColumnPath{strings.Repeat("x", 4<<20)}
	if got := testing.AllocsPerRun(10, func() {
		if ValidateParquetProjectedColumn(oversized) == nil {
			t.Fatal("oversized input admitted")
		}
	}); got != 0 {
		t.Fatal("oversized path allocated before refusal", got)
	}
	for _, path := range []ParquetColumnPath{nil, make(ParquetColumnPath, 17), {"a\x00b"}, {string([]byte{255})}, {"password", strings.Repeat("x", 257)}} {
		if ValidateParquetProjectedColumn(path) == nil {
			t.Fatal("invalid path admitted")
		}
	}
}
