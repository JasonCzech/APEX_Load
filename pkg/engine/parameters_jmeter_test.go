package engine

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
	"time"
)

func TestParameterRuntimeJMeterMilliseconds(t *testing.T) {
	r := NewParameterRuntime([]dsl.Parameter{{Name: "clock", Type: "date_time", Update: "each_occurrence", Format: "HH:mm:ss.S", FormatDialect: "java_simple_date_format", Timezone: "UTC"}}, nil, 0)
	for _, c := range []struct {
		ms   int
		want string
	}{{1, "00:00:00.1"}, {12, "00:00:00.12"}, {123, "00:00:00.123"}} {
		r.now = func() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, c.ms*1000000, time.UTC) }
		got, ok := r.ResolveNamed("clock")
		if !ok || got != c.want {
			t.Fatalf("got %q %v", got, ok)
		}
	}
}
func TestJMeterCompatibilityCannotBypassEngineGate(t *testing.T) {
	s := &dsl.Script{Name: "pending", Protocol: "kafka", Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile}, Kafka: dsl.KafkaConfig{Mock: true}}, Actions: []dsl.Action{{Name: "send", Type: "kafka_produce", Target: "events"}}}
	if _, e := New(Config{Script: s, VUsers: 1, Duration: time.Second}); e == nil {
		t.Fatal("unverified profile executed after metadata removal")
	}
}

func TestJMeterStrippedProfileCannotBypassRemainingContracts(t *testing.T) {
	for _, s := range []*dsl.Script{
		{Actions: []dsl.Action{{Name: "paced", Type: "GET", PacingIDs: []string{"timer"}}}},
		{Parameters: []dsl.Parameter{{Name: "counter", Type: "counter", Scope: "vuser", ScopeID: "source-counter", Step: 1}}},
	} {
		s.Name, s.Protocol = "stripped", "http"
		if _, err := New(Config{Script: s, VUsers: 1, Duration: time.Second}); err == nil {
			t.Fatal("direct engine accepted compatibility settings without profile")
		}
	}
}
