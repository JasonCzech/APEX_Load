// Package telemetrypolicy defines immutable telemetry budget receipts and their pure validation.
package telemetrypolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"
)

const Version = "apexload.telemetry_policy.v1"
const telemetryPolicyVersion = Version
const SustainedBreachWindow = 15 * time.Second

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

type TelemetryPolicyEvidence struct {
	Availability string                 `json:"availability"`
	Reason       string                 `json:"reason,omitempty"`
	Policy       *TelemetryBudgetPolicy `json:"policy,omitempty"`
}

func Hash(policy *TelemetryBudgetPolicy) string {
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

func Clone(policy *TelemetryBudgetPolicy) *TelemetryBudgetPolicy {
	if policy == nil {
		return nil
	}
	owned := *policy
	owned.Budgets = append([]TelemetryStageBudget(nil), policy.Budgets...)
	return &owned
}

func Validate(policy *TelemetryBudgetPolicy) bool {
	if policy == nil || policy.SchemaVersion != telemetryPolicyVersion || policy.PolicyID != "system-telemetry-budgets" || policy.Revision != 1 ||
		policy.CapturedAt.IsZero() || policy.CapturedAt.Location() != time.UTC ||
		policy.CaptureBoundary != "execution-admission-before-staging" || policy.ClassificationAlgorithm != "legacy-mixed-diagnostic.v1" ||
		policy.SustainedBreachWindowMs != SustainedBreachWindow.Milliseconds() ||
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
	return len(policy.Hash) == 64 && policy.Hash == Hash(policy)
}

func ValidateTelemetryBudgetPolicy(policy *TelemetryBudgetPolicy) bool { return Validate(policy) }
