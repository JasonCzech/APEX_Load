package jmeter

import (
	"strings"
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
					<XPath2Extractor testname="Page Title XPath2" enabled="true">
						<stringProp name="XPathExtractor2.refname">page_title_xpath2</stringProp>
						<stringProp name="XPathExtractor2.xpathQuery">//title/text()</stringProp>
					</XPath2Extractor>
					<hashTree/>
					<XPath2Extractor testname="Namespaced Page Title XPath2" enabled="true">
						<stringProp name="XPathExtractor2.refname">page_title_xpath2_ns</stringProp>
						<stringProp name="XPathExtractor2.xpathQuery">/age:ag/hd:head/title</stringProp>
						<stringProp name="XPathExtractor2.namespaces">age=http://www.w3.org/wgs84_pos#
hd=http://www.w3.org/wgs85_pos#</stringProp>
					</XPath2Extractor>
					<hashTree/>
						<XPath2Extractor testname="Main Section Fragment XPath2" enabled="true">
							<stringProp name="XPathExtractor2.refname">main_section_fragment</stringProp>
							<stringProp name="XPathExtractor2.xpathQuery">//section[@id='main']</stringProp>
							<stringProp name="XPathExtractor2.fragment">true</stringProp>
						</XPath2Extractor>
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

const sampleSOAPJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
	<hashTree>
		<TestPlan testname="SOAP Plan" enabled="true"/>
		<hashTree>
			<ThreadGroup testname="SOAP Users" enabled="true">
				<stringProp name="ThreadGroup.num_threads">3</stringProp>
			</ThreadGroup>
			<hashTree>
				<HeaderManager testname="SOAP Headers" enabled="true">
					<collectionProp name="HeaderManager.headers">
						<elementProp name="" elementType="Header">
							<stringProp name="Header.name">Content-Type</stringProp>
							<stringProp name="Header.value">text/xml; charset=utf-8</stringProp>
						</elementProp>
						<elementProp name="" elementType="Header">
							<stringProp name="Header.name">SOAPAction</stringProp>
							<stringProp name="Header.value">urn:GetQuote</stringProp>
						</elementProp>
					</collectionProp>
				</HeaderManager>
				<hashTree/>
				<HTTPSamplerProxy testname="Get Quote" enabled="true">
					<stringProp name="HTTPSampler.domain">example.com</stringProp>
					<stringProp name="HTTPSampler.protocol">https</stringProp>
					<stringProp name="HTTPSampler.path">/services/quotes</stringProp>
					<stringProp name="HTTPSampler.method">POST</stringProp>
					<boolProp name="HTTPSampler.postBodyRaw">true</boolProp>
					<elementProp name="HTTPsampler.Arguments" elementType="Arguments">
						<collectionProp name="Arguments.arguments">
							<elementProp name="" elementType="HTTPArgument">
								<stringProp name="Argument.value">&lt;soapenv:Envelope xmlns:soapenv=&quot;http://schemas.xmlsoap.org/soap/envelope/&quot;&gt;&lt;soapenv:Body&gt;&lt;GetQuote xmlns=&quot;urn:quotes&quot;&gt;&lt;Symbol&gt;STORM&lt;/Symbol&gt;&lt;/GetQuote&gt;&lt;/soapenv:Body&gt;&lt;/soapenv:Envelope&gt;</stringProp>
							</elementProp>
						</collectionProp>
					</elementProp>
				</HTTPSamplerProxy>
				<hashTree/>
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

func TestConvertJMXResponseAssertionFieldsAndDynamicFunctions(t *testing.T) {
	const assertionJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Assertion Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <ResponseAssertion testname="Created Status" enabled="true">
          <stringProp name="Assertion.test_field">Assertion.response_code</stringProp>
          <intProp name="Assertion.test_type">8</intProp>
          <collectionProp name="Assertion.test_strings">
            <stringProp name="0">201</stringProp>
          </collectionProp>
        </ResponseAssertion>
        <hashTree/>
        <ResponseAssertion testname="Body Missing Error" enabled="true">
          <stringProp name="Assertion.test_field">Assertion.response_data</stringProp>
          <intProp name="Assertion.test_type">6</intProp>
          <collectionProp name="Assertion.test_strings">
            <stringProp name="0">error</stringProp>
          </collectionProp>
        </ResponseAssertion>
        <hashTree/>
        <ResponseAssertion testname="Header Assertion" enabled="true">
          <stringProp name="Assertion.test_field">Assertion.response_headers</stringProp>
          <collectionProp name="Assertion.test_strings">
            <stringProp name="0">X-Trace</stringProp>
          </collectionProp>
        </ResponseAssertion>
        <hashTree/>
        <HTTPSamplerProxy testname="Create Order" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/orders/${__UUID()}?ts=${__time()}&amp;tenant=${tenant}</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
	result, err := ConvertJMX([]byte(assertionJMX), ConvertOptions{FileName: "assertions.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	if action.Target != "/orders/{{fn.uuid()}}?ts={{fn.timestamp.unix_ms}}&tenant={{var.tenant}}" {
		t.Fatalf("target = %q", action.Target)
	}
	if len(action.Assertions) != 2 {
		t.Fatalf("assertions = %#v, want status_code and body_not_contains", action.Assertions)
	}
	if action.Assertions[0].Type != "status_code" || action.Assertions[0].Expected != 201 {
		t.Fatalf("first assertion = %#v", action.Assertions[0])
	}
	if action.Assertions[1].Type != "body_not_contains" || action.Assertions[1].Value != "error" {
		t.Fatalf("second assertion = %#v", action.Assertions[1])
	}
	if !containsJMeterDiagnostic(result, "response_headers") {
		t.Fatalf("diagnostics = %#v, want response_headers warning", result.Unsupported)
	}
}

func TestConvertJMXAdvancedSamplerFidelity(t *testing.T) {
	const advancedJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Advanced Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <AuthManager testname="Auth" enabled="true">
          <collectionProp name="AuthManager.auth_list">
            <elementProp name="" elementType="Authorization">
              <stringProp name="Authorization.url"></stringProp>
              <stringProp name="Authorization.username">api-user</stringProp>
              <stringProp name="Authorization.password">api-pass</stringProp>
              <stringProp name="Authorization.mechanism">BASIC</stringProp>
            </elementProp>
            <elementProp name="" elementType="Authorization">
              <stringProp name="Authorization.url">https://api.example.com/private</stringProp>
              <stringProp name="Authorization.username">digest-user</stringProp>
              <stringProp name="Authorization.password">digest-pass</stringProp>
              <stringProp name="Authorization.mechanism">DIGEST</stringProp>
            </elementProp>
          </collectionProp>
        </AuthManager>
        <hashTree/>
        <GaussianRandomTimer testname="Gaussian Think" enabled="true">
          <stringProp name="GaussianRandomTimer.delay">1000</stringProp>
          <stringProp name="GaussianRandomTimer.range">400</stringProp>
        </GaussianRandomTimer>
        <hashTree/>
        <TransactionController testname="Checkout" enabled="true">
          <boolProp name="TransactionController.includeTimers">false</boolProp>
        </TransactionController>
        <hashTree>
          <HTTPSamplerProxy testname="Upload Receipt" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/private/upload</stringProp>
            <stringProp name="HTTPSampler.method">POST</stringProp>
            <stringProp name="HTTPSampler.connect_timeout">5000</stringProp>
            <stringProp name="HTTPSampler.response_timeout">15000</stringProp>
            <boolProp name="HTTPSampler.follow_redirects">false</boolProp>
            <elementProp name="HTTPsampler.Files" elementType="HTTPFileArgs">
              <collectionProp name="HTTPFileArgs.files">
                <elementProp name="" elementType="HTTPFileArg">
                  <stringProp name="File.path">/tmp/receipt.pdf</stringProp>
                  <stringProp name="File.paramname">receipt</stringProp>
                  <stringProp name="File.mimetype">application/pdf</stringProp>
                </elementProp>
              </collectionProp>
            </elementProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
	result, err := ConvertJMX([]byte(advancedJMX), ConvertOptions{FileName: "advanced.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	if script.Config.HTTP.Auth.Type != "basic" || script.Config.HTTP.Auth.Username != "api-user" {
		t.Fatalf("global auth = %#v", script.Config.HTTP.Auth)
	}
	action := script.Actions[0]
	if action.Name != "Checkout_Upload_Receipt" {
		t.Fatalf("action name = %q", action.Name)
	}
	if action.Metadata["transaction"] != "Checkout" {
		t.Fatalf("metadata = %#v, want transaction", action.Metadata)
	}
	if action.Auth == nil || action.Auth.Type != "digest" || action.Auth.Username != "digest-user" {
		t.Fatalf("action auth = %#v", action.Auth)
	}
	if action.TimeoutMs != 15000 {
		t.Fatalf("timeout = %d, want 15000", action.TimeoutMs)
	}
	if action.MaxRedirects == nil || *action.MaxRedirects != 0 {
		t.Fatalf("max redirects = %#v, want 0", action.MaxRedirects)
	}
	if action.ThinkTime.MeanMs != 1200 || !action.ThinkTimeAfter {
		t.Fatalf("think time = %#v after=%v", action.ThinkTime, action.ThinkTimeAfter)
	}
	upload, ok := action.Params["receipt"].(map[string]any)
	if !ok || upload["value"] != "{{file.receipt}}" || upload["content_type"] != "application/pdf" {
		t.Fatalf("upload params = %#v", action.Params)
	}
	if !containsJMeterDiagnostic(result, "JMeter HTTPFileArg") {
		t.Fatalf("diagnostics = %#v, want HTTPFileArg warning", result.Unsupported)
	}
}

func TestConvertJMXThroughputTimerApproximatesThinkTime(t *testing.T) {
	const throughputJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Throughput Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <ConstantThroughputTimer testname="One Per Second" enabled="true">
          <doubleProp><name>throughput</name><value>60</value><savedValue>0.0</savedValue></doubleProp>
        </ConstantThroughputTimer>
        <hashTree/>
        <HTTPSamplerProxy testname="Ping" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/ping</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
	result, err := ConvertJMX([]byte(throughputJMX), ConvertOptions{FileName: "throughput.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	if action.ThinkTime.MeanMs != 1000 || !action.ThinkTimeAfter {
		t.Fatalf("think time = %#v after=%v, want fixed 1000ms", action.ThinkTime, action.ThinkTimeAfter)
	}
	if !containsJMeterDiagnostic(result, "ConstantThroughputTimer") {
		t.Fatalf("diagnostics = %#v, want throughput approximation info", result.Unsupported)
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
	if len(action.Extractors) != 11 {
		t.Fatalf("extractors = %d, want 11", len(action.Extractors))
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
	if action.Extractors[6].Name != "page_title_xpath2" || action.Extractors[6].Source != "body" || action.Extractors[6].Type != "xpath" || action.Extractors[6].Expression != "//title/text()" {
		t.Fatalf("unexpected XPath2 extractor: %#v", action.Extractors[6])
	}
	expectedNamespacedXPath2 := encodeXPathExpression("/age:ag/hd:head/title", false, map[string]string{
		"age": "http://www.w3.org/wgs84_pos#",
		"hd":  "http://www.w3.org/wgs85_pos#",
	})
	if action.Extractors[7].Name != "page_title_xpath2_ns" || action.Extractors[7].Source != "body" || action.Extractors[7].Type != "xpath" || action.Extractors[7].Expression != expectedNamespacedXPath2 {
		t.Fatalf("unexpected namespaced XPath2 extractor: %#v", action.Extractors[7])
	}
	if action.Extractors[8].Name != "main_section_fragment" || action.Extractors[8].Source != "body" || action.Extractors[8].Type != "xpath" || action.Extractors[8].Expression != xpathFragmentExpressionPrefix+"//section[@id='main']" {
		t.Fatalf("unexpected XPath2 fragment extractor: %#v", action.Extractors[8])
	}
	if action.Extractors[9].Name != "session_id" || action.Extractors[9].Source != "body" || action.Extractors[9].Type != "boundary" || action.Extractors[9].Expression != "sessionId=|||&" {
		t.Fatalf("unexpected boundary extractor: %#v", action.Extractors[9])
	}
	if action.Extractors[10].Name != "trace_id" || action.Extractors[10].Source != "header" || action.Extractors[10].Type != "boundary" || action.Extractors[10].Expression != "Set-Cookie: trace_id=|||;" {
		t.Fatalf("unexpected header boundary extractor: %#v", action.Extractors[10])
	}
}

func TestConvertJMXSOAPHTTPPlan(t *testing.T) {
	result, err := ConvertJMX([]byte(sampleSOAPJMX), ConvertOptions{FileName: "soap.jmx", IncludeAssertions: true})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	script := result.Scripts[0]
	if script.Protocol != "soap" {
		t.Fatalf("protocol = %q, want soap", script.Protocol)
	}
	if script.Config.SOAP.Version != "1.1" {
		t.Fatalf("soap version = %q, want 1.1", script.Config.SOAP.Version)
	}
	if !script.Config.SOAP.FaultHandling.FailOnFault {
		t.Fatal("expected fail_on_fault to be enabled")
	}
	if len(script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(script.Actions))
	}
	action := script.Actions[0]
	if action.SOAP == nil {
		t.Fatal("expected SOAP action metadata to be present")
	}
	if action.SOAP.Action != "urn:GetQuote" {
		t.Fatalf("soap action = %q, want urn:GetQuote", action.SOAP.Action)
	}
	if action.SOAP.BodyMode != "raw_xml" {
		t.Fatalf("body mode = %q, want raw_xml", action.SOAP.BodyMode)
	}
	if action.Headers["SOAPAction"] != "urn:GetQuote" {
		t.Fatalf("SOAPAction header = %q, want urn:GetQuote", action.Headers["SOAPAction"])
	}
	if !strings.Contains(action.Headers["Content-Type"], "text/xml") {
		t.Fatalf("content type = %q, want text/xml", action.Headers["Content-Type"])
	}
	if action.Target != "/services/quotes" {
		t.Fatalf("target = %q, want /services/quotes", action.Target)
	}
}

func TestConvertJMXXPathExtractorFragment(t *testing.T) {
	const sampleXPathFragmentJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="XPath Fragment Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Auth Users" enabled="true">
        <stringProp name="ThreadGroup.num_threads">1</stringProp>
      </ThreadGroup>
      <hashTree>
        <HTTPSamplerProxy testname="Create Session" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/session</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
        </HTTPSamplerProxy>
        <hashTree>
          <XPathExtractor testname="Page Fragment XPath" enabled="true">
            <stringProp name="XPathExtractor.refname">page_fragment</stringProp>
            <stringProp name="XPathExtractor.xpathQuery">//page</stringProp>
            <stringProp name="XPathExtractor.fragment">true</stringProp>
          </XPathExtractor>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(sampleXPathFragmentJMX), ConvertOptions{FileName: "extractors.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	if len(action.Extractors) != 1 {
		t.Fatalf("extractors = %d, want 1", len(action.Extractors))
	}
	if action.Extractors[0].Name != "page_fragment" || action.Extractors[0].Expression != xpathFragmentExpressionPrefix+"//page" {
		t.Fatalf("unexpected XPath fragment extractor: %#v", action.Extractors[0])
	}
}

func TestConvertJMXXPath2NamespacesSupported(t *testing.T) {
	const sampleXPath2NamespacedJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="XPath2 Namespaced Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Auth Users" enabled="true">
        <stringProp name="ThreadGroup.num_threads">1</stringProp>
      </ThreadGroup>
      <hashTree>
        <HTTPSamplerProxy testname="Create Session" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/session</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
        </HTTPSamplerProxy>
        <hashTree>
          <XPath2Extractor testname="Namespaced XPath2" enabled="true">
            <stringProp name="XPathExtractor2.refname">page_title_xpath2</stringProp>
						<stringProp name="XPathExtractor2.xpathQuery">/age:ag/hd:head/title</stringProp>
						<stringProp name="XPathExtractor2.namespaces">age=http://www.w3.org/wgs84_pos#
hd=http://www.w3.org/wgs85_pos#</stringProp>
          </XPath2Extractor>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(sampleXPath2NamespacedJMX), ConvertOptions{FileName: "extractors.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	if len(action.Extractors) != 1 {
		t.Fatalf("extractors = %d, want 1", len(action.Extractors))
	}
	expected := encodeXPathExpression("/age:ag/hd:head/title", false, map[string]string{
		"age": "http://www.w3.org/wgs84_pos#",
		"hd":  "http://www.w3.org/wgs85_pos#",
	})
	if action.Extractors[0].Expression != expected {
		t.Fatalf("unexpected namespaced XPath2 encoding: %#v", action.Extractors[0])
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	if action.Extractors[0].Name != "page_title_xpath2" || action.Extractors[0].Type != "xpath" {
		t.Fatalf("unexpected namespaced XPath2 extractor: %#v", action.Extractors[0])
	}
}

func TestConvertJMXXPath2InvalidNamespacesWarn(t *testing.T) {
	const sampleXPath2InvalidNamespacesJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="XPath2 Invalid Namespaces Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Auth Users" enabled="true">
        <stringProp name="ThreadGroup.num_threads">1</stringProp>
      </ThreadGroup>
      <hashTree>
        <HTTPSamplerProxy testname="Create Session" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/session</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
        </HTTPSamplerProxy>
        <hashTree>
          <XPath2Extractor testname="Broken Namespaces XPath2" enabled="true">
            <stringProp name="XPathExtractor2.refname">page_title_xpath2</stringProp>
            <stringProp name="XPathExtractor2.xpathQuery">/age:ag/head/title</stringProp>
            <stringProp name="XPathExtractor2.namespaces">age</stringProp>
          </XPath2Extractor>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(sampleXPath2InvalidNamespacesJMX), ConvertOptions{FileName: "extractors.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	if len(action.Extractors) != 0 {
		t.Fatalf("extractors = %d, want 0", len(action.Extractors))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %#v, want 1 warning", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "namespace bindings were invalid") {
		t.Fatalf("unexpected warning detail: %#v", result.Warnings)
	}
	if !containsJMeterDiagnostic(result, "JMeter XPath2Extractor") {
		t.Fatalf("diagnostics = %#v, want XPath2Extractor warning", result.Unsupported)
	}
}

func TestConvertJMXReportsFlattenedControllersAndScriptSamplers(t *testing.T) {
	const scriptedJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Scripted Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <IfController testname="Only Premium Users" enabled="true">
          <stringProp name="IfController.condition">${isPremium}</stringProp>
        </IfController>
        <hashTree>
          <HTTPSamplerProxy testname="Get Premium Offer" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/premium</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>
        <JSR223Sampler testname="Sign Request" enabled="true">
          <stringProp name="scriptLanguage">groovy</stringProp>
          <stringProp name="script">vars.put("sig", "abc")</stringProp>
        </JSR223Sampler>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(scriptedJMX), ConvertOptions{FileName: "scripted.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
	}
	if result.Stats.TotalSamplers != 2 || result.Stats.SkippedSamplers != 1 {
		t.Fatalf("sampler accounting = total %d skipped %d, want total 2 skipped 1", result.Stats.TotalSamplers, result.Stats.SkippedSamplers)
	}
	if !containsJMeterDiagnostic(result, "JMeter IfController") {
		t.Fatalf("diagnostics = %#v, want IfController warning", result.Unsupported)
	}
	if !containsJMeterDiagnostic(result, "JMeter JSR223Sampler") {
		t.Fatalf("diagnostics = %#v, want JSR223Sampler warning", result.Unsupported)
	}
}

func containsJMeterDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}
