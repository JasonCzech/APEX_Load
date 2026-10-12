package api

import (
	"net/http"
	"reflect"
	"slices"
	"testing"
)

func TestOpenAPIProjectLiveTableWriteRequests(t *testing.T) {
	routes := []discoveredRoute{
		{Method: http.MethodPost, Path: "/api/v1/projects/{id}/live-tables", Handler: "handleCreateProjectLiveTable"},
		{Method: http.MethodPost, Path: "/api/v1/projects/{id}/live-tables/{tableID}/mutations", Handler: "handleMutateProjectLiveTable"},
		{Method: http.MethodPost, Path: "/api/v1/projects/{id}/live-tables/{tableID}/lifecycle", Handler: "handleProjectLiveTableLifecycle"},
	}
	contracts, err := inferRouteContracts("server.go", routes)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		t.Run(route.Handler, func(t *testing.T) {
			body := contracts[route.Method+" "+route.Path].RequestBody
			if body == nil || !body.Required || len(body.Content) != 1 {
				t.Fatalf("expected one required JSON body, got %#v", body)
			}
			media, ok := body.Content["application/json"]
			if !ok {
				t.Fatalf("bounded JSON decoder was mistaken for a raw upload: %#v", body.Content)
			}
			schema := media.Schema
			assertProjectTableClosedSchema(t, schema)
			switch route.Handler {
			case "handleCreateProjectLiveTable":
				assertProjectTableSchemaFields(t, schema, "name", "profile", "columns", "limits", "seed_pool_id")
				assertProjectTableRequiredFields(t, schema, "columns", "name")
				if schema.Properties["name"].Type != "string" || schema.Properties["seed_pool_id"].Type != "string" || !slices.Equal(schema.Properties["profile"].Enum, []string{"native_queue", "vts_columns", ""}) {
					t.Fatal("create identity or defaultable profile contract lost", schema)
				}
				assertProjectTableStringArray(t, schema.Properties["columns"])
				limits := schema.Properties["limits"]
				assertProjectTableClosedSchema(t, limits)
				assertProjectTableSchemaFields(t, limits, "max_rows", "max_bytes", "max_result_bytes", "max_receipts", "retry_window_ms", "lease_ttl_ms", "max_lease_lifetime_ms", "max_delivery_attempts")
				assertProjectTableRequiredFields(t, limits)
				for name, field := range limits.Properties {
					if field.Type != "integer" {
						t.Fatalf("limit %s is not numeric: %#v", name, field)
					}
				}
			case "handleMutateProjectLiveTable":
				assertProjectTableSchemaFields(t, schema, "idempotency_key", "issued_at", "expected_instance_id", "expected_revision", "operation", "record_id", "position", "expected_version", "values", "columns", "delta")
				assertProjectTableRequiredFields(t, schema, "idempotency_key", "issued_at", "expected_instance_id", "expected_revision", "operation")
				if schema.Properties["issued_at"].Format != "date-time" || schema.Properties["idempotency_key"].Format != "uuid" || !slices.Equal(schema.Properties["operation"].Enum, []string{"append", "update", "clear", "increment"}) {
					t.Fatal("command identity or operation contract lost", schema)
				}
				for _, field := range []string{"expected_revision", "expected_version", "delta"} {
					if schema.Properties[field].Type != "string" {
						t.Fatalf("%s must preserve decimal strings, not floating point numbers", field)
					}
				}
				values := schema.Properties["values"]
				cell, ok := values.AdditionalProperties.(*oasSchema)
				if values.Type != "object" || !ok || cell.Type != "string" || cell.Nullable {
					t.Fatalf("literal string cells became opaque or nullable: %#v", values)
				}
				assertProjectTableStringArray(t, schema.Properties["columns"])
				if schema.Properties["position"].Type != "integer" {
					t.Fatal("shared-column position is not an integer")
				}
				for name, field := range schema.Properties {
					if field.Nullable {
						t.Fatalf("strict decoder does not admit null for %s", name)
					}
				}
			case "handleProjectLiveTableLifecycle":
				fields := []string{"action", "expected_config_revision", "expected_instance_id", "expected_revision", "reason"}
				assertProjectTableSchemaFields(t, schema, fields...)
				assertProjectTableRequiredFields(t, schema, fields...)
				if !slices.Equal(schema.Properties["action"].Enum, []string{"quiesce", "resume", "reset", "archive"}) {
					t.Fatal("lifecycle action choices lost", schema)
				}
				for _, name := range fields {
					if schema.Properties[name].Type != "string" {
						t.Fatalf("lifecycle identity/reason %s must remain a string", name)
					}
				}
			}
		})
	}
}

func assertProjectTableClosedSchema(t *testing.T, schema oasSchema) {
	t.Helper()
	additional, ok := schema.AdditionalProperties.(*bool)
	if schema.Type != "object" || len(schema.Properties) == 0 || !ok || *additional || schema.Nullable {
		t.Fatalf("expected a closed typed object, got %#v", schema)
	}
}

func assertProjectTableSchemaFields(t *testing.T, schema oasSchema, expected ...string) {
	t.Helper()
	actual := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		actual = append(actual, name)
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("request fields = %v, want %v", actual, expected)
	}
}

func assertProjectTableRequiredFields(t *testing.T, schema oasSchema, expected ...string) {
	t.Helper()
	actual := slices.Clone(schema.Required)
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatalf("required fields = %v, want %v", actual, expected)
	}
}

func assertProjectTableStringArray(t *testing.T, schema oasSchema) {
	t.Helper()
	if schema.Type != "array" || schema.Items == nil || schema.Items.Type != "string" || schema.Items.Nullable {
		t.Fatalf("expected string array, got %#v", schema)
	}
}
