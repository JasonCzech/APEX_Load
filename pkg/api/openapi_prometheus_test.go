package api

import (
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestGeneratedOpenAPIPrometheusContracts(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var document oasDoc
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/projects/{id}/monitoring/prometheus-connections"
	for _, route := range []struct {
		method, path string
		update       bool
	}{{"post", base, false}, {"put", base + "/{connection_id}", true}} {
		body := document.Paths[route.path][route.method].RequestBody
		if body == nil || !body.Required {
			t.Fatal("connection mutation requires typed JSON")
		}
		schema := body.Content["application/json"].Schema
		if schema.Properties["name"].Type != "string" || !containsOpenAPIField(schema.Required, "name") || schema.Properties["revision"].Type != "integer" || containsOpenAPIField(schema.Required, "revision") != route.update {
			t.Fatal("incorrect draft/revision contract")
		}
		config := schema.Properties["config"]
		for _, field := range []string{"endpoint", "backend_profile", "auth_type", "azure_credential_mode", "azure_tenant_id"} {
			if config.Properties[field].Type != "string" {
				t.Fatalf("opaque profile field %s", field)
			}
		}
		for _, field := range []string{"token_secret", "username_secret", "password_secret", "client_secret_secret", "client_certificate_secret", "client_key_secret"} {
			if schema.Properties[field].Type != "string" || !schema.Properties[field].Nullable || containsOpenAPIField(schema.Required, field) {
				t.Fatalf("credential replacement %s requires nullable optional reference", field)
			}
		}
	}
	preview := document.Paths[base+"/{connection_id}/preview"]["post"].RequestBody
	if preview == nil {
		t.Fatal("missing preview contract")
	}
	queries := preview.Content["application/json"].Schema.Properties["selection"].Properties["queries"]
	if queries.Items == nil || queries.Items.Properties["expression"].Type != "string" || queries.Items.Properties["terminal_expression"].Type != "string" {
		t.Fatal("query definitions must be typed")
	}
	for _, field := range []string{"token_secret_name", "client_secret_secret_name", "client_certificate_secret_name", "client_key_secret_name"} {
		response := document.Paths[base+"/{connection_id}"]["get"].Responses["200"].Content["application/json"].Schema
		if _, exists := response.Properties[field]; exists {
			t.Fatalf("public connection exposes secret reference %s", field)
		}
	}
}
