package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type recordedEnvironment struct{ version string }

func (e *recordedEnvironment) RecordedBrowserVersion() string { return e.version }

// The browser build a script was recorded with reaches the browser protocol,
// from a stored script (JSON) or one converted in memory.
func TestBrowserConfigCarriesTheRecordedBrowserVersion(t *testing.T) {
	for name, environment := range map[string]any{
		"stored":    map[string]any{"version": 1, "engine": "chromium", "browser_version": "149.0.7827.55"},
		"in memory": &recordedEnvironment{version: "149.0.7827.55"},
	} {
		script := &dsl.Script{Protocol: "browser", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"recording_environment": environment}}}}
		cfg := map[string]any{}
		appendBrowserConfig(cfg, script)
		if cfg["browser_recorded_version"] != "149.0.7827.55" {
			t.Fatalf("%s: browser_recorded_version = %v", name, cfg["browser_recorded_version"])
		}
	}
	for name, script := range map[string]*dsl.Script{
		"no metadata":          {Protocol: "browser"},
		"no recorded version":  {Protocol: "browser", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"recording_environment": map[string]any{"version": 1}}}}},
		"unexpected structure": {Protocol: "browser", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"recording_environment": "149.0"}}}},
	} {
		cfg := map[string]any{}
		appendBrowserConfig(cfg, script)
		if _, present := cfg["browser_recorded_version"]; present {
			t.Fatalf("%s: browser_recorded_version = %v", name, cfg["browser_recorded_version"])
		}
	}
}
