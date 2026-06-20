//go:build kafka_real

package kafka

import "testing"

// TestEventHubsOAuthScope verifies the OAUTHBEARER token scope resolution (EH.2):
// an explicit oauth_scope wins, otherwise it is derived from the first broker
// host (the Event Hubs / Entra ID convention).
func TestEventHubsOAuthScope(t *testing.T) {
	cases := []struct {
		name     string
		brokers  []string
		explicit string
		want     string
	}{
		{"explicit scope wins", []string{"ns.servicebus.windows.net:9093"}, "https://custom.example/.default", "https://custom.example/.default"},
		{"derive from event hubs host", []string{"ns.servicebus.windows.net:9093"}, "", "https://ns.servicebus.windows.net/.default"},
		{"host without port", []string{"broker.example.com"}, "", "https://broker.example.com/.default"},
		{"no brokers, no explicit", nil, "", ""},
	}
	for _, c := range cases {
		if got := eventHubsOAuthScope(c.brokers, c.explicit); got != c.want {
			t.Errorf("%s: eventHubsOAuthScope(%v, %q) = %q, want %q", c.name, c.brokers, c.explicit, got, c.want)
		}
	}
}
