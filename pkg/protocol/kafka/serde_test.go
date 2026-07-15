package kafka

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestConfluentWireFormatRoundTrip(t *testing.T) {
	framed := encodeConfluentWireFormat(42, []byte(`{"a":1}`))
	if framed[0] != confluentMagicByte {
		t.Fatalf("missing magic byte: 0x%02x", framed[0])
	}
	if len(framed) != 5+len(`{"a":1}`) {
		t.Fatalf("framed length = %d, want %d", len(framed), 5+len(`{"a":1}`))
	}
	id, payload, err := decodeConfluentWireFormat(framed)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if id != 42 {
		t.Fatalf("schema id = %d, want 42", id)
	}
	if string(payload) != `{"a":1}` {
		t.Fatalf("payload = %q, want the original JSON", payload)
	}
}

func TestDecodeConfluentWireFormatRejectsBadInput(t *testing.T) {
	if _, _, err := decodeConfluentWireFormat([]byte{0x00, 0x01}); err == nil {
		t.Fatal("expected error for a too-short value")
	}
	if _, _, err := decodeConfluentWireFormat([]byte{0x09, 0, 0, 0, 1, 'x'}); err == nil {
		t.Fatal("expected error for a missing magic byte")
	}
}

func TestSchemaRegistryClientRegistersAndCaches(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"id": 7})
	}))
	defer srv.Close()

	client := newSchemaRegistryClient(srv.URL, `{"type":"object"}`, "JSON")
	id, err := client.schemaID(context.Background(), "orders-value")
	if err != nil {
		t.Fatalf("schemaID: %v", err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
	// Second call for the same subject is served from cache (no extra HTTP call).
	if _, err := client.schemaID(context.Background(), "orders-value"); err != nil {
		t.Fatalf("cached schemaID: %v", err)
	}
	if calls != 1 {
		t.Fatalf("registry HTTP calls = %d, want 1 (second resolved from cache)", calls)
	}
}

func TestSchemaRegistryEscapesSubjectAsOnePathSegment(t *testing.T) {
	var escapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()

	client := newSchemaRegistryClient(srv.URL, `{}`, "JSON")
	if _, err := client.schemaID(context.Background(), "orders/../../admin?x=1"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(escapedPath, "/../") || !strings.Contains(escapedPath, "orders%2F..%2F..%2Fadmin%3Fx=1") {
		t.Fatalf("schema registry escaped path = %q", escapedPath)
	}
}

// TestKafkaJSONSchemaSerdeRoundTrip verifies produce frames the value in the
// Confluent wire format and consume strips it, against a fake Schema Registry.
func TestKafkaJSONSchemaSerdeRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int{"id": 11})
	}))
	defer srv.Close()

	ctx := context.Background()
	k := newMockKafka(t, map[string]any{
		"kafka_topic":               "orders",
		"kafka_serialization":       "json_schema",
		"kafka_schema_registry_url": srv.URL,
		"kafka_schema":              `{"type":"object"}`,
	})
	if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(`{"orderId":"A-1"}`)}); err != nil {
		t.Fatalf("produce: %v", err)
	}
	// The stored record value is wire-format framed (magic byte + id 11).
	stored, _, ok := k.client.(mockClient).broker.consume("orders", "framecheck")
	if !ok {
		t.Fatal("expected a stored record")
	}
	if stored.Value[0] != confluentMagicByte {
		t.Fatalf("stored value is not Confluent-framed: 0x%02x", stored.Value[0])
	}

	// A json_schema consume strips the framing back to the original JSON: the
	// reported received bytes equal the original payload (17), not the framed
	// length (22 = 5-byte envelope + payload).
	res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"})
	if err != nil || !res.Success {
		t.Fatalf("consume: %v %s", err, res.ErrorMessage)
	}
	if res.BytesReceived != int64(len(`{"orderId":"A-1"}`)) {
		t.Fatalf("BytesReceived = %d, want %d (framing must be stripped)", res.BytesReceived, len(`{"orderId":"A-1"}`))
	}
}

// TestKafkaRequestReplyAppliesValueSerde verifies request_reply frames the
// outgoing request and deframes the reply with the configured serde, matching
// kafka_produce / kafka_consume (otherwise a schema-registry peer rejects the
// unframed request and extractors see the raw Confluent envelope).
func TestKafkaRequestReplyAppliesValueSerde(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{
		"kafka_topic":         "orders",
		"kafka_serialization": "json_schema",
		"kafka_schema_id":     float64(99),
	})
	body := []byte(`{"k":"v"}`)
	res, err := k.Execute(ctx, &protocol.Action{Name: "rr", Type: "kafka_request_reply", Body: body, Params: map[string]any{"reply_to": "orders.reply"}})
	if err != nil || !res.Success {
		t.Fatalf("request_reply: %v %s", err, res.ErrorMessage)
	}
	// The on-wire request is Confluent-framed (magic byte), like kafka_produce.
	stored, _, ok := k.client.(mockClient).broker.consume("orders", "framecheck")
	if !ok {
		t.Fatal("expected a stored request record")
	}
	if stored.Value[0] != confluentMagicByte {
		t.Fatalf("request value not Confluent-framed: 0x%02x", stored.Value[0])
	}
	// The reply is deframed before byte accounting/extractors: received bytes
	// equal the original payload, not the 5-byte-envelope-framed length.
	if res.BytesReceived != int64(len(body)) {
		t.Fatalf("BytesReceived = %d, want %d (reply framing must be stripped)", res.BytesReceived, len(body))
	}
}

func TestKafkaStaticSchemaIDSerdeNeedsNoRegistry(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{
		"kafka_topic":         "orders",
		"kafka_serialization": "json_schema",
		"kafka_schema_id":     float64(99),
	})
	if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(`{"k":"v"}`)}); err != nil {
		t.Fatalf("produce: %v", err)
	}
	res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"})
	if err != nil || !res.Success {
		t.Fatalf("consume: %v %s", err, res.ErrorMessage)
	}
}
