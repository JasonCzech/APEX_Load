//go:build kafka_real

package kafka

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaTargetPolicyDialerBlocksAdvertisedBroker(t *testing.T) {
	client := &franzClient{cfg: config{AllowedTargetHosts: []string{"broker.example.test"}}}
	conn, err := client.targetPolicyDialer(nil)(context.Background(), "tcp", "advertised.attacker.example:9092")
	if conn != nil || err == nil || !strings.Contains(err.Error(), "advertised.attacker.example") {
		t.Fatalf("policy dial = %v, %v; want advertised broker rejection", conn, err)
	}
	if err := protocol.ValidateTargetAddress("broker.example.test:9092", client.cfg.AllowedTargetHosts); err != nil {
		t.Fatalf("configured broker policy rejected: %v", err)
	}
}

func TestPrimeReplyConsumerReturnsForExistingAssignment(t *testing.T) {
	client := &franzClient{replyAssigned: map[string]bool{"replies": true}}
	if err := client.primeReplyConsumer(context.Background(), nil, "replies", time.Second); err != nil {
		t.Fatalf("prime existing assignment: %v", err)
	}
}

func TestConsumeTopicRetainedUntilExplicitCommitBoundary(t *testing.T) {
	handle, err := kgo.NewClient(
		kgo.SeedBrokers("127.0.0.1:1"),
		kgo.ConsumerGroup("explicit-commit-test"),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	client := &franzClient{
		cfg:              config{ConsumerGroup: "explicit-commit-test"},
		consumeTopicRefs: make(map[string]int),
	}
	if err := client.addConsumeTopic(handle, "orders"); err != nil {
		t.Fatal(err)
	}
	client.releaseConsumeTopic(handle, "orders")
	if refs, ok := client.consumeTopicRefs["orders"]; !ok || refs != 0 {
		t.Fatalf("topic state after consume = (%d, %v), want retained with zero active refs", refs, ok)
	}

	client.purgeCommittedConsumeTopics(handle)
	if _, ok := client.consumeTopicRefs["orders"]; ok {
		t.Fatal("topic remained tracked after successful commit boundary cleanup")
	}
}

func TestConsumeTopicTrackingIsBounded(t *testing.T) {
	client := &franzClient{consumeTopicRefs: make(map[string]int, maxTrackedConsumeTopics)}
	for i := 0; i < maxTrackedConsumeTopics; i++ {
		client.consumeTopicRefs[fmt.Sprintf("topic-%d", i)] = 0
	}
	if err := client.addConsumeTopic(nil, "overflow"); err == nil {
		t.Fatal("expected topic tracking limit error")
	}
}

func TestExplicitCommitBookkeepingExcludesUndeliveredPendingTopic(t *testing.T) {
	client := &franzClient{}
	delivered := kafkaRecord{Topic: "orders", Partition: 0, Offset: 12, LeaderEpoch: 3}
	undelivered := kafkaRecord{Topic: "audit", Partition: 1, Offset: 44, LeaderEpoch: 5}
	if err := client.trackConsumedRecord(delivered); err != nil {
		t.Fatal(err)
	}
	client.pendingMu.Lock()
	if err := client.addPendingLocked(undelivered); err != nil {
		client.pendingMu.Unlock()
		t.Fatal(err)
	}
	client.pendingMu.Unlock()
	defer client.Teardown(context.Background())

	records := client.consumedRecordsSnapshot()
	if len(records) != 1 {
		t.Fatalf("commit record count = %d, want only delivered record", len(records))
	}
	if got := records[0]; got.Topic != delivered.Topic || got.Partition != delivered.Partition || got.Offset != delivered.Offset || got.LeaderEpoch != delivered.LeaderEpoch {
		t.Fatalf("commit record = %#v, want delivered record %#v", got, delivered)
	}
	if len(client.pending) != 1 || client.pending[0].record.Topic != undelivered.Topic {
		t.Fatalf("pending records changed during commit bookkeeping: %#v", client.pending)
	}
}

func TestRevocationFencesPendingDeliveryAndExplicitCommit(t *testing.T) {
	client := &franzClient{
		cfg:             config{ConsumerGroup: "shared"},
		ownedPartitions: make(map[string]map[int32]uint64),
	}
	client.onPartitionsAssigned(map[string][]int32{"orders": {0}})
	if err := client.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: 0, Offset: 12}); err != nil {
		t.Fatal(err)
	}
	client.pendingMu.Lock()
	if err := client.addPendingLocked(kafkaRecord{Topic: "orders", Partition: 0, Offset: 13, Value: []byte("pending")}); err != nil {
		client.pendingMu.Unlock()
		t.Fatal(err)
	}
	client.pendingMu.Unlock()

	client.onPartitionsRevoked(map[string][]int32{"orders": {0}})
	if records := client.consumedRecordsSnapshot(); len(records) != 0 {
		t.Fatalf("revoked records remained committable: %#v", records)
	}
	client.pendingMu.Lock()
	pending := len(client.pending)
	client.pendingMu.Unlock()
	if pending != 0 {
		t.Fatalf("revoked pending records = %d, want 0", pending)
	}
	if err := client.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: 0, Offset: 14}); err == nil {
		t.Fatal("record from revoked partition remained deliverable")
	}
}

func TestTwoClientsCannotCommitEachOthersReassignedPartition(t *testing.T) {
	newMember := func() *franzClient {
		return &franzClient{cfg: config{ConsumerGroup: "shared"}, ownedPartitions: make(map[string]map[int32]uint64)}
	}
	memberA, memberB := newMember(), newMember()
	memberA.onPartitionsAssigned(map[string][]int32{"orders": {0}})
	if err := memberA.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: 0, Offset: 12}); err != nil {
		t.Fatal(err)
	}
	memberA.onPartitionsRevoked(map[string][]int32{"orders": {0}})
	memberB.onPartitionsAssigned(map[string][]int32{"orders": {0}})
	if err := memberB.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: 0, Offset: 50}); err != nil {
		t.Fatal(err)
	}
	if stale := memberA.consumedRecordsSnapshot(); len(stale) != 0 {
		t.Fatalf("member A can still commit after reassignment: %#v", stale)
	}
	fresh := memberB.consumedRecordsSnapshot()
	if len(fresh) != 1 || fresh[0].Offset != 50 {
		t.Fatalf("member B commit snapshot = %#v, want offset 50", fresh)
	}
}

func TestPendingAdmissionRejectsWithoutEvictionOrOffsetSkip(t *testing.T) {
	client := &franzClient{}
	client.pendingMu.Lock()
	for i := 0; i < maxPendingReplyRecords; i++ {
		if err := client.addPendingLocked(kafkaRecord{Topic: "orders", Partition: 0, Offset: int64(i), Value: []byte("x")}); err != nil {
			client.pendingMu.Unlock()
			t.Fatalf("fill pending at %d: %v", i, err)
		}
	}
	first := client.pending[0].record.Offset
	err := client.addPendingLocked(kafkaRecord{Topic: "orders", Partition: 0, Offset: 9999, Value: []byte("overflow")})
	count := len(client.pending)
	last := client.pending[len(client.pending)-1].record.Offset
	client.pendingMu.Unlock()
	defer client.Teardown(context.Background())
	if err == nil {
		t.Fatal("pending overflow unexpectedly succeeded")
	}
	if count != maxPendingReplyRecords || first != 0 || last != int64(maxPendingReplyRecords-1) {
		t.Fatalf("pending state changed on rejection: count=%d first=%d last=%d", count, first, last)
	}
	if records := client.consumedRecordsSnapshot(); len(records) != 0 {
		t.Fatalf("undelivered pending records became committable: %#v", records)
	}
}

func TestPendingAdmissionRejectsOversizedRecord(t *testing.T) {
	client := &franzClient{}
	client.pendingMu.Lock()
	err := client.addPendingLocked(kafkaRecord{Topic: "orders", Value: make([]byte, maxPendingReplyBytes+1)})
	count := len(client.pending)
	client.pendingMu.Unlock()
	if err == nil || count != 0 {
		t.Fatalf("oversized pending admission = err %v count %d, want rejection with no mutation", err, count)
	}
}

func TestReplyHandleBuildsValidDedicatedGroupConsumer(t *testing.T) {
	client := &franzClient{cfg: config{Brokers: []string{"127.0.0.1:1"}}}
	handle, err := client.replyHandle()
	if err != nil {
		t.Fatalf("replyHandle rejected its consumer configuration: %v", err)
	}
	if handle == nil {
		t.Fatal("replyHandle returned a nil client")
	}
	handle.Close()
}
