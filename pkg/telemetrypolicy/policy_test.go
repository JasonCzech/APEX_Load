package telemetrypolicy

import (
	"math"
	"strings"
	"testing"
	"time"
)

func policyFixture() *TelemetryBudgetPolicy {
	p := &TelemetryBudgetPolicy{SchemaVersion: Version, PolicyID: "system-telemetry-budgets", Revision: 1, CapturedAt: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), CaptureBoundary: "execution-admission-before-staging", ClassificationAlgorithm: "legacy-mixed-diagnostic.v1", ExpectedControllerPublicationIntervalMs: 1000, CadenceScope: "local-controller-ticker", SustainedBreachWindowMs: SustainedBreachWindow.Milliseconds(), CoverageRule: "retained-classifications-only;gaps-unknown"}
	for _, stage := range []string{"sample_age", "generator_processing", "transport", "controller_fanout", "browser_render"} {
		p.Budgets = append(p.Budgets, TelemetryStageBudget{Stage: stage, Enabled: true, LimitMs: 100})
	}
	p.Hash = Hash(p)
	return p
}

func TestPolicyReceiptHashAndOwnership(t *testing.T) {
	p := policyFixture()
	if !Validate(p) || !ValidateTelemetryBudgetPolicy(p) {
		t.Fatal("valid receipt rejected")
	}
	owned := Clone(p)
	owned.CapturedAt = owned.CapturedAt.Add(time.Hour)
	owned.Hash = "ignored by hash"
	if Hash(owned) != p.Hash {
		t.Fatal("capture metadata altered semantic hash")
	}
	owned.Budgets[0].LimitMs++
	if p.Budgets[0].LimitMs != 100 || Hash(owned) == p.Hash {
		t.Fatal("clone aliases source or semantic mutation was not hashed")
	}
	if Clone(nil) != nil || Hash(nil) != "" || Validate(nil) {
		t.Fatal("nil receipt changed")
	}
	p.Budgets[0].LimitMs++
	if Validate(p) {
		t.Fatal("tampered receipt admitted")
	}
}

func TestPolicyValidationBoundaries(t *testing.T) {
	for name, change := range map[string]func(*TelemetryBudgetPolicy){
		"wrong version":      func(p *TelemetryBudgetPolicy) { p.SchemaVersion = "future" },
		"missing capture":    func(p *TelemetryBudgetPolicy) { p.CapturedAt = time.Time{} },
		"non UTC capture":    func(p *TelemetryBudgetPolicy) { p.CapturedAt = p.CapturedAt.In(time.FixedZone("offset", 3600)) },
		"subsecond cadence":  func(p *TelemetryBudgetPolicy) { p.ExpectedControllerPublicationIntervalMs = 999 },
		"fractional cadence": func(p *TelemetryBudgetPolicy) { p.ExpectedControllerPublicationIntervalMs = 1500 },
		"overflow cadence":   func(p *TelemetryBudgetPolicy) { p.ExpectedControllerPublicationIntervalMs = math.MaxInt64 },
		"distributed cadence": func(p *TelemetryBudgetPolicy) {
			p.CadenceScope = "distributed-controller-coalescing-gate"
			p.ExpectedControllerPublicationIntervalMs = 2000
		},
		"missing stage":     func(p *TelemetryBudgetPolicy) { p.Budgets = p.Budgets[:4] },
		"reordered stage":   func(p *TelemetryBudgetPolicy) { p.Budgets[0], p.Budgets[1] = p.Budgets[1], p.Budgets[0] },
		"negative limit":    func(p *TelemetryBudgetPolicy) { p.Budgets[0].LimitMs = -1 },
		"nan limit":         func(p *TelemetryBudgetPolicy) { p.Budgets[0].LimitMs = math.NaN() },
		"infinite limit":    func(p *TelemetryBudgetPolicy) { p.Budgets[0].LimitMs = math.Inf(1) },
		"disabled positive": func(p *TelemetryBudgetPolicy) { p.Budgets[0].Enabled = false },
		"enabled zero":      func(p *TelemetryBudgetPolicy) { p.Budgets[0].LimitMs = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			p := policyFixture()
			change(p)
			p.Hash = Hash(p)
			if Validate(p) {
				t.Fatal("invalid policy accepted")
			}
		})
	}
	for _, scope := range []string{"local-controller-ticker", "distributed-controller-coalescing-gate"} {
		p := policyFixture()
		p.CadenceScope = scope
		p.Budgets[0].LimitMs = 0
		p.Budgets[0].Enabled = false
		p.Hash = Hash(p)
		if !Validate(p) {
			t.Fatalf("valid disabled stage/scope %s rejected", scope)
		}
	}
}

func projectPolicyFixture() *TelemetryBudgetPolicy {
	p := policyFixture()
	p.SchemaVersion, p.PolicyID, p.Revision = VersionProjectPolicy, "tight-budgets", 3
	p.DefinitionSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p.Hash = Hash(p)
	return p
}

func TestProjectPolicyReceiptIdentity(t *testing.T) {
	if p := projectPolicyFixture(); !Validate(p) {
		t.Fatal("valid project policy receipt rejected")
	}
	if p := policyFixture(); Hash(p) != p.Hash || !Validate(p) {
		t.Fatal("the version 1 receipt changed")
	}
	for name, change := range map[string]func(*TelemetryBudgetPolicy){
		"v1 with digest":    func(p *TelemetryBudgetPolicy) { p.SchemaVersion, p.PolicyID, p.Revision = Version, SystemPolicyID, 1 },
		"v1 other id":       func(p *TelemetryBudgetPolicy) { p.SchemaVersion, p.DefinitionSHA256 = Version, "" },
		"v2 system id":      func(p *TelemetryBudgetPolicy) { p.PolicyID = SystemPolicyID },
		"v2 empty id":       func(p *TelemetryBudgetPolicy) { p.PolicyID = "" },
		"v2 padded id":      func(p *TelemetryBudgetPolicy) { p.PolicyID = " tight" },
		"v2 control id":     func(p *TelemetryBudgetPolicy) { p.PolicyID = "tight\n" },
		"v2 long id":        func(p *TelemetryBudgetPolicy) { p.PolicyID = strings.Repeat("a", 129) },
		"v2 revision zero":  func(p *TelemetryBudgetPolicy) { p.Revision = 0 },
		"v2 missing digest": func(p *TelemetryBudgetPolicy) { p.DefinitionSHA256 = "" },
		"v2 upper digest":   func(p *TelemetryBudgetPolicy) { p.DefinitionSHA256 = strings.ToUpper(p.DefinitionSHA256) },
		"v2 short digest":   func(p *TelemetryBudgetPolicy) { p.DefinitionSHA256 = p.DefinitionSHA256[:63] },
		"v2 other window":   func(p *TelemetryBudgetPolicy) { p.SustainedBreachWindowMs = 30000 },
		"unknown version":   func(p *TelemetryBudgetPolicy) { p.SchemaVersion = "apexload.telemetry_policy.v3" },
	} {
		t.Run(name, func(t *testing.T) {
			p := projectPolicyFixture()
			change(p)
			p.Hash = Hash(p)
			if Validate(p) {
				t.Fatal("invalid project policy receipt accepted")
			}
		})
	}
	// The digest covers the chosen revision's identity.
	p := projectPolicyFixture()
	p.Revision++
	if Validate(p) {
		t.Fatal("a changed revision kept its hash")
	}
}
