//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type delayedCheckpointIO struct {
	checkpointRecordIO
	entered, resume chan struct{}
	uncertain       bool
	once            sync.Once
}

func (d *delayedCheckpointIO) Write(ctx context.Context, r partitionCheckpointRecord, expected string) (partitionCheckpointRecord, error) {
	if r.Sequence != nil {
		d.once.Do(func() {
			if d.entered != nil {
				close(d.entered)
			}
			if d.resume != nil {
				<-d.resume
			}
		})
	}
	updated, err := d.checkpointRecordIO.Write(ctx, r, expected)
	if r.Sequence != nil && d.uncertain && err == nil {
		return partitionCheckpointRecord{}, errors.New("ambiguous transport failure")
	}
	return updated, err
}
func trackedStoreForTest(t *testing.T, records checkpointRecordIO) (*fencedCheckpointStore, *processorProgress, string) {
	t.Helper()
	s := processorStoreForTest("worker", records)
	p := newProcessorProgress(config{Namespace: s.namespace, EventHub: s.hub, ConsumerGroup: s.group})
	p.setInventory(s.partitions)
	s.progress = p
	claimProcessorForTest(t, s)
	g, e := s.ownershipFence(t.Context(), "0")
	if e != nil {
		t.Fatal(e)
	}
	return s, p, g
}
func checkpointFixture(s *fencedCheckpointStore, seq int64, offset string) eh.Checkpoint {
	return eh.Checkpoint{FullyQualifiedNamespace: s.namespace, EventHubName: s.hub, ConsumerGroup: s.group, PartitionID: "0", SequenceNumber: &seq, Offset: &offset}
}
func onlyCurrentProgress(t *testing.T, p *processorProgress) protocol.MessagingProgressRow {
	t.Helper()
	s := p.snapshot()
	if !s.Valid() || len(s.Rows) != 1 {
		t.Fatal(s)
	}
	return s.Rows[0]
}
func TestProcessorCheckpointLateDurableCompletionPreservesCancelledAction(t *testing.T) {
	records := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	s, p, g := trackedStoreForTest(t, records)
	base := context.WithValue(t.Context(), checkpointFenceContextKey{}, g)
	if err := s.SetCheckpoint(base, checkpointFixture(s, 4, "2:-1:-1"), nil); err != nil {
		t.Fatal(err)
	}
	delayed := &delayedCheckpointIO{checkpointRecordIO: records, entered: make(chan struct{}), resume: make(chan struct{})}
	s.records = delayed
	action, cancel := context.WithCancel(base)
	done := make(chan error, 1)
	go func() { done <- s.SetCheckpoint(action, checkpointFixture(s, math.MaxInt64, "2:geo-dr:-1"), nil) }()
	<-delayed.entered
	cancel()
	pending := onlyCurrentProgress(t, p)
	if pending.Checkpoint.InFlight != 1 || pending.CheckpointPosition.Sequence != "4" {
		t.Fatal("pending CAS discarded durable cursor", pending)
	}
	close(delayed.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	final := onlyCurrentProgress(t, p)
	if action.Err() == nil || final.Checkpoint.Committed != 2 || final.Checkpoint.Attempts != 2 || final.CheckpointPosition.Sequence != strconv.FormatInt(math.MaxInt64, 10) || final.CheckpointPosition.ProviderOffset != "2:geo-dr:-1" || final.Revision == pending.Revision {
		t.Fatal("late success lost durable provenance", final)
	}
}
func TestProcessorCheckpointAmbiguousWriteRetainsOldCursorUntilVerified(t *testing.T) {
	records := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	s, p, g := trackedStoreForTest(t, records)
	ctx := context.WithValue(t.Context(), checkpointFenceContextKey{}, g)
	if err := s.SetCheckpoint(ctx, checkpointFixture(s, 4, "2:-1:-1"), nil); err != nil {
		t.Fatal(err)
	}
	s.records = &delayedCheckpointIO{checkpointRecordIO: records, uncertain: true}
	if err := s.SetCheckpoint(ctx, checkpointFixture(s, 5, "2:5:-1"), nil); err == nil {
		t.Fatal("ambiguous write succeeded")
	}
	r := onlyCurrentProgress(t, p)
	if r.CheckpointPosition.Sequence != "4" || r.CheckpointPosition.State != "unconfirmed" || r.Checkpoint.Unconfirmed != 1 || r.Checkpoint.Committed != 1 {
		t.Fatal(r)
	}
	if _, err := s.ownershipFence(ctx, "0"); err != nil {
		t.Fatal(err)
	}
	r = onlyCurrentProgress(t, p)
	if r.CheckpointPosition.Sequence != "5" || r.CheckpointPosition.ProviderOffset != "2:5:-1" || r.CheckpointPosition.State != "observed" || r.Checkpoint.Committed != 1 {
		t.Fatal("verification fabricated successful attempt", r)
	}
}
func TestProcessorCheckpointCancellationAndABAFenceProgress(t *testing.T) {
	records := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	s, p, g := trackedStoreForTest(t, records)
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), checkpointFenceContextKey{}, g))
	cancel()
	if err := s.SetCheckpoint(ctx, checkpointFixture(s, 4, "2:-1:-1"), nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r := onlyCurrentProgress(t, p)
	if r.Checkpoint.CancelledBeforeWrite != 1 || r.CheckpointPosition.State != "missing" {
		t.Fatal(r)
	}
	other := processorStoreForTest("foreign", records)
	claimProcessorForTest(t, other)
	// A rebalance can verify the loss before a previously delivered event's
	// checkpoint fails its fence. The two observations describe one loss.
	if _, err := s.ListOwnership(t.Context(), s.namespace, s.hub, s.group, nil); err != nil {
		t.Fatal(err)
	}
	r = onlyCurrentProgress(t, p)
	if r.Ownership != "lost" || r.Lost != 1 {
		t.Fatal("ownership observation did not establish fixture loss", r)
	}
	active := context.WithValue(t.Context(), checkpointFenceContextKey{}, g)
	if err := s.SetCheckpoint(active, checkpointFixture(s, 4, "2:-1:-1"), nil); !errors.Is(err, errCheckpointFence) {
		t.Fatal(err)
	}
	r = onlyCurrentProgress(t, p)
	if r.Ownership != "lost" || r.Checkpoint.Fenced != 1 || r.Lost != 1 {
		t.Fatal(r)
	}
}
func TestProcessorCheckpointMetadataAllowsOpaqueOffsets(t *testing.T) {
	owner, generation, sequence, offset := "owner", "82d72e25-5524-4110-91cc-11c17238c1b1", "9007199254740993", "2:-1:-1"
	data := map[string]*string{"apex_owner": &owner, "apex_generation": &generation, "apex_sequence": &sequence, "apex_offset": &offset}
	r, err := decodeCheckpointMetadata("record", data, "etag", time.Now())
	if err != nil || r.Offset != offset || *r.Sequence != 9007199254740993 {
		t.Fatal(r, err)
	}
	for _, bad := range []string{"", "line\nbreak", string(make([]byte, 257))} {
		offset = bad
		if _, err := decodeCheckpointMetadata("record", data, "etag", time.Now()); err == nil {
			t.Fatal("unbounded/invalid opaque offset accepted")
		}
	}
}
func TestProcessorProgressReadErrorDoesNotCountOwnershipLoss(t *testing.T) {
	records := &checkpointMemoryIO{values: map[string]partitionCheckpointRecord{}}
	s, p, _ := trackedStoreForTest(t, records)
	s.records = failingOwnershipIO{records}
	if _, err := s.ownershipFence(t.Context(), "0"); err == nil {
		t.Fatal("fixture expected error")
	}
	r := onlyCurrentProgress(t, p)
	if r.Ownership != "unverified" || r.Lost != 0 || r.Acquired != 1 {
		t.Fatal(r)
	}
}

func TestProcessorTailSingleSourceAdmissionAndCancelledLateQuery(t *testing.T) {
	cfg := config{Namespace: "tail-test.servicebus.windows.net", EventHub: "events", ConsumerGroup: "workers", ObservePartitionTail: true, TailPollInterval: 10 * time.Second}
	p := newProcessorProgress(cfg)
	p.setInventory(map[string]bool{"0": true})
	p.ownership("0", "generation", "owned", nil, "")
	ctx, cancel := context.WithCancel(t.Context())
	entered, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	query := func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-resume
		return eh.PartitionProperties{EventHubName: cfg.EventHub, PartitionID: "0", LastEnqueuedSequenceNumber: math.MaxInt64, LastEnqueuedOffset: "2:-1:-1"}, nil
	}
	done := startProcessorTail(ctx, cfg, p.inventory, p, query)
	<-entered
	second := newProcessorProgress(cfg)
	second.setInventory(p.inventory)
	second.ownership("0", "second-generation", "owned", nil, "")
	otherCtx, otherCancel := context.WithCancel(t.Context())
	otherDone := startProcessorTail(otherCtx, cfg, second.inventory, second, query)
	cancel()
	select {
	case <-done:
		t.Fatal("query cleanup did not join actual completion")
	default:
	}
	if calls.Load() != 1 {
		t.Fatal("source had simultaneous polls", calls.Load())
	}
	close(resume)
	<-done
	otherCancel()
	<-otherDone
	if onlyCurrentProgress(t, p).TailPosition.State != "missing" {
		t.Fatal("cancelled owner published late tail")
	}
}
func TestProcessorTailGenerationFenceAndEmptyMissingCursor(t *testing.T) {
	p := trackerFixture()
	p.tailObservedGeneration("0", "generation", math.MaxInt64, "2:-1:-1", false, false)
	r := onlyCurrentProgress(t, p)
	if r.TailPosition.Sequence != strconv.FormatInt(math.MaxInt64, 10) {
		t.Fatal(r)
	}
	if _, ok := r.QualifiedLag("delivered", 30000); ok {
		t.Fatal("missing cursor fabricated lag")
	}
	p.observeForeignOwner("0", "foreign")
	p.ownership("0", "newgeneration", "owned", nil, "")
	p.tailObservedGeneration("0", "generation", 0, "0", false, false)
	s := p.snapshot()
	for _, row := range s.Rows {
		if row.Generation == "newgeneration" && row.TailPosition.State != "missing" {
			t.Fatal("old generation polluted new owner")
		}
	}
	p.tailObservedGeneration("0", "newgeneration", -1, "", true, false)
	for _, row := range p.snapshot().Rows {
		if row.Generation == "newgeneration" && row.TailPosition.State != "empty" {
			t.Fatal("empty tail lost explicit state")
		}
	}
}

func TestProcessorTailDistinctIdentityEvidenceAndCadence(t *testing.T) {
	cfg := config{Namespace: "mixed-tail.servicebus.windows.net", EventHub: "events", ConsumerGroup: "workers", ObservePartitionTail: true, TailPollInterval: 300 * time.Second}
	slow := newProcessorProgress(cfg)
	slow.setInventory(map[string]bool{"0": true})
	slow.ownership("0", "slow", "owned", nil, "")
	slowCtx, slowCancel := context.WithCancel(t.Context())
	defer slowCancel()
	entered, resume := make(chan struct{}), make(chan struct{})
	firstDone := startProcessorTail(slowCtx, cfg, slow.inventory, slow, func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error) {
		close(entered)
		<-resume
		return eh.PartitionProperties{EventHubName: cfg.EventHub, PartitionID: "0", LastEnqueuedSequenceNumber: 7, LastEnqueuedOffset: "2:-1:-1"}, nil
	})
	<-entered
	cfg.TailPollInterval = 10 * time.Second
	fast := newProcessorProgress(cfg)
	fast.setInventory(slow.inventory)
	fast.ownership("0", "fast", "owned", nil, "")
	fastCtx, fastCancel := context.WithCancel(t.Context())
	defer fastCancel()
	queried := make(chan struct{})
	secondDone := startProcessorTail(fastCtx, cfg, fast.inventory, fast, func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error) {
		close(queried)
		return eh.PartitionProperties{}, errors.New("management identity revoked")
	})
	close(resume)
	select {
	case <-queried:
	case <-time.After(5 * time.Second):
		t.Fatal("slow cadence suppressed independent fast participant")
	}
	deadline := time.Now().Add(5 * time.Second)
	for onlyCurrentProgress(t, fast).TailPosition.State != "unconfirmed" {
		if time.Now().After(deadline) {
			t.Fatal("failed identity outcome not published")
		}
		time.Sleep(time.Millisecond)
	}
	slowCancel()
	fastCancel()
	<-firstDone
	<-secondDone
	a, b := onlyCurrentProgress(t, slow), onlyCurrentProgress(t, fast)
	if a.TailPosition.Sequence != "7" || b.TailPosition.State != "unconfirmed" || b.TailPosition.Sequence != "" {
		t.Fatal("another principal's tail hid identity failure", a, b)
	}
}

func TestProcessorProgressSnapshotReinitializationIsAtomicAndNeverTransientlyMissing(t *testing.T) {
	t.Setenv("APEXLOAD_EVENTHUBS_CHECKPOINT_CONTAINERS", "https://account.blob.core.windows.net/checkpoints")
	cfg := map[string]any{"eventhubs_consumer_mode": "processor", "eventhubs_checkpoint_container_url": "https://account.blob.core.windows.net/checkpoints", "eventhubs_auth_type": "aad", "eventhubs_namespace": "approved.servicebus.windows.net", "eventhubs_event_hub": "events", "eventhubs_consumer_group": "workers", "eventhubs_observe_partition_tail": true}
	e := &EventHubs{}
	if err := e.Init(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				s := e.MessagingProgressSnapshot()
				if s == nil || !s.Valid() {
					t.Error("successful iteration reset exposed missing/invalid progress")
				}
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 50; i++ {
		if err := e.Teardown(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := e.Init(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	c := e.client.(*realClient)
	if c.processor != nil || c.cred != nil || len(c.consumers) > 0 {
		t.Fatal("snapshot/reinit generated target traffic")
	}
	if err := e.Teardown(t.Context()); err != nil {
		t.Fatal(err)
	}
}
