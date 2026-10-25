package messaging

import "testing"

// Kafka and JMS share one provenance rule, with the keys only Kafka filters
// kept on the JMS wire.
func TestMetadataKeyIsProvenance(t *testing.T) {
	for _, test := range []struct {
		key        string
		kafka, jms bool
	}{
		{key: "original_url", kafka: true, jms: true},
		{key: " Source_Path ", kafka: true, jms: true},
		{key: "source_step_id", kafka: true, jms: true},
		{key: "source_profile", kafka: true, jms: true},
		{key: "source_uid", kafka: true, jms: true},
		{key: "source_evidence", kafka: true, jms: true},
		{key: "ASYNCAPI_operation", kafka: true, jms: true},
		{key: "source_node_id", kafka: true},
		{key: "jmeter_thread", kafka: true},
		{key: "source_transaction"},
		{key: "trace_id"},
	} {
		if got := MetadataKeyIsProvenance(FamilyKafka, test.key); got != test.kafka {
			t.Errorf("kafka %q = %v, want %v", test.key, got, test.kafka)
		}
		if got := MetadataKeyIsProvenance(FamilyJMS, test.key); got != test.jms {
			t.Errorf("jms %q = %v, want %v", test.key, got, test.jms)
		}
		// Evidence is what every send keeps off the wire.
		if got := MetadataKeyIsEvidence(test.key); got != (test.kafka && test.jms) {
			t.Errorf("evidence %q = %v, want %v", test.key, got, test.kafka && test.jms)
		}
	}
}
