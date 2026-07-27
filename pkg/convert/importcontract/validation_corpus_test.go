package importcontract

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/brokerconfig"
	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/raml"
	"github.com/jasonczech/apexload/pkg/convert/recorderjson"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestImporterHappyPathsProduceValidScripts is the cross-importer validation
// corpus for the importers that the frozen baseline contracts do not cover.
//
// dsl.ValidateScriptIssues applies pacing, run-logic, and think-time resource
// limits to every protocol rather than only the messaging families, and
// validateScript is a hard 400 on script create/update and on the stored-script
// revalidation that runs when a managed test is launched. An importer that
// emits a value outside one of those vocabularies therefore produces drafts
// that cannot be saved. This test is deliberately validation-only — it freezes
// no output — so adding an importer here is cheap and never needs a golden
// file regenerated.
func TestImporterHappyPathsProduceValidScripts(t *testing.T) {
	tests := []struct {
		name    string
		convert func(t *testing.T) []*dsl.Script
	}{
		{
			name: "enterprisearchive",
			convert: func(t *testing.T) []*dsl.Script {
				t.Helper()
				archive := buildZIPFixture(t, []zipFixtureEntry{
					{Name: "script.usr", Body: []byte("[General]\nType=Web\n[Actions]\nAction=Action.c\n")},
					{Name: "default.cfg", Body: []byte("[General]\nDefaultRunLogic=default.usp\n")},
					{Name: "default.usp", Body: []byte("[RunLogicRunRoot]\nRunLogicNumOfIterations=\"2\"\nRunLogicPaceType=\"Const\"\nRunLogicPaceConstTime=\"5\"\n")},
					{Name: "Action.c", Body: []byte("Action()\n{\n\tweb_url(\"orders\",\n\t\t\"URL=https://api.example.test/orders\",\n\t\tLAST);\n\treturn 0;\n}\n")},
				})
				result, err := enterprisearchive.ConvertZip(archive, enterprisearchive.ConvertOptions{})
				if err != nil {
					t.Fatalf("enterprisearchive.ConvertZip: %v", err)
				}
				return collectScripts(result.Script, result.Scripts)
			},
		},
		{
			name: "devweb",
			convert: func(t *testing.T) []*dsl.Script {
				t.Helper()
				result, err := devweb.Convert(devweb.ImportRequest{
					Name: "devweb corpus",
					Content: `load.initialize(async function () {});
load.action(async function () {
  const response = await new load.WebRequest({
    url: "https://api.example.test/orders",
    method: "GET",
  }).send();
});`,
				})
				if err != nil {
					t.Fatalf("devweb.Convert: %v", err)
				}
				return collectScripts(result.Script, nil)
			},
		},
		{
			name: "raml",
			convert: func(t *testing.T) []*dsl.Script {
				t.Helper()
				result, err := raml.Convert(raml.ImportRequest{
					Name: "raml corpus",
					Content: `#%RAML 1.0
title: Orders
baseUri: https://api.example.test
/orders:
  get:
    responses:
      200:
        body:
          application/json:
`,
				})
				if err != nil {
					t.Fatalf("raml.Convert: %v", err)
				}
				return collectScripts(result.Script, nil)
			},
		},
		{
			name: "asyncapi",
			convert: func(t *testing.T) []*dsl.Script {
				t.Helper()
				result, err := asyncapi.Convert(asyncapi.ImportRequest{
					Name: "asyncapi corpus",
					Content: `asyncapi: "2.6.0"
info:
  title: Orders
  version: "1.0.0"
servers:
  production:
    url: broker.example.test:9092
    protocol: kafka
channels:
  orders.created:
    publish:
      operationId: publishOrderCreated
      message:
        payload:
          type: object
          properties:
            id:
              type: string
`,
				})
				if err != nil {
					t.Fatalf("asyncapi.Convert: %v", err)
				}
				return collectScripts(result.Script, result.Scripts)
			},
		},
		{
			name: "recorderjson",
			convert: func(t *testing.T) []*dsl.Script {
				t.Helper()
				result, err := recorderjson.Convert([]byte(`{
  "title": "recorder corpus",
  "steps": [
    {"type": "navigate", "url": "https://api.example.test/orders"}
  ]
}`), recorderjson.ConvertOptions{ScriptName: "recorder corpus"})
				if err != nil {
					t.Fatalf("recorderjson.Convert: %v", err)
				}
				return collectScripts(result.Script, nil)
			},
		},
		{
			name: "sample_curl",
			convert: func(t *testing.T) []*dsl.Script {
				t.Helper()
				result, err := sample.Convert(sample.ImportRequest{
					Name:    "curl corpus",
					Format:  "curl",
					Content: `curl -X GET 'https://api.example.test/orders' -H 'Accept: application/json'`,
				})
				if err != nil {
					t.Fatalf("sample.Convert(curl): %v", err)
				}
				return collectScripts(result.Script, nil)
			},
		},
		{
			name: "brokerconfig",
			convert: func(t *testing.T) []*dsl.Script {
				t.Helper()
				result, err := brokerconfig.Convert(brokerconfig.ConvertRequest{
					Name: "broker corpus",
					Data: []byte("DEFINE QLOCAL('ORDERS.IN') REPLACE\nDEFINE TOPIC('ORDERS.EVENTS') TOPICSTR('orders/events') REPLACE\n"),
				})
				if err != nil {
					t.Fatalf("brokerconfig.Convert: %v", err)
				}
				return collectScripts(result.Script, nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scripts := test.convert(t)
			if len(scripts) == 0 {
				t.Fatal("converter produced no script")
			}
			for index, script := range scripts {
				if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
					t.Fatalf("script[%d] %q is invalid: %v", index, script.Name, issues)
				}
			}
		})
	}
}

func collectScripts(single *dsl.Script, many []*dsl.Script) []*dsl.Script {
	scripts := make([]*dsl.Script, 0, len(many)+1)
	for _, script := range many {
		if script != nil {
			scripts = append(scripts, script)
		}
	}
	if len(scripts) == 0 && single != nil {
		scripts = append(scripts, single)
	}
	return scripts
}
