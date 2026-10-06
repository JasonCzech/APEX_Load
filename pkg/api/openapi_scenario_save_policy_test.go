package api

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScenarioSavePolicyUnavailableOpenAPIRefusalContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ path, method string }{{"/api/v1/projects", "post"}, {"/api/v1/projects/{id}", "put"}} {
		operation := doc.Paths[route.path][route.method]
		policy := operation.RequestBody.Content["application/json"].Schema.Properties["settings"].Properties["authoring"].Properties["require_scenario_save_if_match"]
		if policy.Type != "boolean" || !strings.Contains(policy.Description, "true is rejected with 400") || !strings.Contains(policy.Description, "unavailable") {
			t.Fatalf("%s %s advertises ineffective enablement: %#v", route.method, route.path, policy)
		}
	}
	put := doc.Paths["/api/v1/scenarios/{id}"]["put"]
	condition := false
	for _, parameter := range put.Parameters {
		if parameter.Name == "If-Match" {
			condition = parameter.In == "header" && !parameter.Required && parameter.Schema.Type == "string" && strings.Contains(parameter.Description, "unavailable") && strings.Contains(parameter.Description, "503") && strings.Contains(parameter.Description, "ownership")
		}
	}
	if !condition {
		t.Fatal("explicit unsupported If-Match refusal is undocumented")
	}
	response := put.Responses["503"]
	body := response.Content["application/json"].Schema
	if body.Properties["error"].Type != "string" || body.Properties["ref"].Type != "string" || !reflect.DeepEqual(body.Properties["code"].Enum, []string{"scenario_save_if_match_unavailable", "scenario_save_strict_policy_unavailable"}) || !reflect.DeepEqual(body.Required, []string{"error"}) || !strings.Contains(response.Description, "generic error/ref") {
		t.Fatal("known capability codes or generic private-failure compatibility are undocumented")
	}
	get := doc.Paths["/api/v1/scenarios/{id}"]["get"]
	view := false
	for _, parameter := range get.Parameters {
		if parameter.Name == "view" {
			view = parameter.In == "query" && !parameter.Required && reflect.DeepEqual(parameter.Schema.Enum, []string{"authoring"})
		}
	}
	if !view {
		t.Fatal("scenario authoring view is undocumented")
	}
	etag, exists := get.Responses["200"].Headers["ETag"]
	if !exists || !strings.Contains(etag.Description, "scenario-authoring-v1:") || !strings.Contains(etag.Description, "only for view=authoring") {
		t.Fatalf("scenario authoring ETag is undocumented or ambiguous: %#v", etag)
	}
	if schemas := get.Responses["200"].Content["application/json"].Schema.OneOf; len(schemas) != 2 || !reflect.DeepEqual(schemas[1].Properties["projection_version"].Enum, []string{scenarioAuthoringProjectionVersion}) {
		t.Fatal("scenario authoring projection schema is undocumented")
	}
}
