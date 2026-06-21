//go:build eventhubs_real

package eventhubs

import (
	"context"
	"strings"
	"testing"
)

// TestNewRealClientType confirms the eventhubs_real build wires the azeventhubs
// adapter (not the fail-closed stub) into the non-mock path.
func TestNewRealClientType(t *testing.T) {
	c := newRealClient(config{Namespace: "ns.servicebus.windows.net", EventHub: "h"})
	if _, ok := c.(*realClient); !ok {
		t.Fatalf("newRealClient returned %T, want *realClient", c)
	}
}

// TestRealClientSendRequiresEndpoint stays offline: with no namespace and no
// connection string, producer construction fails before any network call.
func TestRealClientSendRequiresEndpoint(t *testing.T) {
	c := newRealClient(config{})
	_, err := c.Send(context.Background(), "h", event{Body: []byte("x")})
	if err == nil {
		t.Fatal("Send succeeded without an endpoint, want error")
	}
	if !strings.Contains(err.Error(), "namespace") && !strings.Contains(err.Error(), "connection_string") {
		t.Fatalf("expected endpoint-required error, got: %v", err)
	}
}

func TestRealClientUseConnString(t *testing.T) {
	cases := []struct {
		name string
		cfg  config
		want bool
	}{
		{"auth_aad pins credential", config{AuthType: "aad", ConnectionString: "cs"}, false},
		{"auth_connection_string pins SAS", config{AuthType: "connection_string", Namespace: "ns"}, true},
		{"auto prefers connection string", config{ConnectionString: "cs"}, true},
		{"auto falls back to namespace", config{Namespace: "ns"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := &realClient{cfg: tc.cfg}
			if got := rc.useConnString(); got != tc.want {
				t.Fatalf("useConnString() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRealClientConnStringAuthMissingConnString(t *testing.T) {
	c := newRealClient(config{AuthType: "connection_string", Namespace: "ns"})
	_, err := c.Send(context.Background(), "h", event{Body: []byte("x")})
	if err == nil || !strings.Contains(err.Error(), "connection_string is required") {
		t.Fatalf("expected connection_string-required error, got: %v", err)
	}
}
