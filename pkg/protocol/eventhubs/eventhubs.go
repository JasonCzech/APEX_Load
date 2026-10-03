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
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/identitypolicy"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
)

const (
	eventHubsProtocolName      = "eventhubs"
	defaultTimeout             = 5 * time.Second
	MaxConfiguredTimeout       = time.Hour
	MaxEventHubNameBytes       = 256
	MaxEventHubIdentifierBytes = 4 << 10
	MaxEventHubsEventBytes     = 64 << 20
	maxEventHubsProperties     = 256
	maxEventHubsPropertyBytes  = 1 << 20
)

// errNoEvent signals that the read-only consume observer probe found no event
// within its timeout — an expected empty tail in this producer-first protocol
// (the probe can outrun the producer), NOT a transport/auth failure. Adapters
// wrap it so consume() can keep an empty probe out of the error/SLA accounting.
var (
	errNoEvent                = errors.New("eventhubs: no event within timeout")
	errEventHubsActionTimeout = errors.New("eventhubs: configured action timeout")
)

func init() {
	protocol.Register(eventHubsProtocolName, func() protocol.Protocol {
		return &EventHubs{name: eventHubsProtocolName}
	})
	protocol.RegisterRuntimeNamespaceManager(eventHubsProtocolName, sharedBroker.acquire, sharedBroker.release)
}

type config struct {
	ObservePartitionTail      bool
	TailPollInterval          time.Duration
	progress                  *processorProgress
	OwnerLevel                *int64
	ConsumerMode              string
	ProcessorMaxPartitions    int64
	processorReservation      *ProcessorReservation
	processorReservationBound bool
	CheckpointContainerURL    string
	Lifetime                  context.Context
	ProducerLifecycle         string
	SampleTiming              string
	Namespace                 string
	EventHub                  string
	ConnectionString          string
	ConsumerGroup             string
	PartitionKey              string
	PartitionID               string
	StartPosition             string // observer-probe start: "latest" (default) | "earliest"
	AuthType                  string // connection_string | aad
	Timeout                   time.Duration
	Mock                      bool
	AllowedTargetHosts        []string
}

// EventHubs is a per-VUser Azure Event Hubs client.
type EventHubs struct {
	name             string
	progressSnapshot atomic.Pointer[processorProgress]
	cfg              config
	client           eventHubsClient
	initialized      bool
	clientFactory    func() eventHubsClient
	// observer detects duplicate / out-of-order events by sequence number on the
	// consume probe. Event Hubs has no ack/rollback model, so the session is used
	// only for its bounded sequence observer (lazily created on first consume).
	observer *messaging.Session[struct{}]
}

func (e *EventHubs) Name() string { return e.name }

func (e *EventHubs) MessagingProgressSnapshot() *protocol.MessagingProgressSnapshot {
	return e.progressSnapshot.Load().snapshot()
}

func (e *EventHubs) Init(ctx context.Context, cfg map[string]any) error {
	ready := false
	// Iteration reset first releases the old client. Keep its immutable local
	// evidence visible while constructing a replacement, so an ordinary tick
	// cannot turn an intermediate nil into sticky missing-source coverage.
	defer func() {
		if !ready {
			e.progressSnapshot.Store(nil)
		}
	}()

	timeoutMS, err := boundedInteger(cfg["eventhubs_timeout_ms"], int(defaultTimeout/time.Millisecond), 0, int(MaxConfiguredTimeout/time.Millisecond))
	if err != nil {
		return fmt.Errorf("eventhubs timeout_ms: %w", err)
	}
	if value, present := cfg["eventhubs_consumer_mode"]; present && value != nil {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("eventhubs consumer_mode must be a string")
		}
	}
	partitionCeiling := MaxProcessorPartitions
	if value, present := cfg["eventhubs_processor_max_partitions"]; present {
		if value == nil {
			return fmt.Errorf("eventhubs processor_max_partitions must be an integer")
		}
		partitionCeiling, err = boundedInteger(value, MaxProcessorPartitions, 0, MaxProcessorPartitions)
		if err != nil {
			return fmt.Errorf("eventhubs processor_max_partitions: %w", err)
		}
		if partitionCeiling != 0 && stringValue(cfg, "eventhubs_consumer_mode") != "processor" {
			return fmt.Errorf("processor_max_partitions requires processor mode")
		}
		if partitionCeiling == 0 {
			partitionCeiling = MaxProcessorPartitions
		}
	}
	tailEnabled := false
	if value, present := cfg["eventhubs_observe_partition_tail"]; present {
		var ok bool
		tailEnabled, ok = value.(bool)
		if !ok {
			return fmt.Errorf("eventhubs observe_partition_tail must be a boolean")
		}
		if stringValue(cfg, "eventhubs_consumer_mode") != "processor" {
			return fmt.Errorf("observe_partition_tail requires processor mode")
		}
	}
	tailInterval := 10000
	if value, present := cfg["eventhubs_tail_poll_interval_ms"]; present {
		if value == nil {
			return fmt.Errorf("eventhubs tail_poll_interval_ms must be an integer")
		}
		if stringValue(cfg, "eventhubs_consumer_mode") != "processor" {
			return fmt.Errorf("tail_poll_interval_ms requires processor mode")
		}
		tailInterval, err = boundedInteger(value, 10000, 0, 300000)
		if err != nil || tailInterval > 0 && tailInterval < 10000 {
			return fmt.Errorf("eventhubs tail_poll_interval_ms must be zero or between 10000 and 300000")
		}
		if tailInterval == 0 {
			tailInterval = 10000
		}
	}
	ownerLevel, err := parseOwnerLevel(cfg["eventhubs_owner_level"])
	if err != nil {
		return err
	}
	e.cfg = config{
		ObservePartitionTail: tailEnabled, TailPollInterval: time.Duration(tailInterval) * time.Millisecond,
		OwnerLevel:             ownerLevel,
		ProcessorMaxPartitions: int64(partitionCeiling),
		Lifetime:               ctx, ConsumerMode: stringValue(cfg, "eventhubs_consumer_mode"), CheckpointContainerURL: stringValue(cfg, "eventhubs_checkpoint_container_url"),
		ProducerLifecycle:  stringValue(cfg, "eventhubs_producer_lifecycle"),
		SampleTiming:       stringValue(cfg, "eventhubs_sample_timing"),
		Namespace:          stringValue(cfg, "eventhubs_namespace"),
		EventHub:           stringValue(cfg, "eventhubs_event_hub"),
		ConnectionString:   stringValue(cfg, "eventhubs_connection_string"),
		ConsumerGroup:      stringValue(cfg, "eventhubs_consumer_group"),
		PartitionKey:       stringValue(cfg, "eventhubs_partition_key"),
		PartitionID:        stringValue(cfg, "eventhubs_partition_id"),
		StartPosition:      strings.ToLower(strings.TrimSpace(stringValue(cfg, "eventhubs_start_position"))),
		AuthType:           strings.ToLower(strings.TrimSpace(stringValue(cfg, "eventhubs_auth_type"))),
		Timeout:            time.Duration(timeoutMS) * time.Millisecond,
		Mock:               boolValue(cfg, "eventhubs_mock"),
		AllowedTargetHosts: protocol.TargetPolicyHosts(cfg),
	}
	if err := ValidateExclusiveSettings(e.cfg.ConsumerMode, e.cfg.EventHub, e.cfg.ConsumerGroup, e.cfg.PartitionID, e.cfg.OwnerLevel, e.cfg.Mock); err != nil {
		return err
	}
	if e.cfg.AuthType == "" {
		e.cfg.AuthType = strings.TrimSpace(os.Getenv("APEXLOAD_EVENTHUBS_AUTH"))
	}
	if e.cfg.ConsumerGroup == "" {
		e.cfg.ConsumerGroup = strings.TrimSpace(os.Getenv("APEXLOAD_EVENTHUBS_PROBE_CONSUMER_GROUP"))
	}
	if e.cfg.ConnectionString == "" {
		e.cfg.ConnectionString = stringValue(cfg, "eventhubs_connection_string_secret")
	}
	if err := ValidateProcessorSettings(e.cfg.ConsumerMode, e.cfg.CheckpointContainerURL, e.cfg.AuthType, e.cfg.Namespace, e.cfg.ConsumerGroup, e.cfg.PartitionID, e.cfg.Mock); err != nil {
		return err
	}
	if (e.cfg.ConsumerMode == "processor" || e.cfg.ConsumerMode == "exclusive") && !realEventHubsAvailable {
		return fmt.Errorf("Event Hubs %s consumer requires an eventhubs_real build", e.cfg.ConsumerMode)
	}
	if e.cfg.ConsumerMode == "processor" {
		pool, err := defaultProcessorPool()
		if err != nil {
			return err
		}
		if e.cfg.ProcessorMaxPartitions > pool.limits.partitions {
			return fmt.Errorf("processor_max_partitions exceeds the configured process limit")
		}
		reservation, bound := protocol.RuntimeResourceReservationFromContext(ctx, eventHubsProtocolName)
		e.cfg.processorReservationBound = bound
		if bound {
			parent, ok := reservation.(*ProcessorReservation)
			if !ok || parent == nil || parent.pool != pool || !parent.Current() || parent.Demand().MaxPartitionsPerProcessor != e.cfg.ProcessorMaxPartitions {
				return fmt.Errorf("bound Event Hubs processor reservation is unavailable or mismatched")
			}
			e.cfg.processorReservation = parent
		}
	}
	if e.cfg.ConsumerMode == "processor" {
		if _, err := identitypolicy.ValidateEventHubsCheckpointContainer(e.cfg.CheckpointContainerURL); err != nil {
			return err
		}
	}
	if err := validateEventHubsConfigBounds(e.cfg); err != nil {
		return err
	}
	if e.cfg.ConsumerGroup == "" {
		e.cfg.ConsumerGroup = "$Default" // the Event Hubs default consumer group
	}
	if e.cfg.Timeout <= 0 {
		e.cfg.Timeout = defaultTimeout
	}
	if !e.cfg.Mock {
		for _, target := range []string{e.cfg.Namespace, e.cfg.ConnectionString, e.cfg.CheckpointContainerURL} {
			if strings.TrimSpace(target) == "" {
				continue
			}
			if err := protocol.ValidateTargetReference(target, e.cfg.AllowedTargetHosts); err != nil {
				return err
			}
		}
	}
	if e.cfg.ConsumerMode == "processor" {
		e.cfg.progress = newProcessorProgress(e.cfg)
	}
	if e.cfg.Mock {
		namespace := stringValue(cfg, "runtime_mock_namespace")
		sharedBroker.acquire(namespace)
		e.client = mockClient{broker: sharedBroker, namespace: namespace, release: &sync.Once{}}
	} else {
		e.client = newRealClient(e.cfg)
	}
	mockNamespace := stringValue(cfg, "runtime_mock_namespace")
	e.clientFactory = func() eventHubsClient {
		if e.cfg.Mock {
			namespace := mockNamespace
			sharedBroker.acquire(namespace)
			return mockClient{broker: sharedBroker, namespace: namespace, release: &sync.Once{}}
		}
		return newRealClient(e.cfg)
	}
	e.initialized = true
	e.progressSnapshot.Store(e.cfg.progress)
	ready = true
	return nil
}

func (e *EventHubs) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	if action == nil {
		return nil, fmt.Errorf("eventhubs action is required")
	}
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
	if err := e.validateActionBounds(action); err != nil {
		result.Success = false
		result.StatusCode = 400
		result.ErrorMessage = err.Error()
		result.Duration = time.Since(start)
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	actionCtx, actionCancel := context.WithTimeoutCause(ctx, e.timeout(action), errEventHubsActionTimeout)
	defer actionCancel()
	ctx = actionCtx

	if e.cfg.ProducerLifecycle == "per_sample" && strings.EqualFold(action.Type, "eventhubs_produce") {
		if e.clientFactory == nil {
			return nil, fmt.Errorf("per-sample producer factory is unavailable")
		}
		previous := e.client
		client := e.clientFactory()
		if client == nil {
			return nil, fmt.Errorf("per-sample producer factory returned no client")
		}
		e.client = client
		defer func() { e.client = previous }()
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closeErr := e.client.Teardown(cleanupCtx)
			result.WorkerDuration = time.Since(start)
			_ = protocol.StoreExtraction(result, "eventhubs_worker_occupancy_ms", strconv.FormatFloat(float64(result.WorkerDuration)/float64(time.Millisecond), 'f', 3, 64))
			if closeErr != nil {
				result.Success = false
				result.StatusCode = 500
				result.ErrorMessage = "Event Hubs producer cleanup failed"
			}
			result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		}()
	}
	var err error
	var sent, received event
	var hasSent, hasReceived, sendAttempted bool
	switch strings.ToLower(action.Type) {
	case "eventhubs_produce":
		sent, err = e.produce(ctx, action, result, &hasSent, &sendAttempted)
	case "eventhubs_consume":
		received, err = e.consume(ctx, action, result, &hasReceived)
	default:
		err = fmt.Errorf("unsupported eventhubs action type %q", action.Type)
	}

	result.Duration = time.Since(start)
	if err != nil && !hasSent && e.cfg.SampleTiming == "pnop_create_send_excludes_close" {
		result.Duration = 0
	}
	// An empty observer probe (no event within the timeout) is an expected
	// no-content outcome for this read-only, producer-first probe — consume() has
	// already set StatusCode 204 and the empty-probe extraction. Clear the error
	// so it is not scored as a failed transaction (which would inflate the error
	// rate and can trip error-rate SLA gates on otherwise-healthy runs). hasReceived
	// was computed before this and is correctly false (nothing was received).
	if errors.Is(err, errNoEvent) {
		err = nil
	}
	transportErr := err
	if hasSent || hasReceived || !sendAttempted && strings.EqualFold(action.Type, "eventhubs_produce") {
		transportErr = nil
	}
	result.ServerTelemetry = finalizeTelemetry(result, sent, hasSent, received, hasReceived, transportErr)
	if result.ServerTelemetry != nil && hasReceived {
		result.ServerTelemetry.SubscriptionID = paramString(action.Params, "consumer_group", e.cfg.ConsumerGroup)
	}
	if err != nil {
		result.Success = false
		if result.StatusCode == 0 || result.StatusCode == 200 {
			result.StatusCode = 500
		}
		result.ErrorMessage = err.Error()
		if strings.EqualFold(action.Type, "eventhubs_produce") && !hasSent {
			// Count a failed produce attempt so the messaging reliability telemetry
			// reflects send failures instead of always reading zero.
			if result.ServerTelemetry == nil {
				result.ServerTelemetry = &protocol.ServerTelemetry{}
			}
			result.ServerTelemetry.SendFailureCount++
		}
	}
	if strings.EqualFold(action.Type, "eventhubs_produce") {
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		protocol.AnnotateMessagingSendOutcome(result.ServerTelemetry, sendAttempted, hasSent, false)
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
	if e.cfg.ProducerLifecycle == "per_sample" {
		return nil
	}
	if !e.initialized || e.client == nil {
		return nil
	}
	return e.client.HealthCheck(ctx)
}

func (e *EventHubs) produce(ctx context.Context, action *protocol.Action, result *protocol.Result, accepted, attempted *bool) (observed event, operationErr error) {
	var sent event
	defer func() {
		if *accepted {
			observed = sent
		}
	}()
	if value, present := action.Params["messages"]; present {
		bodies, err := MessageBodies(value)
		if err != nil {
			return event{}, err
		}
		sent, operationErr = e.produceBatch(ctx, action, result, bodies, accepted, attempted)
		return sent, operationErr
	}
	hub := e.hub(action)
	if hub == "" {
		return event{}, fmt.Errorf("event hub name is required")
	}
	ev := e.buildEvent(action)
	if size := eventSize(ev); size > MaxEventHubsEventBytes {
		return event{}, fmt.Errorf("event payload and properties exceed %d bytes", MaxEventHubsEventBytes)
	}
	*attempted = true
	meta, err := e.client.Send(ctx, hub, ev)
	if err != nil {
		return event{}, err
	}
	ev.Offset, ev.SequenceNum, ev.EnqueuedAt = meta.Offset, meta.SequenceNum, meta.EnqueuedAt
	ev.AcknowledgementMs = meta.AcknowledgementMs
	if meta.PartitionID != "" {
		ev.PartitionID = meta.PartitionID
	}
	sent, *accepted = ev, true
	result.BytesSent = int64(len(ev.Body))
	if err := addEventExtractions(result, ev); err != nil {
		return event{}, err
	}
	if err := protocol.StoreExtraction(result, "eventhubs_operation", "produce"); err != nil {
		return event{}, err
	}
	return ev, evaluateAssertions(action.Assertions, result, ev)
}

func validateEventHubsConfigBounds(cfg config) error {
	if cfg.ProducerLifecycle != "" && cfg.ProducerLifecycle != "per_sample" {
		return fmt.Errorf("unsupported Event Hubs producer lifecycle")
	}
	if cfg.SampleTiming != "" && (cfg.SampleTiming != "pnop_create_send_excludes_close" || cfg.ProducerLifecycle != "per_sample") {
		return fmt.Errorf("unsupported Event Hubs sample timing")
	}
	if len(cfg.EventHub) > MaxEventHubNameBytes {
		return fmt.Errorf("eventhubs event_hub exceeds %d bytes", MaxEventHubNameBytes)
	}
	for name, value := range map[string]string{
		"namespace": cfg.Namespace, "consumer_group": cfg.ConsumerGroup,
		"partition_key": cfg.PartitionKey, "partition_id": cfg.PartitionID,
	} {
		if len(value) > MaxEventHubIdentifierBytes {
			return fmt.Errorf("eventhubs %s exceeds %d bytes", name, MaxEventHubIdentifierBytes)
		}
	}
	if len(cfg.ConnectionString) > 64<<10 {
		return fmt.Errorf("eventhubs connection string exceeds %d bytes", 64<<10)
	}
	return nil
}

func (e *EventHubs) validateActionBounds(action *protocol.Action) error {
	if value, present := action.Params["messages"]; present {
		if action.Type != "eventhubs_produce" || len(action.Body) != 0 {
			return fmt.Errorf("messages require a produce action without body")
		}
		if _, err := MessageBodies(value); err != nil {
			return err
		}
	}
	if len(e.hub(action)) > MaxEventHubNameBytes {
		return fmt.Errorf("eventhubs event hub name exceeds %d bytes", MaxEventHubNameBytes)
	}
	for name, value := range map[string]string{
		"consumer_group": paramString(action.Params, "consumer_group", e.cfg.ConsumerGroup),
		"partition_key":  paramString(action.Params, "partition_key", e.cfg.PartitionKey),
		"partition_id":   paramString(action.Params, "partition_id", e.cfg.PartitionID),
	} {
		if len(value) > MaxEventHubIdentifierBytes {
			return fmt.Errorf("eventhubs %s exceeds %d bytes", name, MaxEventHubIdentifierBytes)
		}
	}
	if _, present := action.Params["timeout_ms"]; present {
		if _, err := boundedInteger(action.Params["timeout_ms"], 0, 0, int(MaxConfiguredTimeout/time.Millisecond)); err != nil {
			return fmt.Errorf("eventhubs timeout_ms: %w", err)
		}
	}
	if len(action.Headers) > maxEventHubsProperties {
		return fmt.Errorf("eventhubs properties exceed %d entries", maxEventHubsProperties)
	}
	propertyBytes := 0
	for key, value := range action.Headers {
		propertyBytes += len(key) + len(value)
	}
	if propertyBytes > maxEventHubsPropertyBytes {
		return fmt.Errorf("eventhubs properties exceed %d bytes", maxEventHubsPropertyBytes)
	}
	if len(action.Body) > MaxEventHubsEventBytes {
		return fmt.Errorf("eventhubs body exceeds %d bytes", MaxEventHubsEventBytes)
	}
	return nil
}

func (e *EventHubs) consume(ctx context.Context, action *protocol.Action, result *protocol.Result, received *bool) (observed event, operationErr error) {
	var delivery event
	defer func() {
		if *received {
			observed = delivery
		}
	}()
	hub := e.hub(action)
	if hub == "" {
		return event{}, fmt.Errorf("event hub name is required")
	}
	group := paramString(action.Params, "consumer_group", e.cfg.ConsumerGroup)
	ev, lag, err := e.client.Receive(ctx, hub, group, e.timeout(action))
	if err != nil {
		configuredProbeTimeout := errors.Is(err, context.DeadlineExceeded) && errors.Is(context.Cause(ctx), errEventHubsActionTimeout)
		if errors.Is(err, errNoEvent) || configuredProbeTimeout {
			// Expected empty tail: report no-content success, not a failure.
			result.StatusCode = 204
			for key, value := range map[string]string{
				"eventhubs_operation": "consume", "eventhubs_empty_probe": "1",
				"eventhubs_consumer_lag": strconv.FormatInt(lag, 10),
			} {
				if storeErr := protocol.StoreExtraction(result, key, value); storeErr != nil {
					return event{}, storeErr
				}
			}
			return event{}, errNoEvent
		}
		// A genuine transport/auth error from the adapter is a real failure.
		result.StatusCode = 502
		return event{}, err
	}
	delivery, *received = ev, true
	accepted := false
	defer func() {
		if !accepted && ev.receipt != nil {
			ev.receipt.reject()
		}
	}()
	result.BytesReceived = int64(len(ev.Body))
	if err := addEventExtractions(result, ev); err != nil {
		return event{}, err
	}
	if err := protocol.StoreExtraction(result, "eventhubs_operation", "consume"); err != nil {
		return event{}, err
	}
	if lag >= 0 {
		if err := protocol.StoreExtraction(result, "eventhubs_consumer_lag", strconv.FormatInt(lag, 10)); err != nil {
			return event{}, err
		}
	}
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
			if err := protocol.StoreExtraction(result, "eventhubs_duplicate", "1"); err != nil {
				return event{}, err
			}
		}
		if ooo {
			if err := protocol.StoreExtraction(result, "eventhubs_out_of_order", "1"); err != nil {
				return event{}, err
			}
		}
	}
	if err := runExtractors(action.Extractors, result, ev); err != nil {
		return event{}, err
	}
	if err := evaluateAssertions(action.Assertions, result, ev); err != nil {
		return event{}, err
	}
	if ev.receipt != nil {
		if err := ev.receipt.finish(ctx); err != nil {
			return event{}, err
		}
	}
	accepted = true
	return ev, nil
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
