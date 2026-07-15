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
)

// newRealClient returns the azeventhubs-backed adapter. It is the non-mock path
// for builds tagged eventhubs_real. Construction is lazy: producer/consumer
// clients are created per event hub on first use, so a missing namespace or
// connection string surfaces as an actionable error at send/receive time rather
// than at Init.
func newRealClient(cfg config) eventHubsClient {
	return &realClient{
		cfg:        cfg,
		producers:  make(map[string]*azeventhubs.ProducerClient),
		consumers:  make(map[string]*azeventhubs.ConsumerClient),
		partitions: make(map[string]*azeventhubs.PartitionClient),
	}
}

// realClient is a per-VUser Event Hubs adapter. It is producer-first: Send is
// the primary path; Receive is a read-only observer probe over a single
// partition (cfg.PartitionID, default "0") used to measure end-to-end latency
// and offset backlog. Checkpointing / Processor-based exclusive consumption are
// deferred per the locked producer-first scope.
type realClient struct {
	cfg config

	mu         sync.Mutex
	cred       azcore.TokenCredential
	producers  map[string]*azeventhubs.ProducerClient  // keyed by event hub
	consumers  map[string]*azeventhubs.ConsumerClient  // keyed by hub\x00group
	partitions map[string]*azeventhubs.PartitionClient // keyed by hub\x00group\x00partition
	closed     bool
}

const (
	maxEventHubsProducers        = 64
	maxEventHubsConsumers        = 128
	maxEventHubsPartitionClients = 256
	maxEventHubsResourceKeyBytes = 128 << 10
	eventHubsCloseTimeout        = 5 * time.Second
)

func (c *realClient) Send(ctx context.Context, hub string, ev event) (eventMeta, error) {
	prod, err := c.producer(ctx, hub)
	if err != nil {
		return eventMeta{}, err
	}
	opts := &azeventhubs.EventDataBatchOptions{}
	if ev.PartitionKey != "" {
		pk := ev.PartitionKey
		opts.PartitionKey = &pk
	}
	if ev.PartitionID != "" {
		pid := ev.PartitionID
		opts.PartitionID = &pid
	}
	batch, err := prod.NewEventDataBatch(ctx, opts)
	if err != nil {
		return eventMeta{}, err
	}
	if err := batch.AddEventData(&azeventhubs.EventData{Body: ev.Body, Properties: toAnyMap(ev.Properties)}, nil); err != nil {
		if errors.Is(err, azeventhubs.ErrEventDataTooLarge) {
			return eventMeta{}, fmt.Errorf("event exceeds the Event Hubs batch size limit: %w", err)
		}
		return eventMeta{}, err
	}
	if err := prod.SendEventDataBatch(ctx, batch, nil); err != nil {
		return eventMeta{}, err
	}
	// SendEventDataBatch does not return per-event offsets/sequence numbers, so we
	// report the client-side send time and the resolved partition id (if any).
	return eventMeta{PartitionID: ev.PartitionID, EnqueuedAt: time.Now()}, nil
}

func (c *realClient) Receive(ctx context.Context, hub, group string, timeout time.Duration) (event, int64, error) {
	partition := c.cfg.PartitionID
	if partition == "" {
		partition = "0"
	}
	pc, err := c.partitionClient(hub, group, partition)
	if err != nil {
		return event{}, 0, err
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	events, err := pc.ReceiveEvents(rctx, 1, nil)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return event{}, 0, err
	}
	if len(events) == 0 {
		return event{}, 0, fmt.Errorf("no event received from %q partition %q within %s: %w", hub, partition, timeout, errNoEvent)
	}
	rd := events[0]
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
	// Offset backlog = tail sequence - received sequence (best effort; a failed
	// probe of partition properties just yields zero lag). Bound the probe by the
	// receive timeout so a stalled management link can't blow the action budget.
	lag := int64(0)
	if cc, cerr := c.consumer(hub, group); cerr == nil {
		lctx, lcancel := context.WithTimeout(ctx, timeout)
		if props, perr := cc.GetPartitionProperties(lctx, partition, nil); perr == nil {
			if d := props.LastEnqueuedSequenceNumber - rd.SequenceNumber; d > 0 {
				lag = d
			}
		}
		lcancel()
	}
	return ev, lag, nil
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
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	partitions := c.partitions
	consumers := c.consumers
	producers := c.producers
	c.partitions = make(map[string]*azeventhubs.PartitionClient)
	c.consumers = make(map[string]*azeventhubs.ConsumerClient)
	c.producers = make(map[string]*azeventhubs.ProducerClient)
	c.mu.Unlock()
	closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), eventHubsCloseTimeout)
	defer closeCancel()
	var firstErr error
	for _, pc := range partitions {
		if pc != nil {
			if err := pc.Close(closeCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, cc := range consumers {
		if cc != nil {
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
	pc, err := cc.NewPartitionClient(partition, &azeventhubs.PartitionClientOptions{
		StartPosition: start,
	})
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
