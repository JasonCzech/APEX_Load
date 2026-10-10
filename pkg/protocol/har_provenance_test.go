package protocol

import (
	"encoding/json"
	"testing"
)

func TestTagBrowserHARHTTPVersions(t *testing.T) {
	input := []byte(`{"log":{"entries":[
		{"request":{"httpVersion":"h3"}},
		{"request":{"httpVersion":"HTTP/2.0"}},
		{"request":{"httpVersion":"http/1.1"}},
		{"request":{"httpVersion":"h3-29"}},
		{"request":{}},
		{"request":{"httpVersion":"h3"},"_apexload_http_version":{"version":1,"request_leg":"client_to_proxy","response_leg":"proxy_to_origin"}}
	]}}`)
	tagged, err := TagBrowserHARHTTPVersions(input, HARProvenanceBrowser)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Log struct {
			Entries []map[string]any `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(tagged, &document); err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"h3", "h2", "h1", "unknown", "unknown"} {
		got, _ := document.Log.Entries[index][HARHTTPVersionProvenanceKey].(map[string]any)
		if got["version"] != float64(2) || got["source"] != HARProvenanceBrowser || got["observed"] != want {
			t.Fatalf("entry %d provenance = %v, want observed %s", index, got, want)
		}
	}
	// Existing provenance is never overwritten.
	if kept, _ := document.Log.Entries[5][HARHTTPVersionProvenanceKey].(map[string]any); kept["version"] != float64(1) {
		t.Fatalf("existing provenance replaced: %v", kept)
	}
	if _, err := TagBrowserHARHTTPVersions([]byte("not json"), HARProvenanceBrowserRun); err == nil {
		t.Fatal("a non-JSON HAR was accepted")
	}
	empty := []byte(`{"log":{"entries":[]}}`)
	if out, err := TagBrowserHARHTTPVersions(empty, HARProvenanceBrowserRun); err != nil || string(out) != string(empty) {
		t.Fatalf("an empty HAR changed: %s %v", out, err)
	}
}
