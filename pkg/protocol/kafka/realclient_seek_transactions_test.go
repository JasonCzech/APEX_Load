//go:build kafka_real

package kafka

import (
	"context"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"testing"
)

func TestRealSeekTargetsAndCompleteBrokerResponses(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   int64
		valid  bool
	}{{"earliest", -2, true}, {"latest", -1, true}, {"0", 0, true}, {"42", 42, true}, {"-1", 0, false}, {"1.5", 0, false}, {"9223372036854775808", 0, false}} {
		got, err := parseRealSeekTarget(tc.target)
		if (err == nil) != tc.valid || tc.valid && got != tc.want {
			t.Fatalf("%s %d %v", tc.target, got, err)
		}
	}
	requested := map[int32]kgo.EpochOffset{0: {}, 1: {}}
	shard := func(parts ...kmsg.ListOffsetsResponseTopicPartition) kgo.ResponseShard {
		return kgo.ResponseShard{Resp: &kmsg.ListOffsetsResponse{Topics: []kmsg.ListOffsetsResponseTopic{{Topic: "orders", Partitions: parts}}}}
	}
	p0 := kmsg.ListOffsetsResponseTopicPartition{Partition: 0, Offset: 4, LeaderEpoch: 2}
	p1 := kmsg.ListOffsetsResponseTopicPartition{Partition: 1, Offset: 9, LeaderEpoch: 3}
	out, err := resolveSeekOffsets("orders", requested, []kgo.ResponseShard{shard(p0), shard(p1)})
	if err != nil || out[1].Offset != 9 || out[0].Epoch != 2 {
		t.Fatalf("resolved %+v %v", out, err)
	}
	for _, shards := range [][]kgo.ResponseShard{{shard(p0)}, {shard(p0, p0)}, {shard(kmsg.ListOffsetsResponseTopicPartition{Partition: 0, Offset: 4, ErrorCode: 6}, p1)}, {shard(kmsg.ListOffsetsResponseTopicPartition{Partition: 2, Offset: 4}, p1)}} {
		if _, err := resolveSeekOffsets("orders", requested, shards); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}
func TestConsumerTransactionSessionIsLazyAndStrict(t *testing.T) {
	c := newRealClient(config{ConsumerTransactions: true, ConsumerGroup: "group", TransactionalID: "apex-test", Brokers: []string{"127.0.0.1:1"}, IsolationLevel: "read_committed"}).(*franzClient)
	defer c.Teardown(context.Background())
	if c.cl != nil || c.transact != nil {
		t.Fatal("construction opened runtime")
	}
	if _, _, err := c.Consume(context.Background(), "orders", "group", 0); err == nil {
		t.Fatal("consume without transaction accepted")
	}
	if c.transact == nil {
		t.Fatal("missing native group transaction session")
	}
	if err := c.BeginTransaction(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.BeginTransaction(context.Background()); err == nil {
		t.Fatal("duplicate begin accepted")
	}
	if err := c.Commit(context.Background()); err == nil {
		t.Fatal("nontransactional offset commit accepted")
	}
	if err := c.Seek(context.Background(), "orders", "group", "0"); err == nil {
		t.Fatal("seek in transaction accepted")
	}
	if len((&Kafka{}).RuntimeCapabilities().Features) != 2 {
		t.Fatal("real feature not advertised")
	}
}
