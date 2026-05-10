package api

import (
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
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

func TestEstimateCapacityAdvisoryUsesLiveAvailabilitySnapshot(t *testing.T) {
	t.Setenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS", "3")
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
