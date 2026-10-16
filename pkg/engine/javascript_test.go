package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
)

func TestNativeJavaScriptLiteralSourceSecretDiscovery(t *testing.T) {
	script := &dsl.Script{Name: "literal source", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.invalid"}, Actions: []dsl.Action{{Name: "script", Type: "native_javascript", Params: map[string]any{
		"source": `return {literal:"{{secret.inert}}",run:"{{run.unknown}}",value:input.value};`,
		"inputs": map[string]any{"value": "{{secret.active}}"},
	}}}}
	names, err := ReferencedSecretNames(script)
	if err != nil || !reflect.DeepEqual(names, []string{"active"}) {
		t.Fatalf("literal source acquired authority: %v %v", names, err)
	}
	if err := dsl.ValidateScriptError(script); err != nil {
		t.Fatal(err)
	}
}
func TestNativeJavaScriptUnsupportedHostFailsPreflight(t *testing.T) {
	if NativeJavaScriptAvailable() {
		t.Skip("supported execution host")
	}
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "push")
	a := dsl.Action{Type: "native_javascript", Params: map[string]any{"source": "return {};"}}
	if _, err := vu.handleEngineAction(context.Background(), a); err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("unsupported host ran source: %v", err)
	}
	vu.Script.Actions = []dsl.Action{a}
	if _, err := PreflightScriptRuntime(context.Background(), vu.Script, nil); err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("unsupported preflight: %v", err)
	}
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	script := &dsl.Script{Name: "reject before target", Protocol: "http", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Actions: []dsl.Action{
		{Name: "earlier target", Type: "http_request", Method: "GET", Target: target.URL}, a,
	}}
	engine, err := New(Config{Script: script, VUsers: 1, TerminationMode: dsl.TerminationIterations})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("single engine admitted unsupported worker: %v", err)
	}
	if _, err := ReplayScript(t.Context(), script, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("replay admitted unsupported worker: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("target action ran before native worker preflight")
	}
}
func TestNativeJavaScriptRealWorkerLiteralInputsAndData(t *testing.T) {
	if !NativeJavaScriptAvailable() {
		t.Skip("real worker requires Linux hard limits")
	}
	if raceDetectorEnabled {
		t.Skip("the worker re-executes this test binary under 256 MiB data and 2 s CPU limits, which a race-instrumented binary can exceed")
	}
	for _, profile := range []livetable.Profile{livetable.ProfileNative, livetable.ProfileVTS} {
		t.Run(string(profile), func(t *testing.T) {
			vu, _ := nativeTestVU(t, profile, "push", "pop")
			vu.Proto = &stubProtocol{}
			vu.Metrics = NewMetricsCollector(16)
			vu.Variables["injection"] = `"};throw new Error("injected");//`
			vu.Variables["input_id"] = "snow 雪"
			vu.Script.Config.RunLogic.Iterations = 1
			vu.Script.Actions = []dsl.Action{{Name: "native", Type: "native_javascript", Params: map[string]any{
				"source": `const q=apex.data.table("work");q.push({id:input.id,empty:""});const r=q.pop();return {literal:"{{var.injection}}",id:r.row.values.id,empty:Object.prototype.hasOwnProperty.call(r.row.values,"empty"),absent:Object.prototype.hasOwnProperty.call(r.row.values,"counter"),revision:r.revision};`,
				"inputs": map[string]any{"id": "{{var.input_id}}"}, "result_prefix": "native", "timeout_ms": 10000,
			}}}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			vu.Run(ctx)
			if vu.Variables["native.literal"] != "{{var.injection}}" || vu.Variables["native.id"] != "snow 雪" || vu.Variables["native.empty"] != "true" || vu.Variables["native.absent"] != "false" {
				t.Fatalf("full engine worker result: %+v", vu.Variables)
			}
		})
	}
}
func TestNativeJavaScriptRealWorkerLimitsAndNoAmbientCapabilities(t *testing.T) {
	if !NativeJavaScriptAvailable() {
		t.Skip("real worker requires Linux hard limits")
	}
	vu, _ := nativeTestVU(t, livetable.ProfileNative, "push")
	for _, tc := range []struct {
		name, source string
		timeout      int
		success      bool
	}{
		{"no ambient", `return {safe:typeof process==="undefined"&&typeof require==="undefined"&&typeof fetch==="undefined"&&typeof __nativeDataCall==="undefined",frozen:Object.isFrozen(apex)&&Object.isFrozen(apex.data)};`, 5000, true},
		{"timeout", `while(true){}`, 100, false},
		{"memory", `let a=[];for(;;)a.push(new Array(1000000).fill("memory"));`, 5000, false},
		{"oversized output", `return {large:"x".repeat(65537)};`, 5000, false},
		{"unknown authority", `apex.data.call({alias:"work",operation:"push",client_id:"forged",values:{id:"bad"}});return {};`, 5000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			err := vu.executeNativeJavaScript(context.Background(), dsl.Action{Type: "native_javascript", Params: map[string]any{"source": tc.source, "timeout_ms": tc.timeout, "result_prefix": "result"}})
			if (err == nil) != tc.success {
				t.Fatalf("success=%v err=%v", tc.success, err)
			}
			if time.Since(start) > 8*time.Second {
				t.Fatal("worker did not terminate promptly")
			}
			if tc.success && (vu.Variables["result.safe"] != "true" || vu.Variables["result.frozen"] != "true") {
				t.Fatal("ambient capability exposed")
			}
			if len(nativeJSWorkers) != 0 {
				t.Fatal("worker admission leaked")
			}
		})
	}
}
