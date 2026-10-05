package dsl

import "testing"

func TestEventHubsPartitionTailConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		enabled    bool
		interval   int
		invalid    bool
	}{
		{"default", "processor", false, 0, false},
		{"opt in", "processor", true, 0, false},
		{"minimum", "processor", true, 10000, false},
		{"maximum", "processor", true, 300000, false},
		{"off configured", "processor", false, 10000, false},
		{"negative", "processor", true, -1, true},
		{"too fast", "processor", true, 9999, true},
		{"too slow", "processor", true, 300001, true},
		{"observer", "observer", true, 0, true},
		{"implicit observer", "", false, 10000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := eventHubsScript(func(s *Script) {
				s.Config.EventHubs.ConsumerMode = tc.mode
				s.Config.EventHubs.ObservePartitionTail = tc.enabled
				s.Config.EventHubs.TailPollIntervalMs = tc.interval
			})
			issues := validateEventHubsScriptIssues(script)
			bad := hasIssueContaining(issues, "partition-tail settings") || hasIssueContaining(issues, "tail_poll_interval_ms")
			if bad != tc.invalid {
				t.Fatalf("invalid=%v want %v: %v", bad, tc.invalid, issues)
			}
			features := RequiredMessagingRuntimeFeatures(script)
			var progress bool
			for _, f := range features {
				if f == "messaging_progress_v1" {
					progress = true
				}
			}
			if progress != (tc.mode == "processor" && tc.enabled) {
				t.Fatalf("wrong explicit progress admission: %v", features)
			}
		})
	}
}
