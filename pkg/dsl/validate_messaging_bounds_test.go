package dsl

import (
	"strings"
	"testing"

	eventhubsprotocol "github.com/jasonczech/apexload/pkg/protocol/eventhubs"
	jmsprotocol "github.com/jasonczech/apexload/pkg/protocol/jms"
	kafkaprotocol "github.com/jasonczech/apexload/pkg/protocol/kafka"
)

func requireValidationIssueContaining(t *testing.T, issues []string, substring string) {
	t.Helper()
	for _, issue := range issues {
		if strings.Contains(issue, substring) {
			return
		}
	}
	t.Fatalf("issues %v do not contain %q", issues, substring)
}

func TestKafkaValidationMirrorsRuntimeResourceBounds(t *testing.T) {
	script := &Script{
		Name: "kafka", Protocol: "kafka",
		Config:  ScriptConfig{Kafka: KafkaConfig{Mock: true, Topic: "orders", Partitions: kafkaprotocol.MaxConfiguredPartitions + 1}},
		Actions: []Action{{Name: "produce", Type: "kafka_produce", Body: "x"}},
	}
	requireValidationIssueContaining(t, ValidateScriptIssues(script), "partitions must not exceed")
	script.Config.Kafka.Partitions = 1
	script.Actions[0].Target = strings.Repeat("t", kafkaprotocol.MaxKafkaTopicBytes+1)
	requireValidationIssueContaining(t, ValidateScriptIssues(script), "topic must not exceed")
}

func TestEventHubsValidationMirrorsRuntimeResourceBounds(t *testing.T) {
	script := &Script{
		Name: "eventhubs", Protocol: "eventhubs",
		Config: ScriptConfig{EventHubs: EventHubsConfig{Mock: true, EventHub: "orders"}},
		Actions: []Action{{Name: "consume", Type: "eventhubs_consume", Params: map[string]any{
			"timeout_ms": int(eventhubsprotocol.MaxConfiguredTimeout.Milliseconds()) + 1,
		}}},
	}
	requireValidationIssueContaining(t, ValidateScriptIssues(script), "timeout_ms must be between")
}

func TestJMSValidationMirrorsRuntimeBatchBounds(t *testing.T) {
	sequenceNumbers := make([]any, jmsprotocol.MaxJMSSequenceNumbers+1)
	script := &Script{
		Name: "jms", Protocol: "jms-solace",
		Config: ScriptConfig{JMS: JMSConfig{Mock: true, Destination: "orders"}},
		Actions: []Action{{Name: "deferred", Type: "jms_receive_deferred", Params: map[string]any{
			"sequence_numbers": sequenceNumbers,
		}}},
	}
	requireValidationIssueContaining(t, ValidateScriptIssues(script), "sequence_numbers must not exceed")
}
