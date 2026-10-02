package raml

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMuleSoftLibraryTraitsTypesAndRequestFields(t *testing.T) {
	content := `#%RAML 1.0
title: Bundled API
baseUri: https://{host}/{path}
baseUriParameters:
  host: {example: api.example.test}
  path: {example: /v1}
uses:
  common: /libraries/common.raml
  model: libraries/model.raml
/records:
  is: [common.trace]
  post:
    is: [common.create: {payload: model.Request}]
    headers:
      source: {example: override}
    queryParameters:
      mode: {type: string, required: true, enum: [reset, update]}
      search: {example: 'a & b'}
      count: {type: integer, example: 0}
      optional: {required: false}
    body:
      application/json:
        type: model.Request
`
	files := map[string][]byte{
		"bundle/api.raml": []byte(content),
		"bundle/libraries/common.raml": []byte(`#%RAML 1.0 Library
traits:
  trace:
    headers:
      source: {example: shared}
      client_id: {example: example-client}
      client_secret: {example: DO-NOT-EMBED}
      x-transaction-id: {example: trace-example}
  create:
    body:
      application/json: {type: '<<payload>>'}
    responses:
      201: {}
`),
		"bundle/libraries/model.raml": []byte(`#%RAML 1.0 Library
uses:
  nested: nested.raml
types:
  Request:
    type: object
    properties:
      enabled: {type: boolean, example: false}
      items: nested.Item[]
      optional?: string
`),
		"bundle/libraries/nested.raml": []byte(`#%RAML 1.0 Library
types:
  Item:
    properties:
      id: {example: ITEM-1}
`),
	}
	result, err := Convert(ImportRequest{Content: content, Files: files, RootFile: "bundle/api.raml"})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if result.Summary.BaseURL != "https://api.example.test/v1" {
		t.Fatalf("base URL = %q", result.Summary.BaseURL)
	}
	if action.Target != "/records?count=0&mode=reset&search=a+%26+b" {
		t.Fatalf("target = %q", action.Target)
	}
	if action.Headers["source"] != "override" || action.Headers["x-transaction-id"] != "trace-example" || action.Headers["client_id"] != "{{env.RAML_CLIENT_ID}}" || action.Headers["client_secret"] != "{{secret.RAML_CLIENT_SECRET}}" {
		t.Fatal("trait headers lost or credential not governed")
	}
	if action.Assertions[0].Expected != 201 {
		t.Fatalf("status = %#v", action.Assertions)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(action.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != false || len(body["items"].([]any)) != 1 {
		t.Fatalf("typed example body shape = %#v", body)
	}
	if action.Metadata["execution_blocker"] != nil || len(result.SecretRequirements) != 1 || len(result.Summary.Policies) != 1 {
		t.Fatalf("review/accounting = %#v", result.Accounting())
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "DO-NOT-EMBED") {
		t.Fatal("source credential example leaked")
	}
}

func TestRAMLReviewGatesIncompleteContracts(t *testing.T) {
	cases := []struct{ name, content, element string }{
		{"required query", "/items:\n  get:\n    queryParameters:\n      mode: {type: string}\n", "queryParameters"},
		{"required header", "/items:\n  get:\n    headers:\n      source: {type: string}\n", "headers"},
		{"unknown trait", "/items:\n  get:\n    is: [missing]\n", "inheritance"},
		{"cyclic trait", "traits:\n  loop: {is: [loop]}\n/items:\n  get:\n    is: [loop]\n", "inheritance"},
		{"required property", "/items:\n  post:\n    body:\n      application/json:\n        properties:\n          id: string\n", "request body"},
		{"unknown type", "/items:\n  post:\n    body:\n      application/json: {type: Missing}\n", "request body"},
		{"union", "/items:\n  post:\n    body:\n      application/json: {type: 'A | B'}\n", "request body"},
		{"cyclic type", "types:\n  Node:\n    properties:\n      next: Node\n/items:\n  post:\n    body:\n      application/json: {type: Node}\n", "request body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: "#%RAML 1.0\ntitle: Gate\n" + tc.content})
			if err != nil {
				t.Fatal(err)
			}
			if result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" || len(result.Unsupported) == 0 || !strings.Contains(result.Unsupported[0].Element, tc.element) {
				t.Fatalf("missing %s gate", tc.element)
			}
			if a := result.Accounting(); a.Detected != 1 || a.ReviewOnly != 1 || a.Converted != 0 {
				t.Fatalf("accounting=%#v", a)
			}
		})
	}
}

func TestRAMLReferenceResolutionAndValidation(t *testing.T) {
	cases := []struct {
		name, content string
		files         map[string][]byte
		wantErr       bool
		body          string
	}{
		{name: "relative includes", content: "/items: !include resources/items.raml\n", files: map[string][]byte{"resources/items.raml": []byte("post:\n  body:\n    application/json:\n      example: !include ../examples/body.json\n"), "examples/body.json": []byte(`{"id":9007199254740993,"statementID":1,"statementId":2}`)}, body: `{"id":9007199254740993,"statementID":1,"statementId":2}`},
		{name: "NamedExample", content: "/items:\n  post:\n    body:\n      application/json:\n        example: !include example.raml\n", files: map[string][]byte{"example.raml": []byte("#%RAML 1.0 NamedExample\ndisplayName: Demo\nvalue:\n  id: SAMPLE\n")}, body: `{"id":"SAMPLE"}`},
		{name: "wrong basename", content: "/items: !include wrong/items.raml\n", files: map[string][]byte{"right/items.raml": []byte("get: {}")}, wantErr: true},
		{name: "cyclic include", content: "/items: !include loop.raml\n", files: map[string][]byte{"loop.raml": []byte("get: !include loop.raml")}, wantErr: true},
		{name: "duplicate included JSON", content: "/items:\n  post:\n    body:\n      application/json:\n        example: !include example.json\n", files: map[string][]byte{"example.json": []byte(`{"id":1,"id":2}`)}, wantErr: true},
		{name: "invalid included YAML", content: "/items: !include bad.raml\n", files: map[string][]byte{"bad.raml": []byte("get: [")}, wantErr: true},
		{name: "missing library", content: "uses: {lib: missing.raml}\n/items: {get: {}}\n", wantErr: true},
		{name: "cyclic libraries", content: "uses: {lib: library.raml}\n/items: {get: {}}\n", files: map[string][]byte{"library.raml": []byte("#%RAML 1.0 Library\nuses: {again: library.raml}")}, wantErr: true},
		{name: "duplicate included keys", content: "/items: !include bad.raml\n", files: map[string][]byte{"bad.raml": []byte("get: {}\nget: {}")}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: "#%RAML 1.0\ntitle: References\n" + tc.content, Files: tc.files})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && result.Script.Actions[0].Body != tc.body {
				t.Fatalf("body=%q want=%q", result.Script.Actions[0].Body, tc.body)
			}
		})
	}
}

func TestRAMLResourceTypeAndLocalExamplePrecedence(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Resource Types
mediaType: application/json
types:
  Base: {properties: {id: {example: base}}}
  Derived: {type: Base, properties: {enabled: {example: true}}}
resourceTypes:
  collection:
    post:
      body: {type: Derived}
      responses: {201: {}}
    get?: {}
/items:
  type: collection
  post:
    body:
      example: {id: local}
`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Body != `{"id":"local"}` || result.Script.Actions[0].Assertions[0].Expected != 201 {
		t.Fatalf("resource type expansion incorrect")
	}
}

func TestRAMLCredentialQueryAndHeaderRequirements(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Credentials
/items:
  get:
    headers:
      api_key: {example: never-embed-this}
    queryParameters:
      client_id: {type: string}
      client_secret: {example: never-embed-this-either}
`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 2 || !strings.Contains(result.Script.Actions[0].Target, "client_secret={{secret.RAML_CLIENT_SECRET}}") {
		t.Fatal("query credentials not bound")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "never-embed") {
		t.Fatal("credential example leaked")
	}
}

// The private customer archives are evaluated locally without checking their
// customer data or credential examples into the repository. Synthetic tests
// above retain coverage on CI.
func TestCustomerRAMLArchiveFidelity(t *testing.T) {
	dir := os.Getenv("APEXLOAD_RAML_FIDELITY_ARCHIVES")
	if dir == "" {
		t.Skip("set APEXLOAD_RAML_FIDELITY_ARCHIVES to evaluate the private source archives")
	}
	cases := []struct {
		name, root                                  string
		actions, bodies, credentialActions, blocked int
	}{
		{"customer-crm-billing-eapi-1.0.32-raml.zip", "customer-crm-billing-eapi.raml", 9, 9, 6, 0},
		{"cc-mule-orders-api-1.0.0-raml.zip", "orders-api.raml", 1, 0, 0, 0},
		{"customer-profile-crm-eapi-1.0.75-raml.zip", "customer-profile-crm-eapi.raml", 13, 11, 13, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive, err := zip.OpenReader(filepath.Join(dir, tc.name))
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			files := map[string][]byte{}
			for _, entry := range archive.File {
				if entry.FileInfo().IsDir() {
					continue
				}
				r, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
				files[entry.Name] = content
			}
			result, err := Convert(ImportRequest{Content: string(files[tc.root]), Files: files, RootFile: tc.root})
			if err != nil {
				t.Fatal(err)
			}
			if result.Stats.ConvertedActions != tc.actions || result.Stats.RequestBodies != tc.bodies {
				t.Fatalf("stats = %#v", result.Stats)
			}
			explicitExamples := map[string]string{}
			if strings.Contains(tc.name, "billing-eapi") {
				explicitExamples = map[string]string{
					"/billing/v1/balances":                       "examples/paymentBalanceRequest.json",
					"/billing/v1/details":                        "examples/billDetailRequest.json",
					"/billing/v1/history":                        "examples/billHistoryRequest.json",
					"/billing/v1/pdf":                            "examples/pdfRequest.json",
					"/billing/v1/reportingAndCachingForBalances": "examples/paymentBalanceV2Request.json",
					"/billing/v2/balances":                       "examples/paymentBalanceV2Request.json",
					"/generateSummaryDocument":                   "examples/GenerateSummaryTLGandBSSE-Request.json",
					"/v1/archivedCustomerServiceSummary":         "examples/ArchivedCSSReq.json",
					"/v1/customerserviceSummary":                 "examples/CSSReq.json",
				}
			} else if strings.Contains(tc.name, "profile-crm") {
				explicitExamples = map[string]string{
					"/v1/account/nickname":          "examples/updateAcctNickNameRequest.raml",
					"/v1/customer/account/userrole": "examples/postRemoveSecondaryUserReqExample.json",
				}
			}
			credentials := 0
			empty := 0
			for _, action := range result.Script.Actions {
				if example, ok := explicitExamples[action.Target]; ok {
					var expected, actual any
					if err := json.Unmarshal(files[example], &expected); err != nil {
						t.Fatalf("source request example %s is invalid", example)
					}
					if err := json.Unmarshal([]byte(action.Body), &actual); err != nil {
						t.Fatalf("imported request body for %s is invalid", action.Target)
					}
					if !reflect.DeepEqual(actual, expected) {
						t.Fatalf("request example fields lost for %s", action.Target)
					}
				}
				if action.Headers["client_secret"] == "{{secret.RAML_CLIENT_SECRET}}" {
					credentials++
				}
				if action.Body == "{}" || action.Body == "null" {
					empty++
					if action.Metadata["execution_blocker"] == nil {
						t.Fatal("incomplete body allowed to replay")
					}
				}
				if action.Target == "/billing/v1/pdf" && action.Assertions[0].Expected != 201 {
					t.Fatal("trait response status lost")
				}
			}
			if credentials != tc.credentialActions || result.Accounting().ReviewOnly != tc.blocked {
				t.Fatalf("credentials=%d reviewOnly=%d", credentials, result.Accounting().ReviewOnly)
			}
			if strings.Contains(tc.name, "orders-api") && result.Script.Actions[0].Target != "/orders?customerId=CUST-5001&status=SHIPPED" {
				t.Fatal("order filters dropped")
			}
			if strings.Contains(tc.name, "profile-crm") {
				if empty != 2 || strings.Contains(result.Summary.BaseURL, ".com//") {
					t.Fatal("body/base URL regression")
				}
				found := false
				for _, action := range result.Script.Actions {
					if action.Target == "/v1/passcode?mode=reset" {
						found = true
					}
				}
				if !found {
					t.Fatal("required passcode mode dropped")
				}
			}
			t.Logf("methods=%d bodies=%d credentialActions=%d reviewOnly=%d", tc.actions, tc.bodies, credentials, result.Accounting().ReviewOnly)
		})
	}
}

func TestRAMLReferenceAndSampleBudgets(t *testing.T) {
	t.Run("excessive includes", func(t *testing.T) {
		files := map[string][]byte{}
		for i := 0; i < 24; i++ {
			files[fmt.Sprintf("part%d.raml", i)] = []byte(fmt.Sprintf("get: !include part%d.raml", i+1))
		}
		_, err := Convert(ImportRequest{Content: "#%RAML 1.0\ntitle: Depth\n/items: !include part0.raml\n", Files: files})
		if err == nil || !strings.Contains(err.Error(), "depth") {
			t.Fatalf("unbounded includes: %v", err)
		}
	})
	t.Run("bounded type DAG", func(t *testing.T) {
		types := map[string]any{"Leaf": map[string]any{"example": "leaf"}}
		previous := "Leaf"
		for i := 0; i < 15; i++ {
			name := fmt.Sprintf("Node%d", i)
			types[name] = map[string]any{"properties": map[string]any{"left": previous, "right": previous}}
			previous = name
		}
		state := &ramlSample{active: map[string]bool{}, remaining: 100}
		_, complete := sampleRAMLType(previous, types, state, 0)
		if complete || state.remaining >= 0 {
			t.Fatal("type expansion did not stop at structural budget")
		}
	})
	t.Run("missing array item", func(t *testing.T) {
		_, complete := sampleRAMLType(map[string]any{"type": "array"}, nil, &ramlSample{active: map[string]bool{}, remaining: 100}, 0)
		if complete {
			t.Fatal("array without an item example claimed complete")
		}
	})
}

func TestRAMLArrayRequestFieldsRequireWireReview(t *testing.T) {
	result, err := Convert(ImportRequest{Content: "#%RAML 1.0\ntitle: Array\n/items:\n  get:\n    queryParameters:\n      ids: {type: 'string[]', example: [one, two]}\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script.Actions[0].Target != "/items" || result.Accounting().ReviewOnly != 1 {
		t.Fatal("array field was silently stringified")
	}
}

func TestRAMLEmptyExampleWarnsAndBlocksMissingRequestSample(t *testing.T) {
	result, err := Convert(ImportRequest{Content: "#%RAML 1.0\ntitle: Empty example\n/items:\n  post:\n    body:\n      application/json:\n        type: string\n        example: !include empty.json\n", Files: map[string][]byte{"empty.json": {}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || result.Accounting().ReviewOnly != 1 {
		t.Fatal("empty request example was not surfaced for review")
	}
}

func TestRAMLEmptyDocumentDoesNotPanic(t *testing.T) {
	for _, content := range []string{"", " \n", "#%RAML 1.0\n"} {
		if _, err := Convert(ImportRequest{Content: content}); err == nil {
			t.Fatal("empty document accepted")
		}
	}
}

func TestRAMLResourceTraitsStayScopedToTheirMethods(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `#%RAML 1.0
title: Trait Scopes
traits:
  parent: {headers: {source: {example: parent}}}
  own: {headers: {source: {example: child}}}
resourceTypes:
  collection:
    is: [parent]
    get: {}
/parent:
  type: collection
  /child:
    get:
      is: [own]
`})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 || result.Script.Actions[0].Headers["source"] != "parent" || result.Script.Actions[1].Headers["source"] != "child" {
		t.Fatal("resource traits did not respect method scopes")
	}
}
