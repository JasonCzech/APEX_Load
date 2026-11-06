package eventhubs

import (
	"context"
	"strings"
	"testing"
)

// Synthetic, non-functional SAS material. The trailing '=' (base64 padding in a
// real key) checks that a value containing the separator survives intact.
const (
	testSASKey      = "c3ludGhldGlj="
	testNamespaceCS = "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=" + testSASKey
)

func TestNormalizeConnectionStringTrimsLikeJavaSDK(t *testing.T) {
	for _, tc := range []struct{ name, raw, want, entityPath string }{
		{"already clean", testNamespaceCS, testNamespaceCS, ""},
		// The value copied out of a JMeter plan kept a trailing space after the key.
		{"trailing space on key", testNamespaceCS + " ", testNamespaceCS, ""},
		{"trailing newline from a pasted secret", testNamespaceCS + "\r\n", testNamespaceCS, ""},
		{"spaces around keys and values", " Endpoint = sb://example.servicebus.windows.net/ ; SharedAccessKeyName= send ;SharedAccessKey =" + testSASKey + " ;", testNamespaceCS, ""},
		{"empty segments dropped", ";;" + testNamespaceCS + ";;", testNamespaceCS, ""},
		{"hub scoped", testNamespaceCS + ";EntityPath= Orders ", testNamespaceCS + ";EntityPath=Orders", "Orders"},
		{"last entity path wins like azeventhubs", testNamespaceCS + ";EntityPath=a;entitypath=b", testNamespaceCS + ";entitypath=b", "b"},
		{"empty entity path dropped", testNamespaceCS + ";EntityPath= ", testNamespaceCS, ""},
		// The last EntityPath is empty, so the raw string is namespace-level;
		// the earlier "a" must not survive in the normalized string.
		{"trailing empty entity path clears an earlier one", testNamespaceCS + ";EntityPath=a;EntityPath=", testNamespaceCS, ""},
		{"entity path in the middle keeps its place", "Endpoint=sb://example.servicebus.windows.net/;EntityPath=a;SharedAccessKeyName=send;SharedAccessKey=" + testSASKey, "Endpoint=sb://example.servicebus.windows.net/;EntityPath=a;SharedAccessKeyName=send;SharedAccessKey=" + testSASKey, "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, entityPath, err := normalizeConnectionString(tc.raw)
			if err != nil {
				t.Fatalf("normalizeConnectionString: %v", err)
			}
			if got != tc.want || entityPath != tc.entityPath {
				t.Fatalf("normalizeConnectionString = (%q, %q), want (%q, %q)", got, entityPath, tc.want, tc.entityPath)
			}
			// The run parses the normalized string a second time
			// (ResolveConnectionStringEventHub); both parses must agree.
			again, reparsed, err := normalizeConnectionString(got)
			if err != nil || again != got || reparsed != entityPath {
				t.Fatalf("re-normalizing = (%q, %q, %v), want a fixed point (%q, %q)", again, reparsed, err, got, entityPath)
			}
		})
	}
}

func TestEventHubsInitTrailingEmptyEntityPathIsNamespaceLevel(t *testing.T) {
	e := &EventHubs{name: eventHubsProtocolName}
	if err := e.Init(context.Background(), map[string]any{
		"eventhubs_connection_string": testNamespaceCS + ";EntityPath=orders;EntityPath=",
		"eventhubs_event_hub":         "payments",
	}); err != nil {
		t.Fatalf("Init: %v (the last EntityPath is empty, so any hub is reachable)", err)
	}
	if e.cfg.EventHub != "payments" || e.cfg.connectionEntityPath != "" {
		t.Fatalf("EventHub = %q, entity path = %q, want payments and none", e.cfg.EventHub, e.cfg.connectionEntityPath)
	}
	if strings.Contains(e.cfg.ConnectionString, "EntityPath") {
		t.Fatal("normalized connection string still carries an EntityPath")
	}
	if hub, err := connectionStringEventHub(e.cfg.connectionEntityPath, e.cfg.EventHub); err != nil || hub != "payments" {
		t.Fatalf("SDK hub = (%q, %v), want payments with a namespace-level string", hub, err)
	}
}

func TestNormalizeConnectionStringMalformedErrorHidesSecret(t *testing.T) {
	_, _, err := normalizeConnectionString(testNamespaceCS + ";dangling")
	if err == nil {
		t.Fatal("malformed segment accepted")
	}
	if strings.Contains(err.Error(), testSASKey) || strings.Contains(err.Error(), "example.servicebus") {
		t.Fatalf("error leaks connection string material: %v", err)
	}
}

func TestConnectionStringEventHubPassesEmptyHubForMatchingEntityPath(t *testing.T) {
	for _, tc := range []struct{ name, entityPath, hub, want string }{
		{"namespace scoped keeps hub", "", "orders", "orders"},
		{"matching hub", "orders", "orders", ""},
		{"hub names are case-insensitive", "Orders", "orders", ""},
		{"no hub uses entity path", "orders", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := connectionStringEventHub(tc.entityPath, tc.hub)
			if err != nil || got != tc.want {
				t.Fatalf("connectionStringEventHub(%q, %q) = (%q, %v), want %q", tc.entityPath, tc.hub, got, err, tc.want)
			}
		})
	}
	_, err := connectionStringEventHub("orders", "payments")
	if err == nil || !strings.Contains(err.Error(), `"orders"`) || !strings.Contains(err.Error(), `"payments"`) {
		t.Fatalf("mismatch error = %v, want both hub names", err)
	}
}

func TestResolveConnectionStringEventHub(t *testing.T) {
	scoped := testNamespaceCS + ";EntityPath=orders"
	if hub, err := ResolveConnectionStringEventHub(scoped, ""); err != nil || hub != "orders" {
		t.Fatalf("empty hub = (%q, %v), want EntityPath", hub, err)
	}
	if hub, err := ResolveConnectionStringEventHub(scoped, "ORDERS"); err != nil || hub != "ORDERS" {
		t.Fatalf("matching hub = (%q, %v), want configured hub kept", hub, err)
	}
	if hub, err := ResolveConnectionStringEventHub(testNamespaceCS, "payments"); err != nil || hub != "payments" {
		t.Fatalf("namespace scoped = (%q, %v), want configured hub", hub, err)
	}
	_, err := ResolveConnectionStringEventHub(scoped, "payments")
	if err == nil || !strings.Contains(err.Error(), "config.eventhubs.event_hub") || !strings.Contains(err.Error(), `"orders"`) || !strings.Contains(err.Error(), `"payments"`) {
		t.Fatalf("mismatch error = %v, want both hubs named", err)
	}
	if strings.Contains(err.Error(), testSASKey) {
		t.Fatalf("mismatch error leaks the key: %v", err)
	}
}

func TestEventHubsInitReconcilesConnectionStringEntityPath(t *testing.T) {
	ctx := context.Background()
	t.Run("entity path supplies the hub and the key is trimmed", func(t *testing.T) {
		e := &EventHubs{name: eventHubsProtocolName}
		if err := e.Init(ctx, map[string]any{
			"eventhubs_connection_string_secret": testNamespaceCS + " ;EntityPath=orders\n",
		}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if e.cfg.EventHub != "orders" || e.cfg.connectionEntityPath != "orders" {
			t.Fatalf("EventHub = %q, entity path = %q, want orders", e.cfg.EventHub, e.cfg.connectionEntityPath)
		}
		if e.cfg.ConnectionString != testNamespaceCS+";EntityPath=orders" {
			t.Fatal("connection string was not normalized")
		}
	})
	t.Run("matching hub is accepted", func(t *testing.T) {
		e := &EventHubs{name: eventHubsProtocolName}
		if err := e.Init(ctx, map[string]any{
			"eventhubs_connection_string": testNamespaceCS + ";EntityPath=Orders",
			"eventhubs_event_hub":         "orders",
		}); err != nil {
			t.Fatalf("Init: %v", err)
		}
	})
	t.Run("different hub is a configuration error", func(t *testing.T) {
		e := &EventHubs{name: eventHubsProtocolName}
		err := e.Init(ctx, map[string]any{
			"eventhubs_connection_string": testNamespaceCS + ";EntityPath=orders",
			"eventhubs_event_hub":         "payments",
		})
		if err == nil || !strings.Contains(err.Error(), `"orders"`) || !strings.Contains(err.Error(), `"payments"`) || strings.Contains(err.Error(), testSASKey) {
			t.Fatalf("Init error = %v, want sanitized hub mismatch", err)
		}
	})
	t.Run("aad auth ignores the connection string", func(t *testing.T) {
		e := &EventHubs{name: eventHubsProtocolName}
		if err := e.Init(ctx, map[string]any{
			"eventhubs_connection_string": testNamespaceCS + ";EntityPath=orders",
			"eventhubs_event_hub":         "payments",
			"eventhubs_namespace":         "example.servicebus.windows.net",
			"eventhubs_auth_type":         "aad",
		}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if e.cfg.EventHub != "payments" {
			t.Fatalf("EventHub = %q, want payments", e.cfg.EventHub)
		}
	})
}

// A malformed secret whose EntityPath runs into the key segment without a ';'
// leaves the key inside the parsed EntityPath. Mismatch errors must not echo it.
func TestConnectionStringMismatchErrorsHideKeyBearingEntityPath(t *testing.T) {
	malformed := "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;EntityPath=ordersSharedAccessKey=" + testSASKey
	_, err := ResolveConnectionStringEventHub(malformed, "orders")
	if err == nil {
		t.Fatal("mismatch accepted")
	}
	if strings.Contains(err.Error(), testSASKey) || strings.Contains(err.Error(), "SharedAccessKey=") {
		t.Fatalf("ResolveConnectionStringEventHub error leaks the key: %v", err)
	}
	if !strings.Contains(err.Error(), "not a valid event hub name") || !strings.Contains(err.Error(), `"orders"`) {
		t.Fatalf("ResolveConnectionStringEventHub error = %v, want the invalid-EntityPath explanation", err)
	}
	_, entityPath, nerr := normalizeConnectionString(malformed)
	if nerr != nil {
		t.Fatalf("normalizeConnectionString: %v", nerr)
	}
	_, err = connectionStringEventHub(entityPath, "orders")
	if err == nil {
		t.Fatal("connectionStringEventHub mismatch accepted")
	}
	if strings.Contains(err.Error(), testSASKey) || strings.Contains(err.Error(), "SharedAccessKey=") {
		t.Fatalf("connectionStringEventHub error leaks the key: %v", err)
	}
	e := &EventHubs{name: eventHubsProtocolName}
	err = e.Init(context.Background(), map[string]any{
		"eventhubs_connection_string_secret": malformed,
		"eventhubs_event_hub":                "orders",
	})
	if err == nil || strings.Contains(err.Error(), testSASKey) {
		t.Fatalf("Init error = %v, want a mismatch that hides the key", err)
	}
}

// An EntityPath is adopted as the run's hub only when it is a valid event hub
// name. A key-bearing one (a missing ';') or one that breaks Azure's naming
// rules is a configuration error that never echoes the value.
func TestResolveConnectionStringEventHubRejectsInvalidEntityPath(t *testing.T) {
	for _, entityPath := range []string{
		"ordersSharedAccessKey=" + testSASKey,
		"-orders",
		"orders.",
		"orders/consumergroups/$Default",
	} {
		cs := "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;EntityPath=" + entityPath
		for _, hub := range []string{"", strings.ToUpper(entityPath)} {
			got, err := ResolveConnectionStringEventHub(cs, hub)
			if err == nil {
				t.Fatalf("EntityPath %q with hub %q adopted as %q, want an error", entityPath, hub, got)
			}
			if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(entityPath)) || strings.Contains(err.Error(), testSASKey) {
				t.Fatalf("error echoes the EntityPath: %v", err)
			}
			if !strings.Contains(err.Error(), "not a valid event hub name") {
				t.Fatalf("error = %v, want the invalid-EntityPath explanation", err)
			}
		}
	}
	e := &EventHubs{name: eventHubsProtocolName}
	err := e.Init(context.Background(), map[string]any{
		"eventhubs_connection_string_secret": "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;EntityPath=ordersSharedAccessKey=" + testSASKey,
	})
	if err == nil || strings.Contains(err.Error(), testSASKey) || e.cfg.EventHub != "" {
		t.Fatalf("Init = %v with hub %q, want a sanitized invalid-EntityPath error", err, e.cfg.EventHub)
	}
}
