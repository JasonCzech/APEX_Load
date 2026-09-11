package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
)

func TestDevWebImportKeepsExactCredentialOpaqueAndMigratable(t *testing.T) {
	server, records, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x4a}, 32), KeyID: "devweb-source-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{Records: records, Wrapper: wrapper}
	const canary = "devweb-api-protected-token-96f2"
	body, _ := json.Marshal(map[string]any{
		"name": "DevWeb protected", "project_id": projectID,
		"content": `load.action("Protected", async function() {
  await new load.WebRequest({url: "https://api.example.test/orders", method: "GET",
    headers: {"Authorization": "Bearer ` + canary + `"}}).send();
});`,
	})
	response := invokeProtectedCredentialImport(t, server, "/api/v1/scripts/import/devweb", body)
	if !bytes.Contains(response.Body, []byte(canary)) {
		t.Fatal("DevWeb response did not preserve the protected credential")
	}
	if len(response.Requirements) == 0 || response.SessionID == "" || !response.MigrationAllowed {
		t.Fatalf("DevWeb protected review = %#v session=%q migration=%t", response.Requirements, response.SessionID, response.MigrationAllowed)
	}
	for _, requirement := range response.Requirements {
		if !requirement.SourceValueAvailable || !requirement.DestinationValuePreserved {
			t.Fatalf("DevWeb exact credential requirement lost migration/preservation evidence: %#v", requirement)
		}
	}
}

func TestConverterRequirementsReachStandardizedTopLevelReview(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		body     func(t *testing.T) ([]byte, string)
	}{
		{
			name: "asyncapi event hubs", endpoint: "/api/v1/scripts/import/asyncapi",
			body: converterRequirementsJSONBody(map[string]any{
				"name": "Event Hubs",
				"content": `asyncapi: 3.0.0
info: {title: Event Hubs, version: 1.0.0}
servers:
  eh: {host: myns.servicebus.windows.net:9093, protocol: kafka-secure}
channels:
  events:
    address: telemetry
    messages: {event: {payload: {type: object}}}
operations:
  send: {action: send, channel: {$ref: '#/channels/events'}}`,
			}),
		},
		{
			name: "devweb", endpoint: "/api/v1/scripts/import/devweb",
			body: converterRequirementsJSONBody(map[string]any{
				"name": "DevWeb unavailable",
				"content": `load.action("Unavailable", async function() {
  await new load.WebRequest({url: "https://api.example.test/orders", method: "GET",
    headers: {"Authorization": "Bearer [rEdAcTeD]"}}).send();
});`,
			}),
		},
		{
			name: "k6", endpoint: "/api/v1/scripts/import/k6",
			body: converterRequirementsJSONBody(map[string]any{
				"name": "k6 unavailable",
				"content": `import http from 'k6/http';
export default function () { http.get('https://api.example.test/orders',
  { headers: { 'Authorization': 'Bearer [rEdAcTeD]' } }); }`,
			}),
		},
		{
			name: "wsdl", endpoint: "/api/v1/scripts/import/wsdl",
			body: converterRequirementsJSONBody(map[string]any{
				"name": "WSDL unavailable",
				"content": `<definitions name="LoginService" targetNamespace="urn:login"
  xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:tns="urn:login">
  <types><xsd:schema targetNamespace="urn:login" elementFormDefault="qualified">
    <xsd:element name="LoginRequest"><xsd:complexType><xsd:sequence>
      <xsd:element name="password" type="xsd:string" fixed="[rEdAcTeD]"/>
    </xsd:sequence></xsd:complexType></xsd:element>
  </xsd:schema></types>
  <message name="LoginInput"><part name="parameters" element="tns:LoginRequest"/></message>
  <portType name="LoginPortType"><operation name="Login"><input message="tns:LoginInput"/></operation></portType>
  <binding name="LoginBinding" type="tns:LoginPortType"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Login"><soap:operation soapAction="urn:Login"/><input><soap:body use="literal"/></input></operation>
  </binding>
  <service name="LoginService"><port name="LoginPort" binding="tns:LoginBinding"><soap:address location="https://soap.example.test/login"/></port></service>
</definitions>`,
			}),
		},
		{
			name: "jmeter", endpoint: "/api/v1/scripts/import/jmeter",
			body: jmeterUnavailableCredentialBody,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			body, contentType := test.body(t)
			request := httptest.NewRequest(http.MethodPost, test.endpoint, bytes.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			var payload struct {
				Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
				SessionID    string                     `json:"source_secret_session_id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Requirements) == 0 {
				t.Fatalf("standardized requirements are empty: %s", response.Body.String())
			}
			requirement := payload.Requirements[0]
			if requirement.ID == "" || requirement.Path == "" || requirement.Label == "" ||
				requirement.Context == "" || requirement.SuggestedName == "" ||
				!strings.Contains(requirement.Replacement, "{{secret.") {
				t.Fatalf("standardized requirement omitted converter context: %#v", requirement)
			}
			if requirement.PreservationUnavailableReason == "" || requirement.DestinationValuePreserved ||
				requirement.SourceValueAvailable || payload.SessionID != "" {
				t.Fatalf("unavailable source was advertised as migratable/preserved: %#v session=%q", requirement, payload.SessionID)
			}
		})
	}
}

func TestOpenAPIGeneratedCredentialsHaveUniqueApplicableReview(t *testing.T) {
	server := NewServer(ServerConfig{})
	content := `{
  "openapi":"3.0.3","info":{"title":"Generated auth","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "components":{"securitySchemes":{
    "bearerAuth":{"type":"http","scheme":"bearer"},
    "headerKey":{"type":"apiKey","in":"header","name":"X-API-Key"}
  }},
  "paths":{
    "/bearer":{"get":{"security":[{"bearerAuth":[]}],"responses":{"200":{}}}},
    "/key":{"get":{"security":[{"headerKey":[]}],"responses":{"200":{}}}}
  }
}`
	body, err := json.Marshal(map[string]any{"name": "Generated auth", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Script       map[string]any             `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Requirements) != 2 {
		t.Fatalf("generated OpenAPI requirements = %#v, want two without duplicates", payload.Requirements)
	}
	ids := map[string]bool{}
	bindings := make([]map[string]string, 0, len(payload.Requirements))
	for _, requirement := range payload.Requirements {
		if requirement.ID == "" || requirement.Path == "" || ids[requirement.ID] ||
			requirement.SourceValueAvailable || requirement.DestinationValuePreserved {
			t.Fatalf("invalid generated OpenAPI requirement: %#v", requirement)
		}
		ids[requirement.ID] = true
		bindings = append(bindings, map[string]string{
			"requirement_id": requirement.ID,
			"secret_name":    requirement.SuggestedName,
		})
	}
	applied := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script": payload.Script, "bindings": bindings,
	})
	if applied.Code != http.StatusOK {
		t.Fatalf("generated OpenAPI binding apply status = %d body=%s", applied.Code, applied.Body.String())
	}
}

func TestOpenAPIMixedCredentialReviewAppliesEverySelectionOnce(t *testing.T) {
	server := NewServer(ServerConfig{})
	const literal = "openapi-mixed-literal-password-7f31"
	content := `{
  "openapi":"3.0.3","info":{"title":"Mixed auth","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
  "paths":{"/login":{"post":{
    "security":[{"bearerAuth":[]}],
    "parameters":[{"name":"X-Existing-Token","in":"header","schema":{"type":"string","example":"{{secret.AUTHORED_TOKEN}}"}}],
    "requestBody":{"content":{"application/json":{"example":{"username":"alice","password":"` + literal + `"}}}},
    "responses":{"204":{}}
  }}}
}`
	body, err := json.Marshal(map[string]any{"name": "Mixed auth", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), literal) {
		t.Fatalf("mixed import did not preserve literal credential: %s", response.Body.String())
	}
	var payload struct {
		Script       map[string]any             `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Requirements) != 3 {
		t.Fatalf("mixed OpenAPI requirements = %#v, want literal, generated, and authored-reference cards", payload.Requirements)
	}
	seenIDs := make(map[string]struct{}, len(payload.Requirements))
	seenGenerated, seenAuthored, seenLiteral := false, false, false
	bindings := make([]map[string]string, 0, len(payload.Requirements))
	for index, requirement := range payload.Requirements {
		if requirement.ID == "" || requirement.Path == "" {
			t.Fatalf("incomplete mixed requirement: %#v", requirement)
		}
		if _, duplicate := seenIDs[requirement.ID]; duplicate {
			t.Fatalf("duplicate mixed requirement id %q: %#v", requirement.ID, payload.Requirements)
		}
		seenIDs[requirement.ID] = struct{}{}
		switch {
		case requirement.Replacement == "{{secret.OPENAPI_BEARERAUTH_BEARER_TOKEN}}":
			seenGenerated = true
		case requirement.Replacement == "{{secret.AUTHORED_TOKEN}}":
			seenAuthored = true
		case requirement.DestinationValuePreserved &&
			strings.Contains(requirement.Replacement, "{{secret.POST_LOGIN_PASSWORD}}"):
			seenLiteral = true
		}
		bindings = append(bindings, map[string]string{
			"requirement_id": requirement.ID,
			"secret_name":    fmt.Sprintf("MIXED_BOUND_%d", index+1),
		})
	}
	if !seenGenerated || !seenAuthored || !seenLiteral {
		t.Fatalf("mixed requirement kinds generated=%t authored=%t literal=%t: %#v", seenGenerated, seenAuthored, seenLiteral, payload.Requirements)
	}
	applied := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script": payload.Script, "bindings": bindings,
	})
	if applied.Code != http.StatusOK {
		t.Fatalf("mixed OpenAPI binding apply status = %d body=%s", applied.Code, applied.Body.String())
	}
	var resolved struct {
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(applied.Body.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved.Requirements) != 0 {
		t.Fatalf("mixed OpenAPI binding left requirements: %#v", resolved.Requirements)
	}
}

func TestAgentforceGeneratedClientSecretSelectionApplies(t *testing.T) {
	server := NewServer(ServerConfig{})
	content := `#%RAML 1.0
title: Agent API
version: v1
baseUri: https://api.salesforce.com/einstein/ai-agent/v1/agents/0XxAGENT
/sessions:
  post:
    displayName: Start Session
/sessions/{sessionId}/messages/stream:
  post:
    displayName: Stream Message
`
	body, err := json.Marshal(map[string]any{"name": "Agent API", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/raml?agentforce_transform=true",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Script       map[string]any             `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var clientSecret *SecretBindingRequirement
	for index := range payload.Requirements {
		if payload.Requirements[index].SuggestedName == "SALESFORCE_CLIENT_SECRET" {
			clientSecret = &payload.Requirements[index]
			break
		}
	}
	if clientSecret == nil {
		t.Fatalf("Agentforce response omitted client-secret guidance: %#v", payload.Requirements)
	}
	applied := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script": payload.Script,
		"bindings": []map[string]string{{
			"requirement_id": clientSecret.ID,
			"secret_name":    "SALESFORCE_CONNECTED_APP_SECRET",
		}},
	})
	if applied.Code != http.StatusOK {
		t.Fatalf("Agentforce client-secret apply status = %d body=%s", applied.Code, applied.Body.String())
	}
}

func converterRequirementsJSONBody(payload map[string]any) func(*testing.T) ([]byte, string) {
	return func(t *testing.T) ([]byte, string) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return body, "application/json"
	}
}

func jmeterUnavailableCredentialBody(t *testing.T) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "unavailable.jmx")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2"><hashTree><TestPlan testname="Plan"/><hashTree>
<ThreadGroup testname="Users"/><hashTree>
<HeaderManager testname="Headers"><collectionProp name="HeaderManager.headers">
  <elementProp name="Authorization" elementType="Header"><stringProp name="Header.name">Authorization</stringProp><stringProp name="Header.value">Bearer [rEdAcTeD]</stringProp></elementProp>
</collectionProp></HeaderManager><hashTree/>
<HTTPSamplerProxy testname="Orders"><stringProp name="HTTPSampler.domain">api.example.test</stringProp><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.path">/orders</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}
