package importreview

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A non-mock Kafka or Event Hubs script cannot reach its brokers on a build
// without the real client, so readiness warns (it does not fail: the same
// script runs on an enterprise build) and says why in plain words.
func TestReplayMessagingPreflightWarnsWithoutRealAdapter(t *testing.T) {
	restoreKafka, restoreEventHubs := realKafkaAdapterCompiled, realEventHubsAdapterCompiled
	t.Cleanup(func() { realKafkaAdapterCompiled, realEventHubsAdapterCompiled = restoreKafka, restoreEventHubs })

	kafkaScript := &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Brokers: []string{"broker.example.test:9093"}}}}
	eventHubsScript := &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{Namespace: "ns.servicebus.windows.net"}}}
	mockScript := &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{Mock: true}}}

	check := func(t *testing.T, script *dsl.Script, id string) (ReplayPreflightCheck, []string, []string) {
		t.Helper()
		issues, warnings, checks := ReplayMessagingPreflight(script)
		for _, candidate := range checks {
			if candidate.ID == id {
				return candidate, issues, warnings
			}
		}
		t.Fatalf("no %s check in %#v", id, checks)
		return ReplayPreflightCheck{}, nil, nil
	}

	for _, compiled := range []bool{false, true} {
		realKafkaAdapterCompiled = func() bool { return compiled }
		realEventHubsAdapterCompiled = func() bool { return compiled }
		for _, tc := range []struct {
			script *dsl.Script
			id     string
			tag    string
		}{{kafkaScript, "messaging_kafka", "kafka_real"}, {eventHubsScript, "messaging_eventhubs", "eventhubs_real"}} {
			got, issues, warnings := check(t, tc.script, tc.id)
			if len(issues) != 0 {
				t.Fatalf("%s compiled=%v issues = %v, want none: a build gap is not a script defect", tc.id, compiled, issues)
			}
			if compiled {
				if got.Status != "pass" || len(warnings) != 0 {
					t.Fatalf("%s with the real client = %s %v, want pass", tc.id, got.Status, warnings)
				}
				continue
			}
			if got.Status != "warn" || len(warnings) != 1 || !strings.Contains(got.Detail, tc.tag) || !strings.Contains(warnings[0], "enterprise build") {
				t.Fatalf("%s without the real client = %s %q %v, want a plain warning naming %s", tc.id, got.Status, got.Detail, warnings, tc.tag)
			}
		}
	}
	realEventHubsAdapterCompiled = func() bool { return false }
	if got, _, warnings := check(t, mockScript, "messaging_eventhubs"); got.Status != "warn" || len(warnings) != 0 || !strings.Contains(got.Detail, "mock") {
		t.Fatalf("mock Event Hubs = %s %q %v, want the mock warning only", got.Status, got.Detail, warnings)
	}
}
