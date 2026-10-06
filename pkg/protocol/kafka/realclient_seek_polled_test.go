//go:build kafka_real

package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func newSeekPolledClient() (*franzClient, map[int32]kgo.EpochOffset) {
	client := &franzClient{cfg: config{ConsumerGroup: "workers"}}
	client.onPartitionsAssigned(map[string][]int32{"orders": {0, 1, 2}})
	registered := map[int32]kgo.EpochOffset{0: {Epoch: 1, Offset: 1}, 1: {Epoch: 1, Offset: 1}, 2: {Epoch: 1, Offset: 1}}
	return client, registered
}

func TestRealSeekPollingSurvivesMatchingCommittedOffsets(t *testing.T) {
	client, registered := newSeekPolledClient()
	committed := make([]*kgo.Record, 0, 3)
	for partition := int32(0); partition < 3; partition++ {
		record := kafkaRecord{Topic: "orders", Partition: partition, Offset: 0, LeaderEpoch: 1}
		if err := client.trackConsumedRecord(record); err != nil {
			t.Fatal(err)
		}
		committed = append(committed, &kgo.Record{Topic: record.Topic, Partition: partition, Offset: record.Offset, LeaderEpoch: record.LeaderEpoch})
	}
	// A delivery of offset0 has next1, matching the broker's committed-next1.
	// The SDK's difference-only UncommittedOffsets is empty in this case.
	// Explicit commit bookkeeping also must not erase actual polling evidence.
	client.clearCommittedRecords(committed)
	if len(client.consumedRecordsSnapshot()) != 0 {
		t.Fatal("committed records remained committable")
	}
	positions, generation, err := client.polledSeekPositions("orders", registered)
	if err != nil || len(positions) != 3 || generation != client.assignmentGeneration {
		t.Fatalf("polling was mistaken for commit differences: %+v %d %v", positions, generation, err)
	}
	// A seek clears delivery tracking; consecutive seeks retain the same
	// actually-polled ownership scope without creating commit records.
	client.consumedMu.Lock()
	client.consumedOffsets = nil
	client.consumedMu.Unlock()
	if _, _, err := client.polledSeekPositions("orders", registered); err != nil {
		t.Fatalf("delivery clear invalidated a consecutive seek: %v", err)
	}
	if len(client.consumedRecordsSnapshot()) != 0 {
		t.Fatal("seek polling inventory became commit evidence")
	}
}

func TestRealSeekPollingRequiresOwnershipGenerationAndRegistration(t *testing.T) {
	client, registered := newSeekPolledClient()
	if _, _, err := client.polledSeekPositions("orders", registered); err == nil {
		t.Fatal("joined registrations were mistaken for actual polls")
	}
	if err := client.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: 0}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.polledSeekPositions("orders", registered); err == nil {
		t.Fatal("one poll admitted unpolled owned partitions")
	}
	for _, partition := range []int32{1, 2} {
		if err := client.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: partition}); err != nil {
			t.Fatal(err)
		}
	}
	missing := map[int32]kgo.EpochOffset{0: registered[0], 1: registered[1]}
	if _, _, err := client.polledSeekPositions("orders", missing); err == nil {
		t.Fatal("purged SDK registration remained seekable")
	}
	client.onPartitionsAssigned(map[string][]int32{"orders": {0}})
	if _, _, err := client.polledSeekPositions("orders", registered); err == nil {
		t.Fatal("old-generation polling survived reassignment")
	}
	if err := client.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: 0}); err != nil {
		t.Fatal(err)
	}
	client.onPartitionsRevoked(map[string][]int32{"orders": {2}})
	positions, _, err := client.polledSeekPositions("orders", registered)
	if err != nil || len(positions) != 2 {
		t.Fatalf("current owned subset lost polling: %+v %v", positions, err)
	}
	if _, unowned := positions[2]; unowned {
		t.Fatal("revoked partition included in seek")
	}
	client.onPartitionsRevoked(map[string][]int32{"orders": {0, 1}})
	if _, _, err := client.polledSeekPositions("orders", registered); err == nil {
		t.Fatal("unowned topic remained seekable")
	}
	client.onPartitionsAssigned(map[string][]int32{"orders": {0, 1, 2}})
	if _, _, err := client.polledSeekPositions("orders", registered); err == nil {
		t.Fatal("new assignment reused old polling")
	}
}

func TestRealSeekBufferedPollsDoNotBecomeDeliveredCommitRecords(t *testing.T) {
	client, registered := newSeekPolledClient()
	defer client.Teardown(context.Background())
	for partition := int32(0); partition < 3; partition++ {
		client.pendingMu.Lock()
		err := client.addPendingLocked(kafkaRecord{Topic: "orders", Partition: partition, Offset: 0})
		client.pendingMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := client.polledSeekPositions("orders", registered); err != nil {
		t.Fatalf("actual buffered polls lost their polling scope: %v", err)
	}
	if len(client.consumedRecordsSnapshot()) != 0 {
		t.Fatal("buffered polls became committable deliveries")
	}
	client.onPartitionsRevoked(map[string][]int32{"orders": {0}})
	client.onPartitionsAssigned(map[string][]int32{"orders": {0}})
	if _, _, err := client.polledSeekPositions("orders", registered); err == nil {
		t.Fatal("buffered old-generation polling survived reassignment")
	}
}

func TestRealSeekPollingInventoryBoundsSurviveCommitClears(t *testing.T) {
	for _, mode := range []string{"partitions", "topics"} {
		t.Run(mode, func(t *testing.T) {
			client := &franzClient{cfg: config{ConsumerGroup: "workers"}}
			limit := maxTrackedConsumePartitions
			if mode == "topics" {
				limit = maxTrackedConsumeTopics
			}
			for index := 0; index < limit; index++ {
				topic, partition := "orders", int32(index)
				if mode == "topics" {
					topic, partition = fmt.Sprintf("topic-%d", index), 0
				}
				client.onPartitionsAssigned(map[string][]int32{topic: {partition}})
				if err := client.trackConsumedRecord(kafkaRecord{Topic: topic, Partition: partition}); err != nil {
					t.Fatalf("poll %d: %v", index, err)
				}
				client.clearCommittedRecords([]*kgo.Record{{Topic: topic, Partition: partition}})
			}
			topic, partition := "orders", int32(limit)
			if mode == "topics" {
				topic, partition = "overflow", 0
			}
			client.onPartitionsAssigned(map[string][]int32{topic: {partition}})
			if err := client.trackConsumedRecord(kafkaRecord{Topic: topic, Partition: partition}); err == nil {
				t.Fatal("commit clears bypassed polling inventory bound")
			}
			if len(client.consumedRecordsSnapshot()) != 0 {
				t.Fatal("rejected poll became committable")
			}
			globalKafkaPending.mu.Lock()
			beforeRecords, beforeBytes := globalKafkaPending.records, globalKafkaPending.bytes
			globalKafkaPending.mu.Unlock()
			client.pendingMu.Lock()
			err := client.addPendingLocked(kafkaRecord{Topic: topic, Partition: partition})
			client.pendingMu.Unlock()
			if err == nil || len(client.pending) != 0 {
				t.Fatal("rejected polling admission buffered a record")
			}
			globalKafkaPending.mu.Lock()
			afterRecords, afterBytes := globalKafkaPending.records, globalKafkaPending.bytes
			globalKafkaPending.mu.Unlock()
			if beforeRecords != afterRecords || beforeBytes != afterBytes {
				t.Fatal("rejected polling admission leaked global pending capacity")
			}
		})
	}
}

func TestRealSeekRefusesClosedNativeClient(t *testing.T) {
	client := newRealClient(config{ConsumerGroup: "workers", Brokers: []string{"127.0.0.1:1"}}).(*franzClient)
	handle, err := client.handle()
	if err != nil {
		t.Fatal(err)
	}
	handle.Close()
	if err := client.Seek(context.Background(), "orders", "workers", "0"); !errors.Is(err, kgo.ErrClientClosed) {
		t.Fatalf("closed native client admitted seek: %v", err)
	}
}

func TestRealSeekPollingConcurrentOwnershipChanges(t *testing.T) {
	client, registered := newSeekPolledClient()
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for index := 0; index < 100; index++ {
			client.onPartitionsRevoked(map[string][]int32{"orders": {0, 1, 2}})
			client.onPartitionsAssigned(map[string][]int32{"orders": {0, 1, 2}})
		}
	}()
	go func() {
		defer workers.Done()
		for index := 0; index < 100; index++ {
			for partition := int32(0); partition < 3; partition++ {
				_ = client.trackConsumedRecord(kafkaRecord{Topic: "orders", Partition: partition, Offset: int64(index)})
			}
			positions, _, err := client.polledSeekPositions("orders", registered)
			if err == nil && len(positions) != 3 {
				t.Error("partial polling inventory admitted under concurrent reassignment")
			}
		}
	}()
	workers.Wait()
}
