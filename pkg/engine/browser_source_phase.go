package engine

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"log/slog"
)

// Called only under mge.mu. Dynamic populations and rate-shaped workloads are
// deliberately unavailable in this first proof; equal interval samples cannot
// reconstruct the transitions between them.
func (mge *MultiGroupEngine) browserSourcePhaseLocked(steady bool) {
	if !steady && mge.browserProofPhaseActive && (mge.baseCtx != nil && mge.baseCtx.Err() != nil || mge.loopCtx != nil && mge.loopCtx.Err() != nil) {
		mge.invalidateBrowserSourcePhaseLocked()
		return
	}
	if !steady && !mge.browserProofPhaseActive {
		return
	}
	total := 0
	if steady {
		if mge.paused || mge.initialRampInProgress || !mge.controlsReady || mge.browserProofDynamic {
			return
		}
		for _, g := range mge.groups {
			if isOpenWorkload(g.Script) || g.ThroughputCapEnabled {
				return
			}
			total += g.VUsers
		}
		if total <= 0 || mge.runningVUserCount() != total {
			return
		}
	}
	seen := map[*protocol.BrowserSourceJournal]bool{}
	for _, g := range mge.groups {
		if g.Script == nil || !dsl.BrowserDocumentCaptureRequested(g.Script) || g.BrowserDocumentProofVersion != 0 && g.BrowserDocumentProofVersion < 3 {
			continue
		}
		provider, ok := g.ResultSink.(protocol.BrowserSourceJournalProvider)
		if !ok {
			continue
		}
		journal, err := provider.BrowserDocumentSourceJournal()
		if err != nil {
			slog.Warn("browser source phase unavailable", "error", err)
			continue
		}
		if journal == nil || seen[journal] {
			continue
		}
		seen[journal] = true
		if err = journal.Phase(steady, total); err != nil {
			slog.Warn("browser source phase publication failed", "error", err)
		}
	}
	mge.browserProofPhaseActive = steady && len(seen) > 0
}

// An unexpected VUser exit has no pre-departure load fence. Invalidate proof
// instead of backdating an inferred phase boundary after its teardown.
func (mge *MultiGroupEngine) browserDocumentLoadLeaving(unexpected bool) {
	mge.mu.Lock()
	defer mge.mu.Unlock()
	if !unexpected && (!mge.controlsReady || mge.browserProofDynamic) {
		return
	}
	mge.invalidateBrowserSourcePhaseLocked()
}

func (mge *MultiGroupEngine) invalidateBrowserSourcePhaseLocked() {
	for _, g := range mge.groups {
		if provider, ok := g.ResultSink.(protocol.BrowserSourceJournalProvider); ok {
			j, err := provider.BrowserDocumentSourceJournal()
			if err == nil && j != nil {
				_ = j.Invalidate()
			}
		}
	}
	mge.browserProofPhaseActive = false
	mge.browserProofDynamic = true
}
