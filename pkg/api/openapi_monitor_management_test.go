package api

import (
	"encoding/json"
	"go/ast"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestManagementOpenAPIContractsRemainExact(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err = yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	sources := doc.Paths["/api/v1/projects/{id}/monitor-sources"]["get"].Responses["200"].Content["application/json"].Schema
	if sources.Type != "array" || sources.Items == nil || sources.Items.Properties["id"].Type != "string" {
		t.Fatalf("source list is not a typed array: %#v", sources)
	}
	scope := sources.Items.Properties["management_scope"]
	if scope.Properties["entities"].Items == nil || scope.Properties["entities"].Items.Properties["kafka"].Properties["topic"].Type != "string" {
		t.Fatalf("source scope is opaque: %#v", scope)
	}
	saved := doc.Paths["/api/v1/projects/{id}/monitor-sources"]["post"].Responses["200"].Content["application/json"].Schema
	if saved.Type != "object" || saved.Properties["id"].Type != "string" {
		t.Fatalf("saved source is not a single typed model: %#v", saved)
	}
	operation := doc.Paths["/api/v1/tests/{id}/monitor-management"]["get"]
	response := operation.Responses["200"].Content["application/json"].Schema
	records := response.Properties["records"]
	if records.Items == nil {
		t.Fatal("missing typed records")
	}
	record := *records.Items
	if record.Properties["sequence"].Type != "string" {
		t.Fatal("receipt sequence lost exact string representation")
	}
	snapshot := record.Properties["evidence"].Properties["messaging_management"]
	for _, field := range []string{"generation", "poll_sequence", "missed_ticks"} {
		if snapshot.Properties[field].Type != "string" {
			t.Fatalf("%s lost exact string representation", field)
		}
	}
	if snapshot.Properties["rows"].Items == nil {
		t.Fatal("missing management rows")
	}
	row := *snapshot.Properties["rows"].Items
	if row.Properties["entity"].Properties["kafka"].Properties["partition"].Type != "integer" || row.Properties["observed_at"].Type != "string" || row.Properties["observed_at"].Format != "date-time" {
		t.Fatalf("row identity/time is opaque: %#v", row)
	}
	for _, field := range []struct{ group, name string }{{"kafka_offsets", "committed_next"}, {"service_bus_runtime", "active"}, {"solace_semp", "depth"}} {
		observation := row.Properties[field.group].Properties[field.name]
		if observation.Properties["state"].Type != "string" || observation.Properties["value"].Type != "string" || containsOpenAPIField(observation.Required, "value") {
			t.Fatalf("observation %s.%s must preserve optional exact value: %#v", field.group, field.name, observation)
		}
	}
	queries := map[string]bool{}
	for _, parameter := range operation.Parameters {
		queries[parameter.Name] = true
	}
	for _, name := range []string{"from", "to", "source_id", "entity_id", "limit", "cursor"} {
		if !queries[name] {
			t.Fatalf("missing query %s", name)
		}
	}
}

func TestManagementOpenAPIDepthResetRetainsCycleAndGeneralGuards(t *testing.T) {
	definitions := map[string]ast.Expr{"analysis.MessagingManagementRow": &ast.StructType{Fields: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("Next")}, Type: ast.NewIdent("MessagingManagementRow")}}}}}
	schema := openAPISchemaForASTTypeInPackage(ast.NewIdent("MessagingManagementRow"), definitions, map[string]bool{}, 100, "analysis")
	if schema.Properties["Next"].AdditionalProperties == nil {
		t.Fatal("bounded model reset bypassed the visiting cycle guard")
	}
	generic := openAPISchemaForASTTypeInPackage(ast.NewIdent("UserStructure"), map[string]ast.Expr{}, map[string]bool{}, 100, "analysis")
	if generic.AdditionalProperties == nil || len(generic.Properties) != 0 {
		t.Fatal("unapproved model bypassed the general depth guard")
	}
}

func TestManagementOpenAPICollectorRoutesRequireTheirOwnToken(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	catalogData, err := GenerateEndpointCatalogJSON("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var catalog endpointCatalog
	if err := json.Unmarshal(catalogData, &catalog); err != nil {
		t.Fatal(err)
	}

	bearer := doc.Components.SecuritySchemes["collectorBearerAuth"]
	apiKey := doc.Components.SecuritySchemes["collectorApiKeyAuth"]
	if bearer.Type != "http" || bearer.Scheme != "bearer" || !strings.Contains(bearer.Description, "Collector-scoped") || apiKey.Type != "apiKey" || apiKey.Name != "X-APEXLoad-API-Key" || apiKey.In != "header" {
		t.Fatalf("collector token schemes are not explicit: bearer=%+v apiKey=%+v", bearer, apiKey)
	}
	for _, route := range []struct{ method, path, handler string }{
		{"POST", "/api/v1/monitoring/collectors/{id}/heartbeat", "handleCollectorHeartbeat"},
		{"GET", "/api/v1/monitoring/collectors/{id}/jobs", "handlePollRunMonitorJobs"},
		{"POST", "/api/v1/monitoring/collectors/{id}/jobs/{job_id}/samples", "handleRunMonitorSamples"},
		{"POST", "/api/v1/monitoring/collectors/{id}/jobs/{job_id}/management-polls", "handleManagementPoll"},
	} {
		operation := doc.Paths[route.path][strings.ToLower(route.method)]
		if operation.Security == nil || len(*operation.Security) != 2 || !strings.Contains(operation.XRoles, "Collector token") {
			t.Fatalf("Swagger lost collector token contract: %s %s %+v", route.method, route.path, operation)
		}

		seen := map[string]bool{}
		for _, alternative := range *operation.Security {
			if len(alternative) != 1 {
				t.Fatalf("collector alternatives are not separate token channels: %#v", *operation.Security)
			}
			for scheme := range alternative {
				if scheme != "collectorBearerAuth" && scheme != "collectorApiKeyAuth" {
					t.Fatalf("collector route inherited other identity: %s", scheme)
				}
				seen[scheme] = true
			}
		}
		if len(seen) != 2 {
			t.Fatalf("missing collector token alternative: %#v", *operation.Security)
		}
		found := false
		for _, entry := range catalog.Routes {
			if entry.Method == route.method && entry.Path == route.path {
				found = true
				if entry.Public || !entry.Authenticated || !strings.Contains(entry.Access, "Collector token") {
					t.Fatalf("collector endpoint falsely public: %+v", entry)
				}
			}
		}
		if !found {
			t.Fatalf("missing collector catalog route: %s %s", route.method, route.path)
		}
		if !inHandlerAuthenticatedRoute(route.method, route.path, route.handler) || inHandlerAuthenticatedRoute(route.method, route.path, "unverifiedHandler") {
			t.Fatalf("in-handler auth recognition is not exact: %+v", route)
		}
	}
	if inHandlerAuthenticatedRoute("POST", "/api/v1/monitoring/collectors/{id}/jobs/{job_id}/management-polls/other", "handleManagementPoll") {
		t.Fatal("unrecognized collector route inherited an authentication claim")
	}
}
