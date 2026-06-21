// Package eventhubs provides a native Azure Event Hubs protocol, distinct from
// the Kafka-endpoint profile in pkg/protocol/kafka. It is producer-first:
// eventhubs_produce is the primary action (stress the ingestion path) and
// eventhubs_consume is a lightweight read-only observer probe. The real
// azeventhubs-backed adapter is opt-in behind the eventhubs_real build tag; the
// default build ships an in-memory mock plus a fail-closed stub.
package eventhubs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	eventHubsProtocolName = "eventhubs"
	defaultTimeout        = 5 * time.Second
)

func init() {
	protocol.Register(eventHubsProtocolName, func() protocol.Protocol {
		return &EventHubs{name: eventHubsProtocolName}
	})
}

type config struct {
	Namespace        string
	EventHub         string
	ConnectionString string
	ConsumerGroup    string
	PartitionKey     string
	PartitionID      string
	AuthType         string // connection_string | aad
	Timeout          time.Duration
	Mock             bool
}

// EventHubs is a per-VUser Azure Event Hubs client.
type EventHubs struct {
	name        string
	cfg         config
	client      eventHubsClient
	initialized bool
}

func (e *EventHubs) Name() string { return e.name }

func (e *EventHubs) Init(_ context.Context, cfg map[string]any) error {
	e.cfg = config{
		Namespace:        stringValue(cfg, "eventhubs_namespace"),
		EventHub:         stringValue(cfg, "eventhubs_event_hub"),
		ConnectionString: stringValue(cfg, "eventhubs_connection_string"),
		ConsumerGroup:    stringValue(cfg, "eventhubs_consumer_group"),
		PartitionKey:     stringValue(cfg, "eventhubs_partition_key"),
		PartitionID:      stringValue(cfg, "eventhubs_partition_id"),
		AuthType:         strings.ToLower(strings.TrimSpace(stringValue(cfg, "eventhubs_auth_type"))),
		Timeout:          durationValue(cfg, "eventhubs_timeout_ms", defaultTimeout),
		Mock:             boolValue(cfg, "eventhubs_mock"),
	}
	if e.cfg.ConnectionString == "" {
		e.cfg.ConnectionString = stringValue(cfg, "eventhubs_connection_string_secret")
	}
	if e.cfg.ConsumerGroup == "" {
		e.cfg.ConsumerGroup = "$Default" // the Event Hubs default consumer group
	}
	if e.cfg.Timeout <= 0 {
		e.cfg.Timeout = defaultTimeout
	}
	if e.cfg.Mock {
		e.client = mockClient{broker: sharedBroker}
	} else {
		e.client = newRealClient(e.cfg)
	}
	e.initialized = true
	return nil
}

func (e *EventHubs) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	if e.client == nil {
		// Execute should never run before Init in the normal engine flow, but if
		// it does we fail closed rather than silently substituting a working mock
		// (which would dishonestly no-op-succeed a non-mock script).
		e.client = unavailableClient{namespace: e.cfg.Namespace}
	}
	hub := e.hub(action)
	result := &protocol.Result{
		ActionName:       action.Name,
		StartTime:        start,
		StatusCode:       200,
		Success:          true,
		Extractions:      make(map[string]string),
		DependencyType:   eventHubsProtocolName,
		DependencyTarget: hub,
	}

	var err error
	var sent, received event
	var hasSent, hasReceived bool
	switch strings.ToLower(action.Type) {
	case "eventhubs_produce":
		sent, err = e.produce(ctx, action, result)
		hasSent = err == nil
	case "eventhubs_consume":
		received, err = e.consume(ctx, action, result)
		hasReceived = err == nil
	default:
		err = fmt.Errorf("unsupported eventhubs action type %q", action.Type)
	}

	result.Duration = time.Since(start)
	result.ServerTelemetry = finalizeTelemetry(result, sent, hasSent, received, hasReceived, err)
	if err != nil {
		result.Success = false
		if result.StatusCode == 0 || result.StatusCode == 200 {
			result.StatusCode = 500
		}
		result.ErrorMessage = err.Error()
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	return result, nil
}

func (e *EventHubs) Teardown(ctx context.Context) error {
	if e.client == nil {
		return nil
	}
	return e.client.Teardown(ctx)
}

func (e *EventHubs) HealthCheck(ctx context.Context) error {
	if !e.initialized || e.client == nil {
		return nil
	}
	return e.client.HealthCheck(ctx)
}

func (e *EventHubs) produce(ctx context.Context, action *protocol.Action, result *protocol.Result) (event, error) {
	hub := e.hub(action)
	if hub == "" {
		return event{}, fmt.Errorf("event hub name is required")
	}
	ev := e.buildEvent(action)
	meta, err := e.client.Send(ctx, hub, ev)
	if err != nil {
		return event{}, err
	}
	ev.Offset, ev.SequenceNum, ev.EnqueuedAt = meta.Offset, meta.SequenceNum, meta.EnqueuedAt
	if meta.PartitionID != "" {
		ev.PartitionID = meta.PartitionID
	}
	result.BytesSent = int64(len(ev.Body))
	addEventExtractions(result, ev)
	result.Extractions["eventhubs_operation"] = "produce"
	return ev, evaluateAssertions(action.Assertions, result, ev)
}

func (e *EventHubs) consume(ctx context.Context, action *protocol.Action, result *protocol.Result) (event, error) {
	hub := e.hub(action)
	if hub == "" {
		return event{}, fmt.Errorf("event hub name is required")
	}
	group := paramString(action.Params, "consumer_group", e.cfg.ConsumerGroup)
	ev, lag, err := e.client.Receive(ctx, hub, group, e.timeout(action))
	if err != nil {
		result.StatusCode = 204
		return event{}, err
	}
	result.BytesReceived = int64(len(ev.Body))
	addEventExtractions(result, ev)
	result.Extractions["eventhubs_operation"] = "consume"
	result.Extractions["eventhubs_consumer_lag"] = strconv.FormatInt(lag, 10)
	if err := runExtractors(action.Extractors, result, ev); err != nil {
		return event{}, err
	}
	return ev, evaluateAssertions(action.Assertions, result, ev)
}

func (e *EventHubs) hub(action *protocol.Action) string {
	return protocol.FirstNonEmpty(action.Target, paramString(action.Params, "event_hub", ""), e.cfg.EventHub)
}

func (e *EventHubs) timeout(action *protocol.Action) time.Duration {
	if ms := paramString(action.Params, "timeout_ms", ""); ms != "" {
		if n, err := strconv.Atoi(ms); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	if e.cfg.Timeout > 0 {
		return e.cfg.Timeout
	}
	return defaultTimeout
}

func (e *EventHubs) buildEvent(action *protocol.Action) event {
	props := map[string]string{}
	for k, v := range action.Headers {
		props[k] = v
	}
	return event{
		Body:         action.Body,
		PartitionKey: paramString(action.Params, "partition_key", e.cfg.PartitionKey),
		PartitionID:  paramString(action.Params, "partition_id", e.cfg.PartitionID),
		Properties:   props,
	}
}
