package devweb

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
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

func TestConvertAttachesThinkTimeToSourcePredecessor(t *testing.T) {
	result, err := Convert(ImportRequest{
		Content: `
load.action("Ordered waits", async function() {
  new load.WebRequest({ url: "https://api.example.test/first" }).send();
  load.thinkTime(0.1);
  new load.WebRequest({ url: "https://api.example.test/second" }).send();
  load.thinkTime(0.2);
});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	for i, want := range []float64{100, 200} {
		action := result.Script.Actions[i]
		if !action.ThinkTimeAfter || action.ThinkTime.Type != "fixed" || action.ThinkTime.MeanMs != want {
			t.Fatalf("action %d think time = %#v after=%v, want %.0fms after", i, action.ThinkTime, action.ThinkTimeAfter, want)
		}
	}
}

func TestConvertDoesNotInventDefaultThinkTime(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `
load.action("No wait", async function() {
  new load.WebRequest({ url: "https://api.example.test/ping" }).send();
});`})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script.Config.ThinkTime.Type != "" || result.Script.Config.ThinkTime.MeanMs != 0 {
		t.Fatalf("script think time = %#v, want unset", result.Script.Config.ThinkTime)
	}
	if result.Script.Actions[0].ThinkTimeAfter {
		t.Fatal("action unexpectedly has think time")
	}
}

func TestConvertTracksActiveTransactionAcrossLaterDeclarations(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `
load.action("Transactions", async function() {
  const outer = new load.Transaction("Outer");
  outer.start();
  const neverStarted = new load.Transaction("Not active");
  new load.WebRequest({ url: "https://api.example.test/in-outer" }).send();
  outer.stop();
  new load.WebRequest({ url: "https://api.example.test/outside" }).send();
});`})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got := result.Script.Actions[0].Metadata["source_transaction"]; got != "Outer" {
		t.Fatalf("first source transaction = %#v, want Outer", got)
	}
	if got := result.Script.Actions[1].Metadata["source_transaction"]; got != nil {
		t.Fatalf("second source transaction = %#v, want none", got)
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

func TestConvertZipAllocatesCollisionSafeParameterAndDataSourceNames(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "AOS/main.js", `
load.action("Action", async function() {
  await new load.WebRequest({
    url: `+"`https://api.example.test/${load.params.Token}/${load.params.token}`"+`,
    method: "GET",
  }).send();
});`)
	writeDevWebZipFile(t, zw, "AOS/parameters.yml", `
parameters:
- name: Token
  type: csv
  fileName: one/a-b.csv
  columnName: value
- name: token
  type: csv
  fileName: two/a_b.csv
  columnName: value
`)
	writeDevWebZipFile(t, zw, "AOS/one/a-b.csv", "value\nupper\n")
	writeDevWebZipFile(t, zw, "AOS/two/a_b.csv", "value\nlower\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.DataSources) != 2 ||
		result.Script.DataSources[0].Name != "a_b" ||
		result.Script.DataSources[1].Name != "a_b_2" {
		t.Fatalf("data sources = %#v, want collision-safe source identities", result.Script.DataSources)
	}
	if len(result.Script.Parameters) != 2 ||
		result.Script.Parameters[0].Name != "Token" ||
		result.Script.Parameters[1].Name != "token_2" {
		t.Fatalf("parameters = %#v, want case-fold-safe destination identities", result.Script.Parameters)
	}
	target := result.Script.Actions[0].Target
	for _, reference := range []string{
		"{{data.a_b.value}}",
		"{{data.a_b_2.value}}",
	} {
		if !strings.Contains(target, reference) {
			t.Fatalf("target = %q, missing source-identity reference %q", target, reference)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("collision-safe DevWeb script validation issues = %v", issues)
	}
}

func TestConvertZipLeavesAmbiguousParameterDataFileUnresolved(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "AOS/main.js", `
load.action("Action", async function() {
  await new load.WebRequest({
    url: `+"`https://api.example.test/${load.params.token}`"+`,
    method: "GET",
  }).send();
});`)
	writeDevWebZipFile(t, zw, "AOS/parameters.yml", `
parameters:
- name: token
  type: csv
  fileName: credentials.csv
  columnName: value
`)
	writeDevWebZipFile(t, zw, "AOS/first/credentials.csv", "value\nfirst\n")
	writeDevWebZipFile(t, zw, "AOS/second/credentials.csv", "value\nsecond\n")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.DataSources) != 0 || len(result.Script.Parameters) != 0 {
		t.Fatalf("ambiguous archive file was bound: %#v / %#v", result.Script.DataSources, result.Script.Parameters)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Target, "{{var.token}}") ||
		action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("ambiguous source identity was not left as a blocked reference: %#v", action)
	}
	found := false
	for _, item := range result.Unsupported {
		if item.Element == "DevWeb parameter data file collision" &&
			strings.Contains(item.Detail, "matches multiple archive files") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("diagnostics = %#v, want ambiguous data-file explanation", result.Unsupported)
	}
}

func TestConvertZipPreservesProxyCredentialsButRedactsRuntimeSourceReview(t *testing.T) {
	const (
		proxyUsername = "devweb-proxy-user-canary-71c"
		proxyPassword = "devweb-proxy-password-canary-29e"
	)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "AOS/main.js", `
load.action("Action", async function() {
  await new load.WebRequest({url: "https://api.example.test/", method: "GET"}).send();
});`)
	writeDevWebZipFile(t, zw, "AOS/rts.yml", `
proxy:
  useProxy: true
  proxyServer: http://proxy.example.test:8080
  proxyUser: `+proxyUsername+`
  proxyPassword: `+proxyPassword+`
`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	proxy := result.Script.Config.HTTP.Proxy
	if proxy.Username != proxyUsername || proxy.Password != proxyPassword {
		t.Fatalf("executable proxy credentials changed: %#v", proxy)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("available proxy credentials unexpectedly require re-entry: %#v", requirements)
	}
	review, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{proxyUsername, proxyPassword} {
		if strings.Contains(string(review), canary) {
			t.Fatalf("source review disclosed runtime credential %q: %s", canary, review)
		}
	}
}

func TestConvertZipTreatsRuntimeCredentialPlaceholderAsUnavailable(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "AOS/main.js", `
load.action("Action", async function() {
  await new load.WebRequest({url: "https://api.example.test/", method: "GET"}).send();
});`)
	writeDevWebZipFile(t, zw, "AOS/rts.yml", `
proxy:
  useProxy: true
  proxyServer: http://proxy.example.test:8080
  proxyUser: operator
  proxyPassword: "%255BReDaCtEd%255D"
`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	password := result.Script.Config.HTTP.Proxy.Password
	if !strings.HasPrefix(password, "{{secret.DEVWEB_PROXY_PASSWORD") {
		t.Fatalf("runtime placeholder was retained as an executable password: %q", password)
	}
	requirements := result.ProtectedRequirements.Take()
	if len(requirements) != 1 {
		t.Fatalf("requirements = %#v, want one unavailable runtime credential", requirements)
	}
	requirement := requirements[0]
	if requirement.Label != "DevWeb HTTP proxy password" ||
		requirement.Context != "HTTP proxy password from rts.yml" ||
		requirement.Source.Path != "AOS/rts.yml" ||
		requirement.Source.Pointer != "/proxy/proxyPassword" ||
		requirement.SourceValueAvailable || requirement.Value != "" ||
		requirement.SourceValueStatus != credentialpolicy.SourceCredentialValueRedactedPlaceholder {
		t.Fatalf("runtime credential requirement = %#v", requirement)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("unavailable runtime credential did not gate execution: %#v", action.Metadata)
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
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("dynamic request draft = %#v, want one durable blocked review placeholder", result.Script.Actions)
	}
	if result.Script.Config.BaseURL != "https://missing-base-url.apexload.invalid" {
		t.Fatalf("base URL = %q, want unroutable blocked-draft placeholder", result.Script.Config.BaseURL)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertDevWebPreservesQueryObjectOrderAndLastDuplicateValue(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Ordered Query",
		Content: `load.action("Search", async function() {
		  new load.WebRequest({
		    url: "https://api.example.test/search?existing=0#results",
		    queryString: {
		      zeta: "first",
		      alpha: "second",
		      zeta: "effective"
		    }
		  }).send();
		});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Target != "https://api.example.test/search?existing=0&zeta=effective&alpha=second#results" {
		t.Fatalf("target = %q, want source property order with last duplicate value", action.Target)
	}
	ordered, ok := action.Metadata["devweb_query_string_ordered"].([]map[string]string)
	if !ok || len(ordered) != 2 ||
		ordered[0]["name"] != "zeta" || ordered[0]["value"] != "effective" ||
		ordered[1]["name"] != "alpha" || ordered[1]["value"] != "second" {
		t.Fatalf("ordered query metadata = %#v", action.Metadata["devweb_query_string_ordered"])
	}
}

func TestConvertDevWebKeepsRuntimeQueryReferencesResolvable(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Runtime Query",
		Content: "load.action(\"Search\", async function() {\n" +
			"  new load.WebRequest({\n" +
			"    url: \"https://api.example.test/search\",\n" +
			"    queryString: { q: `${load.params.searchTerm}` }\n" +
			"  }).send();\n" +
			"});",
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got := result.Script.Actions[0].Target; got !=
		"https://api.example.test/search?q={{var.searchTerm}}" {
		t.Fatalf("target = %q, runtime template was encoded into inert URL text", got)
	}
}

func TestConvertDevWebObjectBodyPreservesJSONTypesAndOrder(t *testing.T) {
	const password = "devweb-typed-password"
	result, err := Convert(ImportRequest{
		Name: "Typed Body",
		Content: `load.action("Create", async function() {
		  new load.WebRequest({
		    url: "https://api.example.test/items",
		    method: "POST",
		    headers: {"Content-Type": "application/json"},
		    body: {
		      password: "` + password + `",
		      count: 2,
		      enabled: true,
		      missing: null,
		      nested: {ratio: 1.5},
		      values: [1, "two", false]
		    }
		  }).send();
		});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	action := result.Script.Actions[0]
	want := `{"password":"` + password + `","count":2,"enabled":true,"missing":null,"nested":{"ratio":1.5},"values":[1,"two",false]}`
	if action.Body != want {
		t.Fatalf("body = %q, want type-preserving ordered JSON %q", action.Body, want)
	}
	if action.Metadata["execution_blocker"] != nil {
		t.Fatalf("static typed body was unexpectedly blocked: %#v", action.Metadata)
	}
}

func TestConvertDevWebDynamicObjectBodyBecomesBlockedDraft(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Dynamic Body",
		Content: `load.action("Create", async function() {
		  new load.WebRequest({
		    url: "https://api.example.test/items",
		    method: "POST",
		    body: {count: computeCount()}
		  }).send();
		});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Stats.HTTPRequests != 0 || len(result.Script.Actions) != 1 {
		t.Fatalf("conversion = %#v / %#v, want one review placeholder", result.Stats, result.Script.Actions)
	}
	if result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("dynamic object draft = %#v, want durable review gate", result.Script.Actions[0])
	}
}

func TestConvertDevWebDecodesStaticJavaScriptStringEscapes(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Escaped Literal",
		Content: `load.action("Escaped", async function() {
		  new load.WebRequest({
		    url: "https://api.example.test/line\x2Ditem/\u0041/\u{42}",
		    method: "GET"
		  }).send();
		});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got := result.Script.Actions[0].Target; got != "https://api.example.test/line-item/A/B" {
		t.Fatalf("target = %q, want decoded JavaScript literal", got)
	}
}

func TestConvertDevWebRejectsUnsupportedJavaScriptStringEscape(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "Unsupported Escape",
		Content: `load.action("Escaped", async function() {
		  new load.WebRequest({
		    url: "https://api.example.test/\q",
		    method: "GET"
		  }).send();
		});`,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("unsupported literal = %#v, want blocked review draft", result.Script.Actions)
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
	if len(result.Script.Actions) != 4 {
		t.Fatalf("converted actions = %d, want 4 including dynamic-request, residual-assignment, and correlation review placeholders", len(result.Script.Actions))
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
		t.Fatalf("converted actions = %d, want 6 including the durable merged dynamic-request review placeholder", len(result.Script.Actions))
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

func TestConvertDevWebResidualSourceIsDurableDisclosureSafeAndExecutionGated(t *testing.T) {
	const canary = "devweb-residual-neutral-canary-5a21"
	content := `
load.action("Residual", async function() {
  const signingMaterial = "` + canary + `";
  new load.WebRequest({ url: "https://api.example.test/health" }).send();
});`
	result, err := Convert(ImportRequest{Name: "residual", Content: content})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(result.Script.Actions) < 2 {
		t.Fatalf("actions = %#v, want request and review placeholders", result.Script.Actions)
	}
	for index, action := range result.Script.Actions {
		if action.Metadata["execution_blocker"] != "import_review_required" {
			t.Fatalf("actions[%d] blocker = %#v, want import_review_required", index, action.Metadata)
		}
		if action.Type == "review_required" &&
			(strings.Contains(action.Value, canary) ||
				strings.Contains(action.Value, "signingMaterial")) {
			t.Fatalf("review placeholder copied raw source: %#v", action)
		}
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatalf("import metadata = %#v", result.Script.Metadata)
	}
	snapshot, ok := result.Script.Metadata.Import.Preserved[devWebGatedSourceMetadataKey].(map[string]string)
	if !ok {
		t.Fatalf("gated source = %#v, want exact file snapshot", result.Script.Metadata.Import.Preserved[devWebGatedSourceMetadataKey])
	}
	retained := snapshot["residual.js"]
	if retained != content || !strings.Contains(retained, canary) {
		t.Fatalf("durable gated source = %q, want exact source", retained)
	}
	reviewPayload, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatalf("marshal source review: %v", err)
	}
	for _, forbidden := range []string{canary, "signingMaterial"} {
		if strings.Contains(string(reviewPayload), forbidden) {
			t.Fatalf("public source review retained %q: %s", forbidden, reviewPayload)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
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

func TestConvertZipRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name       string
		entryNames []string
		symlink    bool
	}{
		{name: "traversal", entryNames: []string{"../main.js"}},
		{name: "absolute", entryNames: []string{"/main.js"}},
		{name: "control character", entryNames: []string{"bad\x01/main.js"}},
		{name: "separator collision", entryNames: []string{`root\main.js`, "root/main.js"}},
		{name: "case collision", entryNames: []string{"root/main.js", "ROOT/MAIN.JS"}},
		{name: "symlink", entryNames: []string{"main.js"}, symlink: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			for _, name := range test.entryNames {
				header := &zip.FileHeader{Name: name}
				if test.symlink {
					header.SetMode(fs.ModeSymlink | 0o777)
				}
				entry, err := writer.CreateHeader(header)
				if err != nil {
					t.Fatalf("CreateHeader: %v", err)
				}
				_, _ = entry.Write([]byte(`load.action("x", function() {});`))
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if _, err := ConvertZip(archive.Bytes(), ConvertOptions{}); err == nil {
				t.Fatal("ConvertZip accepted unsafe archive")
			}
		})
	}
}

// TestConvertPreservesAuthoredCredentialHeaders pins the shared credential
// preservation contract for DevWeb.
//
// The converter used to emit authored header values verbatim, so an
// "Authorization: Bearer <token>" in the source became a literal in the
// executable draft. That is intentional: the operator already possesses the
// source and the destination script must remain runnable without reentry.
// Source-review surfaces are still irreversibly redacted.
func TestConvertPreservesAuthoredCredentialHeaders(t *testing.T) {
	const canary = "devweb-authored-credential-canary-9Z1"
	result, err := Convert(ImportRequest{
		Name: "credentials",
		Content: `load.action(async function () {
  await new load.WebRequest({
    url: "https://api.example.test/orders",
    method: "GET",
    headers: {"Authorization": "Bearer ` + canary + `", "X-Api-Key": "` + canary + `"},
  }).send();
});`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if got := devWebHeaderValue(action.Headers, "authorization"); got != "Bearer "+canary {
		t.Fatalf("authorization = %q, want preserved authored value", got)
	}
	if got := devWebHeaderValue(action.Headers, "x-api-key"); got != canary {
		t.Fatalf("X-Api-Key = %q, want preserved authored value", got)
	}

	// The destination conversion result carries the executable source value...
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if !strings.Contains(string(payload), canary) {
		t.Fatalf("destination conversion result dropped the authored credential: %s", payload)
	}
	// ...without creating a protected handoff that requires operator reentry.
	protected := result.ProtectedRequirements.Take()
	if protected != nil || len(result.SecretRequirements) != 0 {
		t.Fatalf("preserved credentials unexpectedly require rebinding: %#v / %#v", protected, result.SecretRequirements)
	}
	if result.SourceReview == nil {
		t.Fatal("source review is missing")
	}
	reviewJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reviewJSON), canary) {
		t.Fatalf("source review disclosed the authored credential: %s", reviewJSON)
	}
}

func TestConvertPreservesCredentialURLButRedactsItFromSourceReview(t *testing.T) {
	const (
		password = "devweb-url-password-canary"
		token    = "devweb-url-query-canary"
	)
	authoredURL := "https://alice:" + password + "@api.example.test/orders?api_key=" + token
	result, err := Convert(ImportRequest{
		Name: "credential URL",
		Content: `load.action(async function () {
  await new load.WebRequest({
    url: "` + authoredURL + `",
    method: "GET",
  }).send();
});`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Target != authoredURL {
		t.Fatalf("destination URL = %#v, want byte-exact %q", result.Script.Actions, authoredURL)
	}
	if result.SourceReview == nil {
		t.Fatal("source review is missing")
	}
	reviewJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{password, token, authoredURL} {
		if strings.Contains(string(reviewJSON), canary) {
			t.Fatalf("source review disclosed URL credential %q: %s", canary, reviewJSON)
		}
	}
}

func TestConvertZipMasksCredentialBoundNeutralCSVColumnOnlyInSourceReview(t *testing.T) {
	const (
		credentialCanary = "devweb-neutral-csv-credential-73c1"
		ordinaryValue    = "devweb-safe-review-label"
	)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	writeDevWebZipFile(t, writer, "Neutral/main.js", `load.action("Login", async function () {
  await new load.WebRequest({
	    url: "https://api.example.test/login",
	    method: "POST",
	    headers: {"Content-Type": "application/json"},
	    body: {
	      "password": `+"`${load.params.displayLabel}`"+`,
	      "display": `+"`${load.params.displayLabel}`"+`,
	      "kind": "test"
	    }
	  }).send();
	});`)
	writeDevWebZipFile(t, writer, "Neutral/parameters.yml", `
parameters:
- name: displayLabel
  type: csv
  fileName: credentials.dat
  columnName: displayLabel
  nextRow: sequential
  nextValue: iteration
  onEnd: loop
`)
	writeDevWebZipFile(
		t,
		writer,
		"Neutral/credentials.dat",
		"displayLabel,ordinary\n"+credentialCanary+","+ordinaryValue+"\n",
	)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 ||
		strings.Count(
			result.Script.Actions[0].Body,
			"{{data.credentials.displayLabel}}",
		) != 2 {
		t.Fatalf("durable request lost CSV credential binding: %#v", result.Script)
	}
	if len(result.Script.DataSources) != 1 ||
		!strings.Contains(result.Script.DataSources[0].InlineData, credentialCanary) {
		t.Fatalf("durable inline CSV lost imported credential: %#v", result.Script.DataSources)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved[dsl.ImportPreservedCredentialPolicyKey] !=
		dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("credential preservation policy = %#v", preserved)
	}
	credentialNames, err := json.Marshal(
		preserved[dsl.ImportPreservedCredentialParameterNamesKey],
	)
	if err != nil || !strings.Contains(string(credentialNames), "displayLabel") {
		t.Fatalf("credential parameter provenance = %s, err=%v", credentialNames, err)
	}
	projected, err := scriptdisclosure.Project(result.Script)
	if err != nil {
		t.Fatalf("project disclosure copy: %v", err)
	}
	projectedPayload, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("json.Marshal(disclosure copy): %v", err)
	}
	if strings.Contains(string(projectedPayload), credentialCanary) ||
		!strings.Contains(string(projectedPayload), ordinaryValue) {
		t.Fatalf("disclosure projection did not isolate credential CSV column: %s", projectedPayload)
	}
	if !strings.Contains(result.Script.DataSources[0].InlineData, credentialCanary) {
		t.Fatalf("disclosure projection mutated durable CSV: %#v", result.Script.DataSources)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("preserved CSV credential requires operator reentry: %#v", requirements)
	}
	reviewPayload, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatalf("json.Marshal(source review): %v", err)
	}
	if strings.Contains(string(reviewPayload), credentialCanary) {
		t.Fatalf("source review disclosed credential-bound neutral CSV cell: %s", reviewPayload)
	}
	if !strings.Contains(string(reviewPayload), ordinaryValue) {
		t.Fatalf("source review discarded nonsensitive CSV cell: %s", reviewPayload)
	}
}

func TestConvertPreservesExplicitSecretReferenceWithoutCreatingRequirement(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "existing secret reference",
		Content: `load.action(async function () {
  await new load.WebRequest({
    url: "https://api.example.test/orders",
    method: "GET",
    headers: {"Authorization": "Bearer {{secret.EXISTING_TOKEN}}"},
  }).send();
});`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := devWebHeaderValue(result.Script.Actions[0].Headers, "authorization"); got != "Bearer {{secret.EXISTING_TOKEN}}" {
		t.Fatalf("authored secret reference changed: %q", got)
	}
	if protected := result.ProtectedRequirements.Take(); protected != nil || len(result.SecretRequirements) != 0 {
		t.Fatalf("authored secret reference created redundant requirements: %#v / %#v", protected, result.SecretRequirements)
	}
}

func TestConvertPreservesStructuredCredentialBytesButBlocksMalformedAndDuplicateJSON(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{
			name:   "malformed",
			body:   `{"password":"devweb-malformed-canary"`,
			reason: "credential_json_malformed",
		},
		{
			name:   "duplicate key",
			body:   `{"password":"first-canary","password":"second-canary"}`,
			reason: "credential_json_duplicate_key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quotedBody := strings.ReplaceAll(strings.ReplaceAll(test.body, `\`, `\\`), `"`, `\"`)
			result, err := Convert(ImportRequest{
				Name: test.name,
				Content: `load.action(async function () {
  await new load.WebRequest({
    url: "https://api.example.test/login",
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: "` + quotedBody + `",
  }).send();
});`,
			})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("actions = %#v", result.Script.Actions)
			}
			action := result.Script.Actions[0]
			if action.Body != test.body {
				t.Fatalf("body = %q, want byte-preserved %q", action.Body, test.body)
			}
			if action.Metadata["execution_blocker"] != test.reason {
				t.Fatalf("malformed/duplicate JSON was not blocked: %#v", action.Metadata)
			}
			review, ok := action.Metadata["import_review"].(map[string]any)
			if !ok || !devWebReviewHasReason(review, test.reason) {
				t.Fatalf("import review = %#v, want %q", action.Metadata["import_review"], test.reason)
			}
			if result.SourceReview == nil {
				t.Fatal("source review is missing")
			}
			reviewJSON, err := json.Marshal(result.SourceReview)
			if err != nil {
				t.Fatal(err)
			}
			for _, canary := range []string{"devweb-malformed-canary", "first-canary", "second-canary"} {
				if strings.Contains(string(reviewJSON), canary) {
					t.Fatalf("source review disclosed %q from a blocked body: %s", canary, reviewJSON)
				}
			}
		})
	}
}

func TestDevWebCredentialBodyReferenceProjectionFailsClosedAtWorkLimit(t *testing.T) {
	body := `{"label":"{{data.accounts.displayName}}","password":"{{data.accounts.password}}"}`
	recorded := make(map[string]struct{})
	record := func(value string) {
		for _, match := range devWebDataReferencePattern.FindAllStringSubmatch(value, -1) {
			if len(match) == 3 {
				recorded[match[1]+"."+match[2]] = struct{}{}
			}
		}
	}

	collectDevWebCredentialBodyDataReferencesWithBudget(
		body,
		"application/json",
		record,
		1,
	)
	for _, want := range []string{"accounts.displayName", "accounts.password"} {
		if _, ok := recorded[want]; !ok {
			t.Fatalf("bounded fallback did not conservatively protect %q: %#v", want, recorded)
		}
	}
}

func TestDevWebCredentialSourceRedactionFailsClosedAtWorkLimit(t *testing.T) {
	const canary = "devweb-redaction-work-canary"
	projected, changed := redactDevWebCredentialValuesWithBudget(
		"first\nAuthorization: "+canary+"\nlast",
		[]credentialpolicy.Requirement{{Value: canary}},
		nil,
		1,
	)
	if !changed || strings.Contains(projected, canary) ||
		!strings.Contains(projected, "WORK LIMIT EXCEEDED") {
		t.Fatalf("bounded source projection = %q, changed=%v", projected, changed)
	}
	if strings.Count(projected, "\n") != 2 {
		t.Fatalf("bounded source projection changed line coordinates: %q", projected)
	}
}

func devWebReviewHasReason(review map[string]any, want string) bool {
	values, ok := review["reason_codes"].([]string)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestConvertPreservesCorrelatedCredentialHeader is the companion invariant: a
// header assembled from a DevWeb extractor output is response-derived and must
// stay executable. Governing it would replace a working correlation with a
// binding the operator cannot satisfy, because the credential never existed as
// a literal.
func TestConvertPreservesCorrelatedCredentialHeader(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "correlated",
		Content: `load.action(async function () {
  const Login = new load.WebRequest({
    url: "https://api.example.test/login",
    method: "POST",
    extractors: [new load.BoundaryExtractor("token", "<token>", "<")],
  }).send();
  await new load.WebRequest({
    url: "https://api.example.test/orders",
    method: "GET",
    headers: {"Authorization": ` + "`Basic ${Login.extractors.token}`" + `},
  }).send();
});`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	for _, action := range result.Script.Actions {
		if value := devWebHeaderValue(action.Headers, "authorization"); value != "" {
			if value != "Basic {{var.token}}" {
				t.Fatalf("correlated authorization = %q, want the extractor reference preserved", value)
			}
			return
		}
	}
	t.Fatalf("no authorization header found in %#v", result.Script.Actions)
}

// devWebHeaderValue looks a header up case-insensitively. The shared finalizer
// preserves the authored header casing, so tests must not assume lowercase.
func devWebHeaderValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// TestConvertZipProducesSaveableScript pins that a DevWeb archive with a
// parameter file yields a script that passes DSL validation.
//
// It previously did not: the importer emitted the DevWeb spellings verbatim
// (update="iteration", on_exhaustion="loop"), set a data-source Path to an
// archive-relative name alongside inline rows, and left config.base_url empty
// whenever the host came from a parameter. Each of those fails validateScript,
// which is a hard 400 on POST /scripts — so every zip import with parameters
// produced a draft that could be converted but never saved.
func TestConvertZipProducesSaveableScript(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "AOS/main.js", `load.action("Action", async function() {
  const Login = new load.WebRequest({
    url: `+"`${load.params.AOS_URL}/account/${load.params.loginUser}`"+`,
    method: "POST",
    body: { "password": `+"`${load.params.loginPassword}`"+` },
  }).sendSync();
});`)
	writeDevWebZipFile(t, zw, "AOS/parameters.yml", `
parameters:
- name: loginUser
  type: csv
  fileName: credentials.csv
  columnName: loginUser
  nextRow: random
  nextValue: iteration
  onEnd: loop
`)
	writeDevWebZipFile(t, zw, "AOS/credentials.csv", "loginUser,loginPassword\nalice,secret\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("generated script is not saveable: %v", issues)
	}

	if len(result.Script.Parameters) != 1 {
		t.Fatalf("parameters = %#v, want one", result.Script.Parameters)
	}
	parameter := result.Script.Parameters[0]
	if parameter.Update != "each_iteration" {
		t.Errorf("update = %q, want the DSL spelling each_iteration", parameter.Update)
	}
	if parameter.OnExhaustion != "wrap" {
		t.Errorf("on_exhaustion = %q, want the DSL spelling wrap", parameter.OnExhaustion)
	}

	if len(result.Script.DataSources) != 1 {
		t.Fatalf("data sources = %#v, want one", result.Script.DataSources)
	}
	source := result.Script.DataSources[0]
	if source.InlineData == "" {
		t.Error("inline rows were not carried over from the archive")
	}
	if source.Path != "" {
		t.Errorf("path = %q, want empty when rows are inlined", source.Path)
	}
	// The host comes from a parameter, so the base URL is the leading template.
	if result.Script.Config.BaseURL == "" {
		t.Error("config.base_url is empty for a fully templated target")
	}
}

// TestConvertZipFlagsUnknownParameterVocabulary keeps the mapping honest: an
// unrecognized DevWeb value falls back to a safe default AND is surfaced for
// review, rather than being silently reinterpreted.
func TestConvertZipFlagsUnknownParameterVocabulary(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDevWebZipFile(t, zw, "AOS/main.js", `load.action("Action", async function() {
  await new load.WebRequest({url: "https://api.example.test/orders", method: "GET"}).send();
});`)
	writeDevWebZipFile(t, zw, "AOS/parameters.yml", `
parameters:
- name: loginUser
  type: csv
  fileName: credentials.csv
  columnName: loginUser
  nextRow: sequential
  nextValue: everyThirdTuesday
  onEnd: explodeGracefully
`)
	writeDevWebZipFile(t, zw, "AOS/credentials.csv", "loginUser\nalice\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("unknown vocabulary produced an unsaveable script: %v", issues)
	}
	reported := strings.Join(result.Unsupported.Strings(), " | ")
	if !strings.Contains(reported, "everyThirdTuesday") || !strings.Contains(reported, "explodeGracefully") {
		t.Fatalf("unknown parameter vocabulary was not surfaced for review: %s", reported)
	}
}
