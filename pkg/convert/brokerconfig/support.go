package brokerconfig

// SupportMatrixEntry describes coverage of one broker-config construct.
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

// GetSupportMatrix returns the broker-config import support matrix.
func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "brokerconfig-mqsc-queues", Area: "IBM MQ MQSC", Construct: "DEFINE QLOCAL/QREMOTE/QALIAS/QMODEL", Status: "supported", ExecutionStatus: "review_required", ReviewRequired: true, Fields: []string{"object name"}, Notes: "Queue objects become jms-ibmmq destinations (publish+consume skeleton). SYSTEM.* objects are skipped. The IBM MQ runtime adapter is not built in, so execution is mock-only/review."},
		{ID: "brokerconfig-mqsc-topics", Area: "IBM MQ MQSC", Construct: "DEFINE TOPIC", Status: "supported", ExecutionStatus: "review_required", ReviewRequired: true, Fields: []string{"object name"}, Notes: "Topic objects become jms-ibmmq topic destinations. Topic strings (TOPICSTR) are not yet mapped — the object name is used."},
		{ID: "brokerconfig-jndi-queues", Area: "JMS JNDI", Construct: "queue.<name> = <destination>", Status: "supported", ExecutionStatus: "executable", Fields: []string{"destination value"}, Notes: "JNDI queue/topic bindings become jms-solace destinations (executable via -tags jms_solace_real). The provider defaults to solace — confirm before running."},
		{ID: "brokerconfig-jndi-connection", Area: "JMS JNDI", Construct: "connectionFactory.* / messageVPN", Status: "supported", ExecutionStatus: "executable", Fields: []string{"uri", "message_vpn"}, Notes: "A connectionFactory URI seeds config.jms.broker_url; messageVPN seeds config.jms.message_vpn."},
		{ID: "brokerconfig-semp", Area: "Solace SEMP", Construct: "SEMP CLI/XML config export", Status: "not_supported", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Solace SEMP export parsing is not yet implemented; export queue bindings via JNDI as an interim path."},
	}
}

// GetSupportMatrixNotes returns top-level caveats for the broker-config importer.
func GetSupportMatrixNotes() []string {
	return []string{
		"Broker-config import auto-detects IBM MQ MQSC scripts and JMS JNDI .properties files; set format explicitly to override.",
		"Imports produce a runnable skeleton (one publish + one consume per discovered destination) in mock mode — author the message bodies before promoting.",
		"MQSC imports target jms-ibmmq, whose runtime adapter is not built into APEX Load, so they are review-only for live execution.",
		"JNDI imports target jms-solace and are executable against a real broker with a -tags jms_solace_real build.",
	}
}
