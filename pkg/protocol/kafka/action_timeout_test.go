package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type blockingKafkaProduceClient struct{ kafkaClient }

func (blockingKafkaProduceClient) Produce(ctx context.Context, _ string, _ kafkaRecord) (recordMeta, error) {
	<-ctx.Done()
	return recordMeta{}, ctx.Err()
}

func TestKafkaActionTimeoutBoundsProduce(t *testing.T) {
	const timeout = 20 * time.Millisecond
	k := newMockKafka(t, map[string]any{"kafka_topic": "events", "kafka_timeout_ms": int(timeout / time.Millisecond)})
	k.client = blockingKafkaProduceClient{kafkaClient: k.client}
	start := time.Now()
	result, err := k.Execute(context.Background(), &protocol.Action{Name: "produce", Type: "kafka_produce", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, context.DeadlineExceeded.Error()) {
		t.Fatalf("result = %#v, want deadline failure", result)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("produce took %s, want configured %s action timeout", elapsed, timeout)
	}
}
