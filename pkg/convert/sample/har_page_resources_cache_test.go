package sample

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func pageResourceHAR(t *testing.T) string {
	t.Helper()
	entry := func(url, mimeType string) map[string]any {
		return map[string]any{
			"pageref":         "page_1",
			"startedDateTime": "2026-01-01T00:00:00Z",
			"time":            10,
			"request":         map[string]any{"method": "GET", "url": url, "headers": []any{}},
			"response":        map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Type", "value": mimeType}}, "content": map[string]any{"mimeType": mimeType, "text": "x"}},
		}
	}
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		entry("https://shop.test/home", "text/html"),
		entry("https://shop.test/app.js", "application/javascript"),
		entry("https://shop.test/logo.png", "image/png"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Pages that carry their recorded resources (resource_mode in_step) replay
// with browser cache simulation on, so each resource is fetched once per new
// user and then served from cache, as a browser does. A script that already
// decides the cache keeps its choice, and the default mode is unchanged.
func TestInStepPageResourcesTurnOnBrowserCacheSimulation(t *testing.T) {
	result, err := Convert(ImportRequest{Name: "Shop", Format: "har", Content: pageResourceHAR(t), ResourceMode: "in_step"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Params["resources"] == nil {
		t.Fatalf("actions = %#v, want one page carrying its resources", result.Script.Actions)
	}
	cache := result.Script.Config.BrowserEmulation
	if cache == nil || cache.Cache == nil || cache.Cache.SimulateCache == nil || !*cache.Cache.SimulateCache {
		t.Fatalf("browser emulation = %#v, want cache simulation on", cache)
	}

	runtime, err := Convert(ImportRequest{Name: "Shop", Format: "har", Content: pageResourceHAR(t)})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Script.Config.BrowserEmulation != nil {
		t.Fatalf("default resource mode set browser emulation: %#v", runtime.Script.Config.BrowserEmulation)
	}

	off := false
	script := &dsl.Script{Actions: []dsl.Action{{Params: map[string]any{"resources": []any{}}}}}
	script.Config.BrowserEmulation = &dsl.BrowserEmulationConfig{Cache: &dsl.BrowserCacheSimulationConfig{SimulateCache: &off}}
	if simulateBrowserCacheForPageResources(script) || *script.Config.BrowserEmulation.Cache.SimulateCache {
		t.Fatal("an explicit cache choice was overridden")
	}
}
