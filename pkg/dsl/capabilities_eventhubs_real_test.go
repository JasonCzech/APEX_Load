//go:build eventhubs_real

package dsl

import "testing"

func TestEventHubsRealAdapterAvailableInRealBuild(t *testing.T) {
	if !EventHubsRealAdapterAvailable() {
		t.Fatal("EventHubsRealAdapterAvailable() = false under -tags eventhubs_real, want true")
	}
}

func TestValidateEventHubsNonMockPassesInRealBuild(t *testing.T) {
	// Under eventhubs_real the real adapter is present, so a well-formed non-mock
	// script must validate cleanly (no fail-closed issue).
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs = EventHubsConfig{Namespace: "ns.servicebus.windows.net", EventHub: "telemetry"}
	})
	issues := ValidateScriptIssues(s)
	if len(issues) != 0 {
		t.Fatalf("expected no issues for valid non-mock script in real build, got: %v", issues)
	}
}
