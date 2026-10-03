//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"fmt"
	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/jasonczech/apexload/pkg/identitypolicy"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxProcessorEventBytes = 1 << 20

type processorPartition interface {
	ReceiveEvents(context.Context, int, *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error)
	UpdateCheckpoint(context.Context, *eh.ReceivedEventData, *eh.UpdateCheckpointOptions) error
	PartitionID() string
	Close(context.Context) error
}
type eventHubsProcessor struct {
	pauseGate         protocol.RuntimePauseGate
	heartbeatInterval time.Duration
	demands           processorDemandSet
	ctx               context.Context
	cancel            context.CancelFunc
	done              chan struct{}
	deliveries        chan event
	failure           chan error
	store             *fencedCheckpointStore
	cleanupMu         sync.Mutex
	cleanupErr        error
	closeTimeout      time.Duration
	closeTransport    func(context.Context) error
	consumer          *eh.ConsumerClient
	progress          *processorProgress
	tailDone          chan struct{}
}

// Admission waiting is bounded by the caller, without spawning waiter goroutines.
type processorLifecycleGate struct {
	once  sync.Once
	token chan struct{}
}

func (g *processorLifecycleGate) Lock(ctx context.Context) error {
	ctx = resourceContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	g.once.Do(func() { g.token = make(chan struct{}, 1); g.token <- struct{}{} })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.token:
		if err := ctx.Err(); err != nil {
			g.Unlock()
			return err
		}
		return nil
	}
}
func (g *processorLifecycleGate) Unlock() { g.token <- struct{}{} }

func (c *realClient) receiveProcessed(ctx context.Context, hub, group string, timeout time.Duration) (event, int64, error) {
	if hub != c.cfg.EventHub || group != c.cfg.ConsumerGroup {
		return event{}, -1, fmt.Errorf("processor consumes only its configured event hub and consumer group")
	}
	ctx = resourceContext(ctx)
	budget, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.processorMu.Lock(budget); err != nil {
		if ctx.Err() != nil {
			return event{}, -1, ctx.Err()
		}
		return event{}, -1, errNoEvent
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		c.processorMu.Unlock()
		return event{}, -1, fmt.Errorf("Event Hubs client is closed")
	}
	if c.processor == nil {
		session, err := c.startProcessor(budget)
		if err != nil {
			c.processorMu.Unlock()
			if budget.Err() != nil {
				if ctx.Err() != nil {
					return event{}, -1, ctx.Err()
				}
				return event{}, -1, errNoEvent
			}
			return event{}, -1, err
		}
		c.processor = session
	}
	session := c.processor
	c.processorMu.Unlock()
	releaseDemand, err := session.registerDemand(budget)
	if err != nil {
		return event{}, -1, err
	}
	defer releaseDemand()
	select {
	case ev := <-session.deliveries:
		return ev, -1, nil
	case err := <-session.failure:
		return event{}, -1, err
	case <-session.done:
		return event{}, -1, fmt.Errorf("Event Hubs processor ended; recreate the VUser to restart")
	case <-budget.Done():
		if ctx.Err() != nil {
			return event{}, -1, ctx.Err()
		}
		return event{}, -1, errNoEvent
	}
}
func (c *realClient) startProcessor(ctx context.Context) (*eventHubsProcessor, error) {
	if c.cfg.Lifetime == nil || c.cfg.Lifetime.Err() != nil {
		return nil, fmt.Errorf("processor requires an active protocol lifecycle")
	}
	child, ownedParent, err := acquireProcessorResources(ctx, c.cfg)
	if err != nil {
		return nil, err
	}
	admitted := false
	var session *eventHubsProcessor
	defer func() {
		if !admitted {
			c.abortProcessorStart(session, child, ownedParent)
		}
	}()
	cc, err := c.consumer(c.cfg.EventHub, c.cfg.ConsumerGroup)
	if err != nil {
		return nil, err
	}
	session = c.ownProcessorConsumer(cc)
	startupCtx, startupCancel := context.WithCancel(ctx)
	stopLifetime := context.AfterFunc(session.ctx, startupCancel)
	defer stopLifetime()
	defer startupCancel()
	if session.ctx.Err() != nil {
		startupCancel()
	}
	if err := startupCtx.Err(); err != nil {
		return nil, err
	}
	var props eh.EventHubProperties
	if c.processorProperties != nil {
		props, err = c.processorProperties(startupCtx, cc)
	} else {
		props, err = cc.GetEventHubProperties(startupCtx, nil)
	}
	if err == nil {
		if session.ctx.Err() != nil {
			err = session.ctx.Err()
		} else {
			err = startupCtx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	partitions, err := validateProcessorPartitionInventory(props.PartitionIDs, child.parent.Demand().MaxPartitionsPerProcessor)
	if err != nil {
		return nil, err
	}
	approved, err := identitypolicy.ValidateEventHubsCheckpointContainer(c.cfg.CheckpointContainerURL)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	cred, err := c.credentialLocked()
	namespace := c.cfg.Namespace
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	blobs, err := newCheckpointBlobClient(approved, cred)
	if err != nil {
		return nil, err
	}
	store := &fencedCheckpointStore{pauseGate: c.cfg.pauseGate, records: blobCheckpointRecords{client: blobs}, namespace: namespace, hub: c.cfg.EventHub, group: c.cfg.ConsumerGroup, owner: cc.InstanceID(), partitions: partitions, progress: c.cfg.progress}
	start := eh.StartPosition{}
	yes := true
	if c.cfg.StartPosition == "earliest" {
		start.Earliest = &yes
	} else {
		start.Latest = &yes
	}
	var processor *eh.Processor
	if c.cfg.pauseGate == nil {
		processor, err = eh.NewProcessor(cc, store, &eh.ProcessorOptions{Prefetch: -1, StartPositions: eh.StartPositions{Default: start}})
		if err != nil {
			return nil, err
		}
	}
	session.store = store
	session.progress = c.cfg.progress
	session.progress.setInventory(partitions)
	lifetime, cancel := session.ctx, session.cancel
	c.mu.Lock()
	if c.closed || lifetime.Err() != nil || startupCtx.Err() != nil {
		c.mu.Unlock()
		return nil, errors.New("Event Hubs processor startup cancelled before execution")
	}
	admitted = true
	if c.cfg.ObservePartitionTail {
		session.tailDone = startProcessorTail(session.ctx, c.cfg, partitions, session.progress, cc.GetPartitionProperties)
	}
	go func() {
		defer session.finishProcessor(child, ownedParent)
		defer cancel()
		if session.pauseGate != nil {
			if err := session.runGovernedProcessor(child, c.cfg.StartPosition, func(id, generation string, position eh.StartPosition) (processorPartition, error) {
				return &governedPartition{store: store, partition: id, generation: generation, anchor: position, queryBoundary: cc.GetPartitionProperties, reopen: func(position eh.StartPosition) (partitionReceiver, error) {
					private, err := eh.NewConsumerClient(namespace, c.cfg.EventHub, c.cfg.ConsumerGroup, cred, &eh.ConsumerClientOptions{InstanceID: store.owner})
					if err != nil {
						return nil, err
					}
					owned := &ownedPartitionReceiver{consumer: private}
					level := int64(0)
					owned.inner, err = private.NewPartitionClient(id, &eh.PartitionClientOptions{OwnerLevel: &level, Prefetch: -1, StartPosition: position})
					return owned, err
				}}, nil
			}); err != nil {
				session.fail(err)
			}
			return
		}
		var workers sync.WaitGroup
		dispatched := make(chan struct{})
		go func() {
			defer close(dispatched)
			session.dispatchPartitions(child, &workers, func(ctx context.Context) processorPartition {
				partition := processor.NextPartitionClient(ctx)
				if partition == nil {
					return nil
				}
				return partition
			})
		}()
		if err := processor.Run(lifetime); err != nil {
			session.fail(err)
		}
		cancel()
		<-dispatched
		workers.Wait()
	}()
	c.mu.Unlock()
	return session, nil
}

// Called under processorMu by receiveProcessed. Ownership transfers before any
// inventory request, so even failed startup closes through the same proof path.
func (c *realClient) ownProcessorConsumer(consumer *eh.ConsumerClient) *eventHubsProcessor {
	lifetime, cancel := context.WithCancel(c.cfg.Lifetime)
	session := &eventHubsProcessor{pauseGate: c.cfg.pauseGate, ctx: lifetime, cancel: cancel, done: make(chan struct{}), deliveries: make(chan event), failure: make(chan error, 1), consumer: consumer, closeTransport: consumer.Close, progress: c.cfg.progress}
	c.mu.Lock()
	c.processorCancel = cancel
	c.processorOwnedConsumer = consumer
	if c.closed {
		cancel()
	}
	key := c.cfg.EventHub + "\x00" + c.cfg.ConsumerGroup
	if c.consumers[key] == consumer {
		delete(c.consumers, key)
	}
	c.mu.Unlock()
	return session
}
func (c *realClient) abortProcessorStart(session *eventHubsProcessor, child *processorLease, ownedParent bool) {
	if session == nil {
		child.release()
		if ownedParent {
			child.parent.Release()
		}
		return
	}
	// Publish the failed session before releasing processorMu. Subsequent reads
	// fail explicitly rather than starting another processor over pending cleanup.
	session.fail(errors.New("Event Hubs processor initialization failed; recreate the VUser"))
	c.processor = session
	go session.finishProcessor(child, ownedParent)
}

// The SDK owns an internal queue and closes it when Run exits. Cancelling its
// NextPartitionClient context can skip queued handles, so drain with preserved
// values and no cancellation until that channel closes. Cancelled work never
// receives or polls messages; it only proves each returned partition's close.
func (s *eventHubsProcessor) dispatchPartitions(child *processorLease, workers *sync.WaitGroup, next func(context.Context) processorPartition) {
	drainCtx := context.WithoutCancel(s.ctx)
	for {
		partition := next(drainCtx)
		if partition == nil {
			return
		}
		if s.ctx.Err() != nil {
			s.closePartition(partition, nil, child)
			continue
		}
		worker, err := child.acquireWorker(partition.PartitionID())
		if err != nil {
			s.fail(errors.New("Event Hubs processor worker reservation unavailable"))
			s.closePartition(partition, nil, child)
			continue
		}
		workers.Add(1)
		go func() { defer workers.Done(); s.partitionWorkerWithLease(partition, worker) }()
	}
}
func validateProcessorPartitionInventory(ids []string, ceiling int64) (map[string]bool, error) {
	if len(ids) == 0 || int64(len(ids)) > ceiling {
		return nil, errors.New("Event Hubs partition inventory exceeds the reserved processor ceiling")
	}
	partitions := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 64 || partitions[id] {
			return nil, errors.New("invalid processor partition inventory")
		}
		partitions[id] = true
	}
	return partitions, nil
}
func (l *processorLease) beginDrain() {
	if l == nil || l.parent == nil {
		return
	}
	l.parent.pool.mu.Lock()
	defer l.parent.pool.mu.Unlock()
	l.closing = true
}

func (s *eventHubsProcessor) finishProcessor(child *processorLease, ownedParent bool) {
	child.beginDrain()
	s.progress.state("draining")
	if s.tailDone != nil {
		<-s.tailDone
	}
	// The SDK closes partition handles internally and may discard their errors.
	// Closing this exclusively owned consumer also closes its AMQP connection;
	// pinned go-amqp Conn.Close waits for both reader/writer goroutines. A blocked
	// transport close keeps the child charged and done unopened until it exits.
	if s.closeTransport != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.cleanupTimeout())
		err := s.closeTransport(ctx)
		cancel()
		if err != nil {
			child.quarantine()
			s.recordCleanupError("Event Hubs processor transport cleanup incomplete")
		}
	}
	child.release()
	if ownedParent {
		child.parent.Release()
	}
	if s.cleanupError() != nil {
		s.progress.state("quarantined")
	} else {
		s.progress.state("released")
	}
	// Teardown readers observe final pool accounting and cleanup error once done.
	close(s.done)
}
func (s *eventHubsProcessor) fail(err error) {
	select {
	case s.failure <- err:
	default:
	}
	s.cancel()
}

// An error is deliberately redaction-safe; provider errors can contain tokens
// or destination details. Unproven closes retain their resource entitlement.
func (s *eventHubsProcessor) closePartition(partition processorPartition, worker *processorWorkerLease, rejected *processorLease) bool {
	ctx, cancel := context.WithTimeout(context.Background(), s.cleanupTimeout())
	defer cancel()
	if err := partition.Close(ctx); err != nil {
		if rejected != nil {
			rejected.quarantine()
		}
		s.recordCleanupError("Event Hubs processor partition cleanup incomplete")
		return false
	} else if worker != nil {
		worker.release()
	}
	return true
}
func (s *eventHubsProcessor) cleanupTimeout() time.Duration {
	if s.closeTimeout > 0 {
		return s.closeTimeout
	}
	return eventHubsCloseTimeout
}
func (s *eventHubsProcessor) recordCleanupError(message string) {
	incomplete := errors.New(message)
	s.cleanupMu.Lock()
	s.cleanupErr = incomplete
	s.cleanupMu.Unlock()
	s.fail(incomplete)
}
func (s *eventHubsProcessor) cleanupError() error {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	return s.cleanupErr
}
func (s *eventHubsProcessor) partitionWorker(partition processorPartition) {
	s.partitionWorkerWithLease(partition, nil)
}

func (s *eventHubsProcessor) partitionWorkerWithLease(partition processorPartition, worker *processorWorkerLease) {
	s.partitionWorkerContext(partition, worker, s.ctx)
}
func (s *eventHubsProcessor) partitionWorkerContext(partition processorPartition, worker *processorWorkerLease, workerCtx context.Context, expectedGeneration ...string) {
	var unresolvedReceiveProof func()
	defer func() {
		// Only an actual proved exclusive connection close can discharge a
		// receive whose first cleanup returned an error. Resource retention alone
		// is insufficient to keep PauseAndWait from claiming quiescence.
		if s.closePartition(partition, worker, nil) && unresolvedReceiveProof != nil {
			unresolvedReceiveProof()
		}
	}()
	fence, err := s.store.ownershipFence(workerCtx, partition.PartitionID())
	if err != nil {
		if !errors.Is(err, errCheckpointFence) && s.ctx.Err() == nil {
			s.fail(fmt.Errorf("processor ownership verification failed: %w", err))
		}
		return
	}
	if len(expectedGeneration) > 0 && fence != expectedGeneration[0] {
		return
	}
	failures := 0
	for {
		if workerCtx.Err() != nil {
			return
		}
		receiveCtx, releaseReceive, err := s.receiveAdmission(s.pauseGate != nil, workerCtx)
		if err != nil {
			if workerCtx.Err() != nil || s.ctx.Err() != nil {
				return
			}
			if s.pauseGate != nil && !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
				continue
			}
			s.fail(err)
			return
		}
		if s.pauseGate != nil {
			current, verifyErr := s.store.ownershipFence(receiveCtx, partition.PartitionID())
			if verifyErr != nil || current != fence {
				cancelled := receiveCtx.Err() != nil
				releaseReceive()
				if errors.Is(verifyErr, errCheckpointFence) || verifyErr == nil {
					s.progress.ownership(partition.PartitionID(), fence, "lost", nil, "")
					return
				}
				if cancelled {
					continue
				}
				s.fail(errors.New("processor ownership could not be verified before receive"))
				return
			}
		}
		operationCtx, cancelOperation := context.WithCancel(receiveCtx)
		stopWorker := context.AfterFunc(workerCtx, cancelOperation)
		events, err := partition.ReceiveEvents(operationCtx, 1, nil)
		interrupted := s.pauseInterrupted(operationCtx, err)
		stopWorker()
		cancelOperation()
		if errors.Is(err, errProcessorReceiveCleanupUnproven) {
			unresolvedReceiveProof = releaseReceive
			s.recordCleanupError("processor receive transport cleanup incomplete")
			return
		}
		releaseReceive()
		if err != nil {
			if errors.Is(err, errCheckpointFence) {
				s.progress.ownership(partition.PartitionID(), fence, "lost", nil, "")
				return
			}
			var providerErr *eh.Error
			if errors.As(err, &providerErr) && providerErr.Code == eh.ErrorCodeOwnershipLost {
				s.progress.ownership(partition.PartitionID(), fence, "lost", nil, "")
				return
			}
			if s.ctx.Err() != nil || workerCtx.Err() != nil {
				return
			}
			if interrupted {
				if len(events) == 0 {
					continue
				}
			} else {
				s.fail(err)
				return
			}
		}
		if len(events) > 1 {
			s.fail(errors.New("processor receive exceeded bounded event count"))
			return
		}
		if len(events) == 0 {
			continue
		}
		rd := events[0]
		if rd == nil || rd.SequenceNumber < 0 || !validProviderOffset(rd.Offset) || len(rd.Body) > maxProcessorEventBytes || len(rd.Properties) > 256 {
			s.fail(errors.New("processor event cursor or retained payload exceeds limits"))
			return
		}
		retainedBytes := len(rd.Body) + len(rd.Offset)
		partitionKey := ""
		if rd.PartitionKey != nil {
			if len(*rd.PartitionKey) > 64<<10 || !utf8.ValidString(*rd.PartitionKey) {
				s.fail(errors.New("processor partition key exceeds limits"))
				return
			}
			partitionKey = strings.Clone(*rd.PartitionKey)
			retainedBytes += len(partitionKey)
		}
		if retainedBytes > maxProcessorEventBytes {
			s.fail(errors.New("processor retained event exceeds 1 MiB"))
			return
		}
		properties, err := processorProperties(rd.Properties, retainedBytes)
		if err != nil {
			s.fail(errors.New("processor retained event exceeds 1 MiB"))
			return
		}
		ev := event{Body: append([]byte(nil), rd.Body...), Properties: properties, PartitionID: partition.PartitionID(), Offset: rd.SequenceNumber, SequenceNum: rd.SequenceNumber, PartitionKey: partitionKey}
		if rd.EnqueuedTime != nil {
			ev.EnqueuedAt = *rd.EnqueuedTime
		}
		// SDK raw AMQP/system metadata can contain additional unbounded fields.
		// Keep only the cursor consumed by UpdateCheckpoint across the ACK/CAS
		// wait, with selected payload copied into the bounded application event.
		rd = &eh.ReceivedEventData{SequenceNumber: rd.SequenceNumber, Offset: strings.Clone(rd.Offset)}
		events = nil
		deliveredBefore := false
		for {
			receipt := &processorReceipt{acks: make(chan processorAck, 1), result: make(chan error, 1), lifetime: workerCtx}
			ev.receipt = receipt
			deliveryCtx, releaseDelivery, err := s.receiveAdmission(s.pauseGate != nil, workerCtx)
			if err != nil {
				if workerCtx.Err() != nil || s.ctx.Err() != nil {
					return
				}
				if errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
					s.fail(err)
					return
				}
				continue
			}
			if s.pauseGate != nil {
				current, verifyErr := s.store.ownershipFence(deliveryCtx, partition.PartitionID())
				if verifyErr != nil || current != fence {
					cancelled := deliveryCtx.Err() != nil
					releaseDelivery()
					if errors.Is(verifyErr, errCheckpointFence) || verifyErr == nil {
						s.progress.ownership(partition.PartitionID(), fence, "lost", nil, "")
						return
					}
					if cancelled {
						continue
					}
					s.fail(errors.New("processor ownership could not be verified before delivery"))
					return
				}
			}
			select {
			case s.deliveries <- ev:
				s.progress.deliveredEvent(partition.PartitionID(), fence, rd.SequenceNumber, rd.Offset, deliveredBefore)
				deliveredBefore = true
			case <-deliveryCtx.Done():
				releaseDelivery()
				if workerCtx.Err() != nil || s.ctx.Err() != nil {
					return
				}
				continue
			case <-workerCtx.Done():
				releaseDelivery()
				return
			}
			releaseDelivery()
			var ack processorAck
			select {
			case ack = <-receipt.acks:
			case <-workerCtx.Done():
				return
			}
			if !ack.accept {
				s.progress.rejectedEvent(partition.PartitionID(), fence)
				continue
			}
			checkpointCtx := context.WithValue(ack.ctx, checkpointFenceContextKey{}, fence)
			checkpointCtx = context.WithValue(checkpointCtx, checkpointRetryContextKey{}, failures > 0)
			err = partition.UpdateCheckpoint(checkpointCtx, rd, nil)
			receipt.result <- err
			if errors.Is(err, errCheckpointFence) {
				return
			}
			if err == nil {
				failures = 0
				break
			}
			failures++
			if failures >= 3 {
				s.fail(fmt.Errorf("processor checkpoint failed repeatedly: %w", err))
				return
			}
		}
	}
}

func processorProperties(values map[string]any, bodyBytes int) (map[string]string, error) {
	out := map[string]string{}
	total := bodyBytes
	for key, value := range values {
		if len(key) > 256 || !utf8.ValidString(key) {
			return nil, fmt.Errorf("processor property key exceeds limit")
		}
		var text string
		switch v := value.(type) {
		case string:
			if len(v) > 64<<10 || !utf8.ValidString(v) {
				return nil, fmt.Errorf("processor property exceeds limit")
			}
			text = strings.Clone(v)
		case []byte:
			if len(v) > 64<<10 {
				return nil, fmt.Errorf("processor property exceeds limit")
			}
			text = string(v)
		case nil:
			text = ""
		case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			text = fmt.Sprint(v)
		case time.Time:
			text = v.UTC().Format(time.RFC3339Nano)
		default:
			return nil, fmt.Errorf("processor properties must be bounded scalar values")
		}
		total += len(key) + len(text)
		if total > maxProcessorEventBytes {
			return nil, fmt.Errorf("processor retained event exceeds 1 MiB")
		}
		out[strings.Clone(key)] = text
	}
	return out, nil
}
