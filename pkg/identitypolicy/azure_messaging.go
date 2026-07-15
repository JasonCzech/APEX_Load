// Package identitypolicy binds process credentials to operator-approved remote
// identities before protocol adapters may obtain or transmit bearer tokens.
package identitypolicy

import (
	"fmt"
	"net"
	"os"
	"strings"
)

const AzureMessagingAllowedNamespacesEnv = "APEXLOAD_AZURE_MESSAGING_ALLOWED_NAMESPACES"

const AzureEventHubsOAuthScope = "https://eventhubs.azure.net/.default"

const maxAzureMessagingNamespaces = 256

// ValidateAzureMessagingNamespace requires an exact hostname match in the
// operator-owned allowlist. It deliberately fails closed when the allowlist is
// empty: a project-authored script must never be able to send the generator's
// managed-identity token to an arbitrary TLS endpoint.
func ValidateAzureMessagingNamespace(value string) (string, error) {
	host, err := normalizeAzureMessagingHost(value)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(os.Getenv(AzureMessagingAllowedNamespacesEnv))
	if raw == "" {
		return "", fmt.Errorf("Azure messaging workload-identity auth is disabled until %s is configured", AzureMessagingAllowedNamespacesEnv)
	}
	entries := strings.Split(raw, ",")
	if len(entries) > maxAzureMessagingNamespaces {
		return "", fmt.Errorf("%s exceeds %d entries", AzureMessagingAllowedNamespacesEnv, maxAzureMessagingNamespaces)
	}
	for _, entry := range entries {
		allowed, normalizeErr := normalizeAzureMessagingHost(entry)
		if normalizeErr != nil {
			return "", fmt.Errorf("invalid %s entry: %w", AzureMessagingAllowedNamespacesEnv, normalizeErr)
		}
		if host == allowed {
			return host, nil
		}
	}
	return "", fmt.Errorf("Azure messaging namespace %q is not allowed by %s", host, AzureMessagingAllowedNamespacesEnv)
}

func normalizeAzureMessagingHost(value string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(value))
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil || strings.ContainsAny(host, "/\\@;[]:") {
		return "", fmt.Errorf("Azure messaging namespace must be a DNS hostname")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("Azure messaging namespace must be fully qualified")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("Azure messaging namespace contains an invalid DNS label")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("Azure messaging namespace contains an invalid DNS label")
			}
		}
	}
	return host, nil
}
