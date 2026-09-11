package wsdl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertWSDLPreservesSensitiveSchemaLiteralsAndEnvelopeBytes(t *testing.T) {
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
  <types><xsd:schema targetNamespace="urn:login" elementFormDefault="qualified">
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
      <tns:password>` + passwordCanary + `</tns:password>
      <tns:accessToken>` + tokenCanary + `</tns:accessToken>
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
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("preserved credentials unexpectedly require rebinding: %#v", result.SecretRequirements)
	}
	if blocker := action.Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("informational credential preservation finding blocked execution: %#v", action.Metadata)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil ||
		result.Script.Metadata.Import.Source != "wsdl" ||
		result.Script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] !=
			dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("WSDL script has no import credential-preservation marker: %#v", result.Script.Metadata)
	}
	rawTargets, err := json.Marshal(
		action.Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey],
	)
	if err != nil {
		t.Fatalf("marshal preserved credential targets: %v", err)
	}
	var preservedTargets []credentialpolicy.TargetRef
	if err := json.Unmarshal(rawTargets, &preservedTargets); err != nil {
		t.Fatalf("decode preserved credential targets: %v", err)
	}
	if len(preservedTargets) != 2 ||
		preservedTargets[0].Field != "body.xml.text" ||
		preservedTargets[0].Pointer != "/Envelope/Body/LoginRequest/password" ||
		preservedTargets[1].Field != "body.xml.text" ||
		preservedTargets[1].Pointer != "/Envelope/Body/LoginRequest/accessToken" {
		t.Fatalf("preserved credential targets = %#v", preservedTargets)
	}
	if result.Readiness.State != importreview.Ready ||
		!result.Readiness.ReplayReady ||
		!result.Readiness.PromotionReady {
		t.Fatalf("credential-preserving WSDL readiness = %#v, want executable", result.Readiness)
	}
	if containsString(result.Readiness.BlockingCodes, "credential_bindings_required") {
		t.Fatalf("preserved credentials made the script depend on operator reentry: %#v", result.Readiness)
	}
	for _, canary := range []string{passwordCanary, tokenCanary} {
		found := false
		if result.SourceReview != nil {
			for _, file := range result.SourceReview.SourceFiles {
				found = found || strings.Contains(file.Content, canary)
				if file.Redacted {
					t.Fatalf("source review file %q was redacted", file.Path)
				}
			}
		}
		if !found {
			t.Fatalf("source review lost canary %q", canary)
		}
	}
}

func TestConvertWSDLPreservesCredentialInUnusedSchemaBranch(t *testing.T) {
	const unusedCanary = "unused-credential-canary-1c95f761a0"
	result, err := Convert(ImportRequest{
		Content: `<definitions name="ReviewSurface" targetNamespace="urn:review"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:tns="urn:review">
  <types><xsd:schema targetNamespace="urn:review" elementFormDefault="qualified">
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
	if !strings.Contains(string(encoded), unusedCanary) {
		t.Fatalf("unused schema credential was not preserved: %s", encoded)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("unused schema source was redacted: %#v", result.SourceReview)
	}
}

func TestConvertWSDLPreservesEndpointCredentialsAndSourceReview(t *testing.T) {
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
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("preserved endpoint credentials unexpectedly require rebinding: %#v", result.SecretRequirements)
	}
	const wantBaseURL = "https://alice:wsdl-pass@api.example.test/root?api_key=wsdl%2Dquery"
	if result.Script.Config.BaseURL != wantBaseURL {
		t.Fatalf("base URL = %q, want preserved %q", result.Script.Config.BaseURL, wantBaseURL)
	}
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Target != result.Script.Config.BaseURL {
		t.Fatalf("SOAP target did not reuse the preserved base URL: %#v", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		!strings.Contains(result.SourceReview.SourceFiles[0].Content, "wsdl&#45;pass") ||
		result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("encoded endpoint source was not preserved: %#v", result.SourceReview)
	}
}

func TestProjectWSDLCredentialsPreservesEntireXMLAndAttributeSyntax(t *testing.T) {
	const (
		textCanary = "credential-canary-text-13af"
		attrCanary = "credential-canary-attr-24b0"
	)
	body := []byte(`<env:Envelope xmlns:env="urn:env"><env:Header/><env:Body><auth:token xmlns:auth="urn:auth" apiKey='` + attrCanary + `'>  ` + textCanary + `  </auth:token><note a="1">&amp; exact</note></env:Body></env:Envelope>`)
	projected, secrets, findings, targets, err := finalizeWSDLXMLBody(body, wsdlCredentialProjectionOptions{
		RequestKey: "login",
		SourcePath: "login.wsdl",
		Pointer:    "operation/Login/body",
		Registry:   credentialpolicy.NewNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 ||
		findings[0].Code != "import_credential_preserved" ||
		findings[0].Gate != importreview.GateNone ||
		len(secrets) != 0 ||
		len(targets) != 2 {
		t.Fatalf("projection sidecars: findings=%#v secrets=%#v targets=%#v", findings, secrets, targets)
	}
	got := string(projected)
	if got != string(body) {
		t.Fatalf("credential XML bytes changed:\n got: %q\nwant: %q", got, body)
	}
	if !strings.Contains(got, `apiKey='`+attrCanary+`'`) {
		t.Fatalf("single-quoted attribute syntax was not preserved: %s", got)
	}
	if !strings.Contains(got, `>  `+textCanary+`  </auth:token>`) {
		t.Fatalf("element whitespace was not preserved: %s", got)
	}
	if !strings.Contains(got, `<note a="1">&amp; exact</note>`) {
		t.Fatalf("nonsensitive XML bytes changed: %s", got)
	}
}

func TestProjectWSDLCredentialsPreservesValidMixedSensitiveContent(t *testing.T) {
	const (
		canary      = "credential-canary-mixed-35c1"
		childCanary = "credential-canary-child-46d2"
	)
	body := []byte(`<Envelope><credentials>
  <username>alice</username>
  <password>` + canary + `<accessToken>` + childCanary + `</accessToken>suffix</password>
</credentials><note>exact</note></Envelope>`)
	projected, secrets, findings, targets, err := finalizeWSDLXMLBody(body, wsdlCredentialProjectionOptions{
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
		findings[0].Gate != importreview.GateNone {
		t.Fatalf("findings = %#v", findings)
	}
	if len(targets) != 1 ||
		targets[0].Field != "body.xml.mixed_content" ||
		targets[0].Pointer != "/Envelope/credentials" {
		t.Fatalf("preserved targets = %#v", targets)
	}
	got := string(projected)
	if got != string(body) {
		t.Fatalf("valid mixed credential XML changed:\n got: %q\nwant: %q", got, body)
	}
}

func TestProjectWSDLCredentialsPreservesMalformedXMLButBlocksExecution(t *testing.T) {
	body := []byte(`<Envelope><password>wsdl-malformed-canary</Envelope>`)
	projected, secrets, findings, _, err := finalizeWSDLXMLBody(body, wsdlCredentialProjectionOptions{
		RequestKey: "login",
		SourcePath: "login.wsdl",
		Pointer:    "operation/Login/body",
		Registry:   credentialpolicy.NewNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(projected) != string(body) {
		t.Fatalf("malformed XML bytes changed:\n got: %q\nwant: %q", projected, body)
	}
	if len(secrets) != 0 {
		t.Fatalf("malformed preserved XML unexpectedly created requirements: %#v", secrets)
	}
	if !wsdlFindingHasExecutionGate(findings, "credential_xml_malformed") {
		t.Fatalf("findings = %#v, want malformed XML execution gate", findings)
	}
}

func TestProjectWSDLCredentialsPreservesExplicitSecretReference(t *testing.T) {
	body := []byte(`<Envelope><password>{{secret.EXISTING_PASSWORD}}</password></Envelope>`)
	projected, secrets, findings, _, err := finalizeWSDLXMLBody(body, wsdlCredentialProjectionOptions{
		RequestKey: "login",
		SourcePath: "login.wsdl",
		Pointer:    "operation/Login/body",
		Registry:   credentialpolicy.NewNameRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(projected) != string(body) {
		t.Fatalf("authored secret reference changed:\n got: %q\nwant: %q", projected, body)
	}
	if len(secrets) != 0 || wsdlFindingHasExecutionGate(findings, "") {
		t.Fatalf("authored secret reference produced side effects: secrets=%#v findings=%#v", secrets, findings)
	}
}

func wsdlFindingHasExecutionGate(findings []importreview.Finding, code string) bool {
	for _, finding := range findings {
		if (code == "" || finding.Code == code) && finding.Gate == importreview.GateExecution {
			return true
		}
	}
	return false
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
  <types><xsd:schema targetNamespace="urn:test" elementFormDefault="qualified"><xsd:import namespace="urn:missing" schemaLocation="missing.xsd"/></xsd:schema></types>
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
