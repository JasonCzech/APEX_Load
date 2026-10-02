//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"fmt"
	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
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
