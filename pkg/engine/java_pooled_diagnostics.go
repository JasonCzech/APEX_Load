package engine

import (
	"context"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// JavaHostEvidence contains bounded, value-free host diagnostics. Slot IDs are
// admission identities, not proof of thread ownership. Events are a rolling
// tail; Count includes deduplicated events observed before that tail was evicted.
// JFR delivery is asynchronous: the final unflushed tail is never claimed complete.
type JavaHostEvidence struct {
	HostID          string                    `json:"host_id"`
	SlotIDs         []string                  `json:"slot_ids"`
	Observations    int64                     `json:"observations"`
	ReadFailures    int64                     `json:"read_failures"`
	OutputBytes     [3]int                    `json:"output_bytes"`
	OutputDropped   [3]uint64                 `json:"output_dropped"`
	OutputTruncated bool                      `json:"output_truncated"`
	GCStatus        string                    `json:"gc_status"`
	GCPauseCount    uint64                    `json:"gc_pause_count"`
	GCMissing       uint64                    `json:"gc_missing"`
	GCTail          []javarunner.GCPauseEvent `json:"gc_tail"`
	UnflushedTail   bool                      `json:"unflushed_tail"`
}

type javaHostEvidenceCollector struct {
	mu        sync.Mutex
	evidence  JavaHostEvidence
	remaining int
	last      uint64
	read      func(context.Context, string) (javarunner.HostDiagnostics, error)
}

// Caller holds mu, also serializing the final diagnostic read with slot closure.
func (h *javaHostEvidenceCollector) collect(ctx context.Context) {
	if h.remaining == 0 {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	d, err := h.read(callCtx, h.evidence.HostID)
	if err != nil || !javarunner.ValidHostDiagnostics(d) {
		h.evidence.ReadFailures++
		return
	}
	e := &h.evidence
	e.Observations++
	for i, c := range []javarunner.DiagnosticCapture{d.Stdout, d.Stderr, d.Java} {
		e.OutputBytes[i] = len(c.Bytes)
		e.OutputDropped[i] = c.Dropped
		e.OutputTruncated = e.OutputTruncated || c.Truncated
	}
	if d.GCPauses == nil {
		e.GCStatus = "unsupported"
		return
	}
	latest := d.GCPauses.Evicted + uint64(len(d.GCPauses.Events))
	if latest < h.last {
		e.ReadFailures++
		e.GCStatus = "failed"
		return
	}
	e.GCStatus = d.GCPauses.Status
	for _, event := range d.GCPauses.Events {
		if event.Sequence <= h.last {
			continue
		}
		e.GCMissing += event.Sequence - h.last - 1
		h.last = event.Sequence
		e.GCPauseCount++
	}
	e.GCTail = append(e.GCTail[:0], d.GCPauses.Events...)
}

func (p *javaPooledPilot) startDiagnostics(ctx context.Context) {
	if len(p.diagnostics) == 0 {
		return
	}
	ctx, p.diagnosticsCancel = context.WithCancel(ctx)
	p.diagnosticsDone = make(chan struct{})
	go func() {
		defer close(p.diagnosticsDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		// One worker bounds both outstanding requests and capture allocations. Busy
		// hosts are skipped; final closure does its own read before reaping.
		for {
			for _, h := range p.diagnostics {
				if ctx.Err() != nil {
					return
				}
				if h.mu.TryLock() {
					h.collect(ctx)
					h.mu.Unlock()
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// JavaPooledDiagnostics returns a detached snapshot after Run has returned.
// It must not be called concurrently with Run. Raw Java/native output is never
// retained in evidence artifacts because it may contain workload secrets.
func (e *Engine) JavaPooledDiagnostics() []JavaHostEvidence {
	if e.pooledPilot == nil {
		return nil
	}
	result := make([]JavaHostEvidence, 0, len(e.pooledPilot.diagnostics))
	for _, h := range e.pooledPilot.diagnostics {
		h.mu.Lock()
		value := h.evidence
		value.SlotIDs = append([]string(nil), value.SlotIDs...)
		value.GCTail = append([]javarunner.GCPauseEvent(nil), value.GCTail...)
		h.mu.Unlock()
		result = append(result, value)
	}
	return result
}
