package wsdl

import (
	"strings"
	"testing"
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
