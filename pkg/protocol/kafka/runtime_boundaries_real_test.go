//go:build kafka_real

package kafka

import (
	"context"
	"testing"
)

func TestKafkaInitSupportedIsolationKeepsRealClientLazy(t *testing.T) {
	for _, level := range []string{"", " read_COMMITTED ", " read_UNCOMMITTED "} {
		t.Run(level, func(t *testing.T) {
			instance := &Kafka{name: "kafka"}
			if err := instance.Init(context.Background(), map[string]any{
				"kafka_isolation_level": level, "kafka_brokers": []string{"127.0.0.1:1"},
			}); err != nil {
				t.Fatal(err)
			}
			client := instance.client.(*franzClient)
			if client.cl != nil || client.replyCl != nil || client.transact != nil {
				t.Fatal("Init created a network client")
			}
			if client.cfg.IsolationLevel != instance.cfg.IsolationLevel {
				t.Fatal("real client received a different isolation level")
			}
			if err := instance.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
