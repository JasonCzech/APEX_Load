package api

import (
	"net/http"
	"reflect"
	"testing"
)

func TestBrowserResourceOpenAPIExactTypedPageAndApprovedQueryContract(t *testing.T) {
	routes, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := inferRouteContracts("server.go", routes)
	if err != nil {
		t.Fatal(err)
	}
	contract, found := contracts["GET /api/v1/tests/{id}/archive/browser-resources"]
	if !found {
		t.Fatal("resource route was not discovered")
	}
	schema := contract.Responses["200"].Content["application/json"].Schema
	items := schema.Properties["items"]
	if schema.Type != "object" || items.Type != "array" || items.Nullable || items.Items == nil || !containsOpenAPIField(schema.Required, "items") || !containsOpenAPIField(schema.Required, "source_integrity") {
		t.Fatal("page shape/presence became opaque or nullable")
	}
	population := schema.Properties["population"]
	if !population.Properties["retained_entry_observations"].Nullable || !containsOpenAPIField(population.Required, "retained_entry_observations") {
		t.Fatal("unavailable population became observed zero")
	}
	native := items.Items.Properties["browser_evidence"]
	if native.Properties["bytes"].Properties["response_encoded_body"].Type != "integer" || !native.Properties["service_worker"].Nullable || native.Properties["service_worker"].Type != "boolean" || native.Properties["phases"].Items == nil {
		t.Fatal("native0/false resource contract became opaque")
	}
	names := map[string]bool{}
	for _, parameter := range contract.QueryParameters {
		if names[parameter.Name] {
			t.Fatal("duplicate query parameter")
		}
		names[parameter.Name] = true
		if parameter.Name == "sort_by" && !reflect.DeepEqual(parameter.Schema.Enum, []string{"capture_order", "duration_ms", "native_start_ms", "response_encoded_body"}) {
			t.Fatal("sort enum drift")
		}
	}
	if len(names) != 20 || !names["observation_action_id"] || !names["initiating_action_id"] || names["sql"] {
		t.Fatal("approved exact filters drift")
	}
	for _, status := range []string{"400", "403", "404", "408", "409", "413", "422", "503"} {
		if _, ok := contract.Responses[status]; !ok {
			t.Fatal("bounded refusal status missing")
		}
	}
	route := newBrowserResourceReadAPIRoute()
	if route.Method != http.MethodGet || route.RequestTimeout != browserResourceReadTimeout {
		t.Fatal("published resource route lost bounded read admission")
	}
}
