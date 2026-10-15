package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	_ "github.com/jasonczech/apexload/pkg/protocol/appium"
)

// A device observer script replays like any other: its Appium session opens
// with the script's capabilities (secrets resolved), its steps run on the
// device, and its transactions time them.
func TestReplayAppiumDeviceObserver(t *testing.T) {
	var mu sync.Mutex
	var capabilities map[string]any
	var commands, typed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		commands = append(commands, r.Method+" "+r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			capabilities = body["capabilities"].(map[string]any)["alwaysMatch"].(map[string]any)
			_, _ = w.Write([]byte(`{"value":{"sessionId":"s1","capabilities":{}}}`))
		case r.URL.Path == "/session/s1/element":
			_, _ = w.Write([]byte(`{"value":{"element-6066-11e4-a5d6-4a0f6f0ba3b9":"e1"}}`))
		case r.URL.Path == "/session/s1/element/e1/value":
			typed = append(typed, body["text"].(string))
			_, _ = w.Write([]byte(`{"value":null}`))
		case r.URL.Path == "/session/s1/element/e1/text":
			_, _ = w.Write([]byte(`{"value":"3 results"}`))
		default:
			_, _ = w.Write([]byte(`{"value":null}`))
		}
	}))
	defer server.Close()
	script := &dsl.Script{
		Name:     "SearchAuth on a simulator",
		Protocol: "appium",
		Config: dsl.ScriptConfig{Appium: dsl.AppiumConfig{
			ServerURL:    server.URL,
			Capabilities: map[string]any{"platformName": "iOS", "appium:udid": "{{secret.SIMULATOR_UDID}}"},
		}},
		Actions: []dsl.Action{
			{Name: "Start Search", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{"Search"}}},
			{Name: "Query", Type: "appium_type", Target: "searchField", Value: "{{var.term}}"},
			{Name: "Go", Type: "appium_tap", Target: "Search"},
			{Name: "Results", Type: "appium_wait", Target: "resultsList", Params: map[string]any{"text": "results"}},
			{Name: "End Search", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction", "args": []any{"Search"}}},
		},
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		IgnoreThinkTime: true,
		Variables:       map[string]string{"term": "acme"},
		SecretResolver: SecretResolverFunc(func(name string) (string, bool) {
			return "9E41B418-1930-45E6-B444-072070DF69B4", name == "SIMULATOR_UDID"
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s", report.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if capabilities["appium:udid"] != "9E41B418-1930-45E6-B444-072070DF69B4" || strings.Join(typed, ",") != "acme" {
		t.Fatalf("capabilities = %v, typed %v", capabilities, typed)
	}
	var steps []string
	for _, event := range report.Events {
		if !event.Success {
			t.Fatalf("event %s failed: %+v", event.ActionName, event)
		}
		steps = append(steps, event.ActionType+":"+event.ActionName)
	}
	joined := strings.Join(steps, ",")
	for _, want := range []string{"appium_type:Query", "appium_tap:Go", "appium_wait:Results"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("events %s miss %s", joined, want)
		}
	}
	if !strings.Contains(strings.Join(commands, ","), "DELETE /session/s1") {
		t.Fatalf("the session was not closed: %v", commands)
	}
}
