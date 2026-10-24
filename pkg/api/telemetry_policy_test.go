package api

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestTelemetryBudgetPolicyResolvedSemanticsAndHash(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	settings := RunArchiveExecutionSettingsSnapshot{General: GeneralSettings{MetricsIntervalSeconds: 3}}
	policy := captureTelemetryBudgetPolicy(settings, false, now)
	if !validTelemetryBudgetPolicy(policy) || policy.ExpectedControllerPublicationIntervalMs != 3000 || policy.CadenceScope != "local-controller-ticker" {
		t.Fatalf("invalid local receipt: %#v", policy)
	}
	defaults := defaultThresholdSettings()
	if policy.Budgets[0].LimitMs != *defaults.MaxTelemetrySampleAgeMs || !policy.Budgets[0].Enabled {
		t.Fatal("omitted budget must resolve to the existing default")
	}
	later := captureTelemetryBudgetPolicy(settings, false, now.Add(time.Hour))
	if policy.Hash != later.Hash || policy.CapturedAt == later.CapturedAt {
		t.Fatal("receipt time must not change semantic policy identity")
	}
	settings.Thresholds.MaxTelemetrySampleAgeMs = float64Setting(0)
	disabled := captureTelemetryBudgetPolicy(settings, false, now)
	if !validTelemetryBudgetPolicy(disabled) || disabled.Budgets[0].Enabled || disabled.Budgets[0].LimitMs != 0 || disabled.Hash == policy.Hash {
		t.Fatal("explicit zero must disable this budget and change policy identity")
	}
	settings.Thresholds.MaxTelemetrySampleAgeMs = float64Setting(17)
	custom := captureTelemetryBudgetPolicy(settings, false, now)
	if !validTelemetryBudgetPolicy(custom) || custom.Budgets[0].LimitMs != 17 || !custom.Budgets[0].Enabled {
		t.Fatal("positive custom budget must be retained")
	}
	distributed := captureTelemetryBudgetPolicy(settings, true, now)
	if !validTelemetryBudgetPolicy(distributed) || distributed.ExpectedControllerPublicationIntervalMs != 1000 || distributed.CadenceScope != "distributed-controller-coalescing-gate" || distributed.Hash == custom.Hash {
		t.Fatal("distributed cadence must describe its actual coalescing gate")
	}
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		settings.Thresholds.MaxTelemetrySampleAgeMs = &value
		if captureTelemetryBudgetPolicy(settings, false, now) != nil {
			t.Fatalf("invalid budget %v was accepted", value)
		}
	}
	for _, interval := range []int{0, -1} {
		settings.General.MetricsIntervalSeconds = interval
		settings.Thresholds = ThresholdSettings{}
		if got := captureTelemetryBudgetPolicy(settings, false, now); got.ExpectedControllerPublicationIntervalMs != 1000 {
			t.Fatalf("invalid legacy cadence %d must use actual one-second fallback", interval)
		}
	}
}

func TestTelemetryBudgetPolicyRejectsMalformedReceipt(t *testing.T) {
	valid := captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{}, false, time.Now())
	changes := map[string]func(*TelemetryBudgetPolicy){
		"wrong-version":   func(p *TelemetryBudgetPolicy) { p.SchemaVersion = "unknown" },
		"bad-hash":        func(p *TelemetryBudgetPolicy) { p.Hash = "wrong" },
		"modified-budget": func(p *TelemetryBudgetPolicy) { p.Budgets[0].LimitMs++ },
		"negative-budget": func(p *TelemetryBudgetPolicy) { p.Budgets[0].LimitMs = -1; p.Hash = telemetryBudgetPolicyHash(p) },
		"enabled-zero": func(p *TelemetryBudgetPolicy) {
			p.Budgets[0].LimitMs = 0
			p.Budgets[0].Enabled = true
			p.Hash = telemetryBudgetPolicyHash(p)
		},
		"missing-stage": func(p *TelemetryBudgetPolicy) { p.Budgets = p.Budgets[:4]; p.Hash = telemetryBudgetPolicyHash(p) },
		"duplicate-stage": func(p *TelemetryBudgetPolicy) {
			p.Budgets[1].Stage = p.Budgets[0].Stage
			p.Hash = telemetryBudgetPolicyHash(p)
		},
		"wrong-cadence-scope": func(p *TelemetryBudgetPolicy) {
			p.CadenceScope = "generator-measured"
			p.Hash = telemetryBudgetPolicyHash(p)
		},
		"bad-cadence": func(p *TelemetryBudgetPolicy) {
			p.ExpectedControllerPublicationIntervalMs = 0
			p.Hash = telemetryBudgetPolicyHash(p)
		},
		"fractional-local-cadence": func(p *TelemetryBudgetPolicy) {
			p.ExpectedControllerPublicationIntervalMs = 1500
			p.Hash = telemetryBudgetPolicyHash(p)
		},
		"subsecond-local-cadence": func(p *TelemetryBudgetPolicy) {
			p.ExpectedControllerPublicationIntervalMs = 1
			p.Hash = telemetryBudgetPolicyHash(p)
		},
		"missing-time": func(p *TelemetryBudgetPolicy) { p.CapturedAt = time.Time{} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			value := cloneTelemetryBudgetPolicy(valid)
			change(value)
			if validTelemetryBudgetPolicy(value) {
				t.Fatal("malformed policy was accepted")
			}
			evidence := telemetryPolicyEvidenceFromProvenance(&RunArchiveProvenance{TelemetryPolicy: value})
			if evidence.Availability != "unavailable" || evidence.Reason != "immutable-policy-invalid" || evidence.Policy != nil {
				t.Fatalf("invalid receipt must stay explicitly unavailable: %#v", evidence)
			}
		})
	}
	for _, initial := range []*RunArchiveProvenance{nil, {}} {
		evidence := telemetryPolicyEvidenceFromProvenance(initial)
		if evidence.Availability != "unavailable" || evidence.Policy != nil {
			t.Fatal("legacy provenance was invented")
		}
	}
}

func TestTelemetryBudgetPolicyOwnedCopiesAndJSONRoundTrip(t *testing.T) {
	original := captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{}, false, time.Now())
	initial := RunArchiveProvenance{TelemetryPolicy: original, ExecutionSettings: RunArchiveExecutionSettingsSnapshot{Thresholds: ThresholdSettings{MaxTelemetrySampleAgeMs: float64Setting(29)}}}
	masked := maskRunArchiveProvenance(initial)
	evidence := telemetryPolicyEvidenceFromProvenance(&initial)
	live := &LiveMetrics{TelemetryPolicy: evidence}
	cloned := cloneLiveMetrics(live)
	copied := cloneTelemetryPolicyEvidence(evidence)
	masked.TelemetryPolicy.Budgets[0].LimitMs = 100
	*masked.ExecutionSettings.Thresholds.MaxTelemetrySampleAgeMs = 100
	cloned.TelemetryPolicy.Policy.Budgets[0].LimitMs = 200
	copied.Policy.Budgets[0].LimitMs = 300
	if original.Budgets[0].LimitMs == 100 || original.Budgets[0].LimitMs == 200 || original.Budgets[0].LimitMs == 300 || *initial.ExecutionSettings.Thresholds.MaxTelemetrySampleAgeMs != 29 {
		t.Fatal("consumer projection aliases authoritative policy/settings")
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var reopened TelemetryBudgetPolicy
	if err := json.Unmarshal(raw, &reopened); err != nil || !validTelemetryBudgetPolicy(&reopened) {
		t.Fatalf("policy lost authority after JSON round trip: %v %#v", err, reopened)
	}
}

func TestTelemetryBudgetPolicyDurableAdmissionAndRestart(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	path := filepath.Join(t.TempDir(), "policy.db")
	store, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	test := &ManagedTest{ID: "policy-run", Name: "Policy run", Status: "INITIALIZING", ProjectID: storage.DefaultProjectID, CreatedAt: now}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, Status: test.Status, ProjectID: test.ProjectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	settings := runner.GetSystemSettings()
	settings.Thresholds.MaxTelemetrySampleAgeMs = float64Setting(11)
	settings.General.MetricsIntervalSeconds = 3
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	artifacts, err := newRunArtifacts(runner, test, runner.saveRunArchiveRecord)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifacts.recorder.Finalize("ABORTED", now, "fixture cleanup", false, false, nil); err != nil {
		t.Fatal(err)
	}
	first, thresholds := runner.runTelemetryPolicy(test)
	if first.Availability != "available" || *thresholds.MaxTelemetrySampleAgeMs != 11 || first.Policy.ExpectedControllerPublicationIntervalMs != 3000 {
		t.Fatalf("admission policy not retained: %#v", first)
	}
	settings = runner.GetSystemSettings()
	settings.Thresholds.MaxTelemetrySampleAgeMs = float64Setting(999)
	settings.General.MetricsIntervalSeconds = 1
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	second, changedThresholds := runner.runTelemetryPolicy(test)
	if second.Policy.Hash != first.Policy.Hash || *changedThresholds.MaxTelemetrySampleAgeMs != 11 {
		t.Fatal("live settings change reinterpreted admitted run")
	}
	diagnostic, _ := buildTelemetryDiagnostic(test, &LiveMetrics{Telemetry: &TelemetryHealth{SampleAgeMs: 12}}, changedThresholds, "")
	if diagnostic.Severity != "warning" {
		t.Fatal("run diagnostic must use immutable11ms policy despite current999ms settings")
	}
	stored, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(stored.InitialProvenance)
	if initial == nil || !validTelemetryBudgetPolicy(initial.TelemetryPolicy) || initial.TelemetryPolicy.Hash != first.Policy.Hash {
		t.Fatal("durable initial snapshot lost policy")
	}
	reopenedRunner := NewTestRunner(1, NewHub(), store)
	reopened := &ManagedTest{ID: test.ID, ProjectID: test.ProjectID, initialArchiveProvenance: initial}
	recovered, recoveredLimits := reopenedRunner.runTelemetryPolicy(reopened)
	if recovered.Policy.Hash != first.Policy.Hash || *recoveredLimits.MaxTelemetrySampleAgeMs != 11 {
		t.Fatal("restart changed policy")
	}
	candidate := maskRunArchiveProvenance(*initial)
	candidate.TelemetryPolicy = captureTelemetryBudgetPolicy(RunArchiveExecutionSettingsSnapshot{Thresholds: ThresholdSettings{MaxTelemetrySampleAgeMs: float64Setting(999)}}, false, now.Add(time.Hour))
	authoritative, err := reopenedRunner.persistInitialArchiveProvenance(reopened, &candidate)
	if err != nil {
		t.Fatal(err)
	}
	if authoritative.TelemetryPolicy.Hash != first.Policy.Hash {
		t.Fatal("takeover candidate replaced write-once policy")
	}
	// Manifest cache misses must recover the same durable receipt, rather than
	// silently showing it as unavailable or synthesizing current settings.
	runner.mu.Lock()
	test.initialArchiveProvenance = nil
	runner.mu.Unlock()
	manifest, err := runner.GetTestManifest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TelemetryPolicy == nil || manifest.TelemetryPolicy.Policy == nil || manifest.TelemetryPolicy.Policy.Hash != first.Policy.Hash {
		t.Fatal("manifest cache miss lost durable receipt")
	}
	maskedManifest := maskTestRunManifest(manifest)
	maskedManifest.TelemetryPolicy.Policy.Budgets[0].LimitMs = 888
	if manifest.TelemetryPolicy.Policy.Budgets[0].LimitMs != 11 {
		t.Fatal("masked manifest aliases its authoritative receipt")
	}
}

func TestTelemetryBudgetPolicyLegacyUsesRetainedSettingsWithoutClaimingReceipt(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{initialArchiveProvenance: &RunArchiveProvenance{ExecutionSettings: RunArchiveExecutionSettingsSnapshot{Thresholds: ThresholdSettings{MaxTelemetrySampleAgeMs: float64Setting(19)}}}}
	evidence, thresholds := runner.runTelemetryPolicy(test)
	if evidence.Availability != "unavailable" || evidence.Policy != nil || *thresholds.MaxTelemetrySampleAgeMs != 19 {
		t.Fatal("legacy retained thresholds must not receive invented policy identity or current settings")
	}
}
