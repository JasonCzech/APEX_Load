package wsdl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertWSDLProjectsSensitiveSchemaLiteralsAndPreservesEnvelopeBytes(t *testing.T) {
	const (
		passwordCanary = "credential-canary-password-7d40"
		tokenCanary    = "credential-canary-token-8c51"
	)
	result, err := Convert(ImportRequest{
		RootFile: "login.wsdl",
		Content: `<definitions name="LoginService" targetNamespace="urn:login"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:login">
  <types><xsd:schema targetNamespace="urn:login">
    <xsd:element name="LoginRequest">
      <xsd:complexType><xsd:sequence>
        <xsd:element name="note" type="xsd:string" fixed="safe&amp;stable"/>
        <xsd:element name="password" type="xsd:string" fixed="` + passwordCanary + `"/>
        <xsd:element name="accessToken" type="xsd:string" default="` + tokenCanary + `"/>
        <xsd:element name="count" type="xsd:int" fixed="7"/>
      </xsd:sequence></xsd:complexType>
    </xsd:element>
  </xsd:schema></types>
  <message name="LoginInput"><part name="parameters" element="tns:LoginRequest"/></message>
  <portType name="LoginPortType">
    <operation name="Login"><input message="tns:LoginInput"/></operation>
  </portType>
  <binding name="LoginBinding" type="tns:LoginPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Login">
      <soap:operation soapAction="urn:Login"/>
      <input><soap:body use="literal"/></input>
    </operation>
  </binding>
  <service name="LoginService"><port name="LoginPort" binding="tns:LoginBinding">
    <soap:address location="https://soap.example.test/login"/>
  </port></service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("validation issues = %#v", issues)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	wantBody := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:tns="urn:login">
  <soapenv:Header/>
  <soapenv:Body>
    <tns:LoginRequest>
      <tns:note>safe&amp;stable</tns:note>
      <tns:password>{{secret.WSDL_PASSWORD}}</tns:password>
      <tns:accessToken>{{secret.WSDL_ACCESSTOKEN}}</tns:accessToken>
      <tns:count>7</tns:count>
    </tns:LoginRequest>
  </soapenv:Body>
</soapenv:Envelope>`
	if action.Body != wantBody {
		t.Fatalf("SOAP body bytes changed unexpectedly:\n got: %q\nwant: %q", action.Body, wantBody)
	}
	if len(action.Headers) != 0 || len(action.HeaderList) != 0 {
		t.Fatalf("SOAP runtime-owned HTTP headers changed: map=%#v list=%#v", action.Headers, action.HeaderList)
	}
	if len(result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v, want 2", result.SecretRequirements)
	}
	for _, requirement := range result.SecretRequirements {
		if requirement.Value != "" || requirement.SourceValueAvailable {
			t.Fatalf("WSDL result retained unprotected migration material: %#v", requirement)
		}
		if requirement.ID == "" || requirement.Replacement == "" {
			t.Fatalf("incomplete credential requirement: %#v", requirement)
		}
	}
	if result.Readiness.State != importreview.Blocked ||
		!reflect.DeepEqual(result.Readiness.BlockingCodes, []string{"credential_bindings_required"}) {
		t.Fatalf("readiness = %#v", result.Readiness)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{passwordCanary, tokenCanary} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("serialized conversion leaked canary %q: %s", canary, encoded)
		}
		if result.SourceReview != nil {
			for _, file := range result.SourceReview.SourceFiles {
				if strings.Contains(file.Content, canary) {
					t.Fatalf("source review leaked canary %q: %s", canary, file.Content)
				}
			}
		}
	}
}

func TestConvertWSDLScrubsCredentialInUnusedSchemaBranch(t *testing.T) {
	const unusedCanary = "unused-credential-canary-1c95f761a0"
	result, err := Convert(ImportRequest{
		Content: `<definitions name="ReviewSurface" targetNamespace="urn:review"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:review">
  <types><xsd:schema targetNamespace="urn:review">
    <xsd:element name="password" type="xsd:string" fixed="` + unusedCanary + `"/>
  </xsd:schema></types>
  <message name="PingInput"/>
  <portType name="PingPortType"><operation name="Ping"><input message="tns:PingInput"/></operation></portType>
  <binding name="PingBinding" type="tns:PingPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Ping"><soap:operation soapAction="urn:Ping"/><input><soap:body use="literal"/></input></operation>
  </binding>
  <service name="PingService"><port name="PingPort" binding="tns:PingBinding">
    <soap:address location="https://soap.example.test/ping"/>
  </port></service>
</definitions>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), unusedCanary) {
		t.Fatalf("unused schema credential leaked through source review: %s", encoded)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		!strings.Contains(result.SourceReview.SourceFiles[0].Content, credential.Redacted) {
		t.Fatalf("unused schema credential was not redacted: %#v", result.SourceReview)
	}
}

func TestConvertWSDLProjectsEndpointCredentialsAcrossAllPublicSurfaces(t *testing.T) {
	const source = `<definitions name="EndpointCredentials" targetNamespace="urn:endpoint"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:endpoint">
  <message name="PingInput"/>
  <portType name="PingPortType">
    <operation name="Ping"><input message="tns:PingInput"/></operation>
  </portType>
  <binding name="PingBinding" type="tns:PingPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Ping">
      <soap:operation soapAction="urn:Ping"/>
      <input><soap:body use="literal"/></input>
    </operation>
  </binding>
  <service name="PingService"><port name="PingPort" binding="tns:PingBinding">
    <soap:address location="https://alice:wsdl&#45;pass@api.example.test/root?api_key=wsdl%2Dquery"/>
  </port></service>
</definitions>`
	result, err := Convert(ImportRequest{RootFile: "endpoint.wsdl", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v, want userinfo and query bindings", result.SecretRequirements)
	}
	for _, requirement := range result.SecretRequirements {
		if requirement.Value != "" || requirement.SourceValueAvailable {
			t.Fatalf("public requirement retained migration material: %#v", requirement)
		}
	}
	if strings.Contains(result.Script.Config.BaseURL, "wsdl-pass") ||
		strings.Contains(result.Script.Config.BaseURL, "wsdl%2Dquery") ||
		!strings.Contains(result.Script.Config.BaseURL, "{{secret.") {
		t.Fatalf("base URL was not projected safely: %q", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Target != result.Script.Config.BaseURL ||
		result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("SOAP target did not reuse the gated projected base URL: %#v", result.Script.Actions)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{
		"wsdl&#45;pass",
		"wsdl-pass",
		"wsdl%2Dquery",
		"wsdl-query",
	} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("serialized conversion disclosed %q: %s", canary, encoded)
		}
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		!strings.Contains(result.SourceReview.SourceFiles[0].Content, "WSDL SOURCE OMITTED") {
		t.Fatalf("encoded endpoint source was not fail-closed: %#v", result.SourceReview)
	}
}

func TestProjectWSDLCredentialsPreservesNonsensitiveXMLAndAttributeSyntax(t *testing.T) {
	const (
		textCanary = "credential-canary-text-13af"
		attrCanary = "credential-canary-attr-24b0"
	)
	body := []byte(`<env:Envelope xmlns:env="urn:env"><env:Header/><env:Body><auth:token xmlns:auth="urn:auth" apiKey='` + attrCanary + `'>  ` + textCanary + `  </auth:token><note a="1">&amp; exact</note></env:Body></env:Envelope>`)
	projected, secrets, findings, err := finalizeWSDLXMLBody(body, wsdlCredentialProjectionOptions{
		RequestKey: "login",
		SourcePath: "login.wsdl",
		Pointer:    "operation/Login/body",
		Registry:   credentialpolicy.NewNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 || len(secrets) != 2 {
		t.Fatalf("projection sidecars: findings=%#v secrets=%#v", findings, secrets)
	}
	got := string(projected)
	if strings.Contains(got, textCanary) || strings.Contains(got, attrCanary) {
		t.Fatalf("projected body retained canary: %s", got)
	}
	if !strings.Contains(got, `apiKey='{{secret.WSDL_APIKEY}}'`) {
		t.Fatalf("single-quoted attribute syntax was not preserved: %s", got)
	}
	if !strings.Contains(got, `>  {{secret.WSDL_TOKEN}}  </auth:token>`) {
		t.Fatalf("element whitespace was not preserved: %s", got)
	}
	if !strings.Contains(got, `<note a="1">&amp; exact</note>`) {
		t.Fatalf("nonsensitive XML bytes changed: %s", got)
	}
}

func TestProjectWSDLCredentialsRedactsAndBlocksMixedSensitiveContent(t *testing.T) {
	const (
		canary      = "credential-canary-mixed-35c1"
		childCanary = "credential-canary-child-46d2"
	)
	body := []byte(`<Envelope><credentials>
  <username>alice</username>
  <password>` + canary + `<accessToken>` + childCanary + `</accessToken>suffix</password>
</credentials><note>exact</note></Envelope>`)
	projected, secrets, findings, err := finalizeWSDLXMLBody(body, wsdlCredentialProjectionOptions{
		RequestKey: "login",
		SourcePath: "login.wsdl",
		Pointer:    "operation/Login/body",
		Registry:   credentialpolicy.NewNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 {
		t.Fatalf("unsafe mixed content should not create a migratable requirement: %#v", secrets)
	}
	if len(findings) != 1 ||
		findings[0].Code != "credential_xml_sensitive_mixed_content_review_required" ||
		findings[0].Gate != importreview.GateExecution {
		t.Fatalf("findings = %#v", findings)
	}
	got := string(projected)
	if strings.Contains(got, canary) || strings.Contains(got, childCanary) ||
		!strings.Contains(got, `<credentials>`+credential.Redacted+`</credentials>`) {
		t.Fatalf("mixed sensitive content was not fail-safe redacted: %s", got)
	}
	if !strings.Contains(got, `<credentials>`) || !strings.Contains(got, `<note>exact</note>`) {
		t.Fatalf("credential container or nonsensitive sibling changed: %s", got)
	}
}

func TestConvertWSDLRPCEncodedIsValidBlockedDraft(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<definitions name="Legacy" targetNamespace="urn:legacy"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:legacy">
  <message name="LegacyInput"><part name="value" type="xsd:string"/></message>
  <portType name="LegacyPortType">
    <operation name="LegacyCall"><input message="tns:LegacyInput"/></operation>
  </portType>
  <binding name="LegacyBinding" type="tns:LegacyPortType">
    <soap:binding style="rpc" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="LegacyCall">
      <soap:operation soapAction="urn:LegacyCall" style="rpc"/>
      <input><soap:body use="encoded" namespace="urn:legacy"/></input>
    </operation>
  </binding>
  <service name="Legacy"><port name="LegacyPort" binding="tns:LegacyBinding">
    <soap:address location="https://soap.example.test/legacy"/>
  </port></service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %#v", issues)
	}
	if result.Readiness.State != importreview.Blocked || result.Readiness.ReplayReady {
		t.Fatalf("readiness = %#v", result.Readiness)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("execution blocker = %#v", action.Metadata)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import_review = %#v", action.Metadata["import_review"])
	}
	reasons, ok := review["reason_codes"].([]string)
	if !ok || !reflect.DeepEqual(reasons, []string{
		"wsdl_encoded_use_review_required",
		"wsdl_rpc_style_review_required",
	}) {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if review["source"] != "wsdl" || review["resolution"] != "replace_behavior_and_remove_gate" {
		t.Fatalf("review metadata = %#v", review)
	}
	if !strings.Contains(action.Body, `<tns:LegacyCall>`) ||
		action.SOAP == nil || action.SOAP.Action != "urn:LegacyCall" {
		t.Fatalf("blocked draft lost SOAP behavior: %#v", action)
	}
	if len(result.SourceReview.Mappings) != 1 ||
		result.SourceReview.Mappings[0].TargetPath != "actions[0]" {
		t.Fatalf("source mappings = %#v", result.SourceReview)
	}
}

func TestConvertWSDLIncompleteSkeletonsAreDurablyBlocked(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCode string
	}{
		{
			name: "missing input message",
			input: `<definitions name="MissingInput" targetNamespace="urn:test"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:test">
  <portType name="TestPortType"><operation name="Call"/></portType>
  <binding name="TestBinding" type="tns:TestPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Call"><soap:operation soapAction="urn:Call"/></operation>
  </binding>
  <service name="Test"><port name="TestPort" binding="tns:TestBinding">
    <soap:address location="https://soap.example.test/service"/>
  </port></service>
</definitions>`,
			wantCode: "wsdl_unresolved_input_message_review_required",
		},
		{
			name: "unresolved soap header",
			input: `<definitions name="MissingHeader" targetNamespace="urn:test"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:test">
  <message name="Input"><part name="value" type="xsd:string"/></message>
  <portType name="TestPortType"><operation name="Call"><input message="tns:Input"/></operation></portType>
  <binding name="TestBinding" type="tns:TestPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Call"><soap:operation soapAction="urn:Call"/><input>
      <soap:body use="literal"/><soap:header message="tns:Missing" part="token" use="literal"/>
    </input></operation>
  </binding>
  <service name="Test"><port name="TestPort" binding="tns:TestBinding">
    <soap:address location="https://soap.example.test/service"/>
  </port></service>
</definitions>`,
			wantCode: "wsdl_unresolved_soap_header_review_required",
		},
		{
			name: "unresolved xsd type",
			input: `<definitions name="MissingType" targetNamespace="urn:test"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:test">
  <message name="Input"><part name="payload" type="tns:MissingType"/></message>
  <portType name="TestPortType"><operation name="Call"><input message="tns:Input"/></operation></portType>
  <binding name="TestBinding" type="tns:TestPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation>
  </binding>
  <service name="Test"><port name="TestPort" binding="tns:TestBinding">
    <soap:address location="https://soap.example.test/service"/>
  </port></service>
</definitions>`,
			wantCode: "wsdl_unresolved_xsd_element_review_required",
		},
		{
			name: "unresolved binding operation",
			input: `<definitions name="MissingBindingOperation" targetNamespace="urn:test"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:test">
  <message name="Input"><part name="value" type="xsd:string"/></message>
  <portType name="TestPortType"><operation name="Call"><input message="tns:Input"/></operation></portType>
  <binding name="TestBinding" type="tns:TestPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
  </binding>
  <service name="Test"><port name="TestPort" binding="tns:TestBinding">
    <soap:address location="https://soap.example.test/service"/>
  </port></service>
</definitions>`,
			wantCode: "wsdl_unresolved_binding_operation_review_required",
		},
		{
			name: "unsupported soap transport",
			input: `<definitions name="UnsupportedTransport" targetNamespace="urn:test"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:test">
  <message name="Input"><part name="value" type="xsd:string"/></message>
  <portType name="TestPortType"><operation name="Call"><input message="tns:Input"/></operation></portType>
  <binding name="TestBinding" type="tns:TestPortType">
    <soap:binding style="document" transport="urn:example:non-http"/>
    <operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation>
  </binding>
  <service name="Test"><port name="TestPort" binding="tns:TestBinding">
    <soap:address location="https://soap.example.test/service"/>
  </port></service>
</definitions>`,
			wantCode: "wsdl_unsupported_transport_review_required",
		},
		{
			name: "unresolved imported schema",
			input: `<definitions name="UnresolvedImport" targetNamespace="urn:test"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:test">
  <types><xsd:schema targetNamespace="urn:test"><xsd:import namespace="urn:missing" schemaLocation="missing.xsd"/></xsd:schema></types>
  <message name="Input"><part name="value" type="xsd:string"/></message>
  <portType name="TestPortType"><operation name="Call"><input message="tns:Input"/></operation></portType>
  <binding name="TestBinding" type="tns:TestPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation>
  </binding>
  <service name="Test"><port name="TestPort" binding="tns:TestBinding">
    <soap:address location="https://soap.example.test/service"/>
  </port></service>
</definitions>`,
			wantCode: "wsdl_unresolved_import_review_required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{RootFile: "service.wsdl", Content: tc.input})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("blocked draft validation issues = %#v", issues)
			}
			if result.Readiness.State != importreview.Blocked || result.Readiness.ReplayReady {
				t.Fatalf("readiness = %#v", result.Readiness)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("actions = %d, want 1", len(result.Script.Actions))
			}
			review, ok := result.Script.Actions[0].Metadata["import_review"].(map[string]any)
			if !ok {
				t.Fatalf("import_review = %#v", result.Script.Actions[0].Metadata["import_review"])
			}
			reasons, ok := review["reason_codes"].([]string)
			if !ok || !containsString(reasons, tc.wantCode) {
				t.Fatalf("reason codes = %#v, want %q", review["reason_codes"], tc.wantCode)
			}
		})
	}
}

func TestConvertWSDLMissingEndpointUsesInertBlockedTarget(t *testing.T) {
	result, err := Convert(ImportRequest{
		RootFile: "service.wsdl",
		Content: `<definitions name="NoEndpoint" targetNamespace="urn:test"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:test">
  <message name="Input"><part name="value" type="xsd:string"/></message>
  <portType name="TestPortType"><operation name="Call"><input message="tns:Input"/></operation></portType>
  <binding name="TestBinding" type="tns:TestPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation>
  </binding>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %#v", issues)
	}
	if result.Readiness.State != importreview.Blocked || result.Readiness.ReplayReady {
		t.Fatalf("readiness = %#v", result.Readiness)
	}
	if got := result.Script.Actions[0].Target; got != "https://wsdl.invalid/" {
		t.Fatalf("target = %q, want inert .invalid endpoint", got)
	}
	if !containsString(result.Readiness.BlockingCodes, "wsdl_missing_endpoint_review_required") {
		t.Fatalf("blocking codes = %#v", result.Readiness.BlockingCodes)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
