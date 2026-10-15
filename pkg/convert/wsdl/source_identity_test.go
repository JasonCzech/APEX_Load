package wsdl

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertStampsTheDeclaredService(t *testing.T) {
	result, err := Convert(ImportRequest{Name: "account.wsdl", Content: `<?xml version="1.0"?>
<definitions name="AccountService" targetNamespace="urn:accounts"
  xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:tns="urn:accounts">
  <portType name="AccountPortType"><operation name="GetAccount"/></portType>
  <binding name="AccountBinding" type="tns:AccountPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="GetAccount"><soap:operation soapAction="urn:GetAccount"/></operation>
  </binding>
  <service name="AccountService">
    <port name="AccountPort" binding="tns:AccountBinding"><soap:address location="https://soap.example.test/account"/></port>
  </service>
</definitions>`})
	if err != nil {
		t.Fatal(err)
	}
	document, _, declared, ok := dsl.ImportSourceIdentityOf(result.Script.Metadata.Import)
	if !ok || document != "urn:accounts AccountService" || !declared {
		t.Fatalf("identity = %q declared=%v ok=%v", document, declared, ok)
	}
}

// WCF's default service name in its default namespace declares nothing.
func TestConvertDoesNotDeclareADefaultWCFService(t *testing.T) {
	if !wsdlDefaultService("http://tempuri.org/", "Service1") || !wsdlDefaultService("", "Service") {
		t.Fatal("a default WCF service is taken as declared")
	}
	if wsdlDefaultService("urn:accounts", "Service1") || wsdlDefaultService("http://tempuri.org/", "AccountService") {
		t.Fatal("a named or namespaced service is taken as a default")
	}
}
