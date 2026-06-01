package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleWebAPIExecutionSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/web-api/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID              string `json:"id"`
			Status          string `json:"status"`
			ExecutionStatus string `json:"execution_status"`
		} `json:"support_matrix"`
		BenchmarkEnvelopes []struct {
			Protocol          string `json:"protocol"`
			RecommendedVUsers int    `json:"recommended_vusers_per_generator"`
		} `json:"benchmark_envelopes"`
		Diagnostics []struct {
			Protocol string   `json:"protocol"`
			Signals  []string `json:"signals"`
		} `json:"diagnostics"`
		SampleProjects []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"sample_projects"`
		StatusCounts map[string]int `json:"status_counts"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	for _, id := range []string{"http-web", "openapi-authoring", "postman-authoring", "soap-wsdl", "java-over-http", "websocket", "grpc", "jms-solace", "browser-canaries", "devweb-import"} {
		if !webAPIMatrixContains(result.SupportMatrix, id) {
			t.Fatalf("support matrix missing %s", id)
		}
	}
	for _, protocol := range []string{"http", "soap", "java_http", "websocket", "grpc", "jms-solace", "browser"} {
		if !webAPIEnvelopeContains(result.BenchmarkEnvelopes, protocol) {
			t.Fatalf("benchmark envelopes missing %s", protocol)
		}
		if !webAPIDiagnosticsContains(result.Diagnostics, protocol) {
			t.Fatalf("diagnostics missing %s", protocol)
		}
	}
	if len(result.SampleProjects) < 6 {
		t.Fatalf("sample projects = %d, want at least 6", len(result.SampleProjects))
	}
	if result.StatusCounts["supported"] == 0 {
		t.Fatal("expected supported status count")
	}
}

func TestHandleDevWebSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/devweb/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID              string `json:"id"`
			ExecutionStatus string `json:"execution_status"`
		} `json:"support_matrix"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !devWebMatrixContains(result.SupportMatrix, "devweb-grpc-unary") {
		t.Fatal("expected gRPC unary DevWeb support entry")
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected support matrix notes")
	}
}

func TestHandleImportOpenAPI(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Orders API",
		"project_id": "proj-api",
		"content": `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders/{id}": {
      "get": {
        "operationId": "getOrder",
        "parameters": [{"name": "id", "in": "path", "schema": {"type": "string"}}],
        "responses": {"200": {}}
      }
    }
  }
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Actions   []struct {
				Target string `json:"target"`
			} `json:"actions"`
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
		} `json:"script"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		SourceReview map[string]any `json:"source_review"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-api" || result.Script.Protocol != "http" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || !strings.Contains(result.Script.Actions[0].Target, "{{data.openapi.id}}") {
		t.Fatalf("unexpected actions: %+v", result.Script.Actions)
	}
	if len(result.SourceReview) == 0 {
		t.Fatal("expected root source_review in response")
	}
	if result.Script.Metadata.Import.Preserved["source_review"] == nil {
		t.Fatalf("script metadata missing preserved source_review: %#v", result.Script.Metadata.Import.Preserved)
	}
	assertReviewSection(t, result.ReviewSections, "openapi_operations", "info", "getOrder")
}

func TestHandleImportOpenAPIRawJSONDocument(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi?project_id=proj-api&base_url=https://override.example.test", strings.NewReader(`{
  "openapi": "3.0.3",
  "info": {"title": "Raw Orders", "version": "1.0"},
  "paths": {
    "/orders": {
      "get": {
        "operationId": "listOrders",
        "responses": {"200": {}}
      }
    }
  }
}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Config    struct {
				BaseURL string `json:"base_url"`
			} `json:"config"`
			Actions []struct {
				Name string `json:"name"`
			} `json:"actions"`
		} `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-api" || result.Script.Config.BaseURL != "https://override.example.test" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Name != "listOrders" {
		t.Fatalf("actions = %+v, want listOrders", result.Script.Actions)
	}
}

func TestHandleImportOpenAPISurfacesUnsupportedElements(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name": "External Ref API",
		"content": `{
  "openapi": "3.0.3",
  "info": {"title": "External Ref", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {"$ref": "external.yaml#/components/schemas/Order"}
            }
          }
        },
        "responses": {"201": {}}
      }
    }
  }
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		UnsupportedElements []string `json:"unsupported_elements"`
		ReviewSections      []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !stringSliceContains(result.UnsupportedElements, "schema reference external.yaml") {
		t.Fatalf("unsupported elements = %#v, want unresolved ref diagnostic", result.UnsupportedElements)
	}
	assertReviewSection(t, result.ReviewSections, "import_diagnostics_warning", "warning", "OpenAPI schema $ref at POST /orders [warning]: schema reference external.yaml#/components/schemas/Order could not be resolved; external refs are not inlined")
}

func TestHandleImportPostman(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Orders Postman",
		"project_id": "proj-postman",
		"content": `{
  "info": {"name": "Orders Collection", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable": [{"key": "baseUrl", "value": "https://api.example.test"}, {"key": "order_id", "value": "42"}],
  "item": [{
    "name": "Orders",
    "item": [{
      "name": "Get Order",
      "request": {
        "method": "GET",
        "header": [{"key": "X-Request-ID", "value": "{{request_id}}"}],
        "url": "{{baseUrl}}/orders/{{order_id}}"
      }
    }]
  }]
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID  string `json:"project_id"`
			Protocol   string `json:"protocol"`
			Parameters []struct {
				Name string `json:"name"`
			} `json:"parameters"`
			Config struct {
				BaseURL string `json:"base_url"`
			} `json:"config"`
			Actions []struct {
				Name    string            `json:"name"`
				Target  string            `json:"target"`
				Headers map[string]string `json:"headers"`
			} `json:"actions"`
		} `json:"script"`
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
		} `json:"stats"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-postman" || result.Script.Protocol != "http" || result.Script.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Name != "Orders_Get_Order" || result.Script.Actions[0].Target != "/orders/{{var.order_id}}" {
		t.Fatalf("actions = %+v", result.Script.Actions)
	}
	if result.Script.Actions[0].Headers["X-Request-ID"] != "{{var.request_id}}" {
		t.Fatalf("headers = %+v", result.Script.Actions[0].Headers)
	}
	if len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Name != "order_id" {
		t.Fatalf("parameters = %+v", result.Script.Parameters)
	}
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	assertReviewSection(t, result.ReviewSections, "postman_requests", "info", "GET /orders/{{var.order_id}}")
	assertReviewSection(t, result.ReviewSections, "postman_variables", "info", "order_id")
}

func TestHandleImportWSDL(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Account SOAP",
		"project_id": "proj-soap",
		"content": `<definitions name="AccountService" targetNamespace="urn:accounts" xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/">
  <portType name="AccountPortType"><operation name="GetAccount"/></portType>
  <binding name="AccountBinding" type="tns:AccountPortType"><operation name="GetAccount"><soap:operation soapAction="urn:GetAccount"/></operation></binding>
  <service name="AccountService"><port name="AccountPort"><soap:address location="https://soap.example.test/account"/></port></service>
</definitions>`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/wsdl", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Actions   []struct {
				Body string `json:"body"`
			} `json:"actions"`
		} `json:"script"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-soap" || result.Script.Protocol != "soap" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || !strings.Contains(result.Script.Actions[0].Body, "GetAccount") {
		t.Fatalf("unexpected actions: %+v", result.Script.Actions)
	}
	assertReviewSection(t, result.ReviewSections, "import_diagnostics_warning", "warning", "WSDL input message at operation GetAccount [warning]: input message/part could not be resolved; generated a placeholder SOAP body")
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func webAPIMatrixContains(entries []struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	ExecutionStatus string `json:"execution_status"`
}, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Status != "" && entry.ExecutionStatus != "" {
			return true
		}
	}
	return false
}

func webAPIEnvelopeContains(entries []struct {
	Protocol          string `json:"protocol"`
	RecommendedVUsers int    `json:"recommended_vusers_per_generator"`
}, protocol string) bool {
	for _, entry := range entries {
		if entry.Protocol == protocol && entry.RecommendedVUsers > 0 {
			return true
		}
	}
	return false
}

func webAPIDiagnosticsContains(entries []struct {
	Protocol string   `json:"protocol"`
	Signals  []string `json:"signals"`
}, protocol string) bool {
	for _, entry := range entries {
		if entry.Protocol == protocol && len(entry.Signals) > 0 {
			return true
		}
	}
	return false
}

func devWebMatrixContains(entries []struct {
	ID              string `json:"id"`
	ExecutionStatus string `json:"execution_status"`
}, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.ExecutionStatus != "" {
			return true
		}
	}
	return false
}
