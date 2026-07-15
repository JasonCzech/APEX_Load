package network

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	hardLDAPMaxEntries         = 1_000
	hardLDAPAttributesPerEntry = 128
	hardLDAPValuesPerAttribute = 256
	hardLDAPValueBytes         = 64 << 10
	hardLDAPNameBytes          = 1 << 10
	hardLDAPRetainedBytes      = 1 << 20
	hardLDAPWireResponseBytes  = 4 << 20
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
	conn, err := dialBoundedLDAP(ctx, address, dialer)
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
	if maxEntries > hardLDAPMaxEntries {
		maxEntries = hardLDAPMaxEntries
	}
	searchTimeout := int(timeout.Seconds())
	if timeout > 0 && searchTimeout == 0 {
		searchTimeout = 1
	}
	searchRequest := ldap.NewSearchRequest(baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, maxEntries, searchTimeout, false, filter, attrs, nil)
	// Enforce locally as well as asking the server. A non-compliant directory
	// must not make the client parse/retain unlimited entries.
	searchRequest.EnforceSizeLimit = true
	searchResult, err := conn.Search(searchRequest)
	if err != nil {
		return nil, err
	}
	entries, err := boundedLDAPEntries(searchResult.Entries)
	if err != nil {
		return nil, err
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

type boundedLDAPNetConn struct {
	net.Conn
	remaining int64
}

func (conn *boundedLDAPNetConn) Read(buffer []byte) (int, error) {
	if conn.remaining <= 0 {
		return 0, fmt.Errorf("LDAP response exceeds %d bytes", hardLDAPWireResponseBytes)
	}
	readSize := len(buffer)
	if int64(readSize) > conn.remaining+1 {
		readSize = int(conn.remaining + 1)
	}
	n, err := conn.Conn.Read(buffer[:readSize])
	if int64(n) > conn.remaining {
		conn.remaining = 0
		return 0, fmt.Errorf("LDAP response exceeds %d bytes", hardLDAPWireResponseBytes)
	}
	conn.remaining -= int64(n)
	return n, err
}

func dialBoundedLDAP(ctx context.Context, address string, dialer *net.Dialer) (*ldap.Conn, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	host := parsed.Host
	if _, _, splitErr := net.SplitHostPort(host); splitErr != nil {
		port := "389"
		if strings.EqualFold(parsed.Scheme, "ldaps") {
			port = "636"
		}
		host = net.JoinHostPort(parsed.Hostname(), port)
	}
	raw, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	isTLS := strings.EqualFold(parsed.Scheme, "ldaps")
	var transport net.Conn = raw
	if isTLS {
		tlsConn := tls.Client(raw, &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		transport = tlsConn
	} else if !strings.EqualFold(parsed.Scheme, "ldap") {
		_ = raw.Close()
		return nil, fmt.Errorf("unsupported LDAP URL scheme %q", parsed.Scheme)
	}
	conn := ldap.NewConn(&boundedLDAPNetConn{Conn: transport, remaining: hardLDAPWireResponseBytes}, isTLS)
	conn.Start()
	return conn, nil
}

func boundedLDAPEntries(source []*ldap.Entry) ([]map[string]any, error) {
	if len(source) > hardLDAPMaxEntries {
		return nil, fmt.Errorf("LDAP result exceeds %d entries", hardLDAPMaxEntries)
	}
	retained := 0
	retain := func(size int) error {
		if size < 0 || size > hardLDAPRetainedBytes-retained {
			return fmt.Errorf("LDAP result exceeds %d retained bytes", hardLDAPRetainedBytes)
		}
		retained += size
		return nil
	}
	entries := make([]map[string]any, 0, len(source))
	for _, entry := range source {
		if entry == nil {
			continue
		}
		if len(entry.DN) > hardLDAPValueBytes {
			return nil, fmt.Errorf("LDAP entry DN exceeds %d bytes", hardLDAPValueBytes)
		}
		if len(entry.Attributes) > hardLDAPAttributesPerEntry {
			return nil, fmt.Errorf("LDAP entry exceeds %d attributes", hardLDAPAttributesPerEntry)
		}
		if err := retain(len(entry.DN)); err != nil {
			return nil, err
		}
		attributes := make(map[string][]string, len(entry.Attributes))
		for _, attr := range entry.Attributes {
			if attr == nil {
				continue
			}
			if len(attr.Name) > hardLDAPNameBytes {
				return nil, fmt.Errorf("LDAP attribute name exceeds %d bytes", hardLDAPNameBytes)
			}
			if len(attr.Values) > hardLDAPValuesPerAttribute {
				return nil, fmt.Errorf("LDAP attribute %q exceeds %d values", attr.Name, hardLDAPValuesPerAttribute)
			}
			if err := retain(len(attr.Name)); err != nil {
				return nil, err
			}
			values := make([]string, len(attr.Values))
			for index, value := range attr.Values {
				if len(value) > hardLDAPValueBytes {
					return nil, fmt.Errorf("LDAP attribute %q value exceeds %d bytes", attr.Name, hardLDAPValueBytes)
				}
				if err := retain(len(value)); err != nil {
					return nil, err
				}
				values[index] = value
			}
			attributes[attr.Name] = values
		}
		entries = append(entries, map[string]any{"dn": entry.DN, "attributes": attributes})
	}
	return entries, nil
}
