//go:build kafka_real

package kafka

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/identitypolicy"
)

// TestEventHubsOAuthScope verifies the OAUTHBEARER token scope resolution (EH.2):
// only the Event Hubs resource scope is accepted; project-authored arbitrary
// scopes could otherwise mint unrelated managed-identity bearer tokens.
func TestEventHubsOAuthScope(t *testing.T) {
	cases := []struct {
		name     string
		brokers  []string
		explicit string
		want     string
		wantErr  bool
	}{
		{"fixed default", []string{"ns.servicebus.windows.net:9093"}, "", identitypolicy.AzureEventHubsOAuthScope, false},
		{"explicit fixed scope", []string{"ns.servicebus.windows.net:9093"}, identitypolicy.AzureEventHubsOAuthScope, identitypolicy.AzureEventHubsOAuthScope, false},
		{"custom scope rejected", []string{"ns.servicebus.windows.net:9093"}, "https://graph.microsoft.com/.default", "", true},
	}
	for _, c := range cases {
		got, err := eventHubsOAuthScope(c.brokers, c.explicit)
		if c.wantErr != (err != nil) || got != c.want {
			t.Errorf("%s: eventHubsOAuthScope(%v, %q) = %q, %v; want %q error=%v", c.name, c.brokers, c.explicit, got, err, c.want, c.wantErr)
		}
	}
}

func TestKafkaManagedIdentityOAuthRequiresBoundEventHubsEndpoint(t *testing.T) {
	t.Setenv(identitypolicy.AzureMessagingAllowedNamespacesEnv, "orders.servicebus.windows.net")
	tests := []struct {
		name    string
		cfg     config
		wantErr string
	}{
		{name: "profile", cfg: config{Brokers: []string{"orders.servicebus.windows.net:9093"}}, wantErr: "profile=eventhubs"},
		{name: "TLS verification", cfg: config{Profile: "eventhubs", Brokers: []string{"orders.servicebus.windows.net:9093"}, TLSInsecureSkipVerify: true}, wantErr: "forbids"},
		{name: "namespace", cfg: config{Profile: "eventhubs", Brokers: []string{"evil.example:9093"}}, wantErr: "not allowed"},
		{name: "scope", cfg: config{Profile: "eventhubs", Brokers: []string{"orders.servicebus.windows.net:9093"}, OAuthScope: "https://graph.microsoft.com/.default"}, wantErr: "oauth_scope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&franzClient{cfg: tc.cfg}).oauthTokenSource()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("oauthTokenSource error = %v, want %q", err, tc.wantErr)
			}
		})
	}

	t.Setenv(identitypolicy.AzureMessagingAllowedNamespacesEnv, "")
	_, err := (&franzClient{cfg: config{Profile: "eventhubs", Brokers: []string{"orders.servicebus.windows.net:9093"}}}).oauthTokenSource()
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("empty identity namespace policy error = %v", err)
	}
}
