package recordbridge

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplaySyntheticHARDeduplicatesIdentitiesAndPreservesRepeatedRequests(t *testing.T) {
	now := time.Now().UTC()
	entry := protocol.NetworkEntry{URL: "https://example.test/same", Method: "GET", StatusCode: 200,
		BrowserEvidence: &protocol.BrowserResourceEvidence{SourceGeneration: "generation", RequestID: "request-1"}}
	completed := entry
	completed.StatusCode = 503
	other := entry
	identity := *entry.BrowserEvidence
	identity.RequestID = "request-2"
	other.BrowserEvidence = &identity
	events := []engine.ReplayActionEvent{
		{StartedAt: now, Network: []protocol.NetworkEntry{entry}},
		{StartedAt: now, Snapshot: &protocol.ErrorSnapshot{NetworkEntries: []protocol.NetworkEntry{completed, other}}},
		{StartedAt: now, Network: []protocol.NetworkEntry{{URL: "https://example.test/same", Method: "GET"}, {URL: "https://example.test/same", Method: "GET"}, {URL: "https://example.test/cached", Method: "GET", ResourceType: "image(cache)"}}},
	}
	var out bytes.Buffer
	summary, err := WriteReplaySyntheticHAR(events, &out)
	if err != nil || summary.Entries != 4 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	var har struct {
		Log struct {
			Entries []struct {
				Response struct {
					Status int `json:"status"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(out.Bytes(), &har); err != nil {
		t.Fatal(err)
	}
	if summary.Omitted["cache_hit"] != 1 {
		t.Fatal("cached resource was exported as a dispatch")
	}
	if har.Log.Entries[0].Response.Status != 503 {
		t.Fatal("export kept an earlier request observation")
	}
}
