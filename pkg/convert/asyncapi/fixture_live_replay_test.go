//go:build kafka_real && kafka_integration

package asyncapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Uses a dedicated topic per run: an old or unrelated message cannot satisfy
// this assertion. The live adapter is exercised through the imported DSL.
func TestFixtureAsyncAPIKafkaLiveReplayExactPayload(t *testing.T) {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("KAFKA_BROKERS is required for live fixture replay")
	}
	data, err := os.ReadFile("testdata/order-event.yaml")
	if err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("fixture-%d", time.Now().UnixNano())
	topic := "apexload." + marker
	client, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(brokers, ",")...))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	create := kmsg.NewPtrCreateTopicsRequest()
	create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
	created, err := create.RequestWith(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Topics) != 1 || created.Topics[0].ErrorCode != 0 {
		t.Fatalf("unable to provision fixture topic: %+v", created.Topics)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		remove := kmsg.NewPtrDeleteTopicsRequest()
		remove.TopicNames = []string{topic}
		remove.Topics = []kmsg.DeleteTopicsRequestTopic{{Topic: &topic}}
		deleted, err := remove.RequestWith(cleanupCtx, client)
		if err != nil {
			t.Errorf("remove fixture topic: %v", err)
		} else if len(deleted.Topics) != 1 || deleted.Topics[0].ErrorCode != 0 {
			t.Errorf("fixture topic cleanup failed: %+v", deleted.Topics)
		}
	}()
	content := strings.NewReplacer("FIXTURE_BROKER", strings.Split(brokers, ",")[0], "FIXTURE_TOPIC", topic, "FIXTURE_MARKER", marker).Replace(string(data))
	result, err := Convert(ImportRequest{Content: content, Options: ConvertOptions{IncludeAssertions: boolPtr(true)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Script.Actions) != 2 || result.Script.Actions[0].Type != "kafka_produce" || result.Script.Actions[1].Type != "kafka_consume" {
		t.Fatal("import did not preserve the publish/consume workflow")
	}
	result.Script.Config.Kafka.Mock = false
	result.Script.Config.Kafka.Brokers = strings.Split(brokers, ",")
	result.Script.Config.Kafka.ConsumerGroup = marker
	report, err := engine.ReplayScript(ctx, result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || len(report.Events) != 2 {
		t.Fatalf("live replay failed: %s", report.Error)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(report.Events[1].Extractions["kafka_body"]), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"orderId": marker, "amount": 19.95, "currency": "EUR", "items": []any{"alpha", "beta"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consumed payload = %#v, want %#v", got, want)
	}
	if report.Events[1].Extractions["kafka_offset"] == "" {
		t.Fatal("missing real broker offset evidence")
	}
	assertNoAsyncAPIHeaders(t, report)
}
