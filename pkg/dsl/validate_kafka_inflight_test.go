package dsl

import "testing"

func TestValidateKafkaProducerMaxInFlightRequests(t *testing.T) {
	inflight := func(n int) func(*Script) {
		return func(s *Script) { s.Config.Kafka.Producer = &KafkaProducerConfig{MaxInFlightRequests: &n} }
	}
	for name, test := range map[string]struct {
		mutate  func(*Script)
		wantHas string
	}{
		"zero rejected":                 {mutate: inflight(0), wantHas: "kafka producer_max_in_flight_requests must be an integer between 1 and 2147483647"},
		"idempotent default above five": {mutate: inflight(6), wantHas: "max_in_flight_requests above 5 requires idempotence: disabled"},
		"idempotent at five":            {mutate: inflight(5)},
		"idempotence disabled": {mutate: func(s *Script) {
			inflight(50)(s)
			s.Config.Kafka.Idempotence = "disabled"
		}},
		"leader acks": {mutate: func(s *Script) {
			inflight(50)(s)
			s.Config.Kafka.Acks = "leader"
		}},
	} {
		t.Run(name, func(t *testing.T) {
			issues := validateKafkaScriptIssues(kafkaScript(test.mutate))
			if test.wantHas == "" {
				if len(issues) != 0 {
					t.Fatalf("unexpected issues: %v", issues)
				}
				return
			}
			if !hasIssueContaining(issues, test.wantHas) {
				t.Fatalf("issues %v do not contain %q", issues, test.wantHas)
			}
		})
	}
}
