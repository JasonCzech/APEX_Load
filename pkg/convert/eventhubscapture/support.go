package eventhubscapture

// SupportMatrixEntry describes coverage of one Event Hubs Capture construct.
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

// GetSupportMatrix returns the Event Hubs Capture import support matrix.
func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "ehcapture-container", Area: "Container", Construct: "Avro Object Container File", Status: "supported", ExecutionStatus: "executable", Fields: []string{"Body", "Properties", "SystemProperties", "SequenceNumber"}, Notes: "Capture's Avro OCF is decoded record by record into eventhubs_produce actions."},
		{ID: "ehcapture-body", Area: "Event", Construct: "EventData.Body", Status: "supported", ExecutionStatus: "executable", Fields: []string{"Body"}, Notes: "The captured body is replayed verbatim as the produce payload; non-UTF-8 (binary) bodies are stored base64 with body_encoding=base64 and decoded on send."},
		{ID: "ehcapture-properties", Area: "Event", Construct: "EventData.Properties", Status: "partial", ExecutionStatus: "review_required", ReviewRequired: true, Fields: []string{"Properties"}, Notes: "String application properties become action headers. Numeric, boolean, and binary properties are preserved in metadata and review-gated because the current DSL header surface cannot retain their AMQP types."},
		{ID: "ehcapture-partition-key", Area: "Routing", Construct: "x-opt-partition-key", Status: "supported", ExecutionStatus: "executable", Fields: []string{"SystemProperties[x-opt-partition-key]"}, Notes: "When present, the captured partition key seeds params.partition_key for co-location."},
		{ID: "ehcapture-sequence", Area: "Event", Construct: "SequenceNumber / Offset / EnqueuedTimeUtc", Status: "partial", ExecutionStatus: "executable", ReviewRequired: false, Notes: "Sequence number is preserved in action metadata for traceability; offset/enqueue time are informational only."},
		{ID: "ehcapture-avro-records", Area: "Container", Construct: "Avro data file (non-Capture schema)", Status: "partial", ExecutionStatus: "executable", ReviewRequired: true, Notes: "A container whose schema is not the Capture EventData envelope is a plain data file: each record becomes one event body. String and bytes schemas are sent verbatim; every other schema is rendered as JSON in schema field order with unions unwrapped, exact longs, logical types as their underlying number, decimals as exact numbers, and bytes/fixed as base64. No properties or partition keys exist to import."},
		{ID: "ehcapture-namespace", Area: "Target", Construct: "namespace / event hub", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Capture files carry no namespace; the script imports in mock mode — set config.eventhubs.namespace/connection_string and build -tags eventhubs_real to run live."},
	}
}

// GetSupportMatrixNotes returns top-level caveats for the Capture importer.
func GetSupportMatrixNotes() []string {
	return []string{
		"Event Hubs Capture import targets the native eventhubs protocol; each captured event becomes an eventhubs_produce action.",
		"Avro data files that are not Capture output are accepted too: each record becomes one event body (JSON in schema field order unless the schema is a plain string or bytes).",
		"Generated scripts default to config.eventhubs.mock so they validate and run immediately; set a namespace/connection string and build -tags eventhubs_real to drive a real hub.",
		"Only the first max_events (default 25, at most 1000) events are seeded as actions; the full event count is read from the container block headers and reported for sizing.",
		"Binary bodies are base64-encoded in the draft and decoded by the runtime so non-UTF-8 payloads round-trip losslessly.",
		"Typed or binary application properties are never stringified: they are preserved as typed metadata and require an explicit mapping before promotion.",
		"Uploads are bounded before decoding: a malformed or truncated block, or one that would decompress beyond 256 MiB, ends the import at the blocks before it with a warning.",
	}
}
