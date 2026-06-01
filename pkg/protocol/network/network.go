package network

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jmespath/go-jmespath"
)

func init() {
	protocol.Register("network", func() protocol.Protocol {
		return &Network{}
	})
}

type Network struct {
	subprotocol string
	host        string
	address     string
	port        int
	timeout     time.Duration
	useTLS      bool
	username    string
	password    string
	from        string
	to          []string
	baseDN      string
}

type operationResult struct {
	payload       string
	target        string
	dependency    string
	bytesSent     int64
	bytesReceived int64
	connectTime   time.Duration
	dnsTime       time.Duration
	messagesSent  int
	messagesRecv  int
}

func (n *Network) Name() string { return "network" }

func (n *Network) Init(_ context.Context, config map[string]any) error {
	n.subprotocol = strings.ToLower(strings.TrimSpace(configString(config, "network_subprotocol")))
	n.host = strings.TrimSpace(configString(config, "network_host"))
	n.address = strings.TrimSpace(configString(config, "network_address"))
	n.port = configInt(config, "network_port")
	n.timeout = 30 * time.Second
	if timeoutMS := configInt(config, "network_timeout_ms"); timeoutMS > 0 {
		n.timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	n.useTLS, _ = config["network_tls"].(bool)
	n.username = configString(config, "network_username")
	n.password = configString(config, "network_password")
	n.from = configString(config, "network_from")
	n.to = configStringSlice(config, "network_to")
	n.baseDN = configString(config, "network_base_dn")
	return nil
}

func (n *Network) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{
		ActionName:     actionName(action),
		StartTime:      start,
		DependencyType: "network",
		Extractions:    make(map[string]string),
		Success:        true,
		StatusCode:     200,
	}
	if action == nil {
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = "network action is required"
		result.Duration = time.Since(start)
		return result, nil
	}

	op, err := n.execute(ctx, action)
	result.Duration = time.Since(start)
	if op != nil {
		result.ResponseBody = op.payload
		result.DependencyTarget = op.target
		if op.dependency != "" {
			result.DependencyType = op.dependency
		}
		result.BytesSent = op.bytesSent
		result.BytesReceived = op.bytesReceived
		result.ConnectTime = op.connectTime
		result.DNSTime = op.dnsTime
		result.ServerTelemetry = &protocol.ServerTelemetry{
			ServerProcessingMs: float64(result.Duration.Microseconds()) / 1000,
			RequestPayloadKB:   float64(op.bytesSent) / 1024,
			ResponsePayloadKB:  float64(op.bytesReceived) / 1024,
			MessagesSent:       op.messagesSent,
			MessagesReceived:   op.messagesRecv,
		}
	}
	if err != nil {
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = err.Error()
	}
	if result.Success {
		if extractorErr := runNetworkExtractors(action.Extractors, result); extractorErr != "" {
			result.Success = false
			result.StatusCode = 500
			result.ErrorMessage = extractorErr
		}
	}
	if assertionErr := evaluateNetworkAssertions(action.Assertions, result); assertionErr != "" {
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = assertionErr
	}
	if !result.Success {
		result.Snapshot = networkSnapshot(action, result)
	}
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	return result, nil
}

func (n *Network) execute(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "network_health":
		return n.runHealth(ctx, action)
	case "dns_query", "dns_lookup":
		return n.runDNS(ctx, action)
	case "tcp_request", "tcp_send", "winsock_tcp":
		return n.runSocket(ctx, action, "tcp")
	case "udp_request", "udp_send", "winsock_udp":
		return n.runSocket(ctx, action, "udp")
	case "ftp_login", "ftp_noop", "ftp_command":
		return n.runFTP(ctx, action)
	case "smtp_send":
		return n.runSMTP(ctx, action)
	case "pop3_command":
		return n.runPOP3(ctx, action)
	case "imap_command":
		return n.runIMAP(ctx, action)
	case "ldap_bind", "ldap_search":
		return n.runLDAP(ctx, action)
	default:
		return nil, fmt.Errorf("unsupported network action type: %s", action.Type)
	}
}

func (n *Network) Teardown(context.Context) error { return nil }

func (n *Network) HealthCheck(ctx context.Context) error {
	action := &protocol.Action{Name: "health", Type: "network_health"}
	_, err := n.runHealth(ctx, action)
	return err
}

func (n *Network) runHealth(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	switch n.effectiveSubprotocol(action) {
	case "dns":
		return n.runDNS(ctx, &protocol.Action{Name: action.Name, Type: "dns_query", Target: firstNonEmpty(action.Target, n.host, n.address, "localhost")})
	case "ldap":
		return n.runLDAP(ctx, &protocol.Action{Name: action.Name, Type: "ldap_bind", Target: action.Target})
	default:
		networkName := "tcp"
		if n.effectiveSubprotocol(action) == "udp" {
			networkName = "udp"
		}
		target, err := n.endpoint(action, defaultPortForSubprotocol(n.effectiveSubprotocol(action)))
		if err != nil {
			return nil, err
		}
		conn, connectTime, err := n.dial(ctx, networkName, target)
		if err != nil {
			return nil, err
		}
		_ = conn.Close()
		return &operationResult{
			payload:      encodePayload(map[string]any{"status": "ok", "network": networkName, "target": target}),
			target:       target,
			dependency:   "network_" + networkName,
			connectTime:  connectTime,
			messagesSent: 1,
			messagesRecv: 1,
		}, nil
	}
}

func (n *Network) runDNS(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	domain := strings.TrimSpace(firstNonEmpty(action.Target, n.host, n.address))
	if domain == "" {
		return nil, errors.New("dns query target is required")
	}
	recordType := strings.ToUpper(strings.TrimSpace(actionParamString(action.Params, "record_type")))
	if recordType == "" {
		recordType = "A"
	}
	start := time.Now()
	records := map[string]any{"record_type": recordType, "name": domain}
	switch recordType {
	case "A", "AAAA":
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(addrs))
		for _, addr := range addrs {
			if recordType == "A" && addr.IP.To4() == nil {
				continue
			}
			if recordType == "AAAA" && addr.IP.To4() != nil {
				continue
			}
			values = append(values, addr.IP.String())
		}
		sort.Strings(values)
		records["records"] = values
	case "CNAME":
		cname, err := net.DefaultResolver.LookupCNAME(ctx, domain)
		if err != nil {
			return nil, err
		}
		records["records"] = []string{cname}
	case "MX":
		mx, err := net.DefaultResolver.LookupMX(ctx, domain)
		if err != nil {
			return nil, err
		}
		values := make([]map[string]any, 0, len(mx))
		for _, item := range mx {
			values = append(values, map[string]any{"host": item.Host, "pref": item.Pref})
		}
		records["records"] = values
	case "NS":
		ns, err := net.DefaultResolver.LookupNS(ctx, domain)
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(ns))
		for _, item := range ns {
			values = append(values, item.Host)
		}
		sort.Strings(values)
		records["records"] = values
	case "TXT":
		txt, err := net.DefaultResolver.LookupTXT(ctx, domain)
		if err != nil {
			return nil, err
		}
		sort.Strings(txt)
		records["records"] = txt
	default:
		return nil, fmt.Errorf("unsupported dns record_type %q", recordType)
	}
	records["record_count"] = recordCount(records["records"])
	payload := encodePayload(records)
	return &operationResult{
		payload:       payload,
		target:        domain,
		dependency:    "network_dns",
		bytesReceived: int64(len(payload)),
		dnsTime:       time.Since(start),
		messagesSent:  1,
		messagesRecv:  1,
	}, nil
}

func (n *Network) runSocket(ctx context.Context, action *protocol.Action, networkName string) (*operationResult, error) {
	target, err := n.endpoint(action, defaultPortForSubprotocol(networkName))
	if err != nil {
		return nil, err
	}
	conn, connectTime, err := n.dial(ctx, networkName, target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload := socketPayload(action)
	var bytesSent int64
	if len(payload) > 0 {
		written, err := conn.Write(payload)
		bytesSent = int64(written)
		if err != nil {
			return nil, err
		}
	}
	readLimit := actionParamInt(action.Params, "read_bytes")
	readRequested := actionParamBool(action.Params, "read") || readLimit > 0
	if readLimit <= 0 {
		readLimit = 4096
	}
	var response []byte
	if readRequested {
		response = make([]byte, readLimit)
		read, err := conn.Read(response)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		response = response[:read]
	}
	body := encodePayload(map[string]any{
		"network":        networkName,
		"target":         target,
		"bytes_sent":     bytesSent,
		"bytes_received": len(response),
		"response":       string(response),
	})
	return &operationResult{
		payload:       body,
		target:        target,
		dependency:    "network_" + networkName,
		bytesSent:     bytesSent,
		bytesReceived: int64(len(response)),
		connectTime:   connectTime,
		messagesSent:  boolCount(len(payload) > 0),
		messagesRecv:  boolCount(len(response) > 0),
	}, nil
}

func (n *Network) runFTP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "21")
	if err != nil {
		return nil, err
	}
	conn, connectTime, err := n.dial(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tp := textproto.NewConn(conn)
	defer tp.Close()

	lines := []string{}
	greeting, err := readResponseLine(tp)
	if err != nil {
		return nil, err
	}
	lines = append(lines, greeting)
	username := firstNonEmpty(actionParamString(action.Params, "username"), n.username, "anonymous")
	password := firstNonEmpty(actionParamString(action.Params, "password"), n.password, "apexload@example.invalid")
	if username != "" {
		lines = append(lines, n.commandResponse(tp, "USER "+username))
		if password != "" {
			lines = append(lines, n.commandResponse(tp, "PASS "+password))
		}
	}
	command := ftpCommand(action)
	if command == "" {
		command = "NOOP"
	}
	lines = append(lines, n.commandResponse(tp, command))
	lines = append(lines, n.commandResponse(tp, "QUIT"))
	payload := encodePayload(map[string]any{"protocol": "ftp", "target": target, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_ftp",
		bytesSent:     int64(len(username) + len(password) + len(command)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  3,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}

func (n *Network) runSMTP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "25")
	if err != nil {
		return nil, err
	}
	conn, connectTime, err := n.dial(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tp := textproto.NewConn(conn)
	defer tp.Close()

	lines := []string{}
	if _, line, err := tp.ReadResponse(220); err == nil {
		lines = append(lines, line)
	} else {
		return nil, err
	}
	hostname := firstNonEmpty(actionParamString(action.Params, "helo"), "apexload.local")
	lines = append(lines, smtpCommandResponse(tp, "EHLO "+hostname, 250))
	if n.username != "" || actionParamString(action.Params, "username") != "" {
		username := firstNonEmpty(actionParamString(action.Params, "username"), n.username)
		password := firstNonEmpty(actionParamString(action.Params, "password"), n.password)
		auth := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
		lines = append(lines, smtpCommandResponse(tp, "AUTH PLAIN "+auth, 235))
	}
	from := firstNonEmpty(actionParamString(action.Params, "from"), n.from)
	if from == "" {
		return nil, errors.New("smtp from address is required")
	}
	to := actionParamStringSlice(action.Params, "to")
	if len(to) == 0 {
		to = n.to
	}
	if len(to) == 0 {
		return nil, errors.New("smtp recipient is required")
	}
	lines = append(lines, smtpCommandResponse(tp, "MAIL FROM:<"+from+">", 250))
	for _, recipient := range to {
		lines = append(lines, smtpCommandResponse(tp, "RCPT TO:<"+recipient+">", 250))
	}
	if err := tp.PrintfLine("DATA"); err != nil {
		return nil, err
	}
	lines = append(lines, mustReadResponse(tp, 354))
	message := smtpMessage(from, to, action)
	writer := tp.DotWriter()
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	lines = append(lines, mustReadResponse(tp, 250))
	lines = append(lines, smtpCommandResponse(tp, "QUIT", 221))
	payload := encodePayload(map[string]any{"protocol": "smtp", "target": target, "from": from, "to": to, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_smtp",
		bytesSent:     int64(len(message)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  len(to) + 5,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}

func (n *Network) runPOP3(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "110")
	if err != nil {
		return nil, err
	}
	conn, connectTime, err := n.dial(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tp := textproto.NewConn(conn)
	defer tp.Close()

	lines := []string{}
	greeting, err := readResponseLine(tp)
	if err != nil {
		return nil, err
	}
	lines = append(lines, greeting)
	username := firstNonEmpty(actionParamString(action.Params, "username"), n.username)
	password := firstNonEmpty(actionParamString(action.Params, "password"), n.password)
	if username != "" {
		lines = append(lines, n.commandResponse(tp, "USER "+username))
		if password != "" {
			lines = append(lines, n.commandResponse(tp, "PASS "+password))
		}
	}
	command := firstNonEmpty(actionParamString(action.Params, "command"), strings.TrimSpace(action.Value), string(action.Body), action.Target)
	if command == "" || strings.Contains(command, ":") {
		command = "STAT"
	}
	lines = append(lines, n.commandResponse(tp, command))
	lines = append(lines, n.commandResponse(tp, "QUIT"))
	payload := encodePayload(map[string]any{"protocol": "pop3", "target": target, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_pop3",
		bytesSent:     int64(len(command)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  2,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}

func (n *Network) runIMAP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	target, err := n.endpoint(action, "143")
	if err != nil {
		return nil, err
	}
	conn, connectTime, err := n.dial(ctx, "tcp", target)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tp := textproto.NewConn(conn)
	defer tp.Close()

	lines := []string{}
	greeting, err := readResponseLine(tp)
	if err != nil {
		return nil, err
	}
	lines = append(lines, greeting)
	tagID := 1
	username := firstNonEmpty(actionParamString(action.Params, "username"), n.username)
	password := firstNonEmpty(actionParamString(action.Params, "password"), n.password)
	if username != "" {
		tag := fmt.Sprintf("A%03d", tagID)
		tagID++
		if err := tp.PrintfLine("%s LOGIN %s %s", tag, username, password); err != nil {
			return nil, err
		}
		loginLines, err := readIMAPTaggedResponse(tp, tag)
		if err != nil {
			return nil, err
		}
		lines = append(lines, loginLines...)
	}
	command := firstNonEmpty(actionParamString(action.Params, "command"), strings.TrimSpace(action.Value), string(action.Body), "CAPABILITY")
	tag := fmt.Sprintf("A%03d", tagID)
	tagID++
	if err := tp.PrintfLine("%s %s", tag, command); err != nil {
		return nil, err
	}
	commandLines, err := readIMAPTaggedResponse(tp, tag)
	if err != nil {
		return nil, err
	}
	lines = append(lines, commandLines...)
	logoutTag := fmt.Sprintf("A%03d", tagID)
	_ = tp.PrintfLine("%s LOGOUT", logoutTag)
	logoutLines, _ := readIMAPTaggedResponse(tp, logoutTag)
	lines = append(lines, logoutLines...)
	payload := encodePayload(map[string]any{"protocol": "imap", "target": target, "responses": compactLines(lines)})
	return &operationResult{
		payload:       payload,
		target:        target,
		dependency:    "network_imap",
		bytesSent:     int64(len(command)),
		bytesReceived: int64(len(payload)),
		connectTime:   connectTime,
		messagesSent:  tagID,
		messagesRecv:  len(lines),
	}, firstResponseError(lines)
}

func (n *Network) runLDAP(ctx context.Context, action *protocol.Action) (*operationResult, error) {
	_ = ctx
	address := n.ldapURL(action)
	start := time.Now()
	dialer := &net.Dialer{Timeout: n.actionTimeout(action)}
	conn, err := ldap.DialURL(address, ldap.DialWithDialer(dialer), ldap.DialWithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	connectTime := time.Since(start)

	username := firstNonEmpty(actionParamString(action.Params, "username"), n.username)
	password := firstNonEmpty(actionParamString(action.Params, "password"), n.password)
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

	baseDN := firstNonEmpty(action.Target, actionParamString(action.Params, "base_dn"), n.baseDN)
	if baseDN == "" {
		return nil, errors.New("ldap search base DN is required")
	}
	filter := firstNonEmpty(actionParamString(action.Params, "filter"), "(objectClass=*)")
	attrs := actionParamStringSlice(action.Params, "attributes")
	maxEntries := actionParamInt(action.Params, "max_entries")
	if maxEntries <= 0 {
		maxEntries = 100
	}
	searchRequest := ldap.NewSearchRequest(baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, maxEntries, int(n.actionTimeout(action).Seconds()), false, filter, attrs, nil)
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

func (n *Network) dial(ctx context.Context, networkName, target string) (net.Conn, time.Duration, error) {
	timeout := n.timeout
	dialer := &net.Dialer{Timeout: timeout}
	start := time.Now()
	var conn net.Conn
	var err error
	if networkName == "tcp" && n.useTLS {
		host, _, _ := net.SplitHostPort(target)
		conn, err = tls.DialWithDialer(dialer, networkName, target, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, networkName, target)
	}
	if err != nil {
		return nil, 0, err
	}
	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	return conn, time.Since(start), nil
}

func (n *Network) endpoint(action *protocol.Action, defaultPort string) (string, error) {
	raw := firstNonEmpty(actionParamString(action.Params, "address"), n.address, action.Target)
	if raw == "" {
		if n.host == "" {
			return "", errors.New("network target address is required")
		}
		port := n.port
		if port == 0 {
			if parsed, err := textPort(defaultPort); err == nil {
				port = parsed
			}
		}
		return net.JoinHostPort(n.host, fmt.Sprintf("%d", port)), nil
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		raw = parsed.Host
	}
	if _, _, err := net.SplitHostPort(raw); err == nil {
		return raw, nil
	}
	port := n.port
	if actionPort := actionParamInt(action.Params, "port"); actionPort > 0 {
		port = actionPort
	}
	if port == 0 {
		parsed, err := textPort(defaultPort)
		if err != nil {
			return "", fmt.Errorf("network target %q is missing port", raw)
		}
		port = parsed
	}
	return net.JoinHostPort(raw, fmt.Sprintf("%d", port)), nil
}

func (n *Network) effectiveSubprotocol(action *protocol.Action) string {
	if subprotocol := actionParamString(action.Params, "subprotocol"); subprotocol != "" {
		return strings.ToLower(subprotocol)
	}
	if n.subprotocol != "" {
		return n.subprotocol
	}
	if action == nil {
		return "tcp"
	}
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "dns_query", "dns_lookup":
		return "dns"
	case "udp_request", "udp_send", "winsock_udp":
		return "udp"
	case "ftp_login", "ftp_noop", "ftp_command":
		return "ftp"
	case "smtp_send":
		return "smtp"
	case "pop3_command":
		return "pop3"
	case "imap_command":
		return "imap"
	case "ldap_bind", "ldap_search":
		return "ldap"
	default:
		return "tcp"
	}
}

func (n *Network) ldapURL(action *protocol.Action) string {
	raw := firstNonEmpty(actionParamString(action.Params, "address"), n.address, action.Target)
	if raw == "" && n.host != "" {
		port := n.port
		if port == 0 {
			if n.useTLS {
				port = 636
			} else {
				port = 389
			}
		}
		raw = net.JoinHostPort(n.host, fmt.Sprintf("%d", port))
	}
	if !strings.Contains(raw, "://") {
		scheme := "ldap"
		if n.useTLS {
			scheme = "ldaps"
		}
		raw = scheme + "://" + raw
	}
	return raw
}

func (n *Network) actionTimeout(action *protocol.Action) time.Duration {
	if timeoutMS := actionParamInt(action.Params, "timeout_ms"); timeoutMS > 0 {
		return time.Duration(timeoutMS) * time.Millisecond
	}
	return n.timeout
}

func (n *Network) commandResponse(tp *textproto.Conn, command string) string {
	if command == "" {
		return ""
	}
	if err := tp.PrintfLine("%s", command); err != nil {
		return "ERR " + err.Error()
	}
	line, err := readResponseLine(tp)
	if err != nil {
		return "ERR " + err.Error()
	}
	return line
}

func actionName(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	return action.Name
}

func configString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func configStringSlice(config map[string]any, key string) []string {
	if config == nil {
		return nil
	}
	switch value := config[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func configInt(config map[string]any, key string) int {
	if config == nil {
		return 0
	}
	switch value := config[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

func actionParamString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if value, ok := params[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func actionParamStringSlice(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	switch value := params[key].(type) {
	case []string:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				out = append(out, strings.TrimSpace(part))
			}
		}
		return out
	default:
		return nil
	}
}

func actionParamInt(params map[string]any, key string) int {
	if params == nil {
		return 0
	}
	switch value := params[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

func actionParamBool(params map[string]any, key string) bool {
	if params == nil {
		return false
	}
	value, _ := params[key].(bool)
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func defaultPortForSubprotocol(subprotocol string) string {
	switch strings.ToLower(subprotocol) {
	case "dns":
		return "53"
	case "ftp":
		return "21"
	case "smtp":
		return "25"
	case "pop3":
		return "110"
	case "imap":
		return "143"
	case "ldap":
		return "389"
	default:
		return "0"
	}
}

func socketPayload(action *protocol.Action) []byte {
	if len(action.Body) > 0 {
		return action.Body
	}
	if action.Value != "" {
		return []byte(action.Value)
	}
	if payload := actionParamString(action.Params, "payload"); payload != "" {
		return []byte(payload)
	}
	return nil
}

func ftpCommand(action *protocol.Action) string {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "ftp_login":
		return "PWD"
	case "ftp_noop":
		return "NOOP"
	default:
		return firstNonEmpty(actionParamString(action.Params, "command"), strings.TrimSpace(action.Value), string(action.Body), action.Target)
	}
}

func smtpMessage(from string, to []string, action *protocol.Action) string {
	body := string(action.Body)
	if body == "" {
		body = action.Value
	}
	if body == "" {
		body = "apexload smtp probe"
	}
	headers := map[string]string{
		"From": from,
		"To":   strings.Join(to, ", "),
	}
	if subject := actionParamString(action.Params, "subject"); subject != "" {
		headers["Subject"] = subject
	}
	lines := make([]string, 0, len(headers)+2)
	for key, value := range headers {
		lines = append(lines, key+": "+value)
	}
	sort.Strings(lines)
	lines = append(lines, "", body)
	return strings.Join(lines, "\r\n")
}

func readResponseLine(tp *textproto.Conn) (string, error) {
	line, err := tp.ReadLine()
	if err != nil {
		return "", err
	}
	return line, nil
}

func mustReadResponse(tp *textproto.Conn, code int) string {
	_, line, err := tp.ReadResponse(code)
	if err != nil {
		return "ERR " + err.Error()
	}
	return line
}

func smtpCommandResponse(tp *textproto.Conn, command string, code int) string {
	if err := tp.PrintfLine("%s", command); err != nil {
		return "ERR " + err.Error()
	}
	return mustReadResponse(tp, code)
}

func readIMAPTaggedResponse(tp *textproto.Conn, tag string) ([]string, error) {
	lines := []string{}
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return lines, err
		}
		lines = append(lines, line)
		if strings.HasPrefix(line, tag+" ") {
			return lines, nil
		}
	}
}

func compactLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstResponseError(lines []string) error {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ERR ") {
			return errors.New(strings.TrimPrefix(line, "ERR "))
		}
		if strings.HasPrefix(line, "-ERR") || strings.Contains(strings.ToUpper(line), " NO ") || strings.Contains(strings.ToUpper(line), " BAD ") || strings.HasPrefix(line, "4") || strings.HasPrefix(line, "5") {
			return errors.New(line)
		}
	}
	return nil
}

func textPort(raw string) (int, error) {
	if raw == "" || raw == "0" {
		return 0, nil
	}
	var port int
	if _, err := fmt.Sscanf(raw, "%d", &port); err != nil {
		return 0, err
	}
	return port, nil
}

func encodePayload(payload map[string]any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return `{"error":"failed to encode network payload"}`
	}
	return string(encoded)
}

func runNetworkExtractors(extractors []protocol.Extractor, result *protocol.Result) string {
	if len(extractors) == 0 {
		return ""
	}
	payload, err := decodeNetworkPayload(result.ResponseBody)
	if err != nil {
		return err.Error()
	}
	for _, extractor := range extractors {
		value, err := extractNetworkValue(extractor, payload, []byte(result.ResponseBody), result)
		if err != nil {
			return err.Error()
		}
		if extractor.Name != "" {
			result.Extractions[extractor.Name] = value
		}
	}
	return ""
}

func extractNetworkValue(extractor protocol.Extractor, payload map[string]any, body []byte, result *protocol.Result) (string, error) {
	switch strings.ToLower(strings.TrimSpace(extractor.Source)) {
	case "", "body", "records":
		return extractNetworkBodyValue(extractor, body)
	case "response":
		return stringifyNetworkExtraction(payload["response"]), nil
	case "target":
		return result.DependencyTarget, nil
	case "record_count", "entry_count":
		return stringifyNetworkExtraction(payload[strings.ToLower(strings.TrimSpace(extractor.Source))]), nil
	default:
		return "", fmt.Errorf("unsupported network extraction source: %s", extractor.Source)
	}
}

func extractNetworkBodyValue(extractor protocol.Extractor, body []byte) (string, error) {
	switch strings.ToLower(strings.TrimSpace(extractor.Type)) {
	case "", "json", "jsonpath":
		return extractNetworkJSONPath(extractor.Expression, body)
	case "jmespath":
		return extractNetworkJMESPath(extractor.Expression, body)
	case "regex", "regexp":
		return extractNetworkRegex(extractor.Expression, body)
	case "boundary":
		return extractNetworkBoundary(extractor.Expression, body)
	default:
		return "", fmt.Errorf("unsupported network extraction type: %s", extractor.Type)
	}
}

func evaluateNetworkAssertions(assertions []protocol.Assertion, result *protocol.Result) string {
	if len(assertions) == 0 {
		return ""
	}
	payload, _ := decodeNetworkPayload(result.ResponseBody)
	for _, assertion := range assertions {
		switch strings.ToLower(strings.TrimSpace(assertion.Type)) {
		case "record_count":
			actual := numericNetworkValue(payload["record_count"])
			expected, ok := numericNetworkAssertionValue(assertion.Expected)
			if ok && actual != expected {
				return fmt.Sprintf("record_count assertion failed: got %d, want %d", actual, expected)
			}
			if assertion.Max > 0 && actual > assertion.Max {
				return fmt.Sprintf("record_count assertion failed: got %d, max %d", actual, assertion.Max)
			}
		case "entry_count":
			actual := numericNetworkValue(payload["entry_count"])
			expected, ok := numericNetworkAssertionValue(assertion.Expected)
			if ok && actual != expected {
				return fmt.Sprintf("entry_count assertion failed: got %d, want %d", actual, expected)
			}
			if assertion.Max > 0 && actual > assertion.Max {
				return fmt.Sprintf("entry_count assertion failed: got %d, max %d", actual, assertion.Max)
			}
		case "messages_received":
			actual := 0
			if result.ServerTelemetry != nil {
				actual = result.ServerTelemetry.MessagesReceived
			}
			expected, ok := numericNetworkAssertionValue(assertion.Expected)
			if ok && actual != expected {
				return fmt.Sprintf("messages_received assertion failed: got %d, want %d", actual, expected)
			}
		case "body_contains":
			if !strings.Contains(result.ResponseBody, assertion.Value) {
				return fmt.Sprintf("body_contains assertion failed: %q not found", assertion.Value)
			}
		case "body_not_contains":
			if strings.Contains(result.ResponseBody, assertion.Value) {
				return fmt.Sprintf("body_not_contains assertion failed: %q found", assertion.Value)
			}
		case "response_time_ms":
			if assertion.Max > 0 && result.Duration > time.Duration(assertion.Max)*time.Millisecond {
				return fmt.Sprintf("response_time_ms assertion failed: got %.1f ms, max %d", float64(result.Duration.Microseconds())/1000, assertion.Max)
			}
		case "status_code":
			expected, ok := numericNetworkAssertionValue(assertion.Expected)
			if ok && result.StatusCode != expected {
				return fmt.Sprintf("status_code assertion failed: got %d, want %d", result.StatusCode, expected)
			}
		}
	}
	return ""
}

func decodeNetworkPayload(body string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, fmt.Errorf("decode network response: %w", err)
	}
	return payload, nil
}

func extractNetworkJSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", fmt.Errorf("jsonpath expression is required")
	}
	expr = strings.TrimPrefix(expr, "$.")
	expr = strings.TrimPrefix(expr, "$")
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", err
	}
	if expr == "" {
		return stringifyNetworkExtraction(current), nil
	}
	for _, part := range strings.Split(expr, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, indexes := splitNetworkPathToken(part)
		if key != "" {
			object, ok := current.(map[string]any)
			if !ok {
				return "", fmt.Errorf("jsonpath %q expected object", expr)
			}
			next, ok := object[key]
			if !ok {
				return "", fmt.Errorf("jsonpath %q not found", expr)
			}
			current = next
		}
		for _, index := range indexes {
			items, ok := current.([]any)
			if !ok || index < 0 || index >= len(items) {
				return "", fmt.Errorf("jsonpath %q index out of range", expr)
			}
			current = items[index]
		}
	}
	return stringifyNetworkExtraction(current), nil
}

func splitNetworkPathToken(token string) (string, []int) {
	key := token
	indexes := []int{}
	if bracket := strings.Index(token, "["); bracket >= 0 {
		key = token[:bracket]
		rest := token[bracket:]
		for strings.HasPrefix(rest, "[") {
			end := strings.Index(rest, "]")
			if end <= 1 {
				break
			}
			var index int
			if _, err := fmt.Sscanf(rest[1:end], "%d", &index); err == nil {
				indexes = append(indexes, index)
			}
			rest = rest[end+1:]
		}
	}
	return key, indexes
}

func extractNetworkJMESPath(expr string, body []byte) (string, error) {
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", err
	}
	value, err := jmespath.Search(expr, current)
	if err != nil {
		return "", err
	}
	return stringifyNetworkExtraction(value), nil
}

func extractNetworkRegex(expr string, body []byte) (string, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", err
	}
	match := re.FindSubmatch(body)
	if len(match) == 0 {
		return "", fmt.Errorf("regex %q not found", expr)
	}
	if len(match) > 1 {
		return string(match[1]), nil
	}
	return string(match[0]), nil
}

func extractNetworkBoundary(expr string, body []byte) (string, error) {
	parts := strings.Split(expr, "|||")
	if len(parts) != 2 {
		return "", fmt.Errorf("boundary expression must be left|||right")
	}
	text := string(body)
	start := strings.Index(text, parts[0])
	if start < 0 {
		return "", fmt.Errorf("left boundary not found")
	}
	start += len(parts[0])
	end := strings.Index(text[start:], parts[1])
	if end < 0 {
		return "", fmt.Errorf("right boundary not found")
	}
	return text[start : start+end], nil
}

func stringifyNetworkExtraction(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64, bool, int, int64:
		return fmt.Sprint(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func numericNetworkValue(value any) int {
	out, _ := numericNetworkAssertionValue(value)
	return out
}

func numericNetworkAssertionValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	default:
		return 0, false
	}
}

func networkSnapshot(action *protocol.Action, result *protocol.Result) *protocol.ErrorSnapshot {
	if action == nil || result == nil {
		return nil
	}
	return &protocol.ErrorSnapshot{
		RequestMethod:  action.Type,
		RequestURL:     firstNonEmpty(result.DependencyTarget, action.Target),
		RequestHeaders: cloneActionHeaders(action.Headers),
		RequestBody:    string(action.Body),
		ResponseStatus: result.StatusCode,
		ResponseBody:   result.ResponseBody,
	}
}

func cloneActionHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func recordCount(value any) int {
	switch typed := value.(type) {
	case []string:
		return len(typed)
	case []map[string]any:
		return len(typed)
	default:
		return 0
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
