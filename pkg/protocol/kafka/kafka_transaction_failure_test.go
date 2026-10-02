package kafka

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// flakyClient wraps a kafkaClient to inject failures for transaction-path tests.
type flakyClient struct {
	kafkaClient
	produceFailOn int // fail the Nth Produce (1-based); 0 = never
	produceCount  int
	commitErr     error
	abortCount    int
}

func (f *flakyClient) Produce(ctx context.Context, topic string, rec kafkaRecord) (recordMeta, error) {
	f.produceCount++
	if f.produceFailOn > 0 && f.produceCount == f.produceFailOn {
		return recordMeta{}, fmt.Errorf("simulated produce failure on call %d", f.produceCount)
	}
	return f.kafkaClient.Produce(ctx, topic, rec)
}

func (f *flakyClient) Commit(ctx context.Context) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	return f.kafkaClient.Commit(ctx)
}

func (f *flakyClient) AbortTransaction(ctx context.Context) error {
	f.abortCount++
	return f.kafkaClient.AbortTransaction(ctx)
}

// TestKafkaCommitClientCommitErrorSurfaces covers kafka-1: kafka_commit now calls
// client.Commit and a broker-side commit failure must surface (not silently
// report success while bumping a counter).
func TestKafkaCommitClientCommitErrorSurfaces(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "events"})
	k.client = &flakyClient{kafkaClient: k.client, commitErr: fmt.Errorf("offset commit boom")}

	res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_commit"})
	if err != nil {
		t.Fatalf("Execute transport error: %v", err)
	}
	if res.Success {
		t.Fatal("kafka_commit should fail when client.Commit errors")
	}
	if !strings.Contains(res.ErrorMessage, "offset commit boom") {
		t.Fatalf("error = %q, want the client.Commit failure surfaced", res.ErrorMessage)
	}
	if k.session.Commits() != 0 {
		t.Fatalf("session commits = %d, want 0 (commit failed)", k.session.Commits())
	}
}

// TestKafkaCommitTransactionProduceFailureAborts covers xcut-tests-2: a buffered
// produce failing during the commit flush must abort the transaction, return the
// error, leave no records visible, and reset transaction state.
func TestKafkaCommitTransactionProduceFailureAborts(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "orders", "kafka_transactional_id": "tx-1"})
	fc := &flakyClient{kafkaClient: k.client, produceFailOn: 1} // fail the first flush
	k.client = fc

	exec := func(a *protocol.Action) *protocol.Result {
		r, err := k.Execute(ctx, a)
		if err != nil {
			t.Fatalf("execute %s: %v", a.Type, err)
		}
		return r
	}
	exec(&protocol.Action{Name: "begin", Type: "kafka_begin_transaction"})
	exec(&protocol.Action{Name: "p1", Type: "kafka_produce", Body: []byte("a")})
	exec(&protocol.Action{Name: "p2", Type: "kafka_produce", Body: []byte("b")})

	res := exec(&protocol.Action{Name: "commit", Type: "kafka_commit_transaction"})
	if res.Success {
		t.Fatal("commit_transaction should fail when a buffered produce fails")
	}
	if k.txOpen {
		t.Fatal("txOpen must be false after a failed commit")
	}
	if fc.abortCount == 0 {
		t.Fatal("a failed commit flush must abort the transaction on the client")
	}
	// The first flush failed before any record reached the broker.
	if cons := exec(&protocol.Action{Name: "c", Type: "kafka_consume"}); cons.Success {
		t.Fatal("no records should be visible after the aborted commit")
	}
}

func TestKafkaMockCommitCapacityFailureIsAtomicAcrossTopics(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_transactional_id": "tx-capacity"})
	mock := k.client.(mockClient)

	// Leave exactly one free topic slot. A sequential flush would append the
	// first new topic, then fail on the second and leak the first record despite
	// reporting that the transaction commit failed.
	mock.broker.mu.Lock()
	for i := 0; i < maxBrokerTopicKeys-1; i++ {
		mock.broker.logs[fmt.Sprintf("occupied-%d", i)] = nil
	}
	mock.broker.mu.Unlock()

	exec := func(action *protocol.Action) *protocol.Result {
		result, err := k.Execute(ctx, action)
		if err != nil {
			t.Fatalf("execute %s: %v", action.Type, err)
		}
		return result
	}
	if result := exec(&protocol.Action{Name: "begin", Type: "kafka_begin_transaction"}); !result.Success {
		t.Fatalf("begin failed: %s", result.ErrorMessage)
	}
	if result := exec(&protocol.Action{Name: "first", Type: "kafka_produce", Target: "first-new-topic", Body: []byte("first")}); !result.Success {
		t.Fatalf("first buffered produce failed: %s", result.ErrorMessage)
	}
	if result := exec(&protocol.Action{Name: "second", Type: "kafka_produce", Target: "second-new-topic", Body: []byte("second")}); !result.Success {
		t.Fatalf("second buffered produce failed: %s", result.ErrorMessage)
	}

	commit := exec(&protocol.Action{Name: "commit", Type: "kafka_commit_transaction"})
	if commit.Success || !strings.Contains(commit.ErrorMessage, "capacity") {
		t.Fatalf("commit result = %#v, want topic-capacity failure", commit)
	}
	if k.txOpen || len(k.txBuffer) != 0 {
		t.Fatalf("transaction state survived failed commit: open=%v buffered=%d", k.txOpen, len(k.txBuffer))
	}
	if k.session.Commits() != 0 {
		t.Fatalf("session commits = %d, want 0", k.session.Commits())
	}

	mock.broker.mu.Lock()
	defer mock.broker.mu.Unlock()
	if _, exists := mock.broker.logs["first-new-topic"]; exists {
		t.Fatal("failed transaction leaked the first topic's record")
	}
	if _, exists := mock.broker.logs["second-new-topic"]; exists {
		t.Fatal("failed transaction created the second topic")
	}
	if got := len(mock.broker.logs); got != maxBrokerTopicKeys-1 {
		t.Fatalf("topic keys = %d, want unchanged %d", got, maxBrokerTopicKeys-1)
	}
}

// TestKafkaTeardownAbortsOpenTransaction covers xcut-tests-3: Teardown must abort
// a transaction the script left open, discard buffered records, and record the
// rollback on the session.
func TestKafkaTeardownAbortsOpenTransaction(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "orders", "kafka_transactional_id": "tx-2"})
	fc := &flakyClient{kafkaClient: k.client}
	k.client = fc

	exec := func(a *protocol.Action) {
		r, err := k.Execute(ctx, a)
		if err != nil || !r.Success {
			t.Fatalf("execute %s failed: err=%v ok=%v", a.Type, err, r != nil && r.Success)
		}
	}
	exec(&protocol.Action{Name: "begin", Type: "kafka_begin_transaction"})
	exec(&protocol.Action{Name: "p1", Type: "kafka_produce", Body: []byte("a")})
	exec(&protocol.Action{Name: "p2", Type: "kafka_produce", Body: []byte("b")})

	// No commit/abort: Teardown must auto-abort.
	if err := k.Teardown(ctx); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if k.txOpen {
		t.Fatal("txOpen must be false after Teardown")
	}
	if fc.abortCount == 0 {
		t.Fatal("Teardown must abort the open transaction")
	}
	if k.session.Aborts() != 1 {
		t.Fatalf("session aborts = %d, want 1", k.session.Aborts())
	}
	cons, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if cons.Success {
		t.Fatal("buffered records must be discarded after Teardown")
	}
}

type ownershipAbortClient struct{ kafkaClient }

func (c ownershipAbortClient) CommitTransaction(context.Context) error {
	return errKafkaTransactionAborted
}
func TestKnownOwnershipAbortAllowsNextTransaction(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_transactional_id": "recover"})
	k.client = ownershipAbortClient{k.client}
	result := &protocol.Result{Extractions: map[string]string{}}
	if err := k.beginTransaction(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if err := k.commitTransaction(context.Background(), result); err == nil || k.txOpen || k.txPoisoned || k.session.Commits() != 0 {
		t.Fatal("known abort was committed or poisoned")
	}
	if err := k.beginTransaction(context.Background(), result); err != nil {
		t.Fatal("recovery failed", err)
	}
}
func TestKafkaSeekRejectsInexactOffsets(t *testing.T) {
	for _, input := range []any{1.5, -1, float64(9007199254740992), "-1", []string{"0"}} {
		if _, err := seekTarget(map[string]any{"offset": input}); err == nil {
			t.Fatalf("accepted %v", input)
		}
	}
	for _, input := range []any{0, float64(2), "9223372036854775807", "earliest", "latest"} {
		if _, err := seekTarget(map[string]any{"offset": input}); err != nil {
			t.Fatal(err)
		}
	}
}
