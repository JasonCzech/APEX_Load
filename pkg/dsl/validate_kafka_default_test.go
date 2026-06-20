//go:build !kafka_real

package dsl

import "testing"

func TestKafkaRealAdapterUnavailableInDefaultBuild(t *testing.T) {
	if KafkaRealAdapterAvailable() {
		t.Fatal("KafkaRealAdapterAvailable() must be false in a default (untagged) build")
	}
}

func TestValidateKafkaRealModeFailsClosedInDefaultBuild(t *testing.T) {
	issues := ValidateScriptIssues(kafkaScript(func(s *Script) {
		s.Config.Kafka.Mock = false
		s.Config.Kafka.Brokers = []string{"broker:9092"}
	}))
	if !hasIssueContaining(issues, "real Kafka network adapter is not available") {
		t.Fatalf("expected the real-adapter-unavailable issue, got: %v", issues)
	}
	if !hasIssueContaining(issues, "-tags kafka_real") {
		t.Fatalf("expected the issue to mention -tags kafka_real, got: %v", issues)
	}
}
