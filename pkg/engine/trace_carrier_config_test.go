package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestTraceCarrierReachesProtocolConfig(t *testing.T) {
	script := &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Observability: dsl.ObservabilityConfig{TraceCarrier: " W3C "}}}
	if got := buildProtoConfig(script)[protocol.ConfigMessagingTraceCarrier]; got != protocol.TraceCarrierW3C {
		t.Fatalf("protocol carrier = %v", got)
	}
	if _, set := buildProtoConfig(&dsl.Script{Protocol: "kafka"})[protocol.ConfigMessagingTraceCarrier]; set {
		t.Fatal("scripts without trace_carrier must leave messages unchanged")
	}
}
