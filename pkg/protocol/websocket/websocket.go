package websocket

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract"
)

const (
	defaultReadTimeout    = 30 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	defaultMaxMessageSize = int64(1024 * 1024)
	// RFC 6455 control frames have a 125-byte payload ceiling. Reject an
	// oversized authored ping before touching connection state; it is a local
	// configuration error, not an unexpected disconnect eligible for reconnect.
	maxWebSocketControlPayloadBytes = 125
	// A close control frame reserves two payload bytes for the status code.
	maxWebSocketCloseReasonBytes = maxWebSocketControlPayloadBytes - 2
)

func init() {
	protocol.Register("websocket", func() protocol.Protocol {
		return &WebSocket{}
	})
}

type WebSocket struct {
	writeMu            sync.Mutex
	pendingWrites      atomic.Int64
	authoredPings      map[string]authoredPing
	pongRTTs           []float64
	callbacks          *callbackState
	callbackInterval   time.Duration
	baseURL            string
	allowedTargetHosts []string
	conn               *gorillawebsocket.Conn
	dialer             *gorillawebsocket.Dialer
	defaultHeaders     http.Header
	readTimeout        time.Duration
	writeTimeout       time.Duration
	maxMessageSize     int64
	origin             string
	reconnect          bool
	lastTarget         string
	lastHeaders        http.Header
	closedByAction     bool
	connectionEpoch    uint64
	mu                 sync.Mutex
}

func (w *WebSocket) Name() string { return "websocket" }

func (w *WebSocket) Init(_ context.Context, config map[string]any) error {
	w.callbackInterval = 500 * time.Millisecond
	if raw, ok := config["websocket_callback_interval_ms"]; ok {
		d, e := protocol.BoundedMilliseconds(raw, 500*time.Millisecond, time.Minute)
		if e != nil || d < time.Millisecond {
			return fmt.Errorf("websocket callback interval must be 1..60000 milliseconds")
		}
		w.callbackInterval = d
	}
	w.baseURL, _ = stringConfig(config, "base_url")
	w.allowedTargetHosts = protocol.TargetPolicyHosts(config)
	var err error
	w.readTimeout, err = protocol.BoundedMilliseconds(config["websocket_read_timeout_ms"], defaultReadTimeout, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return fmt.Errorf("websocket read_timeout_ms %w", err)
	}
	w.writeTimeout, err = protocol.BoundedMilliseconds(config["websocket_write_timeout_ms"], defaultWriteTimeout, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return fmt.Errorf("websocket write_timeout_ms %w", err)
	}
	w.maxMessageSize = int64Config(config, "websocket_max_message_size", defaultMaxMessageSize)
	if w.maxMessageSize > dsl.MaxWebSocketMessageBytes {
		return fmt.Errorf("websocket_max_message_size exceeds %d byte limit", dsl.MaxWebSocketMessageBytes)
	}
	w.origin, _ = stringConfig(config, "websocket_origin")
	w.reconnect = boolConfigDefault(config, "websocket_reconnect", true)

	subprotocol, _ := stringConfig(config, "websocket_subprotocol")
	compression, _ := boolConfig(config, "websocket_compression")
	w.dialer = &gorillawebsocket.Dialer{
		Proxy:             protocol.NewTargetPolicyProxy(http.ProxyFromEnvironment, w.allowedTargetHosts),
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
	if action == nil {
		return &protocol.Result{StartTime: time.Now(), ErrorMessage: "websocket action is required"}, nil
	}
	result := &protocol.Result{
		ActionName:     action.Name,
		StartTime:      time.Now(),
		Extractions:    make(map[string]string),
		DependencyType: "websocket",
	}

	var err error
	switch action.Type {
	case "ws_on_message":
		err = w.registerCallback(ctx)
	case "ws_dispatch":
	// Dispatch is owned by the VUser goroutine.
	case "ws_connect":
		err = w.connect(ctx, action, result)
	case "ws_send":
		err = w.send(ctx, action, result)
	case "ws_receive":
		err = w.receive(ctx, action, result)
	case "ws_ping":
		err = w.ping(ctx, action, result)
	case "ws_close":
		err = w.close(action, result)
	default:
		err = fmt.Errorf("unsupported websocket action type: %s", action.Type)
	}

	w.finishDiagnostics(ctx, action, result, err)
	result.Duration = time.Since(result.StartTime)
	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		result.Snapshot = w.buildSnapshot(action, result)
		result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
		return result, nil
	}
	result.Success = true
	result.ProtocolEvidence = protocol.BuildProtocolEvidence(action, result)
	return result, nil
}

func (w *WebSocket) Teardown(_ context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closedByAction = true
	w.stopCallbacksLocked()
	w.connectionEpoch++
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
	headerListNames := websocketHeaderListNameSet(action.HeaderList)
	for key, value := range action.Headers {
		if _, exists := headerListNames[strings.ToLower(strings.TrimSpace(key))]; exists {
			continue
		}
		headers.Set(key, value)
	}
	for name := range headerListNames {
		headers.Del(name)
	}
	for _, header := range action.HeaderList {
		if strings.TrimSpace(header.Name) != "" {
			headers.Add(header.Name, header.Value)
		}
	}
	if w.origin != "" && headers.Get("Origin") == "" {
		headers.Set("Origin", w.origin)
	}
	w.mu.Lock()
	// An explicit connect is a new connection intent. A later explicit close or
	// connect increments the epoch and prevents this in-flight dial from
	// replacing the newer state when the handshake completes.
	w.stopCallbacksLocked()
	w.connectionEpoch++
	connectionEpoch := w.connectionEpoch
	oldConn := w.conn
	w.conn = nil
	w.mu.Unlock()
	// Wake any cancelled callback reader even if the new dial fails.
	if oldConn != nil {
		_ = oldConn.Close()
	}

	dialStart := time.Now()
	conn, resp, err := w.dialer.DialContext(ctx, target, headers)
	wsDiagnostics(result).HandshakeMs = wsElapsed(dialStart)
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
	if w.connectionEpoch != connectionEpoch {
		w.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("websocket connection state changed while connecting")
	}
	if w.conn != nil {
		_ = w.conn.Close()
	}
	w.conn = conn
	w.authoredPings = nil
	w.pongRTTs = nil
	w.instrumentPongs(conn, connectionEpoch)
	w.lastTarget = target
	w.lastHeaders = cloneHeader(headers)
	w.closedByAction = false
	w.mu.Unlock()

	result.StatusCode = http.StatusSwitchingProtocols
	wsDiagnostics(result)
	result.ServerTelemetry.ConnectionReused = false
	return nil
}

func (w *WebSocket) send(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	messageType, err := messageType(action)
	if err != nil {
		return err
	}
	if len(action.Body) > dsl.MaxWebSocketMessageBytes {
		return fmt.Errorf("websocket message exceeds %d byte limit", dsl.MaxWebSocketMessageBytes)
	}
	conn, err := w.currentOrReconnect(ctx, result)
	if err != nil {
		return err
	}
	if err := w.measuredWrite(conn, messageType, action.Body, result); err != nil {
		if !w.reconnect {
			return err
		}
		conn, reconnectErr := w.reconnectConn(ctx, result)
		if reconnectErr != nil {
			return fmt.Errorf("%w; reconnect failed: %v", err, reconnectErr)
		}
		if retryErr := w.measuredWrite(conn, messageType, action.Body, result); retryErr != nil {
			return retryErr
		}
	}
	result.BytesSent = int64(len(action.Body))
	wsDiagnostics(result).MessagesSent = 1
	result.ServerTelemetry.MessagesSent = 1
	return nil
}

func (w *WebSocket) receive(ctx context.Context, action *protocol.Action, result *protocol.Result) error {
	w.mu.Lock()
	callbackMode := w.callbacks != nil
	w.mu.Unlock()
	if callbackMode {
		return fmt.Errorf("ws_receive cannot compete with a registered callback; use ws_dispatch")
	}

	conn, err := w.currentOrReconnect(ctx, result)
	if err != nil {
		return err
	}
	timeout, err := actionTimeout(action, w.readTimeout)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if timeout > 0 {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return err
		}
	}
	stopCancellationWakeup := context.AfterFunc(ctx, func() {
		// gorilla/websocket permits concurrent Close/SetReadDeadline with a
		// blocked reader. Advancing the deadline wakes ReadMessage promptly when
		// a run is cancelled even when the action timeout is much longer.
		_ = conn.SetReadDeadline(time.Now())
	})
	defer stopCancellationWakeup()
	readStart := time.Now()
	messageType, message, err := conn.ReadMessage()
	wsDiagnostics(result).ReceiveMs = wsElapsed(readStart)
	if err != nil {
		w.discardConn(conn)
		return err
	}
	result.BytesReceived = int64(len(message))
	result.ResponseBody = responseBody(messageType, message)
	d := wsDiagnostics(result)
	d.MessagesReceived = 1
	d.MessageType = wsMessageType(messageType)
	result.ServerTelemetry.MessagesReceived = 1

	if expected, ok := stringParam(action.Params, "match"); ok && !strings.Contains(result.ResponseBody, expected) {
		return fmt.Errorf("websocket message does not contain %q", expected)
	}
	for _, extractor := range action.Extractors {
		value, err := extract(extractor, []byte(result.ResponseBody))
		if err := protocol.ApplyExtraction(result, extractor, value, err); err != nil {
			result.Extractions = nil
			return fmt.Errorf("websocket extraction failed: %w", err)
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
	if len(action.Body) > maxWebSocketControlPayloadBytes {
		return fmt.Errorf("websocket ping payload exceeds %d byte limit", maxWebSocketControlPayloadBytes)
	}
	conn, err := w.currentOrReconnect(ctx, result)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(w.writeTimeout)
	wsDiagnostics(result).MessageType = "ping"
	w.noteAuthoredPing(action.Body)
	if err := conn.WriteControl(gorillawebsocket.PingMessage, action.Body, deadline); err != nil {
		w.discardAuthoredPing(action.Body)
		if !w.reconnect {
			return err
		}
		conn, reconnectErr := w.reconnectConn(ctx, result)
		if reconnectErr != nil {
			return fmt.Errorf("%w; reconnect failed: %v", err, reconnectErr)
		}
		deadline = time.Now().Add(w.writeTimeout)
		w.noteAuthoredPing(action.Body)
		if retryErr := conn.WriteControl(gorillawebsocket.PingMessage, action.Body, deadline); retryErr != nil {
			w.discardAuthoredPing(action.Body)
			return retryErr
		}
	}
	result.BytesSent = int64(len(action.Body))
	wsDiagnostics(result).MessagesSent = 1
	result.ServerTelemetry.MessagesSent = 1
	return nil
}

func (w *WebSocket) close(action *protocol.Action, result *protocol.Result) error {
	var params map[string]any
	if action != nil {
		params = action.Params
	}
	code, err := webSocketCloseCode(params)
	if err != nil {
		return err
	}
	reason, _ := stringParam(params, "reason")
	if !utf8.ValidString(reason) {
		return fmt.Errorf("websocket close reason must be valid UTF-8")
	}
	if len(reason) > maxWebSocketCloseReasonBytes {
		return fmt.Errorf("websocket close reason exceeds %d byte limit", maxWebSocketCloseReasonBytes)
	}
	msg := gorillawebsocket.FormatCloseMessage(code, reason)
	d := wsDiagnostics(result)
	d.MessageType = "close"
	c := int32(code)
	d.CloseCode = &c
	d.DisconnectReason = "normal"
	if code != gorillawebsocket.CloseNormalClosure {
		d.DisconnectReason = "local_close"
	}

	w.mu.Lock()
	conn := w.conn
	w.conn = nil
	w.closedByAction = true
	w.stopCallbacksLocked()
	w.connectionEpoch++
	w.mu.Unlock()
	if conn == nil {
		return nil
	}

	deadline := time.Now().Add(w.writeTimeout)
	writeErr := conn.WriteControl(gorillawebsocket.CloseMessage, msg, deadline)
	result.BytesSent = int64(len(msg))
	closeErr := conn.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func webSocketCloseCode(params map[string]any) (int, error) {
	raw, authored := params["code"]
	if !authored {
		return gorillawebsocket.CloseNormalClosure, nil
	}
	var code int64
	switch typed := raw.(type) {
	case int:
		code = int64(typed)
	case int64:
		code = typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return 0, fmt.Errorf("websocket close code must be an integer")
		}
		code = int64(typed)
	case float32:
		converted := float64(typed)
		if math.IsNaN(converted) || math.IsInf(converted, 0) || math.Trunc(converted) != converted {
			return 0, fmt.Errorf("websocket close code must be an integer")
		}
		code = int64(converted)
	default:
		return 0, fmt.Errorf("websocket close code must be an integer")
	}
	if !validWebSocketCloseCode(code) {
		return 0, fmt.Errorf("websocket close code %d is not a valid sendable status code", code)
	}
	return int(code), nil
}

// validWebSocketCloseCode mirrors Gorilla's receive-side status validation so
// this adapter never emits a close frame that the same transport would reject.
// Codes 1004-1006 and 1015 are reserved/non-wire sentinels; unregistered codes
// below 3000 are not application-defined.
func validWebSocketCloseCode(code int64) bool {
	switch code {
	case gorillawebsocket.CloseNormalClosure,
		gorillawebsocket.CloseGoingAway,
		gorillawebsocket.CloseProtocolError,
		gorillawebsocket.CloseUnsupportedData,
		gorillawebsocket.CloseInvalidFramePayloadData,
		gorillawebsocket.ClosePolicyViolation,
		gorillawebsocket.CloseMessageTooBig,
		gorillawebsocket.CloseMandatoryExtension,
		gorillawebsocket.CloseInternalServerErr,
		gorillawebsocket.CloseServiceRestart,
		gorillawebsocket.CloseTryAgainLater:
		return true
	default:
		return code >= 3000 && code <= 4999
	}
}

func (w *WebSocket) currentOrReconnect(ctx context.Context, result *protocol.Result) (*gorillawebsocket.Conn, error) {
	w.mu.Lock()
	conn := w.conn
	shouldReconnect := conn == nil && w.reconnect && !w.closedByAction && w.lastTarget != ""
	w.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	if shouldReconnect {
		return w.reconnectConn(ctx, result)
	}
	return nil, fmt.Errorf("websocket connection is not open")
}

func (w *WebSocket) writeMessage(conn *gorillawebsocket.Conn, messageType int, body []byte) error {
	if len(body) > dsl.MaxWebSocketMessageBytes {
		return fmt.Errorf("websocket message exceeds %d byte limit", dsl.MaxWebSocketMessageBytes)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(w.writeTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(messageType, body)
}

func (w *WebSocket) reconnectConn(ctx context.Context, result *protocol.Result) (*gorillawebsocket.Conn, error) {
	w.mu.Lock()
	if w.closedByAction {
		w.mu.Unlock()
		return nil, fmt.Errorf("websocket connection is not open")
	}
	w.stopCallbacksLocked()
	w.connectionEpoch++
	connectionEpoch := w.connectionEpoch
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

	dialStart := time.Now()
	conn, resp, err := w.dialer.DialContext(ctx, target, headers)
	d := wsDiagnostics(result)
	d.ReconnectMs = wsElapsed(dialStart)
	d.Reconnects++
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
	if w.closedByAction || w.connectionEpoch != connectionEpoch {
		w.mu.Unlock()
		_ = conn.Close()
		return nil, fmt.Errorf("websocket connection was closed while reconnecting")
	}
	if w.conn != nil {
		_ = w.conn.Close()
	}
	w.conn = conn
	w.authoredPings = nil
	w.pongRTTs = nil
	w.instrumentPongs(conn, connectionEpoch)
	w.closedByAction = false
	w.mu.Unlock()

	result.DependencyTarget = dependencyTarget(target)
	wsDiagnostics(result)
	result.ServerTelemetry.ConnectionReused = false
	return conn, nil
}

func (w *WebSocket) discardConn(conn *gorillawebsocket.Conn) {
	w.mu.Lock()
	if w.conn == conn {
		w.conn = nil
		w.closedByAction = false
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
		resolved, err := websocketScheme(raw)
		if err != nil {
			return "", err
		}
		if err := protocol.ValidateTargetURL(resolved, w.allowedTargetHosts); err != nil {
			return "", err
		}
		return resolved, nil
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
	resolved := parsedBase.ResolveReference(relative).String()
	if err := protocol.ValidateTargetURL(resolved, w.allowedTargetHosts); err != nil {
		return "", err
	}
	return resolved, nil
}

func websocketHeaderListNameSet(headers protocol.HeaderList) map[string]struct{} {
	if len(headers) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		if name != "" {
			names[name] = struct{}{}
		}
	}
	return names
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
	value, err := jsonextract.JSONPath(expr, body)
	if errors.Is(err, jsonextract.ErrNotFound) {
		return "", protocol.MarkExtractionMiss(err)
	}
	if err != nil {
		return "", fmt.Errorf("invalid JSONPath extraction: %w", err)
	}
	return value, nil
}

func extractJMESPath(expr string, body []byte) (string, error) {
	result, err := jsonextract.JMESPathValue(expr, body)
	var failure *jsonextract.JMESPathError
	if errors.As(err, &failure) {
		switch failure.Kind {
		case jsonextract.JMESPathInvalidJSON:
			return "", fmt.Errorf("invalid JSON message: %w", failure.Err)
		case jsonextract.JMESPathInvalidExpression:
			return "", fmt.Errorf("invalid JMESPath expression: %w", failure.Err)
		default:
			return "", fmt.Errorf("evaluate JMESPath expression: %w", failure.Err)
		}
	}
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", protocol.MarkExtractionMiss(fmt.Errorf("no match for JMESPath %q", expr))
	}
	return jsonextract.FormatValue(result), nil
}

func extractRegex(expr string, body []byte) (string, error) {
	matches, err := compileRegexCached(expr)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	found := matches.FindSubmatch(body)
	if len(found) == 0 {
		return "", protocol.MarkExtractionMiss(fmt.Errorf("no match for regex %q", expr))
	}
	if len(found) == 1 {
		return string(found[0]), nil
	}
	return string(found[1]), nil
}

var regexCache = boundedcache.NewStringCache[*regexp.Regexp](0, 0, 0)

func compileRegexCached(expr string) (*regexp.Regexp, error) {
	if cached, ok := regexCache.Load(expr); ok {
		return cached, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := regexCache.LoadOrStore(expr, re)
	return actual, nil
}

func extractBoundary(expr string, body []byte) (string, error) {
	parts := strings.SplitN(expr, "|||", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("boundary expression must use ||| separator: left|||right")
	}
	text := string(body)
	left := strings.Index(text, parts[0])
	if left < 0 {
		return "", protocol.MarkExtractionMiss(fmt.Errorf("left boundary %q not found", parts[0]))
	}
	start := left + len(parts[0])
	right := strings.Index(text[start:], parts[1])
	if right < 0 {
		return "", protocol.MarkExtractionMiss(fmt.Errorf("right boundary %q not found", parts[1]))
	}
	return text[start : start+right], nil
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

func actionTimeout(action *protocol.Action, fallback time.Duration) (time.Duration, error) {
	if action == nil || action.Params == nil {
		return fallback, nil
	}
	value, present := action.Params["timeout_ms"]
	if !present {
		return fallback, nil
	}
	timeout, err := protocol.BoundedMilliseconds(value, fallback, dsl.MaxConfiguredProtocolTimeout)
	if err != nil {
		return 0, fmt.Errorf("websocket action timeout_ms %w", err)
	}
	return timeout, nil
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
