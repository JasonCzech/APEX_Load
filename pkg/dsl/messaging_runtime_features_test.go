package dsl

import "testing"

func TestKafkaConsumerTransactionsRequireCompleteConfiguration(t *testing.T) {
	s := kafkaScript(func(s *Script) {
		s.Config.Kafka.Mock = false
		s.Config.Kafka.ConsumerTransactions = true
		s.Config.Kafka.ConsumerGroup = "worker"
		s.Config.Kafka.TransactionalID = "pipeline"
		s.Config.Kafka.IsolationLevel = "read_committed"
		s.Config.Kafka.Brokers = []string{"broker:9092"}
	})
	if hasIssueContaining(ValidateScriptIssues(s), "consumer_transactions requires") {
		t.Fatal("valid coordination rejected")
	}
	for _, mutate := range []func(*KafkaConfig){func(c *KafkaConfig) { c.ConsumerGroup = "" }, func(c *KafkaConfig) { c.TransactionalID = "" }, func(c *KafkaConfig) { c.IsolationLevel = "read_uncommitted" }, func(c *KafkaConfig) { c.Mock = true }} {
		copy := *s
		mutate(&copy.Config.Kafka)
		if !hasIssueContaining(ValidateScriptIssues(&copy), "consumer_transactions requires") {
			t.Fatal("incomplete configuration accepted")
		}
	}
	if len(RequiredMessagingRuntimeFeatures(s)) != 1 {
		t.Fatal("missing coordination capability")
	}
}

func TestEventHubsProcessorRequiresVersionedRuntime(t *testing.T) {
	s := &Script{Protocol: "eventhubs"}
	if len(RequiredMessagingRuntimeFeatures(s)) != 0 {
		t.Fatal("observer requires new runtime")
	}
	s.Config.EventHubs.ConsumerMode = "processor"
	features := RequiredMessagingRuntimeFeatures(s)
	if len(features) != 1 || features[0] != "eventhubs_processor_checkpoints_v1" {
		t.Fatal(features)
	}
}
