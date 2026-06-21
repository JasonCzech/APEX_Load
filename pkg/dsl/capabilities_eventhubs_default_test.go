//go:build !eventhubs_real

package dsl

import "testing"

func TestEventHubsRealAdapterUnavailableInDefaultBuild(t *testing.T) {
	if EventHubsRealAdapterAvailable() {
		t.Fatal("EventHubsRealAdapterAvailable() = true in default build, want false")
	}
}

func TestValidateEventHubsNonMockFailsClosed(t *testing.T) {
	// Default build: real adapter unavailable, so a non-mock script must fail
	// closed even with a valid namespace.
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs = EventHubsConfig{Namespace: "ns.servicebus.windows.net", EventHub: "telemetry"}
	})
	issues := ValidateScriptIssues(s)
	if !hasIssueContaining(issues, "eventhubs_real") {
		t.Fatalf("expected fail-closed issue naming eventhubs_real, got: %v", issues)
	}
}
