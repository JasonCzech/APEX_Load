package enterprisearchive

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestParameterSourceMappingsUseMergedIndicesAndSourceFiles(t *testing.T) {
	content := "[parameter:Account]\nType=File\nTable=accounts.dat\nColumnName=Account\n"
	script := &dsl.Script{Parameters: []dsl.Parameter{{Name: "existing"}, {Name: "Account"}}, DataSources: []dsl.DataSource{{Name: "accounts", FileName: "accounts.dat"}}}
	result := &ConvertResult{Script: script}
	appendParameterSourceMappings(result, script, "test.prm", content, map[string]string{"test.prm": content, "data/accounts.dat": "Account\n123"}, 1, 0)
	mappings := result.SourceReview.Mappings
	if len(mappings) != 2 || mappings[0].TargetPath != "parameters[1]" || mappings[0].SourceFile != "test.prm" || mappings[0].StartLine != 1 || mappings[1].TargetPath != "data_sources[0]" || mappings[1].SourceFile != "data/accounts.dat" {
		t.Fatalf("incorrect provenance: %#v", mappings)
	}
}

func TestParameterSourceMappingsFallBackToDefinitionForMissingOrAmbiguousData(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string]string
	}{
		{"missing", map[string]string{}},
		{"ambiguous", map[string]string{"one/accounts.dat": "Account\n1", "two/accounts.dat": "Account\n2"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := "[parameter:Account]\nTable=accounts.dat\n"
			script := &dsl.Script{DataSources: []dsl.DataSource{{Name: "accounts", FileName: "accounts.dat"}}}
			result := &ConvertResult{Script: script}
			appendParameterSourceMappings(result, script, "test.prm", content, test.files, 0, 0)
			mapping := result.SourceReview.Mappings[0]
			if mapping.SourceFile != "test.prm" || mapping.TargetPath != "data_sources[0]" {
				t.Fatalf("ambiguous provenance must point to its definition: %#v", mapping)
			}
		})
	}
}
