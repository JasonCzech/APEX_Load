package browser

import (
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Aliases retain the local diagnostic API while sharing its passive wire
// contract with durable collectors. Raw browser_documents_v2 is unchanged.
type BrowserDocumentReceipt = protocol.BrowserDocumentReceipt
type BrowserDocumentReceiptEvidence = protocol.BrowserDocumentReceiptEvidence

const maxBrowserDocumentReceipts = protocol.MaxBrowserDocumentReceipts // begin, five metrics, end per lifecycle
const browserDocumentWallStepThreshold = 100 * time.Millisecond

type browserDocumentReceiptClock struct {
	lifetime                           *protocol.BrowserNativeLifetime
	start                              time.Time // Retains Go's process monotonic reading; never serialize it directly.
	receipts                           map[string]BrowserDocumentReceipt
	wallStepObserved, incomplete, loss bool
}

func newBrowserDocumentReceiptClock(start time.Time) browserDocumentReceiptClock {
	return browserDocumentReceiptClock{start: start, receipts: make(map[string]BrowserDocumentReceipt)}
}

// All clock methods run under Browser.mu. There are no timers, native calls,
// additional target probes, or asynchronous workers to join during teardown.
func (c *browserDocumentReceiptClock) observe(now time.Time) (elapsed, drift time.Duration) {
	if c.start.IsZero() {
		c.incomplete = true
		c.loss = true
		return 0, 0
	}
	return c.observeSample(now, now.Sub(c.start))
}

func (c *browserDocumentReceiptClock) observeSample(now time.Time, elapsed time.Duration) (time.Duration, time.Duration) {
	drift := now.UTC().Sub(c.start.UTC()) - elapsed
	if elapsed < 0 {
		c.incomplete = true
		c.loss = true
	}
	if drift > browserDocumentWallStepThreshold || drift < -browserDocumentWallStepThreshold {
		c.wallStepObserved = true
	}
	return elapsed, drift
}

func (c *browserDocumentReceiptClock) record(d *browserDocumentState, p browserDocumentPayload, measurementID string, revision uint64, now time.Time) {
	elapsed, drift := c.observe(now)
	if c.start.IsZero() || elapsed < 0 {
		return
	}
	key := d.lifecycleID + "/" + p.Kind + "/" + measurementID
	// Duplicate/late lifecycle callbacks cannot revise a frozen receipt.
	if _, exists := c.receipts[key]; exists {
		return
	}
	if len(c.receipts) >= maxBrowserDocumentReceipts {
		c.incomplete = true
		c.loss = true
		return
	}
	c.receipts[key] = BrowserDocumentReceipt{DocumentID: d.documentID, LifecycleID: d.lifecycleID, FrameID: d.frameID, Kind: p.Kind, MeasurementID: measurementID, Revision: revision, BrowserTimeOriginMS: p.TimeOrigin, BrowserOffsetMS: p.Offset, ReceivedAt: now.UTC(), HostElapsedNS: int64(elapsed), WallDriftNS: int64(drift)}
}

// BrowserDocumentReceiptEvidence returns owned acquisition data without
// touching the browser. Disabled or uninitialized capture returns nil. The
// snapshot contains no URLs, target text or credentials. No clock/source
// qualification status is exposed because one-way receipts cannot prove it.
func (b *Browser) BrowserDocumentReceiptEvidence() *BrowserDocumentReceiptEvidence {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &b.evidence
	if !b.documentMetricsEnabled || s.generation == "" || s.clock.start.IsZero() {
		return nil
	}
	out := &BrowserDocumentReceiptEvidence{Version: protocol.BrowserDocumentReceiptClosureVersion, SourceGeneration: s.generation, StartedAt: s.clock.start.UTC(), WallStepObserved: s.clock.wallStepObserved, Incomplete: s.clock.incomplete || s.incomplete || s.cancelled || s.droppedObservations > 0}
	out.Closure = b.nativeSourceClosureLocked()
	if s.clock.lifetime != nil {
		lifetime := *s.clock.lifetime
		out.Lifetime = &lifetime
	}
	for _, receipt := range s.clock.receipts {
		out.Receipts = append(out.Receipts, receipt)
	}
	sort.Slice(out.Receipts, func(i, j int) bool {
		a, b := out.Receipts[i], out.Receipts[j]
		if a.LifecycleID != b.LifecycleID {
			return a.LifecycleID < b.LifecycleID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.MeasurementID < b.MeasurementID
	})
	return out
}

// BrowserDocumentReceiptTransportVersion is trusted runtime wiring; scripts
// cannot opt a legacy receiving peer into a newer sidecar contract.
func (b *Browser) BrowserDocumentReceiptTransportVersion() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.documentMetricsEnabled {
		return 0
	}
	return b.documentProofVersion
}

func (b *Browser) BrowserDocumentReceiptMaxVersion() int {
	return protocol.BrowserDocumentReceiptClosureVersion
}
