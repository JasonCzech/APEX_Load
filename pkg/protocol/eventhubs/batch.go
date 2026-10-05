package eventhubs

import (
	"context"
	"fmt"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const MaxEventHubsBatchMessages = 4096

type eventHubsBatchClient interface {
	SendBatch(context.Context, string, []event) (eventMeta, error)
}

// MessageBodies validates a sampled message list. The transport may split it
// into bounded wire batches; it remains one action and one latency sample.
func MessageBodies(value any) ([]string, error) {
	var bodies []string
	switch v := value.(type) {
	case []string:
		bodies = v
	case []any:
		if len(v) > MaxEventHubsBatchMessages {
			return nil, fmt.Errorf("too many Event Hubs messages")
		}
		bodies = make([]string, len(v))
		for i, item := range v {
			var ok bool
			bodies[i], ok = item.(string)
			if !ok {
				return nil, fmt.Errorf("Event Hubs messages must be strings")
			}
		}
	default:
		return nil, fmt.Errorf("Event Hubs messages must be an array")
	}
	if len(bodies) == 0 || len(bodies) > MaxEventHubsBatchMessages {
		return nil, fmt.Errorf("Event Hubs message count must be 1..%d", MaxEventHubsBatchMessages)
	}
	bytes := 0
	for _, body := range bodies {
		if len(body) > MaxEventHubsEventBytes-bytes {
			return nil, fmt.Errorf("Event Hubs message list exceeds size limit")
		}
		bytes += len(body)
	}
	return bodies, nil
}

func (e *EventHubs) produceBatch(ctx context.Context, action *protocol.Action, result *protocol.Result, bodies []string, accepted, attempted *bool) (observed event, operationErr error) {
	var acknowledged event
	defer func() {
		if *accepted {
			observed = acknowledged
		}
	}()
	client, ok := e.client.(eventHubsBatchClient)
	if !ok {
		return event{}, fmt.Errorf("Event Hubs adapter does not support sampled message lists")
	}
	hub := e.hub(action)
	if hub == "" {
		return event{}, fmt.Errorf("event hub name is required")
	}
	base := e.buildEvent(action)
	events := make([]event, len(bodies))
	size := 0
	for i, body := range bodies {
		events[i] = base
		events[i].Body = []byte(body)
		n := eventSize(events[i])
		if n > MaxEventHubsEventBytes-size {
			return event{}, fmt.Errorf("Event Hubs message list and properties exceed size limit")
		}
		size += n
	}
	*attempted = true
	meta, err := client.SendBatch(ctx, hub, events)
	if err != nil {
		return event{}, err
	}
	sent := events[len(events)-1]
	sent.MessageCount = len(events)
	sent.Offset = meta.Offset
	sent.SequenceNum = meta.SequenceNum
	sent.EnqueuedAt = meta.EnqueuedAt
	sent.AcknowledgementMs = meta.AcknowledgementMs
	// A provider's final-event metadata cannot establish the placement of
	// every event in an unpinned sampled list (including split wire batches).
	// Only an explicit homogeneous partition selection qualifies volume skew.
	if base.PartitionID == "" || (meta.PartitionID != "" && meta.PartitionID != base.PartitionID) {
		sent.PartitionID = ""
	}
	acknowledged, *accepted = sent, true
	for _, body := range bodies {
		result.BytesSent += int64(len(body))
	}
	if err = addEventExtractions(result, sent); err != nil {
		return event{}, err
	}
	if err = protocol.StoreExtraction(result, "eventhubs_operation", "produce"); err != nil {
		return event{}, err
	}
	if err = protocol.StoreExtraction(result, "eventhubs_message_count", fmt.Sprint(len(bodies))); err != nil {
		return event{}, err
	}
	return sent, evaluateAssertions(action.Assertions, result, sent)
}

func (c mockClient) SendBatch(ctx context.Context, hub string, events []event) (eventMeta, error) {
	var last eventMeta
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return eventMeta{}, err
		}
		var err error
		last, err = c.Send(ctx, hub, event)
		if err != nil {
			return eventMeta{}, err
		}
	}
	return last, nil
}
