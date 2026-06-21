package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestKafkaAvroSerdeRoundTrip verifies produce Avro-encodes + Confluent-frames a
// JSON body and consume Avro-decodes it back to JSON (mock broker, static id, so
// no registry is needed). String fields are used to avoid JSON/Avro numeric
// coercion in the assertion.
func TestKafkaAvroSerdeRoundTrip(t *testing.T) {
	ctx := context.Background()
	schema := `{"type":"record","name":"Order","fields":[{"name":"orderId","type":"string"},{"name":"status","type":"string"}]}`
	k := newMockKafka(t, map[string]any{
		"kafka_topic":         "orders",
		"kafka_serialization": "avro",
		"kafka_schema":        schema,
		"kafka_schema_id":     float64(21),
	})
	if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(`{"orderId":"A-1","status":"created"}`)}); err != nil {
		t.Fatalf("produce: %v", err)
	}

	// The stored value is Confluent-framed Avro binary, not the original JSON.
	stored, _, ok := k.client.(mockClient).broker.consume("orders", "peek")
	if !ok {
		t.Fatal("expected a stored record")
	}
	if len(stored.Value) < 5 || stored.Value[0] != confluentMagicByte {
		t.Fatalf("stored value is not Confluent-framed: %v", stored.Value[:min(5, len(stored.Value))])
	}
	if strings.Contains(string(stored.Value), `"status":"created"`) {
		t.Fatal("stored value still looks like JSON; expected Avro binary")
	}

	// Consume Avro-decodes back to JSON; the orderId survives the round trip.
	res, err := k.Execute(ctx, &protocol.Action{
		Name:       "c",
		Type:       "kafka_consume",
		Extractors: []protocol.Extractor{{Name: "oid", Source: "body", Type: "regex", Expression: `"orderId":"([^"]+)"`}},
	})
	if err != nil || !res.Success {
		t.Fatalf("consume: %v %s", err, res.ErrorMessage)
	}
	if !strings.Contains(res.Extractions["oid"], "A-1") {
		t.Fatalf("round-tripped orderId extraction = %q, want it to contain A-1", res.Extractions["oid"])
	}
}

func TestBuildSerdeAvroRequiresSchema(t *testing.T) {
	if _, err := buildSerde(config{Serialization: "avro", SchemaID: 1}); err == nil {
		t.Fatal("expected an error: avro serialization requires config.kafka.schema")
	}
}
