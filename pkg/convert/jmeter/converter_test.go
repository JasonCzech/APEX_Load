package jmeter

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
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
	if len(script.DataSources) != 1 ||
		script.DataSources[0].Path != "" ||
		script.DataSources[0].FileName != "customers.csv" ||
		script.DataSources[0].InlineData != requestimport.MissingFilePlaceholder {
		t.Fatalf("unexpected data sources: %#v", script.DataSources)
	}
	if len(script.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(script.Actions))
	}
	action := script.Actions[0]
	if action.Target != "/v1/customer/{{var.customer_id}}" {
		t.Fatalf("target = %q", action.Target)
	}
	if action.Headers["X-Tenant"] != "blue" {
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
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 || len(result.SourceReview.Mappings) == 0 {
		t.Fatalf("source review = %#v, want files and mappings", result.SourceReview)
	}
	assertJMeterSourceMapping(t, result.SourceReview.Mappings, "HTTPSamplerProxy", "actions[0]", "review_only")
	assertJMeterSourceMapping(t, result.SourceReview.Mappings, "jmeter_assertion", "actions[0].assertions[0]", "contributor")
}

func TestConvertJMXCSVDataSetInfersHeadersFromSidecarFile(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="CSV Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <CSVDataSet testname="Customers" enabled="true">
          <stringProp name="filename">data/customers.csv</stringProp>
          <stringProp name="delimiter">,</stringProp>
        </CSVDataSet>
        <hashTree/>
        <HTTPSamplerProxy testname="Lookup" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/customers/${customer_id}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{
		FileName: "checkout.jmx",
		SidecarFiles: map[string][]byte{
			"data/customers.csv": []byte("customer_id,zip\n42,02139\n"),
		},
	})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	if len(script.DataSources) != 1 {
		t.Fatalf("data_sources = %#v, want one", script.DataSources)
	}
	source := script.DataSources[0]
	if !source.FirstRowHeader || source.InlineData == "" {
		t.Fatalf("source = %#v, want first-row header with preserved inline data", source)
	}
	if strings.Join(source.Columns, ",") != "customer_id,zip" {
		t.Fatalf("columns = %#v, want inferred customer_id,zip", source.Columns)
	}
	if script.Actions[0].Target != "/customers/{{var.customer_id}}" {
		t.Fatalf("target = %q, want converted CSV variable reference", script.Actions[0].Target)
	}
}

func assertJMeterSourceMapping(t *testing.T, mappings []sourceview.Mapping, function string, targetPath string, status string) {
	t.Helper()
	for _, mapping := range mappings {
		if mapping.Function == function && mapping.TargetPath == targetPath && mapping.Status == status {
			return
		}
	}
	t.Fatalf("missing source mapping function=%s target=%s status=%s in %#v", function, targetPath, status, mappings)
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
	if action.Auth == nil || action.Auth.Type != "digest" ||
		action.Auth.Username != "digest-user" || action.Auth.Password != "digest-pass" {
		t.Fatalf("action auth = %#v, want native digest credentials", action.Auth)
	}
	if containsJMeterDiagnostic(result, "Digest auth cannot be executed") {
		t.Fatalf("diagnostics = %#v, digest is now executable", result.Unsupported)
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

func TestConvertJMXMapsSimpleIfControllerAndReportsScriptSamplers(t *testing.T) {
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
	if result.Stats.ConvertedActions != 2 {
		t.Fatalf("converted actions = %d, want 2 (HTTP sampler + translated JSR223 sampler)", result.Stats.ConvertedActions)
	}
	// The IfController converts to control_if and the standalone JSR223 sampler's
	// safe vars.put("sig","abc") translates into a source_parameter action.
	actions := result.Scripts[0].Actions
	if len(actions) != 2 || actions[0].Type != "control_if" || actions[1].Type != "source_parameter" {
		t.Fatalf("actions = %#v, want control_if + source_parameter", actions)
	}
	control := actions[0].Control
	if control == nil || control.Condition == nil || control.Condition.Left != "{{var.isPremium}}" || control.Condition.Op != "exists" || len(control.Steps) != 1 {
		t.Fatalf("control = %#v, want exists condition with one child", control)
	}
	param := actions[1]
	if param.Params["function"] != "lr_save_string" {
		t.Fatalf("source_parameter function = %v, want lr_save_string", param.Params["function"])
	}
	args, _ := param.Params["args"].([]any)
	if len(args) != 2 || args[0] != "abc" || args[1] != "sig" {
		t.Fatalf("source_parameter args = %#v, want [abc sig]", param.Params["args"])
	}
	if _, copied := param.Metadata["jsr223_source"]; copied {
		t.Fatalf("translated action retained raw JSR223 source: %#v", param.Metadata)
	}
	// Both samplers produced executable actions, so neither is counted as skipped
	// (the JSR223 sampler's safe-subset translation is a successful conversion).
	if result.Stats.TotalSamplers != 2 || result.Stats.SkippedSamplers != 0 {
		t.Fatalf("sampler accounting = total %d skipped %d, want total 2 skipped 0", result.Stats.TotalSamplers, result.Stats.SkippedSamplers)
	}
	if !containsJMeterDiagnostic(result, "JMeter JSR223Sampler") {
		t.Fatalf("diagnostics = %#v, want JSR223Sampler translation note", result.Unsupported)
	}
	// The translated sampler is reported as a mapped conversion, not unsupported.
	for _, name := range result.UnsupportedElements {
		if name == "JSR223Sampler" {
			t.Fatalf("JSR223Sampler should not be listed unsupported once translated: %#v", result.UnsupportedElements)
		}
	}
}

func TestConvertJMXMapsLoopCacheManagerTestActionAndDebugSampler(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <CacheManager testname="Cache" enabled="true">
          <boolProp name="clearEachIteration">true</boolProp>
          <boolProp name="useExpires">false</boolProp>
        </CacheManager>
        <LoopController testname="Browse Twice" enabled="true">
          <stringProp name="LoopController.loops">2</stringProp>
        </LoopController>
        <hashTree>
          <TestAction testname="Pause" enabled="true">
            <intProp name="ActionProcessor.duration">750</intProp>
          </TestAction>
          <hashTree/>
          <HTTPSamplerProxy testname="Home" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/home</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
          <DebugSampler testname="Debug Vars" enabled="true"/>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "loop.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	if got := script.Config.WebRuntime.AdditionalAttributes["jmeter.cache_manager.clear_each_iteration"]; got != "true" {
		t.Fatalf("cache manager attributes = %#v", script.Config.WebRuntime.AdditionalAttributes)
	}
	parity, ok := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string)
	if !ok || parity["cacheManager"] != "partial" || parity["testActionPause"] != "executed" {
		t.Fatalf("runtime parity = %#v, want cacheManager partial and TestAction executed", script.Metadata.Import.Preserved["runtime_parity"])
	}
	if len(script.Actions) != 1 || script.Actions[0].Type != "control_loop" {
		t.Fatalf("actions = %#v, want one control_loop", script.Actions)
	}
	control := script.Actions[0].Control
	if control == nil || control.Count != 2 || len(control.Steps) != 1 {
		t.Fatalf("control = %#v, want count=2 with one child", control)
	}
	child := control.Steps[0]
	if child.Target != "/home" || !child.ThinkTimeAfter || child.ThinkTime.MeanMs != 750 {
		t.Fatalf("loop child = %#v, want /home with TestAction think time", child)
	}
	if containsJMeterDiagnostic(result, "JMeter DebugSampler") && len(result.UnsupportedElements) > 0 {
		t.Fatalf("debug sampler diagnostics/unsupported = %#v/%#v, want non-blocking diagnostic only", result.Unsupported, result.UnsupportedElements)
	}
}

func TestConvertJMXMapsOnceOnlyControllerToInit(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <OnceOnlyController testname="Login Once" enabled="true"/>
        <hashTree>
          <HTTPSamplerProxy testname="Login" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/login</stringProp>
            <stringProp name="HTTPSampler.method">POST</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>
        <HTTPSamplerProxy testname="Home" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "once-only.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	if len(script.Init) != 1 || script.Init[0].Target != "/login" || script.Init[0].Method != "POST" {
		t.Fatalf("init actions = %#v, want Login under OnceOnlyController", script.Init)
	}
	if len(script.Actions) != 1 || script.Actions[0].Target != "/home" {
		t.Fatalf("main actions = %#v, want Home outside OnceOnlyController", script.Actions)
	}
	parity, ok := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string)
	if !ok || parity["onceOnlyController"] != "executed" {
		t.Fatalf("runtime parity = %#v, want onceOnlyController executed", script.Metadata.Import.Preserved["runtime_parity"])
	}
	if result.Stats.ConvertedActions != 2 {
		t.Fatalf("converted actions = %d, want init + main samplers counted", result.Stats.ConvertedActions)
	}
	if result.SourceReview == nil || !containsJMeterMapping(result.SourceReview.Mappings, "OnceOnlyController", "init[0]", "converted") {
		t.Fatalf("source review = %#v, want OnceOnlyController init mapping", result.SourceReview)
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

func containsJMeterMapping(mappings []sourceview.Mapping, function string, targetPath string, status string) bool {
	for _, mapping := range mappings {
		if mapping.Function == function && mapping.TargetPath == targetPath && mapping.Status == status {
			return true
		}
	}
	return false
}

func TestConvertJMXMapsSimpleWhileControllerToConditionLoop(t *testing.T) {
	const whileJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Polling Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <WhileController testname="Poll Until Done" enabled="true">
          <stringProp name="WhileController.condition">${jobStatus} != done</stringProp>
        </WhileController>
        <hashTree>
          <HTTPSamplerProxy testname="Check Status" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/jobs/status</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(whileJMX), ConvertOptions{FileName: "while.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts[0].Actions) != 1 || result.Scripts[0].Actions[0].Type != "control_loop" {
		t.Fatalf("actions = %#v, want one control_loop", result.Scripts[0].Actions)
	}
	control := result.Scripts[0].Actions[0].Control
	if control == nil || control.Condition == nil {
		t.Fatalf("control = %#v, want condition-driven loop", control)
	}
	if control.Condition.Left != "{{var.jobStatus}}" || control.Condition.Op != "ne" || control.Condition.Right != "done" {
		t.Fatalf("condition = %#v, want jobStatus ne done", control.Condition)
	}
	if control.Count != 0 {
		t.Fatalf("count = %d, want 0 (condition-driven)", control.Count)
	}
	if control.MaxIterations != jmeterWhileLoopSafetyCap {
		t.Fatalf("max iterations = %d, want safety cap %d", control.MaxIterations, jmeterWhileLoopSafetyCap)
	}
	if len(control.Steps) != 1 || control.Steps[0].Target != "/jobs/status" {
		t.Fatalf("steps = %#v, want one nested status request", control.Steps)
	}
}

func TestConvertJMXFlattensDynamicWhileCondition(t *testing.T) {
	const dynamicWhileJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <WhileController testname="Scripted While" enabled="true">
          <stringProp name="WhileController.condition">${__javaScript(${count} &lt; 5)}</stringProp>
        </WhileController>
        <hashTree>
          <HTTPSamplerProxy testname="Step" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/step</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(dynamicWhileJMX), ConvertOptions{FileName: "dynamic-while.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	// Untranslatable condition: a durable placeholder precedes children flattened
	// in source order.
	actions := result.Scripts[0].Actions
	if len(actions) != 2 || actions[0].Type != "review_required" || actions[1].Type == "control_loop" {
		t.Fatalf("actions = %#v, want review placeholder + flattened request", actions)
	}
	if actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("dynamic while placeholder metadata = %#v", actions[0].Metadata)
	}
	if !containsJMeterDiagnostic(result, "JMeter WhileController") {
		t.Fatalf("diagnostics = %#v, want WhileController flattening warning", result.Unsupported)
	}
}

func TestConvertJMXMakesMultiThreadGroupNameOverrideUnique(t *testing.T) {
	const multiGroupJMX = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Readers" enabled="true"/><hashTree>
        <HTTPSamplerProxy testname="Read" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.path">/read</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy><hashTree/>
      </hashTree>
      <ThreadGroup testname="Writers" enabled="true"/><hashTree>
        <HTTPSamplerProxy testname="Write" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.path">/write</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
        </HTTPSamplerProxy><hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(multiGroupJMX), ConvertOptions{FileName: "multi.jmx", ScriptName: "Orders"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	if got := []string{result.Scripts[0].Name, result.Scripts[1].Name}; got[0] != "Orders_1" || got[1] != "Orders_2" {
		t.Fatalf("script names = %#v, want unique override names", got)
	}
	if result.ScenarioDraft != nil {
		t.Fatalf("scenario draft = %#v, want none without scenario option", result.ScenarioDraft)
	}
}

func TestConvertJMXPreservesHeaderManagerOccurrenceOrderAndCase(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <HeaderManager testname="Headers" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Trace</stringProp>
              <stringProp name="Header.value">one</stringProp>
            </elementProp>
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">x-trace</stringProp>
              <stringProp name="Header.value">two</stringProp>
            </elementProp>
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Trace</stringProp>
              <stringProp name="Header.value">three</stringProp>
            </elementProp>
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">Cookie</stringProp>
              <stringProp name="Header.value">session=abc</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/orders</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "headers.jmx", BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	wantNames := []string{"X-Trace", "x-trace", "X-Trace", "Cookie"}
	wantValues := []string{"one", "two", "three", "session=abc"}
	if len(action.HeaderList) != len(wantNames) {
		t.Fatalf("header list = %#v, want %d occurrences", action.HeaderList, len(wantNames))
	}
	for index := range wantNames {
		if action.HeaderList[index].Name != wantNames[index] || action.HeaderList[index].Value != wantValues[index] {
			t.Fatalf("header_list[%d] = %#v, want %s: %s", index, action.HeaderList[index], wantNames[index], wantValues[index])
		}
	}
	if len(action.Headers) != 2 || action.Headers["X-Trace"] != "three" || action.Headers["Cookie"] != "session=abc" {
		t.Fatalf("compatibility header map = %#v, want case-insensitive last occurrences", action.Headers)
	}
	if got := result.Scripts[0].Config.HTTP.DefaultHeaders; len(got) != 2 || got["X-Trace"] != "three" {
		t.Fatalf("default header map = %#v, want case-insensitive last occurrences", got)
	}
	if result.Stats.Headers != 4 {
		t.Fatalf("header stats = %d, want occurrence count 4", result.Stats.Headers)
	}
	if issues := dsl.ValidateScriptIssues(result.Scripts[0]); len(issues) != 0 {
		t.Fatalf("header-list draft validation issues = %v", issues)
	}
}

func TestConvertJMXRetainsInvalidHeaderBytesOnlyInGatedEnvelope(t *testing.T) {
	const (
		headerName  = "Authorization"
		headerValue = "Bearer jmeter-inert-header-canary-58c1\r\nX-Injected: yes"
	)
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <HeaderManager testname="Headers" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">Authorization</stringProp>
              <stringProp name="Header.value">Bearer jmeter-inert-header-canary-58c1&#13;&#10;X-Injected: yes</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/private</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX(
		[]byte(jmx),
		ConvertOptions{FileName: "invalid-header.jmx", BaseURL: "https://api.example.com"},
	)
	if err != nil {
		t.Fatalf("ConvertJMX: %v", err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	script := result.Scripts[0]
	action := script.Actions[0]
	if len(action.HeaderList) != 0 || len(action.Headers) != 0 ||
		len(script.Config.HTTP.DefaultHeaders) != 0 {
		t.Fatalf(
			"invalid header remained transport-active: action list %#v map %#v defaults %#v",
			action.HeaderList,
			action.Headers,
			script.Config.HTTP.DefaultHeaders,
		)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("invalid header draft was not gated: %#v", action.Metadata)
	}
	pairs := jmeterPreservedHeaderBytePairs(t, action)
	if len(pairs) == 0 {
		t.Fatal("no exact inert header occurrence was retained")
	}
	for _, pair := range pairs {
		if pair[0] != headerName || pair[1] != headerValue {
			t.Fatalf("preserved header = %q: %q, want %q: %q", pair[0], pair[1], headerName, headerValue)
		}
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("inert preserved header required reentry: %#v", result.SecretRequirements)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertJMXReviewGatesUnsupportedAuthWithoutBasicApproximation(t *testing.T) {
	for _, mechanism := range []string{"KERBEROS", "CUSTOM-SIGNATURE"} {
		t.Run(mechanism, func(t *testing.T) {
			jmx := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <AuthManager testname="Auth" enabled="true">
          <collectionProp name="AuthManager.auth_list">
            <elementProp name="" elementType="Authorization">
              <stringProp name="Authorization.url"></stringProp>
              <stringProp name="Authorization.username">source-user</stringProp>
              <stringProp name="Authorization.password">source-password</stringProp>
              <stringProp name="Authorization.mechanism">%s</stringProp>
            </elementProp>
          </collectionProp>
        </AuthManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/private</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`, mechanism)

			result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "auth.jmx", BaseURL: "https://api.example.com"})
			if err != nil {
				t.Fatalf("ConvertJMX returned error: %v", err)
			}
			script := result.Scripts[0]
			if script.Config.HTTP.Auth.Type != "" {
				t.Fatalf("global auth = %#v, unsupported source auth must not be approximated", script.Config.HTTP.Auth)
			}
			action := script.Actions[0]
			if action.Auth == nil ||
				action.Auth.Type != strings.ToLower(mechanism) ||
				action.Auth.Username != "source-user" ||
				action.Auth.Password != "source-password" {
				t.Fatalf("action auth = %#v, want exact source auth retained on blocked draft", action.Auth)
			}
			if action.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatalf("metadata = %#v, want durable import review blocker", action.Metadata)
			}
			review, ok := action.Metadata["import_review"].(map[string]any)
			if !ok {
				t.Fatalf("import_review = %#v, want structured review metadata", action.Metadata["import_review"])
			}
			reasons, ok := review["reason_codes"].([]string)
			if !ok || !strings.Contains(strings.Join(reasons, ","), "unsupported_auth_scheme") {
				t.Fatalf("reason codes = %#v", review["reason_codes"])
			}
			if !containsJMeterDiagnostic(result, "requires review") {
				t.Fatalf("diagnostics = %#v, want unsupported auth review warning", result.Unsupported)
			}
			if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("blocked draft validation issues = %v", issues)
			}
		})
	}
}

func TestConvertJMXClampsHTTPTimeoutAndUsesRuntimeRedirectLimit(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <HTTPSamplerProxy testname="Request" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/slow</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
          <stringProp name="HTTPSampler.response_timeout">7200001</stringProp>
          <boolProp name="HTTPSampler.follow_redirects">true</boolProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "bounds.jmx", BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	action := result.Scripts[0].Actions[0]
	capabilities := httpprotocol.Capabilities()
	if action.TimeoutMs != capabilities.MaxTimeoutMS {
		t.Fatalf("timeout = %d, want runtime clamp %d", action.TimeoutMs, capabilities.MaxTimeoutMS)
	}
	if action.MaxRedirects == nil || *action.MaxRedirects != capabilities.MaxRedirects {
		t.Fatalf("max redirects = %#v, want runtime limit %d", action.MaxRedirects, capabilities.MaxRedirects)
	}
	if !containsJMeterDiagnostic(result, "was clamped") {
		t.Fatalf("diagnostics = %#v, want timeout clamp diagnostic", result.Unsupported)
	}
	if issues := dsl.ValidateScriptIssues(result.Scripts[0]); len(issues) != 0 {
		t.Fatalf("bounded draft validation issues = %v", issues)
	}
}

func TestConvertJMXDerivesFirstSamplerBaseAndPreservesCrossHostTarget(t *testing.T) {
	const jmx = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="hosts"/><hashTree>
  <ThreadGroup testname="users"/><hashTree>
    <HTTPSamplerProxy testname="first">
      <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
      <stringProp name="HTTPSampler.protocol">https</stringProp>
      <stringProp name="HTTPSampler.path">/first</stringProp>
      <stringProp name="HTTPSampler.method">GET</stringProp>
    </HTTPSamplerProxy><hashTree/>
    <HTTPSamplerProxy testname="second">
      <stringProp name="HTTPSampler.domain">identity.example.com</stringProp>
      <stringProp name="HTTPSampler.protocol">https</stringProp>
      <stringProp name="HTTPSampler.path">/token</stringProp>
      <stringProp name="HTTPSampler.method">POST</stringProp>
    </HTTPSamplerProxy><hashTree/>
  </hashTree>
</hashTree></hashTree></jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "hosts.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	script := result.Scripts[0]
	if script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("base URL = %q, want first effective sampler host", script.Config.BaseURL)
	}
	if script.Actions[0].Target != "/first" {
		t.Fatalf("first target = %q, want base-relative path", script.Actions[0].Target)
	}
	if script.Actions[1].Target != "https://identity.example.com/token" {
		t.Fatalf("second target = %q, want cross-host absolute URL", script.Actions[1].Target)
	}
	for index, action := range script.Actions {
		if action.Metadata["execution_blocker"] == "import_review_required" {
			t.Fatalf("actions[%d] unexpectedly review-gated: %#v", index, action.Metadata)
		}
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("validation issues = %v", issues)
	}
}

func TestConvertJMXPreservesCredentialsAndIsDeterministic(t *testing.T) {
	const (
		urlCanary      = "jmeter-url-canary-22f1"
		headerCanary   = "jmeter-header-canary-9d63"
		passwordCanary = "jmeter-password-canary-b881"
		bodyCanary     = "jmeter-body-canary-401e"
	)
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Credential Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Users" enabled="true"/>
      <hashTree>
        <AuthManager testname="Auth" enabled="true">
          <collectionProp name="AuthManager.auth_list">
            <elementProp name="" elementType="Authorization">
              <stringProp name="Authorization.url"></stringProp>
              <stringProp name="Authorization.username">service-user</stringProp>
              <stringProp name="Authorization.password">jmeter-password-canary-b881</stringProp>
              <stringProp name="Authorization.mechanism">BASIC</stringProp>
            </elementProp>
          </collectionProp>
        </AuthManager>
        <hashTree/>
        <HeaderManager testname="Headers" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">Content-Type</stringProp>
              <stringProp name="Header.value">application/json</stringProp>
            </elementProp>
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Api-Key</stringProp>
              <stringProp name="Header.value">jmeter-header-canary-9d63</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <HTTPSamplerProxy testname="Login" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/login?access_token=jmeter-url-canary-22f1</stringProp>
          <stringProp name="HTTPSampler.method">POST</stringProp>
          <boolProp name="HTTPSampler.postBodyRaw">true</boolProp>
          <elementProp name="HTTPsampler.Arguments" elementType="Arguments">
            <collectionProp name="Arguments.arguments">
              <elementProp name="" elementType="HTTPArgument">
                <boolProp name="HTTPArgument.always_encode">false</boolProp>
                <stringProp name="Argument.value">{"password":"jmeter-body-canary-401e","note":"byte-exact"}</stringProp>
                <stringProp name="Argument.metadata">=</stringProp>
              </elementProp>
            </collectionProp>
          </elementProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	var baseline [sha256.Size]byte
	for run := 0; run < 25; run++ {
		result, err := ConvertJMX([]byte(jmx), ConvertOptions{
			FileName: "credentials.jmx",
			BaseURL:  "https://api.example.com",
		})
		if err != nil {
			t.Fatalf("run %d: ConvertJMX returned error: %v", run, err)
		}
		if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
			t.Fatalf("run %d: scripts/actions = %#v", run, result.Scripts)
		}
		script := result.Scripts[0]
		action := script.Actions[0]
		if !strings.Contains(action.Target, urlCanary) {
			t.Fatalf("run %d: target = %q, want preserved query credential", run, action.Target)
		}
		if action.Headers["X-Api-Key"] != headerCanary {
			t.Fatalf("run %d: header = %q, want preserved credential", run, action.Headers["X-Api-Key"])
		}
		if !strings.Contains(action.Body, `"password":"`+bodyCanary+`"`) ||
			!strings.Contains(action.Body, `"note":"byte-exact"`) {
			t.Fatalf("run %d: body = %q, want preserved password with nonsensitive bytes retained", run, action.Body)
		}
		if script.Config.HTTP.Auth.Password != passwordCanary {
			t.Fatalf("run %d: auth = %#v, want preserved password", run, script.Config.HTTP.Auth)
		}
		if len(result.SecretRequirements) != 0 {
			t.Fatalf("run %d: requirements = %#v, preserved credentials need no reentry", run, result.SecretRequirements)
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("run %d: validation issues = %v", run, issues)
		}

		if script.Metadata != nil && script.Metadata.Import != nil {
			script.Metadata.Import.Preserved["imported_at"] = ""
		}
		payload, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("run %d: marshal result: %v", run, err)
		}
		sourceReview, err := json.Marshal(result.SourceReview)
		if err != nil {
			t.Fatalf("run %d: marshal source review: %v", run, err)
		}
		for _, canary := range []string{urlCanary, headerCanary, passwordCanary, bodyCanary} {
			if !strings.Contains(string(sourceReview), canary) {
				t.Fatalf("run %d: source review lost canary %q: %s", run, canary, sourceReview)
			}
		}
		hash := sha256.Sum256(payload)
		if run == 0 {
			baseline = hash
		} else if hash != baseline {
			t.Fatalf("run %d: normalized result hash = %x, want %x", run, hash, baseline)
		}
	}
}

// TestConvertJMXAppliesSamplerScopedHeaderManager pins per-sampler header
// scoping.
//
// In JMeter a HeaderManager nested inside a sampler's own hashTree applies to
// that sampler only. The importer skipped sampler children entirely and
// classified HeaderManager as a supported element, so those headers were
// dropped with no warning: the converted request went out without them, auth
// headers included, and nothing in the review surfaced the loss.
func TestConvertJMXAppliesSamplerScopedHeaderManager(t *testing.T) {
	result, err := ConvertJMX([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="scoped"><boolProp name="TestPlan.functional_mode">false</boolProp></TestPlan>
    <hashTree>
      <ThreadGroup testname="Group"><stringProp name="ThreadGroup.num_threads">1</stringProp><stringProp name="ThreadGroup.ramp_time">1</stringProp></ThreadGroup>
      <hashTree>
        <HeaderManager testname="Shared" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Shared</stringProp>
              <stringProp name="Header.value">shared-value</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <HTTPSamplerProxy testname="scoped" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/scoped</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree>
          <HeaderManager testname="Scoped" enabled="true">
            <collectionProp name="HeaderManager.headers">
              <elementProp name="" elementType="Header">
                <stringProp name="Header.name">X-Scoped</stringProp>
                <stringProp name="Header.value">scoped-value</stringProp>
              </elementProp>
            </collectionProp>
          </HeaderManager>
          <hashTree/>
        </hashTree>
        <HTTPSamplerProxy testname="plain" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/plain</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 2 {
		t.Fatalf("actions = %#v, want two", result.Scripts)
	}
	scoped := result.Scripts[0].Actions[0]
	plain := result.Scripts[0].Actions[1]

	if headerValue(scoped, "X-Scoped") != "scoped-value" {
		t.Fatalf("sampler-scoped header was dropped: %#v", scoped.HeaderList)
	}
	if headerValue(scoped, "X-Shared") != "shared-value" {
		t.Fatalf("thread-group header did not reach the scoped sampler: %#v", scoped.HeaderList)
	}
	// Scoping is the point: the second sampler must not inherit it.
	if headerValue(plain, "X-Scoped") != "" {
		t.Fatalf("sampler-scoped header leaked to a sibling sampler: %#v", plain.HeaderList)
	}
	if headerValue(plain, "X-Shared") != "shared-value" {
		t.Fatalf("thread-group header did not reach the sibling sampler: %#v", plain.HeaderList)
	}
}

func jmeterPreservedHeaderBytePairs(t *testing.T, action dsl.Action) [][2]string {
	t.Helper()
	marker, ok := action.Metadata[dsl.ImportHeaderFidelityMetadataKey].(map[string]any)
	if !ok {
		t.Fatalf("missing header fidelity marker: %#v", action.Metadata)
	}
	var occurrences []map[string]any
	switch values := marker["omitted_occurrences"].(type) {
	case []map[string]any:
		occurrences = values
	case []any:
		for _, raw := range values {
			if occurrence, ok := raw.(map[string]any); ok {
				occurrences = append(occurrences, occurrence)
			}
		}
	}
	var out [][2]string
	for _, occurrence := range occurrences {
		name, nameOK := occurrence[dsl.ImportHeaderFidelityPreservedNameBase64Key].(string)
		value, valueOK := occurrence[dsl.ImportHeaderFidelityPreservedValueBase64Key].(string)
		if !nameOK || !valueOK {
			continue
		}
		nameBytes, err := base64.StdEncoding.DecodeString(name)
		if err != nil {
			t.Fatalf("decode preserved header name: %v", err)
		}
		valueBytes, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("decode preserved header value: %v", err)
		}
		out = append(out, [2]string{string(nameBytes), string(valueBytes)})
	}
	return out
}

func headerValue(action dsl.Action, name string) string {
	for _, header := range action.HeaderList {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	for key, value := range action.Headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
