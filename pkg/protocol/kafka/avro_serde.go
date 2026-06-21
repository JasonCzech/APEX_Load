package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hamba/avro/v2"
)

// avroSerde encodes/decodes Kafka record values as Confluent-framed Avro binary.
// The DSL body is treated as JSON: Serialize converts it to a native value and
// Avro-encodes it against the configured schema; Deserialize Avro-decodes the
// framed payload back to JSON so downstream extractors/assertions see JSON.
//
// Limitations (acceptable for a load-testing serde): decode uses the configured
// (reader) schema rather than fetching the writer schema by id from the registry
// (schema-evolution resolution lands later); JSON↔Avro numeric fidelity follows
// hamba/avro's coercion rules, so match the schema's numeric types to the body.
type avroSerde struct {
	schema          avro.Schema
	resolver        schemaIDResolver
	subjectForTopic func(topic string) string
}

func newAvroSerde(schemaJSON string, resolver schemaIDResolver, subjectForTopic func(string) string) (*avroSerde, error) {
	sch, err := avro.Parse(schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("kafka avro schema: %w", err)
	}
	return &avroSerde{schema: sch, resolver: resolver, subjectForTopic: subjectForTopic}, nil
}

func (a *avroSerde) Serialize(ctx context.Context, topic string, value []byte) ([]byte, error) {
	var native any
	if err := json.Unmarshal(value, &native); err != nil {
		return nil, fmt.Errorf("kafka avro: record value must be JSON for Avro encoding: %w", err)
	}
	bin, err := avro.Marshal(a.schema, native)
	if err != nil {
		return nil, fmt.Errorf("kafka avro encode: %w", err)
	}
	id, err := a.resolver.schemaID(ctx, a.subjectForTopic(topic))
	if err != nil {
		return nil, err
	}
	return encodeConfluentWireFormat(id, bin), nil
}

func (a *avroSerde) Deserialize(_ context.Context, value []byte) ([]byte, error) {
	_, payload, err := decodeConfluentWireFormat(value)
	if err != nil {
		return nil, err
	}
	var native any
	if err := avro.Unmarshal(a.schema, payload, &native); err != nil {
		return nil, fmt.Errorf("kafka avro decode: %w", err)
	}
	out, err := json.Marshal(native)
	if err != nil {
		return nil, fmt.Errorf("kafka avro: re-encode to JSON: %w", err)
	}
	return out, nil
}
