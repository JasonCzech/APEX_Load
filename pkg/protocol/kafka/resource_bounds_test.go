package kafka

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestKafkaInitRejectsResourceAmplifyingNumericConfiguration(t *testing.T) {
	tests := []struct {
		key   string
		value any
	}{
		{"kafka_partitions", float64(MaxConfiguredPartitions + 1)},
		{"kafka_timeout_ms", float64(MaxConfiguredTimeout.Milliseconds() + 1)},
		{"kafka_max_message_bytes", float64(MaxConfiguredMessageBytes + 1)},
		{"kafka_schema_id", float64(math.MaxInt32) + 1},
		{"kafka_partitions", math.Inf(1)},
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			instance := &Kafka{name: "kafka"}
			if err := instance.Init(context.Background(), map[string]any{"kafka_mock": true, test.key: test.value}); err == nil {
				t.Fatalf("Init accepted %s=%v", test.key, test.value)
			}
		})
	}
}

func TestKafkaRuntimeRejectsOversizedDataDrivenKeys(t *testing.T) {
	instance := &Kafka{name: "kafka"}
	if err := instance.Init(context.Background(), map[string]any{"kafka_mock": true}); err != nil {
		t.Fatal(err)
	}
	defer instance.Teardown(context.Background())
	result, err := instance.Execute(context.Background(), &protocol.Action{
		Name: "produce", Type: "kafka_produce",
		Target: strings.Repeat("t", MaxKafkaTopicBytes+1), Body: []byte("x"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.StatusCode != 400 || !strings.Contains(result.ErrorMessage, "topic exceeds") {
		t.Fatalf("oversized topic result = %#v", result)
	}
}

func TestKafkaHardRecordCapAppliesWhenBrokerCapIsUnset(t *testing.T) {
	instance := &Kafka{cfg: config{}}
	if err := instance.checkRecordSize(kafkaRecord{Value: make([]byte, MaxConfiguredMessageBytes+1)}); err == nil {
		t.Fatal("unset max_message_bytes disabled the protocol hard cap")
	}
}

func TestKafkaMemoryBrokerHasAggregateRetentionBudget(t *testing.T) {
	broker := newMemoryBroker()
	broker.totalBytes = maxBrokerRetainedBytes
	if _, err := broker.produce("orders", kafkaRecord{Value: []byte("x")}); err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("produce at aggregate cap error = %v", err)
	}
	if len(broker.logs) != 0 {
		t.Fatal("rejected aggregate-cap produce mutated broker logs")
	}
}
