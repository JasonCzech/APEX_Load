package wsdl

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestConvertWSDLUsesSchemaNamespaceAndLocalElementForm(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `<?xml version="1.0"?>
<definitions name="NamespaceService"
  targetNamespace="urn:service"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:svc="urn:service"
  xmlns:typ="urn:types"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
    <xsd:schema targetNamespace="urn:types" elementFormDefault="unqualified">
      <xsd:element name="Request">
        <xsd:complexType>
          <xsd:sequence>
            <xsd:element name="localChild" type="xsd:string"/>
            <xsd:element name="qualifiedChild" type="xsd:string" form="qualified"/>
          </xsd:sequence>
        </xsd:complexType>
      </xsd:element>
    </xsd:schema>
  </types>
  <message name="RequestMessage"><part name="parameters" element="typ:Request"/></message>
  <portType name="NamespacePort"><operation name="Call"><input message="svc:RequestMessage"/></operation></portType>
  <binding name="NamespaceBinding" type="svc:NamespacePort">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Call">
      <soap:operation soapAction="urn:Call"/>
      <input><soap:body use="literal"/></input>
    </operation>
  </binding>
  <service name="NamespaceService"><port name="NamespacePort" binding="svc:NamespaceBinding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	for _, expected := range []string{
		`xmlns:tns="urn:types"`,
		`<tns:Request>`,
		`<localChild>{{data.wsdl.localChild}}</localChild>`,
		`<tns:qualifiedChild>{{data.wsdl.qualifiedChild}}</tns:qualifiedChild>`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("SOAP body missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "<tns:localChild>") || strings.Contains(body, `xmlns:tns="urn:service"`) {
		t.Fatalf("SOAP body used the wrong element namespace/form:\n%s", body)
	}
}

func TestConvertWSDLResolvesQualifiedElementAgainstMatchingSchemaNamespace(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `<?xml version="1.0"?>
<definitions name="QualifiedService"
  targetNamespace="urn:service"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:svc="urn:service"
  xmlns:a="urn:schema:a"
  xmlns:b="urn:schema:b"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <types>
	    <xsd:schema targetNamespace="urn:schema:a" elementFormDefault="qualified">
	      <xsd:simpleType name="Status"><xsd:restriction base="xsd:string"><xsd:enumeration value="WRONG"/></xsd:restriction></xsd:simpleType>
	      <xsd:element name="Request"><xsd:complexType><xsd:sequence>
        <xsd:element name="wrongSchemaField" type="xsd:string"/>
      </xsd:sequence></xsd:complexType></xsd:element>
    </xsd:schema>
	    <xsd:schema targetNamespace="urn:schema:b" elementFormDefault="qualified">
	      <xsd:simpleType name="Status"><xsd:restriction base="xsd:string"><xsd:enumeration value="RIGHT"/></xsd:restriction></xsd:simpleType>
	      <xsd:element name="Request"><xsd:complexType><xsd:sequence>
	        <xsd:element name="rightSchemaField" type="xsd:string"/>
	        <xsd:element name="status" type="b:Status"/>
      </xsd:sequence></xsd:complexType></xsd:element>
    </xsd:schema>
  </types>
  <message name="RequestMessage"><part name="parameters" element="b:Request"/></message>
  <portType name="QualifiedPort"><operation name="Call"><input message="svc:RequestMessage"/></operation></portType>
  <binding name="QualifiedBinding" type="svc:QualifiedPort">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation>
  </binding>
  <service name="QualifiedService"><port name="QualifiedPort" binding="svc:QualifiedBinding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	for _, expected := range []string{`xmlns:tns="urn:schema:b"`, `<tns:Request>`, `<tns:rightSchemaField>`, `>RIGHT</tns:status>`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("SOAP body missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "wrongSchemaField") || strings.Contains(body, `xmlns:tns="urn:schema:a"`) || strings.Contains(body, "WRONG") {
		t.Fatalf("SOAP body resolved b:Request through the wrong same-local-name schema:\n%s", body)
	}
}

func TestConvertWSDLRendersNestedCrossNamespaceRefWithDeclaringNamespace(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `<?xml version="1.0"?>
<definitions targetNamespace="urn:service" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:svc="urn:service"
 xmlns:a="urn:schema:a" xmlns:b="urn:schema:b" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types>
  <xsd:schema targetNamespace="urn:schema:a" elementFormDefault="qualified">
   <xsd:element name="Request"><xsd:complexType><xsd:sequence><xsd:element ref="b:Other"/></xsd:sequence></xsd:complexType></xsd:element>
  </xsd:schema>
  <xsd:schema targetNamespace="urn:schema:b" elementFormDefault="qualified">
   <xsd:element name="Other"><xsd:complexType><xsd:sequence><xsd:element name="fromB" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element>
  </xsd:schema>
 </types>
 <message name="RequestMessage"><part name="parameters" element="a:Request"/></message>
 <portType name="Port"><operation name="Call"><input message="svc:RequestMessage"/></operation></portType>
 <binding name="Binding" type="svc:Port"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation></binding>
 <service name="Service"><port name="Port" binding="svc:Binding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`})
	if err != nil {
		t.Fatal(err)
	}
	namespaces := soapStartElementNamespaces(t, result.Script.Actions[0].Body)
	for element, want := range map[string]string{"Request": "urn:schema:a", "Other": "urn:schema:b", "fromB": "urn:schema:b"} {
		if got := namespaces[element]; got != want {
			t.Fatalf("element %s namespace = %q, want %q; body:\n%s", element, got, want, result.Script.Actions[0].Body)
		}
	}
}

func TestConvertWSDLResolvesElementLocalQNameBindings(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `<?xml version="1.0"?>
<definitions targetNamespace="urn:service" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:svc="urn:service"
 xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types>
  <xsd:schema targetNamespace="urn:schema:a" elementFormDefault="qualified">
   <xsd:element name="Request"><xsd:complexType><xsd:sequence>
    <xsd:element ref="b:Other" xmlns:b="urn:schema:b"/>
    <xsd:element name="typed" type="b:OtherType" xmlns:b="urn:schema:b"/>
   </xsd:sequence></xsd:complexType></xsd:element>
  </xsd:schema>
  <xsd:schema targetNamespace="urn:schema:b" elementFormDefault="qualified">
   <xsd:element name="Other"><xsd:complexType><xsd:sequence><xsd:element name="fromLocalRef" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element>
   <xsd:complexType name="OtherType"><xsd:sequence><xsd:element name="fromLocalType" type="xsd:string"/></xsd:sequence></xsd:complexType>
  </xsd:schema>
 </types>
 <message name="RequestMessage"><part name="parameters" element="a:Request" xmlns:a="urn:schema:a"/></message>
 <portType name="Port"><operation name="Call"><input message="svc:RequestMessage"/></operation></portType>
 <binding name="Binding" type="svc:Port"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation></binding>
 <service name="Service"><port name="Port" binding="svc:Binding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	namespaces := soapStartElementNamespaces(t, body)
	for element, want := range map[string]string{
		"Request":       "urn:schema:a",
		"Other":         "urn:schema:b",
		"fromLocalRef":  "urn:schema:b",
		"typed":         "urn:schema:a",
		"fromLocalType": "urn:schema:b",
	} {
		if got := namespaces[element]; got != want {
			t.Fatalf("element %s namespace = %q, want %q; body:\n%s", element, got, want, body)
		}
	}
}

func TestConvertWSDLResolvesAncestorLocalQNameBindings(t *testing.T) {
	const document = `<?xml version="1.0"?>
<definitions targetNamespace="urn:service" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:svc="urn:service"
 xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types>
  <xsd:schema targetNamespace="urn:schema:a" elementFormDefault="qualified">
   <xsd:element name="Request">__REQUEST_MODEL__</xsd:element>
  </xsd:schema>
  <xsd:schema targetNamespace="urn:schema:b" elementFormDefault="qualified">
   <xsd:element name="Other"><xsd:complexType><xsd:sequence><xsd:element name="fromB" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element>
  </xsd:schema>
 </types>
 <message name="RequestMessage"><part name="parameters" element="a:Request" xmlns:a="urn:schema:a"/></message>
 <portType name="Port"><operation name="Call"><input message="svc:RequestMessage"/></operation></portType>
 <binding name="Binding" type="svc:Port"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation></binding>
 <service name="Service"><port name="Port" binding="svc:Binding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`
	tests := []struct {
		name  string
		model string
	}{
		{
			name:  "complex type declaration",
			model: `<xsd:complexType xmlns:b="urn:schema:b"><xsd:sequence><xsd:element ref="b:Other"/></xsd:sequence></xsd:complexType>`,
		},
		{
			name:  "sequence declaration",
			model: `<xsd:complexType><xsd:sequence xmlns:b="urn:schema:b"><xsd:element ref="b:Other"/></xsd:sequence></xsd:complexType>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: strings.Replace(document, "__REQUEST_MODEL__", tt.model, 1)})
			if err != nil {
				t.Fatal(err)
			}
			body := result.Script.Actions[0].Body
			namespaces := soapStartElementNamespaces(t, body)
			for element, want := range map[string]string{"Request": "urn:schema:a", "Other": "urn:schema:b", "fromB": "urn:schema:b"} {
				if got := namespaces[element]; got != want {
					t.Fatalf("element %s namespace = %q, want %q; body:\n%s", element, got, want, body)
				}
			}
		})
	}
}

func TestConvertWSDLLocallyBindsDifferentHeaderAndBodyNamespaces(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `<?xml version="1.0"?>
<definitions targetNamespace="urn:service" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:svc="urn:service"
 xmlns:a="urn:body" xmlns:b="urn:header" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types>
  <xsd:schema targetNamespace="urn:body" elementFormDefault="qualified"><xsd:element name="Request"><xsd:complexType><xsd:sequence><xsd:element name="bodyValue" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element></xsd:schema>
  <xsd:schema targetNamespace="urn:header" elementFormDefault="qualified"><xsd:element name="AuthHeader"><xsd:complexType><xsd:sequence><xsd:element name="headerValue" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element></xsd:schema>
 </types>
 <message name="RequestMessage"><part name="body" element="a:Request"/></message>
 <message name="HeaderMessage"><part name="auth" element="b:AuthHeader"/></message>
 <portType name="Port"><operation name="Call"><input message="svc:RequestMessage"/></operation></portType>
 <binding name="Binding" type="svc:Port"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/><soap:header message="svc:HeaderMessage" part="auth" use="literal"/></input></operation></binding>
 <service name="Service"><port name="Port" binding="svc:Binding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`})
	if err != nil {
		t.Fatal(err)
	}
	namespaces := soapStartElementNamespaces(t, result.Script.Actions[0].Body)
	for element, want := range map[string]string{"Request": "urn:body", "bodyValue": "urn:body", "AuthHeader": "urn:header", "headerValue": "urn:header"} {
		if got := namespaces[element]; got != want {
			t.Fatalf("element %s namespace = %q, want %q; body:\n%s", element, got, want, result.Script.Actions[0].Body)
		}
	}
}

func soapStartElementNamespaces(t *testing.T, body string) map[string]string {
	t.Helper()
	namespaces := map[string]string{}
	decoder := xml.NewDecoder(strings.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode SOAP XML: %v\n%s", err, body)
		}
		if start, ok := token.(xml.StartElement); ok {
			namespaces[start.Name.Local] = start.Name.Space
		}
	}
	return namespaces
}

func TestConvertWSDLKeepsImportedSchemaNamespacesDistinct(t *testing.T) {
	const root = `<?xml version="1.0"?>
<definitions targetNamespace="urn:service" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:svc="urn:service"
 xmlns:a="urn:schema:a" xmlns:b="urn:schema:b" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types><xsd:schema targetNamespace="urn:service">
  <xsd:import namespace="urn:schema:a" schemaLocation="a.xsd"/>
  <xsd:import namespace="urn:schema:b" schemaLocation="b.xsd"/>
 </xsd:schema></types>
 <message name="RequestMessage"><part name="parameters" element="b:Request"/></message>
 <portType name="Port"><operation name="Call"><input message="svc:RequestMessage"/></operation></portType>
 <binding name="Binding" type="svc:Port"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation></binding>
 <service name="Service"><port name="Port" binding="svc:Binding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`
	const schemaA = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:schema:a" elementFormDefault="qualified"><xsd:element name="Request"><xsd:complexType><xsd:sequence><xsd:element name="fromA" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element></xsd:schema>`
	const schemaB = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:schema:b" elementFormDefault="qualified"><xsd:element name="Request"><xsd:complexType><xsd:sequence><xsd:element name="fromB" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element></xsd:schema>`

	result, err := Convert(ImportRequest{RootFile: "service.wsdl", Files: map[string]string{
		"service.wsdl": root,
		"a.xsd":        schemaA,
		"b.xsd":        schemaB,
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	if !strings.Contains(body, `xmlns:tns="urn:schema:b"`) || !strings.Contains(body, "fromB") || strings.Contains(body, "fromA") {
		t.Fatalf("SOAP body did not retain imported QName ownership:\n%s", body)
	}
}

func TestConvertWSDLPropagatesNamespaceThroughTransitiveChameleonIncludes(t *testing.T) {
	const root = `<?xml version="1.0"?>
<definitions targetNamespace="urn:chameleon" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:tns="urn:chameleon"
 xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types><xsd:schema targetNamespace="urn:chameleon" elementFormDefault="qualified">
  <xsd:include schemaLocation="a.xsd"/>
  <xsd:element name="Request" type="tns:RequestType"/>
 </xsd:schema></types>
 <message name="RequestMessage"><part name="parameters" element="tns:Request"/></message>
 <portType name="Port"><operation name="Call"><input message="tns:RequestMessage"/></operation></portType>
 <binding name="Binding" type="tns:Port"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:Call"/><input><soap:body use="literal"/></input></operation></binding>
 <service name="Service"><port name="Port" binding="tns:Binding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`
	const schemaA = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema"><xsd:include schemaLocation="b.xsd"/></xsd:schema>`
	const schemaB = `<xsd:schema xmlns:xsd="http://www.w3.org/2001/XMLSchema" elementFormDefault="qualified"><xsd:complexType name="RequestType"><xsd:sequence><xsd:element name="fromTransitiveInclude" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:schema>`

	result, err := Convert(ImportRequest{RootFile: "service.wsdl", Files: map[string]string{
		"service.wsdl": root,
		"a.xsd":        schemaA,
		"b.xsd":        schemaB,
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	if !strings.Contains(body, `<tns:fromTransitiveInclude>`) || !strings.Contains(body, `xmlns:tns="urn:chameleon"`) {
		t.Fatalf("SOAP body did not resolve the transitive chameleon type in the including namespace:\n%s", body)
	}
}

func TestConvertWSDLResolvesImportedDefinitionQNamesByOwnerNamespace(t *testing.T) {
	const root = `<?xml version="1.0"?>
<definitions targetNamespace="urn:root" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/">
 <import namespace="urn:contract:b" location="b.wsdl"/>
 <import namespace="urn:contract:a" location="a.wsdl"/>
 <service name="Service"><port name="Port" xmlns:a="urn:contract:a" binding="a:SharedBinding"><soap:address location="https://example.test/soap"/></port></service>
</definitions>`
	const contractA = `<?xml version="1.0"?>
<definitions targetNamespace="urn:contract:a" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types><xsd:schema targetNamespace="urn:contract:a" elementFormDefault="qualified"><xsd:element name="Request"><xsd:complexType><xsd:sequence><xsd:element name="rightContractField" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element></xsd:schema></types>
 <message name="SharedMessage"><part name="parameters" xmlns:a="urn:contract:a" element="a:Request"/></message>
 <portType name="SharedPortType"><operation name="Call"><input xmlns:a="urn:contract:a" message="a:SharedMessage"/></operation></portType>
 <binding name="SharedBinding" xmlns:a="urn:contract:a" type="a:SharedPortType"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:a:Call"/><input><soap:body use="literal"/></input></operation></binding>
</definitions>`
	const contractB = `<?xml version="1.0"?>
<definitions targetNamespace="urn:contract:b" xmlns="http://schemas.xmlsoap.org/wsdl/"
 xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:b="urn:contract:b" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
 <types><xsd:schema targetNamespace="urn:contract:b" elementFormDefault="qualified"><xsd:element name="Request"><xsd:complexType><xsd:sequence><xsd:element name="wrongContractField" type="xsd:string"/></xsd:sequence></xsd:complexType></xsd:element></xsd:schema></types>
 <message name="SharedMessage"><part name="parameters" element="b:Request"/></message>
 <portType name="SharedPortType"><operation name="Call"><input message="b:SharedMessage"/></operation></portType>
 <binding name="SharedBinding" type="b:SharedPortType"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Call"><soap:operation soapAction="urn:b:Call"/><input><soap:body use="literal"/></input></operation></binding>
</definitions>`

	result, err := Convert(ImportRequest{RootFile: "service.wsdl", Files: map[string]string{
		"service.wsdl": root,
		"a.wsdl":       contractA,
		"b.wsdl":       contractB,
	}})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Script.Actions[0].Body
	if !strings.Contains(body, "rightContractField") || strings.Contains(body, "wrongContractField") {
		t.Fatalf("SOAP body resolved same-local imported WSDL declarations through the wrong namespace:\n%s", body)
	}
	if soap := result.Script.Actions[0].SOAP; soap == nil || soap.Action != "urn:a:Call" {
		t.Fatalf("SOAP config = %#v, want imported contract A action", soap)
	}
}

func TestConvertWSDLFallsBackForUniqueUndeclaredWSDLPrefix(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `<definitions targetNamespace="urn:accounts"
 xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/">
 <portType name="AccountPortType"><operation name="GetAccount"/></portType>
 <binding name="AccountBinding" type="tns:AccountPortType"><operation name="GetAccount"><soap:operation soapAction="urn:GetAccount"/></operation></binding>
 <service name="AccountService"><port name="AccountPort"><soap:address location="https://soap.example.test/account"/></port></service>
</definitions>`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %d, want one operation", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Target; got != "https://soap.example.test/account" {
		t.Fatalf("target = %q, want service endpoint retained through unique local-name fallback", got)
	}
}
