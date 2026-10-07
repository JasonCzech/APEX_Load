package importcontract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	k6import "github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/wsdl"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestBaselineNormalizedContracts(t *testing.T) {
	for _, importer := range baselineImporters() {
		t.Run(importer.name, func(t *testing.T) {
			result := importer.convertFixture(t, loadBaselineFixture(t, importer.fixture))
			stable := normalizeConversion(t, result)

			if result.script.Protocol != importer.protocol {
				t.Fatalf("protocol = %q, want %q", result.script.Protocol, importer.protocol)
			}
			if result.script.Config.BaseURL == "" {
				t.Fatal("base URL is empty")
			}
			if len(result.script.Actions) != 1 {
				t.Fatalf("actions = %d, want 1", len(result.script.Actions))
			}
			action := result.script.Actions[0]
			if action.Method != "POST" {
				t.Fatalf("method = %q, want POST", action.Method)
			}
			if action.Target != importer.target {
				t.Fatalf("target = %q, want %q", action.Target, importer.target)
			}
			if stable.Accounting != (conversionAccounting{Detected: 1, Converted: 1}) {
				t.Fatalf("accounting = %+v, want detected:1 converted:1 skipped:0 review_only:0", stable.Accounting)
			}
			if result.sourceReview == nil || len(result.sourceReview.SourceFiles) == 0 || len(result.sourceReview.Mappings) == 0 {
				t.Fatal("source review must contain at least one source file and one mapping")
			}
			if len(stable.ValidationIssues) != 0 {
				t.Fatalf("generated script validation issues: %v", stable.ValidationIssues)
			}
			assertDispositionConservation(t, stable.Accounting)
			assertDispositionMappings(t, result.sourceReview, stable.Accounting)
		})
	}
}

// frozenImportContractHashes are C0 compatibility baselines over each
// converter's complete JSON-visible ConvertResult plus shared validation and
// accounting. Any update must be reviewed as an intentional importer delta.
var frozenImportContractHashes = map[string]string{
	"har":      "418097ed4a2784af2f85a88f180c744db2f7ef5b7a00bfff24c3eb540f28ecfa",
	"jmeter":   "80b9eb2a7b751512db19bcefcee3416afb25480635e4baf4163b42d21d85b3c3",
	"k6":       "83fa7df58e867e39d7544c6b4c0a5124ede5ae3b4a3ebcfb51de6a7736b69c59",
	"openapi":  "282ad40974d2d7ecc3dda22a77b490e2a36149cb0584e7dc43722639c8780041",
	"postman":  "fa2639cd6e22229d0b471fc02dc9a79a29c21ed7a6bb13e23e00b368e17ca38a",
	"raw_http": "753109927bceb2da9bbd6dcff345c41a7d6ea87f6e0582d780cd896c06ce0e7e",
	"wsdl":     "ab668d0db72d276a0c6f7fd526f08c274278225ea025e2e9a4ac574c804b160e",
}

func TestFrozenImportContractsAreExactAndDeterministic(t *testing.T) {
	const repetitions = 25
	for _, importer := range baselineImporters() {
		t.Run(importer.name, func(t *testing.T) {
			content := loadBaselineFixture(t, importer.fixture)
			wantHash := frozenImportContractHashes[importer.name]
			if wantHash == "" {
				t.Fatalf("no committed frozen contract hash for %s", importer.name)
			}
			if importer.name == "jmeter" {
				requireIntentionalDelta(t, "jmeter_stable_source_identity")
				requireIntentionalDelta(t, "jmeter_normalized_review_evidence")
			}
			if importer.name == "wsdl" {
				requireIntentionalDelta(t, "wsdl_incomplete_skeletons_are_blocked")
			}
			if importer.name == "raw_http" {
				requireIntentionalDelta(t, "raw_http_explicit_http11_runtime_mode")
			}
			if importer.name == "har" {
				requireIntentionalDelta(t, "har_request_http_version_fidelity")
			}
			if importer.name == "postman" {
				requireIntentionalDelta(t, "postman_source_file_provenance")
			}
			requireIntentionalDelta(t, "credential_preserving_import_marker")
			requireIntentionalDelta(t, "script_results_contract")
			if importer.name != "wsdl" {
				requireIntentionalDelta(t, "http_imports_disable_implicit_traceparent")
			}
			for run := 0; run < repetitions; run++ {
				stable := normalizeConversion(t, importer.convertFixture(t, content))
				encoded, err := json.Marshal(stable)
				if err != nil {
					t.Fatalf("marshal normalized conversion: %v", err)
				}
				gotHash := fmt.Sprintf("%x", sha256.Sum256(encoded))
				if gotHash != wantHash {
					t.Fatalf("normalized contract hash on run %d = %s, want %s\nnormalized result: %s", run+1, gotHash, wantHash, encoded)
				}
			}
		})
	}
}

func TestFrozenNormalizationPreservesSemanticMetadataAndStableIDs(t *testing.T) {
	script := map[string]any{
		"id":         "generated-script-id",
		"ref_key":    "generated-ref-key",
		"version":    "1.0.0",
		"protocol":   "http",
		"config":     map[string]any{"base_url": "https://api.contract.test"},
		"actions":    []any{},
		"created_at": "2026-07-26T12:00:00Z",
		"updated_at": "2026-07-26T12:01:00Z",
		"metadata": map[string]any{"import": map[string]any{
			"source": "jmeter",
			"preserved": map[string]any{
				"imported_at":     "2026-07-26T12:00:00Z",
				"collection_name": "semantic-metadata",
			},
		}},
	}
	value := map[string]any{
		"script":      script,
		"finding":     map[string]any{"id": "stable-finding-id"},
		"mapping":     map[string]any{"id": "stable-mapping-id"},
		"requirement": map[string]any{"id": "stable-requirement-id"},
		"source":      map[string]any{"created_at": "source-authored-timestamp"},
	}

	normalizeGeneratedPublicFields(value)

	if script["id"] != "<generated-script-id>" || script["ref_key"] != "<generated-script-ref-key>" {
		t.Fatalf("script identity was not normalized: id=%v ref_key=%v", script["id"], script["ref_key"])
	}
	if script["created_at"] != "<generated-timestamp>" || script["updated_at"] != "<generated-timestamp>" {
		t.Fatalf("script timestamps were not normalized: created_at=%v updated_at=%v", script["created_at"], script["updated_at"])
	}
	preserved := script["metadata"].(map[string]any)["import"].(map[string]any)["preserved"].(map[string]any)
	if preserved["imported_at"] != "<generated-timestamp>" || preserved["collection_name"] != "semantic-metadata" {
		t.Fatalf("import metadata normalization changed semantic fields: %#v", preserved)
	}
	for field, want := range map[string]string{
		"finding":     "stable-finding-id",
		"mapping":     "stable-mapping-id",
		"requirement": "stable-requirement-id",
	} {
		if got := value[field].(map[string]any)["id"]; got != want {
			t.Fatalf("%s ID = %v, want %q", field, got, want)
		}
	}
	if got := value["source"].(map[string]any)["created_at"]; got != "source-authored-timestamp" {
		t.Fatalf("source-authored timestamp = %v, want it preserved", got)
	}
}

func assertDispositionConservation(t *testing.T, accounting conversionAccounting) {
	t.Helper()
	if accounting.Detected < 0 || accounting.Converted < 0 || accounting.Skipped < 0 || accounting.ReviewOnly < 0 {
		t.Fatalf("import accounting contains a negative count: %+v", accounting)
	}
	classified := accounting.Converted + accounting.Skipped + accounting.ReviewOnly
	if accounting.Detected != classified {
		t.Fatalf("import accounting does not conserve detected items: detected=%d converted=%d skipped=%d review_only=%d",
			accounting.Detected, accounting.Converted, accounting.Skipped, accounting.ReviewOnly)
	}
}

func assertDispositionMappings(t *testing.T, review *sourceview.Review, accounting conversionAccounting) {
	t.Helper()
	type mappingCounts struct {
		converted  int
		skipped    int
		reviewOnly int
	}
	var counts mappingCounts
	if review != nil {
		for _, mapping := range review.Mappings {
			targetPath := strings.TrimSpace(mapping.TargetPath)
			targetKind := strings.ToLower(strings.TrimSpace(mapping.TargetKind))
			status := strings.ToLower(strings.TrimSpace(mapping.Status))
			switch {
			case targetKind == "action" && targetPath != "" && status == "converted":
				counts.converted++
			case targetKind == "action" && targetPath != "" && status == "review_only":
				counts.reviewOnly++
			case targetPath == "" && status == "review_only" &&
				(targetKind == "action" || targetKind == "request" || targetKind == "operation" || targetKind == "sampler"):
				counts.skipped++
			}
		}
	}
	if counts.converted < accounting.Converted {
		t.Fatalf("converted disposition mappings = %d, want at least %d", counts.converted, accounting.Converted)
	}
	if counts.skipped < accounting.Skipped {
		t.Fatalf("skipped disposition mappings = %d, want at least %d", counts.skipped, accounting.Skipped)
	}
	if counts.reviewOnly < accounting.ReviewOnly {
		t.Fatalf("review-only disposition mappings = %d, want at least %d", counts.reviewOnly, accounting.ReviewOnly)
	}
}

type headerObservation struct {
	Summary []dsl.HeaderEntry
	Ordered []dsl.HeaderEntry
}

func observeHeaders(action dsl.Action) headerObservation {
	keys := make([]string, 0, len(action.Headers))
	for name := range action.Headers {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	summary := make([]dsl.HeaderEntry, 0, len(keys))
	for _, name := range keys {
		summary = append(summary, dsl.HeaderEntry{Name: name, Value: action.Headers[name]})
	}
	return headerObservation{
		Summary: summary,
		Ordered: append([]dsl.HeaderEntry(nil), action.HeaderList...),
	}
}

func TestHeaderOccurrenceContractsCharacterizeCurrentDeltas(t *testing.T) {
	const raw = "GET https://api.contract.test/orders HTTP/1.1\nX-Dupe: first\nX-Dupe: second\n\n"
	rawResult, err := sample.Convert(sample.ImportRequest{Format: "raw_http", Content: raw})
	if err != nil {
		t.Fatalf("raw HTTP conversion: %v", err)
	}

	harResult, err := har.Convert([]byte(`{
	  "log": {"version": "1.2", "creator": {"name": "contract", "version": "1"}, "entries": [{
	    "startedDateTime": "2026-01-02T03:04:05Z", "time": 1,
	    "request": {
	      "method": "GET", "url": "https://api.contract.test/orders", "httpVersion": "HTTP/1.1",
	      "headers": [{"name":"X-Dupe","value":"first"},{"name":"X-Dupe","value":"second"}],
	      "queryString": [], "cookies": [], "headersSize": -1, "bodySize": 0
	    },
	    "response": {
	      "status": 200, "statusText": "OK", "httpVersion": "HTTP/1.1",
	      "headers": [], "cookies": [], "content": {"size": 0, "mimeType": "text/plain", "text": ""},
	      "redirectURL": "", "headersSize": -1, "bodySize": 0
	    },
	    "cache": {}, "timings": {"send":0,"wait":1,"receive":0}
	  }]}
	}`), har.ConvertOptions{})
	if err != nil {
		t.Fatalf("HAR conversion: %v", err)
	}

	postmanResult, err := postman.Convert(postman.ImportRequest{Content: `{
	  "info":{"name":"headers"},
	  "item":[{"name":"dupes","request":{"method":"GET","url":"https://api.contract.test/orders",
	    "header":[{"key":"X-Dupe","value":"first"},{"key":"X-Dupe","value":"second"}]}}]
	}`})
	if err != nil {
		t.Fatalf("Postman conversion: %v", err)
	}

	jmeterResult, err := jmeter.ConvertJMX([]byte(`<?xml version="1.0"?>
	<jmeterTestPlan><hashTree><TestPlan testname="headers"/><hashTree>
	  <ThreadGroup testname="users"/><hashTree>
	    <HeaderManager testname="headers"><collectionProp name="HeaderManager.headers">
	      <elementProp elementType="Header"><stringProp name="Header.name">X-Dupe</stringProp><stringProp name="Header.value">first</stringProp></elementProp>
	      <elementProp elementType="Header"><stringProp name="Header.name">X-Dupe</stringProp><stringProp name="Header.value">second</stringProp></elementProp>
	    </collectionProp></HeaderManager><hashTree/>
	    <HTTPSamplerProxy testname="dupes">
	      <stringProp name="HTTPSampler.domain">api.contract.test</stringProp>
	      <stringProp name="HTTPSampler.protocol">https</stringProp>
	      <stringProp name="HTTPSampler.path">/orders</stringProp>
	      <stringProp name="HTTPSampler.method">GET</stringProp>
	    </HTTPSamplerProxy><hashTree/>
	  </hashTree>
	</hashTree></hashTree></jmeterTestPlan>`), jmeter.ConvertOptions{})
	if err != nil {
		t.Fatalf("JMeter conversion: %v", err)
	}

	k6Result, err := k6import.Convert(k6import.ImportRequest{Content: `
	  http.get('https://api.contract.test/orders', {
	    headers: {'X-Dupe': 'first', 'X-Dupe': 'second'}
	  });
	`})
	if err != nil {
		t.Fatalf("k6 conversion: %v", err)
	}

	preserving := map[string]dsl.Action{
		"raw_http": rawResult.Script.Actions[0],
		"har":      harResult.Script.Actions[0],
		"postman":  postmanResult.Script.Actions[0],
		"jmeter":   jmeterResult.Scripts[0].Actions[0],
	}
	wantOrdered := []dsl.HeaderEntry{
		{Name: "X-Dupe", Value: "first"},
		{Name: "X-Dupe", Value: "second"},
	}
	for name, action := range preserving {
		t.Run(name+"_preserves_occurrences", func(t *testing.T) {
			got := observeHeaders(action)
			if !reflect.DeepEqual(got.Ordered, wantOrdered) {
				t.Fatalf("ordered headers = %#v, want %#v", got.Ordered, wantOrdered)
			}
			if value := action.Headers["X-Dupe"]; value != "second" {
				t.Fatalf("summary X-Dupe = %q, want last-occurrence compatibility summary %q", value, "second")
			}
		})
	}

	requireIntentionalDelta(t, "k6_map_headers_gain_deterministic_header_list")
	k6Headers := observeHeaders(k6Result.Script.Actions[0])
	if want := []dsl.HeaderEntry{{Name: "X-Dupe", Value: "second"}}; !reflect.DeepEqual(k6Headers.Ordered, want) {
		t.Fatalf("k6 ordered headers = %#v, want deterministic source-map projection %#v", k6Headers.Ordered, want)
	}
	if value := k6Result.Script.Actions[0].Headers["X-Dupe"]; value != "second" {
		t.Fatalf("k6 summary X-Dupe = %q, want last-wins value %q", value, "second")
	}
	requireIntentionalDelta(t, "k6_map_headers_report_source_order_unavailable")
	foundSourceOrderDiagnostic := false
	for _, finding := range k6Result.Findings {
		if finding.Code == "source_order_unavailable" {
			foundSourceOrderDiagnostic = true
			break
		}
	}
	if !foundSourceOrderDiagnostic {
		t.Fatalf("k6 findings = %#v, want source_order_unavailable", k6Result.Findings)
	}
}

type authObservation struct {
	DefaultType string
	ActionType  string
	ReviewOnly  string
	Warned      bool
}

func observeAuth(script *dsl.Script, diagnostics diag.Diagnostics) authObservation {
	observation := authObservation{DefaultType: script.Config.HTTP.Auth.Type}
	if len(script.Actions) > 0 {
		action := script.Actions[0]
		if action.Auth != nil {
			observation.ActionType = action.Auth.Type
		}
		if value, ok := action.Metadata["postman_auth_review_only"].(string); ok {
			observation.ReviewOnly = value
		}
	}
	for _, item := range diagnostics {
		if strings.Contains(strings.ToLower(item.String()), "digest") {
			observation.Warned = true
		}
	}
	return observation
}

func TestDigestAuthContractCharacterizesImporterDelta(t *testing.T) {
	postmanResult, err := postman.Convert(postman.ImportRequest{Content: `{
	  "info":{"name":"digest"},
	  "auth":{"type":"digest","digest":[
	    {"key":"username","value":"user"},{"key":"password","value":"pass"}
	  ]},
	  "item":[{"name":"request","request":{"method":"GET","url":"https://api.contract.test/private"}}]
	}`})
	if err != nil {
		t.Fatalf("Postman conversion: %v", err)
	}

	jmeterResult, err := jmeter.ConvertJMX([]byte(`<?xml version="1.0"?>
	<jmeterTestPlan><hashTree><TestPlan testname="digest"/><hashTree>
	  <ThreadGroup testname="users"/><hashTree>
	    <AuthManager testname="auth"><collectionProp name="AuthManager.auth_list">
	      <elementProp elementType="Authorization">
	        <stringProp name="Authorization.url">https://api.contract.test/private</stringProp>
	        <stringProp name="Authorization.username">user</stringProp>
	        <stringProp name="Authorization.password">pass</stringProp>
	        <stringProp name="Authorization.mechanism">DIGEST</stringProp>
	      </elementProp>
	    </collectionProp></AuthManager><hashTree/>
	    <HTTPSamplerProxy testname="request">
	      <stringProp name="HTTPSampler.domain">api.contract.test</stringProp>
	      <stringProp name="HTTPSampler.protocol">https</stringProp>
	      <stringProp name="HTTPSampler.path">/private</stringProp>
	      <stringProp name="HTTPSampler.method">GET</stringProp>
	    </HTTPSamplerProxy><hashTree/>
	  </hashTree>
	</hashTree></hashTree></jmeterTestPlan>`), jmeter.ConvertOptions{})
	if err != nil {
		t.Fatalf("JMeter conversion: %v", err)
	}

	openAPIResult, err := openapi.Convert(openapi.ImportRequest{Content: `{
	  "openapi":"3.0.3",
	  "info":{"title":"digest","version":"1"},
	  "servers":[{"url":"https://api.contract.test"}],
	  "components":{"securitySchemes":{"digestAuth":{"type":"http","scheme":"digest"}}},
	  "security":[{"digestAuth":[]}],
	  "paths":{"/private":{"get":{"operationId":"private","responses":{"200":{"description":"ok"}}}}}
	}`})
	if err != nil {
		t.Fatalf("OpenAPI conversion: %v", err)
	}

	got := map[string]authObservation{
		"postman": observeAuth(postmanResult.Script, postmanResult.Unsupported),
		"jmeter":  observeAuth(jmeterResult.Scripts[0], jmeterResult.Unsupported),
		"openapi": observeAuth(openAPIResult.Script, openAPIResult.Unsupported),
	}
	want := map[string]authObservation{
		"postman": {ActionType: "digest"},
		"jmeter":  {ActionType: "digest"},
		"openapi": {ActionType: "digest"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("digest projections = %#v, want current importer delta %#v", got, want)
	}
	if issues := dsl.ValidateScriptIssues(openAPIResult.Script); len(issues) != 0 {
		t.Fatalf("OpenAPI digest projection should be DSL-valid, issues: %v", issues)
	}
}

const credentialCanary = "C0-credential-canary-8f204b5d"

func executableMaterial(script *dsl.Script) []byte {
	material := struct {
		Config   dsl.ScriptConfig
		Init     []dsl.Action
		Actions  []dsl.Action
		Teardown []dsl.Action
	}{
		Config:   script.Config,
		Init:     script.Init,
		Actions:  script.Actions,
		Teardown: script.Teardown,
	}
	encoded, _ := json.Marshal(material)
	return encoded
}

func TestCredentialDispositionContractCharacterizesCurrentDeltas(t *testing.T) {
	postmanResult, err := postman.Convert(postman.ImportRequest{Content: fmt.Sprintf(`{
	  "info":{"name":"credential"},
	  "item":[{"name":"login","request":{
	    "method":"POST","url":"https://api.contract.test/login",
	    "header":[{"key":"Authorization","value":"Bearer %s"}],
	    "body":{"mode":"raw","raw":"{\"password\":\"%s\"}"}
	  }}]
	}`, credentialCanary, credentialCanary)})
	if err != nil {
		t.Fatalf("Postman conversion: %v", err)
	}

	rawResult, err := sample.Convert(sample.ImportRequest{
		Format: "raw_http",
		Content: fmt.Sprintf(
			"POST https://api.contract.test/login HTTP/1.1\nAuthorization: Bearer %s\nContent-Type: application/json\n\n{\"password\":\"%s\"}",
			credentialCanary,
			credentialCanary,
		),
	})
	if err != nil {
		t.Fatalf("raw HTTP conversion: %v", err)
	}

	harResult, err := har.Convert([]byte(fmt.Sprintf(`{
	  "log":{"version":"1.2","creator":{"name":"contract","version":"1"},"entries":[{
	    "startedDateTime":"2026-01-02T03:04:05Z","time":1,
	    "request":{"method":"POST","url":"https://api.contract.test/login","httpVersion":"HTTP/1.1",
	      "headers":[{"name":"Authorization","value":"Bearer %s"},{"name":"Content-Type","value":"application/json"}],
	      "queryString":[],"cookies":[],"headersSize":-1,"bodySize":1,
	      "postData":{"mimeType":"application/json","text":"{\"password\":\"%s\"}"}},
	    "response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],"cookies":[],
	      "content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},
	    "cache":{},"timings":{"send":0,"wait":1,"receive":0}
	  }]}}
	`, credentialCanary, credentialCanary)), har.ConvertOptions{})
	if err != nil {
		t.Fatalf("HAR conversion: %v", err)
	}

	jmeterResult, err := jmeter.ConvertJMX([]byte(fmt.Sprintf(`<?xml version="1.0"?>
	<jmeterTestPlan><hashTree><TestPlan testname="credential"/><hashTree>
	  <ThreadGroup testname="users"/><hashTree>
	    <HeaderManager testname="headers"><collectionProp name="HeaderManager.headers">
	      <elementProp elementType="Header"><stringProp name="Header.name">Authorization</stringProp><stringProp name="Header.value">Bearer %s</stringProp></elementProp>
	    </collectionProp></HeaderManager><hashTree/>
	    <HTTPSamplerProxy testname="login">
	      <stringProp name="HTTPSampler.domain">api.contract.test</stringProp>
	      <stringProp name="HTTPSampler.protocol">https</stringProp>
	      <stringProp name="HTTPSampler.path">/login</stringProp>
	      <stringProp name="HTTPSampler.method">POST</stringProp>
	      <boolProp name="HTTPSampler.postBodyRaw">true</boolProp>
	      <elementProp name="HTTPsampler.Arguments" elementType="Arguments"><collectionProp name="Arguments.arguments">
	        <elementProp elementType="HTTPArgument"><stringProp name="Argument.value">{&quot;password&quot;:&quot;%s&quot;}</stringProp></elementProp>
	      </collectionProp></elementProp>
	    </HTTPSamplerProxy><hashTree/>
	  </hashTree>
	</hashTree></hashTree></jmeterTestPlan>`, credentialCanary, credentialCanary)), jmeter.ConvertOptions{})
	if err != nil {
		t.Fatalf("JMeter conversion: %v", err)
	}

	openAPIResult, err := openapi.Convert(openapi.ImportRequest{Content: fmt.Sprintf(`{
	  "openapi":"3.0.3","info":{"title":"credential","version":"1"},
	  "servers":[{"url":"https://api.contract.test"}],
	  "paths":{"/login":{"post":{"operationId":"login","requestBody":{"content":{"application/json":{
	    "example":{"password":"%s"}
	  }}},"responses":{"200":{"description":"ok"}}}}}
	}`, credentialCanary)})
	if err != nil {
		t.Fatalf("OpenAPI conversion: %v", err)
	}

	wsdlResult, err := wsdl.Convert(wsdl.ImportRequest{Content: credentialWSDL(credentialCanary)})
	if err != nil {
		t.Fatalf("WSDL conversion: %v", err)
	}

	k6Result, err := k6import.Convert(k6import.ImportRequest{Content: fmt.Sprintf(`
	  http.post(
	    'https://api.contract.test/login',
	    '{"password":"%s"}',
	    {headers: {'Authorization':'Bearer %s'}}
	  );
	`, credentialCanary, credentialCanary)})
	if err != nil {
		t.Fatalf("k6 conversion: %v", err)
	}

	scripts := map[string]*dsl.Script{
		"postman":  postmanResult.Script,
		"raw_http": rawResult.Script,
		"har":      harResult.Script,
		"jmeter":   jmeterResult.Scripts[0],
		"openapi":  openAPIResult.Script,
		"wsdl":     wsdlResult.Script,
		"k6":       k6Result.Script,
	}
	wantExecutableCanary := map[string]bool{
		"postman":  true,
		"raw_http": true,
		"har":      true,
		"jmeter":   true,
		"openapi":  true,
		"wsdl":     true,
		"k6":       true,
	}
	requireIntentionalDelta(t, "postman_http_credentials_use_shared_projection")
	requireIntentionalDelta(t, "jmeter_http_credentials_use_shared_projection")
	requireIntentionalDelta(t, "k6_http_credentials_use_shared_projection")
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			got := strings.Contains(string(executableMaterial(script)), credentialCanary)
			if got != wantExecutableCanary[name] {
				t.Fatalf("executable material contains canary = %v, want current characterization %v\n%s", got, wantExecutableCanary[name], executableMaterial(script))
			}
		})
	}
}

var intentionalDeltaAllowlist = map[string]string{
	"credential_preserving_import_marker":            "Finalized imports carry a stable policy marker so operator-supplied values remain exact across scripts, source review, persistence, and authorized authoring reads.",
	"postman_http_credentials_use_shared_projection": "Postman HTTP headers, typed auth, and structured bodies now reach the source-neutral credential projector before any compatibility rewriting.",
	"postman_source_file_provenance":                 "Postman results identify the collection and selected environment source files so credential guidance can name the exact contributing artifact.",
	"jmeter_http_credentials_use_shared_projection":  "JMeter HTTP headers and bodies now use the source-neutral credential projector.",
	"k6_http_credentials_use_shared_projection":      "Static k6 HTTP literals now use the source-neutral credential projector.",
	"k6_map_headers_gain_deterministic_header_list":  "k6 object headers expose only last-wins map semantics; the shared finalizer now emits their deterministic ordered compatibility list.",
	"k6_map_headers_report_source_order_unavailable": "k6 JavaScript object headers cannot retain source occurrence order, so the shared bridge now emits the stable source_order_unavailable informational finding.",
	"wsdl_incomplete_skeletons_are_blocked":          "WSDL operations with unresolved input, header, or XSD structure now produce valid durable review-gated drafts instead of runnable placeholder envelopes.",
	"raw_http_explicit_http11_runtime_mode":          "A raw capture containing only HTTP/1.1 requests now selects exact native HTTP/1.1 mode so connection-specific header semantics remain faithful.",
	"har_request_http_version_fidelity":              "HAR request protocol versions now reach the shared request model and select an exact script-wide native version when every request agrees.",
	"http_imports_disable_implicit_traceparent":      "HTTP-producing imports without an explicit source choice persist traceparent=false so runtime defaults cannot add an unauthored wire header.",
	"jmeter_normalized_review_evidence":              "JMeter returns source-qualified structured diagnostics and separate behavior-level review counts, restricts unsupported elements to unrepresentable behavior, and retains normalized evidence in new draft provenance.",
	"jmeter_stable_source_identity":                  "JMeter provenance retains content-addressed plan and source group identities, stable source mapping IDs, and separate workload and contributor counts for regeneration and scoped compatibility behavior.",
	"script_results_contract":                        "The optional bounded result-detail policy adds an empty results object to imported script configuration while preserving legacy runtime behavior.",
}

func requireIntentionalDelta(t *testing.T, key string) {
	t.Helper()
	if strings.TrimSpace(intentionalDeltaAllowlist[key]) == "" {
		t.Fatalf("intentional importer delta %q is not allowlisted", key)
	}
}

func credentialWSDL(canary string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
	<definitions name="Credential" targetNamespace="urn:credential"
	  xmlns="http://schemas.xmlsoap.org/wsdl/"
	  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
	  xmlns:tns="urn:credential"
	  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
	  <types><xsd:schema targetNamespace="urn:credential">
	    <xsd:element name="LoginRequest" type="tns:LoginRequestType"/>
	    <xsd:complexType name="LoginRequestType"><xsd:sequence>
	      <xsd:element name="password" type="xsd:string" default="%s"/>
	    </xsd:sequence></xsd:complexType>
	  </xsd:schema></types>
	  <message name="LoginInput"><part name="parameters" element="tns:LoginRequest"/></message>
	  <portType name="CredentialPortType"><operation name="Login"><input message="tns:LoginInput"/></operation></portType>
	  <binding name="CredentialBinding" type="tns:CredentialPortType">
	    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
	    <operation name="Login"><soap:operation soapAction="urn:Login"/></operation>
	  </binding>
	  <service name="CredentialService"><port name="CredentialPort" binding="tns:CredentialBinding">
	    <soap:address location="https://api.contract.test/soap"/>
	  </port></service>
	</definitions>`, canary)
}

type gateObservation struct {
	ReviewActions     int
	ExecutionBlockers int
}

func observeGates(script *dsl.Script) gateObservation {
	var observation gateObservation
	visit := func(actions []dsl.Action) {
		for _, action := range actions {
			if action.Type == "review_required" {
				observation.ReviewActions++
			}
			if metadataHasExecutionBlocker(action.Metadata) {
				observation.ExecutionBlockers++
			}
		}
	}
	visit(script.Init)
	visit(script.Actions)
	visit(script.Teardown)
	return observation
}

func metadataHasExecutionBlocker(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}
	value, ok := metadata["execution_blocker"]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return value != nil
	}
}

func hasDiagnostic(diagnostics diag.Diagnostics, element string) bool {
	for _, item := range diagnostics {
		if strings.Contains(strings.ToLower(item.Element), strings.ToLower(element)) {
			return true
		}
	}
	return false
}

func TestReviewGateContractRequiresDurableBlockersForOmittedBehavior(t *testing.T) {
	postmanResult, err := postman.Convert(postman.ImportRequest{Content: `{
	  "info":{"name":"scripted"},
	  "item":[{"name":"signed","event":[{"listen":"prerequest","script":{
	    "type":"text/javascript","exec":["const signature = sign(pm.request.url);"]
	  }}],"request":{"method":"GET","url":"https://api.contract.test/orders"}}]
	}`})
	if err != nil {
		t.Fatalf("Postman conversion: %v", err)
	}
	if !hasDiagnostic(postmanResult.Unsupported, "Postman pre-request script") {
		t.Fatalf("Postman diagnostics = %#v, want pre-request review evidence", postmanResult.Unsupported)
	}
	if got := observeGates(postmanResult.Script); got != (gateObservation{ExecutionBlockers: 1}) {
		t.Fatalf("Postman gates = %#v, want one durable request blocker", got)
	}

	jmeterResult, err := jmeter.ConvertJMX([]byte(`<?xml version="1.0"?>
	<jmeterTestPlan><hashTree><TestPlan testname="scripted"/><hashTree>
	  <ThreadGroup testname="users"/><hashTree>
	    <JSR223Sampler testname="setup">
	      <stringProp name="scriptLanguage">groovy</stringProp>
	      <stringProp name="script">def token = api.fetch(); vars.put("kept", token)</stringProp>
	    </JSR223Sampler><hashTree/>
	    <HTTPSamplerProxy testname="home">
	      <stringProp name="HTTPSampler.domain">api.contract.test</stringProp>
	      <stringProp name="HTTPSampler.protocol">https</stringProp>
	      <stringProp name="HTTPSampler.path">/home</stringProp>
	      <stringProp name="HTTPSampler.method">GET</stringProp>
	    </HTTPSamplerProxy><hashTree/>
	  </hashTree>
	</hashTree></hashTree></jmeterTestPlan>`), jmeter.ConvertOptions{})
	if err != nil {
		t.Fatalf("JMeter conversion: %v", err)
	}
	if jmeterResult.Stats.ReviewRequired == 0 || !hasDiagnostic(jmeterResult.Unsupported, "JMeter JSR223Sampler") {
		t.Fatalf("JMeter review evidence = stats:%d diagnostics:%#v", jmeterResult.Stats.ReviewRequired, jmeterResult.Unsupported)
	}
	if got := observeGates(jmeterResult.Scripts[0]); got != (gateObservation{ReviewActions: 1, ExecutionBlockers: 1}) {
		t.Fatalf("JMeter gates = %#v, want one durable disclosure-safe source placeholder", got)
	}

	k6Result, err := k6import.Convert(k6import.ImportRequest{Content: `
	  for (let i = 0; i < 2; i++) {
	    http.get('https://api.contract.test/orders');
	  }
	`})
	if err != nil {
		t.Fatalf("k6 conversion: %v", err)
	}
	if !hasDiagnostic(k6Result.Unsupported, "k6 JavaScript control flow") {
		t.Fatalf("k6 diagnostics = %#v, want control-flow review evidence", k6Result.Unsupported)
	}
	if got := observeGates(k6Result.Script); got != (gateObservation{ReviewActions: 1, ExecutionBlockers: 2}) {
		t.Fatalf("k6 gates = %#v, want request and placeholder execution blockers", got)
	}
}
