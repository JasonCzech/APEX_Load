package analysis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParquetIdentityDisclosureRefusesRecognizedProtectedPathsWithoutRewritingLabels(t *testing.T) {
	protected := []ParquetColumnPath{{"password"}, {"API_TOKEN"}, {"credentials", "client_secret"}, {"api", "key"}, {"client", "secret"}, {"storage_state"}, {"auth%2Eprivate_key"}, {"credentials%5Bsession_id%5D"}}
	for _, path := range protected {
		for _, role := range []string{"source", "host", "service", "dimension"} {
			t.Run(strings.Join(path, "/")+"/"+role, func(t *testing.T) {
				mapping, schema := identityMappingFixture()
				switch role {
				case "source":
					mapping.Identity.Source = path
				case "host":
					mapping.Identity.Host = path
				case "service":
					mapping.Identity.Service = path
				case "dimension":
					mapping.Identity.Dimensions[0].Value = path
				}
				schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: path})
				before, _ := json.Marshal(mapping)
				if mapping.Identity.ValidateDisclosure() == nil || mapping.ValidateDefinition() == nil || mapping.Validate(schema) == nil {
					t.Fatal("protected path admitted as a published identity")
				}
				if out, err := DecodeParquetMappingDefinition(before); err == nil || out.Identity != nil {
					t.Fatal("closed mapping decoder published a protected path", out, err)
				}
				after, _ := json.Marshal(mapping)
				if string(before) != string(after) {
					t.Fatal("refusal rewrote mapping or merged its identity")
				}
			})
		}
	}
	// Only recognized input paths are refused. Literal output label names remain
	// exact data, and substring lookalikes do not broaden the credential classifier.
	for _, path := range []ParquetColumnPath{{"transaction"}, {"tokenizer"}, {"author"}, {"monkey"}, {"metadata", "api_operation"}, {"東京"}} {
		mapping, schema := identityMappingFixture()
		mapping.Identity.Dimensions = []ParquetDimensionMapping{{Name: "password", Value: path}}
		schema.Columns = append(schema.Columns, ParquetColumn{PathComponents: path})
		if err := mapping.Validate(schema); err != nil {
			t.Fatal("ordinary analytical path or exact output label refused", path, err)
		}
		raw, _ := json.Marshal(mapping)
		out, err := DecodeParquetMappingDefinition(raw)
		if err != nil || !reflect.DeepEqual(out.Identity.Dimensions, mapping.Identity.Dimensions) {
			t.Fatal("ordinary path/label changed", out, err)
		}
	}
}

func TestParquetIdentityDisclosureBoundsBeforeCredentialClassification(t *testing.T) {
	for _, identity := range []*ParquetIdentityMapping{
		{Source: ParquetColumnPath{strings.Repeat("x", 4<<20)}},
		{Host: make(ParquetColumnPath, 17)},
		{Service: ParquetColumnPath{"a\x00b"}},
		{Source: ParquetColumnPath{string([]byte{255})}},
		{Dimensions: make([]ParquetDimensionMapping, 17)},
		{Dimensions: []ParquetDimensionMapping{{Name: "n"}}},
	} {
		if identity.ValidateDisclosure() == nil {
			t.Fatal("invalid identity geometry reached disclosure admission")
		}
	}
	var absent *ParquetIdentityMapping
	if absent.ValidateDisclosure() != nil {
		t.Fatal("legacy nil identity unavailable")
	}
}
