package wsdl

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	soapproto "github.com/jasonczech/apexload/pkg/protocol/soap"
)

func TestConvertWSDLBuildsSOAPScript(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Account WSDL",
		Content: `<?xml version="1.0"?>
<definitions name="AccountService"
  targetNamespace="urn:accounts"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:accounts">
  <portType name="AccountPortType">
    <operation name="GetAccount"/>
    <operation name="UpdateAccount"/>
  </portType>
  <binding name="AccountBinding" type="tns:AccountPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetAccount">
      <soap:operation soapAction="urn:GetAccount"/>
    </operation>
    <operation name="UpdateAccount">
      <soap:operation soapAction="urn:UpdateAccount"/>
    </operation>
  </binding>
  <service name="AccountService">
    <port name="AccountPort" binding="tns:AccountBinding">
      <soap:address location="https://soap.example.test/account"/>
    </port>
  </service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "soap" {
		t.Fatalf("Protocol = %q, want soap", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "https://soap.example.test/account" {
		t.Fatalf("BaseURL = %q", result.Script.Config.BaseURL)
	}
	if result.Script.Config.SOAP.Version != "1.1" {
		t.Fatalf("SOAP version = %q, want 1.1", result.Script.Config.SOAP.Version)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if result.Script.Actions[0].SOAP == nil || result.Script.Actions[0].SOAP.Action != "urn:GetAccount" {
		t.Fatalf("first SOAP config = %#v", result.Script.Actions[0].SOAP)
	}
	if result.Script.Actions[0].Target != "https://soap.example.test/account" {
		t.Fatalf("first target = %q, want absolute soap:address", result.Script.Actions[0].Target)
	}
	if _, ok := result.Script.Actions[0].Headers["Content-Type"]; ok {
		t.Fatalf("converter should not pre-set Content-Type header: %#v", result.Script.Actions[0].Headers)
	}
	if _, ok := result.Script.Actions[0].Headers["SOAPAction"]; ok {
		t.Fatalf("converter should not pre-set SOAPAction header: %#v", result.Script.Actions[0].Headers)
	}
	if !strings.Contains(result.Script.Actions[0].Body, "<tns:GetAccount>") {
		t.Fatalf("body did not include operation envelope: %s", result.Script.Actions[0].Body)
	}
	if result.Stats.ConvertedActions != 2 || len(result.Stats.SOAPActionsDetected) != 2 {
		t.Fatalf("stats = %+v, want converted SOAP actions", result.Stats)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 || len(result.SourceReview.Mappings) != 2 {
		t.Fatalf("source review = %#v, want source file and two action mappings", result.SourceReview)
	}
	if result.SourceReview.Mappings[0].TargetPath != "actions[0]" || result.SourceReview.Mappings[0].Status != "converted" {
		t.Fatalf("first source mapping = %#v", result.SourceReview.Mappings[0])
	}
}

func TestConvertWSDLFiltersOperationsAndUsesOverrideURL(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<definitions targetNamespace="urn:test" xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap12/">
  <portType name="TestPortType">
    <operation name="Keep"/>
    <operation name="Drop"/>
  </portType>
</definitions>`,
		Options: ConvertOptions{
			BaseURL:    "https://override.example.test/soap",
			Operations: []string{"Keep"},
		},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://override.example.test/soap" {
		t.Fatalf("BaseURL = %q", result.Script.Config.BaseURL)
	}
	if result.Script.Config.SOAP.Version != "1.2" {
		t.Fatalf("SOAP version = %q, want 1.2", result.Script.Config.SOAP.Version)
	}
	if !strings.Contains(result.Script.Actions[0].Body, `http://www.w3.org/2003/05/soap-envelope`) {
		t.Fatalf("SOAP 1.2 body has wrong envelope namespace: %s", result.Script.Actions[0].Body)
	}
	if result.Script.Actions[0].Target != "https://override.example.test/soap" {
		t.Fatalf("target = %q, want absolute override URL", result.Script.Actions[0].Target)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Name != "Keep" {
		t.Fatalf("actions = %+v, want only Keep", result.Script.Actions)
	}
}

func TestConvertWSDLBuildsTypedSkeletonAndReportsAdditionalEndpoints(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions name="AccountService"
  targetNamespace="urn:accounts"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:accounts"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema targetNamespace="urn:accounts">
      <xsd:element name="GetAccountRequest" type="tns:GetAccountRequestType"/>
      <xsd:complexType name="GetAccountRequestType">
        <xsd:sequence>
          <xsd:element name="accountId" type="xsd:string"/>
          <xsd:element name="includeInactive" type="xsd:boolean"/>
          <xsd:element name="billingAddress" type="tns:AddressType"/>
        </xsd:sequence>
      </xsd:complexType>
      <xsd:complexType name="AddressType">
        <xsd:sequence>
          <xsd:element name="street" type="xsd:string"/>
          <xsd:element name="postalCode" type="xsd:string"/>
        </xsd:sequence>
      </xsd:complexType>
    </xsd:schema>
  </types>
  <message name="GetAccountInput">
    <part name="parameters" element="tns:GetAccountRequest"/>
  </message>
  <portType name="AccountPortType">
    <operation name="GetAccount">
      <input message="tns:GetAccountInput"/>
    </operation>
  </portType>
  <binding name="AccountBinding" type="tns:AccountPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetAccount"><soap:operation soapAction="urn:GetAccount"/></operation>
  </binding>
  <service name="AccountService">
    <port name="Primary" binding="tns:AccountBinding">
      <soap:address location="https://primary.example.test/account"/>
    </port>
    <port name="Secondary" binding="tns:AccountBinding">
      <soap:address location="https://secondary.example.test/account"/>
    </port>
  </service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://primary.example.test/account" {
		t.Fatalf("base URL = %q", result.Script.Config.BaseURL)
	}
	body := result.Script.Actions[0].Body
	if !strings.Contains(body, "<tns:GetAccountRequest>") ||
		!strings.Contains(body, "<tns:accountId>{{data.wsdl.accountId}}</tns:accountId>") ||
		!strings.Contains(body, "<tns:includeInactive>true</tns:includeInactive>") ||
		!strings.Contains(body, "<tns:billingAddress>") ||
		!strings.Contains(body, "<tns:street>{{data.wsdl.street}}</tns:street>") {
		t.Fatalf("typed SOAP body was not generated: %s", body)
	}
	if !containsWSDLDiagnostic(result, "WSDL service endpoints") {
		t.Fatalf("diagnostics = %#v, want additional endpoint info", result.Unsupported)
	}
}

func TestConvertWSDLExpandsAllChoiceAndReportsAttributes(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions name="AccountService"
  targetNamespace="urn:accounts"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:accounts"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema targetNamespace="urn:accounts">
      <xsd:element name="UpdateAccountRequest" type="tns:UpdateAccountRequestType"/>
      <xsd:element name="SearchAccountRequest" type="tns:SearchAccountRequestType"/>
      <xsd:complexType name="UpdateAccountRequestType">
        <xsd:all>
          <xsd:element name="accountId" type="xsd:string"/>
          <xsd:element name="status" type="xsd:string"/>
        </xsd:all>
        <xsd:attribute name="requestId" type="xsd:string" use="required"/>
      </xsd:complexType>
      <xsd:complexType name="SearchAccountRequestType">
        <xsd:choice>
          <xsd:element name="accountId" type="xsd:string"/>
          <xsd:element name="email" type="xsd:string"/>
        </xsd:choice>
      </xsd:complexType>
    </xsd:schema>
  </types>
  <message name="UpdateAccountInput">
    <part name="parameters" element="tns:UpdateAccountRequest"/>
  </message>
  <message name="SearchAccountInput">
    <part name="parameters" element="tns:SearchAccountRequest"/>
  </message>
  <portType name="AccountPortType">
    <operation name="UpdateAccount">
      <input message="tns:UpdateAccountInput"/>
    </operation>
    <operation name="SearchAccount">
      <input message="tns:SearchAccountInput"/>
    </operation>
  </portType>
  <binding name="AccountBinding" type="tns:AccountPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="UpdateAccount"><soap:operation soapAction="urn:UpdateAccount"/></operation>
    <operation name="SearchAccount"><soap:operation soapAction="urn:SearchAccount"/></operation>
  </binding>
  <service name="AccountService">
    <port name="AccountPort" binding="tns:AccountBinding">
      <soap:address location="https://soap.example.test/account"/>
    </port>
  </service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	updateBody := bodyForOperation(t, result, "UpdateAccount")
	if !strings.Contains(updateBody, "<tns:accountId>{{data.wsdl.accountId}}</tns:accountId>") ||
		!strings.Contains(updateBody, "<tns:status>{{data.wsdl.status}}</tns:status>") {
		t.Fatalf("xsd:all body was not generated: %s", updateBody)
	}

	searchBody := bodyForOperation(t, result, "SearchAccount")
	if !strings.Contains(searchBody, "<tns:accountId>{{data.wsdl.accountId}}</tns:accountId>") {
		t.Fatalf("xsd:choice first branch was not generated: %s", searchBody)
	}
	if strings.Contains(searchBody, "<tns:email>") {
		t.Fatalf("xsd:choice rendered multiple branches, want representative first branch only: %s", searchBody)
	}

	for _, element := range []string{"WSDL XSD all", "WSDL XSD choice", "WSDL XSD attributes"} {
		if !containsWSDLDiagnostic(result, element) {
			t.Fatalf("diagnostics = %#v, want %s", result.Unsupported, element)
		}
	}
}

func bodyForOperation(t *testing.T, result *ConvertResult, operation string) string {
	t.Helper()
	for _, action := range result.Script.Actions {
		if action.SOAP != nil && action.SOAP.Operation == operation {
			return action.Body
		}
	}
	t.Fatalf("operation %q not found in actions: %#v", operation, result.Script.Actions)
	return ""
}

func containsWSDLDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}

func TestConvertWSDLRendersSOAPHeaderBindings(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions xmlns="http://schemas.xmlsoap.org/wsdl/"
             xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
             xmlns:xsd="http://www.w3.org/2001/XMLSchema"
             xmlns:tns="urn:orders"
             name="OrderService" targetNamespace="urn:orders">
  <types>
    <xsd:schema targetNamespace="urn:orders">
      <xsd:element name="SessionHeader">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="sessionId" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
      <xsd:element name="PlaceOrder">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="orderDate" type="xsd:dateTime"/>
            <xsd:element name="sku" type="xsd:string"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </types>
  <message name="PlaceOrderRequest">
    <part name="parameters" element="tns:PlaceOrder"/>
  </message>
  <message name="SessionHeaderMessage">
    <part name="header" element="tns:SessionHeader"/>
  </message>
  <portType name="OrderPort">
    <operation name="PlaceOrder">
      <input message="tns:PlaceOrderRequest"/>
    </operation>
  </portType>
  <binding name="OrderBinding" type="tns:OrderPort">
    <operation name="PlaceOrder">
      <soap:operation soapAction="urn:orders/PlaceOrder"/>
      <input>
        <soap:header message="tns:SessionHeaderMessage" part="header"/>
      </input>
    </operation>
  </binding>
  <service name="OrderService">
    <port name="OrderPort" binding="tns:OrderBinding">
      <soap:address location="https://orders.example.test/soap"/>
    </port>
  </service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(result.Script.Actions))
	}
	body := result.Script.Actions[0].Body

	// The bound soap:header must be rendered into the envelope header.
	if !strings.Contains(body, "<soapenv:Header>") || !strings.Contains(body, "<tns:SessionHeader>") {
		t.Fatalf("envelope missing rendered SOAP header:\n%s", body)
	}
	if !strings.Contains(body, "<tns:sessionId>") {
		t.Fatalf("envelope header missing nested sessionId field:\n%s", body)
	}

	// dateTime placeholders must use the runtime-supported timestamp function.
	if strings.Contains(body, "{{fn.now()}}") {
		t.Fatalf("envelope uses unsupported fn.now() placeholder:\n%s", body)
	}
	if !strings.Contains(body, "{{fn.timestamp.iso8601}}") {
		t.Fatalf("envelope missing iso8601 timestamp placeholder for dateTime field:\n%s", body)
	}
}

func TestConvertWSDLUnresolvedHeaderBindingWarns(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions xmlns="http://schemas.xmlsoap.org/wsdl/"
             xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
             xmlns:tns="urn:simple"
             name="SimpleService" targetNamespace="urn:simple">
  <message name="DoItRequest">
    <part name="parameters" type="xsd:string"/>
  </message>
  <portType name="SimplePort">
    <operation name="DoIt">
      <input message="tns:DoItRequest"/>
    </operation>
  </portType>
  <binding name="SimpleBinding" type="tns:SimplePort">
    <operation name="DoIt">
      <soap:operation soapAction="urn:simple/DoIt"/>
      <input>
        <soap:header message="tns:MissingHeaderMessage" part="header"/>
      </input>
    </operation>
  </binding>
  <service name="SimpleService">
    <port name="SimplePort" binding="tns:SimpleBinding">
      <soap:address location="https://simple.example.test/soap"/>
    </port>
  </service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	found := false
	for _, diagnostic := range result.Unsupported {
		if strings.Contains(diagnostic.Element, "SOAP header") && strings.Contains(diagnostic.Detail, "could not be resolved") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unresolved soap:header warning, got %+v", result.Unsupported)
	}
}

// TestConvertWSDLHandlesRecursiveTypeWithoutStackOverflow feeds a self-referential
// complexType (NodeType contains a child of type NodeType). Without the depth cap
// this recurses forever and overflows the stack (a fatal throw that crashes the
// controller); the test completing at all is the regression assertion.
func TestConvertWSDLHandlesRecursiveTypeWithoutStackOverflow(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions name="NodeService" targetNamespace="urn:nodes"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:nodes"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema targetNamespace="urn:nodes">
      <xsd:element name="GetNodeRequest" type="tns:NodeType"/>
      <xsd:complexType name="NodeType">
        <xsd:sequence>
          <xsd:element name="label" type="xsd:string"/>
          <xsd:element name="child" type="tns:NodeType"/>
        </xsd:sequence>
      </xsd:complexType>
    </xsd:schema>
  </types>
  <message name="GetNodeInput"><part name="parameters" element="tns:GetNodeRequest"/></message>
  <portType name="NodePortType"><operation name="GetNode"><input message="tns:GetNodeInput"/></operation></portType>
  <binding name="NodeBinding" type="tns:NodePortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetNode"><soap:operation soapAction="urn:GetNode"/></operation>
  </binding>
  <service name="NodeService"><port name="Primary" binding="tns:NodeBinding"><soap:address location="https://nodes.example.test/node"/></port></service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) == 0 || result.Script.Actions[0].Body == "" {
		t.Fatal("expected a bounded SOAP body for the recursive type")
	}
}

// TestConvertWSDLHandlesRecursiveSimpleTypeWithoutStackOverflow covers the
// simpleType placeholder-resolution vector (distinct from the complexType case
// above): a self-restricting simpleType and a mutually-recursive pair. Without
// the depth cap in placeholderForSimpleType/XSDType/NamedSimpleType these
// recurse forever and stack-overflow the controller during import. The test
// completing at all is the regression assertion.
func TestConvertWSDLHandlesRecursiveSimpleTypeWithoutStackOverflow(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions name="CyclicService" targetNamespace="urn:cyc"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:cyc"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema targetNamespace="urn:cyc">
      <xsd:element name="GetThingRequest" type="tns:SelfRef"/>
      <xsd:simpleType name="SelfRef">
        <xsd:restriction base="tns:SelfRef"/>
      </xsd:simpleType>
      <xsd:simpleType name="A">
        <xsd:restriction base="tns:B"/>
      </xsd:simpleType>
      <xsd:simpleType name="B">
        <xsd:restriction base="tns:A"/>
      </xsd:simpleType>
    </xsd:schema>
  </types>
  <message name="GetThingInput"><part name="parameters" element="tns:GetThingRequest"/></message>
  <portType name="ThingPortType"><operation name="GetThing"><input message="tns:GetThingInput"/></operation></portType>
  <binding name="ThingBinding" type="tns:ThingPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetThing"><soap:operation soapAction="urn:GetThing"/></operation>
  </binding>
  <service name="ThingService"><port name="Primary" binding="tns:ThingBinding"><soap:address location="https://things.example.test/thing"/></port></service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) == 0 || result.Script.Actions[0].Body == "" {
		t.Fatal("expected a bounded SOAP body for the recursive simpleType")
	}
}

func TestConvertWSDLSelectsRequestedBinding(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions name="DualService"
  targetNamespace="urn:dual"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:soap12="http://schemas.xmlsoap.org/wsdl/soap12/"
  xmlns:tns="urn:dual">
  <message name="LookupInput"><part name="parameters" type="xsd:string"/></message>
  <portType name="DualPortType"><operation name="Lookup"><input message="tns:LookupInput"/></operation></portType>
  <binding name="DualSoap11" type="tns:DualPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Lookup"><soap:operation soapAction="urn:Lookup11"/></operation>
  </binding>
  <binding name="DualSoap12" type="tns:DualPortType">
    <soap12:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Lookup"><soap12:operation soapAction="urn:Lookup12"/></operation>
  </binding>
  <service name="DualService">
    <port name="Soap11Port" binding="tns:DualSoap11"><soap:address location="https://dual.example.test/soap11"/></port>
    <port name="Soap12Port" binding="tns:DualSoap12"><soap12:address location="https://dual.example.test/soap12"/></port>
  </service>
</definitions>`,
		Options: ConvertOptions{PortName: "Soap12Port"},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Summary.PortName != "Soap12Port" || result.Summary.BindingName != "DualSoap12" {
		t.Fatalf("summary = %+v, want selected SOAP 1.2 port/binding", result.Summary)
	}
	if result.Script.Config.BaseURL != "https://dual.example.test/soap12" {
		t.Fatalf("BaseURL = %q, want selected endpoint", result.Script.Config.BaseURL)
	}
	if result.Script.Config.SOAP.Version != "1.2" {
		t.Fatalf("SOAP version = %q, want 1.2", result.Script.Config.SOAP.Version)
	}
	if result.Script.Actions[0].SOAP.Action != "urn:Lookup12" {
		t.Fatalf("SOAP action = %q, want selected binding action", result.Script.Actions[0].SOAP.Action)
	}
	if len(result.Summary.BindingAlternatives) != 2 {
		t.Fatalf("BindingAlternatives = %+v, want both candidates", result.Summary.BindingAlternatives)
	}
}

func TestConvertWSDLZipResolvesLocalXSDInclude(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	addZipFile := func(name string, content string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	addZipFile("svc/service.wsdl", `<?xml version="1.0"?>
<definitions name="ZipService"
  targetNamespace="urn:zip"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:zip"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema targetNamespace="urn:zip">
      <xsd:include schemaLocation="../schema/request.xsd"/>
    </xsd:schema>
  </types>
  <message name="LookupInput"><part name="parameters" element="tns:LookupRequest"/></message>
  <portType name="ZipPortType"><operation name="Lookup"><input message="tns:LookupInput"/></operation></portType>
  <binding name="ZipBinding" type="tns:ZipPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Lookup"><soap:operation soapAction="urn:Lookup"/></operation>
  </binding>
  <service name="ZipService"><port name="ZipPort" binding="tns:ZipBinding"><soap:address location="https://zip.example.test/soap"/></port></service>
</definitions>`)
	addZipFile("schema/request.xsd", `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:tns="urn:zip" targetNamespace="urn:zip">
  <xsd:element name="LookupRequest" type="tns:LookupRequestType"/>
  <xsd:complexType name="LookupRequestType">
    <xsd:sequence>
      <xsd:element name="accountId" type="xsd:string"/>
    </xsd:sequence>
  </xsd:complexType>
</xsd:schema>`)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	root, files, err := FilesFromZip(archive.Bytes())
	if err != nil {
		t.Fatalf("FilesFromZip returned error: %v", err)
	}
	result, err := Convert(ImportRequest{RootFile: root, Files: files})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	body := result.Script.Actions[0].Body
	if !strings.Contains(body, `<tns:accountId>{{data.wsdl.accountId}}</tns:accountId>`) {
		t.Fatalf("body did not resolve included XSD type: %s", body)
	}
	if containsWSDLDiagnostic(result, "referenced XSD include") {
		t.Fatalf("diagnostics = %#v, include should have been resolved", result.Unsupported)
	}
}

func TestFilesFromZipRejectsUnsafeArchiveEntries(t *testing.T) {
	tests := []struct {
		name       string
		entryNames []string
		symlink    bool
	}{
		{name: "traversal", entryNames: []string{"../service.wsdl"}},
		{name: "absolute", entryNames: []string{"/service.wsdl"}},
		{name: "separator collision", entryNames: []string{`svc\service.wsdl`, "svc/service.wsdl"}},
		{name: "case collision", entryNames: []string{"svc/service.wsdl", "SVC/SERVICE.WSDL"}},
		{name: "symlink", entryNames: []string{"service.wsdl"}, symlink: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			for _, name := range test.entryNames {
				header := &zip.FileHeader{Name: name}
				if test.symlink {
					header.SetMode(fs.ModeSymlink | 0o777)
				}
				entry, err := writer.CreateHeader(header)
				if err != nil {
					t.Fatalf("CreateHeader: %v", err)
				}
				_, _ = entry.Write([]byte("<definitions/>"))
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if _, _, err := FilesFromZip(archive.Bytes()); err == nil {
				t.Fatal("FilesFromZip accepted unsafe archive")
			}
		})
	}
}

func TestConvertRejectsUnsafeDirectFilePathsDeterministically(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "cleaning alias",
			files: map[string]string{
				"service.wsdl":      "<definitions/>",
				"a/../service.wsdl": "<definitions/>",
			},
		},
		{
			name: "traversal",
			files: map[string]string{
				"../../x.wsdl": "<definitions/>",
			},
		},
		{
			name: "separator collision",
			files: map[string]string{
				`svc\service.wsdl`: "<definitions/>",
				"svc/service.wsdl": "<definitions/>",
			},
		},
		{
			name: "case collision",
			files: map[string]string{
				"svc/service.wsdl": "<definitions/>",
				"SVC/SERVICE.WSDL": "<definitions/>",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			firstError := ""
			for iteration := 0; iteration < 25; iteration++ {
				_, err := Convert(ImportRequest{Files: test.files})
				if err == nil {
					t.Fatalf("Convert accepted unsafe direct files: %#v", test.files)
				}
				if firstError == "" {
					firstError = err.Error()
				} else if err.Error() != firstError {
					t.Fatalf("nondeterministic error: first %q, iteration %d %q", firstError, iteration, err)
				}
			}
		})
	}
}

func TestConvertRejectsUnsafeDirectRootFile(t *testing.T) {
	t.Parallel()
	if _, err := Convert(ImportRequest{
		Content:  "<definitions/>",
		RootFile: "../../service.wsdl",
	}); err == nil {
		t.Fatal("Convert accepted traversal root_file")
	}
}

func TestConvertBoundsDirectContentAndRootOverride(t *testing.T) {
	t.Run("direct content", func(t *testing.T) {
		_, err := Convert(ImportRequest{Content: strings.Repeat(" ", maxWSDLBundleBytes+1)})
		if err == nil || !strings.Contains(err.Error(), "maximum size") {
			t.Fatalf("Convert() error = %v, want direct content size rejection", err)
		}
	})

	t.Run("root override contributes to bundle total", func(t *testing.T) {
		_, err := Convert(ImportRequest{
			RootFile: "service.wsdl",
			Content:  strings.Repeat("x", maxWSDLBundleBytes/2+1),
			Files: map[string]string{
				"service.wsdl": "<definitions/>",
				"schema.xsd":   strings.Repeat("y", maxWSDLBundleBytes/2),
			},
		})
		if err == nil || !strings.Contains(err.Error(), "maximum total size") {
			t.Fatalf("Convert() error = %v, want root override bundle size rejection", err)
		}
	})
}

func TestConvertWSDLReplayRoundTripUsesSingleEndpointPath(t *testing.T) {
	var seenPath string
	var seenSOAPAction string
	var seenContentType string
	var seenBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenSOAPAction = r.Header.Get("SOAPAction")
		seenContentType = r.Header.Get("Content-Type")
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		seenBody = string(payload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><ok/></soapenv:Body></soapenv:Envelope>`))
	}))
	defer ts.Close()

	result, err := Convert(ImportRequest{
		Content: `<?xml version="1.0"?>
<definitions name="AccountService"
  targetNamespace="urn:accounts"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:tns="urn:accounts">
  <portType name="AccountPortType"><operation name="GetAccount"/></portType>
  <binding name="AccountBinding" type="tns:AccountPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetAccount"><soap:operation soapAction="urn:GetAccount"/></operation>
  </binding>
  <service name="AccountService"><port name="AccountPort" binding="tns:AccountBinding"><soap:address location="` + ts.URL + `/account"/></port></service>
</definitions>`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != ts.URL+"/account" {
		t.Fatalf("BaseURL = %q", result.Script.Config.BaseURL)
	}
	if result.Script.Actions[0].Target != ts.URL+"/account" {
		t.Fatalf("Target = %q, want absolute endpoint", result.Script.Actions[0].Target)
	}

	s := &soapproto.SOAP{}
	if err := s.Init(context.Background(), map[string]any{
		"base_url":                 result.Script.Config.BaseURL,
		"soap_version":             result.Script.Config.SOAP.Version,
		"soap_fault_fail_on_fault": true,
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	action := result.Script.Actions[0]
	replayResult, err := s.Execute(context.Background(), &protocol.Action{
		Name:    action.Name,
		Type:    action.Type,
		Method:  action.Method,
		Target:  action.Target,
		Headers: action.Headers,
		Body:    []byte(action.Body),
		SOAP: &protocol.SOAPActionConfig{
			Action:    action.SOAP.Action,
			Operation: action.SOAP.Operation,
			BodyMode:  action.SOAP.BodyMode,
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if replayResult == nil || !replayResult.Success {
		t.Fatalf("replay result = %#v, want success", replayResult)
	}
	if seenPath != "/account" {
		t.Fatalf("path = %q, want /account without duplicated path", seenPath)
	}
	if seenSOAPAction != `"urn:GetAccount"` {
		t.Fatalf("SOAPAction = %q, want quoted action", seenSOAPAction)
	}
	if seenContentType != "text/xml; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want SOAP 1.1 default", seenContentType)
	}
	if !strings.Contains(seenBody, `http://schemas.xmlsoap.org/soap/envelope/`) {
		t.Fatalf("body = %q, want SOAP 1.1 envelope namespace", seenBody)
	}
}
