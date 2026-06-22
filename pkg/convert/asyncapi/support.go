package asyncapi

// SupportMatrixEntry describes coverage of one AsyncAPI construct.
type SupportMatrixEntry struct {
	ID              string   `json:"id"`
	Area            string   `json:"area"`
	Construct       string   `json:"construct"`
	Status          string   `json:"status"`
	ExecutionStatus string   `json:"execution_status"`
	ReviewRequired  bool     `json:"review_required"`
	Fields          []string `json:"fields,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

// GetSupportMatrix returns the AsyncAPI import support matrix.
func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "asyncapi-version", Area: "Document", Construct: "asyncapi 2.x / 3.x", Status: "supported", ExecutionStatus: "executable", Fields: []string{"asyncapi", "info"}, Notes: "Both major versions are parsed; 3.x send/receive and 2.x publish/subscribe are normalized."},
		{ID: "asyncapi-servers-kafka", Area: "Servers", Construct: "server.protocol kafka/kafka-secure", Status: "supported", ExecutionStatus: "executable", Fields: []string{"host", "url", "protocol"}, Notes: "Kafka servers become config.kafka.brokers; kafka-secure defaults security_protocol to sasl_ssl for review."},
		{ID: "asyncapi-servers-amqp", Area: "Servers", Construct: "amqp/amqps/jms/solace servers", Status: "supported", ExecutionStatus: "executable", Fields: []string{"host", "url", "protocol"}, Notes: "When no Kafka server is present, AMQP/JMS servers target the jms-solace adapter; server host becomes config.jms.broker_url."},
		{ID: "asyncapi-servers-mqtt", Area: "Servers", Construct: "mqtt servers", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "MQTT has no native APEX Load runtime; MQTT operations are reported for review and are not emitted as executable JMS actions."},
		{ID: "asyncapi-servers-other", Area: "Servers", Construct: "ws/http servers", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Non-messaging servers are ignored; APEX Load converts the Kafka or AMQP/JMS binding only."},
		{ID: "asyncapi-channels", Area: "Channels", Construct: "channels + kafka binding topic", Status: "supported", ExecutionStatus: "executable", Fields: []string{"address", "bindings.kafka.topic"}, Notes: "Topic resolves from the kafka binding, then channel address (3.x), then channel name."},
		{ID: "asyncapi-operations-3x", Area: "Operations", Construct: "operation.action send/receive", Status: "supported", ExecutionStatus: "executable", Fields: []string{"action", "channel", "reply"}, Notes: "send -> kafka_produce, receive -> kafka_consume; an operation reply yields kafka_request_reply."},
		{ID: "asyncapi-operations-2x", Area: "Operations", Construct: "channel publish/subscribe", Status: "supported", ExecutionStatus: "executable", Fields: []string{"publish", "subscribe"}, Notes: "publish -> kafka_produce, subscribe -> kafka_consume (external-client perspective)."},
		{ID: "asyncapi-payload-jsonschema", Area: "Messages", Construct: "message.payload (JSON Schema)", Status: "supported", ExecutionStatus: "executable", Fields: []string{"payload", "example"}, Notes: "JSON Schema payloads generate a sample body with data-source placeholders for scalar fields."},
		{ID: "asyncapi-payload-avro", Area: "Messages", Construct: "avro schemaFormat", Status: "supported", ExecutionStatus: "executable", Fields: []string{"schemaFormat", "payload"}, Notes: "Avro records, unions, arrays, maps, enums, defaults, and logical types synthesize replayable JSON bodies and can emit Kafka Avro serde config."},
		{ID: "asyncapi-payload-protobuf", Area: "Messages", Construct: "protobuf schemaFormat", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Protobuf imports are reported for review because the importer cannot synthesize an executable protobuf body yet."},
		{ID: "asyncapi-correlation", Area: "Messages", Construct: "message.correlationId", Status: "partial", ExecutionStatus: "review_required", ReviewRequired: true, Notes: "Detected and reported; 3.x operation replies become request/reply, otherwise wire correlation manually."},
		{ID: "asyncapi-security", Area: "Security", Construct: "securitySchemes (SASL/TLS)", Status: "partial", ExecutionStatus: "review_required", ReviewRequired: true, Notes: "security_protocol is inferred from the server protocol; SASL mechanism and credentials must be supplied before execution."},
		{ID: "asyncapi-schema-registry", Area: "Schemas", Construct: "Confluent Schema Registry refs", Status: "partial", ExecutionStatus: "review_required", ReviewRequired: true, Fields: []string{"schema_registry_url", "schema_id", "subject", "schema"}, Notes: "Kafka JSON Schema and Avro serde config is emitted when supplied; live replay still requires registry URL or schema id and matching schema material."},
	}
}

// GetSupportMatrixNotes returns top-level caveats for the AsyncAPI importer.
func GetSupportMatrixNotes() []string {
	return []string{
		"AsyncAPI import targets Kafka when a Kafka server is present, otherwise AMQP/JMS servers (jms-solace); MQTT and other non-messaging bindings are review-only.",
		"Generated scripts default to config.kafka.mock so they validate and run immediately; disable mock once a real cluster is configured.",
		"JSON Schema and Avro payloads are sampled into request bodies; Protobuf remains review-only until a safe synthesizer is added.",
		"AsyncAPI 2.x publish/subscribe is normalized from the external-client perspective (publish=produce, subscribe=consume).",
	}
}
