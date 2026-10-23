package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetrypolicy"
)

const telemetryPolicyVersion = telemetrypolicy.Version

// TelemetryBudgetPolicy is an immutable receipt of the resolved settings used
// by the existing diagnostic algorithm. This receipt does not certify source
// clocks, complete time coverage, exact loss totals, or a telemetry CI verdict.
type TelemetryBudgetPolicy = telemetrypolicy.TelemetryBudgetPolicy

type TelemetryStageBudget = telemetrypolicy.TelemetryStageBudget

// Availability belongs to the policy receipt, not to the collected metrics.
// Legacy evidence remains readable without acquiring invented policy identity.
type TelemetryPolicyEvidence = telemetrypolicy.TelemetryPolicyEvidence

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
	return telemetrypolicy.Hash(policy)
}

func cloneTelemetryBudgetPolicy(policy *TelemetryBudgetPolicy) *TelemetryBudgetPolicy {
	return telemetrypolicy.Clone(policy)
}

func validTelemetryBudgetPolicy(policy *TelemetryBudgetPolicy) bool {
	return telemetrypolicy.Validate(policy)
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
	initial.RunIncarnation = uuid.NewString()
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

// telemetryBudgetPolicyDefinition decodes a project telemetry_policy revision
// a run can be classified by. Live classification keeps its fixed sustained-
// breach window, so a policy with another window cannot set a run's budgets.
func telemetryBudgetPolicyDefinition(definition *storage.AnalysisDefinition) (TelemetryPolicyDefinition, error) {
	var payload TelemetryPolicyDefinition
	if definition == nil || definition.Kind != storage.AnalysisDefinitionKindTelemetryPolicy {
		return payload, fmt.Errorf("%w: not a telemetry budget policy", errRunPolicyConfiguration)
	}
	if _, err := decodeAnalysisPolicy(definition.Kind, definition.Definition); err != nil {
		return payload, fmt.Errorf("%w: telemetry budget policy %q is invalid: %v", errRunPolicyConfiguration, definition.Name, err)
	}
	if err := json.Unmarshal(definition.Definition, &payload); err != nil {
		return payload, fmt.Errorf("%w: telemetry budget policy %q is invalid: %v", errRunPolicyConfiguration, definition.Name, err)
	}
	if payload.SustainedWindowSeconds != nil && time.Duration(*payload.SustainedWindowSeconds)*time.Second != telemetrySustainedBreachWindow {
		return payload, fmt.Errorf("%w: telemetry budget policy %q sets a %d s sustained window; a run's live classification uses %d s", errRunPolicyConfiguration, definition.Name, *payload.SustainedWindowSeconds, int(telemetrySustainedBreachWindow/time.Second))
	}
	return payload, nil
}

// projectTelemetryBudgetPolicy replaces the system receipt captured at
// admission with one resolved from the run's chosen telemetry_policy revision.
// Cadence, scope and capture time stay the system receipt's: they describe the
// controller, not the policy. A stage the policy leaves unset uses its
// built-in default, as re-evaluation does; a stage set to 0 is disabled.
func projectTelemetryBudgetPolicy(system *TelemetryBudgetPolicy, definition *storage.AnalysisDefinition) (*TelemetryBudgetPolicy, error) {
	if !validTelemetryBudgetPolicy(system) || system.SchemaVersion != telemetryPolicyVersion {
		return nil, fmt.Errorf("resolve telemetry policy: the admission receipt is invalid")
	}
	payload, err := telemetryBudgetPolicyDefinition(definition)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(definition.Definition)
	policy := cloneTelemetryBudgetPolicy(system)
	policy.SchemaVersion = telemetrypolicy.VersionProjectPolicy
	if definition.Revision < 1 || definition.Revision > math.MaxInt32 {
		return nil, fmt.Errorf("%w: telemetry budget policy %q has revision %d", errRunPolicyConfiguration, definition.Name, definition.Revision)
	}
	policy.PolicyID, policy.Revision, policy.DefinitionSHA256 = definition.ID, int(definition.Revision), hex.EncodeToString(digest[:])
	for index, limit := range []struct {
		value    *float64
		fallback float64
	}{
		{payload.SampleAgeMs, defaultTelemetrySampleAgeBudgetMs},
		{payload.GeneratorLagMs, defaultTelemetryGeneratorProcessingBudgetMs},
		{payload.TransportLagMs, defaultTelemetryTransportBudgetMs},
		{payload.ControllerFanoutMs, defaultTelemetryControllerFanoutBudgetMs},
		{payload.BrowserRenderLagMs, defaultTelemetryBrowserRenderBudgetMs},
	} {
		value := limit.fallback
		if limit.value != nil {
			value = *limit.value
		}
		policy.Budgets[index] = TelemetryStageBudget{Stage: policy.Budgets[index].Stage, Enabled: value > 0, LimitMs: value}
	}
	policy.Hash = telemetryBudgetPolicyHash(policy)
	if !validTelemetryBudgetPolicy(policy) {
		return nil, fmt.Errorf("%w: telemetry budget policy %q cannot form a valid receipt", errRunPolicyConfiguration, definition.Name)
	}
	return policy, nil
}
