package kafka

import (
	"context"
	"encoding/binary"
	"fmt"
)

// confluentMagicByte prefixes every Confluent-framed Kafka record value.
const confluentMagicByte = 0x00

// encodeConfluentWireFormat frames a payload in the Confluent Schema Registry
// wire format: a 0x00 magic byte, the 4-byte big-endian schema id, then the
// serialized payload. (Protobuf additionally inserts a message-index varint
// after the id; that lands with the Protobuf codec in P2.2 part 2.)
func encodeConfluentWireFormat(schemaID int32, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = confluentMagicByte
	binary.BigEndian.PutUint32(out[1:5], uint32(schemaID))
	copy(out[5:], payload)
	return out
}

// decodeConfluentWireFormat strips the wire-format envelope, returning the
// schema id and the raw payload.
func decodeConfluentWireFormat(data []byte) (int32, []byte, error) {
	if len(data) < 5 {
		return 0, nil, fmt.Errorf("kafka serde: value too short for the Confluent wire format (%d bytes)", len(data))
	}
	if data[0] != confluentMagicByte {
		return 0, nil, fmt.Errorf("kafka serde: missing Confluent magic byte (got 0x%02x); is the producer using a schema registry?", data[0])
	}
	return int32(binary.BigEndian.Uint32(data[1:5])), data[5:], nil
}

// serde serializes/deserializes Kafka record values.
type serde interface {
	Serialize(ctx context.Context, topic string, value []byte) ([]byte, error)
	Deserialize(ctx context.Context, value []byte) ([]byte, error)
}

// rawSerde passes values through unchanged (no schema, no framing) — the default.
type rawSerde struct{}

func (rawSerde) Serialize(_ context.Context, _ string, value []byte) ([]byte, error) {
	return value, nil
}

func (rawSerde) Deserialize(_ context.Context, value []byte) ([]byte, error) {
	return value, nil
}

// schemaIDResolver returns the schema id used to frame a record value for a
// given subject. Implementations: a static id, or a Schema Registry client.
type schemaIDResolver interface {
	schemaID(ctx context.Context, subject string) (int32, error)
}

// staticSchemaID frames every record with a fixed schema id (no registry call) —
// useful for shakeout/testing against a known id.
type staticSchemaID int32

func (s staticSchemaID) schemaID(context.Context, string) (int32, error) { return int32(s), nil }

// confluentSerde frames an already-serialized value (e.g. JSON for the
// json_schema format) in the Confluent wire format using a schema id from the
// resolver. Avro/Protobuf binary encoding of the payload lands in P2.2 part 2.
type confluentSerde struct {
	resolver        schemaIDResolver
	subjectForTopic func(topic string) string
}

func (c confluentSerde) Serialize(ctx context.Context, topic string, value []byte) ([]byte, error) {
	id, err := c.resolver.schemaID(ctx, c.subjectForTopic(topic))
	if err != nil {
		return nil, err
	}
	return encodeConfluentWireFormat(id, value), nil
}

func (c confluentSerde) Deserialize(_ context.Context, value []byte) ([]byte, error) {
	_, payload, err := decodeConfluentWireFormat(value)
	return payload, err
}

// topicNameSubject is the default Confluent subject-name strategy for record
// values: "<topic>-value".
func topicNameSubject(topic string) string { return topic + "-value" }
