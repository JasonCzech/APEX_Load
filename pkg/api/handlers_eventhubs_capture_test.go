package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		"Properties": map[string]any{
			"source": map[string]any{"string": "sensor"},
			"count":  map[string]any{"long": int64(42)},
			"ratio":  map[string]any{"double": 1.5},
			"opaque": map[string]any{"bytes": []byte{0xff, 0x00}},
		},
		"Body": map[string]any{"bytes": []byte(`{"deviceId":"device-9"}`)},
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
				Type     string            `json:"type"`
				Target   string            `json:"target"`
				Body     string            `json:"body"`
				Headers  map[string]string `json:"headers"`
				Params   map[string]any    `json:"params"`
				Metadata map[string]any    `json:"metadata"`
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
	action := result.Script.Actions[0]
	if len(action.Headers) != 1 || action.Headers["source"] != "sensor" {
		t.Fatalf("typed properties were stringified into headers: %#v", action.Headers)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("typed properties were not review-gated: %#v", action.Metadata)
	}
	preserved, ok := action.Metadata["eventhubs_capture_unmapped_properties"].(map[string]any)
	if !ok || len(preserved) != 3 {
		t.Fatalf("preserved typed properties = %#v, want count/ratio/opaque", action.Metadata["eventhubs_capture_unmapped_properties"])
	}
	opaque, _ := preserved["opaque"].(map[string]any)
	if opaque["base64"] != "/wA=" {
		t.Fatalf("binary property descriptor = %#v", opaque)
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

// An .avro upload that is not Capture output (the dashboard routes every .avro
// here) must import its records as event bodies and say so in review, not
// seed empty produce actions under a Capture heading.
func TestHandleImportEventHubsCaptureAcceptsGenericAvroRecords(t *testing.T) {
	const schema = `{"type":"record","name":"kylosample","fields":[{"name":"id","type":"long"},{"name":"email","type":"string"},{"name":"salary","type":["null","double"],"default":null}]}`
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(schema, &buf, ocf.WithCodec(ocf.Snappy))
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, rec := range []map[string]any{{"id": int64(1), "email": "a@example.com", "salary": 10.5}, {"id": int64(2), "email": "b@example.com", "salary": nil}} {
		if err := enc.Encode(rec); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/eventhubs-capture?project_id=proj-eh&filename=userdata1.avro", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		Script struct {
			Actions []struct {
				Body string `json:"body"`
			} `json:"actions"`
		} `json:"script"`
		Stats struct {
			SourceFormat string `json:"source_format"`
			AvroSchema   string `json:"avro_schema"`
			EmptyBodies  int    `json:"empty_bodies"`
		} `json:"stats"`
		ReviewSections []struct {
			Key   string   `json:"key"`
			Title string   `json:"title"`
			Items []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Stats.SourceFormat != "avro_records" || result.Stats.AvroSchema != "kylosample" || result.Stats.EmptyBodies != 0 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	if len(result.Script.Actions) != 2 || result.Script.Actions[0].Body != `{"id":1,"email":"a@example.com","salary":10.5}` || result.Script.Actions[1].Body != `{"id":2,"email":"b@example.com","salary":null}` {
		t.Fatalf("actions = %+v", result.Script.Actions)
	}
	for _, section := range result.ReviewSections {
		if section.Key == "eventhubs_capture" {
			if section.Title != "Avro records" || len(section.Items) == 0 || !strings.Contains(section.Items[0], "kylosample") {
				t.Fatalf("review section = %+v", section)
			}
			return
		}
	}
	t.Fatalf("missing eventhubs_capture review section: %+v", result.ReviewSections)
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
