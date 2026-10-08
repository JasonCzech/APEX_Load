package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

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
	// Bound qualifies Observed: exact, or lower_bound when more loss or
	// observed time may exist than was counted.
	Bound string `json:"bound,omitempty"`
	// Numerator and Denominator are a ratio's terms, in its own units
	// (milliseconds of wall time, or records).
	Numerator   *float64 `json:"numerator,omitempty"`
	Denominator *float64 `json:"denominator,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// Bounds of an observed criterion value.
const (
	telemetryAcceptanceExact      = "exact"
	telemetryAcceptanceLowerBound = "lower_bound"
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
		verdict.Criteria = append(verdict.Criteria, telemetryStageLagCriterion(rule, evidence.stages))
	}
	verdict.Outcome = TelemetryAcceptancePass
	for _, criterion := range verdict.Criteria {
		switch criterion.Outcome {
		case TelemetryAcceptanceFail:
			verdict.Outcome = TelemetryAcceptanceFail
		case TelemetryAcceptanceInconclusive:
			if verdict.Outcome == TelemetryAcceptancePass {
				verdict.Outcome = TelemetryAcceptanceInconclusive
			}
		}
		if criterion.Outcome != TelemetryAcceptancePass {
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
	denominator := float64(lost) + float64(max(records, 0))
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

func telemetryStageLagCriterion(rule TelemetryLagPercentileRule, stages []TelemetryStageDistribution) TelemetryAcceptanceCriterion {
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
	if observed > rule.MaxMs {
		criterion.Outcome = TelemetryAcceptanceFail
		criterion.Reason = fmt.Sprintf("%s %s lag %.0f ms exceeds %.0f ms (%d samples)", rule.Stage, rule.Percentile, observed, rule.MaxMs, stage.Samples)
		return criterion
	}
	criterion.Outcome = TelemetryAcceptancePass
	return criterion
}

func inconclusiveCriterion(criterion TelemetryAcceptanceCriterion, reason string) TelemetryAcceptanceCriterion {
	criterion.Outcome, criterion.Reason = TelemetryAcceptanceInconclusive, reason
	return criterion
}

func acceptanceFloat(value float64) *float64 { return &value }

func formatAcceptanceRatio(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", value*100)
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
	evidence := telemetryAcceptanceEvidence{breach: manifest.TelemetryCoverage, loss: manifest.TelemetryLoss, records: manifest.TransactionCount, stages: manifest.TelemetryStageDistributions}
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
	evidence := telemetryAcceptanceEvidence{provisional: true, coverage: telemetry.Coverage, loss: telemetry.Loss, records: metrics.TotalTx + metrics.TotalErrors, stages: telemetry.StageDistributions}
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
// a finalized run whose verdict failed or was inconclusive. The event is
// stamped with the run's completion time, so a repeated notification for the
// same archive carries the same idempotency key.
func (s *Server) notifyTelemetryAcceptance(testID string, manifest *RunArchiveManifest) {
	if s == nil || s.runner == nil || s.runner.notifier == nil || manifest == nil {
		return
	}
	verdict := telemetryAcceptanceForManifest(manifest)
	if verdict == nil || verdict.Outcome == TelemetryAcceptancePass {
		return
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
		if criterion.Outcome != TelemetryAcceptancePass {
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
