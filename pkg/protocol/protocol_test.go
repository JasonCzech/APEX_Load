package protocol

import "testing"

func TestClassifyPayloadSize(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{name: "empty", bytes: 0, expected: ""},
		{name: "small", bytes: 8 * 1024, expected: "small"},
		{name: "medium lower", bytes: 16 * 1024, expected: "medium"},
		{name: "medium upper", bytes: 64 * 1024, expected: "medium"},
		{name: "large", bytes: 128 * 1024, expected: "large"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ClassifyPayloadSize(testCase.bytes); got != testCase.expected {
				t.Fatalf("ClassifyPayloadSize(%d) = %q, want %q", testCase.bytes, got, testCase.expected)
			}
		})
	}
}

func TestBuildProtocolEvidenceLabelsJMSGenerically(t *testing.T) {
	evidence := BuildProtocolEvidence(&Action{Type: "jms_publish", Target: "orders"}, &Result{
		Success:          true,
		DependencyType:   "jms_queue",
		DependencyTarget: "orders",
	})
	if evidence["protocol"] != "jms" {
		t.Fatalf("protocol evidence = %#v, want provider-neutral jms", evidence)
	}
}

func TestBuildProtocolEvidenceSurfacesConsumerOffsetLag(t *testing.T) {
	evidence := BuildProtocolEvidence(&Action{Type: "kafka_consume", Target: "orders"}, &Result{
		Success:          true,
		DependencyType:   "kafka_topic",
		DependencyTarget: "orders",
		ServerTelemetry:  &ServerTelemetry{MessagesReceived: 1, ConsumerOffsetLag: 5},
	})
	messages, ok := evidence["messages"].(map[string]any)
	if !ok {
		t.Fatalf("evidence has no messages map: %#v", evidence)
	}
	if messages["consumer_offset_lag"] != int64(5) {
		t.Fatalf("consumer_offset_lag = %#v, want int64(5)", messages["consumer_offset_lag"])
	}
}
