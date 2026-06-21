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
		{ID: "ehcapture-body", Area: "Event", Construct: "EventData.Body", Status: "supported", ExecutionStatus: "executable", Fields: []string{"Body"}, Notes: "The captured body is replayed verbatim as the produce payload (binary bodies are stored as the raw byte string)."},
		{ID: "ehcapture-properties", Area: "Event", Construct: "EventData.Properties", Status: "supported", ExecutionStatus: "executable", Fields: []string{"Properties"}, Notes: "Application properties become action headers."},
		{ID: "ehcapture-partition-key", Area: "Routing", Construct: "x-opt-partition-key", Status: "supported", ExecutionStatus: "executable", Fields: []string{"SystemProperties[x-opt-partition-key]"}, Notes: "When present, the captured partition key seeds params.partition_key for co-location."},
		{ID: "ehcapture-sequence", Area: "Event", Construct: "SequenceNumber / Offset / EnqueuedTimeUtc", Status: "partial", ExecutionStatus: "executable", ReviewRequired: false, Notes: "Sequence number is preserved in action metadata for traceability; offset/enqueue time are informational only."},
		{ID: "ehcapture-namespace", Area: "Target", Construct: "namespace / event hub", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Capture files carry no namespace; the script imports in mock mode — set config.eventhubs.namespace/connection_string and build -tags eventhubs_real to run live."},
	}
}

// GetSupportMatrixNotes returns top-level caveats for the Capture importer.
func GetSupportMatrixNotes() []string {
	return []string{
		"Event Hubs Capture import targets the native eventhubs protocol; each captured event becomes an eventhubs_produce action.",
		"Generated scripts default to config.eventhubs.mock so they validate and run immediately; set a namespace/connection string and build -tags eventhubs_real to drive a real hub.",
		"Only the first max_events (default 25) events are seeded as actions; the full event count is reported for sizing.",
		"Binary bodies are stored as their raw byte string — re-serialize before promotion if the payload is not UTF-8 text.",
	}
}
