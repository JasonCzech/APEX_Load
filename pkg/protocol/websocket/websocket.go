package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jmespath/go-jmespath"
)

const (
	defaultReadTimeout    = 30 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	defaultMaxMessageSize = int64(1024 * 1024)
)

func init() {
	protocol.Register("websocket", func() protocol.Protocol {
		return &WebSocket{}
	})
}

type WebSocket struct {
	baseURL        string
	conn           *gorillawebsocket.Conn
	dialer         *gorillawebsocket.Dialer
	defaultHeaders http.Header
	readTimeout    time.Duration
	writeTimeout   time.Duration
	maxMessageSize int64
	origin         string
	reconnect      bool
	lastTarget     string
	lastHeaders    http.Header
	mu             sync.Mutex
}

func (w *WebSocket) Name() string { return "websocket" }

func (w *WebSocket) Init(_ context.Context, config map[string]any) error {
	w.baseURL, _ = stringConfig(config, "base_url")
	w.readTimeout = durationConfig(config, "websocket_read_timeout_ms", defaultReadTimeout)
	w.writeTimeout = durationConfig(config, "websocket_write_timeout_ms", defaultWriteTimeout)
	w.maxMessageSize = int64Config(config, "websocket_max_message_size", defaultMaxMessageSize)
	w.origin, _ = stringConfig(config, "websocket_origin")
	w.reconnect = boolConfigDefault(config, "websocket_reconnect", true)

	subprotocol, _ := stringConfig(config, "websocket_subprotocol")
	compression, _ := boolConfig(config, "websocket_compression")
	w.dialer = &gorillawebsocket.Dialer{
		Proxy:             http.ProxyFromEnvironment,
		HandshakeTimeout:  w.writeTimeout,
		EnableCompression: compression,
	}
	if subprotocol != "" {
		w.dialer.Subprotocols = []string{subprotocol}
	}

	w.defaultHeaders = buildDefaultHeaders(config)
	return nil
}

func (w *WebSocket) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := &protocol.Result{
		ActionName:     action.Name,
		StartTime:      time.Now(),
		Extractions:    make(map[string]string),
		DependencyType: "websocket",
	}

	var err error
	switch action.Type {
	case "ws_connect":
		err = w.connect(ctx, action, result)
	case "ws_send":
		err = w.send(ctx, action, result)
	case "ws_receive":
		err = w.receive(action, result)
	case "ws_ping":
		err = w.ping(ctx, action, result)
	case "ws_close":
		err = w.close(action, result)
	default:
		err = fmt.Errorf("unsupported websocket action type: %s", action.Type)
	}

	result.Duration = time.Since(result.StartTime)
	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		result.Snapshot = w.buildSnapshot(action, result)
		return result, nil
	}
	result.Success = true
	return result, nil
}

func (w *WebSocket) Teardown(_ context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn == nil {
		return nil
	}
	err := w.conn.Close()
	w.conn = nil
	return err
}

func (w *WebSocket) HealthCheck(ctx context.Context) error {
	target, err := w.resolveURL("")
	if err != nil {
		return err
	}
	headers := cloneHeader(w.defaultHeaders)
	if w.origin != "" {
		headers.Set("Origin", w.origin)
	}
	conn, _, err := w.dialer.DialContext(ctx, target, headers)
	if err != nil {
		return err
	}
	return conn.Close()
}

func (w *WebSocket) connect(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	target, err := w.resolveURL(action.Target)
	if err != nil {
		return err
	}
	result.DependencyTarget = dependencyTarget(target)

	headers := cloneHeader(w.defaultHeaders)
	for key, value := range action.Headers {
		headers.Set(key, value)
	}
	if w.origin != "" && headers.Get("Origin") == "" {
		headers.Set("Origin", w.origin)
	}

	conn, resp, err := w.dialer.DialContext(ctx, target, headers)
	if resp != nil {
		result.StatusCode = resp.StatusCode
	}
	if err != nil {
		return err
	}
	if w.maxMessageSize > 0 {
		conn.SetReadLimit(w.maxMessageSize)
	}

	w.mu.Lock()
	if w.conn != nil {
		_ = w.conn.Close()
	}
	w.conn = conn
	w.lastTarget = target
	w.lastHeaders = cloneHeader(headers)
	w.mu.Unlock()

	result.StatusCode = http.StatusSwitchingProtocols
	result.ServerTelemetry = &protocol.ServerTelemetry{ConnectionReused: false}
	return nil
}

func (w *WebSocket) send(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	conn, err := w.currentConn()
	if err != nil {
		return err
	}
	messageType, err := messageType(action)
	if err != nil {
		return err
	}
	if err := w.writeMessage(conn, messageType, action.Body); err != nil {
		if !w.reconnect {
			return err
		}
		conn, reconnectErr := w.reconnectConn(ctx, result)
		if reconnectErr != nil {
			return fmt.Errorf("%w; reconnect failed: %v", err, reconnectErr)
		}
		if retryErr := w.writeMessage(conn, messageType, action.Body); retryErr != nil {
			return retryErr
		}
	}
	result.BytesSent = int64(len(action.Body))
	result.ServerTelemetry = &protocol.ServerTelemetry{MessagesSent: 1}
	return nil
}

func (w *WebSocket) receive(action *protocol.Action, result *protocol.Result) error {
	conn, err := w.currentConn()
	if err != nil {
		return err
	}
	timeout := actionTimeout(action, w.readTimeout)
	if timeout > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
	}
	messageType, message, err := conn.ReadMessage()
	if err != nil {
		w.discardConn(conn)
		return err
	}
	result.BytesReceived = int64(len(message))
	result.ResponseBody = responseBody(messageType, message)
	result.ServerTelemetry = &protocol.ServerTelemetry{MessagesReceived: 1}

	if expected, ok := stringParam(action.Params, "match"); ok && !strings.Contains(result.ResponseBody, expected) {
		return fmt.Errorf("websocket message does not contain %q", expected)
	}
	for _, extractor := range action.Extractors {
		value, err := extract(extractor, []byte(result.ResponseBody))
		if err == nil {
			result.Extractions[extractor.Name] = value
		}
	}
	for _, assertion := range action.Assertions {
		if err := evaluate(assertion, result, []byte(result.ResponseBody)); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebSocket) ping(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	conn, err := w.currentConn()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(w.writeTimeout)
	if err := conn.WriteControl(gorillawebsocket.PingMessage, action.Body, deadline); err != nil {
		if !w.reconnect {
			return err
		}
		conn, reconnectErr := w.reconnectConn(ctx, result)
		if reconnectErr != nil {
			return fmt.Errorf("%w; reconnect failed: %v", err, reconnectErr)
		}
		deadline = time.Now().Add(w.writeTimeout)
		if retryErr := conn.WriteControl(gorillawebsocket.PingMessage, action.Body, deadline); retryErr != nil {
			return retryErr
		}
	}
	result.BytesSent = int64(len(action.Body))
	result.ServerTelemetry = &protocol.ServerTelemetry{MessagesSent: 1}
	return nil
}

func (w *WebSocket) close(action *protocol.Action, result *protocol.Result) error {
	w.mu.Lock()
	conn := w.conn
	w.conn = nil
	w.mu.Unlock()
	if conn == nil {
		return nil
	}

	code := intParam(action.Params, "code", gorillawebsocket.CloseNormalClosure)
	reason, _ := stringParam(action.Params, "reason")
	msg := gorillawebsocket.FormatCloseMessage(code, reason)
	deadline := time.Now().Add(w.writeTimeout)
	_ = conn.WriteControl(gorillawebsocket.CloseMessage, msg, deadline)
	result.BytesSent = int64(len(msg))
	return conn.Close()
}

func (w *WebSocket) currentConn() (*gorillawebsocket.Conn, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn == nil {
		return nil, fmt.Errorf("websocket connection is not open")
	}
	return w.conn, nil
}

func (w *WebSocket) writeMessage(conn *gorillawebsocket.Conn, messageType int, body []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(w.writeTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(messageType, body)
}

func (w *WebSocket) reconnectConn(ctx context.Context, result *protocol.Result) (*gorillawebsocket.Conn, error) {
	w.mu.Lock()
	target := w.lastTarget
	headers := cloneHeader(w.lastHeaders)
	oldConn := w.conn
	w.conn = nil
	w.mu.Unlock()

	if oldConn != nil {
		_ = oldConn.Close()
	}
	if target == "" {
		return nil, fmt.Errorf("no previous websocket connection target")
	}

	conn, resp, err := w.dialer.DialContext(ctx, target, headers)
	if resp != nil {
		result.StatusCode = resp.StatusCode
	}
	if err != nil {
		return nil, err
	}
	if w.maxMessageSize > 0 {
		conn.SetReadLimit(w.maxMessageSize)
	}

	w.mu.Lock()
	if w.conn != nil {
		_ = w.conn.Close()
	}
	w.conn = conn
	w.mu.Unlock()

	result.DependencyTarget = dependencyTarget(target)
	result.ServerTelemetry = &protocol.ServerTelemetry{ConnectionReused: false}
	return conn, nil
}

func (w *WebSocket) discardConn(conn *gorillawebsocket.Conn) {
	w.mu.Lock()
	if w.conn == conn {
		w.conn = nil
	}
	w.mu.Unlock()
	_ = conn.Close()
}

func (w *WebSocket) buildSnapshot(action *protocol.Action, result *protocol.Result) *protocol.ErrorSnapshot {
	if action == nil || result == nil {
		return nil
	}
	requestURL := strings.TrimSpace(action.Target)
	if action.RedactedTarget != "" {
		requestURL = strings.TrimSpace(action.RedactedTarget)
	}
	if requestURL != "" {
		if resolved, err := w.resolveURL(requestURL); err == nil {
			requestURL = resolved
		}
	}
	if requestURL == "" {
		w.mu.Lock()
		requestURL = w.lastTarget
		w.mu.Unlock()
	}

	headers := cloneStringHeaders(action.Headers)
	if len(action.RedactedHeaders) > 0 {
		headers = cloneStringHeaders(action.RedactedHeaders)
	}
	body := string(action.Body)
	if len(action.RedactedBody) > 0 {
		body = string(action.RedactedBody)
	}
	return &protocol.ErrorSnapshot{
		RequestMethod:  action.Type,
		RequestURL:     requestURL,
		RequestHeaders: headers,
		RequestBody:    body,
		ResponseStatus: result.StatusCode,
		ResponseBody:   result.ResponseBody,
	}
}

func (w *WebSocket) resolveURL(target string) (string, error) {
	raw := strings.TrimSpace(target)
	if raw == "" {
		raw = strings.TrimSpace(w.baseURL)
	}
	if raw == "" {
		return "", fmt.Errorf("websocket target or base_url is required")
	}
	if isAbsoluteURL(raw) {
		return websocketScheme(raw)
	}
	if strings.TrimSpace(w.baseURL) == "" {
		return "", fmt.Errorf("websocket base_url is required for relative target %q", target)
	}
	base, err := websocketScheme(w.baseURL)
	if err != nil {
		return "", err
	}
	parsedBase, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	relative, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return parsedBase.ResolveReference(relative).String(), nil
}

func buildDefaultHeaders(config map[string]any) http.Header {
	headers := http.Header{}
	if raw, ok := config["default_headers"].(map[string]string); ok {
		for key, value := range raw {
			headers.Set(key, value)
		}
	}
	if userAgent, ok := stringConfig(config, "user_agent"); ok && headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", userAgent)
	}
	if auth := authHeader(config); auth != "" && headers.Get("Authorization") == "" {
		headers.Set("Authorization", auth)
	}
	return headers
}

func authHeader(config map[string]any) string {
	authType, _ := stringConfig(config, "auth_type")
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "basic":
		username, _ := stringConfig(config, "auth_username")
		password, _ := stringConfig(config, "auth_password")
		token := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		return "Basic " + token
	case "bearer":
		token, _ := stringConfig(config, "auth_token")
		if strings.TrimSpace(token) == "" {
			return ""
		}
		return "Bearer " + token
	default:
		return ""
	}
}

func cloneHeader(headers http.Header) http.Header {
	cloned := http.Header{}
	for key, values := range headers {
		for _, value := range values {
			cloned.Add(key, value)
		}
	}
	return cloned
}

func cloneStringHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func websocketScheme(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "ws", "wss":
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported websocket URL scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}

func isAbsoluteURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme != ""
}

func dependencyTarget(target string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func messageType(action *protocol.Action) (int, error) {
	value, _ := stringParam(action.Params, "type")
	switch strings.ToLower(value) {
	case "", "text":
		return gorillawebsocket.TextMessage, nil
	case "binary":
		return gorillawebsocket.BinaryMessage, nil
	default:
		return 0, fmt.Errorf("unsupported websocket message type %q", value)
	}
}

func responseBody(messageType int, message []byte) string {
	if messageType == gorillawebsocket.BinaryMessage {
		return base64.StdEncoding.EncodeToString(message)
	}
	return string(message)
}

func extract(ext protocol.Extractor, body []byte) (string, error) {
	if ext.Source != "" && ext.Source != "body" && ext.Source != "message" {
		return "", fmt.Errorf("unsupported websocket extraction source: %s", ext.Source)
	}
	switch ext.Type {
	case "json", "jsonpath":
		return extractJSONPath(ext.Expression, body)
	case "jmespath":
		return extractJMESPath(ext.Expression, body)
	case "regex":
		return extractRegex(ext.Expression, body)
	case "boundary":
		return extractBoundary(ext.Expression, body)
	default:
		return "", fmt.Errorf("unsupported websocket extraction type: %s", ext.Type)
	}
}

func extractJSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimPrefix(strings.TrimSpace(expr), "$.")
	if expr == "" {
		return "", fmt.Errorf("jsonpath expression is required")
	}
	parts := strings.Split(expr, ".")

	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON message: %w", err)
	}
	for _, part := range parts {
		idx := -1
		if bracket := strings.Index(part, "["); bracket != -1 {
			idxText := strings.TrimSuffix(part[bracket+1:], "]")
			if _, err := fmt.Sscanf(idxText, "%d", &idx); err != nil {
				return "", fmt.Errorf("invalid array index %q", idxText)
			}
			part = part[:bracket]
		}

		obj, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("expected object at %q", part)
		}
		current, ok = obj[part]
		if !ok {
			return "", fmt.Errorf("field %q not found", part)
		}
		if idx >= 0 {
			arr, ok := current.([]any)
			if !ok {
				return "", fmt.Errorf("expected array at %q", part)
			}
			if idx >= len(arr) {
				return "", fmt.Errorf("index %d out of range for %q", idx, part)
			}
			current = arr[idx]
		}
	}
	return stringifyExtraction(current)
}

func extractJMESPath(expr string, body []byte) (string, error) {
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", fmt.Errorf("invalid JSON message: %w", err)
	}
	result, err := jmespath.Search(expr, current)
	if err != nil {
		return "", fmt.Errorf("invalid JMESPath expression: %w", err)
	}
	if result == nil {
		return "", fmt.Errorf("no match for JMESPath %q", expr)
	}
	return stringifyExtraction(result)
}

func extractRegex(expr string, body []byte) (string, error) {
	matches, err := regexp.Compile(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	found := matches.FindSubmatch(body)
	if len(found) < 2 {
		return "", fmt.Errorf("no match for regex %q", expr)
	}
	return string(found[1]), nil
}

func extractBoundary(expr string, body []byte) (string, error) {
	parts := strings.SplitN(expr, "|||", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("boundary expression must use ||| separator: left|||right")
	}
	text := string(body)
	left := strings.Index(text, parts[0])
	if left < 0 {
		return "", fmt.Errorf("left boundary %q not found", parts[0])
	}
	start := left + len(parts[0])
	right := strings.Index(text[start:], parts[1])
	if right < 0 {
		return "", fmt.Errorf("right boundary %q not found", parts[1])
	}
	return text[start : start+right], nil
}

func stringifyExtraction(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []any, map[string]any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", fmt.Errorf("marshal extraction result: %w", err)
		}
		return string(encoded), nil
	default:
		return fmt.Sprintf("%v", typed), nil
	}
}

func evaluate(assertion protocol.Assertion, result *protocol.Result, body []byte) error {
	switch assertion.Type {
	case "message_received":
		if len(body) == 0 {
			return fmt.Errorf("expected websocket message")
		}
	case "body_contains", "message_contains":
		if !strings.Contains(string(body), assertion.Value) {
			return fmt.Errorf("websocket message does not contain %q", assertion.Value)
		}
	case "body_not_contains", "message_not_contains":
		if strings.Contains(string(body), assertion.Value) {
			return fmt.Errorf("websocket message contains %q (unexpected)", assertion.Value)
		}
	case "response_time_ms":
		if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
			return fmt.Errorf("response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max)
		}
	default:
		return fmt.Errorf("unsupported websocket assertion type: %s", assertion.Type)
	}
	return nil
}

func actionTimeout(action *protocol.Action, fallback time.Duration) time.Duration {
	if value, ok := intParamOK(action.Params, "timeout_ms"); ok {
		return time.Duration(value) * time.Millisecond
	}
	return fallback
}

func durationConfig(config map[string]any, key string, fallback time.Duration) time.Duration {
	value, ok := int64ConfigOK(config, key)
	if !ok || value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Millisecond
}

func int64Config(config map[string]any, key string, fallback int64) int64 {
	value, ok := int64ConfigOK(config, key)
	if !ok || value <= 0 {
		return fallback
	}
	return value
}

func int64ConfigOK(config map[string]any, key string) (int64, bool) {
	raw, ok := config[key]
	if !ok {
		return 0, false
	}
	switch typed := raw.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	default:
		return 0, false
	}
}

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := intParamOK(params, key)
	if !ok {
		return fallback
	}
	return value
}

func intParamOK(params map[string]any, key string) (int, bool) {
	raw, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := raw.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	default:
		return 0, false
	}
}

func stringConfig(config map[string]any, key string) (string, bool) {
	raw, ok := config[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func stringParam(params map[string]any, key string) (string, bool) {
	raw, ok := params[key]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func boolConfig(config map[string]any, key string) (bool, bool) {
	raw, ok := config[key]
	if !ok {
		return false, false
	}
	value, ok := raw.(bool)
	return value, ok
}

func boolConfigDefault(config map[string]any, key string, fallback bool) bool {
	value, ok := boolConfig(config, key)
	if !ok {
		return fallback
	}
	return value
}
