package dsl

import "testing"

func TestValidateKafkaCommitConsumerGroupFence(t *testing.T) {
	for _, test := range []struct {
		name       string
		mock       bool
		configured string
		group      string
		refused    bool
	}{
		{name: "real matching", configured: "workers", group: "workers"},
		{name: "real fallback", configured: "workers"},
		{name: "real blank fallback", configured: "workers", group: " \t"},
		{name: "real mismatch", configured: "workers", group: "others", refused: true},
		{name: "real missing configured group", group: "workers", refused: true},
		{name: "real padded mismatch", configured: "workers", group: " workers ", refused: true},
		{name: "real exact padded identity", configured: " workers ", group: " workers "},
		{name: "dynamic action deferred", configured: "workers", group: "{{var.group}}"},
		{name: "dynamic configuration deferred", configured: "{{env.KAFKA_GROUP}}", group: "workers"},
		{name: "dynamic composite deferred", configured: "workers", group: "prefix-{{param.group}}"},
		{name: "mock override preserved", mock: true, configured: "workers", group: "others"},
		{name: "mock unconfigured override preserved", mock: true, group: "workers"},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := kafkaScript(func(s *Script) {
				s.Config.Kafka.Mock = test.mock
				s.Config.Kafka.Brokers = []string{"broker:9092"}
				s.Config.Kafka.ConsumerGroup = test.configured
				s.Actions = []Action{{Name: "commit", Type: "kafka_commit", Params: map[string]any{"consumer_group": test.group}}}
			})
			issues := ValidateScriptIssues(script)
			if got := hasIssueContaining(issues, "one group per client"); got != test.refused {
				t.Fatalf("refused=%t got=%t issues=%v", test.refused, got, issues)
			}
		})
	}
}

func TestValidateNestedKafkaCommitConsumerGroupFence(t *testing.T) {
	script := kafkaScript(func(s *Script) {
		s.Config.Kafka.Mock = false
		s.Config.Kafka.Brokers = []string{"broker:9092"}
		s.Config.Kafka.ConsumerGroup = "workers"
		s.Actions = []Action{{Name: "branch", Type: "control_if", Control: &ControlFlow{Condition: &Condition{Left: "1", Op: "eq", Right: "1"}, Steps: []Action{{Name: "commit", Type: "kafka_commit", Params: map[string]any{"consumer_group": "others"}}}}}}
	})
	issues := ValidateScriptIssues(script)
	if !hasIssueContaining(issues, "one group per client") {
		t.Fatalf("nested mismatched commit bypassed catalog admission: %v", issues)
	}
}
