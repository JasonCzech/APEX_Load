package importcontract

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hamba/avro/v2/ocf"
	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/brokerconfig"
	"github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/eventhubscapture"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/raml"
	"github.com/jasonczech/apexload/pkg/convert/recorderjson"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/wsdl"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var updateImportGoldens = flag.Bool("update", false, "update converter golden files")

type goldenConversion func(t *testing.T) (any, error)

type importerGoldenCase struct {
	name                 string
	valid                goldenConversion
	invalid              goldenConversion
	invalidReturnsResult bool
}

// TestImporterGoldenCorpus freezes both successful conversion output and a
// meaningful diagnostic for each importer in the cross-importer corpus. The
// separate files make review diffs useful: a converter cannot gain a happy-path
// snapshot while its failure behavior quietly disappears.
func TestImporterGoldenCorpus(t *testing.T) {
	for _, tc := range importerGoldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			assertConversionGolden(t, tc.name+".valid.golden", tc.valid, false)
			assertConversionGolden(t, tc.name+".invalid.golden", tc.invalid, !tc.invalidReturnsResult)
		})
	}
}

func importerGoldenCases() []importerGoldenCase {
	return []importerGoldenCase{
		{
			name: "openapi",
			valid: func(t *testing.T) (any, error) {
				return openapi.Convert(openapi.ImportRequest{Name: "openapi golden", Content: `{"openapi":"3.0.3","info":{"title":"Orders","version":"1"},"servers":[{"url":"https://api.example.test"}],"paths":{"/orders":{"get":{"operationId":"listOrders","responses":{"200":{"description":"ok"}}}}}}`})
			},
			invalid: func(t *testing.T) (any, error) {
				return openapi.Convert(openapi.ImportRequest{Name: "invalid", Content: ""})
			},
		},
		{
			name: "bruno",
			valid: func(t *testing.T) (any, error) {
				return bruno.Inspect(bruno.Input{Filename: "health.yml", ProjectID: "golden-project", Content: []byte(`info:
  name: Health
  type: http
  seq: 1
http:
  method: GET
  url: https://api.example.test/health
`)})
			},
			invalid: func(t *testing.T) (any, error) {
				return bruno.Inspect(bruno.Input{})
			},
		},
		{
			name: "postman",
			valid: func(t *testing.T) (any, error) {
				return postman.Convert(postman.ImportRequest{Name: "postman golden", Content: `{"info":{"name":"Orders","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},"item":[{"name":"List Orders","request":{"method":"GET","url":"https://api.example.test/orders"}}]}`})
			},
			invalid: func(t *testing.T) (any, error) {
				return postman.Convert(postman.ImportRequest{Name: "invalid", Content: `{"info":{"name":"Empty"},"item":[]}`})
			},
		},
		{
			name: "wsdl",
			valid: func(t *testing.T) (any, error) {
				return wsdl.Convert(wsdl.ImportRequest{Name: "wsdl golden", Content: `<definitions name="HealthService" targetNamespace="urn:health" xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/" xmlns:tns="urn:health"><portType name="HealthPortType"><operation name="Check"/></portType><binding name="HealthBinding" type="tns:HealthPortType"><soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/><operation name="Check"><soap:operation soapAction="urn:Check"/></operation></binding><service name="HealthService"><port name="HealthPort" binding="tns:HealthBinding"><soap:address location="https://api.example.test/soap"/></port></service></definitions>`})
			},
			invalid: func(t *testing.T) (any, error) {
				return wsdl.Convert(wsdl.ImportRequest{Name: "invalid", Content: `<description xmlns="http://www.w3.org/ns/wsdl"/>`})
			},
		},
		{
			name: "har",
			valid: func(t *testing.T) (any, error) {
				return har.Convert([]byte(`{"log":{"entries":[{"startedDateTime":"2026-01-01T00:00:00Z","time":10,"request":{"method":"GET","url":"https://api.example.test/health","headers":[]},"response":{"status":200,"headers":[],"content":{"text":"ok"}}}]}}`), har.ConvertOptions{ScriptName: "har golden"})
			},
			invalid: func(t *testing.T) (any, error) {
				return har.Convert(nil, har.ConvertOptions{ScriptName: "invalid"})
			},
		},
		{
			name: "jmeter",
			valid: func(t *testing.T) (any, error) {
				return jmeter.ConvertJMX([]byte(`<?xml version="1.0"?><jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree><HTTPSamplerProxy testname="Health"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">api.example.test</stringProp><stringProp name="HTTPSampler.path">/health</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`), jmeter.ConvertOptions{FileName: "health.jmx"})
			},
			invalid: func(t *testing.T) (any, error) {
				return jmeter.ConvertJMX([]byte(`<jmeterTestPlan>`), jmeter.ConvertOptions{FileName: "invalid.jmx"})
			},
		},
		{
			name: "k6",
			valid: func(t *testing.T) (any, error) {
				return k6.Convert(k6.ImportRequest{Name: "k6 golden", Content: `import http from 'k6/http'; export default function () { http.get('https://api.example.test/health'); }`})
			},
			invalid: func(t *testing.T) (any, error) {
				return k6.Convert(k6.ImportRequest{Name: "invalid", Content: ""})
			},
		},
		{
			name: "eventhubscapture",
			valid: func(t *testing.T) (any, error) {
				return eventhubscapture.Convert(eventhubscapture.ConvertRequest{Name: "capture golden", EventHub: "orders", Data: buildEventHubsCapture(t)})
			},
			invalid: func(t *testing.T) (any, error) {
				return eventhubscapture.Convert(eventhubscapture.ConvertRequest{Name: "invalid", Data: nil})
			},
		},
		{
			name: "enterprisearchive",
			valid: func(t *testing.T) (any, error) {
				archive := buildZIPFixture(t, []zipFixtureEntry{
					{Name: "script.usr", Body: []byte("[General]\nType=Web\n[Actions]\nAction=Action.c\n")},
					{Name: "Action.c", Body: []byte("Action()\n{\n\tweb_url(\"orders\",\n\t\t\"URL=https://api.example.test/orders\",\n\t\tLAST);\n\treturn 0;\n}\n")},
				})
				return enterprisearchive.ConvertZip(archive, enterprisearchive.ConvertOptions{})
			},
			invalid: func(t *testing.T) (any, error) {
				return enterprisearchive.ConvertZip([]byte("not a zip archive"), enterprisearchive.ConvertOptions{})
			},
		},
		{
			name: "devweb",
			valid: func(t *testing.T) (any, error) {
				return devweb.Convert(devweb.ImportRequest{Name: "devweb golden", Content: `load.action(async function () {
  await new load.WebRequest({url: "https://api.example.test/orders", method: "GET"}).send();
});`})
			},
			invalid: func(t *testing.T) (any, error) {
				result, err := devweb.Convert(devweb.ImportRequest{Name: "invalid", Content: `load.action("Escaped", async function () {
				  await new load.WebRequest({url: "https://api.example.test/\q", method: "GET"}).send();
				});`})
				if err == nil && (len(result.Script.Actions) != 1 || result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required") {
					t.Fatalf("invalid DevWeb fixture did not produce a blocked diagnostic draft: %#v", result.Script.Actions)
				}
				return result, err
			},
			invalidReturnsResult: true,
		},
		{
			name: "raml",
			valid: func(t *testing.T) (any, error) {
				return raml.Convert(raml.ImportRequest{Name: "raml golden", Content: `#%RAML 1.0
title: Orders
baseUri: https://api.example.test
/orders:
  get:
`})
			},
			invalid: func(t *testing.T) (any, error) {
				return raml.Convert(raml.ImportRequest{Name: "invalid", Content: "#%RAML 1.0\ntitle: ["})
			},
		},
		{
			name: "asyncapi",
			valid: func(t *testing.T) (any, error) {
				return asyncapi.Convert(asyncapi.ImportRequest{Name: "asyncapi golden", Content: `asyncapi: "2.6.0"
info: {title: Orders, version: "1.0.0"}
servers:
  production: {url: broker.example.test:9092, protocol: kafka}
channels:
  orders.created:
    publish:
      operationId: publishOrder
      message:
        payload: {type: object}
`})
			},
			invalid: func(t *testing.T) (any, error) {
				return asyncapi.Convert(asyncapi.ImportRequest{Name: "invalid", Content: ""})
			},
		},
		{
			name: "recorderjson",
			valid: func(t *testing.T) (any, error) {
				return recorderjson.Convert([]byte(`{"title":"recorder golden","steps":[{"type":"navigate","url":"https://api.example.test/orders"}]}`), recorderjson.ConvertOptions{ScriptName: "recorder golden"})
			},
			invalid: func(t *testing.T) (any, error) {
				return recorderjson.Convert([]byte(`{"steps":[`), recorderjson.ConvertOptions{ScriptName: "invalid"})
			},
		},
		{
			name: "sample_curl",
			valid: func(t *testing.T) (any, error) {
				return sample.Convert(sample.ImportRequest{Name: "curl golden", Format: "curl", Content: `curl -X GET 'https://api.example.test/orders' -H 'Accept: application/json'`})
			},
			invalid: func(t *testing.T) (any, error) {
				return sample.Convert(sample.ImportRequest{Name: "invalid", Format: "curl", Content: "curl"})
			},
		},
		{
			name: "brokerconfig",
			valid: func(t *testing.T) (any, error) {
				return brokerconfig.Convert(brokerconfig.ConvertRequest{Name: "broker golden", Data: []byte("DEFINE QLOCAL('ORDERS.IN') REPLACE\n")})
			},
			invalid: func(t *testing.T) (any, error) {
				return brokerconfig.Convert(brokerconfig.ConvertRequest{Name: "invalid", Data: nil})
			},
		},
	}
}

func buildEventHubsCapture(t *testing.T) []byte {
	t.Helper()
	const schema = `{"type":"record","name":"EventData","namespace":"Microsoft.ServiceBus.Messaging","fields":[{"name":"SequenceNumber","type":"long"},{"name":"Offset","type":"string"},{"name":"EnqueuedTimeUtc","type":"string"},{"name":"SystemProperties","type":{"type":"map","values":["long","double","string","bytes"]}},{"name":"Properties","type":{"type":"map","values":["long","double","string","bytes","null"]}},{"name":"Body","type":["null","bytes"]}]}`
	var buffer bytes.Buffer
	encoder, err := ocf.NewEncoder(schema, &buffer)
	if err != nil {
		t.Fatalf("create Event Hubs capture encoder: %v", err)
	}
	record := map[string]any{
		"SequenceNumber":   int64(1),
		"Offset":           "100",
		"EnqueuedTimeUtc":  "2026-01-01T00:00:00Z",
		"SystemProperties": map[string]any{"x-opt-partition-key": map[string]any{"string": "tenant-a"}},
		"Properties":       map[string]any{"source": map[string]any{"string": "golden"}},
		"Body":             map[string]any{"bytes": []byte(`{"order_id":"123"}`)},
	}
	if err := encoder.Encode(record); err != nil {
		t.Fatalf("encode Event Hubs capture: %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("close Event Hubs capture encoder: %v", err)
	}
	return buffer.Bytes()
}

func assertConversionGolden(t *testing.T, name string, convert goldenConversion, wantError bool) {
	t.Helper()
	value, err := convert(t)
	if wantError && err == nil {
		t.Fatalf("%s: invalid fixture unexpectedly converted successfully", name)
	}
	if !wantError && err != nil {
		t.Fatalf("%s: valid fixture failed: %v", name, err)
	}
	if !wantError {
		validateGoldenResultScripts(t, name, value)
	}

	var actual []byte
	if err != nil {
		actual = []byte(normalizeGoldenString(err.Error()) + "\n")
	} else {
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatalf("%s: marshal converter result: %v", name, marshalErr)
		}
		var normalized any
		if unmarshalErr := json.Unmarshal(encoded, &normalized); unmarshalErr != nil {
			t.Fatalf("%s: normalize converter result: %v", name, unmarshalErr)
		}
		normalized = normalizeGoldenValue(normalized)
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if marshalErr = encoder.Encode(normalized); marshalErr != nil {
			t.Fatalf("%s: marshal normalized result: %v", name, marshalErr)
		}
		actual = output.Bytes()
	}

	path := filepath.Join("testdata", "golden", name)
	if *updateImportGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden directory: %v", err)
		}
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatalf("update %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./pkg/convert/importcontract -run TestImporterGoldenCorpus -update)", path, err)
	}
	if string(actual) != string(want) {
		t.Fatalf("%s drifted; review the converter change, then run go test ./pkg/convert/importcontract -run TestImporterGoldenCorpus -update\n%s", path, firstDifference(string(want), string(actual)))
	}
}

func validateGoldenResultScripts(t *testing.T, name string, value any) {
	t.Helper()
	result := reflect.ValueOf(value)
	if result.Kind() == reflect.Pointer {
		if result.IsNil() {
			t.Fatalf("%s: converter returned a nil result", name)
		}
		result = result.Elem()
	}
	if result.Kind() != reflect.Struct {
		t.Fatalf("%s: converter result %T is not a struct", name, value)
	}
	var scripts []*dsl.Script
	if field := result.FieldByName("Script"); field.IsValid() && field.CanInterface() {
		if script, ok := field.Interface().(*dsl.Script); ok && script != nil {
			scripts = append(scripts, script)
		}
	}
	if field := result.FieldByName("Scripts"); field.IsValid() && field.CanInterface() {
		switch many := field.Interface().(type) {
		case []*dsl.Script:
			scripts = append(scripts, many...)
		case []bruno.NativeScriptCandidate:
			for _, candidate := range many {
				scripts = append(scripts, candidate.Script)
			}
		}
	}
	if len(scripts) == 0 {
		t.Fatalf("%s: converter result %T exposes no generated scripts", name, value)
	}
	for index, script := range scripts {
		if script == nil {
			t.Fatalf("%s: converter returned nil script[%d]", name, index)
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("%s: generated script[%d] %q is invalid: %v", name, index, script.Name, issues)
		}
	}
}

var uuidGoldenPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

func normalizeGoldenString(value string) string {
	value = uuidGoldenPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if _, err := uuid.Parse(candidate); err == nil {
			return "<UUID>"
		}
		return candidate
	})
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil && strings.Contains(value, "T") {
		return "<TIMESTAMP>"
	}
	return value
}

func normalizeGoldenValue(value any) any {
	switch typed := value.(type) {
	case string:
		return normalizeGoldenString(typed)
	case []any:
		for index := range typed {
			typed[index] = normalizeGoldenValue(typed[index])
		}
		return typed
	case map[string]any:
		for key := range typed {
			typed[key] = normalizeGoldenValue(typed[key])
		}
		return typed
	default:
		return value
	}
}

func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	limit := min(len(wantLines), len(gotLines))
	for index := 0; index < limit; index++ {
		if wantLines[index] != gotLines[index] {
			return fmt.Sprintf("line %d\nwant: %s\n got: %s", index+1, wantLines[index], gotLines[index])
		}
	}
	return fmt.Sprintf("line count differs: want %d, got %d", len(wantLines), len(gotLines))
}
