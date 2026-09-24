//go:build kafka_real

package kafka

import (
	"github.com/twmb/franz-go/pkg/kgo"
	"testing"
)

func TestProducerBatchSettingRetainsProtocolCap(t *testing.T) {
	for _, size := range []int{512, 1 << 20, 2 << 20} {
		c := &franzClient{cfg: config{Brokers: []string{"synthetic.invalid:9092"}, MaxMessageBytes: 1 << 20, Producer: producerTuning{BatchBytes: &size}}}
		client, err := c.newClient("")
		if err != nil {
			t.Fatal(err)
		}
		want := int32(min(size, 1<<20))
		got := client.OptValue(kgo.ProducerBatchMaxBytes)
		client.Close()
		if got != want {
			t.Fatalf("batch %d: got %v, want %d", size, got, want)
		}
	}
}
