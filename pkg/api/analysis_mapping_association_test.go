package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnalysisCapabilitiesAdvertiseMappingAssociationContract(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).handleAnalysisCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/v1/analysis/capabilities?project_id=project", nil))
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response["mapping_association_version"] != float64(1) {
		t.Fatal(w.Code, response)
	}
}

func TestAnalysisNegotiatedAssociationGeneratedContract(t *testing.T) {
	route := discoveredRoute{Method: http.MethodPatch, Path: "/api/v1/analysis/datasets/{id}/association", Handler: "handleAnalysisMappingAssociation"}
	contracts, err := inferRouteContracts("server.go", []discoveredRoute{route})
	if err != nil {
		t.Fatal(err)
	}
	schema := contracts[route.Method+" "+route.Path].Responses["200"].Content["application/json"].Schema
	if schema.Properties["contract_version"].Type != "integer" || schema.Properties["import"].Type != "object" {
		t.Fatal("negotiated response contract missing", schema)
	}
}
