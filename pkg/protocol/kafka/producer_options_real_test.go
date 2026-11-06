//go:build kafka_real

package kafka

import (
	"reflect"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func synthesizedFranzClient(t *testing.T, cfg config) *kgo.Client {
	t.Helper()
	cfg.Brokers = []string{"synthetic.invalid:9092"}
	client, err := (&franzClient{cfg: cfg}).newClient("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

// Init defaults compression to "none"; franz must not fall back to its
// snappy-first preference, which a Java compression.type=none producer never uses.
func TestFranzCompressionNoneDisablesSnappyDefault(t *testing.T) {
	for name, want := range map[string]kgo.CompressionCodec{"none": kgo.NoCompression(), "gzip": kgo.GzipCompression()} {
		got := synthesizedFranzClient(t, config{Compression: name}).OptValues(kgo.ProducerBatchCompression)
		if len(got) != 1 || !reflect.DeepEqual(got[0], []kgo.CompressionCodec{want}) {
			t.Fatalf("compression %q: got %#v", name, got)
		}
	}
}

func TestFranzMaxInFlightAppliesOnlyWithoutIdempotence(t *testing.T) {
	five := 5
	inflight := func(cfg config) any {
		return synthesizedFranzClient(t, cfg).OptValue(kgo.MaxProduceRequestsInflightPerBroker)
	}
	if got := inflight(config{}); got != 1 {
		t.Fatalf("unset in-flight changed the franz default: %v", got)
	}
	for _, cfg := range []config{
		{Acks: "all", Idempotence: "disabled", Producer: producerTuning{MaxInFlightRequests: &five}},
		{Acks: "leader", Producer: producerTuning{MaxInFlightRequests: &five}},
		// The validator trims acks, so the adapter must resolve it the same way
		// or franz would reject the override on a still-idempotent producer.
		{Acks: " Leader ", Producer: producerTuning{MaxInFlightRequests: &five}},
	} {
		if got := inflight(cfg); got != 5 {
			t.Fatalf("%+v: in-flight %v, want 5", cfg, got)
		}
	}
	// franz rejects any override on an idempotent producer and pipelines up to
	// five idempotent requests itself.
	if got := inflight(config{Acks: "all", Producer: producerTuning{MaxInFlightRequests: &five}}); got != 1 {
		t.Fatalf("idempotent producer got in-flight override %v", got)
	}
}
