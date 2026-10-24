// Package telemetrypolicy defines immutable telemetry budget receipts and their pure validation.
package telemetrypolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Version = "apexload.telemetry_policy.v1"
const telemetryPolicyVersion = Version
const SustainedBreachWindow = 15 * time.Second

// VersionProjectPolicy is a receipt resolved from a project telemetry_policy
// revision a run chose, instead of the system threshold settings. It names that
// revision and its definition digest; everything else keeps version 1's rules.
const VersionProjectPolicy = "apexload.telemetry_policy.v2"

// SystemPolicyID identifies the version 1 receipt of the system settings.
const SystemPolicyID = "system-telemetry-budgets"

// CaptureBoundaryAdmission is a receipt captured when the run was admitted;
// CaptureBoundarySegment one captured for a policy segment the run's
// telemetry changed to while it ran.
const (
	CaptureBoundaryAdmission = "execution-admission-before-staging"
	CaptureBoundarySegment   = "in-run-policy-segment"
)

const maxProjectPolicyIDBytes = 128

type TelemetryBudgetPolicy struct {
	SchemaVersion                           string                 `json:"schema_version"`
	PolicyID                                string                 `json:"policy_id"`
	Revision                                int                    `json:"revision"`
	DefinitionSHA256                        string                 `json:"definition_sha256,omitempty"`
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
	if policy == nil || !validIdentity(policy) ||
		policy.CapturedAt.IsZero() || policy.CapturedAt.Location() != time.UTC ||
		(policy.CaptureBoundary != CaptureBoundaryAdmission && policy.CaptureBoundary != CaptureBoundarySegment) || policy.ClassificationAlgorithm != "legacy-mixed-diagnostic.v1" ||
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

// ValidateAdmission is Validate for a run's admission receipt: an in-run
// segment's receipt is valid, but never as the run's admission evidence.
func ValidateAdmission(policy *TelemetryBudgetPolicy) bool {
	return Validate(policy) && policy.CaptureBoundary == CaptureBoundaryAdmission
}

// validIdentity accepts the system receipt (version 1) or a chosen project
// policy revision (version 2) with its definition digest.
func validIdentity(policy *TelemetryBudgetPolicy) bool {
	switch policy.SchemaVersion {
	case telemetryPolicyVersion:
		return policy.PolicyID == SystemPolicyID && policy.Revision == 1 && policy.DefinitionSHA256 == ""
	case VersionProjectPolicy:
		return validProjectPolicyID(policy.PolicyID) && policy.Revision >= 1 && lowerHexSHA256(policy.DefinitionSHA256)
	}
	return false
}

func validProjectPolicyID(id string) bool {
	if id == "" || len(id) > maxProjectPolicyIDBytes || id == SystemPolicyID || strings.TrimSpace(id) != id || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func lowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
