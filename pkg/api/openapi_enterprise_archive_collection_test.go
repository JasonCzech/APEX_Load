package api

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestEnterpriseArchiveCollectionOpenAPIContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths      map[string]map[string]oasOperation `yaml:"paths"`
		Components struct {
			Schemas map[string]oasSchema `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/scripts/import/enterprise-script-archive", "/api/v1/scripts/import/enterprise-script-archive/validate"} {
		operation := spec.Paths[path]["post"]
		if operation.RequestBody == nil {
			t.Fatalf("%s has no multipart request body", path)
		}
		multipart := operation.RequestBody.Content["multipart/form-data"].Schema
		member := multipart.Properties["archive_member"]
		if member.Type != "string" || !strings.Contains(member.Description, "source_name") || openAPIStringSliceContains(multipart.Required, "archive_member") {
			t.Errorf("%s archive_member upload contract=%#v", path, member)
		}
		found := false
		for _, parameter := range operation.Parameters {
			if parameter.Name == "archive_member" && parameter.In == "query" {
				found = true
				if parameter.Required || parameter.Schema.Type != "string" || !strings.Contains(parameter.Description, "independently") {
					t.Errorf("%s archive_member query=%#v", path, parameter)
				}
			}
		}
		if !found {
			t.Errorf("%s lacks archive_member query selection", path)
		}
		response, ok := operation.Responses["200"]
		if !ok || response.Content["application/json"].Schema.Ref != "#/components/schemas/EnterpriseArchiveReviewResponse" || !strings.Contains(response.Description, "individual members fail") {
			t.Errorf("%s response contract=%#v", path, response)
		}
		if _, ok := operation.Responses["2XX"]; ok {
			t.Errorf("%s retains an untyped success response", path)
		}
	}
	schemas := spec.Components.Schemas
	union := schemas["EnterpriseArchiveReviewResponse"]
	if len(union.OneOf) != 2 || union.OneOf[0].Ref != "#/components/schemas/EnterpriseArchiveScriptReviewResponse" || union.OneOf[1].Ref != "#/components/schemas/EnterpriseArchiveCollectionResponse" {
		t.Fatalf("review envelope union=%#v", union)
	}
	child := schemas["EnterpriseArchiveScriptReviewResponse"]
	if !openAPIStringSliceContains(child.Required, "scripts") || child.Properties["scripts"].Type != "array" {
		t.Fatal("ordinary reviews lost their independent draft list")
	}
	for _, field := range []string{"summary", "source_review", "fidelity_report", "review_artifact", "parameterization", "preflight"} {
		if child.Properties[field].Type != "object" {
			t.Errorf("per-member review property %s lost its contract", field)
		}
	}
	if child.Properties["source_secret_session_id"].Type != "string" || child.Properties["source_secret_session_expires_at"].Format != "date-time" || child.Properties["source_secret_migration_allowed"].Type != "boolean" {
		t.Fatal("member-specific credential capability fields lost their contract")
	}
	collection := schemas["EnterpriseArchiveCollectionResponse"]
	if collection.Properties["archive_collection"].Type != "boolean" || collection.Properties["archives"].Items == nil || collection.Properties["archives"].Items.Ref != "#/components/schemas/EnterpriseArchiveCollectionMember" {
		t.Fatalf("collection discriminator/member schema=%#v", collection)
	}
	for _, field := range []string{"count", "successful_count", "failed_count"} {
		if collection.Properties[field].Type != "integer" || !openAPIStringSliceContains(collection.Required, field) {
			t.Errorf("collection accounting field %s lost its contract", field)
		}
	}
	if _, exists := collection.Properties["scripts"]; exists || openAPIStringSliceContains(collection.Required, "scripts") {
		t.Fatal("collection must not duplicate or flatten sibling drafts")
	}
	if loose := collection.Properties["unassigned_files"]; loose.Type != "array" || !loose.Nullable {
		t.Fatalf("nullable unassigned file inventory=%#v", loose)
	}
	member := schemas["EnterpriseArchiveCollectionMember"]
	if !reflect.DeepEqual(member.Required, []string{"source_name", "source_size"}) || member.Properties["source_name"].Type != "string" || member.Properties["source_size"].Type != "integer" || member.Properties["response"].Ref != "#/components/schemas/EnterpriseArchiveScriptReviewResponse" || member.Properties["error"].Type != "string" {
		t.Fatalf("independent success/error member contract=%#v", member)
	}
}

func TestEnterpriseArchiveCollectionOpenAPIContractLeavesOtherRoutesUnchanged(t *testing.T) {
	for _, route := range []discoveredRoute{
		{Method: http.MethodGet, Path: "/api/v1/scripts/import/enterprise-script-archive"},
		{Method: http.MethodPost, Path: "/api/v1/scripts/import/enterprise-script-archive/save"},
		{Method: http.MethodPost, Path: "/api/v1/scripts/import/bruno/inspect"},
	} {
		contract := inferredRouteContract{Responses: map[string]oasResponse{"201": {Description: "original response"}}}
		applyEnterpriseArchiveCollectionContract(route, &contract)
		if len(contract.QueryParameters) != 0 || len(contract.Responses) != 1 || contract.Responses["201"].Description != "original response" {
			t.Errorf("unrelated route %s %s changed=%#v", route.Method, route.Path, contract)
		}
	}
}
