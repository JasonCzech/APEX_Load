package dsl

import "testing"

func TestEventHubsProcessorPartitionCeilingValidation(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		ceiling    int
		want       string
	}{
		{"default", "processor", 0, ""}, {"lower", "processor", 1, ""}, {"maximum", "processor", 32, ""},
		{"negative", "processor", -1, "must be between"}, {"too large", "processor", 33, "must be between"},
		{"observer", "observer", 1, "requires processor mode"}, {"implicit observer", "", 1, "requires processor mode"},
		{"templated mode", "{{var.mode}}", 1, "requires processor mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := eventHubsScript(func(s *Script) {
				s.Config.EventHubs = EventHubsConfig{ConsumerMode: tc.mode, ProcessorMaxPartitions: tc.ceiling,
					CheckpointContainerURL: "https://account.blob.core.windows.net/checkpoints", AuthType: "aad",
					Namespace: "ns.servicebus.windows.net", ConsumerGroup: "workers", EventHub: "events"}
			})
			issues := validateEventHubsScriptIssues(script)
			if tc.want != "" && !hasIssueContaining(issues, tc.want) {
				t.Fatalf("missing %q: %v", tc.want, issues)
			}
			if tc.want == "" && hasIssueContaining(issues, "processor_max_partitions") {
				t.Fatal(issues)
			}
		})
	}
}
