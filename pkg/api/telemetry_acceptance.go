package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Telemetry acceptance verdicts (C7). A run that pinned a
// telemetry_acceptance_policy at Start is judged against the definition
// frozen with the pin, so a later edit never reinterprets it. The verdict is
// separate from the application verdict: only a fail_ci policy can change a
// CI outcome, and unknown required evidence is inconclusive, never a pass.

// Telemetry acceptance outcomes.
const (
	TelemetryAcceptancePass         = "pass"
	TelemetryAcceptanceFail         = "fail"
	TelemetryAcceptanceInconclusive = "inconclusive"
)

// telemetryAcceptanceModeUnknown is the mode of a run that pinned a policy
// before definitions were frozen with their pins: its mode was not retained.
const telemetryAcceptanceModeUnknown = "unknown"

// TelemetryAcceptancePolicySnapshot is the acceptance policy revision a run
// pinned at Start, with its definition, frozen in the admission receipt.
type TelemetryAcceptancePolicySnapshot struct {
	Pin        RunPolicyPin                        `json:"pin"`
	Definition TelemetryAcceptancePolicyDefinition `json:"definition"`
}

func cloneTelemetryAcceptancePolicySnapshot(in *TelemetryAcceptancePolicySnapshot) *TelemetryAcceptancePolicySnapshot {
	if in == nil {
		return nil
	}
	out := *in
	out.Definition = cloneTelemetryAcceptancePolicyDefinition(in.Definition)
	return &out
}

func cloneTelemetryAcceptancePolicyDefinition(in TelemetryAcceptancePolicyDefinition) TelemetryAcceptancePolicyDefinition {
	out := in
	out.MinWallTimeCoverage = cloneFloat64Ptr(in.MinWallTimeCoverage)
	out.MaxLossRatio = cloneFloat64Ptr(in.MaxLossRatio)
	out.MaxBreachedKnownTimeFraction = cloneFloat64Ptr(in.MaxBreachedKnownTimeFraction)
	if in.MaxLostRecords != nil {
		value := *in.MaxLostRecords
		out.MaxLostRecords = &value
	}
	out.StageLagPercentiles = append([]TelemetryLagPercentileRule(nil), in.StageLagPercentiles...)
	return out
}

// TelemetryAcceptanceVerdict is a run's telemetry verdict under its pinned
// acceptance policy.
type TelemetryAcceptanceVerdict struct {
	// Outcome is pass, fail or inconclusive: any failed criterion fails, any
	// criterion without the evidence to decide it is inconclusive.
	Outcome string `json:"outcome"`
	// Mode is the policy's mode: advisory (never changes a CI outcome),
	// fail_ci, or unknown when the run predates frozen policy definitions.
	Mode string `json:"mode"`
	// Provisional marks a verdict over live evidence, before finalize.
	Provisional bool          `json:"provisional,omitempty"`
	Policy      *RunPolicyPin `json:"policy,omitempty"`
	// Criteria are the policy's criteria, each with its evidence.
	Criteria []TelemetryAcceptanceCriterion `json:"criteria"`
	// Reasons explain every criterion that did not pass.
	Reasons []string `json:"reasons,omitempty"`
	// Exclusions name evidence the verdict deliberately does not count.
	Exclusions []string `json:"exclusions,omitempty"`
}

// TelemetryAcceptanceCriterion is one criterion's result.
type TelemetryAcceptanceCriterion struct {
	// Criterion is min_wall_time_coverage, max_lost_records, max_loss_ratio,
	// max_breached_known_time_fraction or stage_lag:<stage>/<percentile>.
	Criterion string `json:"criterion"`
	Outcome   string `json:"outcome"`
	// Comparator is ">=" (a floor) or "<=" (a ceiling).
	Comparator string  `json:"comparator"`
	Threshold  float64 `json:"threshold"`
	// Unit is ratio, records or ms.
	Unit     string   `json:"unit"`
	Observed *float64 `json:"observed,omitempty"`
	// Bound qualifies Observed: exact; lower_bound when more loss, observed
	// time or lag may exist than was counted; upper_bound when the whole
	// run's value is at most Observed; partial when Observed describes only
	// part of the run's evidence and bounds nothing.
	Bound string `json:"bound,omitempty"`
	// Numerator and Denominator are a ratio's terms, in its own units
	// (milliseconds of wall time, or records).
	Numerator   *float64 `json:"numerator,omitempty"`
	Denominator *float64 `json:"denominator,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	// Advisory marks a criterion on an advisory stage (browser render, which
	// viewers' tabs report): it is shown but never decides the verdict.
	Advisory bool `json:"advisory,omitempty"`
}

// Bounds of an observed criterion value.
const (
	telemetryAcceptanceExact      = "exact"
	telemetryAcceptanceLowerBound = "lower_bound"
	telemetryAcceptanceUpperBound = "upper_bound"
	telemetryAcceptancePartial    = "partial"
)

// telemetryAcceptanceEvidence is the evidence a verdict reads, from an
// archive manifest or from a live snapshot.
type telemetryAcceptanceEvidence struct {
	provisional bool
	coverage    *TelemetryCoverage
	// coveragePending says live coverage is not computed for this run (one
	// executed in the controller's own process); the manifest records it.
	coveragePending bool
	// breach is the classified-time account; nil when unavailable.
	breach *TelemetryCoverageRecord
	loss   *TelemetryLossAccounting
	// records counts the run's recorded results (the loss ratio's
	// denominator, with the lost records).
	records int64
	stages  []TelemetryStageDistribution
	// stageQuantile reads a stage's kept quantile at any position from its
	// archived digest; nil for a live run, whose recorder keeps sampling.
	stageQuantile telemetryStageQuantile
	// startedAt is when the run started; stage distributions sampled only
	// from later are partial.
	startedAt *time.Time
}

// telemetryStageQuantile returns a stage's kept quantile at q, or false when
// its digest is missing or holds a different number of observations than
// the distribution reports.
type telemetryStageQuantile func(stage string, samples int64, q float64) (float64, bool)

// manifestStageQuantile reads kept quantiles from a manifest's stage
// digests, rounded as the distributions are.
func manifestStageQuantile(manifest *RunArchiveManifest) telemetryStageQuantile {
	if len(manifest.TelemetryStageDigests) == 0 {
		return nil
	}
	digests := map[string]*engine.TDigest{}
	return func(stage string, samples int64, q float64) (float64, bool) {
		snapshot, ok := manifest.TelemetryStageDigests[stage]
		if !ok || snapshot.Count != samples || samples <= 0 {
			return 0, false
		}
		digest := digests[stage]
		if digest == nil {
			digest = engine.NewTDigestFromSnapshot(snapshot)
			digests[stage] = digest
		}
		return math.Round(digest.Quantile(q)*1000) / 1000, true
	}
}

// telemetryAcceptanceStageMap maps the policy's stage names to the stage
// distributions the controller samples; end_to_end (capture to a viewer's
// render) has no distribution yet.
var telemetryAcceptanceStageMap = map[string]string{
	"generator":         telemetryStageGeneratorProcessing,
	"transport":         telemetryStageTransport,
	"controller_fanout": telemetryStageControllerFanout,
	"sample_age":        telemetryStageSampleAge,
	"browser_render":    telemetryStageBrowserRender,
}

// telemetryAcceptanceExclusions are stated on every verdict.
var telemetryAcceptanceExclusions = []string{
	"shedded_arrivals: arrivals an open workload could not dispatch are a workload shortfall, not telemetry loss",
	"websocket drops: dashboard delivery counters are controller-wide and never attributed to a run",
}

// evaluateTelemetryAcceptance judges evidence under a policy definition.
func evaluateTelemetryAcceptance(policy TelemetryAcceptancePolicyDefinition, pin *RunPolicyPin, evidence telemetryAcceptanceEvidence) *TelemetryAcceptanceVerdict {
	verdict := &TelemetryAcceptanceVerdict{Mode: policy.EffectiveMode(), Provisional: evidence.provisional, Criteria: []TelemetryAcceptanceCriterion{},
		Exclusions: append([]string(nil), telemetryAcceptanceExclusions...)}
	if pin != nil {
		copied := *pin
		verdict.Policy = &copied
	}
	if policy.MinWallTimeCoverage != nil {
		verdict.Criteria = append(verdict.Criteria, telemetryCoverageCriterion(*policy.MinWallTimeCoverage, evidence))
	}
	if policy.MaxLostRecords != nil || policy.MaxLossRatio != nil {
		lost, bound, reason := telemetryLostRecords(evidence.loss)
		if policy.MaxLostRecords != nil {
			verdict.Criteria = append(verdict.Criteria, telemetryLostRecordsCriterion(*policy.MaxLostRecords, lost, bound, reason))
		}
		if policy.MaxLossRatio != nil {
			verdict.Criteria = append(verdict.Criteria, telemetryLossRatioCriterion(*policy.MaxLossRatio, lost, bound, reason, evidence.records))
		}
	}
	if policy.MaxBreachedKnownTimeFraction != nil {
		verdict.Criteria = append(verdict.Criteria, telemetryBreachCriterion(*policy.MaxBreachedKnownTimeFraction, evidence))
	}
	for _, rule := range policy.StageLagPercentiles {
		verdict.Criteria = append(verdict.Criteria, telemetryStageLagCriterion(rule, evidence.stages, evidence.stageQuantile, evidence.startedAt))
	}
	verdict.Outcome = TelemetryAcceptancePass
	for _, criterion := range verdict.Criteria {
		if criterion.Advisory {
			continue
		}
		switch criterion.Outcome {
		case TelemetryAcceptanceFail:
			verdict.Outcome = TelemetryAcceptanceFail
		case TelemetryAcceptanceInconclusive:
			if verdict.Outcome == TelemetryAcceptancePass {
				verdict.Outcome = TelemetryAcceptanceInconclusive
			}
		}
		if criterion.Outcome != TelemetryAcceptancePass && !slices.Contains(verdict.Reasons, criterion.Reason) {
			verdict.Reasons = append(verdict.Reasons, criterion.Reason)
		}
	}
	return verdict
}

func telemetryCoverageCriterion(floor float64, evidence telemetryAcceptanceEvidence) TelemetryAcceptanceCriterion {
	criterion := TelemetryAcceptanceCriterion{Criterion: "min_wall_time_coverage", Comparator: ">=", Threshold: floor, Unit: "ratio"}
	coverage := evidence.coverage
	switch {
	case coverage == nil && evidence.coveragePending:
		return inconclusiveCriterion(criterion, "wall-time coverage is recorded when the run is finalized")
	case coverage == nil:
		return inconclusiveCriterion(criterion, "wall-time coverage is unavailable for this run")
	case coverage.WindowMs <= 0:
		return inconclusiveCriterion(criterion, "the run has no measured wall-time window")
	}
	ratio := float64(coverage.ObservedMs) / float64(coverage.WindowMs)
	criterion.Observed, criterion.Numerator, criterion.Denominator = &ratio, acceptanceFloat(float64(coverage.ObservedMs)), acceptanceFloat(float64(coverage.WindowMs))
	criterion.Bound = telemetryAcceptanceExact
	if coverage.Truncated {
		criterion.Bound = telemetryAcceptanceLowerBound
	}
	switch {
	case ratio >= floor:
		criterion.Outcome = TelemetryAcceptancePass
	case coverage.Truncated:
		// Observed time is a lower bound: the run may have met the floor.
		criterion.Outcome = TelemetryAcceptanceInconclusive
		criterion.Reason = fmt.Sprintf("wall-time coverage is at least %s, below the %s floor, but observed time is a lower bound", formatAcceptanceRatio(ratio), formatAcceptanceRatio(floor))
	default:
		criterion.Outcome = TelemetryAcceptanceFail
		criterion.Reason = fmt.Sprintf("wall-time coverage %s is below the %s floor (%d of %d ms observed)", formatAcceptanceRatio(ratio), formatAcceptanceRatio(floor), coverage.ObservedMs, coverage.WindowMs)
	}
	return criterion
}

// telemetryLostRecords is the run's lost record count: results the collectors
// dropped. A lost publication or a dropped subscriber batch lost an unknown
// number of records, so either (or a counter that cannot rule one out) makes
// the count a lower bound. reason is set when dropped results are unknown.
func telemetryLostRecords(loss *TelemetryLossAccounting) (int64, string, string) {
	if loss == nil {
		return 0, "", "loss accounting is unavailable for this run"
	}
	dropped := loss.DroppedResults
	if dropped == nil || dropped.State == TelemetryCounterUnavailable {
		reason := "dropped results are unavailable"
		if dropped != nil && dropped.Reason != "" {
			reason += " (" + dropped.Reason + ")"
		}
		return 0, "", reason
	}
	bound := telemetryAcceptanceExact
	if dropped.State != TelemetryCounterExact {
		bound = telemetryAcceptanceLowerBound
	}
	for _, batches := range []*TelemetryCounterTotal{loss.LostPublications, loss.DroppedSubscriberBatches} {
		if batches != nil && (batches.State != TelemetryCounterExact || batches.Value > 0) {
			bound = telemetryAcceptanceLowerBound
		}
	}
	return dropped.Value, bound, ""
}

func telemetryLostRecordsCriterion(ceiling, lost int64, bound, reason string) TelemetryAcceptanceCriterion {
	criterion := TelemetryAcceptanceCriterion{Criterion: "max_lost_records", Comparator: "<=", Threshold: float64(ceiling), Unit: "records"}
	if reason != "" {
		return inconclusiveCriterion(criterion, reason)
	}
	criterion.Observed, criterion.Bound = acceptanceFloat(float64(lost)), bound
	switch {
	case lost > ceiling:
		criterion.Outcome = TelemetryAcceptanceFail
		criterion.Reason = fmt.Sprintf("%s lost records exceed the %d record ceiling", boundedCount(lost, bound), ceiling)
	case bound == telemetryAcceptanceLowerBound:
		criterion.Outcome = TelemetryAcceptanceInconclusive
		criterion.Reason = fmt.Sprintf("at least %d records were lost (within the %d record ceiling), but lost publications or batches hold an unknown number more", lost, ceiling)
	default:
		criterion.Outcome = TelemetryAcceptancePass
	}
	return criterion
}

func telemetryLossRatioCriterion(ceiling float64, lost int64, bound, reason string, records int64) TelemetryAcceptanceCriterion {
	criterion := TelemetryAcceptanceCriterion{Criterion: "max_loss_ratio", Comparator: "<=", Threshold: ceiling, Unit: "ratio"}
	if reason != "" {
		return inconclusiveCriterion(criterion, reason)
	}
	if records < 0 {
		return inconclusiveCriterion(criterion, "the run's recorded result total is unavailable")
	}
	denominator := float64(lost) + float64(records)
	if denominator <= 0 {
		return inconclusiveCriterion(criterion, "the run recorded no results to measure loss against")
	}
	// lost/(lost+recorded) grows with lost, so a lower-bound count gives a
	// lower-bound ratio.
	ratio := float64(lost) / denominator
	criterion.Observed, criterion.Bound = &ratio, bound
	criterion.Numerator, criterion.Denominator = acceptanceFloat(float64(lost)), &denominator
	switch {
	case ratio > ceiling:
		criterion.Outcome = TelemetryAcceptanceFail
		criterion.Reason = fmt.Sprintf("loss ratio %s%s exceeds the %s ceiling (%d of %.0f records)", boundedPrefix(bound), formatAcceptanceRatio(ratio), formatAcceptanceRatio(ceiling), lost, denominator)
	case bound == telemetryAcceptanceLowerBound:
		criterion.Outcome = TelemetryAcceptanceInconclusive
		criterion.Reason = fmt.Sprintf("loss ratio is at least %s (within the %s ceiling), but lost publications or batches hold an unknown number more records", formatAcceptanceRatio(ratio), formatAcceptanceRatio(ceiling))
	default:
		criterion.Outcome = TelemetryAcceptancePass
	}
	return criterion
}

func telemetryBreachCriterion(ceiling float64, evidence telemetryAcceptanceEvidence) TelemetryAcceptanceCriterion {
	criterion := TelemetryAcceptanceCriterion{Criterion: "max_breached_known_time_fraction", Comparator: "<=", Threshold: ceiling, Unit: "ratio"}
	breach := evidence.breach
	switch {
	case breach == nil && evidence.provisional:
		return inconclusiveCriterion(criterion, "breached known time is classified when the run is finalized")
	case breach == nil:
		return inconclusiveCriterion(criterion, "the run's classified telemetry time is unavailable")
	case breach.BreachedKnownFraction == nil || breach.ClassifiedMs <= 0:
		return inconclusiveCriterion(criterion, "no telemetry time could be classified healthy, degraded or breached")
	}
	fraction := *breach.BreachedKnownFraction
	criterion.Observed, criterion.Bound = &fraction, telemetryAcceptanceExact
	criterion.Numerator, criterion.Denominator = acceptanceFloat(float64(breach.BreachedMs)), acceptanceFloat(float64(breach.ClassifiedMs))
	if fraction > ceiling {
		criterion.Outcome = TelemetryAcceptanceFail
		criterion.Reason = fmt.Sprintf("breached known time %s exceeds the %s ceiling (%d of %d classified ms)", formatAcceptanceRatio(fraction), formatAcceptanceRatio(ceiling), breach.BreachedMs, breach.ClassifiedMs)
		return criterion
	}
	criterion.Outcome = TelemetryAcceptancePass
	return criterion
}

func telemetryStageLagCriterion(rule TelemetryLagPercentileRule, stages []TelemetryStageDistribution, quantile telemetryStageQuantile, startedAt *time.Time) TelemetryAcceptanceCriterion {
	criterion := TelemetryAcceptanceCriterion{Criterion: "stage_lag:" + rule.Stage + "/" + rule.Percentile, Comparator: "<=", Threshold: rule.MaxMs, Unit: "ms"}
	name, measured := telemetryAcceptanceStageMap[rule.Stage]
	if !measured {
		return inconclusiveCriterion(criterion, fmt.Sprintf("stage %s has no measured distribution", rule.Stage))
	}
	var stage *TelemetryStageDistribution
	for index := range stages {
		if stages[index].Stage == name {
			stage = &stages[index]
		}
	}
	// Browser render is reported by whichever dashboard tabs were open, so it
	// can never decide a run.
	criterion.Advisory = rule.Stage == "browser_render" || stage != nil && stage.Advisory
	if stage == nil || stage.Availability != "measured" {
		reason := fmt.Sprintf("stage %s was not measured", rule.Stage)
		if stage != nil && stage.Reason != "" {
			reason += " (" + stage.Reason + ")"
		}
		return inconclusiveCriterion(criterion, reason)
	}
	var value *float64
	switch rule.Percentile {
	case "p50":
		value = stage.P50Ms
	case "p95":
		value = stage.P95Ms
	case "p99":
		value = stage.P99Ms
	case "max":
		value = stage.MaxMs
	}
	if value == nil {
		return inconclusiveCriterion(criterion, fmt.Sprintf("stage %s has no %s", rule.Stage, rule.Percentile))
	}
	observed := *value
	criterion.Observed, criterion.Bound = &observed, telemetryAcceptanceExact
	criterion.Denominator = acceptanceFloat(float64(stage.Samples))
	// A distribution sampled only from a failover onward is partial: only a
	// maximum above the limit is conclusive (the whole run's maximum is at
	// least as high); any other percentile of part of the run bounds nothing.
	if stage.SampledFrom != nil && startedAt != nil && stage.SampledFrom.After(*startedAt) {
		partial := fmt.Sprintf("stage %s was sampled only from %s, after the run started (a controller adopted the run)", rule.Stage, stage.SampledFrom.UTC().Format(time.RFC3339))
		if rule.Percentile != "max" {
			criterion.Bound = telemetryAcceptancePartial
			return inconclusiveCriterion(criterion, partial)
		}
		criterion.Bound = telemetryAcceptanceLowerBound
		if observed > rule.MaxMs {
			criterion.Outcome = TelemetryAcceptanceFail
			criterion.Reason = fmt.Sprintf("%s max lag at least %.0f ms exceeds %.0f ms; %s", rule.Stage, observed, rule.MaxMs, partial)
			return criterion
		}
		return inconclusiveCriterion(criterion, partial)
	}
	if excluded := telemetryStageExcluded(stage); excluded > 0 {
		return telemetryStageCriterionWithExclusions(criterion, rule, stage, excluded, quantile)
	}
	if observed > rule.MaxMs {
		criterion.Outcome = TelemetryAcceptanceFail
		criterion.Reason = fmt.Sprintf("%s %s lag %.0f ms exceeds %.0f ms (%d samples)", rule.Stage, rule.Percentile, observed, rule.MaxMs, stage.Samples)
		return criterion
	}
	criterion.Outcome = TelemetryAcceptancePass
	return criterion
}

// telemetryStageCriterionWithExclusions judges a stage percentile some of
// whose observations were excluded. Excluded observations could lie anywhere,
// so the whole run's value is only bounded (telemetryStageExclusionBounds):
// the criterion passes when the upper bound is within the limit, fails when
// the lower bound exceeds it, and is otherwise inconclusive.
func telemetryStageCriterionWithExclusions(criterion TelemetryAcceptanceCriterion, rule TelemetryLagPercentileRule, stage *TelemetryStageDistribution, excluded int64, quantile telemetryStageQuantile) TelemetryAcceptanceCriterion {
	lower, upper := telemetryStageExclusionBounds(stage, rule.Percentile, excluded, quantile)
	kept := fmt.Sprintf("stage %s excluded %d observations and kept %d", rule.Stage, excluded, stage.Samples)
	switch {
	case upper != nil && *upper <= rule.MaxMs:
		criterion.Observed, criterion.Bound, criterion.Outcome = upper, telemetryAcceptanceUpperBound, TelemetryAcceptancePass
		criterion.Reason = fmt.Sprintf("%s; its %s is at most %.0f ms even if every excluded observation was slower", kept, rule.Percentile, *upper)
	case lower != nil && *lower > rule.MaxMs:
		criterion.Observed, criterion.Bound, criterion.Outcome = lower, telemetryAcceptanceLowerBound, TelemetryAcceptanceFail
		criterion.Reason = fmt.Sprintf("%s %s lag at least %.0f ms exceeds %.0f ms even if every excluded observation was faster; %s", rule.Stage, rule.Percentile, *lower, rule.MaxMs, kept)
	case rule.Percentile == "max":
		criterion.Bound = telemetryAcceptanceLowerBound
		criterion = inconclusiveCriterion(criterion, kept+", so its max is unknown")
	default:
		criterion.Bound = telemetryAcceptancePartial
		criterion = inconclusiveCriterion(criterion, fmt.Sprintf("%s, so its %s may exceed %.0f ms", kept, rule.Percentile, rule.MaxMs))
	}
	return criterion
}

// telemetryStageExclusionBounds bounds the whole run's percentile of a stage
// with E excluded and N kept observations. Its rank among all N+E lies, among
// the kept ones, at a quantile from (q·(N+E)−E)/N (every excluded observation
// below it) to q·(N+E)/N (every one above it). An archived digest gives the
// kept quantiles at exactly those positions; without one (a live verdict, or
// an archive written before digests) the bounds are the nearest recorded
// kept percentiles (p50, p95, p99, max) outside that range. The maximum has
// only a lower bound, the kept maximum; a bound that falls among the
// excluded observations is nil.
func telemetryStageExclusionBounds(stage *TelemetryStageDistribution, percentile string, excluded int64, quantile telemetryStageQuantile) (lower, upper *float64) {
	q := map[string]float64{"p50": 0.50, "p95": 0.95, "p99": 0.99, "max": 1}[percentile]
	if q == 0 || stage.Samples <= 0 {
		return nil, nil
	}
	if percentile == "max" {
		return stage.MaxMs, nil
	}
	kept, total := float64(stage.Samples), float64(stage.Samples+excluded)
	if highest := q * total / kept; highest <= 1 {
		upper = telemetryStageKeptQuantile(stage, quantile, highest, true)
	}
	if lowest := (q*total - float64(excluded)) / kept; lowest > 0 {
		lower = telemetryStageKeptQuantile(stage, quantile, lowest, false)
	}
	return lower, upper
}

// telemetryStageKeptQuantile is the kept distribution's quantile at position
// from its digest, else the nearest recorded percentile at or above it (an
// upper bound) or at or below it (a lower bound); nil when none is recorded.
func telemetryStageKeptQuantile(stage *TelemetryStageDistribution, quantile telemetryStageQuantile, position float64, above bool) *float64 {
	if quantile != nil {
		if value, ok := quantile(stage.Stage, stage.Samples, position); ok {
			return &value
		}
	}
	levels := []struct {
		q     float64
		value *float64
	}{{0.50, stage.P50Ms}, {0.95, stage.P95Ms}, {0.99, stage.P99Ms}, {1, stage.MaxMs}}
	var nearest *float64
	for _, l := range levels {
		if l.value == nil {
			continue
		}
		if above && l.q >= position {
			return l.value
		}
		if !above && l.q <= position {
			nearest = l.value
		}
	}
	return nearest
}

func telemetryStageExcluded(stage *TelemetryStageDistribution) int64 {
	var total int64
	for _, count := range stage.Excluded {
		total += count
	}
	return total
}

func inconclusiveCriterion(criterion TelemetryAcceptanceCriterion, reason string) TelemetryAcceptanceCriterion {
	criterion.Outcome, criterion.Reason = TelemetryAcceptanceInconclusive, reason
	return criterion
}

func acceptanceFloat(value float64) *float64 { return &value }

// formatAcceptanceRatio prints a ratio as a percentage with at least two
// significant digits, so a small ceiling (0.03%) stays readable.
func formatAcceptanceRatio(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	percent := value * 100
	if percent == 0 || math.Abs(percent) >= 1 {
		return fmt.Sprintf("%.1f%%", percent)
	}
	precision := min(8, int(math.Ceil(-math.Log10(math.Abs(percent))))+1)
	return fmt.Sprintf("%.*f%%", precision, percent)
}

// liveRunRecords is a live run's recorded results so far (-1 when the run
// totals are not tracked); the snapshot's own counts cover one window.
func liveRunRecords(metrics *LiveMetrics) int64 {
	if metrics.RunTotalTx == nil || metrics.RunTotalErrors == nil {
		return -1
	}
	return *metrics.RunTotalTx + *metrics.RunTotalErrors
}

func boundedCount(value int64, bound string) string {
	return boundedPrefix(bound) + fmt.Sprint(value)
}

func boundedPrefix(bound string) string {
	if bound == telemetryAcceptanceLowerBound {
		return "at least "
	}
	return ""
}

// telemetryAcceptancePin is the run's telemetry acceptance policy pin.
func telemetryAcceptancePin(provenance *RunArchiveProvenance) *RunPolicyPin {
	if provenance == nil {
		return nil
	}
	for index := range provenance.PolicyPins {
		if provenance.PolicyPins[index].Kind == storage.AnalysisDefinitionKindTelemetryAcceptancePolicy {
			pin := provenance.PolicyPins[index]
			return &pin
		}
	}
	return nil
}

// telemetryAcceptanceForProvenance judges evidence under the run's pinned
// policy: nil when the run pinned none. A pin without its frozen definition
// (a run admitted before definitions were frozen) is inconclusive in an
// unknown mode, which no CI gate enforces.
func telemetryAcceptanceForProvenance(provenance *RunArchiveProvenance, evidence telemetryAcceptanceEvidence) *TelemetryAcceptanceVerdict {
	pin := telemetryAcceptancePin(provenance)
	if pin == nil {
		return nil
	}
	snapshot := provenance.TelemetryAcceptancePolicy
	if snapshot == nil || snapshot.Pin.ID != pin.ID || snapshot.Pin.Revision != pin.Revision || snapshot.Pin.SHA256 != pin.SHA256 {
		return &TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptanceInconclusive, Mode: telemetryAcceptanceModeUnknown, Provisional: evidence.provisional, Policy: pin,
			Criteria: []TelemetryAcceptanceCriterion{}, Reasons: []string{"the run pinned this policy before acceptance definitions were retained with their pins, so it cannot be judged"}}
	}
	return evaluateTelemetryAcceptance(snapshot.Definition, pin, evidence)
}

// telemetryAcceptanceForManifest is a finalized run's verdict from its
// archive manifest.
func telemetryAcceptanceForManifest(manifest *RunArchiveManifest) *TelemetryAcceptanceVerdict {
	if manifest == nil {
		return nil
	}
	evidence := telemetryAcceptanceEvidence{breach: manifest.TelemetryCoverage, loss: manifest.TelemetryLoss, records: manifest.TransactionCount, stages: manifest.TelemetryStageDistributions, stageQuantile: manifestStageQuantile(manifest), startedAt: manifest.Provenance.StartedAt}
	if manifest.TelemetryCoverage != nil {
		coverage := manifest.TelemetryCoverage.Coverage
		evidence.coverage = &coverage
	}
	return telemetryAcceptanceForProvenance(&manifest.Provenance, evidence)
}

// attachTelemetryAcceptance adds the run's provisional verdict to a public
// projection of its current snapshot (a clone; tr.mu is held).
func attachTelemetryAcceptance(metrics *LiveMetrics, test *ManagedTest) {
	if metrics == nil || metrics.Telemetry == nil || test == nil || test.initialArchiveProvenance == nil {
		return
	}
	telemetry := metrics.Telemetry
	evidence := telemetryAcceptanceEvidence{provisional: true, coverage: telemetry.Coverage, loss: telemetry.Loss, records: liveRunRecords(metrics), stages: telemetry.StageDistributions, startedAt: test.StartedAt}
	evidence.coveragePending = telemetry.Coverage == nil
	telemetry.Acceptance = telemetryAcceptanceForProvenance(test.initialArchiveProvenance, evidence)
}

func cloneTelemetryAcceptanceVerdict(in *TelemetryAcceptanceVerdict) *TelemetryAcceptanceVerdict {
	if in == nil {
		return nil
	}
	out := *in
	if in.Policy != nil {
		pin := *in.Policy
		out.Policy = &pin
	}
	out.Criteria = append([]TelemetryAcceptanceCriterion(nil), in.Criteria...)
	out.Reasons = append([]string(nil), in.Reasons...)
	out.Exclusions = append([]string(nil), in.Exclusions...)
	return &out
}

// persistTelemetryLoss records the run's loss totals in the manifest at
// finalize, once. A distributed run's totals come from its source ledger; a
// run executed in this process reads its final collector snapshot. A
// distributed run finalized without its ledger (a restart or adoption) has
// every counter unavailable, so a verdict cannot pass on missing loss. An
// adjunct refresh never writes ledger_lost: a manifest without loss totals
// then predates them.
func (tr *TestRunner) persistTelemetryLoss(test *ManagedTest, manifest *RunArchiveManifest, refresh bool) {
	if test == nil || manifest == nil || manifest.TelemetryLoss != nil {
		return
	}
	tr.mu.RLock()
	var loss *TelemetryLossAccounting
	distributed := test.telemetrySources != nil || len(assignedTelemetrySourcesLocked(test)) > 0
	if test.telemetrySources != nil {
		_, loss = telemetrySourceRollupLocked(test)
	} else if !distributed && test.LiveMetrics != nil && test.LiveMetrics.Telemetry != nil {
		loss = test.LiveMetrics.Telemetry.Loss.clone()
	}
	started := test.StartedAt != nil
	tr.mu.RUnlock()
	// A local run's runtime-resource placement names no generator.
	distributed = distributed || len(appendShardTelemetrySources(nil, manifest.Provenance.GeneratorShards)) > 0
	if loss == nil && distributed && started && !refresh {
		totals := map[string]TelemetryCounterTotal{}
		for _, name := range []string{TelemetryCounterDroppedResults, TelemetryCounterShedArrivals, TelemetryCounterDroppedSubscriberBatches, TelemetryCounterLostPublications} {
			totals[name] = TelemetryCounterTotal{State: TelemetryCounterUnavailable, Reason: "ledger_lost"}
		}
		addControllerWideTelemetryCounters(totals)
		loss = telemetryLossAccounting(totals)
	}
	manifest.TelemetryLoss = loss
}

// runArchiveAttached runs the work that waits for a run's finalized archive.
func (s *Server) runArchiveAttached(testID string) {
	s.autoQueueBrowserIntelligence(testID)
	s.notifyRunTelemetryAcceptance(testID)
}

// notifyRunTelemetryAcceptance reads a run's archive manifest and announces
// its telemetry verdict.
func (s *Server) notifyRunTelemetryAcceptance(testID string) {
	if s == nil || s.runner == nil || s.runner.notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, testID)
	if !ok || record == nil {
		return
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		slog.Warn("the run archive could not be leased to announce its telemetry verdict", "test_id", testID, "error", err)
		return
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	lease.Release()
	if err != nil {
		slog.Warn("the run archive manifest could not be read to announce its telemetry verdict", "test_id", testID, "error", err)
		return
	}
	s.notifyTelemetryAcceptance(testID, manifest)
}

// maxTelemetryAcceptanceEventConditions bounds the conditions one
// telemetry_acceptance event lists.
const maxTelemetryAcceptanceEventConditions = 20

// notifyTelemetryAcceptance sends one telemetry_acceptance webhook event for
// a finalized run whose verdict failed or was inconclusive, once per run. The
// event is stamped with the run's completion time.
func (s *Server) notifyTelemetryAcceptance(testID string, manifest *RunArchiveManifest) {
	if s == nil || s.runner == nil || s.runner.notifier == nil || manifest == nil {
		return
	}
	verdict := telemetryAcceptanceForManifest(manifest)
	// A run pinned before acceptance definitions were retained cannot be
	// judged, and announcing that months later helps no one.
	if verdict == nil || verdict.Outcome == TelemetryAcceptancePass || verdict.Mode == telemetryAcceptanceModeUnknown {
		return
	}
	// Each run is announced once per outcome: every later attach of its
	// archive (an adjunct refresh such as a new note, a recovered
	// publication, another leader) finds the claim, while a verdict that
	// changed (an inconclusive one a refresh found failed) is announced. A
	// lost dispatch after the claim is not retried.
	if claims, ok := s.store.(storage.RunNotificationClaimStore); ok {
		claimed, err := claims.ClaimRunNotification(testID, "telemetry_acceptance:"+verdict.Outcome, time.Now())
		if err != nil {
			slog.Warn("the run's telemetry verdict announcement could not be claimed; it is not sent", "test_id", testID, "error", err)
			return
		}
		if !claimed {
			return
		}
	}
	stamp := manifest.CompletedAt
	if stamp.IsZero() {
		stamp = time.Now().UTC()
	}
	conditions := []string{"run:" + testID, "mode:" + verdict.Mode}
	if verdict.Policy != nil {
		conditions = append(conditions, fmt.Sprintf("policy:%s@%d", verdict.Policy.ID, verdict.Policy.Revision))
	}
	for _, criterion := range verdict.Criteria {
		// Advisory criteria never decided the verdict, so they are not why
		// it is announced.
		if criterion.Outcome != TelemetryAcceptancePass && !criterion.Advisory {
			conditions = append(conditions, "criterion:"+criterion.Criterion+" "+criterion.Outcome)
		}
	}
	if coverage := manifest.TelemetryCoverage; coverage != nil {
		// Sources observed for less than the window, with their unknown time.
		for _, source := range coverage.Coverage.Sources {
			if unknown := coverage.Coverage.WindowMs - source.ObservedMs; unknown > 0 && len(conditions) < maxTelemetryAcceptanceEventConditions {
				conditions = append(conditions, fmt.Sprintf("source:%s unknown_ms:%d", source.Source, unknown))
			}
		}
	}
	severity := "warning"
	if verdict.Outcome == TelemetryAcceptanceFail {
		severity = "critical"
	}
	summary := "telemetry acceptance " + verdict.Outcome
	if len(verdict.Reasons) > 0 {
		summary += ": " + verdict.Reasons[0]
	}
	name := manifest.TestName
	if strings.TrimSpace(name) == "" {
		name = testID
	}
	s.runner.notifier.Dispatch(s.runner.currentNotificationSettings(), NotificationEvent{Type: "telemetry_acceptance", TestID: testID, ProjectID: manifest.Provenance.ProjectID,
		TestName: name, Status: verdict.Outcome, Severity: severity, Summary: summary, Conditions: conditions, Timestamp: stamp.UTC()})
}

// CI mappings of an inconclusive telemetry verdict under a fail_ci policy.
const (
	ciTelemetryInconclusivePass = "pass"
	ciTelemetryInconclusiveFail = "fail"
)

// CI outcomes a fail_ci telemetry verdict sets on an otherwise passed run.
const (
	ciOutcomeTelemetryFailed       = "telemetry_failed"
	ciOutcomeTelemetryInconclusive = "telemetry_inconclusive"
)

// ciTelemetryVerdict is a CI run's telemetry verdict and how CI applied it.
type ciTelemetryVerdict struct {
	TelemetryAcceptanceVerdict
	// InconclusivePolicy is the caller's mapping of an inconclusive verdict
	// (pass or fail); empty when the caller chose none.
	InconclusivePolicy string `json:"inconclusive_policy,omitempty"`
	// Enforced says the verdict decided the run's outcome.
	Enforced bool `json:"enforced"`
}

// applyCITelemetryVerdict maps a fail_ci telemetry verdict onto a passed
// run. An advisory verdict, or one judging a run that already failed for
// another reason, is reported and never changes the outcome. An
// inconclusive verdict maps through the caller's choice; without one the run
// is telemetry_inconclusive, never a pass.
func applyCITelemetryVerdict(response *ciRunResponse, metadata ciRunMetadata) {
	verdict := response.TelemetryVerdict
	if verdict == nil {
		return
	}
	verdict.InconclusivePolicy = metadata.TelemetryInconclusive
	if verdict.Mode != "fail_ci" || response.Outcome != "passed" || verdict.Provisional {
		return
	}
	policy := "the run's telemetry acceptance policy"
	if verdict.Policy != nil {
		policy = fmt.Sprintf("telemetry acceptance policy %q revision %d", verdict.Policy.Name, verdict.Policy.Revision)
	}
	reasons := ""
	if len(verdict.Reasons) > 0 {
		reasons = ": " + strings.Join(verdict.Reasons, "; ")
	}
	switch verdict.Outcome {
	case TelemetryAcceptanceFail:
		response.Outcome = ciOutcomeTelemetryFailed
		response.Error = "Telemetry acceptance failed under " + policy + reasons
	case TelemetryAcceptanceInconclusive:
		switch metadata.TelemetryInconclusive {
		case ciTelemetryInconclusivePass:
			return
		case ciTelemetryInconclusiveFail:
			response.Outcome = ciOutcomeTelemetryFailed
			response.Error = "Telemetry acceptance was inconclusive under " + policy + ", which this run maps to fail" + reasons
		default:
			response.Outcome = ciOutcomeTelemetryInconclusive
			response.Error = "Telemetry acceptance was inconclusive under " + policy + " (fail_ci) and the run chose no mapping for an inconclusive verdict (telemetry_inconclusive: pass or fail)" + reasons
		}
	default:
		return
	}
	verdict.Enforced = true
	response.Error = sanitizeCIExternalText(response.Error, ciExternalErrorMaxBytes)
}

// ciTelemetryInconclusiveRequirement refuses a new CI run without a mapping
// for an inconclusive telemetry verdict when the project's default
// acceptance policy is fail_ci (decision 5: no default). The policy is
// pinned at Start, so a later change still meets the
// telemetry_inconclusive outcome rather than a silent pass.
func (s *Server) ciTelemetryInconclusiveRequirement(projectID, choice string) (int, string) {
	if choice != "" {
		return 0, ""
	}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok || store == nil {
		return 0, ""
	}
	policy, err := projectDefaultPolicy(store, projectID, storage.AnalysisDefinitionKindTelemetryAcceptancePolicy)
	if errors.Is(err, errRunPolicyConfiguration) {
		return http.StatusConflict, err.Error()
	}
	if err != nil {
		return http.StatusServiceUnavailable, "the project's telemetry acceptance policy could not be read"
	}
	if policy == nil {
		return 0, ""
	}
	var definition TelemetryAcceptancePolicyDefinition
	if err := json.Unmarshal(policy.Definition, &definition); err != nil {
		return http.StatusConflict, fmt.Sprintf("the project's default telemetry acceptance policy %q is invalid", policy.Name)
	}
	if definition.EffectiveMode() != "fail_ci" {
		return 0, ""
	}
	return http.StatusBadRequest, fmt.Sprintf("the project's default telemetry acceptance policy %q is fail_ci: choose how an inconclusive telemetry verdict maps (telemetry_inconclusive: pass or fail; apexctl --telemetry-inconclusive)", policy.Name)
}
