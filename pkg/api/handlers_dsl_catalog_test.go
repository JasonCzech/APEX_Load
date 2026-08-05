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
			Type             string   `json:"type"`
			Protocols        []string `json:"protocols"`
			Summary          string   `json:"summary"`
			ExecutionPosture string   `json:"execution_posture"`
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
	postures := make(map[string]string, len(result.Actions))
	for _, action := range result.Actions {
		if action.Summary == "" {
			t.Errorf("%s: missing summary in API payload", action.Type)
		}
		types[action.Type] = true
		postures[action.Type] = action.ExecutionPosture
	}
	for _, expected := range []string{"GET", "http_sequence", "browser_click", "ws_send", "grpc_unary", "db_query", "dns_query", "jms_publish", "kafka_produce", "control_if", "rendezvous"} {
		if !types[expected] {
			t.Errorf("catalog payload missing action type %q", expected)
		}
	}
	if postures["sapgui_connect"] != "external_runner_required" {
		t.Errorf("sapgui_connect execution posture = %q", postures["sapgui_connect"])
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

func TestHandleConfigDefaultsReturnsEngineDefaults(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dsl/config-defaults", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Config struct {
			HTTP struct {
				ConnectTimeoutMs int `json:"connect_timeout_ms"`
				RequestTimeoutMs int `json:"request_timeout_ms"`
				TLS              struct {
					MinVersion string `json:"min_version"`
				} `json:"tls"`
			} `json:"http"`
			Browser struct {
				Engine string `json:"engine"`
			} `json:"browser"`
			Pacing struct {
				Type string `json:"type"`
			} `json:"pacing"`
			Log struct {
				Level string `json:"level"`
			} `json:"log"`
		} `json:"config"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Config.HTTP.ConnectTimeoutMs != 30000 || result.Config.HTTP.RequestTimeoutMs != 60000 {
		t.Errorf("http timeout defaults not served: %+v", result.Config.HTTP)
	}
	if result.Config.HTTP.TLS.MinVersion != "1.2" {
		t.Errorf("tls min_version default not served: %q", result.Config.HTTP.TLS.MinVersion)
	}
	if result.Config.Browser.Engine != "chromium" {
		t.Errorf("browser engine default not served: %q", result.Config.Browser.Engine)
	}
	if result.Config.Pacing.Type != "none" {
		t.Errorf("pacing default not served: %q", result.Config.Pacing.Type)
	}
	if result.Config.Log.Level != "standard" {
		t.Errorf("log level default not served: %q", result.Config.Log.Level)
	}
}
