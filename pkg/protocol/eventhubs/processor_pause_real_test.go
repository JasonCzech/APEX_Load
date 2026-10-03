//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/Azure/go-amqp"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// External admission boundary fixture. Engine tests exercise its concrete
// coordinator; these tests count actual provider calls and durable writes.
type pauseFixtureKey struct{}
type pauseFixtureGate struct {
	mu      sync.Mutex
	paused  bool
	changed chan struct{}
	leases  map[*pauseFixtureLease]bool
}
type pauseFixtureLease struct {
	gate   *pauseFixtureGate
	kind   protocol.RuntimePauseActivity
	parent *pauseFixtureLease
	ctx    context.Context
	cancel context.CancelCauseFunc
}

func newPauseFixtureGate(paused bool) *pauseFixtureGate {
	return &pauseFixtureGate{paused: paused, changed: make(chan struct{}), leases: map[*pauseFixtureLease]bool{}}
}
func (g *pauseFixtureGate) notify() { close(g.changed); g.changed = make(chan struct{}) }
func (g *pauseFixtureGate) Admit(ctx context.Context, kind protocol.RuntimePauseActivity) (protocol.RuntimePauseLease, error) {
	for {
		g.mu.Lock()
		parent, _ := ctx.Value(pauseFixtureKey{}).(*pauseFixtureLease)
		if ctx.Err() != nil {
			g.mu.Unlock()
			return nil, ctx.Err()
		}
		if parent != nil && (!g.leases[parent] || parent.gate != g) {
			g.mu.Unlock()
			return nil, protocol.ErrRuntimePauseAdmissionInvalid
		}
		if kind == protocol.RuntimePauseCheckpoint && parent == nil {
			g.mu.Unlock()
			return nil, protocol.ErrRuntimePauseAdmissionInvalid
		}
		if kind == protocol.RuntimePauseAction && parent != nil {
			g.mu.Unlock()
			return &pauseFixtureLease{ctx: ctx}, nil
		}
		if g.paused && (parent == nil || kind == protocol.RuntimePauseTail) {
			wake := g.changed
			g.mu.Unlock()
			select {
			case <-wake:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		child, cancel := context.WithCancelCause(ctx)
		l := &pauseFixtureLease{gate: g, kind: kind, parent: parent, ctx: child, cancel: cancel}
		if kind == protocol.RuntimePauseAction {
			l.ctx = context.WithValue(child, pauseFixtureKey{}, l)
		}
		g.leases[l] = true
		g.notify()
		g.mu.Unlock()
		return l, nil
	}
}
func (l *pauseFixtureLease) Context() context.Context { return l.ctx }
func (l *pauseFixtureLease) Release() {
	if l.gate == nil {
		return
	}
	g := l.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.leases[l] {
		return
	}
	delete(g.leases, l)
	if l.kind == protocol.RuntimePauseAction {
		for child := range g.leases {
			if child.parent == l && child.kind == protocol.RuntimePauseReceive {
				child.cancel(protocol.ErrRuntimePauseSuspended)
			}
		}
	}
	l.cancel(context.Canceled)
	g.notify()
}
func (g *pauseFixtureGate) latch() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paused = true
	for l := range g.leases {
		if l.kind == protocol.RuntimePauseTail || l.kind == protocol.RuntimePauseReceive && l.parent == nil {
			l.cancel(protocol.ErrRuntimePauseSuspended)
		}
	}
	g.notify()
}
func (g *pauseFixtureGate) resume() { g.mu.Lock(); defer g.mu.Unlock(); g.paused = false; g.notify() }
func (g *pauseFixtureGate) count(kind protocol.RuntimePauseActivity) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for l := range g.leases {
		if kind == "" || l.kind == kind {
			n++
		}
	}
	return n
}
func waitProcessor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("provider fixture did not reach required state")
		}
		time.Sleep(time.Millisecond)
	}
}
func newPauseSession(t *testing.T, gate *pauseFixtureGate, store *fencedCheckpointStore) *eventHubsProcessor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	store.pauseGate = gate
	return &eventHubsProcessor{pauseGate: gate, ctx: ctx, cancel: cancel, done: make(chan struct{}), deliveries: make(chan event), failure: make(chan error, 1), store: store, progress: store.progress}
}

type pausedPartitionFixture struct {
	store         *fencedCheckpointStore
	reads, closes atomic.Int32
	got           chan struct{}
	resume        chan struct{}
	ignoredCancel bool
}

func (p *pausedPartitionFixture) PartitionID() string         { return "0" }
func (p *pausedPartitionFixture) Close(context.Context) error { p.closes.Add(1); return nil }
func (p *pausedPartitionFixture) ReceiveEvents(ctx context.Context, _ int, _ *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	n := p.reads.Add(1)
	if n == 1 {
		if p.got != nil {
			close(p.got)
		}
		if p.resume != nil {
			if p.ignoredCancel {
				<-p.resume
			} else {
				select {
				case <-p.resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		return []*eh.ReceivedEventData{{EventData: eh.EventData{Body: []byte("event")}, SequenceNumber: 7, Offset: "opaque:7"}}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *pausedPartitionFixture) UpdateCheckpoint(ctx context.Context, event *eh.ReceivedEventData, _ *eh.UpdateCheckpointOptions) error {
	return p.store.SetCheckpoint(ctx, checkpointFixture(p.store, event.SequenceNumber, event.Offset), nil)
}

func TestProcessorPauseRetainsUndeliveredEventUntilNewAction(t *testing.T) {
	store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
	g := newPauseFixtureGate(false)
	s := newPauseSession(t, g, store)
	part := &pausedPartitionFixture{store: store, got: make(chan struct{}), resume: make(chan struct{}), ignoredCancel: true}
	done := make(chan struct{})
	go func() { defer close(done); s.partitionWorkerContext(part, nil, s.ctx, fence) }()
	action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	release, err := s.registerDemand(action.Context())
	if err != nil {
		t.Fatal(err)
	}
	<-part.got
	release()
	action.Release()
	g.latch()
	if g.count(protocol.RuntimePauseReceive) != 1 {
		t.Fatal("ignored-cancel SDK call lost its proof charge")
	}
	close(part.resume)
	waitProcessor(t, func() bool { return g.count("") == 0 })
	select {
	case <-s.deliveries:
		t.Fatal("undelivered event escaped paused gate")
	case <-time.After(20 * time.Millisecond):
	}
	if part.reads.Load() != 1 {
		t.Fatal("paused processor started extra reads")
	}
	g.resume()
	next, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	defer next.Release()
	releaseNext, _ := s.registerDemand(next.Context())
	defer releaseNext()
	var ev event
	select {
	case ev = <-s.deliveries:
	case <-time.After(time.Second):
		t.Fatal("retained event was not delivered on resume")
	}
	if ev.SequenceNum != 7 || part.reads.Load() != 1 {
		t.Fatal("retained event changed or re-read", ev, part.reads.Load())
	}
	if err := ev.receipt.finish(next.Context()); err != nil {
		t.Fatal(err)
	}
	current, _ := store.records.Read(context.Background(), store.prefix()+"0")
	if current.Sequence == nil || *current.Sequence != 7 || current.Offset != "opaque:7" {
		t.Fatal(current)
	}
	releaseNext()
	next.Release()
	s.cancel()
	<-done
}
func TestProcessorPauseDrainsExistingActionAndCheckpoint(t *testing.T) {
	delayed := &delayedCheckpointIO{checkpointRecordIO: &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}, entered: make(chan struct{}), resume: make(chan struct{})}
	store, p, fence := trackedStoreForTest(t, delayed)
	g := newPauseFixtureGate(false)
	s := newPauseSession(t, g, store)
	part := &pausedPartitionFixture{store: store}
	done := make(chan struct{})
	go func() { defer close(done); s.partitionWorkerContext(part, nil, s.ctx, fence) }()
	action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	release, _ := s.registerDemand(action.Context())
	g.latch()
	var ev event
	select {
	case ev = <-s.deliveries:
	case <-time.After(time.Second):
		t.Fatal("pre-latch action could not finish its receive")
	}
	acknowledged := make(chan error, 1)
	go func() { acknowledged <- ev.receipt.finish(action.Context()) }()
	<-delayed.entered
	release()
	action.Release()
	if g.count(protocol.RuntimePauseCheckpoint) != 1 {
		t.Fatal("actual checkpoint no longer charged")
	}
	select {
	case <-acknowledged:
	case <-time.After(time.Second):
		t.Fatal("receipt did not observe cancelled action")
	}
	if g.count("") == 0 {
		t.Fatal("pause incorrectly proved a blocked durable write")
	}
	close(delayed.resume)
	waitProcessor(t, func() bool { return g.count(protocol.RuntimePauseCheckpoint) == 0 })
	row := onlyCurrentProgress(t, p)
	if row.Checkpoint.Committed != 1 || row.CheckpointPosition.Sequence != "7" {
		t.Fatal(row)
	}
	s.cancel()
	<-done
}
func TestProcessorPauseResumeRejectsRetainedEventAfterABAFence(t *testing.T) {
	records := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	store, _, fence := trackedStoreForTest(t, records)
	g := newPauseFixtureGate(false)
	s := newPauseSession(t, g, store)
	part := &pausedPartitionFixture{store: store, got: make(chan struct{}), resume: make(chan struct{}), ignoredCancel: true}
	done := make(chan struct{})
	go func() { defer close(done); s.partitionWorkerContext(part, nil, s.ctx, fence) }()
	action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	release, _ := s.registerDemand(action.Context())
	<-part.got
	release()
	action.Release()
	g.latch()
	close(part.resume)
	waitProcessor(t, func() bool { return g.count("") == 0 })
	other := processorStoreForTest("other", records)
	claimProcessorForTest(t, other)
	claimProcessorForTest(t, store)
	g.resume()
	next, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	defer next.Release()
	releaseNext, _ := s.registerDemand(next.Context())
	defer releaseNext()
	select {
	case <-done:
	case ev := <-s.deliveries:
		t.Fatal("old generation delivered", ev)
	case <-time.After(time.Second):
		t.Fatal("old generation did not retire")
	}
	if part.reads.Load() != 1 {
		t.Fatal("old generation resumed target reads")
	}
}
func TestProcessorPausedDispatcherOnlyRenewsOwnership(t *testing.T) {
	records := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	store, _, _ := trackedStoreForTest(t, records)
	g := newPauseFixtureGate(true)
	s := newPauseSession(t, g, store)
	s.heartbeatInterval = 5 * time.Millisecond
	pool := testProcessorPool(t, processorLimits{1, 1, 1})
	r := reserveProcessorTest(t, pool, 1, 1)
	if err := r.Adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	child, err := r.acquireProcessor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer child.release()
	var factoryCalls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.runGovernedProcessor(child, "earliest", func(string, string, eh.StartPosition) (processorPartition, error) {
			factoryCalls.Add(1)
			return &pausedPartitionFixture{store: store}, nil
		})
	}()
	waitProcessor(t, func() bool { records.mu.Lock(); defer records.mu.Unlock(); return records.revision >= 4 })
	if factoryCalls.Load() != 0 || g.count("") != 0 {
		t.Fatal("paused dispatcher acquired a data client", factoryCalls.Load(), g.count(""))
	}
	g.resume()
	action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	release, _ := s.registerDemand(action.Context())
	select {
	case ev := <-s.deliveries:
		if err := ev.receipt.finish(action.Context()); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not recover after resume")
	}
	release()
	action.Release()
	g.latch()
	waitProcessor(t, func() bool { return g.count("") == 0 })
	s.cancel()
	<-done
	current, _ := records.Read(context.Background(), store.prefix()+"0")
	if current.Owner != "" || current.Sequence == nil || *current.Sequence != 7 {
		t.Fatal("fenced stop release lost checkpoint", current)
	}
}
func TestProcessorPauseTailRetainsIgnoredCancellationCharge(t *testing.T) {
	store, p, _ := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
	_ = store
	g := newPauseFixtureGate(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	cfg := config{Namespace: "test.servicebus.windows.net", EventHub: "events", TailPollInterval: 10 * time.Second, pauseGate: g}
	done := startProcessorTail(ctx, cfg, map[string]bool{"0": true}, p, func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error) {
		calls.Add(1)
		close(entered)
		<-resume
		return eh.PartitionProperties{PartitionID: "0", EventHubName: "events", LastEnqueuedSequenceNumber: 9, LastEnqueuedOffset: "tail:9"}, nil
	})
	<-entered
	g.latch()
	if g.count(protocol.RuntimePauseTail) != 1 {
		t.Fatal("cancelled tail query was released before its return")
	}
	close(resume)
	waitProcessor(t, func() bool { return g.count("") == 0 })
	if row := onlyCurrentProgress(t, p); row.TailPosition.State == "observed" {
		t.Fatal("late paused tail poll freshened evidence")
	}
	time.Sleep(15 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("paused tail started another query")
	}
	cancel()
	<-done
}

type fallbackReceiver struct {
	reads, closes int
	err           error
	closeErr      error
}

func (p *fallbackReceiver) ReceiveEvents(context.Context, int, *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	p.reads++
	return nil, p.err
}
func (p *fallbackReceiver) Close(context.Context) error { p.closes++; return p.closeErr }
func TestGovernedPartitionGeoReplicationFallbackRemainsLazyAndProvesOldClose(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered", true: "close-failed"}[blocked], func(t *testing.T) {
			old := &fallbackReceiver{err: &amqp.Error{Condition: amqp.ErrCond("com.microsoft:georeplication:invalid-offset")}}
			if blocked {
				old.closeErr = errors.New("blocked")
			}
			next := &fallbackReceiver{}
			opens := 0
			p := &governedPartition{inner: old, reopen: func(position eh.StartPosition) (partitionReceiver, error) {
				opens++
				if position.Earliest == nil || !*position.Earliest || !position.Inclusive {
					t.Fatal(position)
				}
				return next, nil
			}}
			_, err := p.ReceiveEvents(context.Background(), 1, nil)
			if blocked {
				if err == nil || opens != 0 || next.reads != 0 {
					t.Fatal("unproved old close opened replacement")
				}
			} else if err != nil || opens != 1 || old.closes != 1 || next.reads != 1 {
				t.Fatal(err, opens, old, next)
			}
		})
	}
}
func TestProcessorClaimCandidatesBalancedAndBounded(t *testing.T) {
	store := processorStoreForTest("self", nil)
	store.partitions = map[string]bool{"0": true, "1": true, "2": true, "3": true}
	now := time.Now()
	owner := func(id, who string, age time.Duration) eh.Ownership {
		return eh.Ownership{FullyQualifiedNamespace: store.namespace, EventHubName: store.hub, ConsumerGroup: store.group, PartitionID: id, OwnerID: who, LastModifiedTime: now.Add(-age)}
	}
	for _, tc := range []struct {
		name string
		rows []eh.Ownership
		want string
	}{{"free", nil, "0"}, {"expired", []eh.Ownership{owner("0", "other", 2*time.Minute)}, "0"}, {"overfull", []eh.Ownership{owner("0", "other", 0), owner("1", "other", 0), owner("2", "other", 0), owner("3", "other", 0)}, "0"}, {"balanced", []eh.Ownership{owner("0", "self", 0), owner("1", "self", 0), owner("2", "other", 0), owner("3", "other", 0)}, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			claims := processorClaimCandidates(tc.rows, store, now)
			newClaim := ""
			for _, claim := range claims {
				found := false
				for _, row := range tc.rows {
					if row.PartitionID == claim.PartitionID && row.OwnerID == "self" {
						found = true
					}
				}
				if !found {
					if newClaim != "" {
						t.Fatal("multiple new claims", claims)
					}
					newClaim = claim.PartitionID
				}
			}
			if newClaim != tc.want {
				t.Fatal(claims, tc.want)
			}
		})
	}
}

type claimedThenCancelledIO struct {
	checkpointRecordIO
	once   sync.Once
	cancel func()
}

func (c *claimedThenCancelledIO) Write(ctx context.Context, row partitionCheckpointRecord, expected string) (partitionCheckpointRecord, error) {
	updated, err := c.checkpointRecordIO.Write(ctx, row, expected)
	if err == nil && row.Owner != "" && row.Sequence == nil {
		c.once.Do(c.cancel)
	}
	return updated, err
}
func TestProcessorPauseRecoversDurableClaimCancelledBeforeWorker(t *testing.T) {
	records := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	io := &claimedThenCancelledIO{checkpointRecordIO: records}
	store := processorStoreForTest("self", io)
	p := newProcessorProgress(config{Namespace: store.namespace, EventHub: store.hub, ConsumerGroup: store.group})
	p.setInventory(store.partitions)
	store.progress = p
	g := newPauseFixtureGate(false)
	s := newPauseSession(t, g, store)
	s.heartbeatInterval = 5 * time.Millisecond
	action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	release, _ := s.registerDemand(action.Context())
	io.cancel = func() { release(); action.Release(); g.latch() }
	pool := testProcessorPool(t, processorLimits{1, 1, 1})
	r := reserveProcessorTest(t, pool, 1, 1)
	if err := r.Adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	child, err := r.acquireProcessor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer child.release()
	var opens atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.runGovernedProcessor(child, "earliest", func(string, string, eh.StartPosition) (processorPartition, error) {
			opens.Add(1)
			return &pausedPartitionFixture{store: store}, nil
		})
	}()
	waitProcessor(t, func() bool { records.mu.Lock(); defer records.mu.Unlock(); return records.revision >= 4 })
	if opens.Load() != 0 {
		t.Fatal("cancelled claim opened data client")
	}
	g.resume()
	next, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	defer next.Release()
	releaseNext, _ := s.registerDemand(next.Context())
	defer releaseNext()
	select {
	case ev := <-s.deliveries:
		if err := ev.receipt.finish(next.Context()); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("durable owned claim was stranded by renewed ETags")
	}
	if opens.Load() != 1 {
		t.Fatal("recovery created duplicate workers", opens.Load())
	}
	releaseNext()
	next.Release()
	s.cancel()
	<-done
}
func TestProcessorPauseDemandBoundsAndCancellation(t *testing.T) {
	g := newPauseFixtureGate(false)
	store := processorStoreForTest("self", nil)
	s := newPauseSession(t, g, store)
	var releases []func()
	for i := 0; i < 32; i++ {
		release, err := s.registerDemand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := s.registerDemand(context.Background()); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
		t.Fatal("unbounded demand", err)
	}
	for _, release := range releases {
		release()
		release()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.receiveAdmission(true, ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestGovernedPublicSDKPartitionFactoryDoesNotOpenTransport(t *testing.T) {
	var connections atomic.Int32
	consumer, err := eh.NewConsumerClientFromConnectionString("Endpoint=sb://test.servicebus.windows.net/;SharedAccessKeyName=test;SharedAccessKey=dGVzdA==", "events", "workers", &eh.ConsumerClientOptions{NewWebSocketConn: func(context.Context, eh.WebSocketConnParams) (net.Conn, error) {
		connections.Add(1)
		return nil, errors.New("fixture transport denied")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close(context.Background())
	yes := true
	level := int64(0)
	partition, err := consumer.NewPartitionClient("0", &eh.PartitionClientOptions{OwnerLevel: &level, Prefetch: -1, StartPosition: eh.StartPosition{Earliest: &yes}})
	if err != nil {
		t.Fatal(err)
	}
	defer partition.Close(context.Background())
	if connections.Load() != 0 {
		t.Fatal("public lazy factory opened target transport")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := partition.ReceiveEvents(ctx, 1, nil); err == nil {
		t.Fatal("denied transport unexpectedly received")
	}
	if connections.Load() == 0 {
		t.Fatal("transport counter did not observe actual receive admission")
	}
}

type manualCreditReceiver struct {
	reads, ingress, closes        atomic.Int32
	active                        atomic.Bool
	reading, closing, closeResume chan struct{}
	readOnce, closeOnce           sync.Once
	closeErr                      error
}

func (p *manualCreditReceiver) ReceiveEvents(ctx context.Context, _ int, _ *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	p.reads.Add(1)
	p.active.Store(true)
	p.readOnce.Do(func() { close(p.reading) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p *manualCreditReceiver) Close(context.Context) error {
	p.closes.Add(1)
	p.closeOnce.Do(func() { close(p.closing) })
	if p.closeResume != nil {
		<-p.closeResume
	}
	if p.closeErr != nil {
		return p.closeErr
	}
	p.active.Store(false)
	return nil
}
func (p *manualCreditReceiver) lateIngress() {
	if p.active.Load() {
		p.ingress.Add(1)
	}
}
func TestProcessorPauseManualCreditPhysicalCloseProof(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked-close", true: "failed-close"}[failed], func(t *testing.T) {
			store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
			g := newPauseFixtureGate(false)
			s := newPauseSession(t, g, store)
			receiver := &manualCreditReceiver{reading: make(chan struct{}), closing: make(chan struct{})}
			if failed {
				receiver.closeErr = errors.New("fixture transport unproven")
			} else {
				receiver.closeResume = make(chan struct{})
			}
			partition := &governedPartition{inner: receiver, store: store, partition: "0", generation: fence}
			pool := testProcessorPool(t, processorLimits{1, 1, 1})
			r := reserveProcessorTest(t, pool, 1, 1)
			if err := r.Adopt(context.Background()); err != nil {
				t.Fatal(err)
			}
			child, err := r.acquireProcessor(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer child.release()
			worker, err := child.acquireWorker("0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { defer close(done); s.partitionWorkerContext(partition, worker, s.ctx, fence) }()
			action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
			release, _ := s.registerDemand(action.Context())
			<-receiver.reading
			release()
			action.Release()
			g.latch()
			<-receiver.closing
			receiver.lateIngress()
			if g.count(protocol.RuntimePauseReceive) != 1 {
				t.Fatal("SDK return or failed close discharged receive proof")
			}
			if failed {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("failed transport did not quarantine worker")
				}
				if g.count(protocol.RuntimePauseReceive) != 1 || pool.snapshot().ActiveWorkers != 1 || s.cleanupError() == nil {
					t.Fatal("failed close manufactured quiescence or resource freedom", g.count(""), pool.snapshot())
				}
			} else {
				close(receiver.closeResume)
				waitProcessor(t, func() bool { return g.count("") == 0 })
				before := receiver.ingress.Load()
				receiver.lateIngress()
				if receiver.ingress.Load() != before {
					t.Fatal("late existing-credit ingress survived proved close")
				}
				s.cancel()
				<-done
			}
		})
	}
}

type positionReceiver struct {
	event *eh.ReceivedEventData
	reads int
}

func (p *positionReceiver) ReceiveEvents(context.Context, int, *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	p.reads++
	if p.event != nil {
		return []*eh.ReceivedEventData{p.event}, nil
	}
	return nil, context.Canceled
}
func (p *positionReceiver) Close(context.Context) error { return nil }
func TestGovernedLatestBoundarySurvivesEmptyPauseGap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		empty bool
		seq   int64
	}{{"empty", true, -1}, {"nonempty", false, 42}, {"exact-max", false, int64(^uint64(0) >> 1)}} {
		t.Run(tc.name, func(t *testing.T) {
			store, progress, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
			yes := true
			queries := 0
			positions := []eh.StartPosition{}
			p := &governedPartition{store: store, partition: "0", generation: fence, anchor: eh.StartPosition{Latest: &yes}, queryBoundary: func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error) {
				queries++
				return eh.PartitionProperties{PartitionID: "0", EventHubName: store.hub, IsEmpty: tc.empty, LastEnqueuedSequenceNumber: tc.seq}, nil
			}, reopen: func(position eh.StartPosition) (partitionReceiver, error) {
				positions = append(positions, position)
				return &positionReceiver{}, nil
			}}
			for i := 0; i < 2; i++ {
				_, err := p.ReceiveEvents(context.Background(), 1, nil)
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if queries != 1 || len(positions) != 2 {
				t.Fatal("latest seed was polled again or receiver not reopened", queries, positions)
			}
			for _, position := range positions {
				if tc.empty {
					if position.Earliest == nil || !*position.Earliest {
						t.Fatal("empty latest boundary skipped pause-gap messages", position)
					}
				} else if position.SequenceNumber == nil || *position.SequenceNumber != tc.seq || position.Inclusive {
					t.Fatal("exact exclusive sequence changed or overflowed", position)
				}
				if position.Latest != nil || position.Offset != nil {
					t.Fatal("reopened relative latest or conflated provider offset", position)
				}
			}
			if row := onlyCurrentProgress(t, progress); row.TailPosition.State == "observed" || row.DeliveredPosition.State == "observed" || row.CheckpointPosition.State == "observed" {
				t.Fatal("private start cursor manufactured progress", row)
			}
		})
	}
}
func TestGovernedLatestSeedCancellationAndScopeFailClosed(t *testing.T) {
	for _, tc := range []string{"cancelled", "partition", "hub", "negative"} {
		t.Run(tc, func(t *testing.T) {
			store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
			yes := true
			opens := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &governedPartition{store: store, partition: "0", generation: fence, anchor: eh.StartPosition{Latest: &yes}, queryBoundary: func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error) {
				props := eh.PartitionProperties{PartitionID: "0", EventHubName: store.hub, LastEnqueuedSequenceNumber: 9}
				switch tc {
				case "cancelled":
					cancel()
				case "partition":
					props.PartitionID = "foreign"
				case "hub":
					props.EventHubName = "foreign"
				case "negative":
					props.LastEnqueuedSequenceNumber = -2
				}
				return props, nil
			}, reopen: func(eh.StartPosition) (partitionReceiver, error) { opens++; return &positionReceiver{}, nil }}
			if _, err := p.ReceiveEvents(ctx, 1, nil); err == nil || opens != 0 || p.anchorFrozen {
				t.Fatal("invalid/cancelled seed was qualified", err, opens, p.anchorFrozen)
			}
		})
	}
}
func TestGovernedEarliestAndCheckpointBypassSeedAndResumeExactReadCursor(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "earliest", true: "checkpoint"}[checkpoint], func(t *testing.T) {
			store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
			if checkpoint {
				ctx := context.WithValue(context.Background(), checkpointFenceContextKey{}, fence)
				if err := store.SetCheckpoint(ctx, checkpointFixture(store, 4, "opaque:4"), nil); err != nil {
					t.Fatal(err)
				}
			}
			yes := true
			queries := 0
			positions := []eh.StartPosition{}
			p := &governedPartition{store: store, partition: "0", generation: fence, anchor: eh.StartPosition{Earliest: &yes}, queryBoundary: func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error) {
				queries++
				return eh.PartitionProperties{}, errors.New("must not seed")
			}, reopen: func(position eh.StartPosition) (partitionReceiver, error) {
				positions = append(positions, position)
				return &positionReceiver{event: &eh.ReceivedEventData{SequenceNumber: 7, Offset: "opaque:7"}}, nil
			}}
			if _, err := p.ReceiveEvents(context.Background(), 1, nil); err != nil {
				t.Fatal(err)
			}
			if err := p.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := p.ReceiveEvents(context.Background(), 1, nil); err != nil {
				t.Fatal(err)
			}
			if queries != 0 || len(positions) != 2 || positions[1].SequenceNumber == nil || *positions[1].SequenceNumber != 7 || positions[1].Inclusive {
				t.Fatal("actual received cursor was not exact exclusive", queries, positions)
			}
			if checkpoint && (positions[0].Offset == nil || *positions[0].Offset != "opaque:4") {
				t.Fatal("opaque durable cursor was rewritten", positions[0])
			}
			record, _ := store.records.Read(context.Background(), store.prefix()+"0")
			if checkpoint && *record.Sequence != 4 || !checkpoint && record.Sequence != nil {
				t.Fatal("private cursor advanced durable checkpoint", record)
			}
		})
	}
}

func TestGovernedGeoReplicationFallbackAfterPriorReadHonorsEarliest(t *testing.T) {
	store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
	ctx := context.WithValue(context.Background(), checkpointFenceContextKey{}, fence)
	if err := store.SetCheckpoint(ctx, checkpointFixture(store, 7, "7"), nil); err != nil {
		t.Fatal(err)
	}
	seq := int64(7)
	old := &fallbackReceiver{err: &amqp.Error{Condition: amqp.ErrCond("com.microsoft:georeplication:invalid-offset")}}
	opened := []eh.StartPosition{}
	p := &governedPartition{inner: old, store: store, partition: "0", generation: fence, lastSequence: &seq, reopen: func(position eh.StartPosition) (partitionReceiver, error) {
		opened = append(opened, position)
		return &positionReceiver{event: &eh.ReceivedEventData{SequenceNumber: 8, Offset: "new:opaque:8"}}, nil
	}}
	if _, err := p.ReceiveEvents(context.Background(), 1, nil); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0].Earliest == nil || !*opened[0].Earliest || !opened[0].Inclusive || opened[0].Offset != nil || opened[0].SequenceNumber != nil {
		t.Fatal("prior cursor overrode pinned Geo-DR fallback", opened)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReceiveEvents(context.Background(), 1, nil); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[1].SequenceNumber == nil || *opened[1].SequenceNumber != 8 || opened[1].Inclusive {
		t.Fatal("later resume did not use actual qualified cursor", opened)
	}
	record, _ := store.records.Read(context.Background(), store.prefix()+"0")
	if *record.Sequence != 7 || record.Offset != "7" {
		t.Fatal("fallback rewrote durable checkpoint", record)
	}
}

func TestGovernedGeoReplicationEmptyRecoveryRetainsFallbackAnchor(t *testing.T) {
	store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
	ctx := context.WithValue(context.Background(), checkpointFenceContextKey{}, fence)
	if err := store.SetCheckpoint(ctx, checkpointFixture(store, 7, "7"), nil); err != nil {
		t.Fatal(err)
	}
	seq := int64(7)
	opened := []eh.StartPosition{}
	p := &governedPartition{inner: &fallbackReceiver{err: &amqp.Error{Condition: amqp.ErrCond("com.microsoft:georeplication:invalid-offset")}}, store: store, partition: "0", generation: fence, lastSequence: &seq, reopen: func(position eh.StartPosition) (partitionReceiver, error) {
		opened = append(opened, position)
		receiver := &positionReceiver{}
		if len(opened) > 1 {
			receiver.event = &eh.ReceivedEventData{SequenceNumber: 8, Offset: "new:8"}
		}
		return receiver, nil
	}}
	if _, err := p.ReceiveEvents(context.Background(), 1, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.ReceiveEvents(context.Background(), 1, nil); err != nil {
		t.Fatal(err)
	}
	for _, position := range opened {
		if position.Earliest == nil || !*position.Earliest || !position.Inclusive {
			t.Fatal("empty recovery reused old cursor", opened)
		}
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReceiveEvents(context.Background(), 1, nil); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 3 || opened[2].SequenceNumber == nil || *opened[2].SequenceNumber != 8 {
		t.Fatal("actual recovery read did not establish new cursor", opened)
	}
}

type boundedReceivedFixture struct {
	value      *eh.ReceivedEventData
	closed     atomic.Int32
	checkpoint chan *eh.ReceivedEventData
}

func (p *boundedReceivedFixture) PartitionID() string { return "0" }
func (p *boundedReceivedFixture) ReceiveEvents(context.Context, int, *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	return []*eh.ReceivedEventData{p.value}, nil
}
func (p *boundedReceivedFixture) UpdateCheckpoint(_ context.Context, event *eh.ReceivedEventData, _ *eh.UpdateCheckpointOptions) error {
	p.checkpoint <- event
	return nil
}
func (p *boundedReceivedFixture) Close(context.Context) error { p.closed.Add(1); return nil }
func TestProcessorReceivedCursorAndCompleteRetentionBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*eh.ReceivedEventData)
	}{
		{"negative-sequence", func(e *eh.ReceivedEventData) { e.SequenceNumber = -1 }},
		{"large-offset", func(e *eh.ReceivedEventData) { e.Offset = strings.Repeat("o", 257) }},
		{"invalid-offset-utf8", func(e *eh.ReceivedEventData) { e.Offset = string([]byte{255}) }},
		{"control-offset", func(e *eh.ReceivedEventData) { e.Offset = "unsafe\x1b" }},
		{"large-key", func(e *eh.ReceivedEventData) { key := strings.Repeat("k", (64<<10)+1); e.PartitionKey = &key }},
		{"invalid-key-utf8", func(e *eh.ReceivedEventData) { key := string([]byte{255}); e.PartitionKey = &key }},
		{"combined-body-offset-key", func(e *eh.ReceivedEventData) {
			key := "key"
			e.PartitionKey = &key
			e.Body = make([]byte, maxProcessorEventBytes-len(e.Offset))
		}},
		{"combined-body-properties", func(e *eh.ReceivedEventData) {
			e.Body = make([]byte, maxProcessorEventBytes-len(e.Offset))
			e.Properties = map[string]any{"k": "v"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
			g := newPauseFixtureGate(false)
			s := newPauseSession(t, g, store)
			value := &eh.ReceivedEventData{SequenceNumber: 4, Offset: "opaque:4"}
			tc.edit(value)
			part := &boundedReceivedFixture{value: value}
			done := make(chan struct{})
			go func() { defer close(done); s.partitionWorkerContext(part, nil, s.ctx, fence) }()
			action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
			defer action.Release()
			release, _ := s.registerDemand(action.Context())
			defer release()
			select {
			case <-done:
			case <-s.deliveries:
				t.Fatal("malformed or oversized event reached application")
			case <-time.After(time.Second):
				t.Fatal("invalid event did not fail closed")
			}
			if part.closed.Load() != 1 {
				t.Fatal("invalid event did not close")
			}
			select {
			case <-s.failure:
			default:
				t.Fatal("invalid cursor/retention failure was silent")
			}
		})
	}
}
func TestProcessorRetainsOnlySelectedPayloadAndCheckpointCursor(t *testing.T) {
	store, _, fence := trackedStoreForTest(t, &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}})
	g := newPauseFixtureGate(false)
	s := newPauseSession(t, g, store)
	key := "key"
	value := &eh.ReceivedEventData{EventData: eh.EventData{Body: []byte("payload"), Properties: map[string]any{"property": "value"}, CorrelationID: strings.Repeat("discarded", maxProcessorEventBytes)}, SequenceNumber: 4, Offset: "opaque:4", PartitionKey: &key, SystemProperties: map[string]any{"unselected": strings.Repeat("discarded", maxProcessorEventBytes)}}
	part := &boundedReceivedFixture{value: value, checkpoint: make(chan *eh.ReceivedEventData, 1)}
	done := make(chan struct{})
	go func() { defer close(done); s.partitionWorkerContext(part, nil, s.ctx, fence) }()
	action, _ := g.Admit(context.Background(), protocol.RuntimePauseAction)
	defer action.Release()
	release, _ := s.registerDemand(action.Context())
	defer release()
	var ev event
	select {
	case ev = <-s.deliveries:
	case <-time.After(time.Second):
		t.Fatal("bounded selected payload was rejected")
	}
	if string(ev.Body) != "payload" || ev.Properties["property"] != "value" || ev.PartitionKey != "key" {
		t.Fatal("selected payload changed", ev)
	}
	value.Body[0] = 'X'
	if string(ev.Body) != "payload" {
		t.Fatal("payload still retained mutable SDK backing")
	}
	if err := ev.receipt.finish(action.Context()); err != nil {
		t.Fatal(err)
	}
	cursor := <-part.checkpoint
	if cursor.SequenceNumber != 4 || cursor.Offset != "opaque:4" || cursor.Body != nil || cursor.Properties != nil || cursor.SystemProperties != nil || cursor.CorrelationID != nil || cursor.PartitionKey != nil || cursor.RawAMQPMessage != nil {
		t.Fatal("checkpoint wait retained unselected SDK payload", cursor)
	}
	release()
	action.Release()
	s.cancel()
	<-done
}
