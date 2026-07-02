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

func TestBuildProtocolEvidenceRecognizesSOAPAndRedactsMetadata(t *testing.T) {
	evidence := BuildProtocolEvidence(&Action{
		Type:            "request",
		Target:          "https://soap.example.test/AuthService",
		Headers:         map[string]string{"SOAPAction": "urn:auth#super-secret"},
		RedactedHeaders: map[string]string{"SOAPAction": "urn:auth#[REDACTED]"},
		Params:          map[string]any{"lr_parameters": map[string]any{"Password": "super-secret"}},
		RedactedParams:  map[string]any{"lr_parameters": map[string]any{"Password": "[REDACTED]"}},
		SOAP:            &SOAPActionConfig{Action: "urn:auth#super-secret", Operation: "Login", BodyMode: "generated"},
		RedactedSOAP:    &SOAPActionConfig{Action: "urn:auth#[REDACTED]", Operation: "Login", BodyMode: "generated"},
	}, &Result{
		Success:          true,
		DependencyType:   "soap",
		DependencyTarget: "soap.example.test#Login",
		StatusCode:       200,
		Extractions:      map[string]string{"soap_version": "1.1"},
	})
	if evidence["protocol"] != "soap" {
		t.Fatalf("protocol evidence = %#v, want soap", evidence)
	}
	if evidence["operation"] != "Login" {
		t.Fatalf("operation = %#v, want Login", evidence["operation"])
	}
	metadata, ok := evidence["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", evidence["metadata"])
	}
	headers, ok := metadata["headers"].(map[string]string)
	if !ok || headers["SOAPAction"] != "urn:auth#[REDACTED]" {
		t.Fatalf("headers = %#v, want redacted SOAPAction", metadata["headers"])
	}
	soapMetadata, ok := metadata["soap"].(map[string]any)
	if !ok || soapMetadata["action"] != "urn:auth#[REDACTED]" {
		t.Fatalf("soap metadata = %#v, want redacted action", metadata["soap"])
	}
	params, ok := metadata["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v, want redacted params map", metadata["params"])
	}
	lrParams, ok := params["lr_parameters"].(map[string]any)
	if !ok || lrParams["Password"] != "[REDACTED]" {
		t.Fatalf("params = %#v, want redacted lr parameter", metadata["params"])
	}
}
