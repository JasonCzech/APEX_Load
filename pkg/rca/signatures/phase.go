package signatures

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Request phases a transaction's extra time is split into: the archived
// request timing's sequential phases (server is TTFB minus set-up, download
// is duration minus TTFB).
const (
	PhaseDNS      = "dns"
	PhaseConnect  = "connect"
	PhaseTLS      = "tls"
	PhaseServer   = "server"
	PhaseDownload = "download"
)

// Phase sources: where a symptom transaction's extra time was measured.
const (
	// PhaseSourceTail compares the run's slowest instances (above its P95)
	// with its typical ones (at or below its P50).
	PhaseSourceTail = "tail"
	// PhaseSourceBaseline compares the run's mean instance with the
	// baseline run's.
	PhaseSourceBaseline = "baseline"
)

// PhaseGrowth is one phase's share of a transaction's extra time.
type PhaseGrowth struct {
	Phase   string
	DeltaMs float64
	// Share is DeltaMs over the transaction's whole extra time.
	Share float64
}

// PhaseShift is where one symptom transaction's extra time went, as its
// time anatomy splits it. Only an attribution with enough samples and a
// material change becomes a shift; the caller applies those bounds.
type PhaseShift struct {
	Transaction string
	Source      string
	// DeltaMs is the transaction's whole extra time: tail minus median, or
	// the run's mean minus the baseline's.
	DeltaMs float64
	// Grown are the phases that carried a material share of it, largest
	// first.
	Grown []PhaseGrowth
	// Dominant is the phase that carried most of it, or "" when none did.
	Dominant string
	// HandshakesUp says connections were set up more often than in the
	// baseline: reuse fell or TLS was observed on more requests, so more
	// full handshakes ran.
	HandshakesUp bool
	// HandshakeDetail states the reuse or TLS-share change behind
	// HandshakesUp, with its numbers.
	HandshakeDetail string
}

// PhaseIndicator is a request phase whose growth corroborates a signature.
// NeedsHandshakes limits it to growth that came with more handshakes.
type PhaseIndicator struct {
	Phase           string
	NeedsHandshakes bool
}

// Phase scoring: a symptom transaction whose extra time grew in one of a
// signature's phases adds bonusPhase to a signature its other evidence
// already supports; one whose extra time was dominated by a phase the
// signature cannot explain costs it penaltyPhase. Attribution alone never
// supports a signature: it has no onset and no graph path.
const (
	bonusPhase   = 0.5
	penaltyPhase = 0.5
)

// phaseLabels name the phases in notes and reasons.
var phaseLabels = map[string]string{PhaseDNS: "DNS", PhaseConnect: "connect", PhaseTLS: "TLS", PhaseServer: "server", PhaseDownload: "download"}

// PhaseLabel names a phase for a sentence.
func PhaseLabel(phase string) string {
	if label, ok := phaseLabels[phase]; ok {
		return label
	}
	return phase
}

// describe states a phase shift's numbers: "the P95 tail of checkout is
// +420 ms over its median, +360 ms (86%) of it in server".
func (p PhaseShift) describe(g PhaseGrowth) string {
	what := "the P95 tail of " + p.Transaction + " is " + signedMs(p.DeltaMs) + " over its median"
	if p.Source == PhaseSourceBaseline {
		what = p.Transaction + " is " + signedMs(p.DeltaMs) + " slower on average than in the baseline"
	}
	return fmt.Sprintf("%s, %s (%.0f%%) of it in %s", what, signedMs(g.DeltaMs), math.Round(g.Share*100), PhaseLabel(g.Phase))
}

func signedMs(v float64) string {
	return fmt.Sprintf("%+.0f ms", math.Round(v))
}

// judgePhases applies a symptom transaction's phase shift to a signature's
// result. A change dominated by a phase the signature cannot explain weakens
// it, whatever else grew. Otherwise growth in one of its phases strengthens
// it, but only when it is already supported. It runs after every other
// score change, just before the verdict.
func (s Signature) judgePhases(shift *PhaseShift, r *Result) {
	if shift == nil || len(s.Phases) == 0 {
		return
	}
	if shift.Dominant != "" {
		if _, ok := s.explains(shift.Dominant, shift); !ok {
			for _, g := range shift.Grown {
				if g.Phase != shift.Dominant {
					continue
				}
				r.Score -= penaltyPhase
				r.PhaseContradicted = true
				names := make([]string, 0, len(s.Phases))
				for _, i := range s.Phases {
					name := PhaseLabel(i.Phase)
					if i.NeedsHandshakes {
						name += " with more handshakes"
					}
					if !slices.Contains(names, name) {
						names = append(names, name)
					}
				}
				r.Reasons = append(r.Reasons, shift.describe(g)+", not in "+strings.Join(names, " or "))
				return
			}
			return
		}
	}
	for _, g := range shift.Grown {
		indicator, ok := s.explains(g.Phase, shift)
		if !ok {
			continue
		}
		// Only a result that is already supported (some support and a
		// positive score) is strengthened: the bonus never turns a
		// contradicted signature into a hypothesis.
		if len(r.Supporting) == 0 || r.Score <= 0 {
			return
		}
		r.Score += bonusPhase
		r.PhaseSupported = true
		note := shift.describe(g)
		if indicator.NeedsHandshakes {
			note += "; " + shift.HandshakeDetail
		}
		r.Notes = append(r.Notes, note)
		return
	}
}

// explains returns the signature's indicator for a phase, honouring its
// handshake condition: TLS growth without more handshakes is not handshake
// CPU.
func (s Signature) explains(phase string, shift *PhaseShift) (PhaseIndicator, bool) {
	for _, indicator := range s.Phases {
		if indicator.Phase == phase && (!indicator.NeedsHandshakes || shift.HandshakesUp) {
			return indicator, true
		}
	}
	return PhaseIndicator{}, false
}
