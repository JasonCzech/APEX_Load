package jms

import "testing"

func TestResolveServiceBusConn(t *testing.T) {
	tests := []struct {
		name           string
		cfg            config
		wantAuth       string
		wantNamespace  string
		wantConnString bool
		wantErr        bool
	}{
		{
			name:           "connection string",
			cfg:            config{BrokerURL: "Endpoint=sb://orders.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=secret"},
			wantAuth:       "connection_string",
			wantConnString: true,
		},
		{
			name:           "namespace with shared access key fields",
			cfg:            config{BrokerURL: "orders.servicebus.windows.net", Username: "send", Password: "secret"},
			wantAuth:       "shared_access_key",
			wantNamespace:  "orders.servicebus.windows.net",
			wantConnString: true,
		},
		{
			name:          "sb namespace with default credential",
			cfg:           config{BrokerURL: "sb://orders.servicebus.windows.net/"},
			wantAuth:      "default_azure_credential",
			wantNamespace: "orders.servicebus.windows.net",
		},
		{
			name:    "missing broker",
			cfg:     config{},
			wantErr: true,
		},
		{
			name:    "partial shared access key",
			cfg:     config{BrokerURL: "orders.servicebus.windows.net", Username: "send"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveServiceBusConn(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveServiceBusConn returned nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveServiceBusConn returned error: %v", err)
			}
			if got.AuthMode != tt.wantAuth {
				t.Fatalf("AuthMode = %q, want %q", got.AuthMode, tt.wantAuth)
			}
			if got.FullyQualifiedNS != tt.wantNamespace {
				t.Fatalf("FullyQualifiedNS = %q, want %q", got.FullyQualifiedNS, tt.wantNamespace)
			}
			if (got.ConnectionString != "") != tt.wantConnString {
				t.Fatalf("ConnectionString set = %v, want %v", got.ConnectionString != "", tt.wantConnString)
			}
		})
	}
}

func TestSplitServiceBusSubscription(t *testing.T) {
	tests := []struct {
		destination string
		wantTopic   string
		wantSub     string
		wantOK      bool
	}{
		{destination: "orders/subscriptions/perf", wantTopic: "orders", wantSub: "perf", wantOK: true},
		{destination: "/orders/subscriptions/perf/", wantTopic: "orders", wantSub: "perf", wantOK: true},
		{destination: "orders/SUBSCRIPTIONS/perf", wantTopic: "orders", wantSub: "perf", wantOK: true},
		{destination: "orders", wantOK: false},
		{destination: "orders/subscriptions/", wantOK: false},
		{destination: "orders/subscriptions/perf/extra", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.destination, func(t *testing.T) {
			gotTopic, gotSub, gotOK := splitServiceBusSubscription(tt.destination)
			if gotOK != tt.wantOK || gotTopic != tt.wantTopic || gotSub != tt.wantSub {
				t.Fatalf("splitServiceBusSubscription(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.destination, gotTopic, gotSub, gotOK, tt.wantTopic, tt.wantSub, tt.wantOK)
			}
		})
	}
}

func TestServiceBusConsumeDestination(t *testing.T) {
	tests := []struct {
		name            string
		destination     string
		destinationType string
		subscription    string
		want            string
	}{
		{name: "queue unchanged", destination: "orders", destinationType: "queue", subscription: "perf", want: "orders"},
		{name: "topic subscription composed", destination: "orders", destinationType: "topic", subscription: "perf", want: "orders/subscriptions/perf"},
		{name: "topic without subscription unchanged", destination: "orders", destinationType: "topic", want: "orders"},
		{name: "already composed unchanged", destination: "orders/subscriptions/perf", destinationType: "topic", subscription: "other", want: "orders/subscriptions/perf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serviceBusConsumeDestination(tt.destination, tt.destinationType, tt.subscription)
			if got != tt.want {
				t.Fatalf("serviceBusConsumeDestination() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceBusMockComposedDestinationSubscriptionIsAuthoritative(t *testing.T) {
	p := &JMS{provider: "servicebus", cfg: config{Mock: true}}
	topic, subscription := p.mockTopicCoordinates("orders/subscriptions/perf", "other")
	if topic != "orders" || subscription != "perf" {
		t.Fatalf("mock topic coordinates = (%q, %q), want (orders, perf)", topic, subscription)
	}
}

func TestServiceBusReplyToDestination(t *testing.T) {
	if got := serviceBusReplyToDestination("orders.reply/subscriptions/perf"); got != "orders.reply" {
		t.Fatalf("serviceBusReplyToDestination(subscription) = %q, want orders.reply", got)
	}
	if got := serviceBusReplyToDestination("orders.reply"); got != "orders.reply" {
		t.Fatalf("serviceBusReplyToDestination(queue) = %q, want orders.reply", got)
	}
}

func TestServiceBusReceiveMode(t *testing.T) {
	tests := []struct {
		name  string
		ack   string
		props map[string]string
		want  string
	}{
		{name: "default auto is peek lock", ack: "auto", want: "peek_lock"},
		{name: "client still settles immediately", ack: "client", want: "peek_lock"},
		{name: "transacted still settles immediately", ack: "transacted", want: "peek_lock"},
		{name: "receive and delete override", ack: "client", props: map[string]string{"servicebus_receive_mode": "receive_and_delete"}, want: "receive_and_delete"},
		{name: "hyphenated receive and delete override", props: map[string]string{"servicebus_receive_mode": "receive-and-delete"}, want: "receive_and_delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serviceBusReceiveMode(tt.ack, tt.props); got != tt.want {
				t.Fatalf("serviceBusReceiveMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceBusPoolKeyAndShardCount(t *testing.T) {
	conn := serviceBusConn{ConnectionString: "Endpoint=sb://orders.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=secret", AuthMode: "connection_string"}
	if got := serviceBusShardCount(nil); got != serviceBusDefaultClientShards {
		t.Fatalf("serviceBusShardCount(nil) = %d, want default", got)
	}
	if got := serviceBusShardCount(map[string]string{"servicebus_max_clients": "2"}); got != 2 {
		t.Fatalf("serviceBusShardCount(2) = %d, want 2", got)
	}
	if got := serviceBusShardCount(map[string]string{"servicebus_max_clients": "999"}); got != serviceBusMaxClientShards {
		t.Fatalf("serviceBusShardCount(999) = %d, want max", got)
	}
	key0 := serviceBusPoolKey(conn, 0)
	key1 := serviceBusPoolKey(conn, 1)
	if key0 == key1 {
		t.Fatalf("serviceBusPoolKey shard keys are equal: %q", key0)
	}
	if key0 == "" || key1 == "" {
		t.Fatal("serviceBusPoolKey returned empty key")
	}
}
