package rca

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

// Phase attribution bounds. A transaction's time anatomy becomes evidence
// only with enough samples on both sides and a material change; anything
// less says nothing, rather than a little.
const (
	// PhaseMinInstances is the fewest instances either side of a
	// comparison needs: the report's tail-attribution minimum, applied to
	// the baseline comparison's decompositions as well.
	PhaseMinInstances = 40
	// PhaseMinTailInstances and PhaseMinMedianInstances are the joined
	// instances each tail band needs, as the report's tail attribution
	// requires.
	PhaseMinTailInstances   = 3
	PhaseMinMedianInstances = 10
	// PhaseMinJoinCoverage is the share of each side's instances the means
	// must cover; a biased subset's split is not the transaction's.
	PhaseMinJoinCoverage = 0.8
	// PhaseMinDeltaMs and PhaseMinDeltaRatio bound the whole extra time:
	// at least 20 ms and a fifth of the reference mean.
	PhaseMinDeltaMs    = 20.0
	PhaseMinDeltaRatio = 0.2
	// PhaseMinGrowthMs and PhaseMinShare bound one phase's growth: at
	// least 10 ms and a quarter of the extra time.
	PhaseMinGrowthMs = 10.0
	PhaseMinShare    = 0.25
	// PhaseDominantShare is the share a phase must carry to dominate.
	PhaseDominantShare = 0.5
	// PhaseHandshakeShiftPct is the percentage-point change in connection
	// reuse or TLS-observed share that says handshakes became more frequent.
	PhaseHandshakeShiftPct = 10.0
	// MaxPhaseAttributions bounds the attributions one assessment reads.
	MaxPhaseAttributions = 512
)

// LimitationPhaseTransfer: a symptom transaction's extra time went mostly
// to download, which points at bandwidth or payload size, and no signature
// measures either.
const LimitationPhaseTransfer = "phase_transfer_unmodeled"

// PhaseAttribution is one business transaction's time anatomy, as the
// assessment reads it: where its extra time went, against its own median
// (tail) or the baseline run (baseline).
type PhaseAttribution struct {
	Transaction string `json:"transaction"`
	Source      string `json:"source"`
	// Population is how many instances the comparison drew from: every
	// instance of the transaction for a tail attribution.
	Population int64 `json:"population"`
	// Instances and ReferenceInstances are the joined instances the means
	// cover: the tail band and the median band, or the run and the baseline.
	Instances          int64 `json:"instances"`
	ReferenceInstances int64 `json:"reference_instances"`
	// JoinCoverage and ReferenceJoinCoverage are the share of each side's
	// instances the means cover (0..1).
	JoinCoverage          float64 `json:"join_coverage"`
	ReferenceJoinCoverage float64 `json:"reference_join_coverage"`
	// ReferenceMs is the reference side's mean duration; DeltaMs the
	// difference the phases split.
	ReferenceMs float64 `json:"reference_ms"`
	DeltaMs     float64 `json:"delta_ms"`
	// Phases is each request phase's share of DeltaMs, in ms.
	Phases map[string]float64 `json:"phases"`
	// Connection reuse and TLS-observed share (percent), each side, when
	// known: a baseline comparison only.
	ReusePct                *float64    `json:"reuse_pct,omitempty"`
	ReferenceReusePct       *float64    `json:"reference_reuse_pct,omitempty"`
	TLSObservedPct          *float64    `json:"tls_observed_pct,omitempty"`
	ReferenceTLSObservedPct *float64    `json:"reference_tls_observed_pct,omitempty"`
	Evidence                EvidenceRef `json:"evidence"`
}

var attributedPhases = []string{signatures.PhaseDNS, signatures.PhaseConnect, signatures.PhaseTLS, signatures.PhaseServer, signatures.PhaseDownload}

// Shift turns an attribution into a phase shift, or reports false when it
// is too small or too thin to be evidence.
func (p PhaseAttribution) Shift() (signatures.PhaseShift, bool) {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	switch {
	case p.Transaction == "" || (p.Source != signatures.PhaseSourceTail && p.Source != signatures.PhaseSourceBaseline):
		return signatures.PhaseShift{}, false
	case p.Instances < 1 || p.ReferenceInstances < 1:
		return signatures.PhaseShift{}, false
	case p.Source == signatures.PhaseSourceTail && (p.Instances < PhaseMinTailInstances || p.ReferenceInstances < PhaseMinMedianInstances || p.Population < PhaseMinInstances):
		return signatures.PhaseShift{}, false
	case p.Source == signatures.PhaseSourceBaseline && (p.Instances < PhaseMinInstances || p.ReferenceInstances < PhaseMinInstances):
		return signatures.PhaseShift{}, false
	case p.JoinCoverage < PhaseMinJoinCoverage || p.ReferenceJoinCoverage < PhaseMinJoinCoverage:
		return signatures.PhaseShift{}, false
	case !finite(p.DeltaMs) || !finite(p.ReferenceMs) || p.DeltaMs < PhaseMinDeltaMs || p.DeltaMs < PhaseMinDeltaRatio*p.ReferenceMs:
		return signatures.PhaseShift{}, false
	}
	shift := signatures.PhaseShift{Transaction: p.Transaction, Source: p.Source, DeltaMs: p.DeltaMs}
	for _, phase := range attributedPhases {
		delta := p.Phases[phase]
		if !finite(delta) || delta < PhaseMinGrowthMs || delta/p.DeltaMs < PhaseMinShare {
			continue
		}
		shift.Grown = append(shift.Grown, signatures.PhaseGrowth{Phase: phase, DeltaMs: delta, Share: math.Min(1, delta/p.DeltaMs)})
	}
	sort.SliceStable(shift.Grown, func(i, j int) bool { return shift.Grown[i].DeltaMs > shift.Grown[j].DeltaMs })
	if len(shift.Grown) == 0 {
		return signatures.PhaseShift{}, false
	}
	if shift.Grown[0].Share >= PhaseDominantShare {
		shift.Dominant = shift.Grown[0].Phase
	}
	if p.Source == signatures.PhaseSourceBaseline {
		var details []string
		if p.ReusePct != nil && p.ReferenceReusePct != nil && *p.ReferenceReusePct-*p.ReusePct >= PhaseHandshakeShiftPct {
			details = append(details, fmt.Sprintf("connection reuse fell from %.0f%% to %.0f%%", *p.ReferenceReusePct, *p.ReusePct))
		}
		if p.TLSObservedPct != nil && p.ReferenceTLSObservedPct != nil && *p.TLSObservedPct-*p.ReferenceTLSObservedPct >= PhaseHandshakeShiftPct {
			details = append(details, fmt.Sprintf("TLS handshakes ran on %.0f%% of requests, up from %.0f%%", *p.TLSObservedPct, *p.ReferenceTLSObservedPct))
		}
		if len(details) > 0 {
			shift.HandshakesUp = true
			shift.HandshakeDetail = strings.Join(details, " and ") + ", so more full handshakes ran (resumption or reuse lost)"
		}
	}
	return shift, true
}

// phaseShifts indexes the attributions that are evidence by transaction
// node: the baseline comparison when one qualifies, else the tail.
func phaseShifts(attributions []PhaseAttribution) (map[string]signatures.PhaseShift, map[string]EvidenceRef) {
	shifts, refs := map[string]signatures.PhaseShift{}, map[string]EvidenceRef{}
	if len(attributions) > MaxPhaseAttributions {
		attributions = attributions[:MaxPhaseAttributions]
	}
	for _, a := range attributions {
		shift, ok := a.Shift()
		if !ok {
			continue
		}
		node := TransactionNodeID(a.Transaction)
		if existing, seen := shifts[node]; seen && (existing.Source == signatures.PhaseSourceBaseline || shift.Source != signatures.PhaseSourceBaseline) {
			continue
		}
		shifts[node] = shift
		ref := a.Evidence
		if ref.Kind == "" {
			ref.Kind = "time_anatomy"
		}
		if ref.ID == "" {
			ref.ID = stableID("evidence", ref.Kind, ref.Locator, a.Transaction, a.Source)
		}
		refs[node] = ref
	}
	return shifts, refs
}

// symptomPhaseShift is the phase shift of the transaction a symptom's
// series measures, if any.
func symptomPhaseShift(s Series, shifts map[string]signatures.PhaseShift) (*signatures.PhaseShift, string) {
	if !strings.HasPrefix(s.ID, NodeTransaction+"/") {
		return nil, ""
	}
	for _, node := range seriesNodes(s) {
		if shift, ok := shifts[node]; ok {
			return &shift, node
		}
	}
	return nil, ""
}

// transferLimitation names the symptom transactions whose extra time went
// mostly to download.
func transferLimitation(transactions []string) (Limitation, bool) {
	if len(transactions) == 0 {
		return Limitation{}, false
	}
	transactions = slices.Compact(slices.Sorted(slices.Values(transactions)))
	return Limitation{Code: LimitationPhaseTransfer, Detail: fmt.Sprintf("the extra time of %s went mostly to download, which points at bandwidth or payload size; no signature measures either", strings.Join(transactions, ", "))}, true
}
