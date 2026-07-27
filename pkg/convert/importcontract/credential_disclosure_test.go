package importcontract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	k6import "github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/wsdl"
)

const disclosureCanary = "import-disclosure-canary-7Q4Z"

// TestPublicConversionResultsNeverDiscloseAuthoredCredentials is the
// cross-importer disclosure contract.
//
// The existing credential contract checks only executable material, and that is
// not where credentials actually escaped: DevWeb governed the header correctly
// in the script while the raw value survived in source_review, because the
// shared scrubber's patterns do not match a vendor-prefixed name like
// X-Api-Key. Serializing the ENTIRE public result and searching for the canary
// is what catches that class — source review, stats, summaries, findings,
// correlation samples and any future field, without having to enumerate them.
//
// Source values legitimately travel in ProtectedRequirements, which is
// json:"-" and therefore never part of this payload.
func TestPublicConversionResultsNeverDiscloseAuthoredCredentials(t *testing.T) {
	tests := []struct {
		name    string
		convert func(t *testing.T) any
	}{
		{
			name: "devweb",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := devweb.Convert(devweb.ImportRequest{
					Name: "disclosure",
					Content: fmt.Sprintf(`load.action(async function () {
  await new load.WebRequest({
    url: "https://api.contract.test/login",
    method: "POST",
    headers: {"Authorization": "Bearer %s", "X-Api-Key": "%s"},
  }).send();
});`, disclosureCanary, disclosureCanary),
				})
				if err != nil {
					t.Fatalf("devweb.Convert: %v", err)
				}
				result.ProtectedRequirements.Take()
				return result
			},
		},
		{
			name: "postman",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := postman.Convert(postman.ImportRequest{Content: fmt.Sprintf(`{
  "info":{"name":"disclosure"},
  "item":[{"name":"login","request":{
    "method":"POST","url":"https://api.contract.test/login",
    "header":[{"key":"Authorization","value":"Bearer %s"},{"key":"X-Api-Key","value":"%s"}]
  }}]
}`, disclosureCanary, disclosureCanary)})
				if err != nil {
					t.Fatalf("postman.Convert: %v", err)
				}
				result.ProtectedRequirements.Take()
				return result
			},
		},
		{
			name: "raw_http",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := sample.Convert(sample.ImportRequest{
					Format: "raw_http",
					Content: fmt.Sprintf(
						"POST https://api.contract.test/login HTTP/1.1\nAuthorization: Bearer %s\nX-Api-Key: %s\n\n",
						disclosureCanary, disclosureCanary),
				})
				if err != nil {
					t.Fatalf("sample.Convert: %v", err)
				}
				result.ProtectedRequirements.Take()
				return result
			},
		},
		{
			name: "openapi",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := openapi.Convert(openapi.ImportRequest{Content: fmt.Sprintf(`{
  "openapi":"3.0.3",
  "info":{"title":"disclosure","version":"1"},
  "servers":[{"url":"https://api.contract.test"}],
  "paths":{"/login":{"get":{
    "operationId":"login",
    "parameters":[{"name":"X-Api-Key","in":"header","schema":{"type":"string","example":"%s"}}],
    "responses":{"200":{"description":"ok"}}
  }}}
}`, disclosureCanary)})
				if err != nil {
					t.Fatalf("openapi.Convert: %v", err)
				}
				result.ProtectedRequirements.Take()
				return result
			},
		},
		{
			name: "har",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := har.Convert([]byte(fmt.Sprintf(`{
  "log":{"version":"1.2","creator":{"name":"contract","version":"1"},"entries":[{
    "startedDateTime":"2026-01-02T03:04:05Z","time":1,
    "request":{"method":"GET","url":"https://api.contract.test/login","httpVersion":"HTTP/1.1",
      "headers":[{"name":"X-Api-Key","value":"%s"}],
      "queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},
    "response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],"cookies":[],
      "content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},
    "cache":{},"timings":{"send":0,"wait":1,"receive":0}
  }]}}`, disclosureCanary)), har.ConvertOptions{})
				if err != nil {
					t.Fatalf("har.Convert: %v", err)
				}
				result.ProtectedRequirements.Take()
				return result
			},
		},
		{
			name: "wsdl",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := wsdl.Convert(wsdl.ImportRequest{Content: credentialWSDL(disclosureCanary)})
				if err != nil {
					t.Fatalf("wsdl.Convert: %v", err)
				}
				return result
			},
		},
		{
			name: "jmeter",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := jmeter.ConvertJMX([]byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="disclosure"><boolProp name="TestPlan.functional_mode">false</boolProp></TestPlan>
    <hashTree>
      <ThreadGroup testname="Group"><stringProp name="ThreadGroup.num_threads">1</stringProp><stringProp name="ThreadGroup.ramp_time">1</stringProp></ThreadGroup>
      <hashTree>
        <HeaderManager testname="Headers" enabled="true">
          <collectionProp name="HeaderManager.headers">
            <elementProp name="" elementType="Header">
              <stringProp name="Header.name">X-Api-Key</stringProp>
              <stringProp name="Header.value">%s</stringProp>
            </elementProp>
          </collectionProp>
        </HeaderManager>
        <hashTree/>
        <HTTPSamplerProxy testname="login" enabled="true">
          <stringProp name="HTTPSampler.domain">api.contract.test</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/login</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`, disclosureCanary)), jmeter.ConvertOptions{})
				if err != nil {
					t.Fatalf("jmeter.ConvertJMX: %v", err)
				}
				return result
			},
		},
		{
			name: "k6",
			convert: func(t *testing.T) any {
				t.Helper()
				result, err := k6import.Convert(k6import.ImportRequest{Content: fmt.Sprintf(`import http from 'k6/http';
export default function () {
  http.get('https://api.contract.test/login', { headers: { 'X-Api-Key': '%s' } });
}`, disclosureCanary)})
				if err != nil {
					t.Fatalf("k6.Convert: %v", err)
				}
				return result
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.convert(t)
			payload, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal public result: %v", err)
			}
			if strings.Contains(string(payload), disclosureCanary) {
				t.Fatalf("public conversion result disclosed an authored credential:\n%s", payload)
			}
		})
	}
}
