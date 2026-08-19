package importcontract

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/brokerconfig"
	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/raml"
	"github.com/jasonczech/apexload/pkg/convert/recorderjson"
	"github.com/jasonczech/apexload/pkg/convert/sample"
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

func assertConversionGolden(t *testing.T, name string, convert goldenConversion, wantError bool) {
	t.Helper()
	value, err := convert(t)
	if wantError && err == nil {
		t.Fatalf("%s: invalid fixture unexpectedly converted successfully", name)
	}
	if !wantError && err != nil {
		t.Fatalf("%s: valid fixture failed: %v", name, err)
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
		actual, marshalErr = json.MarshalIndent(normalized, "", "  ")
		if marshalErr != nil {
			t.Fatalf("%s: marshal normalized result: %v", name, marshalErr)
		}
		actual = append(actual, '\n')
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
