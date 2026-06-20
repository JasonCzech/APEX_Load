//go:build !kafka_real

package kafka

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestKafkaRealAdapterUnavailable asserts the fail-closed behavior of the
// default build: with mock disabled, newRealClient returns the unavailable
// stub so produce fails and HealthCheck errors. Under -tags kafka_real this
// test is meaningless (a real franz client would attempt a live connection and
// block), so it is constrained to the default build.
func TestKafkaRealAdapterUnavailable(t *testing.T) {
	ctx := context.Background()
	k := &Kafka{name: "kafka"}
	if err := k.Init(ctx, map[string]any{
		"kafka_brokers": []string{"broker:9092"},
		"kafka_topic":   "t",
		"kafka_mock":    false,
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	res, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure: real adapter is not compiled into this build")
	}
	if err := k.HealthCheck(ctx); err == nil {
		t.Fatal("expected HealthCheck to fail for unavailable real adapter")
	}
}
