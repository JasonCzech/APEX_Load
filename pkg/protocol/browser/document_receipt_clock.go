package browser

import (
	"sort"
	"time"
)

// BrowserDocumentReceipt is a one-way delivery observation, not a clock
// synchronization sample. Browser offsets are supplied by the target's
// document binding; host timestamps are sampled by this native process only.
// No maximum delivery delay, browser clock integrity or symmetric latency is
// implied. MeasurementID/Revision bind metric receipts to immutable raw data.
type BrowserDocumentReceipt struct {
	DocumentID          string    `json:"document_id"`
	LifecycleID         string    `json:"lifecycle_id"`
	FrameID             string    `json:"frame_id"`
	Kind                string    `json:"kind"`
	MeasurementID       string    `json:"measurement_id,omitempty"`
	Revision            uint64    `json:"revision,omitempty"`
	BrowserTimeOriginMS float64   `json:"browser_time_origin_ms"`
	BrowserOffsetMS     float64   `json:"browser_offset_ms"`
	ReceivedAt          time.Time `json:"received_at"`
	HostElapsedNS       int64     `json:"host_elapsed_ns"`
	WallDriftNS         int64     `json:"wall_drift_ns"`
}

// BrowserDocumentReceiptEvidence is a bounded, process-local acquisition
// snapshot. It is deliberately separate from the v2 document wire contract:
// callers must not silently add it to archives read by v2-only peers. It is
// not persisted or consumed by RCA yet. A missing terminal receipt may mean
// cancellation, a lost binding, or a silent source; this is NOT an expected
// source ledger, and no count of these receipts proves population coverage.
//
// SourceGeneration identifies the monotonic origin StartedAt. HostElapsedNS
// values can be compared only within that generation. WallStepObserved latches
// a sampled absolute wall/monotonic divergence greater than 100 ms. False does
// not prove clock continuity between samples, bound drift, or qualify a map
// to the controller clock. Raw browser measurement windows stay unchanged.
type BrowserDocumentReceiptEvidence struct {
	Version          int                      `json:"version"`
	SourceGeneration string                   `json:"source_generation"`
	StartedAt        time.Time                `json:"started_at"`
	WallStepObserved bool                     `json:"wall_step_observed"`
	Incomplete       bool                     `json:"incomplete"`
	Receipts         []BrowserDocumentReceipt `json:"receipts"`
}

const maxBrowserDocumentReceipts = browserDocumentLimit * 7 // begin, five metrics, end per lifecycle
const browserDocumentWallStepThreshold = 100 * time.Millisecond

type browserDocumentReceiptClock struct {
	start                        time.Time // Retains Go's process monotonic reading; never serialize it directly.
	receipts                     map[string]BrowserDocumentReceipt
	wallStepObserved, incomplete bool
}

func newBrowserDocumentReceiptClock(start time.Time) browserDocumentReceiptClock {
	return browserDocumentReceiptClock{start: start, receipts: make(map[string]BrowserDocumentReceipt)}
}

// All clock methods run under Browser.mu. There are no timers, native calls,
// additional target probes, or asynchronous workers to join during teardown.
func (c *browserDocumentReceiptClock) observe(now time.Time) (elapsed, drift time.Duration) {
	if c.start.IsZero() {
		c.incomplete = true
		return 0, 0
	}
	return c.observeSample(now, now.Sub(c.start))
}

func (c *browserDocumentReceiptClock) observeSample(now time.Time, elapsed time.Duration) (time.Duration, time.Duration) {
	drift := now.UTC().Sub(c.start.UTC()) - elapsed
	if elapsed < 0 {
		c.incomplete = true
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
	out := &BrowserDocumentReceiptEvidence{Version: 1, SourceGeneration: s.generation, StartedAt: s.clock.start.UTC(), WallStepObserved: s.clock.wallStepObserved, Incomplete: s.clock.incomplete || s.incomplete || s.cancelled || s.droppedObservations > 0}
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
