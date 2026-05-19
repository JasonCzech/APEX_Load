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
