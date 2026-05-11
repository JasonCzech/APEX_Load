package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestSQLiteStoreSaveScriptPreservesMetadata(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-1",
		Name:     "Imported Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
		},
		Actions: []dsl.Action{{
			Name:   "Homepage",
			Type:   "GET",
			Target: "/",
		}},
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Source:           "loadrunner",
				SourceScriptName: "Demo",
				Preserved: map[string]any{
					"runtime_files": map[string]any{
						"default.cfg": map[string]any{
							"HTTP": map[string]any{
								"ConnectTimeout": "45",
							},
						},
					},
				},
			},
		},
	}

	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	roundTrip, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundTrip.Metadata == nil || roundTrip.Metadata.Import == nil {
		t.Fatal("expected metadata to round-trip through sqlite")
	}
	payload, err := json.Marshal(roundTrip.Metadata.Import)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"\"source\":\"loadrunner\"", "default.cfg", "\"ConnectTimeout\":\"45\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected metadata JSON to contain %q, got %s", want, text)
		}
	}
}