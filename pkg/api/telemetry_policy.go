package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const telemetryPolicyVersion = "apexload.telemetry_policy.v1"

// TelemetryBudgetPolicy is an immutable receipt of the resolved settings used
// by the existing diagnostic algorithm. This receipt does not certify source
// clocks, complete time coverage, exact loss totals, or a telemetry CI verdict.
type TelemetryBudgetPolicy struct {
	SchemaVersion                           string                 `json:"schema_version"`
	PolicyID                                string                 `json:"policy_id"`
	Revision                                int                    `json:"revision"`
	Hash                                    string                 `json:"hash"`
	CapturedAt                              time.Time              `json:"captured_at"`
	CaptureBoundary                         string                 `json:"capture_boundary"`
	ClassificationAlgorithm                 string                 `json:"classification_algorithm"`
	ExpectedControllerPublicationIntervalMs int64                  `json:"expected_controller_publication_interval_ms"`
	CadenceScope                            string                 `json:"cadence_scope"`
	SustainedBreachWindowMs                 int64                  `json:"sustained_breach_window_ms"`
	CoverageRule                            string                 `json:"coverage_rule"`
	Budgets                                 []TelemetryStageBudget `json:"budgets"`
}

type TelemetryStageBudget struct {
	Stage   string  `json:"stage"`
	Enabled bool    `json:"enabled"`
	LimitMs float64 `json:"limit_ms"`
}

// Availability belongs to the policy receipt, not to the collected metrics.
// Legacy evidence remains readable without acquiring invented policy identity.
type TelemetryPolicyEvidence struct {
	Availability string                 `json:"availability"`
	Reason       string                 `json:"reason,omitempty"`
	Policy       *TelemetryBudgetPolicy `json:"policy,omitempty"`
}

func captureTelemetryBudgetPolicy(settings RunArchiveExecutionSettingsSnapshot, distributed bool, now time.Time) *TelemetryBudgetPolicy {
	interval, ok := checkedDuration(settings.General.MetricsIntervalSeconds, time.Second)
	if !ok || interval <= 0 {
		interval = time.Second
	}
	scope := "local-controller-ticker"
	if distributed {
		interval = time.Second
		scope = "distributed-controller-coalescing-gate"
	}
	limits := normalizeThresholdSettings(settings.Thresholds)
	stages := []string{"sample_age", "generator_processing", "transport", "controller_fanout", "browser_render"}
	values := []*float64{limits.MaxTelemetrySampleAgeMs, limits.MaxTelemetryGeneratorLagMs, limits.MaxTelemetryTransportLagMs, limits.MaxTelemetryControllerFanoutMs, limits.MaxTelemetryBrowserRenderLagMs}
	policy := &TelemetryBudgetPolicy{
		SchemaVersion: telemetryPolicyVersion, PolicyID: "system-telemetry-budgets", Revision: 1,
		CapturedAt: now.UTC(), CaptureBoundary: "execution-admission-before-staging",
		ClassificationAlgorithm:                 "legacy-mixed-diagnostic.v1",
		ExpectedControllerPublicationIntervalMs: interval.Milliseconds(), CadenceScope: scope,
		SustainedBreachWindowMs: telemetrySustainedBreachWindow.Milliseconds(),
		CoverageRule:            "retained-classifications-only;gaps-unknown",
		Budgets:                 make([]TelemetryStageBudget, len(stages)),
	}
	for i, stage := range stages {
		if values[i] == nil || math.IsNaN(*values[i]) || math.IsInf(*values[i], 0) || *values[i] < 0 {
			return nil
		}
		policy.Budgets[i] = TelemetryStageBudget{Stage: stage, Enabled: *values[i] > 0, LimitMs: *values[i]}
	}
	policy.Hash = telemetryBudgetPolicyHash(policy)
	return policy
}

// Capture time is receipt provenance, not policy semantics: identical resolved
// policies must compare equal across runs. No process credential/fence is hashed
// or disclosed here, and no source-generation identity is synthesized.
func telemetryBudgetPolicyHash(policy *TelemetryBudgetPolicy) string {
	if policy == nil {
		return ""
	}
	value := *policy
	value.Hash = ""
	value.CapturedAt = time.Time{}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func cloneTelemetryBudgetPolicy(policy *TelemetryBudgetPolicy) *TelemetryBudgetPolicy {
	if policy == nil {
		return nil
	}
	owned := *policy
	owned.Budgets = append([]TelemetryStageBudget(nil), policy.Budgets...)
	return &owned
}

func validTelemetryBudgetPolicy(policy *TelemetryBudgetPolicy) bool {
	if policy == nil || policy.SchemaVersion != telemetryPolicyVersion || policy.PolicyID != "system-telemetry-budgets" || policy.Revision != 1 ||
		policy.CapturedAt.IsZero() || policy.CapturedAt.Location() != time.UTC ||
		policy.CaptureBoundary != "execution-admission-before-staging" || policy.ClassificationAlgorithm != "legacy-mixed-diagnostic.v1" ||
		policy.SustainedBreachWindowMs != telemetrySustainedBreachWindow.Milliseconds() ||
		policy.CoverageRule != "retained-classifications-only;gaps-unknown" ||
		policy.ExpectedControllerPublicationIntervalMs <= 0 || policy.ExpectedControllerPublicationIntervalMs > int64((time.Duration(1<<63-1))/time.Millisecond) ||
		(policy.CadenceScope != "local-controller-ticker" && policy.CadenceScope != "distributed-controller-coalescing-gate") || len(policy.Budgets) != 5 ||
		(policy.CadenceScope == "distributed-controller-coalescing-gate" && policy.ExpectedControllerPublicationIntervalMs != 1000) ||
		(policy.CadenceScope == "local-controller-ticker" && (policy.ExpectedControllerPublicationIntervalMs < 1000 || policy.ExpectedControllerPublicationIntervalMs%1000 != 0)) {
		return false
	}
	stages := []string{"sample_age", "generator_processing", "transport", "controller_fanout", "browser_render"}
	for i, budget := range policy.Budgets {
		if budget.Stage != stages[i] || math.IsNaN(budget.LimitMs) || math.IsInf(budget.LimitMs, 0) || budget.LimitMs < 0 || budget.Enabled != (budget.LimitMs > 0) {
			return false
		}
	}
	return len(policy.Hash) == 64 && policy.Hash == telemetryBudgetPolicyHash(policy)
}

func telemetryPolicyEvidenceFromProvenance(initial *RunArchiveProvenance) *TelemetryPolicyEvidence {
	evidence := &TelemetryPolicyEvidence{Availability: "unavailable", Reason: "immutable-policy-not-retained"}
	if initial == nil || initial.TelemetryPolicy == nil {
		return evidence
	}
	if !validTelemetryBudgetPolicy(initial.TelemetryPolicy) {
		evidence.Reason = "immutable-policy-invalid"
		return evidence
	}
	evidence.Availability = "available"
	evidence.Reason = ""
	evidence.Policy = cloneTelemetryBudgetPolicy(initial.TelemetryPolicy)
	return evidence
}

func cloneTelemetryPolicyEvidence(in *TelemetryPolicyEvidence) *TelemetryPolicyEvidence {
	if in == nil {
		return nil
	}
	out := *in
	out.Policy = cloneTelemetryBudgetPolicy(in.Policy)
	return &out
}

func telemetryPolicyThresholds(policy *TelemetryBudgetPolicy) ThresholdSettings {
	limits := ThresholdSettings{}
	if !validTelemetryBudgetPolicy(policy) {
		return limits
	}
	limits.MaxTelemetrySampleAgeMs = float64Setting(policy.Budgets[0].LimitMs)
	limits.MaxTelemetryGeneratorLagMs = float64Setting(policy.Budgets[1].LimitMs)
	limits.MaxTelemetryTransportLagMs = float64Setting(policy.Budgets[2].LimitMs)
	limits.MaxTelemetryControllerFanoutMs = float64Setting(policy.Budgets[3].LimitMs)
	limits.MaxTelemetryBrowserRenderLagMs = float64Setting(policy.Budgets[4].LimitMs)
	return limits
}

// The locked copy pins the receipt used by this exact publication. Legacy runs
// can use their retained threshold snapshot, but never claim a policy receipt.
func (tr *TestRunner) runTelemetryPolicy(test *ManagedTest) (*TelemetryPolicyEvidence, ThresholdSettings) {
	tr.mu.RLock()
	var initial *RunArchiveProvenance
	if test != nil && test.initialArchiveProvenance != nil {
		initial = &RunArchiveProvenance{
			TelemetryPolicy:   cloneTelemetryBudgetPolicy(test.initialArchiveProvenance.TelemetryPolicy),
			ExecutionSettings: RunArchiveExecutionSettingsSnapshot{Thresholds: cloneThresholdSettings(test.initialArchiveProvenance.ExecutionSettings.Thresholds)},
		}
	}
	tr.mu.RUnlock()
	evidence := telemetryPolicyEvidenceFromProvenance(initial)
	if evidence.Policy != nil {
		return evidence, telemetryPolicyThresholds(evidence.Policy)
	}
	if initial != nil {
		return evidence, cloneThresholdSettings(initial.ExecutionSettings.Thresholds)
	}
	// Compatibility callers may publish without a managed execution admission.
	// That diagnostic remains explicitly unqualified; global defaults preserve
	// their legacy diagnostic behavior without creating archived provenance.
	return evidence, tr.GetSystemSettings().Thresholds
}

// ValidateTelemetryBudgetPolicy shares the server's semantic/hash admission
// with bounded clients after they separately verify canonical wire presence.
func ValidateTelemetryBudgetPolicy(policy *TelemetryBudgetPolicy) bool {
	return validTelemetryBudgetPolicy(policy)
}

// The caller holds tr.mu. This full private candidate is stored atomically with
// INITIALIZING, before Start can return or dispatch the asynchronous backend.
// It is installed on the live test only after the exact durable winner returns.
// A policy-only initial object would suppress later configuration capture and
// is deliberately never used. Existing write-once receipts remain authoritative.
func (tr *TestRunner) telemetryAdmissionProvenanceLocked(test *ManagedTest) (*RunArchiveProvenance, error) {
	if test.initialArchiveProvenance != nil {
		value := maskRunArchiveProvenance(*test.initialArchiveProvenance)
		return &value, nil
	}
	prospective := *test
	prospective.Status = "INITIALIZING"
	prospective.PendingSince = nil
	prospective.PendingReason = ""
	prospective.QueuePosition = 0
	prospective.Error = ""
	initial := buildRunArchiveProvenanceFromSnapshot(&prospective)
	initial.RequestedAt = archiveRunTimestamp(test.CreatedAt, tr.testStore)
	settings := cloneSystemSettings(tr.settings)
	initial.ExecutionSettings = RunArchiveExecutionSettingsSnapshot{General: settings.General, Thresholds: settings.Thresholds, CapacityPlanning: settings.CapacityPlanning}
	_, distributed := tr.execution.(*distributedExecutionBackend)
	initial.TelemetryPolicy = captureTelemetryBudgetPolicy(initial.ExecutionSettings, distributed, time.Now())
	if initial.TelemetryPolicy == nil {
		return nil, fmt.Errorf("resolve telemetry policy: invalid settings")
	}
	owned := maskRunArchiveProvenance(initial)
	return &owned, nil
}
