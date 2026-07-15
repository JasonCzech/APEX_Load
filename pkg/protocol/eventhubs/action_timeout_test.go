package eventhubs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type blockingEventHubsSendClient struct{ eventHubsClient }

func (blockingEventHubsSendClient) Send(ctx context.Context, _ string, _ event) (eventMeta, error) {
	<-ctx.Done()
	return eventMeta{}, ctx.Err()
}

func TestEventHubsActionTimeoutBoundsProduce(t *testing.T) {
	const timeout = 20 * time.Millisecond
	e := &EventHubs{
		name:   eventHubsProtocolName,
		cfg:    config{EventHub: "events", Timeout: timeout},
		client: blockingEventHubsSendClient{eventHubsClient: unavailableClient{}},
	}
	start := time.Now()
	result, err := e.Execute(context.Background(), &protocol.Action{Name: "produce", Type: "eventhubs_produce", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, context.DeadlineExceeded.Error()) {
		t.Fatalf("result = %#v, want deadline failure", result)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("produce took %s, want configured %s action timeout", elapsed, timeout)
	}
}
