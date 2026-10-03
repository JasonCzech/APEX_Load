//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	azeventhubs "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/jasonczech/apexload/pkg/identitypolicy"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// newRealClient returns the azeventhubs-backed adapter. It is the non-mock path
// for builds tagged eventhubs_real. Construction is lazy: producer/consumer
// clients are created per event hub on first use, so a missing namespace or
// connection string surfaces as an actionable error at send/receive time rather
// than at Init.
func newRealClient(cfg config) eventHubsClient {
	if cfg.ConsumerMode == "processor" && cfg.progress == nil {
		cfg.progress = newProcessorProgress(cfg)
	}
	return &realClient{
		cfg:        cfg,
		producers:  make(map[string]*azeventhubs.ProducerClient),
		consumers:  make(map[string]*azeventhubs.ConsumerClient),
		partitions: make(map[string]*azeventhubs.PartitionClient),
	}
}

// realClient supports producer and observer paths, plus an opt-in processor
// with balanced partition ownership and fenced durable Blob checkpoints.
// Processor construction and collection remain lazy until the first consume.
const realEventHubsAvailable = true

type realClient struct {
	processorMu            processorLifecycleGate
	processor              *eventHubsProcessor
	processorCancel        context.CancelFunc
	processorOwnedConsumer *azeventhubs.ConsumerClient
	processorProperties    func(context.Context, *azeventhubs.ConsumerClient) (azeventhubs.EventHubProperties, error)
	closeTimeout           time.Duration
	cfg                    config

	mu            sync.Mutex
	cred          azcore.TokenCredential
	producers     map[string]*azeventhubs.ProducerClient  // keyed by event hub
	consumers     map[string]*azeventhubs.ConsumerClient  // keyed by hub\x00group
	partitions    map[string]*azeventhubs.PartitionClient // keyed by hub\x00group\x00partition
	ownershipLost error
	closed        bool
}

const (
	maxEventHubsProducers        = 64
	maxEventHubsConsumers        = 128
	maxEventHubsPartitionClients = 256
	maxEventHubsResourceKeyBytes = 128 << 10
	eventHubsCloseTimeout        = 5 * time.Second
)

func (c *realClient) MessagingProgressSnapshot() *protocol.MessagingProgressSnapshot {
	return c.cfg.progress.snapshot()
}

func (c *realClient) Send(ctx context.Context, hub string, ev event) (eventMeta, error) {
	return c.SendBatch(ctx, hub, []event{ev})
}

func (c *realClient) SendBatch(ctx context.Context, hub string, events []event) (eventMeta, error) {
	if len(events) == 0 {
		return eventMeta{}, fmt.Errorf("Event Hubs message list is empty")
	}
	prod, err := c.producer(ctx, hub)
	if err != nil {
		return eventMeta{}, err
	}
	opts := &azeventhubs.EventDataBatchOptions{}
	first := events[0]
	if first.PartitionKey != "" {
		opts.PartitionKey = &first.PartitionKey
	}
	if first.PartitionID != "" {
		opts.PartitionID = &first.PartitionID
	}
	for _, ev := range events {
		if ev.PartitionKey != first.PartitionKey || ev.PartitionID != first.PartitionID {
			return eventMeta{}, fmt.Errorf("sampled Event Hubs messages require one partition selection")
		}
	}
	batch, err := prod.NewEventDataBatch(ctx, opts)
	if err != nil {
		return eventMeta{}, err
	}
	var ackTotal float64
	sendAcknowledged := func(batch *azeventhubs.EventDataBatch) error {
		started := time.Now()
		err := prod.SendEventDataBatch(ctx, batch, nil)
		if err == nil {
			ackTotal += float64(time.Since(started)) / float64(time.Millisecond)
		}
		return err
	}
	count := 0
	for _, ev := range events {
		data := &azeventhubs.EventData{Body: ev.Body, Properties: toAnyMap(ev.Properties)}
		err = batch.AddEventData(data, nil)
		if errors.Is(err, azeventhubs.ErrEventDataTooLarge) && count > 0 {
			if err = sendAcknowledged(batch); err != nil {
				return eventMeta{}, err
			}
			batch, err = prod.NewEventDataBatch(ctx, opts)
			if err != nil {
				return eventMeta{}, err
			}
			count = 0
			err = batch.AddEventData(data, nil)
		}
		if err != nil {
			return eventMeta{}, err
		}
		count++
	}
	if count > 0 {
		if err = sendAcknowledged(batch); err != nil {
			return eventMeta{}, err
		}
	}
	return eventMeta{PartitionID: first.PartitionID, EnqueuedAt: time.Now(), AcknowledgementMs: &ackTotal}, nil
}

func (c *realClient) Receive(ctx context.Context, hub, group string, timeout time.Duration) (event, int64, error) {
	if c.cfg.ConsumerMode == "processor" {
		return c.receiveProcessed(ctx, hub, group, timeout)
	}
	if err := c.exclusiveReceiveError(hub, group); err != nil {
		return event{}, -1, err
	}
	partition := c.cfg.PartitionID
	if partition == "" {
		partition = "0"
	}
	pc, err := c.partitionClient(hub, group, partition)
	if err != nil {
		return event{}, 0, err
	}
	rd, lag, err := receiveEventHubsWithinBudget(ctx, timeout,
		func(receiveCtx context.Context) ([]*azeventhubs.ReceivedEventData, error) {
			return pc.ReceiveEvents(receiveCtx, 1, nil)
		},
		func(probeCtx context.Context, received *azeventhubs.ReceivedEventData) int64 {
			// Offset backlog = tail sequence - received sequence. This is best
			// effort: a failed properties probe yields zero lag, but it must share
			// the receive budget rather than starting a second full action timeout.
			cc, cerr := c.consumer(hub, group)
			if cerr != nil {
				return -1
			}
			props, perr := cc.GetPartitionProperties(probeCtx, partition, nil)
			if perr != nil {
				return -1
			}
			if delta := props.LastEnqueuedSequenceNumber - received.SequenceNumber; delta > 0 {
				return delta
			}
			return 0
		},
	)
	if err != nil {
		err = c.recordExclusiveOwnershipLoss(err)
		if errors.Is(err, errNoEvent) {
			return event{}, 0, fmt.Errorf("no event received from %q partition %q within %s: %w", hub, partition, timeout, errNoEvent)
		}
		return event{}, 0, err
	}
	ev := event{
		Body:        rd.Body,
		Properties:  fromAnyMap(rd.Properties),
		PartitionID: partition,
		Offset:      rd.SequenceNumber,
		SequenceNum: rd.SequenceNumber,
	}
	if rd.PartitionKey != nil {
		ev.PartitionKey = *rd.PartitionKey
	}
	if rd.EnqueuedTime != nil {
		ev.EnqueuedAt = *rd.EnqueuedTime
	}
	return ev, lag, nil
}

// receiveEventHubsWithinBudget applies one deadline to the broker receive and
// its best-effort lag probe. Starting a fresh timeout for the probe would allow a
// single consume action to run for almost twice its configured timeout.
func receiveEventHubsWithinBudget(
	ctx context.Context,
	timeout time.Duration,
	receive func(context.Context) ([]*azeventhubs.ReceivedEventData, error),
	probeLag func(context.Context, *azeventhubs.ReceivedEventData) int64,
) (*azeventhubs.ReceivedEventData, int64, error) {
	budgetCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	events, err := receive(budgetCtx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, 0, err
	}
	if len(events) == 0 {
		return nil, 0, errNoEvent
	}
	return events[0], probeLag(budgetCtx, events[0]), nil
}

func (c *realClient) HealthCheck(ctx context.Context) error {
	if c.cfg.Namespace == "" && c.cfg.ConnectionString == "" {
		return fmt.Errorf("config.eventhubs.namespace or connection_string is required")
	}
	if c.cfg.EventHub == "" {
		// Without a default event hub we cannot probe properties; resolving the
		// client config above is the best health signal available.
		return nil
	}
	prod, err := c.producer(ctx, c.cfg.EventHub)
	if err != nil {
		return err
	}
	_, err = prod.GetEventHubProperties(ctx, nil)
	return err
}

func (c *realClient) Teardown(ctx context.Context) error {
	closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(resourceContext(ctx)), c.cleanupTimeout())
	defer closeCancel()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.waitProcessorCleanup(ctx)
	}
	c.closed = true
	if c.processorCancel != nil {
		c.processorCancel()
	}
	ownedConsumer := c.processorOwnedConsumer
	partitions := c.partitions
	consumers := c.consumers
	producers := c.producers
	c.partitions = make(map[string]*azeventhubs.PartitionClient)
	c.consumers = make(map[string]*azeventhubs.ConsumerClient)
	c.producers = make(map[string]*azeventhubs.ProducerClient)
	c.mu.Unlock()
	var processor *eventHubsProcessor
	var firstErr error
	if err := c.processorMu.Lock(closeCtx); err != nil {
		firstErr = errors.New("Event Hubs processor cleanup deadline exceeded")
	} else {
		processor = c.processor
		c.processorMu.Unlock()
		if processor != nil {
			processor.cancel()
		}
	}
	if processor == nil && c.cfg.progress != nil && firstErr == nil {
		c.cfg.progress.state("released")
	}
	if processor != nil {
		select {
		case <-processor.done:
			firstErr = processor.cleanupError()
		case <-closeCtx.Done():
			firstErr = fmt.Errorf("Event Hubs processor cleanup deadline exceeded")
		}
	}
	for _, pc := range partitions {
		if pc != nil {
			if err := pc.Close(closeCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for key, cc := range consumers {
		// Only startProcessor can create the fixed processor consumer. Ownership
		// may transfer after this snapshot or even after the gate deadline, so
		// its immutable key also belongs exclusively to startup/session cleanup.
		// A duplicate SDK Close can block on its namespace lock after our budget.
		processorKey := c.cfg.ConsumerMode == "processor" && key == c.cfg.EventHub+"\x00"+c.cfg.ConsumerGroup
		if cc != nil && !processorKey && cc != ownedConsumer && (processor == nil || cc != processor.consumer) {
			if err := cc.Close(closeCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, p := range producers {
		if p != nil {
			if err := p.Close(closeCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (c *realClient) cleanupTimeout() time.Duration {
	if c.closeTimeout > 0 {
		return c.closeTimeout
	}
	return eventHubsCloseTimeout
}
func (c *realClient) waitProcessorCleanup(ctx context.Context) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(resourceContext(ctx)), c.cleanupTimeout())
	defer cancel()
	if err := c.processorMu.Lock(closeCtx); err != nil {
		return errors.New("Event Hubs processor cleanup deadline exceeded")
	}
	processor := c.processor
	c.processorMu.Unlock()
	if processor == nil {
		return nil
	}
	select {
	case <-processor.done:
		return processor.cleanupError()
	case <-closeCtx.Done():
		return errors.New("Event Hubs processor cleanup deadline exceeded")
	}
}

func (c *realClient) resourceKeyBytesLocked() int {
	total := 0
	for key := range c.producers {
		total += len(key)
	}
	for key := range c.consumers {
		total += len(key)
	}
	for key := range c.partitions {
		total += len(key)
	}
	return total
}

func (c *realClient) resourceAdmissionErrorLocked(kind, key string, current, limit int) error {
	if c.closed {
		return fmt.Errorf("Event Hubs client is closed")
	}
	if current >= limit || len(key) > maxEventHubsResourceKeyBytes-c.resourceKeyBytesLocked() {
		return fmt.Errorf("Event Hubs %s capacity exceeded (%d handles, %d key bytes)", kind, limit, maxEventHubsResourceKeyBytes)
	}
	return nil
}

// useConnString decides between SAS (connection string) and AAD (token
// credential) auth. AuthType pins the choice; otherwise a present connection
// string wins (SAS), falling back to AAD against the namespace.
func (c *realClient) useConnString() bool {
	switch c.cfg.AuthType {
	case "aad":
		return false
	case "connection_string":
		return true
	default:
		return c.cfg.ConnectionString != ""
	}
}

func (c *realClient) producer(_ context.Context, hub string) (*azeventhubs.ProducerClient, error) {
	if hub == "" {
		return nil, fmt.Errorf("event hub name is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.producers[hub]; ok {
		return p, nil
	}
	if err := c.resourceAdmissionErrorLocked("producer", hub, len(c.producers), maxEventHubsProducers); err != nil {
		return nil, err
	}
	var (
		p   *azeventhubs.ProducerClient
		err error
	)
	if c.useConnString() {
		if c.cfg.ConnectionString == "" {
			return nil, fmt.Errorf("config.eventhubs.connection_string is required for connection_string auth")
		}
		p, err = azeventhubs.NewProducerClientFromConnectionString(c.cfg.ConnectionString, hub, nil)
	} else {
		if c.cfg.Namespace == "" {
			return nil, fmt.Errorf("config.eventhubs.namespace is required for aad auth")
		}
		cred, cerr := c.credentialLocked()
		if cerr != nil {
			return nil, cerr
		}
		p, err = azeventhubs.NewProducerClient(c.cfg.Namespace, hub, cred, nil)
	}
	if err != nil {
		return nil, err
	}
	c.producers[hub] = p
	return p, nil
}

func (c *realClient) consumer(hub, group string) (*azeventhubs.ConsumerClient, error) {
	if hub == "" {
		return nil, fmt.Errorf("event hub name is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hub + "\x00" + group
	if cc, ok := c.consumers[key]; ok {
		return cc, nil
	}
	if err := c.resourceAdmissionErrorLocked("consumer", key, len(c.consumers), maxEventHubsConsumers); err != nil {
		return nil, err
	}
	var (
		cc  *azeventhubs.ConsumerClient
		err error
	)
	if c.useConnString() {
		if c.cfg.ConnectionString == "" {
			return nil, fmt.Errorf("config.eventhubs.connection_string is required for connection_string auth")
		}
		cc, err = azeventhubs.NewConsumerClientFromConnectionString(c.cfg.ConnectionString, hub, group, nil)
	} else {
		if c.cfg.Namespace == "" {
			return nil, fmt.Errorf("config.eventhubs.namespace is required for aad auth")
		}
		cred, cerr := c.credentialLocked()
		if cerr != nil {
			return nil, cerr
		}
		cc, err = azeventhubs.NewConsumerClient(c.cfg.Namespace, hub, group, cred, nil)
	}
	if err != nil {
		return nil, err
	}
	c.consumers[key] = cc
	return cc, nil
}

func (c *realClient) partitionClient(hub, group, partition string) (*azeventhubs.PartitionClient, error) {
	cc, err := c.consumer(hub, group)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hub + "\x00" + group + "\x00" + partition
	if pc, ok := c.partitions[key]; ok {
		return pc, nil
	}
	if err := c.resourceAdmissionErrorLocked("partition client", key, len(c.partitions), maxEventHubsPartitionClients); err != nil {
		return nil, err
	}
	// Start position for the observer probe. Default "latest" (the partition tail)
	// suits a sustained-load observer sampling freshly-arriving events. A
	// produce→consume round-trip in the SAME run must use "earliest" (from the
	// partition start) — otherwise a receiver opened lazily AFTER the produce
	// starts past the just-produced event and never sees it (the mock reads from
	// the start, so it would pass while the real adapter silently skips).
	var start azeventhubs.StartPosition
	if c.cfg.StartPosition == "earliest" {
		earliest := true
		start.Earliest = &earliest
	} else {
		latest := true
		start.Latest = &latest
	}
	options, err := exclusivePartitionOptions(c.cfg, start)
	if err != nil {
		return nil, err
	}
	pc, err := cc.NewPartitionClient(partition, options)
	if err != nil {
		return nil, err
	}
	c.partitions[key] = pc
	return pc, nil
}

// credentialLocked lazily builds (and caches) a DefaultAzureCredential. Callers
// must hold c.mu.
func (c *realClient) credentialLocked() (azcore.TokenCredential, error) {
	if c.cred != nil {
		return c.cred, nil
	}
	namespace, err := identitypolicy.ValidateAzureMessagingNamespace(c.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	c.cfg.Namespace = namespace
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	c.cred = cred
	return cred, nil
}

func toAnyMap(in map[string]string) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func fromAnyMap(in map[string]any) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmt.Sprint(v)
	}
	return out
}
