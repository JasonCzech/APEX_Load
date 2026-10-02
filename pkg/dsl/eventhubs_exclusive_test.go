package dsl

import (
	"testing"
)

func TestEventHubsExclusiveDSLAndCapabilityScope(t *testing.T) {
	zero := int64(0)
	cfg := EventHubsConfig{ConsumerMode: "exclusive", OwnerLevel: &zero, EventHub: "events", ConsumerGroup: "workers", PartitionID: "0", Namespace: "namespace.servicebus.windows.net"}
	s := &Script{Name: "exclusive", Protocol: "eventhubs", Config: ScriptConfig{EventHubs: cfg}, Actions: []Action{{Name: "next", Type: "eventhubs_consume"}}}
	features := RequiredMessagingRuntimeFeatures(s)
	if len(features) != 1 || features[0] != "eventhubs_exclusive_consumer_v1" {
		t.Fatal(features)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*EventHubsConfig)
		want   string
	}{
		{"missing epoch", func(c *EventHubsConfig) { c.OwnerLevel = nil }, "owner_level"},
		{"missing partition", func(c *EventHubsConfig) { c.PartitionID = "" }, "partition_id"},
		{"mock", func(c *EventHubsConfig) { c.Mock = true }, "non-mock"},
		{"unsafe epoch", func(c *EventHubsConfig) { v := int64(1 << 53); c.OwnerLevel = &v }, "between"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *s
			tc.mutate(&copy.Config.EventHubs)
			if !hasIssueContaining(ValidateScriptIssues(&copy), tc.want) {
				t.Fatal(ValidateScriptIssues(&copy))
			}
		})
	}
	for _, params := range []map[string]any{{"consumer_group": "other"}, {"event_hub": "other"}} {
		action := Action{Name: "override", Type: "eventhubs_consume", Params: params}
		if !hasIssueContaining(validateEventHubsActionSemanticIssues("actions[0]", action, cfg), "cannot override") {
			t.Fatal("exclusive scope override accepted")
		}
	}
}
