package identitypolicy

import (
	"strings"
	"testing"
)

func TestValidateAzureMessagingNamespaceFailsClosedAndMatchesExactly(t *testing.T) {
	t.Setenv(AzureMessagingAllowedNamespacesEnv, "")
	if _, err := ValidateAzureMessagingNamespace("orders.servicebus.windows.net"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("empty allowlist error = %v", err)
	}

	t.Setenv(AzureMessagingAllowedNamespacesEnv, "orders.servicebus.windows.net,audit.servicebus.windows.net")
	host, err := ValidateAzureMessagingNamespace("ORDERS.servicebus.windows.net:9093")
	if err != nil || host != "orders.servicebus.windows.net" {
		t.Fatalf("allowed namespace = %q, %v", host, err)
	}
	for _, denied := range []string{
		"evil.example",
		"orders.servicebus.windows.net.evil.example",
		"127.0.0.1",
		"orders.servicebus.windows.net@evil.example",
	} {
		if _, err := ValidateAzureMessagingNamespace(denied); err == nil {
			t.Fatalf("namespace %q was allowed", denied)
		}
	}
}

func TestValidateAzureMessagingNamespaceRejectsWildcardPolicy(t *testing.T) {
	t.Setenv(AzureMessagingAllowedNamespacesEnv, "*.servicebus.windows.net")
	if _, err := ValidateAzureMessagingNamespace("orders.servicebus.windows.net"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("wildcard policy error = %v", err)
	}
}
