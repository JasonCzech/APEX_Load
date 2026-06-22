//go:build !eventhubs_real

package eventhubs

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestEventHubsNonMockFailsClosed asserts the default build (no eventhubs_real
// tag) refuses to run a non-mock script: the fail-closed stub is selected and
// every action errors with guidance naming the build tag. Gated to the default
// build because under -tags eventhubs_real the real adapter would attempt a
// live Azure connection instead.
func TestEventHubsNonMockFailsClosed(t *testing.T) {
	ctx := context.Background()
	e := &EventHubs{name: eventHubsProtocolName}
	if err := e.Init(ctx, map[string]any{
		"eventhubs_namespace": "ns.servicebus.windows.net",
		"eventhubs_event_hub": "h",
		// no eventhubs_mock -> non-mock path -> unavailable stub in the default build
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := e.Execute(ctx, &protocol.Action{Name: "p", Type: "eventhubs_produce", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result.Success {
		t.Fatal("non-mock produce succeeded in the default build, want fail-closed")
	}
	if !strings.Contains(result.ErrorMessage, "eventhubs_real") {
		t.Fatalf("expected fail-closed guidance naming eventhubs_real, got: %q", result.ErrorMessage)
	}
	// A failed produce must be counted as a send failure for messaging reliability.
	if result.ServerTelemetry == nil || result.ServerTelemetry.SendFailureCount != 1 {
		t.Fatalf("SendFailureCount = %+v, want 1 on a failed produce", result.ServerTelemetry)
	}
}
