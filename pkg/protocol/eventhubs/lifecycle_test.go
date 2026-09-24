package eventhubs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type tracedSampleClient struct {
	eventHubsClient
	trace            *[]string
	sendError        bool
	closeError       bool
	panicSend        bool
	panicClose       bool
	cleanupContextOK *bool
}

func (c *tracedSampleClient) Send(ctx context.Context, hub string, ev event) (eventMeta, error) {
	*c.trace = append(*c.trace, "send")
	if c.panicSend {
		panic("send panic")
	}
	if c.sendError {
		return eventMeta{}, errors.New("send failed")
	}
	return eventMeta{}, nil
}
func (c *tracedSampleClient) Teardown(ctx context.Context) error {
	*c.trace = append(*c.trace, "close")
	if c.panicClose {
		panic("sensitive cleanup panic")
	}
	*c.cleanupContextOK = ctx.Err() == nil
	time.Sleep(10 * time.Millisecond)
	if c.closeError {
		return errors.New("sensitive cleanup detail")
	}
	return nil
}
func TestPerSampleProducerLifecycleAndTiming(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "hub", "eventhubs_producer_lifecycle": "per_sample", "eventhubs_sample_timing": "pnop_create_send_excludes_close"})
			original := e.client
			trace := []string{}
			cleanupOK := false
			e.clientFactory = func() eventHubsClient {
				trace = append(trace, "create")
				return &tracedSampleClient{trace: &trace, sendError: fail, cleanupContextOK: &cleanupOK}
			}
			for range 2 {
				result, err := e.Execute(context.Background(), &protocol.Action{Type: "eventhubs_produce", Name: "send", Body: []byte("payload")})
				if err != nil {
					t.Fatal(err)
				}
				if result.Success == fail {
					t.Fatal("unexpected success", result)
				}
				if result.WorkerDuration-result.Duration < 9*time.Millisecond {
					t.Fatal("cleanup included in sampled duration", result.Duration, result.WorkerDuration)
				}
				if fail && result.Duration != 0 {
					t.Fatal("PNOP failure did not preserve missing sampleEnd")
				}
				if !cleanupOK || e.client != original {
					t.Fatal("cleanup did not restore VUser client")
				}
				if result.Extractions["eventhubs_worker_occupancy_ms"] == "" {
					t.Fatal("missing occupancy evidence")
				}
			}
			if !reflect.DeepEqual(trace, []string{"create", "send", "close", "create", "send", "close"}) {
				t.Fatal(trace)
			}
		})
	}
}
func TestPerSampleProducerPanicCloses(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "hub", "eventhubs_producer_lifecycle": "per_sample"})
	trace := []string{}
	cleanupOK := false
	e.clientFactory = func() eventHubsClient {
		return &tracedSampleClient{trace: &trace, panicSend: true, cleanupContextOK: &cleanupOK}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("missing panic")
			}
		}()
		e.Execute(context.Background(), &protocol.Action{Type: "eventhubs_produce"})
	}()
	if !cleanupOK || !reflect.DeepEqual(trace, []string{"send", "close"}) {
		t.Fatal(trace)
	}
}
func TestSampledMessageListPreservesEmptyAndOrderedBodies(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "hub", "eventhubs_start_position": "earliest"})
	result, err := e.Execute(context.Background(), &protocol.Action{Type: "eventhubs_produce", Params: map[string]any{"messages": []any{"first", "", "last"}}})
	if err != nil || !result.Success || result.BytesSent != 9 || result.Extractions["eventhubs_message_count"] != "3" {
		t.Fatal(result, err)
	}
	for _, want := range []string{"first", "", "last"} {
		ev, _, err := e.client.Receive(context.Background(), "hub", "group", time.Second)
		if err != nil || string(ev.Body) != want {
			t.Fatal(ev, err)
		}
	}
}
func TestMessageListBounds(t *testing.T) {
	for _, value := range []any{nil, []string{}, []any{1}, "payload", make([]string, MaxEventHubsBatchMessages+1)} {
		if _, err := MessageBodies(value); err == nil {
			t.Fatalf("accepted invalid list %T", value)
		}
	}
	if _, err := MessageBodies([]string{""}); err != nil {
		t.Fatal(err)
	}
}

func TestPerSampleCleanupFailureRefreshesEvidence(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "hub", "eventhubs_producer_lifecycle": "per_sample"})
	trace := []string{}
	cleanup := false
	e.clientFactory = func() eventHubsClient {
		return &tracedSampleClient{trace: &trace, cleanupContextOK: &cleanup, closeError: true}
	}
	result, err := e.Execute(context.Background(), &protocol.Action{Type: "eventhubs_produce", Body: []byte("message")})
	if err != nil || result.Success || result.ErrorMessage != "Event Hubs producer cleanup failed" {
		t.Fatal(result, err)
	}
	expected := protocol.BuildProtocolEvidence(&protocol.Action{Type: "eventhubs_produce", Body: []byte("message")}, result)
	if len(expected) == 0 || !reflect.DeepEqual(result.ProtocolEvidence, expected) {
		t.Fatal("stale cleanup evidence")
	}
}

func TestPerSampleRestoresClientAfterCleanupPanic(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "hub", "eventhubs_producer_lifecycle": "per_sample"})
	original := e.client
	trace := []string{}
	e.clientFactory = func() eventHubsClient { return &tracedSampleClient{trace: &trace, panicClose: true} }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected cleanup panic")
			}
		}()
		_, _ = e.Execute(context.Background(), &protocol.Action{Type: "eventhubs_produce"})
	}()
	if e.client != original {
		t.Fatal("cleanup panic left disposed sample client installed")
	}
	e.clientFactory = func() eventHubsClient { return nil }
	if _, err := e.Execute(context.Background(), &protocol.Action{Type: "eventhubs_produce"}); err == nil {
		t.Fatal("nil client accepted")
	}
	if e.client != original {
		t.Fatal("nil factory replaced original client")
	}
}
