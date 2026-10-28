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

// VuGen writes a tab delimiter as a literal tab between quotes. Trimming it
// had turned every tab-delimited file into one comma-delimited column, so each
// parameter held a whole line and same-line parameters lost their row partner.
func TestParseParameterFileKeepsQuotedTabDelimiter(t *testing.T) {
	section := func(name, selectNext, update string) string {
		return "[parameter:" + name + "]\nColumnName=\"" + name + "\"\nDelimiter=\"\t\"\nGenerateNewVal=\"" + update + "\"\nParamName=\"" + name + "\"\nSelectNextRow=\"" + selectNext + "\"\nTable=\"Servers.dat\"\nType=\"Table\"\n"
	}
	parsed := ParseParameterFile("Java.prm",
		section("serverURL", "Sequential", "EachIteration")+section("userName", "Same line as serverURL", "Once"),
		map[string]string{"Servers.dat": "serverURL\tuserName\r\ntcp://a.example.test:1\tuser-a\r\ntcp://b.example.test:1\tuser-b\r\n"},
	)
	if len(parsed.Warnings) != 0 || len(parsed.DataSources) != 1 {
		t.Fatalf("warnings = %#v, data sources = %#v, want one tab-delimited source", parsed.Warnings, parsed.DataSources)
	}
	if source := parsed.DataSources[0]; source.Delimiter != "\t" || !source.FirstRowHeader {
		t.Fatalf("data source = %#v, want a tab delimiter and a header row", source)
	}
	params := map[string]dsl.Parameter{}
	for _, parameter := range parsed.Parameters {
		params[parameter.Name] = parameter
	}
	if got := params["username"]; got.SameLineAs != "serverurl" || got.DataSource != params["serverurl"].DataSource || got.Column != "userName" {
		t.Fatalf("userName = %#v, want same-line sync to serverURL in one source", got)
	}
	if value, ok := firstParameterDataSourceValue(parsed.DataSources, parsed.DataSources[0].Name, "userName"); !ok || value != "user-a" {
		t.Fatalf("first userName = %q (%v), want the second column only", value, ok)
	}
}

func TestRewriteKnownParameterReferencesMultipartHeaders(t *testing.T) {
	body := &dsl.MultipartBody{
		Parts: []dsl.MultipartPart{
			{
				Name:        "field_{{var.token}}",
				Value:       "val_{{var.token}}",
				Filename:    "file_{{var.token}}.txt",
				ContentType: "text/plain; boundary={{var.token}}",
				Headers: map[string]string{
					"X-Header-{{var.token}}": "header-val-{{var.token}}",
					"X-Static":               "keep-{{var.unknown}}",
				},
			},
		},
	}
	rewriteKnownParameterReferencesMultipart(body, map[string]struct{}{
		"token": {},
	})
	part := body.Parts[0]
	if part.Name != "field_{{param.token}}" ||
		part.Value != "val_{{param.token}}" ||
		part.Filename != "file_{{param.token}}.txt" ||
		part.ContentType != "text/plain; boundary={{param.token}}" {
		t.Fatalf("unexpected multipart part fields: %#v", part)
	}
	if len(part.Headers) != 2 {
		t.Fatalf("part.Headers len = %d, want 2: %#v", len(part.Headers), part.Headers)
	}
	if got := part.Headers["X-Header-{{param.token}}"]; got != "header-val-{{param.token}}" {
		t.Fatalf("rewritten header = %q, want %q (headers=%#v)", got, "header-val-{{param.token}}", part.Headers)
	}
	if _, exists := part.Headers["X-Header-{{var.token}}"]; exists {
		t.Fatalf("old key X-Header-{{var.token}} was not replaced: %#v", part.Headers)
	}
	if got := part.Headers["X-Static"]; got != "keep-{{var.unknown}}" {
		t.Fatalf("X-Static header = %q, want %q", got, "keep-{{var.unknown}}")
	}
}
