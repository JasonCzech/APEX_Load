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

func containsWSDLDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}
