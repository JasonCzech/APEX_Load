package dsl

import "testing"

func eventHubsScript(mutate func(*Script)) *Script {
	s := &Script{
		Name:     "eh-test",
		Protocol: "eventhubs",
		Actions: []Action{
			{Name: "publish", Type: "eventhubs_produce", Target: "telemetry", Body: "hello"},
		},
	}
	s.Config.EventHubs = EventHubsConfig{Mock: true, EventHub: "telemetry"}
	if mutate != nil {
		mutate(s)
	}
	return s
}

func TestValidateEventHubsMockHappyPath(t *testing.T) {
	issues := ValidateScriptIssues(eventHubsScript(nil))
	if len(issues) != 0 {
		t.Fatalf("expected no issues for valid mock script, got: %v", issues)
	}
}

func TestValidateEventHubsNonMockFailsClosed(t *testing.T) {
	// Default build: real adapter unavailable, so non-mock must fail closed even
	// with a valid namespace.
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs = EventHubsConfig{Namespace: "ns.servicebus.windows.net", EventHub: "telemetry"}
	})
	issues := ValidateScriptIssues(s)
	if !hasIssueContaining(issues, "eventhubs_real") {
		t.Fatalf("expected fail-closed issue naming eventhubs_real, got: %v", issues)
	}
}

func TestValidateEventHubsNonMockRequiresEndpoint(t *testing.T) {
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs = EventHubsConfig{EventHub: "telemetry"} // no namespace, no conn string, not mock
	})
	issues := ValidateScriptIssues(s)
	if !hasIssueContaining(issues, "namespace (AAD) or connection_string") {
		t.Fatalf("expected endpoint-required issue, got: %v", issues)
	}
}

func TestValidateEventHubsConnectionStringSecretSatisfiesEndpoint(t *testing.T) {
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs = EventHubsConfig{ConnectionStringSecret: "eh-conn", EventHub: "telemetry"}
	})
	issues := ValidateScriptIssues(s)
	if hasIssueContaining(issues, "namespace (AAD) or connection_string") {
		t.Fatalf("connection_string_secret should satisfy the endpoint requirement, got: %v", issues)
	}
}

func TestValidateEventHubsBadAuthType(t *testing.T) {
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs.AuthType = "kerberos"
	})
	issues := ValidateScriptIssues(s)
	if !hasIssueContaining(issues, "auth_type") {
		t.Fatalf("expected auth_type issue, got: %v", issues)
	}
}

func TestValidateEventHubsMissingHub(t *testing.T) {
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs = EventHubsConfig{Mock: true} // no default hub
		s.Actions = []Action{{Name: "publish", Type: "eventhubs_produce", Body: "x"}}
	})
	issues := ValidateScriptIssues(s)
	if !hasIssueContaining(issues, "missing event hub") {
		t.Fatalf("expected missing-event-hub issue, got: %v", issues)
	}
}

func TestValidateEventHubsPartitionKeyAndIDMutuallyExclusive(t *testing.T) {
	s := eventHubsScript(func(s *Script) {
		s.Actions = []Action{{
			Name:   "publish",
			Type:   "eventhubs_produce",
			Target: "telemetry",
			Body:   "x",
			Params: map[string]any{"partition_key": "k", "partition_id": "0"},
		}}
	})
	issues := ValidateScriptIssues(s)
	if !hasIssueContaining(issues, "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion issue, got: %v", issues)
	}
}

func TestValidateEventHubsUnknownActionType(t *testing.T) {
	s := eventHubsScript(func(s *Script) {
		s.Actions = []Action{{Name: "weird", Type: "eventhubs_frobnicate", Target: "telemetry"}}
	})
	issues := ValidateScriptIssues(s)
	if !hasIssueContaining(issues, "unsupported Event Hubs action type") {
		t.Fatalf("expected unsupported-action issue, got: %v", issues)
	}
}

func TestEventHubsRealAdapterUnavailableInDefaultBuild(t *testing.T) {
	if EventHubsRealAdapterAvailable() {
		t.Fatal("EventHubsRealAdapterAvailable() = true in default build, want false")
	}
}
