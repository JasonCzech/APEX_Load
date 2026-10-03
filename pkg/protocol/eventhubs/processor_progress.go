package eventhubs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const FeatureProcessorProgressV1 = "eventhubs_processor_progress_v1"
const FeaturePartitionTailV1 = "eventhubs_partition_tail_v1"

type progressEntry struct {
	row                                   protocol.MessagingProgressRow
	verified, delivered, checkpoint, tail time.Time // process-monotonic observations
}

// No network or lifecycle mutex is held while snapshots read this tracker.
type processorProgress struct {
	mu                                      sync.Mutex
	session, sourceID, source, subscription string
	revision, capture                       int64
	inventory                               map[string]bool
	rows                                    map[string]*progressEntry
	lifecycle                               string
	tailEnabled, incomplete                 bool
	dropped                                 int64
}

func newProcessorProgress(cfg config) *processorProgress {
	digest := sha256.Sum256([]byte(canonicalProcessorNamespace(cfg.Namespace) + "\x00" + cfg.EventHub + "\x00" + cfg.ConsumerGroup))
	return &processorProgress{session: uuid.NewString(), sourceID: hex.EncodeToString(digest[:]), source: cfg.EventHub, subscription: cfg.ConsumerGroup, rows: map[string]*progressEntry{}, inventory: map[string]bool{}, lifecycle: "pending", tailEnabled: cfg.ObservePartitionTail}
}
func (p *processorProgress) change(e *progressEntry) {
	if p.revision == math.MaxInt64 {
		p.incomplete = true
		e.row.Incomplete = true
		return
	}
	p.revision++
	e.row.Revision = strconv.FormatInt(p.revision, 10)
	e.row.UpdatedAt = time.Now().UTC()
}
func (p *processorProgress) entry(partition, generation string) *progressEntry {
	key := partition + "\x00" + generation
	if e := p.rows[key]; e != nil {
		return e
	}
	if len(p.rows) >= protocol.MaxMessagingProgressRows {
		// Evict only terminal history; an acquired generation remains current
		// while storage availability prevents verification.
		keys := []string{}
		for k, e := range p.rows {
			if processorProgressRowPriority(e.row) == 2 {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			p.incomplete = true
			return nil
		}
		delete(p.rows, keys[0])
		p.dropped++
		p.incomplete = true
	}
	tailState := "disabled"
	if p.tailEnabled {
		tailState = "missing"
	}
	e := &progressEntry{row: protocol.MessagingProgressRow{Protocol: "eventhubs", Mode: "processor", SourceID: p.sourceID, Source: p.source, Subscription: p.subscription, SessionID: p.session, Partition: partition, Generation: generation, ExpectedPartitions: int64(len(p.inventory)), InventoryComplete: len(p.inventory) > 0, Ownership: "unverified", DeliveredPosition: protocol.MessagingProgressPosition{State: "missing"}, CheckpointPosition: protocol.MessagingProgressPosition{State: "missing"}, TailPosition: protocol.MessagingProgressPosition{State: tailState}, Checkpoint: protocol.MessagingCheckpointProgress{Duration: protocol.MessagingDurationSummary{Metric: "checkpoint", Population: "checkpoint_operation"}}}}
	p.change(e)
	p.rows[key] = e
	return e
}
func (p *processorProgress) setInventory(ids map[string]bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inventory = ids
	p.lifecycle = "running"
	keys := []string{}
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		p.entry(id, "unverified")
	}
}
func (p *processorProgress) ownership(partition, generation, state string, seq *int64, offset string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.inventory[partition] {
		p.incomplete = true
		return
	}
	if generation == "" {
		generation = "unverified"
	}
	e := p.entry(partition, generation)
	if e == nil {
		return
	}
	if generation != "unverified" {
		delete(p.rows, partition+"\x00unverified")
	}
	if e.row.Ownership == "released" {
		return
	}
	if state == "owned" {
		if e.row.Ownership == "lost" {
			return
		}
		for _, old := range p.rows {
			if old != e && old.row.Partition == partition && (old.row.Ownership == "owned" || old.row.Ownership == "unverified" && old.row.Acquired > 0) {
				loseProcessorOwnership(old)
				p.change(old)
			}
		}
		if e.row.Acquired == 0 {
			e.row.Acquired = 1
		}
		e.verified = time.Now()
		e.row.OwnershipVerifiedAt = e.verified.UTC()
		current, _ := strconv.ParseInt(e.row.CheckpointPosition.Sequence, 10, 64)
		if seq != nil && *seq >= 0 && (e.row.CheckpointPosition.Sequence == "" || *seq >= current) && e.row.Checkpoint.InFlight == 0 {
			e.checkpoint = time.Now()
			e.row.CheckpointPosition = observedProgressPosition(*seq, offset, e.checkpoint)
		}
	} else if state == "lost" {
		loseProcessorOwnership(e)
	}
	e.row.Ownership = state
	p.change(e)
}
func (p *processorProgress) unverified(partition string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.rows {
		if partition == "" || e.row.Partition == partition {
			if e.row.Ownership == "owned" {
				e.row.Ownership = "unverified"
				p.change(e)
			}
		}
	}
}
func observedProgressPosition(seq int64, offset string, now time.Time) protocol.MessagingProgressPosition {
	return protocol.MessagingProgressPosition{State: "observed", Sequence: strconv.FormatInt(seq, 10), ProviderOffset: offset, ObservedAt: now.UTC(), ReceivedAt: now.UTC()}
}
func (p *processorProgress) deliveredEvent(partition, generation string, seq int64, offset string, redelivery bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(partition, generation)
	if e == nil {
		return
	}
	e.row.Delivered++
	if redelivery {
		e.row.Redelivered++
	}
	e.delivered = time.Now()
	e.row.DeliveredPosition = observedProgressPosition(seq, offset, e.delivered)
	p.change(e)
}
func (p *processorProgress) rejectedEvent(partition, generation string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(partition, generation)
	if e != nil {
		e.row.Rejected++
		p.change(e)
	}
}
func (p *processorProgress) checkpointBegin(partition, generation string, retry bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(partition, generation)
	if e == nil {
		return
	}
	e.row.Checkpoint.Attempts++
	e.row.Checkpoint.InFlight++
	if retry {
		e.row.Checkpoint.RetryAttempts++
	}
	p.change(e)
}
func (p *processorProgress) checkpointEnd(partition, generation, outcome string, seq *int64, offset string, duration time.Duration) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(partition, generation)
	if e == nil {
		return
	}
	c := &e.row.Checkpoint
	if c.InFlight == 0 {
		p.incomplete = true
		e.row.Incomplete = true
		return
	}
	c.InFlight--
	c.Duration.Eligible++
	ms := float64(duration) / float64(time.Millisecond)
	if ms >= 0 && ms <= 24*60*60*1000 {
		c.Duration.Distribution.Add(ms)
	} else {
		if c.Duration.Unavailable == nil {
			c.Duration.Unavailable = map[string]int64{}
		}
		c.Duration.Unavailable["censored"]++
	}
	switch outcome {
	case "committed":
		c.Committed++
		if seq != nil && *seq >= 0 {
			e.checkpoint = time.Now()
			e.row.CheckpointPosition = observedProgressPosition(*seq, offset, e.checkpoint)
		}
	case "fenced":
		c.Fenced++
		loseProcessorOwnership(e)
	case "cancelled_before_write":
		c.CancelledBeforeWrite++
	case "unconfirmed":
		c.Unconfirmed++
		e.row.CheckpointPosition.State = "unconfirmed"
	default:
		outcome = "failed"
		c.Failed++
	}
	c.LastOutcome = outcome
	c.LastCompletedAt = time.Now().UTC()
	p.change(e)
}
func (p *processorProgress) state(state string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lifecycle = state
	if state == "quarantined" {
		p.incomplete = true
	}
	for _, e := range p.rows {
		if state == "released" && e.row.Ownership != "lost" {
			e.row.Ownership = "released"
		}
		if state == "quarantined" {
			e.row.Incomplete = true
		}
		p.change(e)
	}
}
func ageMs(now, at time.Time) *int64 {
	if at.IsZero() {
		return nil
	}
	age := now.Sub(at).Milliseconds()
	if age < 0 {
		return nil
	}
	return &age
}
func (p *processorProgress) snapshot() *protocol.MessagingProgressSnapshot {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if p.capture == math.MaxInt64 {
		p.incomplete = true
		for _, e := range p.rows {
			e.row.Incomplete = true
		}
	} else {
		p.capture++
	}
	s := &protocol.MessagingProgressSnapshot{Version: protocol.MessagingProgressVersion, Lifecycle: p.lifecycle, CapturedAt: now.UTC(), Rows: []protocol.MessagingProgressRow{}, Incomplete: p.incomplete, DroppedRows: p.dropped}
	for _, e := range p.rows {
		r := e.row
		r.CapturedAt = now.UTC()
		r.CaptureRevision = strconv.FormatInt(p.capture, 10)
		r.OwnershipVerifiedAgeMs = ageMs(now, e.verified)
		if r.DeliveredPosition.State == "observed" {
			r.DeliveredPosition.ObservationAgeMs = ageMs(now, e.delivered)
		}
		if r.CheckpointPosition.Sequence != "" {
			r.CheckpointPosition.ObservationAgeMs = ageMs(now, e.checkpoint)
		}
		if r.TailPosition.State == "observed" {
			r.TailPosition.ObservationAgeMs = ageMs(now, e.tail)
		}
		s.Rows = append(s.Rows, r)
	}
	sort.Slice(s.Rows, func(i, j int) bool {
		a, b := s.Rows[i], s.Rows[j]
		if ap, bp := processorProgressRowPriority(a), processorProgressRowPriority(b); ap != bp {
			return ap < bp
		}
		if a.Partition != b.Partition {
			return a.Partition < b.Partition
		}
		return a.Generation < b.Generation
	})
	// Preserve current ownership first; byte pressure never silently discards
	// a complete snapshot. Dropped count remains a cumulative lower bound.
	excluded := int64(0)
	for {
		b, err := json.Marshal(s)
		if err == nil && len(b) <= protocol.MaxMessagingProgressBytes {
			break
		}
		if len(s.Rows) == 0 {
			return protocol.NewUnavailableMessagingProgress("oversized")
		}
		s.Rows = s.Rows[:len(s.Rows)-1]
		excluded++
		s.Incomplete = true
		s.DroppedRows = p.dropped + excluded
	}
	// Cloning while holding the tracker lock also protects the duration maps.
	return s.Clone()
}

// Match the shared merge order so local retention cannot discard current
// unverified evidence before historical generations reach the common merger.
func processorProgressRowPriority(row protocol.MessagingProgressRow) int {
	switch row.Ownership {
	case "owned":
		return 0
	case "unverified":
		return 1
	default:
		return 2
	}
}

// Lifecycle loss is distinct from a fenced checkpoint attempt. A generation
// can lose ownership once, and an unknown generation has no acquisition to lose.
// Late callbacks cannot turn a voluntarily released generation into a loss.
func loseProcessorOwnership(e *progressEntry) {
	if e.row.Ownership == "lost" || e.row.Ownership == "released" {
		return
	}
	if e.row.Acquired > 0 {
		e.row.Lost++
	}
	e.row.Ownership = "lost"
}

// SDK offsets are opaque tokens (including geo-DR compound offsets), never
// numeric cursors. Bound metadata without interpreting their provider syntax.
func validProviderOffset(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
func (p *processorProgress) observeForeignOwner(partition, generation string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.rows {
		if e.row.Partition == partition && (e.row.Ownership == "owned" || e.row.Ownership == "unverified") && e.row.Acquired > 0 {
			loseProcessorOwnership(e)
			p.change(e)
		}
	}
}

func (p *processorProgress) releasePartition(partition string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.rows {
		if e.row.Partition == partition && (e.row.Ownership == "owned" || e.row.Ownership == "unverified") {
			e.row.Ownership = "released"
			p.change(e)
		}
	}
}

func canonicalProcessorNamespace(namespace string) string {
	host := strings.ToLower(strings.TrimSpace(namespace))
	if split, _, err := net.SplitHostPort(host); err == nil {
		host = split
	}
	return strings.TrimSuffix(host, ".")
}
