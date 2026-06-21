package eventhubs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestClassifyProduceError(t *testing.T) {
	cases := []struct {
		name                                  string
		err                                   error
		throttle, serverBusy, quota, tooLarge int
	}{
		{"nil", nil, 0, 0, 0, 0},
		{"too large", errors.New("event exceeds the Event Hubs batch size limit: too large for the batch"), 0, 0, 0, 1},
		{"server busy", errors.New("amqp:resource-limit-exceeded: ServerBusy"), 1, 1, 0, 0},
		{"quota", errors.New("quota exceeded for namespace"), 1, 0, 1, 0},
		{"throttled 429", errors.New("request was throttled (429)"), 1, 0, 0, 0},
		{"unrelated", errors.New("no event received within 5s"), 0, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			throttle, serverBusy, quota, tooLarge := classifyProduceError(c.err)
			if throttle != c.throttle || serverBusy != c.serverBusy || quota != c.quota || tooLarge != c.tooLarge {
				t.Fatalf("classifyProduceError = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					throttle, serverBusy, quota, tooLarge, c.throttle, c.serverBusy, c.quota, c.tooLarge)
			}
		})
	}
}

func TestFinalizeTelemetryProduceSetsPartitionID(t *testing.T) {
	result := &protocol.Result{StartTime: time.Now(), Duration: time.Millisecond, Extractions: map[string]string{}}
	sent := event{Body: []byte("payload"), PartitionID: "3"}
	tel := finalizeTelemetry(result, sent, true, event{}, false, nil)
	if tel == nil {
		t.Fatal("telemetry nil for a produce")
	}
	if tel.PartitionID != "3" {
		t.Fatalf("PartitionID = %q, want 3", tel.PartitionID)
	}
	if tel.MessagesSent != 1 {
		t.Fatalf("MessagesSent = %d, want 1", tel.MessagesSent)
	}
}

func TestFinalizeTelemetryConsumeSetsEndToEndLatency(t *testing.T) {
	start := time.Now()
	enqueued := start.Add(-30 * time.Millisecond)
	result := &protocol.Result{StartTime: start, Duration: 5 * time.Millisecond, Extractions: map[string]string{}}
	received := event{Body: []byte("payload"), EnqueuedAt: enqueued}
	tel := finalizeTelemetry(result, event{}, false, received, true, nil)
	if tel == nil {
		t.Fatal("telemetry nil for a consume")
	}
	if tel.EndToEndLatencyMs <= 0 {
		t.Fatalf("EndToEndLatencyMs = %v, want > 0", tel.EndToEndLatencyMs)
	}
	if tel.EndToEndLatencyMs != tel.ConsumerLagMs {
		t.Fatalf("EndToEndLatencyMs (%v) should equal ConsumerLagMs (%v) for the probe", tel.EndToEndLatencyMs, tel.ConsumerLagMs)
	}
}

func TestFinalizeTelemetryEmitsOnThrottleEvenWithoutSample(t *testing.T) {
	result := &protocol.Result{StartTime: time.Now(), Duration: time.Millisecond, Extractions: map[string]string{}}
	tel := finalizeTelemetry(result, event{}, false, event{}, false, errors.New("amqp:resource-limit-exceeded"))
	if tel == nil {
		t.Fatal("telemetry should be emitted for a throttled produce even with no sent/received")
	}
	if tel.ServerBusyCount != 1 || tel.ThrottledRequests != 1 {
		t.Fatalf("throttle counters = busy:%d throttle:%d, want 1/1", tel.ServerBusyCount, tel.ThrottledRequests)
	}
}

func TestExecuteProducePopulatesPartitionTelemetry(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "stream"})
	produced, err := e.Execute(ctx, &protocol.Action{
		Name:   "p",
		Type:   "eventhubs_produce",
		Body:   []byte("x"),
		Params: map[string]any{"partition_id": "2"},
	})
	if err != nil {
		t.Fatalf("produce error: %v", err)
	}
	if produced.ServerTelemetry == nil || produced.ServerTelemetry.PartitionID != "2" {
		t.Fatalf("produce telemetry PartitionID not set to 2: %+v", produced.ServerTelemetry)
	}
}
