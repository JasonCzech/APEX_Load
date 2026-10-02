package identitypolicy

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

const EventHubsCheckpointContainersEnv = "APEXLOAD_EVENTHUBS_CHECKPOINT_CONTAINERS"

// ValidateEventHubsCheckpointContainer binds ambient Azure credentials to an
// exact operator-owned container URL before any credential is constructed.
func ValidateEventHubsCheckpointContainer(value string) (string, error) {
	canonical, err := normalizeCheckpointContainer(value)
	if err != nil {
		return "", err
	}
	entries := strings.Split(os.Getenv(EventHubsCheckpointContainersEnv), ",")
	if len(entries) > 256 {
		return "", fmt.Errorf("checkpoint container allowlist exceeds limit")
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		allowed, err := normalizeCheckpointContainer(strings.TrimSpace(entry))
		if err != nil {
			return "", fmt.Errorf("invalid checkpoint container allowlist entry")
		}
		if allowed == canonical {
			return canonical, nil
		}
	}
	return "", fmt.Errorf("Event Hubs checkpoint container is not approved by %s", EventHubsCheckpointContainersEnv)
}
func normalizeCheckpointContainer(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || len(value) > 1024 || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" || u.RawPath != "" {
		return "", fmt.Errorf("checkpoint storage requires an HTTPS container URL without credentials, port or query")
	}
	host, err := normalizeAzureMessagingHost(u.Host)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if len(name) < 3 || len(name) > 63 || strings.Contains(name, "--") || name[0] == '-' || name[len(name)-1] == '-' {
		return "", fmt.Errorf("checkpoint container name is invalid")
	}
	for _, char := range name {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return "", fmt.Errorf("checkpoint container name is invalid")
		}
	}
	return "https://" + host + "/" + name, nil
}
