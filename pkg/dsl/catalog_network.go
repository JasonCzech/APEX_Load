package dsl

import (
	"fmt"
	"strings"
)

// networkActionSpecs declares the network protocol action types (DNS, raw
// sockets, FTP, SMTP, POP3, IMAP, LDAP). The network validator lowercases
// the incoming type before catalog lookup, so aliases are registered in
// lowercase. The shared timeout_ms / read_bytes / max_entries rules apply to
// every network action and stay in validateNetworkActionIssues.
func networkActionSpecs() []ActionSpec {
	mailboxFields := []FieldSpec{
		{Name: "target", Kind: FieldKindString, Doc: "Command or recipient; one of target, value, or body is required."},
		{Name: "value", Kind: FieldKindString, Doc: "Command argument alternative to target."},
		{Name: "body", Kind: FieldKindString, Doc: "Message body alternative to target."},
	}
	return []ActionSpec{
		{
			Type:      "network_health",
			Protocols: []string{"network"},
			Summary:   "Verifies the configured network endpoint is reachable.",
		},
		{
			Type:      "dns_query",
			Aliases:   []string{"dns_lookup"},
			Protocols: []string{"network"},
			Summary:   "Resolves a DNS record.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Hostname to resolve."},
				{Name: "params.record_type", Kind: FieldKindEnum, Enum: []string{"A", "AAAA", "CNAME", "MX", "NS", "TXT"}, Doc: "DNS record type; defaults to A."},
			},
			Validate: func(index int, action Action) []string {
				label := fmt.Sprintf("actions[%d] %q", index, action.Name)
				var issues []string
				if strings.TrimSpace(action.Target) == "" {
					issues = append(issues, fmt.Sprintf("%s: dns action requires target host", label))
				}
				if recordType, ok := actionParamStringValue(action.Params, "record_type"); ok && !isAllowedValue(strings.ToUpper(recordType), "A", "AAAA", "CNAME", "MX", "NS", "TXT") {
					issues = append(issues, fmt.Sprintf("%s: dns record_type must be A, AAAA, CNAME, MX, NS, or TXT", label))
				}
				return issues
			},
		},
		{
			Type:      "tcp_request",
			Aliases:   []string{"tcp_send", "winsock_tcp"},
			Protocols: []string{"network"},
			Summary:   "Sends bytes over a TCP connection and optionally reads a response.",
			Fields:    socketFields(),
			Validate:  validateNetworkSocketAction,
		},
		{
			Type:      "udp_request",
			Aliases:   []string{"udp_send", "winsock_udp"},
			Protocols: []string{"network"},
			Summary:   "Sends a UDP datagram and optionally reads a response.",
			Fields:    socketFields(),
			Validate:  validateNetworkSocketAction,
		},
		{
			Type:         "ftp_command",
			Protocols:    []string{"network"},
			Summary:      "Issues an FTP command on the configured connection.",
			RequireOneOf: [][]string{{"target", "value", "body"}},
			Fields:       mailboxFields,
			Validate:     networkCommandValidator("ftp_command"),
		},
		{
			Type:         "ftp_login",
			Protocols:    []string{"network"},
			Summary:      "Logs in to the configured FTP server.",
			RequireOneOf: [][]string{{"target", "value", "body"}},
			Fields:       mailboxFields,
			Validate:     networkCommandValidator("ftp_login"),
		},
		{
			Type:         "ftp_noop",
			Protocols:    []string{"network"},
			Summary:      "Sends an FTP NOOP keep-alive.",
			RequireOneOf: [][]string{{"target", "value", "body"}},
			Fields:       mailboxFields,
			Validate:     networkCommandValidator("ftp_noop"),
		},
		{
			Type:         "smtp_send",
			Protocols:    []string{"network"},
			Summary:      "Sends an email through the configured SMTP server.",
			RequireOneOf: [][]string{{"target", "value", "body"}},
			Fields:       mailboxFields,
			Validate:     networkCommandValidator("smtp_send"),
		},
		{
			Type:         "pop3_command",
			Protocols:    []string{"network"},
			Summary:      "Issues a POP3 command on the configured connection.",
			RequireOneOf: [][]string{{"target", "value", "body"}},
			Fields:       mailboxFields,
			Validate:     networkCommandValidator("pop3_command"),
		},
		{
			Type:         "imap_command",
			Protocols:    []string{"network"},
			Summary:      "Issues an IMAP command on the configured connection.",
			RequireOneOf: [][]string{{"target", "value", "body"}},
			Fields:       mailboxFields,
			Validate:     networkCommandValidator("imap_command"),
		},
		{
			Type:      "ldap_bind",
			Protocols: []string{"network"},
			Summary:   "Binds to the configured LDAP server.",
		},
		{
			Type:      "ldap_search",
			Protocols: []string{"network"},
			Summary:   "Runs an LDAP search.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Search base DN."},
				{Name: "value", Kind: FieldKindString, Doc: "Search filter."},
				{Name: "params.max_entries", Kind: FieldKindInt, Doc: "Result cap; zero or greater."},
			},
			Validate: func(index int, action Action) []string {
				if strings.TrimSpace(action.Target) == "" {
					return []string{fmt.Sprintf("actions[%d] %q: ldap_search requires target base DN", index, action.Name)}
				}
				return nil
			},
		},
	}
}

func socketFields() []FieldSpec {
	return []FieldSpec{
		{Name: "target", Required: true, Kind: FieldKindString, Doc: "Destination address host:port; defaults to the configured endpoint when set."},
		{Name: "body", Kind: FieldKindString, Doc: "Bytes to send; template variables are expanded."},
		{Name: "params.read_bytes", Kind: FieldKindInt, Doc: "Bytes to read back; 0 through 1048576."},
		{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Socket timeout in milliseconds; zero or greater."},
	}
}

func validateNetworkSocketAction(index int, action Action) []string {
	if strings.TrimSpace(action.Target) == "" {
		return []string{fmt.Sprintf("actions[%d] %q: socket action requires target address", index, action.Name)}
	}
	if readBytes, ok := actionParamIntValue(action.Params, "read_bytes"); ok && readBytes > 1<<20 {
		return []string{fmt.Sprintf("actions[%d] %q: read_bytes must not exceed 1048576", index, action.Name)}
	}
	return nil
}

// networkCommandValidator builds the shared target/value/body rule for FTP,
// SMTP, POP3, and IMAP commands. The message names the canonical action
// type, matching the lowercased type the original validator interpolated.
func networkCommandValidator(canonicalType string) func(int, Action) []string {
	return func(index int, action Action) []string {
		if strings.TrimSpace(action.Target) == "" && strings.TrimSpace(action.Value) == "" && strings.TrimSpace(action.Body) == "" {
			return []string{fmt.Sprintf("actions[%d] %q: %s requires target, value, or body", index, action.Name, canonicalType)}
		}
		return nil
	}
}
