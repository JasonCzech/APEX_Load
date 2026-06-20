package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestKafkaCommitRecordsCommitCount verifies kafka_commit is no longer a no-op:
// it records a commit on the shared session and reports CommitCount telemetry.
func TestKafkaCommitRecordsCommitCount(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "events", "kafka_consumer_group": "cg-1"})
	res, err := k.Execute(context.Background(), &protocol.Action{Name: "commit", Type: "kafka_commit"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.Success {
		t.Fatalf("commit success = false: %s", res.ErrorMessage)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.CommitCount != 1 {
		t.Fatalf("commit CommitCount = %+v, want 1", res.ServerTelemetry)
	}
	if k.session.Commits() != 1 {
		t.Fatalf("session commits = %d, want 1", k.session.Commits())
	}
}

// TestKafkaConsumerOffsetLagSurfaced verifies the broker offset backlog is
// reported on ServerTelemetry (P1.5) instead of only as a string extraction.
func TestKafkaConsumerOffsetLagSurfaced(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "lag.events"})
	for i := 0; i < 3; i++ {
		if _, err := k.Execute(context.Background(), &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("x")}); err != nil {
			t.Fatalf("produce: %v", err)
		}
	}
	res, err := k.Execute(context.Background(), &protocol.Action{Name: "c", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !res.Success {
		t.Fatalf("consume failed: %s", res.ErrorMessage)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.ConsumerOffsetLag != 2 {
		t.Fatalf("ConsumerOffsetLag = %+v, want 2 (3 produced, 1 consumed)", res.ServerTelemetry)
	}
}

// TestKafkaSeekRepositionsConsumer verifies kafka_seek is no longer a no-op:
// the offset-based mock broker lets a consumer replay from earliest and skip to
// latest. (Pre-P2.1 the mock was destructive FIFO and could not seek.)
func TestKafkaSeekRepositionsConsumer(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "seek.events"})
	for _, body := range []string{"m0", "m1", "m2"} {
		if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(body)}); err != nil {
			t.Fatalf("produce %s: %v", body, err)
		}
	}
	// Consume m0 and m1 (cursor now at offset 2).
	for i := 0; i < 2; i++ {
		if _, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"}); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	// Seek back to earliest; the next consume replays from offset 0 (lag 2 again).
	sk, err := k.Execute(ctx, &protocol.Action{Name: "s", Type: "kafka_seek", Params: map[string]any{"offset": "earliest"}})
	if err != nil {
		t.Fatalf("seek: %v", err)
	}
	if !sk.Success {
		t.Fatalf("seek failed: %s", sk.ErrorMessage)
	}
	replay, err := k.Execute(ctx, &protocol.Action{Name: "r", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("replay consume: %v", err)
	}
	if replay.ServerTelemetry == nil || replay.ServerTelemetry.ConsumerOffsetLag != 2 {
		t.Fatalf("after seek-earliest the replay lag = %+v, want 2 (cursor reset to offset 0)", replay.ServerTelemetry)
	}

	// Seek to latest; the next consume finds nothing (caught up to the tail).
	if _, err := k.Execute(ctx, &protocol.Action{Name: "sl", Type: "kafka_seek", Params: map[string]any{"offset": "latest"}}); err != nil {
		t.Fatalf("seek latest: %v", err)
	}
	tail, err := k.Execute(ctx, &protocol.Action{Name: "ct", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("consume after seek latest: %v", err)
	}
	if tail.Success {
		t.Fatal("expected no record after seek to latest (consumer at tail)")
	}
}

// TestKafkaTransactionCommitMakesRecordsVisible verifies P2.1 part-2b EOS
// semantics on the mock: records produced inside a transaction are buffered
// (invisible to read_committed consumers) until commit, then visible atomically.
func TestKafkaTransactionCommitMakesRecordsVisible(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{
		"kafka_topic":            "tx.events",
		"kafka_transactional_id": "tx-1",
		"kafka_timeout_ms":       float64(40),
	})
	if bt, err := k.Execute(ctx, &protocol.Action{Name: "b", Type: "kafka_begin_transaction"}); err != nil || !bt.Success {
		t.Fatalf("begin: %v %s", err, bt.ErrorMessage)
	}
	for _, body := range []string{"t0", "t1"} {
		if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(body)}); err != nil {
			t.Fatalf("produce %s: %v", body, err)
		}
	}
	// Before commit, buffered records must not be visible.
	pre, err := k.Execute(ctx, &protocol.Action{Name: "pre", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("pre-commit consume: %v", err)
	}
	if pre.Success {
		t.Fatal("records must not be visible before commit (read_committed)")
	}
	cm, err := k.Execute(ctx, &protocol.Action{Name: "cm", Type: "kafka_commit_transaction"})
	if err != nil || !cm.Success {
		t.Fatalf("commit: %v %s", err, cm.ErrorMessage)
	}
	if cm.ServerTelemetry == nil || cm.ServerTelemetry.CommitCount != 1 {
		t.Fatalf("commit CommitCount = %+v, want 1", cm.ServerTelemetry)
	}
	// After commit, both records are visible.
	for i := 0; i < 2; i++ {
		res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"})
		if err != nil || !res.Success {
			t.Fatalf("post-commit consume %d: %v %s", i, err, res.ErrorMessage)
		}
	}
}

// TestKafkaTransactionAbortDiscardsRecords verifies an aborted transaction's
// buffered records are discarded (never visible) and an abort is counted.
func TestKafkaTransactionAbortDiscardsRecords(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{
		"kafka_topic":            "tx.abort",
		"kafka_transactional_id": "tx-2",
		"kafka_timeout_ms":       float64(40),
	})
	if _, err := k.Execute(ctx, &protocol.Action{Name: "b", Type: "kafka_begin_transaction"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, body := range []string{"a0", "a1"} {
		if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(body)}); err != nil {
			t.Fatalf("produce %s: %v", body, err)
		}
	}
	ab, err := k.Execute(ctx, &protocol.Action{Name: "ab", Type: "kafka_abort_transaction"})
	if err != nil || !ab.Success {
		t.Fatalf("abort: %v %s", err, ab.ErrorMessage)
	}
	if ab.ServerTelemetry == nil || ab.ServerTelemetry.AbortCount != 1 {
		t.Fatalf("abort AbortCount = %+v, want 1", ab.ServerTelemetry)
	}
	// Nothing was committed, so a consume finds no records.
	res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if res.Success {
		t.Fatal("aborted records must not be visible")
	}
}

// TestKafkaRequestReplyRejectedInTransaction verifies request_reply (which
// produces+consumes inline) is rejected inside an open transaction, so it can't
// bypass the read_committed buffer.
func TestKafkaRequestReplyRejectedInTransaction(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{
		"kafka_topic":            "tx.rr",
		"kafka_transactional_id": "tx-3",
		"kafka_timeout_ms":       float64(40),
	})
	if _, err := k.Execute(ctx, &protocol.Action{Name: "b", Type: "kafka_begin_transaction"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := k.Execute(ctx, &protocol.Action{
		Name:   "rr",
		Type:   "kafka_request_reply",
		Body:   []byte("{}"),
		Params: map[string]any{"reply_to": "tx.rr.replies"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Success {
		t.Fatal("request_reply must fail inside an open transaction")
	}
	if !strings.Contains(res.ErrorMessage, "transaction") {
		t.Fatalf("error %q should explain request_reply is not allowed in a transaction", res.ErrorMessage)
	}
}
