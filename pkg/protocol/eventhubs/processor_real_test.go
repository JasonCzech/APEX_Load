//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"fmt"
	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"sync"
	"testing"
	"time"
)

// The external storage boundary is isolated; the production checkpoint state
// machine and SDK-shaped delivery/checkpoint calls remain real implementations.
type checkpointMemoryIO struct {
	mu       sync.Mutex
	values   map[string]partitionCheckpointRecord
	revision int
}

func (m *checkpointMemoryIO) List(_ context.Context, prefix string) ([]partitionCheckpointRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []partitionCheckpointRecord
	for k, v := range m.values {
		if strings.HasPrefix(k, prefix) {
			out = append(out, v)
		}
	}
	return out, nil
}
func (m *checkpointMemoryIO) Read(_ context.Context, key string) (partitionCheckpointRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.values[key]
	v.Name = key
	if v.Sequence != nil {
		seq := *v.Sequence
		v.Sequence = &seq
	}
	return v, nil
}
func (m *checkpointMemoryIO) Write(_ context.Context, v partitionCheckpointRecord, expected string) (partitionCheckpointRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values[v.Name].ETag != expected {
		return partitionCheckpointRecord{}, errCheckpointFence
	}
	m.revision++
	v.ETag = fmt.Sprint(m.revision)
	v.Modified = time.Now()
	m.values[v.Name] = v
	return v, nil
}
func processorStoreForTest(owner string, io checkpointRecordIO) *fencedCheckpointStore {
	return &fencedCheckpointStore{records: io, namespace: "test.servicebus.windows.net", hub: "events", group: "workers", owner: owner, partitions: map[string]bool{"0": true}}
}
func claimProcessorForTest(t *testing.T, s *fencedCheckpointStore) {
	t.Helper()
	current, err := s.ListOwnership(context.Background(), s.namespace, s.hub, s.group, nil)
	if err != nil {
		t.Fatal(err)
	}
	claim := eh.Ownership{FullyQualifiedNamespace: s.namespace, EventHubName: s.hub, ConsumerGroup: s.group, PartitionID: "0", OwnerID: s.owner}
	if len(current) > 0 {
		claim.ETag = current[0].ETag
	}
	claims, err := s.ClaimOwnership(context.Background(), []eh.Ownership{claim}, nil)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim %+v %v", claims, err)
	}
}
func TestProcessorCheckpointRestartAndABAFence(t *testing.T) {
	io := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	a := processorStoreForTest("a", io)
	claimProcessorForTest(t, a)
	fence, err := a.ownershipFence(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	old := context.WithValue(context.Background(), checkpointFenceContextKey{}, fence)
	seq := int64(4)
	offset := "4"
	cp := eh.Checkpoint{FullyQualifiedNamespace: a.namespace, EventHubName: a.hub, ConsumerGroup: a.group, PartitionID: "0", SequenceNumber: &seq, Offset: &offset}
	if err := a.SetCheckpoint(old, cp, nil); err != nil {
		t.Fatal(err)
	}
	b := processorStoreForTest("b", io)
	claimProcessorForTest(t, b)
	seq = 5
	offset = "5"
	if err := a.SetCheckpoint(old, cp, nil); !errors.Is(err, errCheckpointFence) {
		t.Fatal("stale owner advanced checkpoint", err)
	}
	claimProcessorForTest(t, a)
	if err := a.SetCheckpoint(old, cp, nil); !errors.Is(err, errCheckpointFence) {
		t.Fatal("ABA ownership bypassed incarnation fence", err)
	}
	restarted := processorStoreForTest("new-process", io)
	checkpoints, err := restarted.ListCheckpoints(context.Background(), a.namespace, a.hub, a.group, nil)
	if err != nil || len(checkpoints) != 1 || *checkpoints[0].SequenceNumber != 4 {
		t.Fatalf("restart checkpoint %+v %v", checkpoints, err)
	}
	cp.ConsumerGroup = "other"
	if err := a.SetCheckpoint(old, cp, nil); err == nil {
		t.Fatal("cross-group checkpoint accepted")
	}
}

type processorPartFixture struct {
	store    *fencedCheckpointStore
	received bool
	failOnce bool
}

func (p *processorPartFixture) ReceiveEvents(ctx context.Context, _ int, _ *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	if p.received {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p.received = true
	return []*eh.ReceivedEventData{{EventData: eh.EventData{Body: []byte("event")}, SequenceNumber: 4, Offset: "4"}}, nil
}
func (p *processorPartFixture) UpdateCheckpoint(ctx context.Context, rd *eh.ReceivedEventData, _ *eh.UpdateCheckpointOptions) error {
	if p.failOnce {
		p.failOnce = false
		return errors.New("storage unavailable")
	}
	seq := rd.SequenceNumber
	offset := rd.Offset
	return p.store.SetCheckpoint(ctx, eh.Checkpoint{FullyQualifiedNamespace: p.store.namespace, EventHubName: p.store.hub, ConsumerGroup: p.store.group, PartitionID: "0", SequenceNumber: &seq, Offset: &offset}, nil)
}
func (*processorPartFixture) PartitionID() string         { return "0" }
func (*processorPartFixture) Close(context.Context) error { return nil }
func TestProcessorWorkerRejectFailureAndDurableDelivery(t *testing.T) {
	io := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	store := processorStoreForTest("worker", io)
	claimProcessorForTest(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &eventHubsProcessor{ctx: ctx, cancel: cancel, deliveries: make(chan event), failure: make(chan error, 1), store: store}
	done := make(chan struct{})
	go func() {
		defer close(done)
		session.partitionWorker(&processorPartFixture{store: store, failOnce: true})
	}()
	next := func() event {
		t.Helper()
		select {
		case ev := <-session.deliveries:
			return ev
		case <-time.After(time.Second):
			t.Fatal("delivery stalled")
			return event{}
		}
	}
	next().receipt.reject()
	ev := next()
	if err := ev.receipt.finish(ctx); err == nil {
		t.Fatal("failed checkpoint reported success")
	}
	ev = next()
	if string(ev.Body) != "event" || ev.SequenceNum != 4 {
		t.Fatal("failed delivery was skipped")
	}
	if err := ev.receipt.finish(ctx); err != nil {
		t.Fatal(err)
	}
	cps, err := store.ListCheckpoints(ctx, store.namespace, store.hub, store.group, nil)
	if err != nil || len(cps) != 1 || *cps[0].SequenceNumber != 4 {
		t.Fatal("checkpoint not durable", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker leaked after cancellation")
	}
}

func TestProcessorPropertiesAreBoundedScalars(t *testing.T) {
	if out, err := processorProperties(map[string]any{"number": int64(4), "text": "value"}, 10); err != nil || out["number"] != "4" {
		t.Fatal(out, err)
	}
	for _, values := range []map[string]any{{"nested": map[string]string{"a": "b"}}, {"large": strings.Repeat("a", (64<<10)+1)}, {strings.Repeat("k", 257): "a"}} {
		if _, err := processorProperties(values, 0); err == nil {
			t.Fatal("unbounded property accepted")
		}
	}
	if _, err := processorProperties(map[string]any{"text": "a"}, maxProcessorEventBytes); err == nil {
		t.Fatal("retained byte budget ignored")
	}
}

func TestCheckpointRequiresPartitionAndStableSequenceOffset(t *testing.T) {
	io := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	s := processorStoreForTest("worker", io)
	claimProcessorForTest(t, s)
	fence, err := s.ownershipFence(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), checkpointFenceContextKey{}, fence)
	seq, offset := int64(4), "4"
	cp := eh.Checkpoint{FullyQualifiedNamespace: s.namespace, EventHubName: s.hub, ConsumerGroup: s.group, PartitionID: "0", SequenceNumber: &seq, Offset: &offset}
	if err := s.SetCheckpoint(ctx, cp, nil); err != nil {
		t.Fatal(err)
	}
	offset = "5"
	if err := s.SetCheckpoint(ctx, cp, nil); err == nil {
		t.Fatal("same sequence changed offset")
	}
	cp.PartitionID = ""
	if err := s.SetCheckpoint(ctx, cp, nil); err == nil {
		t.Fatal("empty partition accepted")
	}
	if _, err := s.ClaimOwnership(ctx, []eh.Ownership{{FullyQualifiedNamespace: s.namespace, EventHubName: s.hub, ConsumerGroup: s.group, OwnerID: s.owner}}, nil); err == nil {
		t.Fatal("empty ownership partition accepted")
	}
}

type failingOwnershipIO struct{ checkpointRecordIO }

func (failingOwnershipIO) Read(context.Context, string) (partitionCheckpointRecord, error) {
	return partitionCheckpointRecord{}, errors.New("storage unavailable")
}
func TestProcessorOwnershipVerificationFailureIsVisible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := processorStoreForTest("worker", failingOwnershipIO{})
	session := &eventHubsProcessor{ctx: ctx, cancel: cancel, failure: make(chan error, 1), store: store}
	session.partitionWorker(&processorPartFixture{})
	select {
	case err := <-session.failure:
		if !strings.Contains(err.Error(), "ownership verification") {
			t.Fatal(err)
		}
	default:
		t.Fatal("storage failure was silent")
	}
}

func TestProcessorInitKeepsPreparationTrafficFree(t *testing.T) {
	t.Setenv("APEXLOAD_EVENTHUBS_CHECKPOINT_CONTAINERS", "https://account.blob.core.windows.net/checkpoints")
	e := &EventHubs{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := e.Init(ctx, map[string]any{"eventhubs_consumer_mode": "processor", "eventhubs_checkpoint_container_url": "https://account.blob.core.windows.net/checkpoints", "eventhubs_auth_type": "aad", "eventhubs_namespace": "approved.servicebus.windows.net", "eventhubs_event_hub": "events", "eventhubs_consumer_group": "workers", "eventhubs_mock": false})
	if err != nil {
		t.Fatal(err)
	}
	c := e.client.(*realClient)
	if c.processor != nil || c.cred != nil || len(c.consumers) != 0 || len(c.producers) != 0 || len(c.partitions) != 0 {
		t.Fatal("Init created network clients or credentials")
	}
	if err := e.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
}

type processorCleanupFixture struct {
	processorPartFixture
	entered  chan struct{}
	resume   chan struct{}
	closeErr error
}

func (p *processorCleanupFixture) Close(context.Context) error {
	if p.entered != nil {
		close(p.entered)
	}
	if p.resume != nil {
		<-p.resume
	}
	return p.closeErr
}
func TestProcessorCleanupDeadlineRetainsChargeUntilActualClose(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 1, 8)
	_ = r.Adopt(context.Background())
	child, _ := r.acquireProcessor(context.Background())
	worker, _ := child.acquireWorker("0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &eventHubsProcessor{ctx: ctx, cancel: cancel, done: make(chan struct{}), failure: make(chan error, 1)}
	partition := &processorCleanupFixture{entered: make(chan struct{}), resume: make(chan struct{})}
	go func() { defer session.finishProcessor(child, false); session.closePartition(partition, worker, nil) }()
	<-partition.entered
	c := &realClient{processor: session, closeTimeout: 10 * time.Millisecond}
	if err := c.Teardown(context.Background()); err == nil || !strings.Contains(err.Error(), "cleanup deadline") {
		t.Fatal(err)
	}
	r.Release()
	if capacity := p.snapshot(); !capacity.Valid() || capacity.ReservedProcessors != 1 || capacity.ReservedWorkers != 8 || capacity.DrainingProcessors != 1 {
		t.Fatal("timeout freed live processor", capacity)
	}
	if err := c.Teardown(context.Background()); err == nil {
		t.Fatal("repeated teardown hid pending cleanup")
	}
	close(partition.resume)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("close never completed")
	}
	if err := c.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if capacity := p.snapshot(); capacity.ReservedProcessors != 0 || capacity.ReservedWorkers != 0 {
		t.Fatal(capacity)
	}
}
func TestProcessorUnprovenCloseRemainsDrainingAndVisible(t *testing.T) {
	for _, kind := range []string{"worker", "rejected SDK partition"} {
		t.Run(kind, func(t *testing.T) {
			p := testProcessorPool(t, processorLimits{4, 32, 32})
			r := reserveProcessorTest(t, p, 1, 8)
			_ = r.Adopt(context.Background())
			child, _ := r.acquireProcessor(context.Background())
			var worker *processorWorkerLease
			var rejected *processorLease
			if kind == "worker" {
				worker, _ = child.acquireWorker("0")
			} else {
				rejected = child
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session := &eventHubsProcessor{ctx: ctx, cancel: cancel, done: make(chan struct{}), failure: make(chan error, 1)}
			session.closePartition(&processorCleanupFixture{closeErr: errors.New("secret-provider-token")}, worker, rejected)
			session.finishProcessor(child, true)
			c := &realClient{processor: session}
			if err := c.Teardown(context.Background()); err == nil || !strings.Contains(err.Error(), "cleanup incomplete") || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
			if err := c.Teardown(context.Background()); err == nil {
				t.Fatal("repeat teardown hid unproven cleanup")
			}
			if capacity := p.snapshot(); !capacity.Valid() || capacity.ReservedProcessors != 1 || capacity.DrainingProcessors != 1 || capacity.ReservedWorkers != 8 {
				t.Fatal(capacity)
			}
		})
	}
}
func TestProcessorActualPartitionInventoryCannotExceedClaim(t *testing.T) {
	for _, ids := range [][]string{nil, {"0", "1", "2"}, {"0", "0"}, {""}, {strings.Repeat("a", 65)}} {
		if _, err := validateProcessorPartitionInventory(ids, 2); err == nil {
			t.Fatal("inventory accepted", ids)
		}
	}
	if ids, err := validateProcessorPartitionInventory([]string{"0", "1"}, 2); err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
}
func processorInitConfigForReservationTest() map[string]any {
	return map[string]any{
		"eventhubs_consumer_mode": "processor", "eventhubs_checkpoint_container_url": "https://account.blob.core.windows.net/checkpoints", "eventhubs_auth_type": "aad", "eventhubs_namespace": "approved.servicebus.windows.net", "eventhubs_event_hub": "events", "eventhubs_consumer_group": "workers", "eventhubs_mock": false, "eventhubs_processor_max_partitions": 2,
	}
}
func TestProcessorBoundInitRemainsLazyAndFailsClosed(t *testing.T) {
	t.Setenv("APEXLOAD_EVENTHUBS_CHECKPOINT_CONTAINERS", "https://account.blob.core.windows.net/checkpoints")
	pool, err := defaultProcessorPool()
	if err != nil {
		t.Fatal(err)
	}
	r := reserveProcessorTest(t, pool, 1, 2)
	ctx := protocol.WithRuntimeResourceReservation(context.Background(), eventHubsProtocolName, r)
	e := &EventHubs{}
	if err = e.Init(ctx, processorInitConfigForReservationTest()); err != nil {
		t.Fatal(err)
	}
	c := e.client.(*realClient)
	if c.cfg.processorReservation != r || c.cred != nil || len(c.consumers) != 0 || c.processor != nil {
		t.Fatal("Init consumed/initialized stage")
	}
	if copied := e.clientFactory().(*realClient); copied.cfg.processorReservation != r {
		t.Fatal("fresh client lost parent")
	}
	if _, err = c.startProcessor(ctx); err == nil {
		t.Fatal("unadopted stage started")
	}
	if c.cred != nil || len(c.consumers) != 0 {
		t.Fatal("stage rejection contacted target/credentials")
	}
	if err = e.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	foreign := reserveProcessorTest(t, testProcessorPool(t, processorLimits{4, 32, 32}), 1, 2)
	for _, bad := range []context.Context{
		protocol.WithRuntimeResourceReservation(context.Background(), eventHubsProtocolName, nil),
		protocol.WithRuntimeResourceReservation(context.Background(), "other", r),
		protocol.WithRuntimeResourceReservation(context.Background(), eventHubsProtocolName, foreign),
	} {
		if err := (&EventHubs{}).Init(bad, processorInitConfigForReservationTest()); err == nil {
			t.Fatal("invalid binding fell back to unmanaged pool")
		}
	}
	wrong := reserveProcessorTest(t, pool, 1, 1)
	if err := (&EventHubs{}).Init(protocol.WithRuntimeResourceReservation(context.Background(), eventHubsProtocolName, wrong), processorInitConfigForReservationTest()); err == nil {
		t.Fatal("ceiling mismatch accepted")
	}
	r.Release()
	if err := (&EventHubs{}).Init(ctx, processorInitConfigForReservationTest()); err == nil {
		t.Fatal("released binding accepted")
	}
}
func TestProcessorDirectRuntimeReservesBeforeTargetRequests(t *testing.T) {
	pool, err := defaultProcessorPool()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{ProcessorMaxPartitions: 2}
	child, owned, err := acquireProcessorResources(context.Background(), cfg)
	if err != nil || !owned || child == nil {
		t.Fatal(child, owned, err)
	}
	if capacity := pool.snapshot(); capacity.ReservedProcessors < 1 || capacity.ReservedWorkers < 2 {
		t.Fatal(capacity)
	}
	child.release()
	child.parent.Release()
	r := reserveProcessorTest(t, pool, 1, 2)
	_ = r.Adopt(context.Background())
	cfg.processorReservationBound = true
	cfg.processorReservation = r
	before := pool.snapshot()
	child, owned, err = acquireProcessorResources(context.Background(), cfg)
	if err != nil || owned {
		t.Fatal(err, owned)
	}
	after := pool.snapshot()
	if after.ReservedProcessors != before.ReservedProcessors || after.ReservedWorkers != before.ReservedWorkers {
		t.Fatal("child activation double charged", before, after)
	}
	child.release()
	for _, ctx := range []context.Context{protocol.WithRuntimeResourceReservation(context.Background(), eventHubsProtocolName, nil), protocol.WithRuntimeResourceReservation(context.Background(), "other", r)} {
		c := &realClient{cfg: config{Lifetime: ctx, ProcessorMaxPartitions: 2}}
		if _, err := c.startProcessor(ctx); err == nil {
			t.Fatal("invalid action context silently reserved")
		}
		if c.cred != nil || len(c.consumers) != 0 {
			t.Fatal("rejected reservation touched target")
		}
	}
}

func TestProcessorDispatcherDrainsCancelledButQueuedSDKPartitions(t *testing.T) {
	for _, failClose := range []bool{false, true} {
		t.Run(fmt.Sprint(failClose), func(t *testing.T) {
			p := testProcessorPool(t, processorLimits{4, 32, 32})
			r := reserveProcessorTest(t, p, 1, 2)
			_ = r.Adopt(context.Background())
			child, _ := r.acquireProcessor(context.Background())
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			session := &eventHubsProcessor{ctx: ctx, cancel: cancel, done: make(chan struct{}), failure: make(chan error, 1)}
			first := &processorCleanupFixture{entered: make(chan struct{})}
			second := &processorCleanupFixture{entered: make(chan struct{})}
			if failClose {
				first.closeErr = context.DeadlineExceeded
			}
			queued := []processorPartition{first, second}
			calls := 0
			var workers sync.WaitGroup
			session.dispatchPartitions(child, &workers, func(nextCtx context.Context) processorPartition {
				if nextCtx.Err() != nil {
					t.Fatal("cancelled dispatcher would drop SDK queue")
				}
				calls++
				if len(queued) == 0 {
					return nil
				}
				v := queued[0]
				queued = queued[1:]
				return v
			})
			workers.Wait()
			session.finishProcessor(child, true)
			if calls != 3 {
				t.Fatal("queue not drained", calls)
			}
			for _, part := range []*processorCleanupFixture{first, second} {
				select {
				case <-part.entered:
				default:
					t.Fatal("queued partition was not closed")
				}
				if part.received {
					t.Fatal("cancelled drain polled target")
				}
			}
			c := p.snapshot()
			if failClose {
				if c.ReservedProcessors != 1 || c.DrainingProcessors != 1 || session.cleanupError() == nil {
					t.Fatal("unproven queued close was freed", c)
				}
			} else if c.ReservedProcessors != 0 || c.ReservedWorkers != 0 {
				t.Fatal(c)
			}
		})
	}
}

func TestProcessorDonePublishesFreedChildBeforePhaseHandoff(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 1, 2)
	_ = r.Adopt(context.Background())
	for i := 0; i < 100; i++ {
		child, err := r.acquireProcessor(context.Background())
		if err != nil {
			t.Fatal("phase could not acquire entitlement", err)
		}
		session := &eventHubsProcessor{done: make(chan struct{})}
		go session.finishProcessor(child, false)
		<-session.done
		if c := p.snapshot(); c.ActiveProcessors != 0 || c.ReservedProcessors != 1 || c.ReservedWorkers != 2 {
			t.Fatal("done preceded child accounting", c)
		}
	}
}

func TestProcessorOwnedTransportCloseKeepsChargeUntilProvenExit(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 1, 2)
	_ = r.Adopt(context.Background())
	child, _ := r.acquireProcessor(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, resume := make(chan struct{}), make(chan struct{})
	session := &eventHubsProcessor{ctx: ctx, cancel: cancel, done: make(chan struct{}), failure: make(chan error, 1), closeTransport: func(context.Context) error { close(entered); <-resume; return nil }}
	go session.finishProcessor(child, true)
	<-entered
	c := &realClient{processor: session, closeTimeout: 10 * time.Millisecond}
	if err := c.Teardown(context.Background()); err == nil || !strings.Contains(err.Error(), "cleanup deadline") {
		t.Fatal(err)
	}
	select {
	case <-session.done:
		t.Fatal("done published before transport goroutines exited")
	default:
	}
	if capacity := p.snapshot(); capacity.ReservedProcessors != 1 || capacity.DrainingProcessors != 1 || capacity.ReservedWorkers != 2 {
		t.Fatal("blocked connection close was not charged/draining before parent release", capacity)
	}
	r.Release()
	close(resume)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("transport close never finished")
	}
	if capacity := p.snapshot(); capacity.ReservedProcessors != 0 || capacity.ReservedWorkers != 0 {
		t.Fatal("done preceded transport accounting", capacity)
	}
	if err := c.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestProcessorFailedOwnedTransportCloseQuarantinesChild(t *testing.T) {
	p := testProcessorPool(t, processorLimits{4, 32, 32})
	r := reserveProcessorTest(t, p, 1, 2)
	_ = r.Adopt(context.Background())
	child, _ := r.acquireProcessor(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &eventHubsProcessor{ctx: ctx, cancel: cancel, done: make(chan struct{}), failure: make(chan error, 1), closeTransport: func(context.Context) error { return errors.New("secret-provider-token") }}
	session.finishProcessor(child, true)
	if capacity := p.snapshot(); !capacity.Valid() || capacity.ReservedProcessors != 1 || capacity.DrainingProcessors != 1 || capacity.ReservedWorkers != 2 {
		t.Fatal("failed transport close freed entitlement", capacity)
	}
	c := &realClient{processor: session}
	if err := c.Teardown(context.Background()); err == nil || !strings.Contains(err.Error(), "transport cleanup incomplete") || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}

func TestProcessorFailedStartupRetainsOwnedTransportUntilCleanup(t *testing.T) {
	for _, failClose := range []bool{false, true} {
		t.Run(fmt.Sprint(failClose), func(t *testing.T) {
			p := testProcessorPool(t, processorLimits{4, 32, 32})
			r := reserveProcessorTest(t, p, 1, 2)
			_ = r.Adopt(context.Background())
			child, _ := r.acquireProcessor(context.Background())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, resume := make(chan struct{}), make(chan struct{})
			consumer := &eh.ConsumerClient{} // Close would panic; only the session callback owns it.
			session := &eventHubsProcessor{ctx: ctx, cancel: cancel, done: make(chan struct{}), failure: make(chan error, 1), consumer: consumer, closeTransport: func(context.Context) error {
				close(entered)
				<-resume
				if failClose {
					return context.DeadlineExceeded
				}
				return nil
			}}
			c := &realClient{closeTimeout: 10 * time.Millisecond, consumers: map[string]*eh.ConsumerClient{"owned": consumer}}
			c.abortProcessorStart(session, child, true)
			<-entered
			if c.processor != session {
				t.Fatal("failed startup hid pending cleanup")
			}
			if err := c.Teardown(context.Background()); err == nil || !strings.Contains(err.Error(), "cleanup deadline") {
				t.Fatal("duplicate owned close escaped deadline", err)
			}
			if capacity := p.snapshot(); capacity.ReservedProcessors != 1 || capacity.DrainingProcessors != 1 || capacity.ReservedWorkers != 2 {
				t.Fatal("failed startup freed active transport or hid its drain", capacity)
			}
			close(resume)
			select {
			case <-session.done:
			case <-time.After(time.Second):
				t.Fatal("startup close never finished")
			}
			capacity := p.snapshot()
			if failClose {
				if capacity.ReservedProcessors != 1 || capacity.DrainingProcessors != 1 || session.cleanupError() == nil {
					t.Fatal("failed startup close freed charge", capacity)
				}
			} else if capacity.ReservedProcessors != 0 || capacity.ReservedWorkers != 0 {
				t.Fatal(capacity)
			}
		})
	}
}
func TestProcessorConsumerTransfersBeforeAnyStartupRequests(t *testing.T) {
	cc := &eh.ConsumerClient{}
	c := &realClient{cfg: config{Lifetime: context.Background(), EventHub: "events", ConsumerGroup: "workers"}, consumers: map[string]*eh.ConsumerClient{"events\x00workers": cc}}
	session := c.ownProcessorConsumer(cc)
	defer session.cancel()
	if session.consumer != cc || session.closeTransport == nil || len(c.consumers) != 0 {
		t.Fatal("exclusive transport stayed in ordinary teardown map")
	}
	// Do not call SDK Close on this constructor-free fixture: no target has been initialized.
}

func TestProcessorLifecycleGateWaitUsesCallerBudget(t *testing.T) {
	var gate processorLifecycleGate
	if err := gate.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := gate.Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("blocked gate ignored caller deadline", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("gate exceeded caller budget")
	}
	gate.Unlock()
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := gate.Lock(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled caller consumed gate", err)
	}
	if err := gate.Lock(context.Background()); err != nil {
		t.Fatal("gate did not remain reusable", err)
	}
	gate.Unlock()
}

// Use the real lazy SDK constructor, but isolate its external properties call.
// It creates no connection or target request; native Close is safe on this client.
func processorStartupClientForTest(t *testing.T) *realClient {
	t.Helper()
	cc, err := eh.NewConsumerClientFromConnectionString("Endpoint=sb://offline.servicebus.windows.net/;SharedAccessKeyName=test;SharedAccessKey=YQ==", "events", "workers", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &realClient{
		cfg:          config{Lifetime: context.Background(), ConsumerMode: "processor", ProcessorMaxPartitions: 2, EventHub: "events", ConsumerGroup: "workers"},
		consumers:    map[string]*eh.ConsumerClient{"events\x00workers": cc},
		closeTimeout: 10 * time.Millisecond,
	}
}
func awaitFailedProcessorCleanupForTest(t *testing.T, c *realClient) *eventHubsProcessor {
	t.Helper()
	if err := c.processorMu.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	session := c.processor
	c.processorMu.Unlock()
	if session == nil {
		t.Fatal("failed startup did not publish its cleanup session")
	}
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("failed startup did not finish its owned transport cleanup")
	}
	return session
}
func TestProcessorReceiveBudgetIncludesStartupProperties(t *testing.T) {
	for _, callerCancellation := range []bool{false, true} {
		t.Run(fmt.Sprint(callerCancellation), func(t *testing.T) {
			pool, err := defaultProcessorPool()
			if err != nil {
				t.Fatal(err)
			}
			before := pool.snapshot()
			c := processorStartupClientForTest(t)
			entered := make(chan struct{})
			c.processorProperties = func(ctx context.Context, _ *eh.ConsumerClient) (eh.EventHubProperties, error) {
				close(entered)
				<-ctx.Done()
				return eh.EventHubProperties{}, ctx.Err()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			started := time.Now()
			go func() { _, _, err := c.Receive(ctx, "events", "workers", 30*time.Millisecond); result <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("startup did not query properties")
			}
			if callerCancellation {
				cancel()
			}
			select {
			case err := <-result:
				want := errNoEvent
				if callerCancellation {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatal("startup did not observe receive/caller budget", err)
				}
			case <-time.After(time.Second):
				t.Fatal("startup exceeded receive budget")
			}
			if time.Since(started) > time.Second {
				t.Fatal("startup exceeded receive budget")
			}
			session := awaitFailedProcessorCleanupForTest(t, c)
			if session.store != nil || c.cred != nil {
				t.Fatal("expired startup initialized execution or credentials")
			}
			after := pool.snapshot()
			if after.ReservedProcessors != before.ReservedProcessors || after.ReservedWorkers != before.ReservedWorkers {
				t.Fatal("failed startup leaked capacity", before, after)
			}
			if err := c.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestProcessorTeardownCancelsBlockedStartupAndRejectsLateCompletion(t *testing.T) {
	pool, err := defaultProcessorPool()
	if err != nil {
		t.Fatal(err)
	}
	before := pool.snapshot()
	c := processorStartupClientForTest(t)
	entered, resume := make(chan struct{}), make(chan struct{})
	var requestContext context.Context
	c.processorProperties = func(ctx context.Context, _ *eh.ConsumerClient) (eh.EventHubProperties, error) {
		requestContext = ctx
		close(entered)
		<-resume // Simulate a provider call that returns a successful result late.
		return eh.EventHubProperties{PartitionIDs: []string{"0"}}, nil
	}
	first := make(chan error, 1)
	go func() { _, _, err := c.Receive(context.Background(), "events", "workers", time.Second); first <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("startup did not enter properties")
	}
	started := time.Now()
	_, _, err = c.Receive(context.Background(), "events", "workers", 10*time.Millisecond)
	if !errors.Is(err, errNoEvent) || time.Since(started) > time.Second {
		t.Fatal("second receive exceeded gate budget", err)
	}
	started = time.Now()
	if err := c.Teardown(context.Background()); err == nil || !strings.Contains(err.Error(), "cleanup deadline") {
		t.Fatal("teardown did not bound the startup gate", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("teardown exceeded its cleanup budget")
	}
	select {
	case <-requestContext.Done():
	default:
		t.Fatal("teardown did not publish startup cancellation before waiting for the gate")
	}
	if current := pool.snapshot(); current.ReservedProcessors != before.ReservedProcessors+1 || current.ReservedWorkers != before.ReservedWorkers+2 {
		t.Fatal("pending startup was freed before transport proof", before, current)
	}
	close(resume)
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("late inventory succeeded after teardown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("late startup did not terminate")
	}
	session := awaitFailedProcessorCleanupForTest(t, c)
	if session.store != nil || c.cred != nil {
		t.Fatal("late startup constructed an execution runtime after teardown")
	}
	if after := pool.snapshot(); after.ReservedProcessors != before.ReservedProcessors || after.ReservedWorkers != before.ReservedWorkers {
		t.Fatal("late startup retained its closed transport", before, after)
	}
	if err := c.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProcessorTeardownSnapshotCannotCloseLateTransferredConsumer(t *testing.T) {
	for _, transferAfterDeadline := range []bool{false, true} {
		t.Run(fmt.Sprint(transferAfterDeadline), func(t *testing.T) {
			p := testProcessorPool(t, processorLimits{4, 32, 32})
			r := reserveProcessorTest(t, p, 1, 2)
			_ = r.Adopt(context.Background())
			child, _ := r.acquireProcessor(context.Background())
			cc := &eh.ConsumerClient{} // Ordinary Close would panic; only the session callback owns it.
			c := &realClient{cfg: config{Lifetime: context.Background(), ConsumerMode: "processor", EventHub: "events", ConsumerGroup: "workers"}, consumers: map[string]*eh.ConsumerClient{"events\x00workers": cc}, closeTimeout: 30 * time.Millisecond}
			if err := c.processorMu.Lock(context.Background()); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- c.Teardown(context.Background()) }()
			deadline := time.After(time.Second)
			for {
				c.mu.Lock()
				closed := c.closed
				c.mu.Unlock()
				if closed {
					break
				}
				select {
				case <-deadline:
					t.Fatal("teardown did not close startup admission")
				case <-time.After(time.Millisecond):
				}
			}
			awaitTeardown := func() {
				t.Helper()
				select {
				case err := <-result:
					if err == nil || !strings.Contains(err.Error(), "cleanup deadline") {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("ordinary consumer close defeated teardown budget")
				}
			}
			if transferAfterDeadline {
				awaitTeardown()
			}
			session := c.ownProcessorConsumer(cc)
			if session.ctx.Err() == nil {
				t.Fatal("late startup escaped closed lifetime")
			}
			entered, resume := make(chan struct{}), make(chan struct{})
			session.closeTransport = func(context.Context) error { close(entered); <-resume; return nil }
			c.abortProcessorStart(session, child, true)
			<-entered
			if !transferAfterDeadline {
				awaitTeardown()
			}
			if capacity := p.snapshot(); capacity.ReservedProcessors != 1 || capacity.DrainingProcessors != 1 {
				t.Fatal("late transport close was freed/hidden", capacity)
			}
			c.processorMu.Unlock()
			close(resume)
			select {
			case <-session.done:
			case <-time.After(time.Second):
				t.Fatal("late owned close did not finish")
			}
			if capacity := p.snapshot(); capacity.ReservedProcessors != 0 || capacity.ReservedWorkers != 0 {
				t.Fatal("late transport retained charge after proof", capacity)
			}
			if err := c.Teardown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
