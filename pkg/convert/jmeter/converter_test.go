package jmeter

import (
	"testing"
	"time"
)

const sampleJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Checkout Plan" enabled="true">
      <elementProp name="TestPlan.user_defined_variables" elementType="Arguments">
        <collectionProp name="Arguments.arguments">
          <elementProp name="tenant" elementType="Argument">
            <stringProp name="Argument.name">tenant</stringProp>
            <stringProp name="Argument.value">blue</stringProp>
          </elementProp>
        </collectionProp>
      </elementProp>
    </TestPlan>
    <hashTree>
      <ThreadGroup testname="Checkout Users" enabled="true">
        <stringProp name="ThreadGroup.num_threads">25</stringProp>
        <stringProp name="ThreadGroup.ramp_time">30</stringProp>
        <stringProp name="ThreadGroup.duration">120</stringProp>
      </ThreadGroup>
      <hashTree>
        <ConfigTestElement testname="HTTP Request Defaults" guiclass="HttpDefaultsGui" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
        </ConfigTestElement>
        <hashTree/>
        <HeaderManager testname="Headers" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Tenant</stringProp>
              <stringProp name="Header.value">${tenant}</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <CSVDataSet testname="Customer CSV" enabled="true">
          <stringProp name="filename">customers.csv</stringProp>
          <stringProp name="variableNames">customer_id,zip</stringProp>
          <stringProp name="delimiter">,</stringProp>
        </CSVDataSet>
        <hashTree/>
        <ConstantTimer testname="Think Time" enabled="true">
          <stringProp name="ConstantTimer.delay">1500</stringProp>
        </ConstantTimer>
        <hashTree/>
        <ResponseAssertion testname="Assert OK" enabled="true">
          <collectionProp name="Asserion.test_strings">
            <stringProp name="0">success</stringProp>
          </collectionProp>
        </ResponseAssertion>
        <hashTree/>
        <HTTPSamplerProxy testname="Get Customer" enabled="true">
          <stringProp name="HTTPSampler.path">/v1/customer/${customer_id}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

const sampleExtractorJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Extractor Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Auth Users" enabled="true">
        <stringProp name="ThreadGroup.num_threads">5</stringProp>
      </ThreadGroup>
      <hashTree>
        <HTTPSamplerProxy testname="Create Session" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/session</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
        </HTTPSamplerProxy>
        <hashTree>
          <RegexExtractor testname="Session Token Regex" enabled="true">
            <stringProp name="RegexExtractor.refname">session_token</stringProp>
            <stringProp name="RegexExtractor.regex">"token":"([^"]+)"</stringProp>
          </RegexExtractor>
          <hashTree/>
					<RegexExtractor testname="Request Id Header Regex" enabled="true">
						<stringProp name="RegexExtractor.useHeaders">true</stringProp>
						<stringProp name="RegexExtractor.refname">request_id</stringProp>
						<stringProp name="RegexExtractor.regex">X-Request-Id: ([^\r\n]+)</stringProp>
					</RegexExtractor>
					<hashTree/>
          <JSONPostProcessor testname="Access Token JSON" enabled="true">
            <stringProp name="JSONPostProcessor.referenceNames">access_token</stringProp>
            <stringProp name="JSONPostProcessor.jsonPathExprs">$.access_token</stringProp>
          </JSONPostProcessor>
          <hashTree/>
					<JMESPathExtractor testname="Primary Role JMESPath" enabled="true">
						<stringProp name="JMESExtractor.referenceName">primary_role</stringProp>
						<stringProp name="JMESExtractor.jmesPathExpr">roles[0].name</stringProp>
					</JMESPathExtractor>
					<hashTree/>
					<HtmlExtractor testname="Login Link CSS" enabled="true">
						<stringProp name="HtmlExtractor.refname">login_link</stringProp>
						<stringProp name="HtmlExtractor.expr">a.login</stringProp>
						<stringProp name="HtmlExtractor.attribute">href</stringProp>
					</HtmlExtractor>
					<hashTree/>
					<XPathExtractor testname="Page Title XPath" enabled="true">
						<stringProp name="XPathExtractor.refname">page_title</stringProp>
						<stringProp name="XPathExtractor.xpathQuery">//title/text()</stringProp>
					</XPathExtractor>
					<hashTree/>
					<BoundaryExtractor testname="Session Id Boundary" enabled="true">
						<stringProp name="BoundaryExtractor.refname">session_id</stringProp>
						<stringProp name="BoundaryExtractor.lboundary">sessionId=</stringProp>
						<stringProp name="BoundaryExtractor.rboundary">&amp;</stringProp>
					</BoundaryExtractor>
					<hashTree/>
					<BoundaryExtractor testname="Trace Id Header Boundary" enabled="true">
						<stringProp name="BoundaryExtractor.useHeaders">true</stringProp>
						<stringProp name="BoundaryExtractor.refname">trace_id</stringProp>
						<stringProp name="BoundaryExtractor.lboundary">Set-Cookie: trace_id=</stringProp>
						<stringProp name="BoundaryExtractor.rboundary">;</stringProp>
					</BoundaryExtractor>
					<hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

func TestConvertJMXHTTPPlan(t *testing.T) {
	result, err := ConvertJMX([]byte(sampleJMX), ConvertOptions{
		FileName:          "checkout.jmx",
		IncludeAssertions: true,
		ScenarioDraft:     true,
	})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	script := result.Scripts[0]
	if script.Name != "Checkout_Users" {
		t.Fatalf("script name = %q, want Checkout_Users", script.Name)
	}
	if script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("base_url = %q", script.Config.BaseURL)
	}
	if len(script.DataSources) != 1 || script.DataSources[0].Path != "customers.csv" {
		t.Fatalf("unexpected data sources: %#v", script.DataSources)
	}
	if len(script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(script.Actions))
	}
	action := script.Actions[0]
	if action.Target != "/v1/customer/{{var.customer_id}}" {
		t.Fatalf("target = %q", action.Target)
	}
	if action.Headers["X-Tenant"] != "{{var.tenant}}" {
		t.Fatalf("X-Tenant header = %q", action.Headers["X-Tenant"])
	}
	if len(action.Assertions) != 2 {
		t.Fatalf("assertions = %d, want body_contains + status_code", len(action.Assertions))
	}
	if action.ThinkTime.MeanMs != 1500 || !action.ThinkTimeAfter {
		t.Fatalf("unexpected think time: %#v after=%v", action.ThinkTime, action.ThinkTimeAfter)
	}
	if result.ScenarioDraft == nil || len(result.ScenarioDraft.Groups) != 1 {
		t.Fatalf("expected scenario draft, got %#v", result.ScenarioDraft)
	}
	if result.ScenarioDraft.Groups[0].VUsers != 25 || result.ScenarioDraft.Groups[0].RampUp != "30s" {
		t.Fatalf("unexpected scenario group: %#v", result.ScenarioDraft.Groups[0])
	}
	if result.Summary.FileName != "checkout.jmx" || result.Stats.ConvertedActions != 1 || result.Stats.CSVSources != 1 {
		t.Fatalf("unexpected summary/stats: %#v %#v", result.Summary, result.Stats)
	}
	if script.GeneratedBy != "jmeter-import" || script.GeneratedFrom != "jmeter" {
		t.Fatalf("unexpected generator provenance: %q %q", script.GeneratedBy, script.GeneratedFrom)
	}
	if script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatalf("expected import metadata, got %#v", script.Metadata)
	}
	preserved := script.Metadata.Import.Preserved
	if preserved["original_file_name"] != "checkout.jmx" {
		t.Fatalf("original_file_name = %#v, want checkout.jmx", preserved["original_file_name"])
	}
	if preserved["warning_count"] != 0 {
		t.Fatalf("warning_count = %#v, want 0", preserved["warning_count"])
	}
	if preserved["unsupported_count"] != 0 {
		t.Fatalf("unsupported_count = %#v, want 0", preserved["unsupported_count"])
	}
	importedAt, ok := preserved["imported_at"].(string)
	if !ok || importedAt == "" {
		t.Fatalf("imported_at = %#v, want non-empty string", preserved["imported_at"])
	}
	if _, err := time.Parse(time.RFC3339, importedAt); err != nil {
		t.Fatalf("imported_at parse error: %v", err)
	}
}

func TestConvertJMXRejectsInvalidXML(t *testing.T) {
	if _, err := ConvertJMX([]byte(`<jmeterTestPlan>`), ConvertOptions{}); err == nil {
		t.Fatal("expected invalid XML to fail")
	}
}

func TestConvertJMXSamplerExtractors(t *testing.T) {
	result, err := ConvertJMX([]byte(sampleExtractorJMX), ConvertOptions{FileName: "extractors.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	action := result.Scripts[0].Actions[0]
	if len(action.Extractors) != 8 {
		t.Fatalf("extractors = %d, want 8", len(action.Extractors))
	}
	if action.Extractors[0].Name != "session_token" || action.Extractors[0].Type != "regex" {
		t.Fatalf("unexpected regex extractor: %#v", action.Extractors[0])
	}
	if action.Extractors[1].Name != "request_id" || action.Extractors[1].Source != "header" || action.Extractors[1].Type != "regex" || action.Extractors[1].Expression != `X-Request-Id: ([^\r\n]+)` {
		t.Fatalf("unexpected header regex extractor: %#v", action.Extractors[1])
	}
	if action.Extractors[2].Name != "access_token" || action.Extractors[2].Type != "jsonpath" || action.Extractors[2].Expression != "$.access_token" {
		t.Fatalf("unexpected JSON extractor: %#v", action.Extractors[2])
	}
	if action.Extractors[3].Name != "primary_role" || action.Extractors[3].Source != "body" || action.Extractors[3].Type != "jmespath" || action.Extractors[3].Expression != "roles[0].name" {
		t.Fatalf("unexpected JMESPath extractor: %#v", action.Extractors[3])
	}
	if action.Extractors[4].Name != "login_link" || action.Extractors[4].Source != "body" || action.Extractors[4].Type != "css" || action.Extractors[4].Expression != "a.login|||href" {
		t.Fatalf("unexpected CSS extractor: %#v", action.Extractors[4])
	}
	if action.Extractors[5].Name != "page_title" || action.Extractors[5].Source != "body" || action.Extractors[5].Type != "xpath" || action.Extractors[5].Expression != "//title/text()" {
		t.Fatalf("unexpected XPath extractor: %#v", action.Extractors[5])
	}
	if action.Extractors[6].Name != "session_id" || action.Extractors[6].Source != "body" || action.Extractors[6].Type != "boundary" || action.Extractors[6].Expression != "sessionId=|||&" {
		t.Fatalf("unexpected boundary extractor: %#v", action.Extractors[6])
	}
	if action.Extractors[7].Name != "trace_id" || action.Extractors[7].Source != "header" || action.Extractors[7].Type != "boundary" || action.Extractors[7].Expression != "Set-Cookie: trace_id=|||;" {
		t.Fatalf("unexpected header boundary extractor: %#v", action.Extractors[7])
	}
}
