package network

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract"
)

const (
	defaultNetworkTimeout = 30 * time.Second
	// MaxConfiguredTimeout bounds both config.network.timeout_ms and the
	// per-action params.timeout_ms override before either is converted to a
	// time.Duration. Besides bounding stalled sockets, this prevents integer
	// overflow from silently disabling deadlines for direct protocol callers.
	MaxConfiguredTimeout = time.Hour
)

func init() {
	protocol.Register("network", func() protocol.Protocol {
		return &Network{}
	})
}

type Network struct {
	subprotocol        string
	host               string
	address            string
	port               int
	timeout            time.Duration
	useTLS             bool
	username           string
	password           string
	from               string
	to                 []string
	baseDN             string
	allowedTargetHosts []string
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
	n.subprotocol = strings.ToLower(strings.TrimSpace(protocol.ConfigString(config, "network_subprotocol")))
	n.host = strings.TrimSpace(protocol.ConfigString(config, "network_host"))
	n.address = strings.TrimSpace(protocol.ConfigString(config, "network_address"))
	n.port = protocol.ConfigInt(config, "network_port")
	var err error
	n.timeout, err = boundedNetworkTimeout(config["network_timeout_ms"], defaultNetworkTimeout)
	if err != nil {
		return fmt.Errorf("network timeout_ms %w", err)
	}
	n.useTLS, _ = config["network_tls"].(bool)
	n.username = protocol.ConfigString(config, "network_username")
	n.password = protocol.ConfigString(config, "network_password")
	n.from = protocol.ConfigString(config, "network_from")
	n.to = protocol.ConfigStringSlice(config, "network_to")
	n.baseDN = protocol.ConfigString(config, "network_base_dn")
	n.allowedTargetHosts = protocol.TargetPolicyHosts(config)
	return nil
}

func (n *Network) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{
		ActionName:     protocol.ActionName(action),
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

	timeout, err := n.actionTimeout(action)
	if err != nil {
		result.Duration = time.Since(start)
		result.Success = false
		result.StatusCode = 500
		result.ErrorMessage = err.Error()
		result.Snapshot = networkSnapshot(action, result)
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	actionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	op, err := n.execute(actionCtx, action)
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
		return n.runDNS(ctx, &protocol.Action{Name: action.Name, Type: "dns_query", Target: protocol.FirstNonEmpty(action.Target, n.host, n.address, "localhost")})
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

func (n *Network) dial(ctx context.Context, networkName, target string) (net.Conn, time.Duration, error) {
	if err := protocol.ValidateTargetAddress(target, n.allowedTargetHosts); err != nil {
		return nil, 0, err
	}
	timeout := n.timeout
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, 0, context.DeadlineExceeded
		}
		if timeout <= 0 || remaining < timeout {
			timeout = remaining
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	start := time.Now()
	var conn net.Conn
	var err error
	if networkName == "tcp" && n.useTLS {
		host, _, _ := net.SplitHostPort(target)
		raw, dialErr := dialer.DialContext(ctx, networkName, target)
		if dialErr != nil {
			return nil, 0, dialErr
		}
		tlsConn := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if handshakeErr := tlsConn.HandshakeContext(ctx); handshakeErr != nil {
			_ = raw.Close()
			return nil, 0, handshakeErr
		}
		conn = tlsConn
	} else {
		conn, err = dialer.DialContext(ctx, networkName, target)
	}
	if err != nil {
		return nil, 0, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	return conn, time.Since(start), nil
}

func (n *Network) endpoint(action *protocol.Action, defaultPort string) (string, error) {
	raw := protocol.FirstNonEmpty(actionParamString(action.Params, "address"), n.address, action.Target)
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
	raw := protocol.FirstNonEmpty(actionParamString(action.Params, "address"), n.address, action.Target)
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

func (n *Network) actionTimeout(action *protocol.Action) (time.Duration, error) {
	if action == nil || action.Params == nil {
		return n.timeout, nil
	}
	value, present := action.Params["timeout_ms"]
	if !present {
		return n.timeout, nil
	}
	timeout, err := boundedNetworkTimeout(value, n.timeout)
	if err != nil {
		return 0, fmt.Errorf("network action timeout_ms %w", err)
	}
	return timeout, nil
}

func boundedNetworkTimeout(value any, fallback time.Duration) (time.Duration, error) {
	return protocol.BoundedMilliseconds(value, fallback, MaxConfiguredTimeout)
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
		return protocol.FirstNonEmpty(actionParamString(action.Params, "command"), strings.TrimSpace(action.Value), string(action.Body), action.Target)
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
			if err := protocol.StoreExtraction(result, extractor.Name, value); err != nil {
				result.Extractions = nil
				return fmt.Sprintf("network extractor %q output rejected: %v", extractor.Name, err)
			}
		}
	}
	return ""
}

func extractNetworkValue(extractor protocol.Extractor, payload map[string]any, body []byte, result *protocol.Result) (string, error) {
	switch strings.ToLower(strings.TrimSpace(extractor.Source)) {
	case "", "body", "records":
		return extractNetworkBodyValue(extractor, body)
	case "response":
		return jsonextract.Stringify(payload["response"]), nil
	case "target":
		return result.DependencyTarget, nil
	case "record_count", "entry_count":
		return jsonextract.Stringify(payload[strings.ToLower(strings.TrimSpace(extractor.Source))]), nil
	default:
		return "", fmt.Errorf("unsupported network extraction source: %s", extractor.Source)
	}
}

func extractNetworkBodyValue(extractor protocol.Extractor, body []byte) (string, error) {
	switch strings.ToLower(strings.TrimSpace(extractor.Type)) {
	case "", "json", "jsonpath":
		return jsonextract.JSONPath(extractor.Expression, body)
	case "jmespath":
		return jsonextract.JMESPath(extractor.Expression, body)
	case "regex", "regexp":
		return jsonextract.Regex(extractor.Expression, body)
	case "boundary":
		return jsonextract.Boundary(extractor.Expression, body)
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
		RequestURL:     protocol.FirstNonEmpty(result.DependencyTarget, action.Target),
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
