package http

import (
	"errors"
	"strings"
	"testing"
)

func TestHTTP3ExecutionNeedsTheFlagAndTheTransport(t *testing.T) {
	previous := http3TransportBuilt
	t.Cleanup(func() { http3TransportBuilt = previous })
	for _, value := range []string{"", "false", "0", "maybe"} {
		t.Setenv(EnvHTTP3, value)
		err := HTTP3ExecutionError()
		if !errors.Is(err, ErrHTTP3NotEnabled) || !strings.Contains(err.Error(), EnvHTTP3+"=true") {
			t.Fatalf("%s=%q: %v, want a refusal naming the flag", EnvHTTP3, value, err)
		}
	}
	t.Setenv(EnvHTTP3, "true")
	http3TransportBuilt = false
	if err := HTTP3ExecutionError(); !errors.Is(err, ErrHTTP3NotEnabled) || !strings.Contains(err.Error(), "no HTTP/3 transport") {
		t.Fatalf("flag on, no transport: %v", err)
	}
	http3TransportBuilt = true
	if err := HTTP3ExecutionError(); err != nil {
		t.Fatalf("flag on with a transport: %v", err)
	}
	// Rollback: turning the flag off blocks new work again.
	t.Setenv(EnvHTTP3, "false")
	if err := HTTP3ExecutionError(); err == nil {
		t.Fatal("disabling the flag did not block HTTP/3")
	}
}

func TestAutoH3WaitsForItsRouter(t *testing.T) {
	previous, previousUpgrade := http3TransportBuilt, http3UpgradeBuilt
	t.Cleanup(func() { http3TransportBuilt, http3UpgradeBuilt = previous, previousUpgrade })
	http3TransportBuilt = true
	t.Setenv(EnvHTTP3, "true")
	http3UpgradeBuilt = false
	if err := HTTP3UpgradeExecutionError(); !errors.Is(err, ErrHTTP3NotEnabled) || !strings.Contains(err.Error(), "auto+h3 is not available") {
		t.Fatalf("upgrade not built: %v", err)
	}
	http3UpgradeBuilt = true
	if err := HTTP3UpgradeExecutionError(); err != nil {
		t.Fatalf("upgrade built: %v", err)
	}
	t.Setenv(EnvHTTP3, "")
	if err := HTTP3UpgradeExecutionError(); err == nil || !strings.Contains(err.Error(), EnvHTTP3) {
		t.Fatalf("flag off: %v", err)
	}
}
