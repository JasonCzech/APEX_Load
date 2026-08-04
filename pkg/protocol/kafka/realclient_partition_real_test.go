//go:build kafka_real

package kafka

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBuildRecordMarksOnlyManualPartitionRouting(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
		params   map[string]any
		want     bool
	}{
		{name: "explicit zero", strategy: "key-hash", params: map[string]any{"partition": 0}, want: true},
		{name: "explicit strategy default", strategy: "explicit", want: true},
		{name: "key hash", strategy: "key-hash", params: map[string]any{"key": "customer-1"}},
		{name: "round robin", strategy: "round-robin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := &Kafka{cfg: config{PartitionStrategy: test.strategy, Partitions: 4}}
			record := instance.buildRecord(&protocol.Action{Params: test.params}, "orders")
			if record.ManualPartition != test.want {
				t.Fatalf("manual partition = %v, want %v", record.ManualPartition, test.want)
			}
		})
	}
}

func TestResolvedProducerRecordPreservesManualPartition(t *testing.T) {
	partitioner := resolvedRecordPartitioner("key-hash").ForTopic("orders")
	for _, partition := range []int32{0, 2} {
		t.Run(fmt.Sprintf("partition_%d", partition), func(t *testing.T) {
			record := resolvedProducerRecord("orders", kafkaRecord{
				Partition:       partition,
				ManualPartition: true,
				Key:             "customer-1",
				Value:           []byte("payload"),
			})
			if record.Partition != partition {
				t.Fatalf("record partition = %d, want %d", record.Partition, partition)
			}
			if got := partitioner.Partition(record, 3); got != int(partition) {
				t.Fatalf("manual partition = %d, want %d", got, partition)
			}
		})
	}
}

func TestResolvedProducerRecordUsesBrokerPartitionCountForKeyHash(t *testing.T) {
	record := resolvedProducerRecord("orders", kafkaRecord{
		Partition: 3, // mock-broker resolution against its default four partitions
		Key:       "customer-1",
		Value:     []byte("payload"),
	})
	if record.Partition != -1 {
		t.Fatalf("non-manual record partition = %d, want unresolved sentinel -1", record.Partition)
	}
	partitioner := resolvedRecordPartitioner("key-hash").ForTopic("orders")
	const brokerPartitions = 3
	hash := fnv.New32a()
	_, _ = hash.Write(record.Key)
	want := int(hash.Sum32() % brokerPartitions)
	if got := partitioner.Partition(record, brokerPartitions); got != want {
		t.Fatalf("key-hash partition = %d, want %d from broker partition count", got, want)
	}
}

func TestResolvedProducerRecordUsesBrokerPartitionCountForRoundRobin(t *testing.T) {
	partitioner := resolvedRecordPartitioner("round-robin").ForTopic("orders")
	var got []int
	for range 4 {
		record := resolvedProducerRecord("orders", kafkaRecord{
			Partition: 3, // must not force the mock-broker hint onto the real topic
			Value:     []byte("payload"),
		})
		got = append(got, partitioner.Partition(record, 3))
	}
	if want := []int{0, 1, 2, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("round-robin partitions = %v, want %v", got, want)
	}
}

func TestResolvedProducerRecordLeavesEmptyKeyForFranzRouting(t *testing.T) {
	record := resolvedProducerRecord("orders", kafkaRecord{
		Partition: 0,
		Value:     []byte("payload"),
	})
	if record.Partition != -1 {
		t.Fatalf("empty-key record partition = %d, want unresolved sentinel -1", record.Partition)
	}
	partition := resolvedRecordPartitioner("key-hash").ForTopic("orders").Partition(record, 3)
	if partition < 0 || partition >= 3 {
		t.Fatalf("empty-key broker partition = %d, want [0,3)", partition)
	}
}
