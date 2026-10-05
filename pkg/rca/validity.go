package rca

import (
	"math"
	"sort"
	"time"
)

// ValidityRulesVersion names the rule set and thresholds a verdict used. The
// thresholds are the plan's starting values; the evaluation corpus (WP3.9)
// recalibrates them under a new version.
const ValidityRulesVersion = "validity.v1"

// Validity verdicts. A run is valid when its load generators and telemetry
// could carry the load it reports; missing evidence never yields valid.
const (
	ValidityValid   = "valid"
	ValiditySuspect = "suspect"
	ValidityInvalid = "invalid"
	ValidityUnknown = "unknown"
)

// Validity rules: the reason a window or run left valid.
const (
	RuleGeneratorCPU          = "generator_cpu"
	RuleGeneratorSteal        = "generator_steal"
	RuleGeneratorThrottled    = "generator_throttled"
	RuleGeneratorOOMKill      = "generator_oom_kill"
	RuleDroppedResults        = "dropped_results"
	RuleShedArrivals          = "shed_arrivals"
	RuleDataExhausted         = "data_exhausted"
	RuleGeneratorStatsMissing = "generator_stats_missing"
	RuleEvidenceIncomplete    = "evidence_incomplete"
	RuleNoIntervals           = "no_intervals"
)

// Anomalies are outcomes worth investigating that do not make a run invalid.
const (
	// AnomalyOpenModelShortfall: an open model missed its arrival target while
	// its generators were healthy, so the system under test fell behind.
	AnomalyOpenModelShortfall = "open_model_shortfall"
	// AnomalyClosedModelSlowdown: a closed model's throughput fell while
	// latency rose at a steady load, which is a system-under-test outcome.
	AnomalyClosedModelSlowdown = "closed_model_slowdown"
)

// Validity limitations: evidence a rule could not use.
const (
	LimitationNoSteadyLevel               = "no_steady_level"
	LimitationGeneratorIdentityUnrecorded = "generator_identity_unrecorded"
	LimitationThrottlePeriodsUnrecorded   = "throttle_periods_unrecorded"
	LimitationDroppedResultsUnrecorded    = "dropped_results_unrecorded"
	LimitationShedArrivalsUnrecorded      = "shed_arrivals_unrecorded"
	LimitationDataExhaustionUnrecorded    = "data_exhaustion_unrecorded"
	LimitationArrivalModelUnrecorded      = "arrival_model_unrecorded"
	LimitationWindowsTruncated            = "windows_truncated"
)

// ArrivalMixed is a run whose groups use both closed and open models.
const ArrivalMixed = "mixed"

// MaxValidityWindows bounds the windows a verdict lists.
const MaxValidityWindows = 200

// ValidityRules are the thresholds a verdict applies.
type ValidityRules struct {
	// CPUPct, StealPct and ThrottledFraction make a generator suspect when held
	// for SustainIntervals consecutive intervals.
	CPUPct            float64 `json:"cpu_pct"`
	StealPct          float64 `json:"steal_pct"`
	ThrottledFraction float64 `json:"throttled_fraction"`
	SustainIntervals  int     `json:"sustain_intervals"`
	// DroppedFraction of an interval's results makes it invalid when exceeded.
	DroppedFraction float64 `json:"dropped_fraction"`
	// MissingFraction of steady intervals without generator stats makes the
	// run unknown when exceeded.
	MissingFraction float64 `json:"missing_fraction"`
}

// DefaultValidityRules are the plan's initial thresholds.
var DefaultValidityRules = ValidityRules{CPUPct: 85, StealPct: 10, ThrottledFraction: 0.10, SustainIntervals: 3, DroppedFraction: 0.001, MissingFraction: 0.10}

// ValidityInterval is one archived interval of the whole run.
type ValidityInterval struct {
	Start time.Time
	End   time.Time
	// Attempts is the transactions attempted in the interval.
	Attempts int64
	// Results is every protocol result recorded in the interval, the unit
	// dropped results are counted in; nil falls back to Attempts.
	Results *int64
	// DroppedResults and ShedArrivals are cumulative counters at the
	// interval's end; nil when the run did not record them.
	DroppedResults *int64
	ShedArrivals   *int64
	TPS            float64
	AvgLatencyMs   float64
	TargetMissed   bool
}

// GeneratorSample is one load generator's resource sample.
type GeneratorSample struct {
	Generator string
	At        time.Time
	CPUPct    float64
	StealPct  float64
	// ThrottledPeriods of Periods CFS periods were throttled since the
	// previous sample; Periods is zero when the platform did not record it.
	ThrottledPeriods uint64
	Periods          uint64
	OOMKills         uint64
}

// TimeRange is a closed span of time.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// ValidityInput is the frozen evidence a verdict reads.
type ValidityInput struct {
	// ArrivalModel is closed, open, mixed or unknown.
	ArrivalModel string
	Intervals    []ValidityInterval
	// Generators lists the generators expected to report. Empty means the
	// generators that appear in Samples.
	Generators []string
	Samples    []GeneratorSample
	// GeneratorIdentityRecorded is false when samples from several generators
	// cannot be told apart; the rules then use the worst sample per interval.
	GeneratorIdentityRecorded bool
	// SteadyLevels are the spans where load held steady. Empty means none was
	// measured, and every interval is judged.
	SteadyLevels []TimeRange
	// DataExhausted is nil when data-pool exhaustion was not recorded.
	DataExhausted *bool
	// EvidenceGaps name missing run evidence; any gap keeps the run from valid.
	EvidenceGaps []string
}

// ValidityFinding is one rule that fired in a window.
type ValidityFinding struct {
	Rule      string  `json:"rule"`
	Generator string  `json:"generator,omitempty"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

// ValidityWindow is a judged interval.
type ValidityWindow struct {
	Start    time.Time         `json:"start"`
	End      time.Time         `json:"end"`
	Verdict  string            `json:"verdict"`
	Findings []ValidityFinding `json:"findings"`
}

// ValidityAnomaly is a span worth investigating that leaves the run valid.
type ValidityAnomaly struct {
	Kind  string    `json:"kind"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// Value is the throughput change in percent for a slowdown, or the number
	// of short intervals for a shortfall.
	Value float64 `json:"value"`
}

// Validity is a run's validity verdict.
type Validity struct {
	RulesVersion string        `json:"rules_version"`
	Rules        ValidityRules `json:"rules"`
	Verdict      string        `json:"verdict"`
	// Reasons are the rules behind the verdict.
	Reasons []string `json:"reasons"`
	// Windows are the judged intervals that were not valid, earliest first.
	Windows []ValidityWindow `json:"windows"`
	// IntervalsJudged counts the intervals the verdict covers: those that
	// overlap a steady level, or all of them when none was measured.
	IntervalsJudged int `json:"intervals_judged"`
	// MissingFraction is the share of judged intervals without stats from
	// every expected generator.
	MissingFraction float64           `json:"missing_fraction"`
	Anomalies       []ValidityAnomaly `json:"anomalies"`
	Limitations     []string          `json:"limitations"`
}

// ValidityRank orders verdicts from best to worst. Evidence of a problem
// outranks missing evidence, and missing evidence outranks valid.
func ValidityRank(verdict string) int {
	switch verdict {
	case ValidityValid:
		return 0
	case ValidityUnknown:
		return 1
	case ValiditySuspect:
		return 2
	case ValidityInvalid:
		return 3
	}
	return 1
}

func worseVerdict(a, b string) string {
	if ValidityRank(b) > ValidityRank(a) {
		return b
	}
	return a
}

// windowStats is one generator's worst sample values in one interval.
type windowStats struct {
	present                   bool
	cpu, steal                float64
	throttled, periods, kills uint64
}

// counterDelta is a cumulative counter's rise above its high-water mark,
// which it advances. A distributed run's counter is the sum over the
// generators whose batches reached a window, so it dips when one is late; a
// dip is not a restart, and only a new high counts. (A replaced generator,
// whose counter does restart, already fails its run.)
func counterDelta(value int64, peak *int64) int64 {
	delta := max(0, value-*peak)
	*peak = max(*peak, value)
	return delta
}

// AssessValidity judges a run's evidence with rules. It is deterministic:
// identical input gives an identical verdict.
func AssessValidity(input ValidityInput, rules ValidityRules) Validity {
	out := Validity{RulesVersion: ValidityRulesVersion, Rules: rules, Verdict: ValidityValid, Reasons: []string{}, Windows: []ValidityWindow{}, Anomalies: []ValidityAnomaly{}, Limitations: []string{}}
	limitations := map[string]bool{}
	intervals := append([]ValidityInterval(nil), input.Intervals...)
	sort.SliceStable(intervals, func(i, j int) bool { return intervals[i].End.Before(intervals[j].End) })
	if len(intervals) == 0 {
		out.Verdict, out.Reasons = ValidityUnknown, []string{RuleNoIntervals}
		return out
	}

	// The verdict covers intervals that overlap a measured steady level.
	judged := make([]bool, len(intervals))
	if len(input.SteadyLevels) == 0 {
		limitations[LimitationNoSteadyLevel] = true
		for i := range judged {
			judged[i] = true
		}
	} else {
		for i, interval := range intervals {
			for _, level := range input.SteadyLevels {
				if interval.Start.Before(level.End) && interval.End.After(level.Start) {
					judged[i] = true
					break
				}
			}
		}
	}

	// Without identities every sample is one pool, judged by its worst value.
	// Otherwise the expected generators and every generator that reported are
	// judged.
	generators := map[string]bool{}
	if input.GeneratorIdentityRecorded {
		for _, name := range input.Generators {
			generators[name] = true
		}
	}
	for _, sample := range input.Samples {
		if input.GeneratorIdentityRecorded {
			generators[sample.Generator] = true
		} else {
			generators[""] = true
		}
	}
	if !input.GeneratorIdentityRecorded && len(input.Samples) > 0 {
		limitations[LimitationGeneratorIdentityUnrecorded] = true
	}
	names := make([]string, 0, len(generators))
	for name := range generators {
		names = append(names, name)
	}
	sort.Strings(names)
	index := make(map[string]int, len(names))
	for i, name := range names {
		index[name] = i
	}
	// stats[g][i] is generator g's worst sample values in interval i. Each
	// sample lands in the interval whose end is nearest, within one interval
	// length, so generator clocks a little off the run's grid still land in
	// one interval each.
	stats := make([][]windowStats, len(names))
	for g := range stats {
		stats[g] = make([]windowStats, len(intervals))
	}
	for _, sample := range input.Samples {
		name := sample.Generator
		if !input.GeneratorIdentityRecorded {
			name = ""
		}
		i := nearestInterval(intervals, sample.At)
		if i < 0 {
			continue
		}
		s := &stats[index[name]][i]
		s.present = true
		s.cpu = math.Max(s.cpu, finiteOrZero(sample.CPUPct))
		s.steal = math.Max(s.steal, finiteOrZero(sample.StealPct))
		s.throttled += sample.ThrottledPeriods
		s.periods += sample.Periods
		s.kills += sample.OOMKills
	}

	// A generator is missing from an interval inside the span it reported
	// over; one added or removed mid-run is not missing outside it. An
	// expected generator that never reported is missing throughout.
	first, last := make([]int, len(names)), make([]int, len(names))
	for g := range names {
		first[g], last[g] = 0, len(intervals)-1
		for i := range intervals {
			if stats[g][i].present {
				first[g] = i
				break
			}
		}
		for i := len(intervals) - 1; i >= 0; i-- {
			if stats[g][i].present {
				last[g] = i
				break
			}
		}
	}
	absent := func(g, i int) bool { return !stats[g][i].present && i >= first[g] && i <= last[g] }
	findings := make([][]ValidityFinding, len(intervals))
	missing := make([]bool, len(intervals))
	for i := range intervals {
		missing[i] = len(names) == 0
		for g := range names {
			if absent(g, i) {
				missing[i] = true
			}
		}
	}

	// Saturation must hold for SustainIntervals consecutive judged intervals
	// on one generator; an interval without its sample, or outside a steady
	// level, breaks the run.
	sustain := max(1, rules.SustainIntervals)
	for g, name := range names {
		type check struct {
			rule      string
			threshold float64
			value     func(*windowStats) (float64, bool)
		}
		for _, c := range []check{
			{RuleGeneratorCPU, rules.CPUPct, func(s *windowStats) (float64, bool) { return s.cpu, true }},
			{RuleGeneratorSteal, rules.StealPct, func(s *windowStats) (float64, bool) { return s.steal, true }},
			{RuleGeneratorThrottled, rules.ThrottledFraction, func(s *windowStats) (float64, bool) {
				if s.periods == 0 {
					if s.throttled > 0 {
						limitations[LimitationThrottlePeriodsUnrecorded] = true
					}
					return 0, false
				}
				return float64(s.throttled) / float64(s.periods), true
			}},
		} {
			run := []int{}
			flush := func() {
				if len(run) >= sustain {
					for _, i := range run {
						value, _ := c.value(&stats[g][i])
						findings[i] = append(findings[i], ValidityFinding{Rule: c.rule, Generator: name, Value: value, Threshold: c.threshold})
					}
				}
				run = run[:0]
			}
			for i := range intervals {
				s := &stats[g][i]
				if !s.present || !judged[i] {
					flush()
					continue
				}
				if value, ok := c.value(s); ok && value >= c.threshold {
					run = append(run, i)
				} else {
					flush()
				}
			}
			flush()
		}
	}

	// Invalidating evidence needs only one interval.
	droppedRecorded, shedRecorded := false, false
	var previousDropped, previousShed int64
	for i, interval := range intervals {
		for g, name := range names {
			if s := &stats[g][i]; s.present && s.kills > 0 {
				findings[i] = append(findings[i], ValidityFinding{Rule: RuleGeneratorOOMKill, Generator: name, Value: float64(s.kills), Threshold: 0})
			}
		}
		if interval.DroppedResults != nil {
			droppedRecorded = true
			dropped := counterDelta(*interval.DroppedResults, &previousDropped)
			// Results are counted by start and drops by completion, so the
			// completed transactions are a floor for an interval with few starts.
			recorded := interval.Attempts
			if interval.Results != nil {
				recorded = max(recorded, *interval.Results)
			}
			if results := recorded + dropped; dropped > 0 && results > 0 {
				if fraction := float64(dropped) / float64(results); fraction > rules.DroppedFraction {
					findings[i] = append(findings[i], ValidityFinding{Rule: RuleDroppedResults, Value: fraction, Threshold: rules.DroppedFraction})
				}
			}
		}
		if interval.ShedArrivals != nil {
			shedRecorded = true
			shed := counterDelta(*interval.ShedArrivals, &previousShed)
			if shed > 0 && input.ArrivalModel != ArrivalClosed {
				findings[i] = append(findings[i], ValidityFinding{Rule: RuleShedArrivals, Value: float64(shed), Threshold: 0})
			}
		}
	}
	if !droppedRecorded {
		limitations[LimitationDroppedResultsUnrecorded] = true
	}
	if !shedRecorded && input.ArrivalModel != ArrivalClosed {
		limitations[LimitationShedArrivalsUnrecorded] = true
	}
	if input.DataExhausted == nil {
		limitations[LimitationDataExhaustionUnrecorded] = true
	}
	if input.ArrivalModel != ArrivalClosed && input.ArrivalModel != ArrivalOpen && input.ArrivalModel != ArrivalMixed {
		limitations[LimitationArrivalModelUnrecorded] = true
	}

	reasons := map[string]bool{}
	missingJudged := 0
	verdicts := make([]string, len(intervals))
	for i := range intervals {
		verdict := ValidityValid
		for _, f := range findings[i] {
			switch f.Rule {
			case RuleGeneratorOOMKill, RuleDroppedResults, RuleShedArrivals:
				verdict = worseVerdict(verdict, ValidityInvalid)
			default:
				verdict = worseVerdict(verdict, ValiditySuspect)
			}
		}
		if missing[i] {
			for g, name := range names {
				if absent(g, i) {
					findings[i] = append(findings[i], ValidityFinding{Rule: RuleGeneratorStatsMissing, Generator: name})
				}
			}
			if len(names) == 0 {
				findings[i] = append(findings[i], ValidityFinding{Rule: RuleGeneratorStatsMissing})
			}
			verdict = worseVerdict(verdict, ValidityUnknown)
		}
		verdicts[i] = verdict
		if !judged[i] {
			continue
		}
		out.IntervalsJudged++
		if missing[i] {
			missingJudged++
		}
		if verdict == ValiditySuspect || verdict == ValidityInvalid {
			out.Verdict = worseVerdict(out.Verdict, verdict)
			for _, f := range findings[i] {
				if f.Rule != RuleGeneratorStatsMissing {
					reasons[f.Rule] = true
				}
			}
		}
		if verdict != ValidityValid {
			if len(out.Windows) < MaxValidityWindows {
				out.Windows = append(out.Windows, ValidityWindow{Start: intervals[i].Start, End: intervals[i].End, Verdict: verdict, Findings: findings[i]})
			} else {
				limitations[LimitationWindowsTruncated] = true
			}
		}
	}
	if out.IntervalsJudged > 0 {
		out.MissingFraction = float64(missingJudged) / float64(out.IntervalsJudged)
	}
	if out.MissingFraction > rules.MissingFraction {
		out.Verdict = worseVerdict(out.Verdict, ValidityUnknown)
		reasons[RuleGeneratorStatsMissing] = true
	}
	if input.DataExhausted != nil && *input.DataExhausted {
		out.Verdict = ValidityInvalid
		reasons[RuleDataExhausted] = true
	}
	if len(input.EvidenceGaps) > 0 {
		out.Verdict = worseVerdict(out.Verdict, ValidityUnknown)
		reasons[RuleEvidenceIncomplete] = true
		for _, gap := range input.EvidenceGaps {
			limitations[gap] = true
		}
	}
	// Reasons explain the verdict as it stands: a suspect generator does not
	// explain an invalid run, and missing stats do not explain a suspect one.
	for rule := range reasons {
		if out.Verdict == ValidityInvalid && rule != RuleGeneratorOOMKill && rule != RuleDroppedResults && rule != RuleShedArrivals && rule != RuleDataExhausted {
			continue
		}
		if out.Verdict == ValiditySuspect && rule != RuleGeneratorCPU && rule != RuleGeneratorSteal && rule != RuleGeneratorThrottled {
			continue
		}
		out.Reasons = append(out.Reasons, rule)
	}
	sort.Strings(out.Reasons)

	out.Anomalies = validityAnomalies(input, intervals, verdicts, judged)
	for limitation := range limitations {
		out.Limitations = append(out.Limitations, limitation)
	}
	sort.Strings(out.Limitations)
	return out
}

// validityAnomalies applies the throughput rules to intervals whose
// generators were healthy.
func validityAnomalies(input ValidityInput, intervals []ValidityInterval, verdicts []string, judged []bool) []ValidityAnomaly {
	anomalies := []ValidityAnomaly{}
	healthy := func(i int) bool { return judged[i] && verdicts[i] == ValidityValid }
	if input.ArrivalModel == ArrivalOpen || input.ArrivalModel == ArrivalMixed {
		start := -1
		closeRun := func(end int) {
			if start >= 0 {
				anomalies = append(anomalies, ValidityAnomaly{Kind: AnomalyOpenModelShortfall, Start: intervals[start].Start, End: intervals[end].End, Value: float64(end - start + 1)})
				start = -1
			}
		}
		for i, interval := range intervals {
			if healthy(i) && interval.TargetMissed {
				if start < 0 {
					start = i
				}
				continue
			}
			closeRun(i - 1)
		}
		closeRun(len(intervals) - 1)
	}
	if input.ArrivalModel == ArrivalClosed {
		// At one steady level, compare the last third of the intervals with
		// the first third.
		for _, level := range input.SteadyLevels {
			var inside []int
			for i, interval := range intervals {
				if !interval.Start.Before(level.Start) && !interval.End.After(level.End) && healthy(i) {
					inside = append(inside, i)
				}
			}
			if len(inside) < 6 {
				continue
			}
			third := len(inside) / 3
			firstTPS, firstLatency := meanOf(intervals, inside[:third])
			lastTPS, lastLatency := meanOf(intervals, inside[len(inside)-third:])
			if firstTPS > 0 && firstLatency > 0 && lastTPS < firstTPS*0.9 && lastLatency > firstLatency*1.1 {
				anomalies = append(anomalies, ValidityAnomaly{Kind: AnomalyClosedModelSlowdown, Start: level.Start, End: level.End, Value: (lastTPS - firstTPS) / firstTPS * 100})
			}
		}
	}
	if len(anomalies) > MaxValidityWindows {
		anomalies = anomalies[:MaxValidityWindows]
	}
	return anomalies
}

func meanOf(intervals []ValidityInterval, indexes []int) (tps, latency float64) {
	for _, i := range indexes {
		tps += finiteOrZero(intervals[i].TPS)
		latency += finiteOrZero(intervals[i].AvgLatencyMs)
	}
	n := float64(len(indexes))
	return tps / n, latency / n
}

// nearestInterval returns the interval whose end is nearest at, or -1 when
// none ends within one interval length of it.
func nearestInterval(intervals []ValidityInterval, at time.Time) int {
	i := sort.Search(len(intervals), func(i int) bool { return !intervals[i].End.Before(at) })
	best, bestDistance := -1, time.Duration(math.MaxInt64)
	for _, candidate := range []int{i - 1, i} {
		if candidate < 0 || candidate >= len(intervals) {
			continue
		}
		distance := intervals[candidate].End.Sub(at)
		if distance < 0 {
			distance = -distance
		}
		length := intervals[candidate].End.Sub(intervals[candidate].Start)
		if distance <= length && distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	return best
}

func finiteOrZero(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}
