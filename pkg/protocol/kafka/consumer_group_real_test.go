//go:build kafka_real

package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRealKafkaConsumerGroupMismatchDoesNotOpenClient(t *testing.T) {
	for _, operation := range []string{"commit", "consume", "seek"} {
		t.Run(operation, func(t *testing.T) {
			client := newRealClient(config{ConsumerGroup: "workers", Brokers: []string{"127.0.0.1:1"}}).(*franzClient)
			defer client.Teardown(context.Background())
			var err error
			switch operation {
			case "commit":
				k := &Kafka{name: "kafka", cfg: client.cfg, client: client}
				result, executeErr := k.Execute(context.Background(), &protocol.Action{Type: "kafka_commit", Params: map[string]any{"consumer_group": "other-workers"}})
				if executeErr != nil || result.Success || result.StatusCode != 400 || !strings.Contains(result.ErrorMessage, "one group per client") || k.session.Commits() != 0 {
					t.Fatalf("mismatched commit was not fenced: %+v %v", result, executeErr)
				}
			case "consume":
				_, _, err = client.Consume(context.Background(), "orders", "other-workers", 0)
			case "seek":
				err = client.Seek(context.Background(), "orders", "other-workers", "earliest")
			}
			if operation != "commit" && (err == nil || !strings.Contains(err.Error(), "one group per client")) {
				t.Fatalf("mismatched %s was not fenced: %v", operation, err)
			}
			if client.cl != nil || client.transact != nil {
				t.Fatal("refused group identity opened a native client")
			}
		})
	}
}

func TestRealKafkaCommitStillRefusesConsumerTransactionMode(t *testing.T) {
	client := newRealClient(config{ConsumerTransactions: true, ConsumerGroup: "workers", TransactionalID: "consumer-scope", IsolationLevel: "read_committed", Brokers: []string{"127.0.0.1:1"}}).(*franzClient)
	defer client.Teardown(context.Background())
	k := &Kafka{name: "kafka", cfg: client.cfg, client: client}
	result, err := k.Execute(context.Background(), &protocol.Action{Type: "kafka_commit", Params: map[string]any{"consumer_group": "workers"}})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, "requires kafka_commit_transaction") || k.session.Commits() != 0 {
		t.Fatalf("ordinary commit bypassed consumer-transaction mode: %+v %v", result, err)
	}
	if client.cl != nil || client.transact != nil {
		t.Fatal("refused ordinary commit opened a transaction session")
	}
}
