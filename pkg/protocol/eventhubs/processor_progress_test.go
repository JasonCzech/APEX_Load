package eventhubs

import (
	"math"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func trackerFixture() *processorProgress {
	p := newProcessorProgress(config{Namespace: "test.servicebus.windows.net", EventHub: "events", ConsumerGroup: "workers", ObservePartitionTail: true})
	p.setInventory(map[string]bool{"0": true})
	p.ownership("0", "generation", "owned", nil, "")
	return p
}
func TestProcessorProgressCapturesDoNotInventEvidence(t *testing.T) {
	p := trackerFixture()
	a := p.snapshot()
	b := p.snapshot()
	if !a.Valid() || !b.Valid() || a.Rows[0].Revision != b.Rows[0].Revision || a.Rows[0].CaptureRevision == b.Rows[0].CaptureRevision {
		t.Fatal(a, b)
	}
	for i := 0; i < 3; i++ {
		p.ownership("0", "generation", "owned", nil, "")
	}
	c := p.snapshot()
	if c.Rows[0].Acquired != 1 || c.Rows[0].Checkpoint.Attempts != 0 || c.Rows[0].Delivered != 0 {
		t.Fatal("renewal manufactured acquisition", c)
	}
}
func TestProcessorProgressExactDeliveryAndCheckpointOutcomes(t *testing.T) {
	p := trackerFixture()
	seq := int64(math.MaxInt64)
	p.deliveredEvent("0", "generation", seq, "2:-1:-1", false)
	p.rejectedEvent("0", "generation")
	p.deliveredEvent("0", "generation", seq, "2:-1:-1", true)
	p.checkpointBegin("0", "generation", false)
	p.checkpointEnd("0", "generation", "committed", &seq, "2:-1:-1", time.Millisecond)
	s := p.snapshot()
	if !s.Valid() {
		t.Fatal(s)
	}
	r := s.Rows[0]
	if r.Delivered != 2 || r.Redelivered != 1 || r.Rejected != 1 || r.DeliveredPosition.Sequence != strconv.FormatInt(seq, 10) || r.DeliveredPosition.ProviderOffset != "2:-1:-1" || r.Checkpoint.Committed != 1 || r.Checkpoint.Duration.Distribution.Count != 1 {
		t.Fatal(r)
	}
	p.checkpointBegin("0", "generation", true)
	p.checkpointEnd("0", "generation", "unconfirmed", nil, "", time.Millisecond)
	s = p.snapshot()
	r = s.Rows[0]
	if !s.Valid() || r.CheckpointPosition.Sequence != strconv.FormatInt(seq, 10) || r.CheckpointPosition.State != "unconfirmed" || r.Checkpoint.RetryAttempts != 1 {
		t.Fatal("lost ambiguous prior cursor provenance", s)
	}
}
func TestProcessorProgressLossAndUnverifiedAreDistinct(t *testing.T) {
	p := trackerFixture()
	p.unverified("0")
	s := p.snapshot()
	if s.Rows[0].Ownership != "unverified" || s.Rows[0].Lost != 0 {
		t.Fatal(s)
	}
	p.ownership("0", "generation", "owned", nil, "")
	p.observeForeignOwner("0", "foreign")
	p.ownership("0", "generation", "owned", nil, "")
	s = p.snapshot()
	if s.Rows[0].Ownership != "lost" || s.Rows[0].Lost != 1 {
		t.Fatal("stale same-generation read resurrected ownership", s)
	}
	p.ownership("0", "newgeneration", "owned", nil, "")
	s = p.snapshot()
	if !s.Valid() || len(s.Rows) != 2 {
		t.Fatal(s)
	}
}
func TestProcessorProgressBoundsAndRevisionOverflow(t *testing.T) {
	p := trackerFixture()
	for i := 0; i < 100; i++ {
		g := strconv.Itoa(i)
		p.ownership("0", g, "owned", nil, "")
		p.ownership("0", g, "lost", nil, "")
	}
	s := p.snapshot()
	if !s.Valid() || !s.Incomplete || len(s.Rows) > 64 || s.DroppedRows == 0 {
		t.Fatal(s)
	}
	p.mu.Lock()
	p.revision = math.MaxInt64
	p.capture = math.MaxInt64
	p.mu.Unlock()
	p.unverified("0")
	s = p.snapshot()
	if !s.Incomplete {
		t.Fatal("overflow silently qualified")
	}
}
func TestProcessorTailSettingsStrictAndDefaultOff(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]any
	}{{"bool_type", map[string]any{"eventhubs_observe_partition_tail": "true"}}, {"observer", map[string]any{"eventhubs_observe_partition_tail": false}}, {"fast", map[string]any{"eventhubs_consumer_mode": "processor", "eventhubs_tail_poll_interval_ms": 9999}}, {"large", map[string]any{"eventhubs_consumer_mode": "processor", "eventhubs_tail_poll_interval_ms": 300001}}, {"null", map[string]any{"eventhubs_consumer_mode": "processor", "eventhubs_tail_poll_interval_ms": nil}}} {
		t.Run(test.name, func(t *testing.T) {
			if e := (&EventHubs{}).Init(t.Context(), test.values); e == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
	e := &EventHubs{}
	if err := e.Init(t.Context(), map[string]any{"eventhubs_mock": true}); err != nil {
		t.Fatal(err)
	}
	if e.cfg.ObservePartitionTail || e.cfg.TailPollInterval != 10*time.Second || e.MessagingProgressSnapshot() != nil {
		t.Fatal("nonprocessor default added progress or polling")
	}
	_ = e.Teardown(t.Context())
}
func TestProcessorProgressTerminalCleanupStates(t *testing.T) {
	p := trackerFixture()
	p.state("draining")
	if p.snapshot().Lifecycle != "draining" {
		t.Fatal("missing pending cleanup")
	}
	p.state("quarantined")
	if s := p.snapshot(); !s.Valid() || !s.Incomplete || s.Lifecycle != "quarantined" {
		t.Fatal(s)
	}
}

func TestProcessorProgressCanonicalSourceAndBytePressure(t *testing.T) {
	a := newProcessorProgress(config{Namespace: " TEST.servicebus.windows.net.:443 ", EventHub: "events", ConsumerGroup: "workers"})
	b := newProcessorProgress(config{Namespace: "test.servicebus.windows.net", EventHub: "events", ConsumerGroup: "workers"})
	if a.sourceID != b.sourceID {
		t.Fatal("equivalent namespace fragmented identity")
	}
	for _, bad := range []string{"\x1b[1m", "with\ttab"} {
		if validProviderOffset(bad) {
			t.Fatal("control offset accepted")
		}
	}
	p := trackerFixture()
	for i := 0; i < 100; i++ {
		g := strconv.Itoa(i)
		p.ownership("0", g, "owned", nil, "")
		p.checkpointBegin("0", g, false)
		p.checkpointEnd("0", g, "committed", nil, "", time.Millisecond)
		p.ownership("0", g, "lost", nil, "")
	}
	s := p.snapshot()
	if !s.Valid() || len(s.Rows) == 0 || !s.Incomplete || s.DroppedRows == 0 {
		t.Fatal("byte pressure erased all known evidence", s)
	}
}

func TestProcessorProgressNewGenerationRetiresPreviouslyUnverifiedOwner(t *testing.T) {
	p := trackerFixture()
	p.unverified("0")
	p.ownership("0", "newgeneration", "owned", nil, "")
	for _, r := range p.snapshot().Rows {
		if r.Generation == "generation" && (r.Ownership != "lost" || r.Lost != 1) {
			t.Fatal("superseded unknown ownership did not retire", r)
		}
	}
}

func TestProcessorProgressEvictionPreservesAcquiredUnverifiedGeneration(t *testing.T) {
	p := newProcessorProgress(config{Namespace: "test.servicebus.windows.net", EventHub: "events", ConsumerGroup: "workers"})
	p.setInventory(map[string]bool{"0": true, "1": true})
	p.ownership("0", "current", "owned", nil, "")
	p.deliveredEvent("0", "current", 42, "2:42:-1", false)
	p.unverified("0")
	for i := 0; i < protocol.MaxMessagingProgressRows+2; i++ {
		p.ownership("1", strconv.Itoa(i), "owned", nil, "")
	}
	p.deliveredEvent("0", "current", 43, "2:43:-1", false)
	s := p.snapshot()
	if !s.Valid() || !s.Incomplete || s.DroppedRows == 0 || len(s.Rows) > protocol.MaxMessagingProgressRows {
		t.Fatal("bounded historical retention lost coverage", s)
	}
	for _, row := range s.Rows {
		if row.Partition == "0" && row.Generation == "current" {
			if row.Ownership != "unverified" || row.Acquired != 1 || row.Lost != 0 || row.Delivered != 2 || row.DeliveredPosition.Sequence != "43" {
				t.Fatal("current unverified generation was evicted and recreated", row)
			}
			return
		}
	}
	t.Fatal("current unverified generation disappeared")
}

func TestProcessorProgressByteClippingPreservesUnverifiedCurrentBeforeHistory(t *testing.T) {
	p := newProcessorProgress(config{Namespace: "test.servicebus.windows.net", EventHub: strings.Repeat("h", 256), ConsumerGroup: strings.Repeat("g", 256), ObservePartitionTail: true})
	p.setInventory(map[string]bool{"0": true, "31": true})
	p.ownership("31", "current", "owned", nil, "")
	p.deliveredEvent("31", "current", 42, "2:42:-1", false)
	p.unverified("31")
	for i := 0; i < protocol.MaxMessagingProgressRows-1; i++ {
		generation := strings.Repeat("g", 240) + strconv.Itoa(i)
		seq := int64(i)
		offset := strings.Repeat("o", 256)
		p.ownership("0", generation, "owned", nil, "")
		p.deliveredEvent("0", generation, seq, offset, false)
		p.checkpointBegin("0", generation, false)
		p.checkpointEnd("0", generation, "committed", &seq, offset, time.Millisecond)
		p.ownership("0", generation, "lost", nil, "")
	}
	s := p.snapshot()
	if !s.Valid() || !s.Incomplete || s.DroppedRows == 0 || len(s.Rows) >= protocol.MaxMessagingProgressRows {
		t.Fatal("fixture did not exercise bounded byte clipping", s)
	}
	for _, row := range s.Rows {
		if row.Partition == "31" && row.Generation == "current" {
			if row.Ownership != "unverified" || row.Acquired != 1 || row.Lost != 0 || row.Delivered != 1 {
				t.Fatal("byte clipping changed current generation", row)
			}
			return
		}
	}
	t.Fatal("byte clipping retained terminal history over current unverified ownership")
}

func TestProcessorProgressFencedAttemptsCountOwnershipLossOnce(t *testing.T) {
	for _, test := range []struct {
		name           string
		acquired, lost int64
		ownership      string
	}{
		{name: "owned", acquired: 1, lost: 1, ownership: "lost"},
		{name: "unverified", acquired: 1, lost: 1, ownership: "lost"},
		{name: "already_lost", acquired: 1, lost: 1, ownership: "lost"},
		{name: "unknown", ownership: "lost"},
		{name: "released", acquired: 1, ownership: "released"},
		{name: "unknown_released", ownership: "released"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := trackerFixture()
			generation := "generation"
			if test.acquired == 0 {
				p = newProcessorProgress(config{Namespace: "test.servicebus.windows.net", EventHub: "events", ConsumerGroup: "workers"})
				p.setInventory(map[string]bool{"0": true})
				generation = "unverified"
			}
			p.checkpointBegin("0", generation, false)
			switch test.name {
			case "unverified":
				p.unverified("0")
			case "already_lost":
				p.observeForeignOwner("0", "foreign")
			case "released", "unknown_released":
				p.releasePartition("0")
			}
			p.checkpointEnd("0", generation, "fenced", nil, "", time.Millisecond)
			p.checkpointBegin("0", generation, true)
			p.checkpointEnd("0", generation, "fenced", nil, "", time.Millisecond)
			// Late store reads and SDK errors must not resurrect a terminal owner
			// or turn voluntary release into an additional lifecycle loss.
			p.observeForeignOwner("0", "foreign")
			p.ownership("0", generation, "lost", nil, "")
			p.ownership("0", generation, "owned", nil, "")
			s := p.snapshot()
			if !s.Valid() || len(s.Rows) != 1 {
				t.Fatal("invalid fenced generation evidence", s)
			}
			row := s.Rows[0]
			if row.Acquired != test.acquired || row.Lost != test.lost || row.Ownership != test.ownership || row.Checkpoint.Fenced != 2 || row.Checkpoint.InFlight != 0 || row.Checkpoint.Duration.Eligible != 2 || row.Checkpoint.Duration.Distribution.Count != 2 {
				t.Fatal("checkpoint outcomes changed distinct lifecycle population", row)
			}
		})
	}
}
