package dsl

import (
	"strings"
	"testing"

	eventhubsprotocol "github.com/jasonczech/apexload/pkg/protocol/eventhubs"
)

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

func TestValidateEventHubsBadStartPosition(t *testing.T) {
	s := eventHubsScript(func(s *Script) {
		s.Config.EventHubs.StartPosition = "middle"
	})
	if issues := ValidateScriptIssues(s); !hasIssueContaining(issues, "start_position") {
		t.Fatalf("expected start_position issue, got: %v", issues)
	}
	ok := eventHubsScript(func(s *Script) {
		s.Config.EventHubs.StartPosition = "earliest"
	})
	if issues := ValidateScriptIssues(ok); hasIssueContaining(issues, "start_position") {
		t.Fatalf("earliest is valid, got start_position issue: %v", issues)
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

func TestValidateEventHubsInlineConnectionStringEntityPathMustMatchHub(t *testing.T) {
	const cs = "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=c3ludGhldGlj;EntityPath=orders"
	for _, tc := range []struct {
		name, connectionString, hub, target, authType string
		wantIssue                                     bool
	}{
		{"matching hub", cs, "Orders", "orders", "", false},
		{"entity path supplies hub", cs, "", "", "", false},
		{"different hub", cs, "payments", "", "", true},
		{"action target differs", cs, "orders", "telemetry", "", true},
		{"action params hub differs", cs, "", "params:telemetry", "", true},
		{"aad ignores connection string", cs, "payments", "telemetry", "aad", false},
		{"secret reference checked at Init", "{{secret.EH}}", "payments", "telemetry", "", false},
		// The EntityPath runs into the key segment without a ';', so it holds
		// the key and must not be named in the issue.
		{"malformed key-bearing entity path", "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;EntityPath=ordersSharedAccessKey=c3ludGhldGlj", "", "orders", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := eventHubsScript(func(s *Script) {
				s.Config.EventHubs = EventHubsConfig{ConnectionString: tc.connectionString, EventHub: tc.hub, AuthType: tc.authType, Namespace: "example.servicebus.windows.net"}
				s.Actions[0].Target = tc.target
				if hub, ok := strings.CutPrefix(tc.target, "params:"); ok {
					s.Actions[0].Target, s.Actions[0].Params = "", map[string]any{"event_hub": hub}
				}
			})
			issues := validateEventHubsScriptIssues(s)
			got := hasIssueContaining(issues, "EntityPath")
			if got != tc.wantIssue {
				t.Fatalf("EntityPath issue = %v, want %v: %v", got, tc.wantIssue, issues)
			}
			if hasIssueContaining(issues, "missing event hub") {
				t.Fatalf("the hub-scoped connection string supplies the hub: %v", issues)
			}
			for _, issue := range issues {
				if strings.Contains(issue, "c3ludGhldGlj") {
					t.Fatalf("issue leaks the key: %q", issue)
				}
			}
		})
	}
}

// The name rule the DSL echo guard uses (eventhubsprotocol.IsEventHubName)
// must accept exactly the EntityPath values the protocol package names in its
// own mismatch errors, including Azure's start/end letter-or-digit rule.
func TestEventHubNameSafeToEchoMatchesProtocolRule(t *testing.T) {
	for _, name := range []string{"orders", "Orders.v2-east_1", "a", "a.b", "-orders", "orders-", ".orders", "orders.", "_orders", "orders_", "-", "ordersSharedAccessKey=c3ludGhldGlj", "a b", "x/y", strings.Repeat("a", 256), strings.Repeat("a", 257)} {
		cs := "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=k;EntityPath=" + name
		_, err := eventhubsprotocol.ResolveConnectionStringEventHub(cs, "other-hub")
		if err == nil {
			t.Fatalf("%.20q: expected a mismatch error", name)
		}
		protocolSafe := !strings.Contains(err.Error(), "not a valid event hub name")
		if got := eventhubsprotocol.IsEventHubName(name); got != protocolSafe {
			t.Fatalf("%.20q: IsEventHubName = %v, protocol names it = %v", name, got, protocolSafe)
		}
	}
}

// A malformed inline connection string whose EntityPath runs into the key
// segment must not supply the hub, and ValidateScriptIssues must not echo the
// key, with or without an action target.
func TestValidateScriptIssuesMalformedKeyBearingEntityPathNeverEchoesKey(t *testing.T) {
	const key = "c3ludGhldGljLWtleQ"
	const cs = "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;EntityPath=ordersSharedAccessKey=" + key
	for _, target := range []string{"", "orders"} {
		t.Run("target="+target, func(t *testing.T) {
			s := eventHubsScript(func(s *Script) {
				s.Config.EventHubs = EventHubsConfig{ConnectionString: cs, Namespace: "example.servicebus.windows.net"}
				s.Actions[0].Target = target
			})
			issues := ValidateScriptIssues(s)
			if !hasIssueContaining(issues, "missing event hub") && !hasIssueContaining(issues, "not a valid event hub name") {
				t.Fatalf("expected a missing or invalid event hub issue, got: %v", issues)
			}
			for _, issue := range issues {
				if strings.Contains(issue, key) || strings.Contains(issue, "SharedAccessKey=") {
					t.Fatalf("issue leaks the key: %q", issue)
				}
			}
		})
	}
}
