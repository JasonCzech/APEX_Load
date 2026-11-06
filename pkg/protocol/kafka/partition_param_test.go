package kafka

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// producedPartitions records the partition of every record handed to the
// client so a test can prove a rejected sample sent nothing.
type producedPartitions struct {
	kafkaClient
	partitions []int32
}

func (p *producedPartitions) Produce(ctx context.Context, topic string, rec kafkaRecord) (recordMeta, error) {
	p.partitions = append(p.partitions, rec.Partition)
	return p.kafkaClient.Produce(ctx, topic, rec)
}

// JMeter-imported scripts template the partition ("{{param.p_PartitionID}}"),
// and the engine interpolates params to strings. The explicit-strategy
// default of 0 must not swallow the authored partition.
func TestKafkaStringPartitionParamRoutesToThatPartition(t *testing.T) {
	for _, strategy := range []string{"explicit", "key-hash", "round-robin"} {
		k := newMockKafka(t, map[string]any{"kafka_topic": "synthetic-orders", "kafka_partition_strategy": strategy})
		recorder := &producedPartitions{kafkaClient: k.client}
		k.client = recorder
		for _, raw := range []string{"3", " 2 ", "0"} {
			res, err := k.Execute(context.Background(), &protocol.Action{
				Name: "produce", Type: "kafka_produce", Body: []byte(`{"n":1}`),
				Params: map[string]any{"key": "synthetic-key", "partition": raw},
			})
			if err != nil || !res.Success {
				t.Fatalf("%s partition %q: err=%v msg=%s", strategy, raw, err, res.ErrorMessage)
			}
			want := strings.TrimSpace(raw)
			if got := res.Extractions["kafka_partition"]; got != want {
				t.Fatalf("%s partition %q routed to %q", strategy, raw, got)
			}
		}
		if len(recorder.partitions) != 3 || recorder.partitions[0] != 3 || recorder.partitions[1] != 2 || recorder.partitions[2] != 0 {
			t.Fatalf("%s produced partitions %v, want [3 2 0]", strategy, recorder.partitions)
		}
		record := k.buildRecord(&protocol.Action{Params: map[string]any{"partition": "3"}}, "synthetic-orders")
		if !record.ManualPartition || record.Partition != 3 {
			t.Fatalf("%s record = partition %d manual %v", strategy, record.Partition, record.ManualPartition)
		}
	}
}

// An unresolved template, an empty string, a fraction or an out-of-range value
// must fail the sample instead of producing to a default partition.
func TestKafkaInvalidPartitionParamFailsWithoutProducing(t *testing.T) {
	for _, raw := range []any{"{{param.p_PartitionID}}", "", "abc", "1.5", float64(1.5), "-1", float64(-1), "2147483648"} {
		k := newMockKafka(t, map[string]any{"kafka_topic": "synthetic-orders", "kafka_partition_strategy": "explicit"})
		recorder := &producedPartitions{kafkaClient: k.client}
		k.client = recorder
		res, err := k.Execute(context.Background(), &protocol.Action{
			Name: "produce", Type: "kafka_produce", Body: []byte(`{"n":1}`),
			Params: map[string]any{"partition": raw},
		})
		if err != nil {
			t.Fatalf("partition %#v: unexpected execute error %v", raw, err)
		}
		if res.Success || !strings.Contains(res.ErrorMessage, "kafka partition must be an integer between 0 and 2147483647") {
			t.Fatalf("partition %#v: success=%v msg=%q", raw, res.Success, res.ErrorMessage)
		}
		if len(recorder.partitions) != 0 {
			t.Fatalf("partition %#v: produced %v despite the invalid partition", raw, recorder.partitions)
		}
	}
}
