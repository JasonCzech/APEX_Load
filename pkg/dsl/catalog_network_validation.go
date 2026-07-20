package dsl

import (
	"fmt"
	"strings"
	"time"

	networkprotocol "github.com/jasonczech/apexload/pkg/protocol/network"
)

func validateNetworkScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.Network
	if cfg.Subprotocol != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(cfg.Subprotocol)), "dns", "ftp", "smtp", "pop3", "imap", "ldap", "winsock", "tcp", "udp") {
		issues = append(issues, fmt.Sprintf("config.network.subprotocol %q is not supported", cfg.Subprotocol))
	}
	if cfg.Port < 0 {
		issues = append(issues, "config.network.port must be zero or greater")
	}
	if cfg.TimeoutMs < 0 {
		issues = append(issues, "config.network.timeout_ms must be zero or greater")
	} else if cfg.TimeoutMs > int(networkprotocol.MaxConfiguredTimeout/time.Millisecond) {
		issues = append(issues, fmt.Sprintf("config.network.timeout_ms must not exceed %d", networkprotocol.MaxConfiguredTimeout/time.Millisecond))
	}
	for index, recipient := range cfg.To {
		if strings.TrimSpace(recipient) == "" {
			issues = append(issues, fmt.Sprintf("config.network.to[%d] must be non-empty", index))
		}
	}
	return issues
}
