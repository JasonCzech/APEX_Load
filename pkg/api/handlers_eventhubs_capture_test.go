package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hamba/avro/v2/ocf"
)

const eventHubsCaptureTestSchema = `{
  "type":"record","name":"EventData","namespace":"Microsoft.ServiceBus.Messaging",
  "fields":[
    {"name":"SequenceNumber","type":"long"},
    {"name":"Offset","type":"string"},
    {"name":"EnqueuedTimeUtc","type":"string"},
    {"name":"SystemProperties","type":{"type":"map","values":["long","double","string","bytes"]}},
    {"name":"Properties","type":{"type":"map","values":["long","double","string","bytes","null"]}},
    {"name":"Body","type":["null","bytes"]}
  ]
}`

func buildCaptureFixture(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(eventHubsCaptureTestSchema, &buf)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	rec := map[string]any{
		"SequenceNumber":   int64(7),
		"Offset":           "512",
		"EnqueuedTimeUtc":  "2026-06-21T00:00:00Z",
		"SystemProperties": map[string]any{"x-opt-partition-key": map[string]any{"string": "device-9"}},
		"Properties":       map[string]any{"source": map[string]any{"string": "sensor"}},
		"Body":             map[string]any{"bytes": []byte(`{"deviceId":"device-9"}`)},
	}
	if err := enc.Encode(rec); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func TestHandleImportEventHubsCapture(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := buildCaptureFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/eventhubs-capture?project_id=proj-eh&filename=capture.avro", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Actions   []struct {
				Type   string         `json:"type"`
				Target string         `json:"target"`
				Body   string         `json:"body"`
				Params map[string]any `json:"params"`
			} `json:"actions"`
		} `json:"script"`
		ReviewSections []struct {
			Key   string   `json:"key"`
			Items []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Script.ProjectID != "proj-eh" || result.Script.Protocol != "eventhubs" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "eventhubs_produce" {
		t.Fatalf("unexpected actions: %+v", result.Script.Actions)
	}
	if pk, _ := result.Script.Actions[0].Params["partition_key"].(string); pk != "device-9" {
		t.Fatalf("partition_key = %q, want device-9", pk)
	}
	foundReview := false
	for _, sec := range result.ReviewSections {
		if sec.Key == "eventhubs_capture" {
			foundReview = true
		}
	}
	if !foundReview {
		t.Fatalf("expected an eventhubs_capture review section, got %+v", result.ReviewSections)
	}
}

func TestHandleImportEventHubsCaptureRejectsGarbage(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/eventhubs-capture", bytes.NewReader([]byte("not avro")))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", resp.Code, resp.Body.String())
	}
}

func TestHandleEventHubsCaptureSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/eventhubs-capture/support-matrix", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		SupportMatrix []map[string]any `json:"support_matrix"`
		Notes         []string         `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.SupportMatrix) == 0 || len(result.Notes) == 0 {
		t.Fatalf("expected non-empty support matrix + notes, got %+v", result)
	}
}
