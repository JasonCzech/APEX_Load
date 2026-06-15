package devweb

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertHTTPDevWebScript(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Orders DevWeb",
		Content: `
load.action("Create Order", async function() {
  new load.WebRequest({
    url: "https://api.example.test/orders",
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Trace": "devweb" },
    body: "{\"sku\":\"SKU-1\"}"
  }).send();
  load.thinkTime(1.5);
});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script.Protocol != "http" {
		t.Fatalf("protocol = %q, want http", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("base_url = %q", result.Script.Config.BaseURL)
	}
	if result.Stats.ConvertedActions != 1 || result.Stats.HTTPRequests != 1 || result.Stats.ThinkTimes != 1 {
		t.Fatalf("stats = %#v", result.Stats)
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" || action.Target != "https://api.example.test/orders" {
		t.Fatalf("action = %#v", action)
	}
	if action.Headers["Content-Type"] != "application/json" || action.Headers["X-Trace"] != "devweb" {
		t.Fatalf("headers = %#v", action.Headers)
	}
	if action.ThinkTime.MeanMs != 1500 || !action.ThinkTimeAfter {
		t.Fatalf("think time = %#v after=%v", action.ThinkTime, action.ThinkTimeAfter)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) != 1 {
		t.Fatalf("source review = %#v, want one mapping", result.SourceReview)
	}
	if mapping := result.SourceReview.Mappings[0]; mapping.Function != "load.WebRequest" || mapping.TargetPath != "actions[0]" || mapping.Status != "converted" {
		t.Fatalf("source mapping = %#v", mapping)
	}
}

func TestConvertWebSocketDevWebScript(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `
load.action("Socket Flow", async function() {
  const ws = new load.WebSocket("wss://stream.example.test/orders");
  ws.send("{\"type\":\"subscribe\"}");
  ws.receive();
  ws.close();
});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script.Protocol != "websocket" {
		t.Fatalf("protocol = %q, want websocket", result.Script.Protocol)
	}
	if result.Stats.WebSocketActions != 4 || result.Stats.ConvertedActions != 4 {
		t.Fatalf("stats = %#v", result.Stats)
	}
	if got := result.Script.Actions[0].Type; got != "ws_connect" {
		t.Fatalf("first action type = %q", got)
	}
	if got := result.Script.Actions[1].Body; got != `{"type":"subscribe"}` {
		t.Fatalf("send body = %q", got)
	}
}

func TestConvertGRPCDevWebScript(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `
load.action("Cache Script", async function() {
  client.invoke("apexload.v1.GeneratorControl", "DistributeScript", { scriptId: "script-1", contentHash: "hash-1" });
});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script.Protocol != "grpc" {
		t.Fatalf("protocol = %q, want grpc", result.Script.Protocol)
	}
	if !result.Script.Config.GRPC.UseReflection {
		t.Fatal("grpc use_reflection should be enabled")
	}
	action := result.Script.Actions[0]
	if action.Type != "grpc_unary" || action.Target != "apexload.v1.GeneratorControl/DistributeScript" {
		t.Fatalf("action = %#v", action)
	}
	if action.Body != `{ "scriptId": "script-1", "contentHash": "hash-1" }` {
		t.Fatalf("body = %q", action.Body)
	}
}

func TestConvertZipSelectsMainJavaScriptAndPreservesHelpers(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	helper, err := zw.Create("scripts/helper.js")
	if err != nil {
		t.Fatalf("Create helper: %v", err)
	}
	_, _ = helper.Write([]byte(`
module.exports = function() { return { getOrder }; };
function getOrder(orderId) {
  const webRequest0 = new load.WebRequest({
    url: ` + "`https://api.example.test/orders/${orderId}`" + `,
    method: "GET",
    headers: { "accept": "application/json" },
    queryString: { "expand": "items" }
  });
  return webRequest0;
}`))
	file, err := zw.Create("scripts/main.js")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _ = file.Write([]byte(`
const helper = require("./helper.js")();
const orderRequest = helper.getOrder("42");
orderRequest.extractors.push(new load.JsonPathExtractor("order_id", "$.id"));
orderRequest.send();
new load.WebRequest({ url: "https://api.example.test/health" }).send();`))
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.Script.Name != "main" {
		t.Fatalf("script name = %q, want main", result.Script.Name)
	}
	if result.Stats.ConvertedActions != 3 {
		t.Fatalf("converted actions = %d, want helper request, direct request, and module review action", result.Stats.ConvertedActions)
	}
	preserved := result.Script.Metadata.Import.Preserved
	helpers, ok := preserved["helper_modules"].([]string)
	if !ok || len(helpers) != 1 || helpers[0] != "scripts/helper.js" {
		t.Fatalf("helper_modules = %#v, want scripts/helper.js", preserved["helper_modules"])
	}
	templates, ok := preserved["helper_module_request_templates"].([]devWebHelperRequestTemplate)
	if !ok || len(templates) != 1 {
		t.Fatalf("helper_module_request_templates = %#v, want one helper request template", preserved["helper_module_request_templates"])
	}
	template := templates[0]
	if template.Module != "scripts/helper.js" || template.Function != "getOrder" || template.Method != "GET" || template.StaticResolvable {
		t.Fatalf("template = %#v, want dynamic getOrder GET template", template)
	}
	if strings.Join(template.QueryKeys, ",") != "expand" || strings.Join(template.HeaderKeys, ",") != "accept" {
		t.Fatalf("template keys = %#v/%#v, want query/header keys", template.QueryKeys, template.HeaderKeys)
	}
	invocations, ok := preserved["helper_module_invocations"].([]devWebHelperInvocation)
	if !ok || len(invocations) != 1 {
		t.Fatalf("helper_module_invocations = %#v, want one helper invocation", preserved["helper_module_invocations"])
	}
	invocation := invocations[0]
	if invocation.Module != "scripts/helper.js" || invocation.Object != "helper" || invocation.Function != "getOrder" || invocation.AssignedTo != "orderRequest" || !invocation.Sent || !invocation.TemplateInventoried {
		t.Fatalf("invocation = %#v, want helper.getOrder assignment/send/template match", invocation)
	}
	if strings.Join(invocation.ExtractorNames, ",") != "order_id" {
		t.Fatalf("extractor names = %#v, want order_id", invocation.ExtractorNames)
	}
	var helperAction dsl.Action
	for _, action := range result.Script.Actions {
		if action.Metadata["devweb_helper_function"] == "getOrder" {
			helperAction = action
			break
		}
	}
	if helperAction.Target != "https://api.example.test/orders/42?expand=items" || helperAction.Headers["accept"] != "application/json" {
		t.Fatalf("helper action = %#v, want resolved getOrder HTTP request", helperAction)
	}
	if len(helperAction.Extractors) != 1 || helperAction.Extractors[0].Name != "order_id" || helperAction.Extractors[0].Type != "jsonpath" {
		t.Fatalf("helper extractors = %#v, want order_id jsonpath extractor", helperAction.Extractors)
	}
	if !containsDevWebDiagnostic(result, "DevWeb helper module request template") {
		t.Fatalf("diagnostics = %#v, want helper request template warning", result.Unsupported)
	}
	if !containsDevWebDiagnostic(result, "DevWeb helper module request") {
		t.Fatalf("diagnostics = %#v, want converted helper request diagnostic", result.Unsupported)
	}
	if !containsDevWebDiagnostic(result, "DevWeb helper module invocation") {
		t.Fatalf("diagnostics = %#v, want helper invocation warning", result.Unsupported)
	}
}

func TestConvertZipMapsHelperFactoryDirectSendAndLocalConstArgs(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "scripts/helper.js", `
module.exports = function() {
  return {
    getOrder(orderId) {
      return new load.WebRequest({
        url: `+"`https://api.example.test/orders/${orderId}`"+`,
        method: "GET",
        headers: { "accept": "application/json" }
      });
    },
    createOrder: function(orderId, sku) {
      return new load.WebRequest({
        url: `+"`https://api.example.test/orders/${orderId}/items`"+`,
        method: "POST",
        headers: { "content-type": "application/json" },
        body: { "sku": `+"`${sku}`"+` }
      });
    }
  };
};`)
	writeDevWebZipFile(t, zw, "scripts/main.js", `
const helper = require("./helper.js")();
const orderId = "A-42";
const sku = "SKU-9";
helper.getOrder(orderId).sendSync();
const created = helper.createOrder(orderId, sku);
created.send();`)
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	var helperActions []dsl.Action
	for _, action := range result.Script.Actions {
		if action.Metadata["devweb_helper_module"] == "scripts/helper.js" {
			helperActions = append(helperActions, action)
		}
	}
	if len(helperActions) != 2 {
		t.Fatalf("helper actions = %#v, want two converted helper requests", helperActions)
	}
	if helperActions[0].Target != "https://api.example.test/orders/A-42" || helperActions[0].Method != "GET" {
		t.Fatalf("first helper action = %#v, want direct send GET with local const argument", helperActions[0])
	}
	if helperActions[1].Target != "https://api.example.test/orders/A-42/items" || helperActions[1].Method != "POST" {
		t.Fatalf("second helper action = %#v, want assigned POST with local const arguments", helperActions[1])
	}
	if !strings.Contains(helperActions[1].Body, `"sku":"SKU-9"`) {
		t.Fatalf("helper body = %q, want resolved sku constant", helperActions[1].Body)
	}
	preserved := result.Script.Metadata.Import.Preserved
	templates, ok := preserved["helper_module_request_templates"].([]devWebHelperRequestTemplate)
	if !ok || len(templates) != 2 {
		t.Fatalf("helper_module_request_templates = %#v, want two object-method templates", preserved["helper_module_request_templates"])
	}
	invocations, ok := preserved["helper_module_invocations"].([]devWebHelperInvocation)
	if !ok || len(invocations) != 2 {
		t.Fatalf("helper_module_invocations = %#v, want two helper invocations", preserved["helper_module_invocations"])
	}
	if !invocations[0].Sent || !invocations[0].TemplateInventoried || invocations[0].AssignedTo != "" {
		t.Fatalf("direct invocation = %#v, want direct-send inventoried invocation", invocations[0])
	}
}

func TestConvertZipMapsDevWebParametersRuntimeTemplatesExtractorsAndTransactions(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "AOS/main.js", strings.Join([]string{
		`load.action("Action", async function() {`,
		`  const tx = new load.Transaction("Sign In");`,
		`  tx.start();`,
		`  const Login = new load.WebRequest({`,
		"    url: `${load.params.AOS_URL}/account/${load.params.loginUser}`,",
		`    method: "POST",`,
		"    headers: { \"content-type\": \"application/json\", \"authorization\": `Basic ${Login.extractors.token}` },",
		"    body: { \"password\": `${load.params.loginPassword}`, \"token\": `${Login.extractors.token}` },",
		`    queryString: { "quantity": "1" },`,
		`    extractors: [`,
		`      new load.BoundaryExtractor("token", "<token>", "<"),`,
		`      new load.JsonPathExtractor("sessionId", "$.sessionId")`,
		`    ]`,
		`  }).sendSync();`,
		`  if (Login.textCheck("sessionId")) {`,
		`    load.log("ok");`,
		`  }`,
		`  tx.stop();`,
		`});`,
	}, "\n"))
	writeDevWebZipFile(t, zw, "AOS/parameters.yml", `
parameters:
- name: loginUser
  type: csv
  fileName: credentials.csv
  columnName: loginUser
  nextRow: random
  nextValue: iteration
  onEnd: loop
- name: loginPassword
  type: csv
  fileName: credentials.csv
  columnName: loginPassword
  nextRow: same as loginUser
  nextValue: iteration
  onEnd: loop
- name: AOS_URL
  type: csv
  fileName: AOS_URL.csv
  columnName: AOS_URL
  nextRow: sequential
  nextValue: iteration
  onEnd: loop
`)
	writeDevWebZipFile(t, zw, "AOS/credentials.csv", "loginUser,loginPassword\nalice,secret\n")
	writeDevWebZipFile(t, zw, "AOS/AOS_URL.csv", "AOS_URL\nhttps://aos.example.test\n")
	writeDevWebZipFile(t, zw, "AOS/rts.yml", `
httpConnection:
  maxPersistentConnectionsPerHost: 6
  maxRedirectDepth: 10
  connectTimeout: 120
  requestTimeout: 60
replay:
  saveSnapshots: error
  useCache: false
vuserLogger:
  logMode: error
thinkTime:
  type: ignore
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	if result.Stats.DataSources != 2 || len(result.Script.DataSources) != 2 || len(result.Script.Parameters) != 3 {
		t.Fatalf("data sources=%d/%d parameters=%d", result.Stats.DataSources, len(result.Script.DataSources), len(result.Script.Parameters))
	}
	if result.Script.Config.HTTP.ConnectTimeoutMs != 120000 || result.Script.Config.HTTP.RequestTimeoutMs != 60000 || result.Script.Config.HTTP.MaxConnsPerHost != 6 {
		t.Fatalf("http runtime = %#v", result.Script.Config.HTTP)
	}
	if !result.Script.Config.ErrorHandling.SnapshotOnError || !result.Script.Config.Log.OnErrorOnly || !result.Script.Config.ThinkTime.Ignore {
		t.Fatalf("runtime config = %#v", result.Script.Config)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Target, "{{data.AOS_URL.AOS_URL}}/account/{{data.credentials.loginUser}}") || !strings.Contains(action.Target, "quantity=1") {
		t.Fatalf("stats=%#v unsupported=%#v actions=%#v", result.Stats, result.Unsupported, result.Script.Actions)
	}
	if action.Headers["authorization"] != "Basic {{var.token}}" {
		t.Fatalf("headers = %#v", action.Headers)
	}
	if !strings.Contains(action.Body, `"password":"{{data.credentials.loginPassword}}"`) || !strings.Contains(action.Body, `"token":"{{var.token}}"`) {
		t.Fatalf("body = %q", action.Body)
	}
	if len(action.Extractors) != 2 || action.Extractors[0].Type != "boundary" || action.Extractors[1].Type != "jsonpath" {
		t.Fatalf("extractors = %#v", action.Extractors)
	}
	if len(action.Assertions) != 1 || action.Assertions[0].Value != "sessionId" {
		t.Fatalf("assertions = %#v", action.Assertions)
	}
	if action.Metadata["source_transaction"] != "Sign In" {
		t.Fatalf("metadata = %#v, want source transaction", action.Metadata)
	}
}

func TestConvertRejectsUnresolvedQueryStringTemplate(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Dynamic Query",
		Content: "load.action(\"Search\", async function() {\n" +
			"  new load.WebRequest({ url: \"https://api.example.test/search\", queryString: { q: `${Date.now()}` } }).send();\n" +
			"});",
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Stats.HTTPRequests != 0 || !containsString(result.Stats.UnsupportedCalls, "load.WebRequest") {
		t.Fatalf("stats = %#v, want WebRequest skipped for unresolved query template", result.Stats)
	}
	if !containsDevWebDiagnostic(result, "DevWeb load.WebRequest") {
		t.Fatalf("diagnostics = %#v, want WebRequest warning", result.Unsupported)
	}
}

func TestConvertDevWebReportsUnparseableWebRequestAndCorrelationHelpers(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `
load.action("Mixed", async function() {
  const options = { url: dynamicUrl(), method: "POST" };
  new load.WebRequest(options).send();
  new load.WebRequest({ url: "https://api.example.test/health" }).send();
  load.extract("token", "body", /token/);
});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("converted actions = %d, want 2", len(result.Script.Actions))
	}
	if !containsDevWebDiagnostic(result, "DevWeb load.WebRequest") {
		t.Fatalf("diagnostics = %#v, want WebRequest warning", result.Unsupported)
	}
	if !containsDevWebDiagnostic(result, "DevWeb correlation helper") {
		t.Fatalf("diagnostics = %#v, want correlation helper warning", result.Unsupported)
	}
}

func TestConvertDevWebReportsReviewOnlyJavaScriptConstructs(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `
load.action("Dynamic", async function() {
  const helper = require("./helper");
  if (helper.enabled) load.thinkTime(0.1);
  for (let i = 0; i < 2; i++) load.thinkTime(i);
  Promise.resolve().then(helper.sign);
  new load.WebRequest({ url: ` + "`https://api.example.test/orders/${helper.id}`" + ` }).send();
  new load.WebRequest({ url: "https://api.example.test/health" }).send();
});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Script.Actions) != 6 {
		t.Fatalf("converted actions = %d, want 6", len(result.Script.Actions))
	}
	for _, element := range []string{
		"DevWeb dynamic JavaScript",
		"DevWeb custom module",
		"DevWeb async control flow",
		"DevWeb template literal",
		"DevWeb load.WebRequest",
	} {
		if !containsDevWebDiagnostic(result, element) {
			t.Fatalf("diagnostics = %#v, want %s warning", result.Unsupported, element)
		}
	}
}

func containsDevWebDiagnostic(result *ConvertResult, element string) bool {
	for _, item := range result.Unsupported {
		if strings.Contains(item.Element, element) || strings.Contains(item.String(), element) {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func writeDevWebZipFile(t *testing.T, zw *zip.Writer, name string, content string) {
	t.Helper()
	file, err := zw.Create(name)
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	_, _ = file.Write([]byte(content))
}

func TestConvertDevWebCarriesStandingAssumptions(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Sample",
		Content: `load.action("Ping", async function() {
  new load.WebRequest({
    url: "https://api.example.test/ping",
    method: "GET"
  }).sendSync();
});`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Assumptions) == 0 {
		t.Fatal("expected standing assumptions on every DevWeb conversion")
	}
	foundJSCaveat := false
	for _, assumption := range result.Assumptions {
		if strings.Contains(assumption, "does not execute the script's JavaScript") {
			foundJSCaveat = true
		}
	}
	if !foundJSCaveat {
		t.Fatalf("assumptions missing JS-not-executed caveat: %v", result.Assumptions)
	}
}

// TestConvertZipRejectsDecompressionBomb ensures an oversized (highly
// compressible) zip entry is rejected rather than fully materialized in memory.
func TestConvertZipRejectsDecompressionBomb(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("bomb.js")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	zeros := make([]byte, maxDevWebEntryBytes+1) // expands past the per-entry cap
	if _, err := w.Write(zeros); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	if _, err := ConvertZip(buf.Bytes(), ConvertOptions{}); err == nil {
		t.Fatal("expected decompression-bomb rejection")
	} else if !strings.Contains(err.Error(), "decompression limit") {
		t.Fatalf("err = %v, want decompression limit error", err)
	}
}

// TestConvertZipRejectsTooManyEntries ensures an entry-count bomb is rejected.
func TestConvertZipRejectsTooManyEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < maxDevWebZipEntries+1; i++ {
		if _, err := zw.Create(fmt.Sprintf("f%d.txt", i)); err != nil {
			t.Fatalf("zip create: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if _, err := ConvertZip(buf.Bytes(), ConvertOptions{}); err == nil {
		t.Fatal("expected too-many-entries rejection")
	}
}
