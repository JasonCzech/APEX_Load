//go:build kafka_real

package dsl

import "testing"

func TestKafkaRealAdapterAvailableUnderTag(t *testing.T) {
	if !KafkaRealAdapterAvailable() {
		t.Fatal("KafkaRealAdapterAvailable() must be true under -tags kafka_real")
	}
}

func TestValidateKafkaRealModeAcceptedUnderTag(t *testing.T) {
	issues := ValidateScriptIssues(kafkaScript(func(s *Script) {
		s.Config.Kafka.Mock = false
		s.Config.Kafka.Brokers = []string{"broker:9092"}
	}))
	if hasIssueContaining(issues, "real Kafka network adapter is not available") {
		t.Fatalf("real-adapter-unavailable issue must not appear under -tags kafka_real, got: %v", issues)
	}
	if len(issues) != 0 {
		t.Fatalf("expected a clean non-mock Kafka script under -tags kafka_real, got: %v", issues)
	}
}

// Smoke test: an Azure Event Hubs Kafka-endpoint profile config must pass
// validation under -tags kafka_real (the EH.1 happy path). PLAIN + sasl_ssl are
// the Event Hubs auth values; the connection string is supplied via a secret ref.
func TestValidateEventHubsKafkaEndpointConfigUnderTag(t *testing.T) {
	issues := ValidateScriptIssues(kafkaScript(func(s *Script) {
		s.Config.Kafka.Mock = false
		s.Config.Kafka.Profile = "eventhubs"
		s.Config.Kafka.Brokers = []string{"my-namespace.servicebus.windows.net:9093"}
		s.Config.Kafka.SecurityProtocol = "sasl_ssl"
		s.Config.Kafka.SASLMechanism = "plain"
		s.Config.Kafka.SASLUsername = "$ConnectionString"
		s.Config.Kafka.SASLPasswordSecret = "eventhubs_conn"
	}))
	if len(issues) != 0 {
		t.Fatalf("expected an Event Hubs Kafka-endpoint config to validate cleanly under -tags kafka_real, got: %v", issues)
	}
}
