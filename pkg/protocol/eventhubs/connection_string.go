package eventhubs

import (
	"errors"
	"fmt"
	"strings"
)

// errMalformedConnectionString never carries the connection string or any
// segment of it: the string embeds the SAS key.
var errMalformedConnectionString = errors.New("config.eventhubs.connection_string is malformed: every ';'-separated segment must be key=value")

// normalizeConnectionString trims whitespace around every key and value and
// reports the EntityPath, if any. The Java SDK (azure-core-amqp
// ConnectionStringProperties), which JMeter's Event Hubs sampler uses, trims
// both; azeventhubs does not, so a key pasted with a trailing space or newline
// would sign every request with the wrong key. An empty EntityPath is dropped
// because azeventhubs would treat it as present and then reject the hub name.
//
// At most one EntityPath survives, the last one (azeventhubs keeps the last it
// sees). Keeping an earlier one would let the normalized string disagree with
// the reported entityPath: for "EntityPath=a;EntityPath=" the raw string is
// namespace-level, so the normalized string must not stay scoped to "a".
func normalizeConnectionString(raw string) (normalized, entityPath string, err error) {
	segments := strings.Split(raw, ";")
	kept := make([]string, 0, len(segments))
	entityPathAt := -1
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		key, value, ok := strings.Cut(segment, "=")
		if !ok {
			return "", "", errMalformedConnectionString
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if strings.EqualFold(key, "EntityPath") {
			entityPath = value
			if entityPathAt >= 0 {
				kept = append(kept[:entityPathAt], kept[entityPathAt+1:]...)
				entityPathAt = -1
			}
			if value == "" {
				continue
			}
			entityPathAt = len(kept)
		}
		kept = append(kept, key+"="+value)
	}
	return strings.Join(kept, ";"), entityPath, nil
}

// connectionStringEventHub returns the hub argument for azeventhubs'
// *FromConnectionString constructors. The SDK rejects a non-empty hub whenever
// the connection string carries an EntityPath, even a matching one, so a
// hub-scoped string (what the Azure portal copies for a hub-level SAS policy)
// must be paired with "". Hub names are case-insensitive in Azure.
func connectionStringEventHub(entityPath, hub string) (string, error) {
	if entityPath == "" {
		return hub, nil
	}
	if hub == "" || strings.EqualFold(strings.TrimSpace(hub), entityPath) {
		return "", nil
	}
	if !IsEventHubName(entityPath) {
		return "", fmt.Errorf("event hub %q does not match %s; a hub-scoped connection string only reaches its own hub, so use a namespace-level connection string", hub, invalidEntityPathMessage)
	}
	return "", fmt.Errorf("event hub %q does not match the EntityPath %q in config.eventhubs.connection_string; a hub-scoped connection string only reaches its own hub, so use a namespace-level connection string or target %q", hub, entityPath, entityPath)
}

// invalidEntityPathMessage stands in for an EntityPath that is not a valid
// event hub name. Such a value is never echoed: in a malformed string whose
// EntityPath runs into the next segment without a ';' (for example
// "EntityPath=ordersSharedAccessKey=..."), the value carries the SAS key.
const invalidEntityPathMessage = "the EntityPath in config.eventhubs.connection_string, which is not a valid event hub name"

// IsEventHubName reports whether s is a legal Event Hub name under Azure's
// naming rules: 1-256 letters, digits, '.', '-' or '_', starting and ending
// with a letter or digit. Only such a value is adopted as a hub or named in an
// error; pkg/dsl applies the same rule before echoing an EntityPath.
func IsEventHubName(s string) bool {
	if s == "" || len(s) > MaxEventHubNameBytes {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '.' || r == '-' || r == '_') && i > 0 && i < len(s)-1:
		default:
			return false
		}
	}
	return true
}

// ResolveConnectionStringEventHub applies the EntityPath rule to a configured
// event hub: an empty hub takes the EntityPath, and a different hub is a
// configuration error naming both. An EntityPath that is not a valid event hub
// name is an error whatever the hub, and is never named. It returns the hub
// the run should target. Errors never include the connection string.
func ResolveConnectionStringEventHub(connectionString, eventHub string) (string, error) {
	_, entityPath, err := normalizeConnectionString(connectionString)
	if err != nil {
		return "", err
	}
	if entityPath == "" {
		return eventHub, nil
	}
	matches := strings.EqualFold(strings.TrimSpace(eventHub), entityPath)
	if !IsEventHubName(entityPath) {
		// A hub equal to the EntityPath is just as invalid, so it is not
		// named either.
		if strings.TrimSpace(eventHub) == "" || matches {
			return "", errors.New("config.eventhubs.event_hub cannot come from " + invalidEntityPathMessage + "; fix the connection string or use a namespace-level one")
		}
		return "", fmt.Errorf("config.eventhubs.event_hub %q does not match %s; fix the connection string or use a namespace-level one", eventHub, invalidEntityPathMessage)
	}
	if strings.TrimSpace(eventHub) == "" {
		return entityPath, nil
	}
	if !matches {
		return "", fmt.Errorf("config.eventhubs.event_hub %q does not match the EntityPath %q in config.eventhubs.connection_string; set event_hub to %q or use a namespace-level connection string", eventHub, entityPath, entityPath)
	}
	return eventHub, nil
}
