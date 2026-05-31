package devweb

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
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

func TestConvertZipUsesFirstJavaScriptFile(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	file, err := zw.Create("scripts/main.js")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _ = file.Write([]byte(`new load.WebRequest({ url: "https://api.example.test/health" }).send();`))
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
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
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
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
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
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
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
