package api

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestEstimateCapacityAdvisoryAppliesHeadroomAndAvailability(t *testing.T) {
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2

	advisory := estimateCapacityAdvisory(settings, []TestGroup{{
		Name:     "buyers",
		Protocol: "http",
		VUsers:   18000,
		script: &dsl.Script{
			Protocol: "http",
			Actions:  []dsl.Action{{Name: "home", Type: "request", Target: "/"}},
		},
	}}, 18000, 30*time.Minute, true, nil)

	if advisory == nil {
		t.Fatal("expected advisory to be returned")
	}
	if advisory.RecommendedGenerators != 3 {
		t.Fatalf("recommended generators = %d, want 3", advisory.RecommendedGenerators)
	}
	if advisory.Bottleneck != "vusers" {
		t.Fatalf("bottleneck = %q, want vusers", advisory.Bottleneck)
	}
	if advisory.ShortfallGenerators != 1 {
		t.Fatalf("shortfall = %d, want 1", advisory.ShortfallGenerators)
	}
	if !advisory.RequiresOverride {
		t.Fatal("expected advisory to require override when configured capacity is short")
	}
	if advisory.UsablePerGenerator.CPUCores != 3.2 {
		t.Fatalf("usable cpu per generator = %v, want 3.2", advisory.UsablePerGenerator.CPUCores)
	}
	if len(advisory.Warnings) == 0 {
		t.Fatal("expected baseline warning to be present")
	}
}

func TestEstimateCapacityAdvisoryIncreasesForHeavierScripts(t *testing.T) {
	settings := defaultSystemSettings(4)
	totalVUsers := 9000

	simple := estimateCapacityAdvisory(settings, []TestGroup{{
		Name:     "simple",
		Protocol: "http",
		VUsers:   totalVUsers,
		script: &dsl.Script{
			Protocol: "http",
			Actions:  []dsl.Action{{Name: "home", Type: "request", Target: "/"}},
		},
	}}, totalVUsers, 15*time.Minute, false, nil)

	heavy := estimateCapacityAdvisory(settings, []TestGroup{{
		Name:           "heavy",
		Protocol:       "http",
		VUsers:         totalVUsers,
		Initialization: "at_once",
		script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				Log:           dsl.LogConfig{Level: "extended"},
				ErrorHandling: dsl.ErrorHandlingConfig{SnapshotOnError: true},
				RunLogic:      dsl.RunLogicConfig{AutoTransactions: "per_action"},
			},
			Actions: []dsl.Action{
				{Name: "login", Type: "request", Target: "/login", Body: "payload", Assertions: []dsl.Assertion{{Type: "status", Expected: 200}}, Extractors: []dsl.Extractor{{Name: "token", Type: "jsonpath", Source: "body", Expression: "$.token"}}},
				{Name: "checkout", Type: "request", Target: "/checkout", Body: "payload", Headers: map[string]string{"A": "1", "B": "2", "C": "3", "D": "4", "E": "5", "F": "6", "G": "7"}},
			},
		},
	}}, totalVUsers, 15*time.Minute, false, nil)

	if simple == nil || heavy == nil {
		t.Fatal("expected both advisories to be returned")
	}
	if heavy.ComplexityScore <= simple.ComplexityScore {
		t.Fatalf("heavy complexity score = %v, want greater than simple %v", heavy.ComplexityScore, simple.ComplexityScore)
	}
	if heavy.EstimatedPerVUser.CPUCores <= simple.EstimatedPerVUser.CPUCores {
		t.Fatalf("heavy per-vuser cpu = %v, want greater than simple %v", heavy.EstimatedPerVUser.CPUCores, simple.EstimatedPerVUser.CPUCores)
	}
	if heavy.RecommendedGenerators < simple.RecommendedGenerators {
		t.Fatalf("heavy recommended generators = %d, want >= simple %d", heavy.RecommendedGenerators, simple.RecommendedGenerators)
	}
}

func TestEstimateCapacityAdvisoryUsesBrowserVUserDensity(t *testing.T) {
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1

	advisory := estimateCapacityAdvisory(settings, []TestGroup{{
		Name:     "mobile checkout",
		Protocol: "browser",
		VUsers:   30,
		script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				Browser: dsl.BrowserConfig{DeviceProfile: "iphone_15_pro"},
			},
			Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "https://example.test"}},
		},
	}}, 30, 10*time.Minute, false, nil)

	if advisory == nil {
		t.Fatal("expected advisory to be returned")
	}
	if advisory.WorkloadClass != "browser" {
		t.Fatalf("workload class = %q, want browser", advisory.WorkloadClass)
	}
	if advisory.BrowserVUsers != 30 {
		t.Fatalf("browser vusers = %d, want 30", advisory.BrowserVUsers)
	}
	if advisory.MaxBrowserVUsersPerGenerator != 25 {
		t.Fatalf("max browser vusers per generator = %d, want 25", advisory.MaxBrowserVUsersPerGenerator)
	}
	if advisory.MaxBrowserCanaryVUsersPerGenerator != 5 {
		t.Fatalf("max browser canary vusers per generator = %d, want 5", advisory.MaxBrowserCanaryVUsersPerGenerator)
	}
	if advisory.BrowserDensityTier != browserDensityTierDiagnosticCanary || advisory.BrowserDensityReleaseGate != browserDensityReleaseGateRequired {
		t.Fatalf("browser density governance = tier %q gate %q, want diagnostic canary requiring evidence", advisory.BrowserDensityTier, advisory.BrowserDensityReleaseGate)
	}
	if !advisory.BrowserDensityOverrideRequired || !advisory.BrowserBenchmarkEvidenceRequired {
		t.Fatalf("browser density gates = override %v evidence %v, want both required", advisory.BrowserDensityOverrideRequired, advisory.BrowserBenchmarkEvidenceRequired)
	}
	if advisory.DimensionRecommendations.VUsers != 2 {
		t.Fatalf("vuser recommendation = %d, want 2", advisory.DimensionRecommendations.VUsers)
	}
	if !advisory.RequiresOverride {
		t.Fatal("expected browser density shortfall to require override with one available generator")
	}
	if advisory.EstimatedPerVUser.MemoryMB < 250 {
		t.Fatalf("browser per-vuser memory = %v, want mobile browser scale estimate", advisory.EstimatedPerVUser.MemoryMB)
	}
	if !containsSubstring(advisory.Warnings, "Browser VUsers launch real browser runtimes") {
		t.Fatalf("warnings = %#v, want browser runtime guidance", advisory.Warnings)
	}
}

func TestEstimateCapacityAdvisoryClearsBrowserDensityGateWithBenchmarkEvidence(t *testing.T) {
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.HeadroomPct = 0
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2

	advisory := estimateCapacityAdvisory(settings, []TestGroup{{
		Name:     "browser checkout",
		Protocol: "browser",
		VUsers:   30,
		script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				Browser: dsl.BrowserConfig{DeviceProfile: "iphone_15_pro"},
			},
			Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "https://example.test"}},
		},
	}}, 30, 10*time.Minute, false, &RuntimeSettingsOverride{
		Env: map[string]string{
			"APEXLOAD_BROWSER_BENCHMARK_EVIDENCE": "benchmarks/browser-density-2026-06-09.txt",
			"APEXLOAD_BROWSER_DENSITY_OVERRIDE":   "true",
		},
	})

	if advisory == nil {
		t.Fatal("expected advisory to be returned")
	}
	if advisory.RequiresOverride {
		t.Fatalf("requires_override = true, want capacity to fit two generators: %#v", advisory)
	}
	if !advisory.BrowserDensityOverrideRequired || !advisory.BrowserBenchmarkEvidenceRequired {
		t.Fatalf("browser density gates = override %v evidence %v, want high-density gate requirement recorded", advisory.BrowserDensityOverrideRequired, advisory.BrowserBenchmarkEvidenceRequired)
	}
	if !advisory.BrowserDensityOverrideRequested || !advisory.BrowserBenchmarkEvidenceAttached {
		t.Fatalf("browser density override/evidence = %v/%v, want both attached", advisory.BrowserDensityOverrideRequested, advisory.BrowserBenchmarkEvidenceAttached)
	}
	if advisory.BrowserDensityTier != browserDensityTierBoundedRealBrowserLoad || advisory.BrowserDensityReleaseGate != browserDensityReleaseGateCleared {
		t.Fatalf("browser density governance = tier %q gate %q, want bounded tier cleared by evidence", advisory.BrowserDensityTier, advisory.BrowserDensityReleaseGate)
	}
	if advisory.BrowserBenchmarkEvidence != "benchmarks/browser-density-2026-06-09.txt" {
		t.Fatalf("benchmark evidence = %q", advisory.BrowserBenchmarkEvidence)
	}
}

func TestEstimateCapacityAdvisoryUsesRuntimeBrowserOverrideForDensity(t *testing.T) {
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 3

	advisory := estimateCapacityAdvisory(settings, []TestGroup{
		{
			Name:     "api",
			Protocol: "http",
			VUsers:   5000,
			script:   &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}}},
		},
		{
			Name:     "browser canary",
			Protocol: "browser",
			VUsers:   35,
			script: &dsl.Script{
				Protocol: "browser",
				Config: dsl.ScriptConfig{
					Browser: dsl.BrowserConfig{DeviceProfile: "pixel_8"},
				},
				Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "https://example.test"}},
			},
		},
	}, 5035, 10*time.Minute, false, &RuntimeSettingsOverride{
		Browser: &RuntimeBrowserOverride{DeviceProfile: "iphone_se"},
	})

	if advisory == nil {
		t.Fatal("expected advisory to be returned")
	}
	if advisory.WorkloadClass != "mixed" {
		t.Fatalf("workload class = %q, want mixed", advisory.WorkloadClass)
	}
	if advisory.BrowserVUsers != 35 {
		t.Fatalf("browser vusers = %d, want 35", advisory.BrowserVUsers)
	}
	if advisory.MaxBrowserVUsersPerGenerator != 40 {
		t.Fatalf("max browser vusers per generator = %d, want runtime override iphone_se density", advisory.MaxBrowserVUsersPerGenerator)
	}
	if advisory.DimensionRecommendations.VUsers != 1 {
		t.Fatalf("vuser recommendation = %d, want mixed scenario to cap only browser vusers by browser density", advisory.DimensionRecommendations.VUsers)
	}
	if advisory.EstimatedTotal.MemoryMB <= 7000 {
		t.Fatalf("estimated total memory = %v, want mixed HTTP plus browser footprint", advisory.EstimatedTotal.MemoryMB)
	}
}

func TestEstimateCapacityAdvisoryAppliesProjectReservations(t *testing.T) {
	settings := defaultSystemSettings(5)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	settings.CapacityPlanning.Reservations = []CapacityReservation{{
		ProjectID:          "critical-checkout",
		ReservedGenerators: 2,
	}}
	groups := []TestGroup{{
		Name:     "buyers",
		Protocol: "http",
		VUsers:   32000,
		script: &dsl.Script{
			Protocol: "http",
			Actions:  []dsl.Action{{Name: "home", Type: "request", Target: "/"}},
		},
	}}

	sharedPoolAdvisory := estimateCapacityAdvisoryForProject(settings, "standard-project", groups, 32000, 30*time.Minute, false, nil)
	if sharedPoolAdvisory == nil {
		t.Fatal("expected shared-pool advisory")
	}
	if sharedPoolAdvisory.AvailableGenerators != 3 {
		t.Fatalf("shared-pool available generators = %d, want 3", sharedPoolAdvisory.AvailableGenerators)
	}
	if sharedPoolAdvisory.SharedAvailableGenerators != 3 {
		t.Fatalf("shared-pool shared generators = %d, want 3", sharedPoolAdvisory.SharedAvailableGenerators)
	}
	if sharedPoolAdvisory.ReservedGenerators != 0 {
		t.Fatalf("shared-pool reserved generators = %d, want 0", sharedPoolAdvisory.ReservedGenerators)
	}
	if !sharedPoolAdvisory.RequiresOverride || sharedPoolAdvisory.ShortfallGenerators != 1 {
		t.Fatalf("shared-pool shortfall = %d override=%v, want one-generator shortfall", sharedPoolAdvisory.ShortfallGenerators, sharedPoolAdvisory.RequiresOverride)
	}
	if !containsSubstring(sharedPoolAdvisory.Warnings, "reserved for other projects") {
		t.Fatalf("shared-pool warnings = %#v, want reservation warning", sharedPoolAdvisory.Warnings)
	}

	reservedProjectAdvisory := estimateCapacityAdvisoryForProject(settings, "critical-checkout", groups, 32000, 30*time.Minute, false, nil)
	if reservedProjectAdvisory == nil {
		t.Fatal("expected reserved-project advisory")
	}
	if reservedProjectAdvisory.AvailableGenerators != 5 {
		t.Fatalf("reserved-project available generators = %d, want 5", reservedProjectAdvisory.AvailableGenerators)
	}
	if reservedProjectAdvisory.SharedAvailableGenerators != 3 {
		t.Fatalf("reserved-project shared generators = %d, want 3", reservedProjectAdvisory.SharedAvailableGenerators)
	}
	if reservedProjectAdvisory.ReservedGenerators != 2 {
		t.Fatalf("reserved-project reserved generators = %d, want 2", reservedProjectAdvisory.ReservedGenerators)
	}
	if reservedProjectAdvisory.RequiresOverride {
		t.Fatalf("reserved-project advisory unexpectedly requires override: %#v", reservedProjectAdvisory)
	}
	if !containsSubstring(reservedProjectAdvisory.Warnings, "Capacity reservation applied") {
		t.Fatalf("reserved-project warnings = %#v, want reservation applied warning", reservedProjectAdvisory.Warnings)
	}
}

func TestEstimateCapacityAdvisoryUsesProtocolDensityEnvelopes(t *testing.T) {
	settings := defaultSystemSettings(5)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 2

	advisory := estimateCapacityAdvisory(settings, []TestGroup{{
		Name:     "mq-orders",
		Protocol: "jms",
		VUsers:   5000,
		script: &dsl.Script{
			Protocol: "jms",
			Actions:  []dsl.Action{{Name: "send", Type: "jms_send", Target: "queue://orders"}},
		},
	}}, 5000, 30*time.Minute, false, nil)

	if advisory == nil {
		t.Fatal("expected advisory")
	}
	if advisory.WorkloadClass != "jms" {
		t.Fatalf("workload class = %q, want jms", advisory.WorkloadClass)
	}
	if len(advisory.ProtocolDensities) != 1 {
		t.Fatalf("protocol densities = %#v, want one JMS envelope", advisory.ProtocolDensities)
	}
	density := advisory.ProtocolDensities[0]
	if density.Protocol != "jms" || density.MaxVUsersPerGenerator != 3000 || density.VUsers != 5000 {
		t.Fatalf("density = %#v, want JMS 5000 VUsers at 3000 VUsers/generator", density)
	}
	if density.BenchmarkProfile != "jms_3k_session_send_4vcpu_8gb" || density.BenchmarkEvidence == "" || density.CalibrationSource != "apexload_benchmark_catalog" || density.CalibratedAt == "" {
		t.Fatalf("density benchmark evidence = %#v, want calibrated JMS profile evidence", density)
	}
	if advisory.DimensionRecommendations.VUsers != 3 {
		t.Fatalf("vuser recommendation = %d, want 3 from JMS density with headroom", advisory.DimensionRecommendations.VUsers)
	}
	if !advisory.RequiresOverride || advisory.ShortfallGenerators != 1 {
		t.Fatalf("shortfall = %d override=%v, want one-generator JMS density shortfall", advisory.ShortfallGenerators, advisory.RequiresOverride)
	}
}

func TestEstimateCapacityAdvisoryCombinesMixedProtocolDensityEnvelopes(t *testing.T) {
	settings := defaultSystemSettings(5)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 4

	advisory := estimateCapacityAdvisory(settings, []TestGroup{
		{
			Name:     "api",
			Protocol: "grpc",
			VUsers:   6000,
			script:   &dsl.Script{Protocol: "grpc", Actions: []dsl.Action{{Name: "call", Type: "grpc_unary", Target: "/payments.Service/Authorize"}}},
		},
		{
			Name:     "notifications",
			Protocol: "websocket",
			VUsers:   6000,
			script:   &dsl.Script{Protocol: "websocket", Actions: []dsl.Action{{Name: "connect", Type: "websocket_connect", Target: "wss://example.test/ws"}}},
		},
	}, 12000, 30*time.Minute, false, nil)

	if advisory == nil {
		t.Fatal("expected advisory")
	}
	if advisory.WorkloadClass != "mixed" {
		t.Fatalf("workload class = %q, want mixed", advisory.WorkloadClass)
	}
	if len(advisory.ProtocolDensities) != 2 {
		t.Fatalf("protocol densities = %#v, want gRPC and WebSocket envelopes", advisory.ProtocolDensities)
	}
	if advisory.ProtocolDensities[0].Protocol != "grpc" || advisory.ProtocolDensities[1].Protocol != "websocket" {
		t.Fatalf("protocol densities = %#v, want sorted gRPC/WebSocket envelopes", advisory.ProtocolDensities)
	}
	if advisory.ProtocolDensities[0].CompatibilityConfidence != "calibrated_model" || advisory.ProtocolDensities[1].BenchmarkProfile == "" {
		t.Fatalf("protocol densities = %#v, want calibrated benchmark metadata", advisory.ProtocolDensities)
	}
	if advisory.DimensionRecommendations.VUsers != 2 {
		t.Fatalf("vuser recommendation = %d, want WebSocket density to drive two generators", advisory.DimensionRecommendations.VUsers)
	}
	if advisory.EstimatedPerVUser.MemoryMB <= 0.35 {
		t.Fatalf("estimated per-vuser memory = %v, want protocol envelope above HTTP default", advisory.EstimatedPerVUser.MemoryMB)
	}
}

func TestCreateTestAttachesCapacityAdvisory(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			Log: dsl.LogConfig{Level: "extended"},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/", Body: "payload"}},
	})

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Capacity Scenario",
		Duration: "10m",
		ScriptID: "script-1",
		VUsers:   12000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if test.CapacityAdvisory == nil {
		t.Fatal("expected capacity advisory to be attached to created test")
	}
	if test.CapacityAdvisory.TotalVUsers != 12000 {
		t.Fatalf("advisory total vusers = %d, want 12000", test.CapacityAdvisory.TotalVUsers)
	}
	if test.CapacityAdvisory.RecommendedGenerators < 2 {
		t.Fatalf("recommended generators = %d, want at least 2", test.CapacityAdvisory.RecommendedGenerators)
	}
}

func containsSubstring(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func TestEstimateDraftCapacityReturnsAdvisoryWithoutPersistingTest(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-estimate",
		Name:     "Estimate Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	estimate, err := runner.EstimateDraftCapacity(CreateTestRequest{
		Duration:         "10m",
		ScriptID:         "script-estimate",
		VUsers:           9000,
		ResourceBaseline: true,
	})
	if err != nil {
		t.Fatalf("EstimateDraftCapacity returned error: %v", err)
	}
	if estimate.CapacityAdvisory == nil {
		t.Fatal("expected draft estimate to include capacity advisory")
	}
	if estimate.TotalVUsers != 9000 {
		t.Fatalf("total vusers = %d, want 9000", estimate.TotalVUsers)
	}
	if !estimate.ResourceBaseline {
		t.Fatal("expected resource baseline flag to be preserved")
	}
	if len(runner.ListTests()) != 0 {
		t.Fatalf("expected draft estimate not to persist a test, got %d tests", len(runner.ListTests()))
	}
}

func TestEstimateDraftCapacityIncludesAssignmentRecommendation(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-a", "gen-b", "gen-c"}, 5*time.Second, nil))
	runner.StoreScript(&dsl.Script{
		ID:       "script-assignment-estimate",
		Name:     "Assignment Estimate Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 3
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	estimate, err := runner.EstimateDraftCapacity(CreateTestRequest{
		Duration: "10m",
		ScriptID: "script-assignment-estimate",
		VUsers:   9000,
		Assignment: &GeneratorAssignmentSettings{
			Mode:            generatorAssignmentModeSpecificHosts,
			SelectedTargets: []string{"gen-a"},
		},
	})
	if err != nil {
		t.Fatalf("EstimateDraftCapacity returned error: %v", err)
	}
	if estimate.Footprint == nil {
		t.Fatal("expected scenario footprint summary")
	}
	if estimate.Recommendation == nil {
		t.Fatal("expected assignment recommendation")
	}
	if estimate.Recommendation.Mode != generatorAssignmentModeSpecificHosts {
		t.Fatalf("recommendation mode = %q, want %q", estimate.Recommendation.Mode, generatorAssignmentModeSpecificHosts)
	}
	if len(estimate.Recommendation.AvailableTargets) != 3 {
		t.Fatalf("available targets = %v, want 3 configured targets", estimate.Recommendation.AvailableTargets)
	}
	if estimate.CapacityAdvisory == nil || estimate.CapacityAdvisory.AvailableGenerators != 1 {
		t.Fatalf("available generators = %#v, want 1 explicit host", estimate.CapacityAdvisory)
	}
	if !estimate.CapacityAdvisory.RequiresOverride {
		t.Fatal("expected explicit single-host estimate to require override")
	}
}

func TestEstimateDraftCapacityIncludesRegionWeightedRecommendation(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-a|region=us-east", "gen-b|region=us-east", "gen-c|region=eu-west"}, 5*time.Second, nil))
	runner.StoreScript(&dsl.Script{
		ID:       "script-region-estimate",
		Name:     "Region Estimate Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 3
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	estimate, err := runner.EstimateDraftCapacity(CreateTestRequest{
		Duration: "10m",
		ScriptID: "script-region-estimate",
		VUsers:   9000,
		Assignment: &GeneratorAssignmentSettings{
			Mode: generatorAssignmentModeRegionWeighted,
			RegionWeights: map[string]int{
				"us-east": 2,
				"eu-west": 1,
			},
		},
	})
	if err != nil {
		t.Fatalf("EstimateDraftCapacity returned error: %v", err)
	}
	if estimate.Recommendation == nil {
		t.Fatal("expected assignment recommendation")
	}
	if estimate.Recommendation.Mode != generatorAssignmentModeRegionWeighted {
		t.Fatalf("recommendation mode = %q, want %q", estimate.Recommendation.Mode, generatorAssignmentModeRegionWeighted)
	}
	if !reflect.DeepEqual(estimate.Recommendation.AvailableRegions, []string{"eu-west", "us-east"}) {
		t.Fatalf("available regions = %v, want [eu-west us-east]", estimate.Recommendation.AvailableRegions)
	}
	if !strings.Contains(estimate.Recommendation.Summary, "us-east 67%") || !strings.Contains(estimate.Recommendation.Summary, "eu-west 33%") {
		t.Fatalf("recommendation summary = %q, want weighted region summary", estimate.Recommendation.Summary)
	}
	if estimate.CapacityAdvisory == nil || estimate.CapacityAdvisory.AvailableGenerators != 3 {
		t.Fatalf("capacity advisory = %#v, want 3 available generators across weighted regions", estimate.CapacityAdvisory)
	}
	if estimate.CapacityAdvisory.RequiresOverride {
		t.Fatalf("expected weighted region estimate to fit available tagged generators, advisory=%#v", estimate.CapacityAdvisory)
	}
}

func TestEstimateCapacityAdvisoryUsesLiveAvailabilitySnapshot(t *testing.T) {
	t.Setenv("APEXLOAD_LIVE_AVAILABLE_GENERATORS", "3")
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5

	advisory := estimateCapacityAdvisory(settings, []TestGroup{{
		Name:     "buyers",
		Protocol: "http",
		VUsers:   18000,
		script: &dsl.Script{
			Protocol: "http",
			Actions:  []dsl.Action{{Name: "home", Type: "request", Target: "/"}},
		},
	}}, 18000, 30*time.Minute, false, nil)

	if advisory == nil {
		t.Fatal("expected advisory")
	}
	if advisory.AvailableGenerators != 3 {
		t.Fatalf("available generators = %d, want 3", advisory.AvailableGenerators)
	}
	if advisory.LiveAvailableGenerators == nil || *advisory.LiveAvailableGenerators != 3 {
		t.Fatalf("live available generators = %#v, want 3", advisory.LiveAvailableGenerators)
	}
	if advisory.ConfiguredAvailableGenerators != 5 {
		t.Fatalf("configured available generators = %d, want 5", advisory.ConfiguredAvailableGenerators)
	}
	if advisory.AvailabilitySource != capacityAvailabilityConfiguredPlusLive {
		t.Fatalf("availability source = %q, want %q", advisory.AvailabilitySource, capacityAvailabilityConfiguredPlusLive)
	}
}
