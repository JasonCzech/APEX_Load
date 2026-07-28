package enterprisearchive

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestParseParameterFileResolvesGeneratedColumnToMatchingHeader(t *testing.T) {
	parsed := ParseParameterFile("NEXT31.prm", `[parameter:ssn]
ColumnName="Col 1"
Delimiter=","
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueWithLast"
ParamName="ssn"
SelectNextRow="Sequential"
StartRow="1"
Table="SPI_Info.dat"
Type="Table"
`, map[string]string{
		"SPI_Info.dat": "birth_date,ssn,dl_number\nsample-date,sample-ssn,sample-dl\n",
	})

	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", parsed.Warnings)
	}
	if len(parsed.Parameters) != 1 {
		t.Fatalf("parameters = %#v, want one parsed parameter", parsed.Parameters)
	}
	param := parsed.Parameters[0]
	if param.Name != "ssn" || param.DataSource != "spi_info" || param.Column != "ssn" {
		t.Fatalf("parameter = %#v, want ssn bound to SPI_Info.dat ssn header", param)
	}
	if len(parsed.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one parsed data source", parsed.DataSources)
	}
	source := parsed.DataSources[0]
	if source.Name != "spi_info" || source.Path != "" || source.FileName != "SPI_Info.dat" || !source.FirstRowHeader || len(source.Columns) != 0 {
		t.Fatalf("data source = %#v, want first-row-header SPI_Info.dat source", source)
	}
	if source.Delimiter != "," {
		t.Fatalf("data source delimiter = %q, want explicit comma", source.Delimiter)
	}
	if got := dsl.ResolveDataSourceDelimiter(source, source.InlineData); got != ',' {
		t.Fatalf("runtime delimiter = %q, want comma", got)
	}
}

func TestParseParameterFileKeepsGeneratedColumnWithoutMatchingHeader(t *testing.T) {
	parsed := ParseParameterFile("Users.prm", `[parameter:account]
ColumnName="Col 2"
Delimiter=","
GenerateNewVal="EachIteration"
ParamName="account"
SelectNextRow="Sequential"
Table="Users.dat"
Type="Table"
`, map[string]string{
		"Users.dat": "first_name,last_name\nAda,Lovelace\n",
	})

	if len(parsed.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", parsed.Warnings)
	}
	if len(parsed.Parameters) != 1 {
		t.Fatalf("parameters = %#v, want one parsed parameter", parsed.Parameters)
	}
	param := parsed.Parameters[0]
	if param.Column != "Col 2" {
		t.Fatalf("parameter = %#v, want generated column retained when no matching header exists", param)
	}
	if len(parsed.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one parsed data source", parsed.DataSources)
	}
	source := parsed.DataSources[0]
	if source.FirstRowHeader || len(source.Columns) != 2 || source.Columns[1] != "Col 2" {
		t.Fatalf("data source = %#v, want generated-column data source fallback", source)
	}
}

func TestApplyKnownParameterReferencesTraversesEveryRequestSurface(t *testing.T) {
	script := &dsl.Script{
		Parameters: []dsl.Parameter{{Name: "token", Type: "list", Values: []string{"value"}}},
		Init: []dsl.Action{{
			Name:       "init",
			Type:       "GET",
			Target:     "/init/{{var.token}}",
			HeaderList: dsl.HeaderList{{Name: "X-Init", Value: "{{var.token}}"}},
		}},
		Actions: []dsl.Action{
			{
				Name: "control",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "{{var.token}}", Op: "exists"},
					Steps: []dsl.Action{{
						Name:       "nested",
						Type:       "POST",
						Target:     "/nested",
						HeaderList: dsl.HeaderList{{Name: "X-Nested", Value: "{{var.token}}"}},
						Body:       "{{var.token}}",
					}},
				},
			},
			{
				Name: "sequence",
				Type: "http_sequence",
				Params: map[string]any{"requests": []any{
					map[string]any{
						"name":   "mapped",
						"type":   "GET",
						"target": "/mapped/{{var.token}}",
						"header_list": dsl.HeaderList{{
							Name: "X-Mapped", Value: "{{var.token}}",
						}},
					},
					dsl.Action{
						Name:       "typed",
						Type:       "GET",
						Target:     "/typed/{{var.token}}",
						HeaderList: dsl.HeaderList{{Name: "X-Typed", Value: "{{var.token}}"}},
					},
				}},
			},
		},
		Teardown: []dsl.Action{{
			Name:   "teardown",
			Type:   "POST",
			Target: "/teardown",
			Body:   "{{var.token}}",
		}},
	}

	applyKnownParameterReferences(script)

	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), "{{var.token}}") {
		t.Fatalf("known parameter reference remained outside the top-level action lane: %s", payload)
	}
	if count := strings.Count(string(payload), "{{param.token}}"); count < 9 {
		t.Fatalf("rewritten parameter references = %d, want every phase/nested/sequence/header surface: %s", count, payload)
	}
	if script.Init[0].HeaderList[0].Value != "{{param.token}}" ||
		script.Actions[0].Control.Steps[0].HeaderList[0].Value != "{{param.token}}" {
		t.Fatalf("authoritative HeaderList values were not rewritten: %#v / %#v", script.Init[0], script.Actions[0])
	}
}
