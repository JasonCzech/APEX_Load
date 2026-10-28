package api

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIReferenceDraftExampleValidatesThroughHandler(t *testing.T) {
	contract := inferredRouteContract{RequestBody: &oasRequestBody{Content: map[string]oasMediaType{"application/json": {}}}}
	applyAPIReferenceExamples(discoveredRoute{Method: http.MethodPost, Path: "/api/v1/scripts/validate"}, &contract, nil)
	body, err := json.Marshal(contract.RequestBody.Content["application/json"].Example)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validate", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var result scriptValidationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("published example does not validate: %s", response.Body.String())
	}
}

func TestAPIReferenceListContractMatchesHandlerEnvelope(t *testing.T) {
	contract := inferredRouteContract{Responses: map[string]oasResponse{"200": {}}, QueryParameters: []oasParameter{{Name: "q"}, {Name: "limit"}, {Name: "offset"}}}
	applyAPIReferenceExamples(discoveredRoute{Method: http.MethodGet, Path: "/api/v1/scripts"}, &contract, map[string]ast.Expr{})
	media := contract.Responses["200"].Content["application/json"]
	if media.Schema.Properties["scripts"].Type != "array" || !media.Schema.Properties["scripts"].Nullable {
		t.Fatalf("scripts contract: %+v", media.Schema)
	}
	for _, parameter := range contract.QueryParameters {
		if parameter.Description == "" {
			t.Fatalf("missing description: %s", parameter.Name)
		}
	}
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	for _, query := range []string{"", "?q=no-match&limit=2&offset=1"} {
		t.Run(query, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/scripts"+query, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			for _, name := range media.Schema.Required {
				if _, ok := body[name]; !ok {
					t.Fatalf("missing required %s", name)
				}
			}
			for name := range body {
				if _, ok := media.Schema.Properties[name]; !ok {
					t.Fatalf("undocumented response field %s", name)
				}
			}
			if query != "" && (string(body["limit"]) != "2" || string(body["offset"]) != "1") {
				t.Fatalf("pagination: %s", response.Body.String())
			}
		})
	}
}

func TestAPIReferenceExamplesPreserveSpecializedErrorContracts(t *testing.T) {
	for _, schema := range []oasSchema{{Ref: "#/components/schemas/ErrorResponse"}, {Type: "object"}} {
		contract := inferredRouteContract{Responses: map[string]oasResponse{"401": {Content: map[string]oasMediaType{"application/json": {Schema: schema}}}}}
		applyAPIReferenceExamples(discoveredRoute{}, &contract, nil)
		example := contract.Responses["401"].Content["application/json"].Example
		if (example != nil) != (schema.Ref != "") {
			t.Fatalf("specialized error example replaced: %+v", example)
		}
	}
	applyAPIReferenceExamples(discoveredRoute{}, nil, nil)
}
