package api

import (
	yaml "gopkg.in/yaml.v3"
	"net/http"
	"strings"
	"testing"
)

func TestScriptAuthoringOpenAPIContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	get := doc.Paths["/api/v1/scripts/{id}"]["get"]
	schema := get.Responses["200"].Content["application/json"].Schema
	if len(schema.OneOf) != 2 {
		t.Fatal("authoring query does not declare both exact response shapes")
	}
	projection := schema.OneOf[1]
	if projection.Properties["content_revision"].Type != "string" || len(projection.Properties["projection_version"].Enum) != 1 || projection.Properties["projection_version"].Enum[0] != scriptAuthoringProjectionVersion || projection.Properties["library_placement"].Properties["release_ids"].Items.Type != "string" || projection.Properties["script"].Properties["goal_config"].Properties["target_average_tps"].Type != "number" {
		t.Fatal("authoring version/nonce/placement/goals lost fixed types")
	}
	for _, field := range []string{"content_revision", "created_at", "updated_at", "archived_at", "authoring_health"} {
		if _, exists := projection.Properties["script"].Properties[field]; exists {
			t.Fatalf("non-authored field %s leaked into canonical document", field)
		}
	}
	if !strings.Contains(get.Responses["200"].Headers["ETag"].Description, "only for view=authoring") {
		t.Fatal("ETag falsely applies to enriched response")
	}
	view := false
	for _, parameter := range get.Parameters {
		if parameter.Name == "view" {
			view = parameter.In == "query" && len(parameter.Schema.Enum) == 1 && parameter.Schema.Enum[0] == "authoring" && !parameter.Required
		}
	}
	if !view {
		t.Fatal("exact authoring query missing")
	}
	put := doc.Paths["/api/v1/scripts/{id}"]["put"]
	condition := false
	for _, parameter := range put.Parameters {
		if parameter.Name == "If-Match" {
			condition = parameter.In == "header" && !parameter.Required && strings.Contains(parameter.Description, "412") && strings.Contains(parameter.Description, "428")
		}
	}
	if !condition {
		t.Fatal("conditional project-policy precondition missing")
	}
	for _, status := range []string{"400", "404", "412", "428", "503"} {
		if _, exists := put.Responses[status]; !exists {
			t.Fatalf("missing %s refusal", status)
		}
	}
	if put.Responses["200"].Headers["X-APEX-Script-Save-Mode"].Schema.Type != "string" {
		t.Fatal("explicit compatibility mode missing")
	}
}

func TestOpenAPISecretRebindPureAuthoringAndLegacyResponsesAreTyped(t *testing.T) {
	route := discoveredRoute{Method: http.MethodPost, Path: "/api/v1/scripts/secret-references/rebind", Handler: "handleRebindScriptSecretReference"}
	contracts, err := inferRouteContracts("server.go", []discoveredRoute{route})
	if err != nil {
		t.Fatal(err)
	}
	schema := contracts[route.Method+" "+route.Path].Responses["200"].Content["application/json"].Schema
	if len(schema.OneOf) != 2 {
		t.Fatal("missing authored/legacy response contract", schema)
	}
	for i, shape := range schema.OneOf {
		script := shape.Properties["script"]
		if script.Type != "object" || script.Properties["name"].Type != "string" || shape.Properties["changed_locations"].Type != "array" {
			t.Fatal("untyped transformed draft", shape)
		}
		_, created := script.Properties["created_at"]
		if created != (i == 1) {
			t.Fatal("context field crossed authored/legacy boundary", i, script.Properties)
		}
	}
}
