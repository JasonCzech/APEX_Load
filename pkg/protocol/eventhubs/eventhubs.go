// Package eventhubs provides a native Azure Event Hubs protocol, distinct from
// the Kafka-endpoint profile in pkg/protocol/kafka. It is producer-first:
// eventhubs_produce is the primary action (stress the ingestion path) and
// eventhubs_consume is a lightweight read-only observer probe. The real
// azeventhubs-backed adapter is opt-in behind the eventhubs_real build tag; the
// default build ships an in-memory mock plus a fail-closed stub.
package eventhubs

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
)

const (
	eventHubsProtocolName = "eventhubs"
	defaultTimeout        = 5 * time.Second
)

// errNoEvent signals that the read-only consume observer probe found no event
// within its timeout — an expected empty tail in this producer-first protocol
// (the probe can outrun the producer), NOT a transport/auth failure. Adapters
// wrap it so consume() can keep an empty probe out of the error/SLA accounting.
var errNoEvent = errors.New("eventhubs: no event within timeout")

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
	StartPosition    string // observer-probe start: "latest" (default) | "earliest"
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
	// observer detects duplicate / out-of-order events by sequence number on the
	// consume probe. Event Hubs has no ack/rollback model, so the session is used
	// only for its bounded sequence observer (lazily created on first consume).
	observer *messaging.Session[struct{}]
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
		StartPosition:    strings.ToLower(strings.TrimSpace(stringValue(cfg, "eventhubs_start_position"))),
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
	// An empty observer probe (no event within the timeout) is an expected
	// no-content outcome for this read-only, producer-first probe — consume() has
	// already set StatusCode 204 and the empty-probe extraction. Clear the error
	// so it is not scored as a failed transaction (which would inflate the error
	// rate and can trip error-rate SLA gates on otherwise-healthy runs). hasReceived
	// was computed before this and is correctly false (nothing was received).
	if errors.Is(err, errNoEvent) {
		err = nil
	}
	result.ServerTelemetry = finalizeTelemetry(result, sent, hasSent, received, hasReceived, err)
	if err != nil {
		result.Success = false
		if result.StatusCode == 0 || result.StatusCode == 200 {
			result.StatusCode = 500
		}
		result.ErrorMessage = err.Error()
		if strings.EqualFold(action.Type, "eventhubs_produce") {
			// Count a failed produce attempt so the messaging reliability telemetry
			// reflects send failures instead of always reading zero.
			if result.ServerTelemetry == nil {
				result.ServerTelemetry = &protocol.ServerTelemetry{}
			}
			result.ServerTelemetry.SendFailureCount++
		}
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
		if errors.Is(err, errNoEvent) {
			// Expected empty tail: report no-content success, not a failure.
			result.StatusCode = 204
			result.Extractions["eventhubs_operation"] = "consume"
			result.Extractions["eventhubs_empty_probe"] = "1"
			result.Extractions["eventhubs_consumer_lag"] = strconv.FormatInt(lag, 10)
			return event{}, errNoEvent
		}
		// A genuine transport/auth error from the adapter is a real failure.
		result.StatusCode = 502
		return event{}, err
	}
	result.BytesReceived = int64(len(ev.Body))
	addEventExtractions(result, ev)
	result.Extractions["eventhubs_operation"] = "consume"
	result.Extractions["eventhubs_consumer_lag"] = strconv.FormatInt(lag, 10)
	// Reliability detection by sequence number. Event Hubs sequence numbers are
	// per-(hub,partition), so observe within that stream's own space — otherwise
	// consuming from a second hub (whose numbers restart) or a future multi-
	// partition consume would interleave independent streams and read as spurious
	// duplicates/out-of-order. The probe has no rollback/redelivery model, so
	// every observation is a fresh delivery.
	if e.observer == nil {
		e.observer = messaging.New[struct{}](messaging.AckAuto)
	}
	partitionID := ev.PartitionID
	if partitionID == "" {
		partitionID = e.cfg.PartitionID
	}
	if partitionID == "" {
		partitionID = "0"
	}
	streamKey := hub + "\x00" + group + "\x00" + partitionID
	if dup, ooo := e.observer.ObserveKeyed(streamKey, ev.SequenceNum); dup || ooo {
		if dup {
			result.Extractions["eventhubs_duplicate"] = "1"
		}
		if ooo {
			result.Extractions["eventhubs_out_of_order"] = "1"
		}
	}
	if err := runExtractors(action.Extractors, result, ev); err != nil {
		return event{}, err
	}
	return ev, evaluateAssertions(action.Assertions, result, ev)
}

func (e *EventHubs) hub(action *protocol.Action) string {
	return protocol.FirstNonEmpty(action.Target, paramString(action.Params, "event_hub", ""), e.cfg.EventHub)
}

func (e *EventHubs) timeout(action *protocol.Action) time.Duration {
	// timeout_ms is a catalog int and arrives as a JSON number (float64), so a
	// string read never matches it. durationValue handles the numeric form; the
	// string path stays as a fallback for hand-authored string values.
	if d := durationValue(action.Params, "timeout_ms", 0); d > 0 {
		return d
	}
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
	// partition_key and partition_id are mutually exclusive — the real adapter
	// rejects an event that carries both. Use the action's explicit routing
	// control; only fall back to config defaults when the action sets neither,
	// preferring the key to keep the choice deterministic.
	pKey := paramString(action.Params, "partition_key", "")
	pID := paramString(action.Params, "partition_id", "")
	if pKey == "" && pID == "" {
		pKey, pID = e.cfg.PartitionKey, e.cfg.PartitionID
		if pKey != "" {
			pID = ""
		}
	}
	// Decode a base64-encoded body (e.g. binary capture payloads) back to raw
	// bytes so non-UTF-8 events round-trip losslessly.
	body := action.Body
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") && len(action.Body) > 0 {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(action.Body))); err == nil {
			body = decoded
		}
	}
	return event{
		Body:         body,
		PartitionKey: pKey,
		PartitionID:  pID,
		Properties:   props,
	}
}
