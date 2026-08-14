package compute

import (
	"errors"
	"testing"
)

func TestEnsureArchiveTerminalFencesEphemeralTermination(t *testing.T) {
	if err := EnsureArchiveTerminal([]Instance{{ProviderID: "task-1", ArchiveTerminal: true}}); err != nil {
		t.Fatalf("terminal archive rejected: %v", err)
	}
	if err := EnsureArchiveTerminal([]Instance{{ProviderID: "task-1", ArchiveTerminal: false}}); !errors.Is(err, ErrArchiveNotDrained) {
		t.Fatalf("undrained archive error = %v, want ErrArchiveNotDrained", err)
	}
}

func TestValidateLaunchIntentsFailsClosed(t *testing.T) {
	valid := LaunchIntent{ClientToken: "client", GeneratorID: "generator", LaunchNonce: "nonce"}
	if err := ValidateLaunchIntents([]LaunchIntent{valid}, 1); err != nil {
		t.Fatalf("valid intent: %v", err)
	}
	for _, intents := range [][]LaunchIntent{
		nil,
		{{GeneratorID: "generator", LaunchNonce: "nonce"}},
		{{ClientToken: "client", LaunchNonce: "nonce"}},
		{{ClientToken: "client", GeneratorID: "generator"}},
	} {
		if err := ValidateLaunchIntents(intents, 1); err == nil {
			t.Fatalf("ValidateLaunchIntents(%+v) succeeded", intents)
		}
	}
}

func TestValidateLaunchScopeFailsClosed(t *testing.T) {
	valid := CapacityRequest{OperationID: "operation", ExecutionTargetID: "target", FleetPoolID: "pool"}
	if err := ValidateLaunchScope(valid, "controller", 1); err != nil {
		t.Fatalf("valid scope: %v", err)
	}
	for _, request := range []CapacityRequest{
		{},
		{ExecutionTargetID: "target", FleetPoolID: "pool"},
		{OperationID: "operation", FleetPoolID: "pool"},
		{OperationID: "operation", ExecutionTargetID: "target"},
	} {
		if err := ValidateLaunchScope(request, "controller", 1); err == nil {
			t.Fatalf("ValidateLaunchScope(%+v) succeeded", request)
		}
	}
	if err := ValidateLaunchScope(valid, "", 1); err == nil {
		t.Fatal("ValidateLaunchScope accepted an empty controller identity")
	}
}
