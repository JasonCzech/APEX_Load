package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Policy-compatible telemetry comparison (C6). Whole-run telemetry health is
// compared between two runs only when the runs were classified the same way:
// the same budgets and sustained window, the same publication cadence, the
// same telemetry source capabilities and a qualified offered workload.
// Otherwise the two runs' evidence is shown side by side with the reasons;
// an explicit re-evaluation under a named common policy can then classify
// both alike (telemetry_reevaluation.go).

// Telemetry comparison incompatibility reasons.
const (
	telemetryReasonPolicyUnavailable     = "policy_unavailable"
	telemetryReasonBudgetsDiffer         = "budgets_differ"
	telemetryReasonSustainedWindowDiffer = "sustained_window_differs"
	telemetryReasonCadenceDiffers        = "cadence_differs"
	telemetryReasonCapabilityDiffers     = "source_capability_differs"
	telemetryReasonWorkloadUnqualified   = "workload_unqualified"
	telemetryReasonWorkloadVersion       = "workload_version_differs"
)

// TelemetryRunProfile is what decides how a run's telemetry was classified.
type TelemetryRunProfile struct {
	RunID string `json:"run_id"`
	// PolicyAvailability is "available" when the run retained a valid
	// immutable policy receipt; only then are the fields below known.
	PolicyAvailability      string                 `json:"policy_availability"`
	PolicyHash              string                 `json:"policy_hash,omitempty"`
	Budgets                 []TelemetryStageBudget `json:"budgets,omitempty"`
	SustainedBreachWindowMs int64                  `json:"sustained_breach_window_ms,omitempty"`
	CadenceMs               int64                  `json:"cadence_ms,omitempty"`
	CadenceScope            string                 `json:"cadence_scope,omitempty"`
	// PolicyPin is the project telemetry policy pinned at Start, recorded
	// beside the receipt (it does not change the receipt's budgets).
	PolicyPin *RunPolicyPin `json:"policy_pin,omitempty"`
	// SourceCapabilities are the telemetry evidence the run's sources could
	// produce (source epochs, clock qualification, wall-time coverage).
	SourceCapabilities []string `json:"source_capabilities"`
	WorkloadVersion    int      `json:"workload_version,omitempty"`
	// Acceptance is the run's own telemetry verdict under its pinned
	// acceptance policy (C7); each run is judged by its own policy, so two
	// verdicts are shown side by side, never compared.
	Acceptance *TelemetryAcceptanceVerdict `json:"acceptance,omitempty"`
	// AcceptanceEvaluated says the verdict was looked for: a comparison saved
	// before telemetry verdicts has neither, and never claims the run had no
	// policy.
	AcceptanceEvaluated bool `json:"acceptance_evaluated,omitempty"`
	// Loss is the run's loss totals: exact, lower bound or unavailable.
	Loss *TelemetryLossAccounting `json:"loss,omitempty"`
}

// BaselineTelemetryComparison compares one baseline's whole-run telemetry
// with the current run's.
type BaselineTelemetryComparison struct {
	BaselineRunID string `json:"baseline_run_id"`
	// Scope is always "whole_run": telemetry health is never restricted to
	// the selected steady-load windows.
	Scope      string              `json:"scope"`
	Comparable bool                `json:"comparable"`
	Reasons    []string            `json:"reasons"`
	Notes      []string            `json:"notes,omitempty"`
	Current    TelemetryRunProfile `json:"current"`
	Baseline   TelemetryRunProfile `json:"baseline"`
	// Health deltas (current minus baseline), only for a comparable pair.
	BreachedIntervalPctDelta   *float64 `json:"breached_interval_pct_delta,omitempty"`
	HealthCoveragePctDelta     *float64 `json:"health_coverage_pct_delta,omitempty"`
	BreachedKnownFractionDelta *float64 `json:"breached_known_fraction_delta,omitempty"`
}

// telemetryRunProfile reads a run's classification profile from its archive
// manifest.
func telemetryRunProfile(runID string, manifest *RunArchiveManifest) TelemetryRunProfile {
	profile := TelemetryRunProfile{RunID: runID, PolicyAvailability: "unavailable", SourceCapabilities: []string{}}
	if manifest == nil {
		return profile
	}
	if evidence := telemetryPolicyEvidenceFromProvenance(&manifest.Provenance); evidence.Policy != nil {
		policy := evidence.Policy
		profile.PolicyAvailability, profile.PolicyHash = "available", policy.Hash
		profile.Budgets = append([]TelemetryStageBudget(nil), policy.Budgets...)
		profile.SustainedBreachWindowMs = policy.SustainedBreachWindowMs
		profile.CadenceMs, profile.CadenceScope = policy.ExpectedControllerPublicationIntervalMs, policy.CadenceScope
	}
	for _, pin := range manifest.Provenance.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindTelemetryPolicy {
			copied := pin
			profile.PolicyPin = &copied
		}
	}
	if manifest.TelemetrySourceEpochCount > 0 {
		profile.SourceCapabilities = append(profile.SourceCapabilities, "source_epochs")
	}
	// Clock qualification classifies only a distributed run's cross-host
	// ages; a local run's clock observations (from monitoring collectors)
	// never changed how it was classified.
	if manifest.ClockObservationsFile != "" && telemetryRunDistributed(manifest) {
		profile.SourceCapabilities = append(profile.SourceCapabilities, "clock_qualification")
	}
	if manifest.TelemetryCoverage != nil {
		profile.SourceCapabilities = append(profile.SourceCapabilities, "coverage:"+manifest.TelemetryCoverage.Algorithm)
	}
	sort.Strings(profile.SourceCapabilities)
	if manifest.Provenance.Workload != nil {
		profile.WorkloadVersion = manifest.Provenance.Workload.Version
	}
	profile.Acceptance, profile.AcceptanceEvaluated = telemetryAcceptanceForManifest(manifest), true
	profile.Loss = manifest.TelemetryLoss.clone()
	return profile
}

func sameTelemetryBudgets(a, b []TelemetryStageBudget) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		// Browser render is advisory and never classified, so its budget
		// does not make two runs' classifications differ.
		if a[i].Stage == "browser_render" && b[i].Stage == "browser_render" {
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// compareBaselineTelemetry decides whether two runs' telemetry health can be
// compared and, if so, how it moved.
func compareBaselineTelemetry(current, baseline *baselineComparisonRun, workload rca.WorkloadQualification) BaselineTelemetryComparison {
	out := BaselineTelemetryComparison{BaselineRunID: baseline.identity.ID, Scope: "whole_run", Reasons: []string{},
		Current: telemetryRunProfile(current.identity.ID, current.manifest), Baseline: telemetryRunProfile(baseline.identity.ID, baseline.manifest)}
	c, b := out.Current, out.Baseline
	if c.PolicyAvailability != "available" || b.PolicyAvailability != "available" {
		out.Reasons = append(out.Reasons, telemetryReasonPolicyUnavailable)
	} else {
		if !sameTelemetryBudgets(c.Budgets, b.Budgets) {
			out.Reasons = append(out.Reasons, telemetryReasonBudgetsDiffer)
		}
		if c.SustainedBreachWindowMs != b.SustainedBreachWindowMs {
			out.Reasons = append(out.Reasons, telemetryReasonSustainedWindowDiffer)
		}
		if c.CadenceMs != b.CadenceMs || c.CadenceScope != b.CadenceScope {
			out.Reasons = append(out.Reasons, telemetryReasonCadenceDiffers)
		}
	}
	if !sameStrings(c.SourceCapabilities, b.SourceCapabilities) {
		out.Reasons = append(out.Reasons, telemetryReasonCapabilityDiffers)
	}
	// Workload keys of different shapes were not qualified alike.
	if c.WorkloadVersion != 0 && b.WorkloadVersion != 0 && c.WorkloadVersion != b.WorkloadVersion {
		out.Reasons = append(out.Reasons, telemetryReasonWorkloadVersion)
	}
	switch workload.Status {
	case rca.BaselineUnqualified:
		out.Reasons = append(out.Reasons, telemetryReasonWorkloadUnqualified)
	case rca.BaselinePartial:
		out.Notes = append(out.Notes, fmt.Sprintf("Offered workload only partly qualified (%v); load-dependent telemetry may still differ.", workload.Reasons))
	}
	if (c.PolicyPin == nil) != (b.PolicyPin == nil) || (c.PolicyPin != nil && b.PolicyPin != nil && c.PolicyPin.SHA256 != b.PolicyPin.SHA256) {
		out.Notes = append(out.Notes, "The runs pinned different project telemetry policies; the receipts above are what classified them.")
	}
	out.Comparable = len(out.Reasons) == 0
	if !out.Comparable {
		return out
	}
	delta := func(x, y *float64) *float64 {
		if x == nil || y == nil {
			return nil
		}
		value := *x - *y
		return &value
	}
	out.BreachedIntervalPctDelta = delta(current.telemetry.BreachedIntervalPct, baseline.telemetry.BreachedIntervalPct)
	out.HealthCoveragePctDelta = delta(current.telemetry.HealthCoveragePct, baseline.telemetry.HealthCoveragePct)
	if current.manifest != nil && baseline.manifest != nil && current.manifest.TelemetryCoverage != nil && baseline.manifest.TelemetryCoverage != nil {
		out.BreachedKnownFractionDelta = delta(current.manifest.TelemetryCoverage.BreachedKnownFraction, baseline.manifest.TelemetryCoverage.BreachedKnownFraction)
	}
	return out
}

// baselineTelemetryComparisonLine is one export line: the deltas of a
// comparable pair, or why the pair is not comparable.
func baselineTelemetryComparisonLine(c BaselineTelemetryComparison) string {
	return baselineTelemetryHealthLine(c) + telemetryAcceptancePairLine(c.Current, c.Baseline)
}

// telemetryAcceptancePairLine states both runs' telemetry verdicts and
// dropped-result totals side by side; each was judged by its own pinned
// policy revision.
func telemetryAcceptancePairLine(current, baseline TelemetryRunProfile) string {
	verdict := func(profile TelemetryRunProfile) string {
		if profile.Acceptance == nil && !profile.AcceptanceEvaluated {
			return "not recorded (saved before telemetry verdicts)"
		}
		if profile.Acceptance == nil {
			return "no acceptance policy"
		}
		label := profile.Acceptance.Outcome + " (" + profile.Acceptance.Mode
		if pin := profile.Acceptance.Policy; pin != nil {
			label += fmt.Sprintf(", %s r%d", pin.Name, pin.Revision)
		}
		return label + ")"
	}
	dropped := func(profile TelemetryRunProfile) string {
		if profile.Loss == nil || profile.Loss.DroppedResults == nil || profile.Loss.DroppedResults.State == TelemetryCounterUnavailable {
			return "unavailable"
		}
		return boundedCount(profile.Loss.DroppedResults.Value, map[string]string{TelemetryCounterExact: telemetryAcceptanceExact, TelemetryCounterLowerBound: telemetryAcceptanceLowerBound}[profile.Loss.DroppedResults.State])
	}
	return fmt.Sprintf("; telemetry acceptance %s / %s; dropped results %s / %s", verdict(current), verdict(baseline), dropped(current), dropped(baseline))
}

func baselineTelemetryHealthLine(c BaselineTelemetryComparison) string {
	if !c.Comparable {
		return fmt.Sprintf("Telemetry of %s vs %s not compared (%s): policy %s / %s, cadence %d / %d ms, sources [%s] / [%s]",
			c.Current.RunID, c.BaselineRunID, strings.Join(c.Reasons, ", "), shortTelemetryHash(c.Current.PolicyHash), shortTelemetryHash(c.Baseline.PolicyHash),
			c.Current.CadenceMs, c.Baseline.CadenceMs, strings.Join(c.Current.SourceCapabilities, ", "), strings.Join(c.Baseline.SourceCapabilities, ", "))
	}
	signed := func(v *float64, unit string) string {
		if v == nil {
			return "unavailable"
		}
		return fmt.Sprintf("%+.3g%s", *v, unit)
	}
	policy := "same policy " + shortTelemetryHash(c.Current.PolicyHash)
	if c.Current.PolicyHash != c.Baseline.PolicyHash {
		// Receipts that differ only in advisory budgets classify alike.
		policy = fmt.Sprintf("equivalent classifying budgets, policies %s / %s", shortTelemetryHash(c.Current.PolicyHash), shortTelemetryHash(c.Baseline.PolicyHash))
	}
	return fmt.Sprintf("Telemetry of %s vs %s comparable (%s): breached classified intervals %s, health coverage %s, breached known-time fraction %s",
		c.Current.RunID, c.BaselineRunID, policy, signed(c.BreachedIntervalPctDelta, " pts"), signed(c.HealthCoveragePctDelta, " pts"), signed(c.BreachedKnownFractionDelta, ""))
}

func shortTelemetryHash(hash string) string {
	if hash == "" {
		return "unavailable"
	}
	return hash[:min(12, len(hash))]
}
