package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleListDSLActionsReturnsFullCatalog(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dsl/actions", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Protocols    []string `json:"protocols"`
		CommonFields []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"common_fields"`
		Actions []struct {
			Type      string   `json:"type"`
			Protocols []string `json:"protocols"`
			Summary   string   `json:"summary"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Protocols) == 0 {
		t.Error("protocols list is empty")
	}
	if len(result.CommonFields) == 0 {
		t.Error("common_fields list is empty")
	}
	types := make(map[string]bool, len(result.Actions))
	for _, action := range result.Actions {
		if action.Summary == "" {
			t.Errorf("%s: missing summary in API payload", action.Type)
		}
		types[action.Type] = true
	}
	for _, expected := range []string{"GET", "http_sequence", "browser_click", "ws_send", "grpc_unary", "db_query", "dns_query", "jms_publish", "kafka_produce", "control_if", "rendezvous"} {
		if !types[expected] {
			t.Errorf("catalog payload missing action type %q", expected)
		}
	}
}

func TestHandleListDSLActionsFiltersByProtocol(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dsl/actions?protocol=websocket", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Protocol string `json:"protocol"`
		Actions  []struct {
			Type string `json:"type"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Protocol != "websocket" {
		t.Errorf("protocol = %q, want websocket", result.Protocol)
	}
	types := make(map[string]bool, len(result.Actions))
	for _, action := range result.Actions {
		types[action.Type] = true
	}
	if !types["ws_send"] || !types["ws_connect"] {
		t.Error("websocket filter missing ws_send/ws_connect")
	}
	if !types["control_if"] {
		t.Error("websocket filter should include engine actions")
	}
	if types["browser_click"] || types["kafka_produce"] {
		t.Error("websocket filter leaked other protocols' actions")
	}
}
