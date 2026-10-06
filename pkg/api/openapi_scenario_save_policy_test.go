package api

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScenarioAuthoringOpenAPIContract(t *testing.T) {
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
		if policy.Type != "boolean" || !strings.Contains(policy.Description, "428") || !strings.Contains(policy.Description, "inside the save transaction") {
			t.Fatalf("%s %s strict scenario policy is undocumented: %#v", route.method, route.path, policy)
		}
	}
	put := doc.Paths["/api/v1/scenarios/{id}"]["put"]
	condition := false
	for _, parameter := range put.Parameters {
		if parameter.Name == "If-Match" {
			condition = parameter.In == "header" && !parameter.Required && parameter.Schema.Type == "string" && strings.Contains(parameter.Description, "scenario-authoring-v1") && strings.Contains(parameter.Description, "412") && strings.Contains(parameter.Description, "428") && strings.Contains(parameter.Description, "ownership")
		}
	}
	if !condition {
		t.Fatal("conditioned scenario save is undocumented")
	}
	for _, status := range []string{"400", "404", "409", "412", "428"} {
		if _, exists := put.Responses[status]; !exists {
			t.Fatalf("scenario PUT response %s is undocumented", status)
		}
	}
	saved := put.Responses["200"]
	mode, hasMode := saved.Headers["X-APEX-Scenario-Save-Mode"]
	if _, hasTag := saved.Headers["ETag"]; !hasTag || !hasMode || !reflect.DeepEqual(mode.Schema.Enum, []string{"conditioned", "legacy_unconditioned"}) {
		t.Fatal("scenario save headers are undocumented")
	}
	body := put.Responses["503"].Content["application/json"].Schema
	if !reflect.DeepEqual(body.Properties["code"].Enum, []string{"scenario_save_outcome_unknown", "scenario_save_outcome_audit_unavailable", "scenario_save_if_match_unavailable", "scenario_save_strict_policy_unavailable"}) || !reflect.DeepEqual(body.Required, []string{"error"}) || !strings.Contains(put.Responses["503"].Description, "never retry automatically") {
		t.Fatal("scenario save outcome codes are undocumented")
	}
	for key := range scenarioUnconditionedWriterRoutes {
		method, path, _ := strings.Cut(key, " ")
		operation := doc.Paths[path][strings.ToLower(method)]
		refused := false
		for _, parameter := range operation.Parameters {
			if parameter.Name == "If-Match" {
				refused = strings.Contains(parameter.Description, "refused with 503")
			}
		}
		if !refused || !reflect.DeepEqual(operation.Responses["503"].Content["application/json"].Schema.Properties["code"].Enum, []string{"scenario_writer_if_match_unavailable"}) {
			t.Fatalf("%s does not document its If-Match refusal", key)
		}
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
