// Package signatures holds the root-cause signatures: each names the
// evidence it needs, what supports and contradicts it, the signatures it
// competes with and the check that would disprove it. The package is pure
// and knows nothing of storage, providers or HTTP.
package signatures

import (
	"math"
	"slices"
	"sort"
)

// Version identifies the signature set; it is part of every analysis's
// frozen inputs.
const Version = "signatures.v1"

// Evidence kinds: what a series measures.
const (
	KindLatency     = "latency"
	KindErrors      = "errors"
	KindThroughput  = "throughput"
	KindCPU         = "cpu"
	KindMemory      = "memory"
	KindGC          = "gc"
	KindPool        = "pool"
	KindConsumerLag = "consumer_lag"
	KindRebalance   = "rebalance"
	KindDeadLetters = "dead_letters"
	KindRetries     = "retries"
	KindScaling     = "scaling"
	KindDBWait      = "db_wait"
	KindDNS         = "dns"
	KindConnect     = "connect"
	KindTLS         = "tls"
)

// Directions an anomaly moves in.
const (
	Increase = "increase"
	Decrease = "decrease"
)

// Order of an observation's onset against the symptom's: earlier by more
// than both onsets' uncertainties, later by more than that, or neither.
const (
	Precedes  = "precedes"
	Unordered = "unordered"
	Follows   = "follows"
)

// Verdicts. A signature that cannot explain the symptom's kind is not
// applicable, which is neither evidence for nor against it.
const (
	Supported       = "supported"
	Contradicted    = "contradicted"
	MissingEvidence = "missing_evidence"
	NotApplicable   = "not_applicable"
)

// Observation is one anomaly as a signature sees it.
type Observation struct {
	AnomalyID string
	Series    string
	Kind      string
	Direction string
	PeakZ     float64
	// Order is the onset's order against the symptom.
	Order string
	// LoadCoincident means the onset fell at a load step.
	LoadCoincident bool
	// Connected means the series was reached through the run's graph from
	// the symptom's dependencies.
	Connected bool
}

// Availability says whether any series measured a kind, and whether one
// was connected to the symptom.
type Availability struct {
	Any       bool
	Connected bool
}

// Context is what a signature judges for one symptom.
type Context struct {
	Symptom Observation
	// Observations are the other anomalies in the symptom's window.
	Observations []Observation
	Available    map[string]Availability
}

// Indicator is an evidence kind and the direction that supports a
// signature.
type Indicator struct {
	Kind      string
	Direction string
}

// Signature is one root-cause pattern.
type Signature struct {
	Name string
	// Required evidence: at least one of these kinds must have been measured
	// for the signature to be judged.
	Required   []string
	Indicators []Indicator
	// Symptoms are the symptom kinds the signature can explain.
	Symptoms      []string
	Competing     []string
	Falsification string
	// Extra adds signature-specific support or contradiction.
	Extra func(Context, *Result)
}

// Result is a signature's verdict for one symptom.
type Result struct {
	Signature     string
	Verdict       string
	Supporting    []Observation
	Contradicting []Observation
	// Reasons explain contradictions that are absences: a measured indicator
	// that stayed within its baseline while nothing supported the signature.
	Reasons []string
	// Notes explain what strengthened the signature.
	Notes   []string
	Missing []string
	Score   float64
}

// Weights: an observation the graph connects counts fully, one it does not
// counts half; one that precedes the symptom counts fully, one that is
// unordered with it 0.6. An indicator that was measured on the symptom's
// path and stayed in band contradicts with weight 1.
const (
	weightUnconnected = 0.5
	weightUnordered   = 0.6
	penaltyFollows    = 0.5
	penaltyFlat       = 1.0
	penaltyFlatLoose  = 0.25
)

// Judge decides a signature for one symptom: missing evidence when none of
// its required kinds was measured; supported when indicators moved in the
// right direction no later than the symptom and outweigh what contradicts
// it; contradicted otherwise.
func (s Signature) Judge(ctx Context) Result {
	r := Result{Signature: s.Name}
	if len(s.Symptoms) > 0 && !slices.Contains(s.Symptoms, ctx.Symptom.Kind) {
		r.Verdict = NotApplicable
		return r
	}
	measured := false
	for _, kind := range s.Required {
		if ctx.Available[kind].Any {
			measured = true
		}
	}
	if !measured {
		r.Verdict, r.Missing = MissingEvidence, append([]string{}, s.Required...)
		return r
	}
	moved := map[string]bool{}
	for _, o := range ctx.Observations {
		indicator := s.indicates(o)
		if !indicator {
			continue
		}
		moved[o.Kind] = true
		if o.Order == Follows {
			r.Contradicting = append(r.Contradicting, o)
			r.Score -= penaltyFollows
			continue
		}
		r.Supporting = append(r.Supporting, o)
		r.Score += weight(o)
	}
	// A measured indicator that never moved contradicts, unless another of
	// the signature's indicators supports it: CPU pressure needs no memory
	// pressure beside it.
	for _, indicator := range s.Indicators {
		available := ctx.Available[indicator.Kind]
		if !available.Any || moved[indicator.Kind] || len(r.Supporting) > 0 {
			continue
		}
		if available.Connected {
			r.Score -= penaltyFlat
		} else {
			r.Score -= penaltyFlatLoose
		}
		r.Reasons = append(r.Reasons, indicator.Kind+" stayed within its baseline")
	}
	if s.Extra != nil {
		s.Extra(ctx, &r)
	}
	r.Reasons, r.Notes = uniqueSorted(r.Reasons), uniqueSorted(r.Notes)
	if len(r.Supporting) > 0 && r.Score > 0 {
		r.Verdict = Supported
	} else {
		r.Verdict = Contradicted
	}
	return r
}

func (s Signature) indicates(o Observation) bool {
	for _, i := range s.Indicators {
		if i.Kind == o.Kind && (i.Direction == "" || i.Direction == o.Direction) {
			return true
		}
	}
	return false
}

// weight scores one supporting observation, larger for stronger anomalies.
func weight(o Observation) float64 {
	w := 1 + math.Min(math.Abs(o.PeakZ), 20)/20
	if !o.Connected {
		w *= weightUnconnected
	}
	if o.Order == Unordered {
		w *= weightUnordered
	}
	return w
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	return slices.Compact(values)
}

// All is the signature set, in a fixed order.
func All() []Signature {
	return []Signature{
		ResourcePressure, GarbageCollection, PoolExhaustion, ConsumerLag, Rebalance,
		DeadLetters, Retries, ColdStart, DatabaseWait, NetworkPhase,
	}
}

// symptomsOfService are the symptoms a service-side cause explains.
var symptomsOfService = []string{KindLatency, KindErrors, KindThroughput}
