package network

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func (n *Network) runLDAP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	address := n.ldapURL(action)
	start := time.Now()
	timeout := n.actionTimeout(action)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if timeout <= 0 || remaining < timeout {
			timeout = remaining
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := ldap.DialURL(address, ldap.DialWithDialer(dialer), ldap.DialWithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if timeout > 0 {
		conn.SetTimeout(timeout)
	}
	operationDone := make(chan struct{})
	defer close(operationDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-operationDone:
		}
	}()
	connectTime := time.Since(start)

	username := protocol.FirstNonEmpty(actionParamString(action.Params, "username"), n.username)
	password := protocol.FirstNonEmpty(actionParamString(action.Params, "password"), n.password)
	if username != "" {
		if err := conn.Bind(username, password); err != nil {
			return nil, err
		}
	}
	if strings.EqualFold(action.Type, "ldap_bind") {
		payload := encodePayload(map[string]any{"protocol": "ldap", "target": address, "bound": username != ""})
		return &operationResult{
			payload:       payload,
			target:        address,
			dependency:    "network_ldap",
			bytesReceived: int64(len(payload)),
			connectTime:   connectTime,
			messagesSent:  boolCount(username != ""),
			messagesRecv:  1,
		}, nil
	}

	baseDN := protocol.FirstNonEmpty(action.Target, actionParamString(action.Params, "base_dn"), n.baseDN)
	if baseDN == "" {
		return nil, errors.New("ldap search base DN is required")
	}
	filter := protocol.FirstNonEmpty(actionParamString(action.Params, "filter"), "(objectClass=*)")
	attrs := actionParamStringSlice(action.Params, "attributes")
	maxEntries := actionParamInt(action.Params, "max_entries")
	if maxEntries <= 0 {
		maxEntries = 100
	}
	searchTimeout := int(timeout.Seconds())
	if timeout > 0 && searchTimeout == 0 {
		searchTimeout = 1
	}
	searchRequest := ldap.NewSearchRequest(baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, maxEntries, searchTimeout, false, filter, attrs, nil)
	searchResult, err := conn.Search(searchRequest)
	if err != nil {
		return nil, err
	}
	entries := make([]map[string]any, 0, len(searchResult.Entries))
	for _, entry := range searchResult.Entries {
		attributes := make(map[string][]string, len(entry.Attributes))
		for _, attr := range entry.Attributes {
			attributes[attr.Name] = attr.Values
		}
		entries = append(entries, map[string]any{"dn": entry.DN, "attributes": attributes})
	}
	payload := encodePayload(map[string]any{"protocol": "ldap", "target": address, "base_dn": baseDN, "filter": filter, "entry_count": len(entries), "entries": entries})
	return &operationResult{
		payload:       payload,
		target:        address,
		dependency:    "network_ldap",
		bytesSent:     int64(len(filter)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  1,
		messagesRecv:  len(entries),
	}, nil
}
