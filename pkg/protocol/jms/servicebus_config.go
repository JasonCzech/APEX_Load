package jms

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	serviceBusDefaultClientShards = 4
	serviceBusMaxClientShards     = 64
)

type serviceBusConn struct {
	ConnectionString       string
	FullyQualifiedNS       string
	AuthMode               string
	SharedAccessKeyName    string
	SharedAccessKeyPresent bool
}

func resolveServiceBusConn(cfg config) (serviceBusConn, error) {
	raw := strings.TrimSpace(cfg.BrokerURL)
	if raw == "" {
		return serviceBusConn{}, fmt.Errorf("jms broker_url is not configured")
	}
	if isServiceBusConnectionString(raw) {
		conn := serviceBusConn{
			ConnectionString: raw,
			AuthMode:         "connection_string",
		}
		for _, part := range strings.Split(raw, ";") {
			key, value, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "sharedaccesskeyname":
				conn.SharedAccessKeyName = strings.TrimSpace(value)
			case "sharedaccesskey":
				conn.SharedAccessKeyPresent = strings.TrimSpace(value) != ""
			}
		}
		return conn, nil
	}

	namespace, err := normalizeServiceBusNamespace(raw)
	if err != nil {
		return serviceBusConn{}, err
	}
	username := strings.TrimSpace(cfg.Username)
	password := strings.TrimSpace(cfg.Password)
	if username != "" || password != "" {
		if username == "" || password == "" {
			return serviceBusConn{}, fmt.Errorf("Service Bus shared access auth requires both config.jms.username and config.jms.password")
		}
		return serviceBusConn{
			ConnectionString:       fmt.Sprintf("Endpoint=sb://%s/;SharedAccessKeyName=%s;SharedAccessKey=%s", namespace, username, password),
			FullyQualifiedNS:       namespace,
			AuthMode:               "shared_access_key",
			SharedAccessKeyName:    username,
			SharedAccessKeyPresent: true,
		}, nil
	}
	return serviceBusConn{
		FullyQualifiedNS: namespace,
		AuthMode:         "default_azure_credential",
	}, nil
}

func isServiceBusConnectionString(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "endpoint=") || strings.Contains(lower, "sharedaccesskey") || strings.Contains(lower, "sharedaccesssignature")
}

func normalizeServiceBusNamespace(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimSuffix(trimmed, "/")
	if strings.HasPrefix(strings.ToLower(trimmed), "sb://") {
		trimmed = trimmed[len("sb://"):]
		trimmed = strings.TrimSuffix(trimmed, "/")
	}
	if strings.Contains(trimmed, "://") {
		return "", fmt.Errorf("Service Bus broker_url must be a connection string, namespace, or sb:// namespace, got %q", raw)
	}
	if strings.Contains(trimmed, ";") || strings.Contains(trimmed, "/") {
		return "", fmt.Errorf("Service Bus namespace broker_url must not include entity paths or connection-string fragments")
	}
	if trimmed == "" {
		return "", fmt.Errorf("Service Bus namespace is empty")
	}
	return trimmed, nil
}

func splitServiceBusSubscription(destination string) (topic string, subscription string, ok bool) {
	trimmed := strings.Trim(strings.TrimSpace(destination), "/")
	if trimmed == "" {
		return "", "", false
	}
	lower := strings.ToLower(trimmed)
	const marker = "/subscriptions/"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return "", "", false
	}
	topic = strings.Trim(trimmed[:idx], "/")
	subscription = strings.Trim(trimmed[idx+len(marker):], "/")
	if topic == "" || subscription == "" || strings.Contains(subscription, "/") {
		return "", "", false
	}
	return topic, subscription, true
}

func serviceBusConsumeDestination(destination string, destinationType string, subscription string) string {
	trimmed := strings.TrimSpace(destination)
	if trimmed == "" {
		return trimmed
	}
	if _, _, ok := splitServiceBusSubscription(trimmed); ok {
		return strings.Trim(trimmed, "/")
	}
	if !strings.EqualFold(strings.TrimSpace(destinationType), "topic") {
		return trimmed
	}
	sub := strings.Trim(strings.TrimSpace(subscription), "/")
	if sub == "" {
		return trimmed
	}
	topic := strings.Trim(trimmed, "/")
	return topic + "/subscriptions/" + sub
}

func serviceBusReplyToDestination(destination string) string {
	if topic, _, ok := splitServiceBusSubscription(destination); ok {
		return topic
	}
	return strings.TrimSpace(destination)
}

func serviceBusReceiveMode(_ string, props map[string]string) string {
	mode := strings.ToLower(strings.TrimSpace(props["servicebus_receive_mode"]))
	mode = strings.ReplaceAll(mode, "-", "_")
	switch mode {
	case "receive_and_delete", "receiveanddelete":
		return "receive_and_delete"
	default:
		return "peek_lock"
	}
}

func serviceBusShardCount(props map[string]string) int {
	raw := strings.TrimSpace(props["servicebus_max_clients"])
	if raw == "" {
		return serviceBusDefaultClientShards
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return serviceBusDefaultClientShards
	}
	if value > serviceBusMaxClientShards {
		return serviceBusMaxClientShards
	}
	return value
}

func serviceBusPoolKey(conn serviceBusConn, shard int) string {
	if shard < 0 {
		shard = 0
	}
	material := conn.ConnectionString
	if material == "" {
		material = conn.FullyQualifiedNS
	}
	sum := sha256.Sum256([]byte(conn.AuthMode + "\x00" + material))
	return fmt.Sprintf("servicebus:%s:%s:%d", conn.AuthMode, hex.EncodeToString(sum[:8]), shard)
}
