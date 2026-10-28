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
	if advisory.MaxBrowserVUsersPerGenerator != 5 {
		t.Fatalf("max browser vusers per generator = %d, want the claimable diagnostic canary envelope of 5", advisory.MaxBrowserVUsersPerGenerator)
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
	if advisory.DimensionRecommendations.VUsers != 1 {
		t.Fatalf("vuser recommendation = %d, want the blocking density gate to govern instead of a duplicate vuser shortfall", advisory.DimensionRecommendations.VUsers)
	}
	if !advisory.RequiresOverride || advisory.Bottleneck != "memory" {
		t.Fatalf("requires override = %v bottleneck = %q, want browser memory footprint shortfall with one available generator", advisory.RequiresOverride, advisory.Bottleneck)
	}
	if advisory.EstimatedPerVUser.MemoryMB < roundCapacityValue(browserDecimalMBToMiB(250)) {
		t.Fatalf("browser per-vuser memory = %v, want mobile browser scale estimate above the 250 MB decimal target (%v MiB)", advisory.EstimatedPerVUser.MemoryMB, roundCapacityValue(browserDecimalMBToMiB(250)))
	}
	if !containsSubstring(advisory.Warnings, "Browser VUsers launch real browser runtimes") {
		t.Fatalf("warnings = %#v, want browser runtime guidance", advisory.Warnings)
	}
}

func TestEstimateCapacityAdvisoryPublishesClaimableBrowserDensity(t *testing.T) {
	browserAdvisory := func(device string, availableGenerators int) *CapacityAdvisory {
		settings := defaultSystemSettings(4)
		settings.CapacityPlanning.HeadroomPct = 0
		settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = availableGenerators
		return estimateCapacityAdvisory(settings, []TestGroup{{
			Name:     "ux canary",
			Protocol: "browser",
			VUsers:   30,
			script: &dsl.Script{
				Protocol: "browser",
				Config: dsl.ScriptConfig{
					Browser: dsl.BrowserConfig{DeviceProfile: device},
				},
				Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "https://example.test"}},
			},
		}}, 30, 10*time.Minute, false, nil)
	}

	// iphone_se is a low device tier (40 VUsers/generator in the resource model),
	// ipad_air mid (32) and iphone_15_pro high (25). None of those densities is
	// claimable, so all three must publish the diagnostic canary envelope.
	for _, device := range []string{"iphone_se", "ipad_air", "iphone_15_pro"} {
		advisory := browserAdvisory(device, 8)
		if advisory == nil {
			t.Fatalf("%s: expected advisory", device)
		}
		if advisory.BrowserDensityReleaseGate != browserDensityReleaseGateClear {
			t.Fatalf("%s: release gate = %q, want clear for 30 VUsers across 8 generators", device, advisory.BrowserDensityReleaseGate)
		}
		if advisory.MaxBrowserVUsersPerGenerator != advisory.MaxBrowserCanaryVUsersPerGenerator {
			t.Fatalf("%s: published density = %d but the enforced canary envelope is %d", device, advisory.MaxBrowserVUsersPerGenerator, advisory.MaxBrowserCanaryVUsersPerGenerator)
		}
		if advisory.DimensionRecommendations.VUsers != 6 {
			t.Fatalf("%s: vuser recommendation = %d, want 30 VUsers sized at the 5 VUser/generator canary envelope", device, advisory.DimensionRecommendations.VUsers)
		}

		// Provisioning exactly what the advisory recommends must not be refused by
		// the release gate published in the same response.
		sized := browserAdvisory(device, advisory.RecommendedGenerators)
		if sized == nil {
			t.Fatalf("%s: expected sized advisory", device)
		}
		if sized.BrowserDensityReleaseGate != browserDensityReleaseGateClear || sized.BrowserDensityOverrideRequired {
			t.Fatalf("%s: sizing to the recommended %d generators still trips the density gate (%q, override required %v)", device, advisory.RecommendedGenerators, sized.BrowserDensityReleaseGate, sized.BrowserDensityOverrideRequired)
		}
		if sized.RequiresOverride {
			t.Fatalf("%s: sizing to the recommended %d generators still reports a capacity shortfall: %#v", device, advisory.RecommendedGenerators, sized)
		}
	}

	// The device-tier model still drives the resource footprint even though it no
	// longer sets the published density.
	lowTier := browserAdvisory("iphone_se", 8)
	highTier := browserAdvisory("iphone_15_pro", 8)
	if highTier.EstimatedPerVUser.MemoryMB <= lowTier.EstimatedPerVUser.MemoryMB {
		t.Fatalf("per-vuser memory low tier = %v high tier = %v, want device-tier footprint to remain in the CPU/memory math", lowTier.EstimatedPerVUser.MemoryMB, highTier.EstimatedPerVUser.MemoryMB)
	}
}

func TestBrowserCapacityProfileReconcilesDecimalMBTargetToGeneratorMiB(t *testing.T) {
	targetMiB := float64(browserTargetRSSBytes) / float64(1<<20)
	if got := browserDecimalMBToMiB(250); got != targetMiB {
		t.Fatalf("browserDecimalMBToMiB(250) = %v, want %v", got, targetMiB)
	}
	low := browserCapacityProfileForTier("low")
	mid := browserCapacityProfileForTier("mid")
	high := browserCapacityProfileForTier("high")
	if !(low.BrowserMemoryMBPerVUser < mid.BrowserMemoryMBPerVUser && mid.BrowserMemoryMBPerVUser < targetMiB && targetMiB < high.BrowserMemoryMBPerVUser) {
		t.Fatalf("expected low (%v) < mid (%v) < 250 MB target (%v MiB) < high (%v)", low.BrowserMemoryMBPerVUser, mid.BrowserMemoryMBPerVUser, targetMiB, high.BrowserMemoryMBPerVUser)
	}
	if high.BrowserMemoryMBPerVUser >= 260 {
		t.Fatalf("expected high-tier 260 decimal MB converted to binary MiB (< 260), got %v", high.BrowserMemoryMBPerVUser)
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
	if advisory.MaxBrowserVUsersPerGenerator != 5 {
		t.Fatalf("max browser vusers per generator = %d, want the claimable canary envelope even for the low-tier iphone_se runtime override", advisory.MaxBrowserVUsersPerGenerator)
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

func TestEstimateCapacityAdvisoryPlansHTTP3GroupsInTheUnmeasuredClass(t *testing.T) {
	settings := defaultSystemSettings(5)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	script := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.example", HTTP: dsl.HTTPConfig{Version: "auto+h3"}},
		Actions: []dsl.Action{{Name: "home", Type: "GET", Target: "/"}}}
	advisory := estimateCapacityAdvisory(settings, []TestGroup{{Name: "h3", Protocol: "http", VUsers: 6000, script: script}}, 6000, 30*time.Minute, false, nil)
	if advisory == nil || advisory.WorkloadClass != "http3" || len(advisory.ProtocolDensities) != 1 {
		t.Fatalf("advisory = %#v", advisory)
	}
	density := advisory.ProtocolDensities[0]
	if density.MaxVUsersPerGenerator != 4000 || density.CompatibilityConfidence != "unmeasured" || density.CalibratedAt != "" {
		t.Fatalf("HTTP/3 must not reuse the HTTP design target: %#v", density)
	}
	if !strings.Contains(strings.Join(advisory.Warnings, "\n"), "HTTP/3 density is not yet measured") {
		t.Fatalf("warnings = %v", advisory.Warnings)
	}
	for _, warning := range advisory.Warnings {
		if strings.Contains(warning, "Unknown protocol") {
			t.Fatalf("http3 is a known class: %v", advisory.Warnings)
		}
	}

	// A group override that replaces config.http with HTTP/2 plans as HTTP.
	override := &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Version: "2"}}
	tcp := estimateCapacityAdvisory(settings, []TestGroup{{Name: "h2", Protocol: "http", VUsers: 6000, script: script, RuntimeSettings: override}}, 6000, 30*time.Minute, false, nil)
	if tcp.WorkloadClass != "http" || tcp.ProtocolDensities[0].MaxVUsersPerGenerator != 10000 {
		t.Fatalf("overridden advisory = %#v", tcp.ProtocolDensities)
	}
}

func TestCanonicalCapacityProtocolTreatsServiceBusAsJMS(t *testing.T) {
	for _, protocol := range []string{"jms-servicebus", "jms_servicebus", "servicebus", "azure service bus"} {
		if got := canonicalCapacityProtocol(TestGroup{Protocol: protocol}); got != "jms" {
			t.Fatalf("canonicalCapacityProtocol(%q) = %q, want jms", protocol, got)
		}
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
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	live := 3
	availability := resolveCapacityAvailabilityWithLiveAt(settings, &live, time.Now())

	advisory := estimateCapacityAdvisoryWithAvailability(settings, availability, []TestGroup{{
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
