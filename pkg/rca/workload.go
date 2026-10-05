package rca

import (
	"math"
	"sort"
)

// WorkloadKeyVersion is the shape of WorkloadKey written into run provenance.
const WorkloadKeyVersion = 1

// Arrival models. A closed model offers VUsers; an open model offers a target
// arrival rate regardless of how fast the system responds.
const (
	ArrivalClosed  = "closed"
	ArrivalOpen    = "open"
	ArrivalUnknown = "unknown"
)

// Offered-load units.
const (
	LoadUnitVUsers = "vusers"
	LoadUnitTPS    = "tps"
)

// Key sources. A frozen key was captured from the compiled run at start; a
// derived key was rebuilt later from recorded provenance and may lack fields.
const (
	WorkloadKeyFrozen  = "frozen"
	WorkloadKeyDerived = "derived"
)

// Qualification statuses.
const (
	BaselineQualified   = "qualified"
	BaselinePartial     = "partial"
	BaselineUnqualified = "unqualified"
)

// Qualification reasons. Unqualified reasons mean the runs offered different
// load; partial reasons mean the load matched but something that can move the
// result changed, or could not be confirmed.
const (
	ReasonArrivalModelChanged    = "arrival_model_changed"
	ReasonLevelsChanged          = "levels_changed"
	ReasonOfferedLoadChanged     = "offered_load_changed"
	ReasonMixChanged             = "mix_changed"
	ReasonPacingChanged          = "pacing_changed"
	ReasonScriptChanged          = "script_changed"
	ReasonConfigChanged          = "config_changed"
	ReasonPopulationChanged      = "population_changed"
	ReasonWorkloadUnrecorded     = "workload_unrecorded"
	ReasonArrivalModelUnrecorded = "arrival_model_unrecorded"
	ReasonScriptUnrecorded       = "script_unrecorded"
	ReasonConfigUnrecorded       = "config_unrecorded"
)

// Qualification thresholds from the plan: offered load within 5% per level
// and a planned-mix L1 distance of at most 0.05.
const (
	OfferedLoadTolerance = 0.05
	MixDistanceTolerance = 0.05
)

// WorkloadKey identifies the load a run offered, taken from its frozen
// configuration rather than from what it achieved.
type WorkloadKey struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
	// Groups is keyed by scenario group name.
	Groups map[string]WorkloadGroup `json:"groups"`
	// RuntimeDigest covers runtime configuration other than pacing and think
	// time, which are compared separately per group.
	RuntimeDigest string `json:"runtime_digest,omitempty"`
	// Population is the measured population (transaction or action). It is set
	// by the comparison, not stored with the run.
	Population string `json:"population,omitempty"`
}

// WorkloadGroup is one group's planned offered load.
type WorkloadGroup struct {
	ArrivalModel string `json:"arrival_model"`
	Unit         string `json:"unit"`
	// Levels are the planned plateaus in order, in Unit. A schedule that holds
	// one load has one level.
	Levels []float64 `json:"levels"`
	// Pacing is a canonical description of the pacing and think-time policy.
	Pacing       string `json:"pacing,omitempty"`
	ScriptDigest string `json:"script_digest,omitempty"`
}

// WorkloadQualification says whether a baseline offered the same load as the
// current run. Achieved throughput is an outcome and never a qualifier.
type WorkloadQualification struct {
	Status             string              `json:"status"`
	Reasons            []string            `json:"reasons"`
	MixDistance        *float64            `json:"mix_distance,omitempty"`
	AchievedThroughput *AchievedThroughput `json:"achieved_throughput,omitempty"`
}

// AchievedThroughput reports both runs' achieved transaction rate.
type AchievedThroughput struct {
	CurrentTPS  float64 `json:"current_tps"`
	BaselineTPS float64 `json:"baseline_tps"`
}

// Recorded reports whether the key holds any offered-load evidence.
func (k WorkloadKey) Recorded() bool {
	return k.Version > 0 && len(k.Groups) > 0
}

// Mix returns each group's share of the planned peak offered load. Shares are
// computed within each unit, so VUser and rate groups never share a total.
func (k WorkloadKey) Mix() map[string]float64 {
	totals := map[string]float64{}
	peaks := map[string]float64{}
	for name, group := range k.Groups {
		peak := 0.0
		for _, level := range group.Levels {
			if finite(level) && level > peak {
				peak = level
			}
		}
		peaks[name] = peak
		totals[group.Unit] += peak
	}
	mix := make(map[string]float64, len(peaks))
	for name, peak := range peaks {
		if total := totals[k.Groups[name].Unit]; total > 0 {
			mix[name] = peak / total
		}
	}
	return mix
}

// QualifyBaseline compares a candidate baseline's workload with the current
// run's. A missing or incomplete key can make a baseline partial, never
// qualified.
func QualifyBaseline(current, candidate WorkloadKey) WorkloadQualification {
	result := WorkloadQualification{Status: BaselineQualified, Reasons: []string{}}
	unqualified, partial := map[string]bool{}, map[string]bool{}
	if !current.Recorded() || !candidate.Recorded() {
		partial[ReasonWorkloadUnrecorded] = true
		return finishQualification(result, unqualified, partial)
	}
	if current.Population != candidate.Population {
		unqualified[ReasonPopulationChanged] = true
	}
	names := map[string]bool{}
	for name := range current.Groups {
		names[name] = true
	}
	for name := range candidate.Groups {
		names[name] = true
	}
	for name := range names {
		a, aok := current.Groups[name]
		b, bok := candidate.Groups[name]
		if !aok || !bok {
			// A group present in only one run moves the mix; the distance below
			// decides whether that is enough to disqualify.
			continue
		}
		switch {
		case a.ArrivalModel == ArrivalUnknown || b.ArrivalModel == ArrivalUnknown:
			partial[ReasonArrivalModelUnrecorded] = true
		case a.ArrivalModel != b.ArrivalModel || a.Unit != b.Unit:
			unqualified[ReasonArrivalModelChanged] = true
			continue
		}
		if len(a.Levels) != len(b.Levels) {
			unqualified[ReasonLevelsChanged] = true
		} else {
			for i := range a.Levels {
				if !withinTolerance(a.Levels[i], b.Levels[i], OfferedLoadTolerance) {
					unqualified[ReasonOfferedLoadChanged] = true
				}
			}
		}
		if a.Pacing != b.Pacing {
			partial[ReasonPacingChanged] = true
		}
		switch {
		case a.ScriptDigest == "" || b.ScriptDigest == "":
			partial[ReasonScriptUnrecorded] = true
		case a.ScriptDigest != b.ScriptDigest:
			partial[ReasonScriptChanged] = true
		}
	}
	distance := mixDistance(current.Mix(), candidate.Mix())
	result.MixDistance = &distance
	if distance > MixDistanceTolerance+1e-9 {
		unqualified[ReasonMixChanged] = true
	}
	switch {
	case current.RuntimeDigest == "" || candidate.RuntimeDigest == "":
		partial[ReasonConfigUnrecorded] = true
	case current.RuntimeDigest != candidate.RuntimeDigest:
		partial[ReasonConfigChanged] = true
	}
	return finishQualification(result, unqualified, partial)
}

func finishQualification(result WorkloadQualification, unqualified, partial map[string]bool) WorkloadQualification {
	for reason := range unqualified {
		result.Reasons = append(result.Reasons, reason)
	}
	for reason := range partial {
		result.Reasons = append(result.Reasons, reason)
	}
	sort.Strings(result.Reasons)
	switch {
	case len(unqualified) > 0:
		result.Status = BaselineUnqualified
	case len(partial) > 0:
		result.Status = BaselinePartial
	}
	return result
}

func mixDistance(a, b map[string]float64) float64 {
	names := map[string]bool{}
	for name := range a {
		names[name] = true
	}
	for name := range b {
		names[name] = true
	}
	distance := 0.0
	for name := range names {
		distance += math.Abs(a[name] - b[name])
	}
	return distance
}

func withinTolerance(current, candidate, tolerance float64) bool {
	if !finite(current) || !finite(candidate) {
		return false
	}
	return math.Abs(current-candidate) <= math.Abs(current)*tolerance+1e-9
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
