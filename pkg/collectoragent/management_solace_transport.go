package collectoragent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	solaceManagementHeaderLimit   = 32 << 10
	solaceManagementPollBodyLimit = 4 << 20
	solaceManagementChunkLimit    = 8192
)

type solaceManagementDialFunc func(context.Context, solaceManagementRoute) (net.Conn, error)

func dialSolaceManagementTCP(ctx context.Context, route solaceManagementRoute) (net.Conn, error) {
	var mu sync.Mutex
	attempted := false
	dialer := net.Dialer{KeepAlive: -1, ControlContext: func(_ context.Context, network, address string, _ syscall.RawConn) error {
		mu.Lock()
		defer mu.Unlock()
		if attempted || network != route.network || address != route.address.String() {
			return &managementObservationError{reason: "permission_denied"}
		}
		attempted = true
		return nil
	}}
	dialer.SetMultipathTCP(false)
	return dialer.DialTCP(ctx, route.network, netip.AddrPort{}, route.address)
}

// An unexplained first raw Close retains ownership and forbids any later
// admission. Its error text never includes transport/provider credentials.
type solaceManagementUnprovenCloseError struct{}

func (*solaceManagementUnprovenCloseError) Error() string {
	return "management transport destruction remains unproven"
}

type solaceManagementPhysicalClose interface{ physicalClosed() bool }
type solaceManagementConnection struct {
	raw          net.Conn
	tls          *tls.Conn
	release      func()
	ioMu         sync.Mutex
	closeMu      sync.Mutex
	closeStarted bool
	closeDone    chan struct{}
	closeErr     error
	closeProven  bool
}

func (c *solaceManagementConnection) close() error {
	c.closeMu.Lock()
	if c.closeStarted {
		done := c.closeDone
		c.closeMu.Unlock()
		<-done
		c.closeMu.Lock()
		err := c.closeErr
		c.closeMu.Unlock()
		return err
	}
	c.closeStarted = true
	c.closeDone = make(chan struct{})
	done := c.closeDone
	c.closeMu.Unlock()
	err := c.raw.Close() // Raw close avoids an uncounted TLS close-notify write.
	proven := err == nil
	if proof, ok := c.raw.(solaceManagementPhysicalClose); ok {
		proven = proven || proof.physicalClosed()
	}
	c.closeMu.Lock()
	if !proven {
		err = &solaceManagementUnprovenCloseError{}
	}
	c.closeErr, c.closeProven = err, proven
	c.closeMu.Unlock()
	if proven && c.release != nil {
		c.release()
	}
	close(done)
	return err
}
func (c *solaceManagementConnection) withIO(ctx context.Context, call func(context.Context) error) (err error) {
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	c.closeMu.Lock()
	closed := c.closeStarted
	c.closeMu.Unlock()
	if closed {
		return &managementObservationError{reason: "cancelled"}
	}
	if err = solaceManagementContextError(ctx); err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return &managementObservationError{reason: "invalid"}
	}
	if err = c.raw.SetDeadline(deadline); err != nil {
		_ = c.close()
		return err
	}
	stopped, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			_ = c.close()
		case <-stopped:
		}
	}()
	defer func() {
		close(stopped)
		<-joined
		if recovered := recover(); recovered != nil {
			_ = c.close()
			panic(recovered)
		}
		if contextErr := solaceManagementContextError(ctx); contextErr != nil {
			err = contextErr
		}
		if err != nil {
			_ = c.close()
		}
	}()
	return call(context.WithoutCancel(ctx))
}

func solaceManagementContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return nil
}

// open admits TCP and TLS independently. Retained ownership is registered
// before socket acquisition; no DNS, address-list fallback or detached work is
// possible in the typed static-IP production dial.
func openSolaceManagementConnection(ctx context.Context, poll *managementObservation, route solaceManagementRoute, dial solaceManagementDialFunc) (connection *solaceManagementConnection, err error) {
	if dial == nil {
		dial = dialSolaceManagementTCP
	}
	defer func() {
		if err != nil && connection != nil {
			_ = connection.close()
		}
	}()
	err = poll.request(ctx, route.origin, false, func(opCtx context.Context) error {
		release, retainErr := poll.retainResource()
		if retainErr != nil {
			return retainErr
		}
		raw, dialErr := dial(opCtx, route)
		if raw == nil {
			release()
		} else {
			connection = &solaceManagementConnection{raw: raw, release: release}
		}
		if dialErr != nil {
			return dialErr
		}
		if raw == nil {
			return &managementObservationError{reason: "invalid"}
		}
		return nil
	})
	if err != nil {
		return connection, err
	}
	connection.tls = tls.Client(connection.raw, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: poll.target.solaceApproval.roots.Clone(), ServerName: route.serverName, NextProtos: []string{"http/1.1"}})
	err = poll.request(ctx, route.origin, false, func(opCtx context.Context) error {
		return connection.withIO(opCtx, func(detached context.Context) error { return connection.tls.HandshakeContext(detached) })
	})
	return connection, err
}

// The caller checks route identity before acquisition. The wire surface has
// only individual GET and the explicitly approved own-session logout PUT.
func validateSolaceManagementRequest(route solaceManagementRoute, logout bool, authentication []byte) error {
	basic, cookie := bytes.HasPrefix(authentication, []byte("Basic ")), bytes.HasPrefix(authentication, []byte("Session="))
	if len(authentication) > 16<<10 || cookie && len(authentication) > 4096 || bytes.ContainsAny(authentication, "\r\n") || strings.ContainsAny(route.requestPath, "\r\n ") || strings.ContainsAny(route.serverName, "\r\n ") {
		return &managementObservationError{reason: "invalid"}
	}
	if logout {
		if route.requestPath != "/SEMP/v2/action/about/user/logout" || !cookie {
			return &managementObservationError{reason: "permission_denied"}
		}
	} else if !strings.HasPrefix(route.requestPath, "/SEMP/v2/monitor/msgVpns/") || !basic && !cookie {
		return &managementObservationError{reason: "permission_denied"}
	}
	return nil
}
func writeSolaceManagementRequest(writer io.Writer, route solaceManagementRoute, logout bool, authentication []byte) error {
	if err := validateSolaceManagementRequest(route, logout, authentication); err != nil {
		return err
	}
	method, body, credentialHeader := "GET", "", "Authorization: "
	if logout {
		method, body, credentialHeader = "PUT", "{}", "Cookie: "
	} else if bytes.HasPrefix(authentication, []byte("Session=")) {
		credentialHeader = "Cookie: "
	}
	request := []byte(method + " " + route.requestPath + " HTTP/1.1\r\nHost: " + solaceManagementHostHeader(route) + "\r\nAccept: application/json\r\n" + credentialHeader)
	request = append(request, authentication...)
	request = append(request, []byte("\r\nConnection: close\r\n")...)
	if logout {
		request = append(request, []byte("Content-Type: application/json\r\nContent-Length: 2\r\n")...)
	}
	request = append(request, []byte("\r\n"+body)...)
	defer clear(request)
	for len(request) > 0 {
		n, err := writer.Write(request)
		if n < 0 || n > len(request) {
			return io.ErrShortWrite
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		request = request[n:]
	}
	return nil
}
func solaceManagementHostHeader(route solaceManagementRoute) string {
	// Explicit nondefault ports remain part of the approved HTTP authority.
	if route.address.Port() == 443 {
		return route.serverName
	}
	return net.JoinHostPort(route.serverName, strconv.Itoa(int(route.address.Port())))
}

type solaceManagementHTTPResponse struct {
	// Keep the local complete-read boundary before physical cleanup/decoding.
	receivedAt       time.Time
	status           int
	body             []byte
	session          []byte
	sessionUncertain bool
}

func solaceManagementHTTPError(reason string) error {
	return &managementObservationError{reason: reason}
}

// Each response is read synchronously on its exclusively owned connection.
// A bounded manual grammar avoids automatic GET retries, redirects, HTTP/2,
// decompression, environment proxies and background transport workers.
func readSolaceManagementHTTPResponse(ctx context.Context, reader io.Reader, bodyLimit int, route solaceManagementRoute, logout bool) (response solaceManagementHTTPResponse, err error) {
	if bodyLimit < 0 || bodyLimit > solaceManagementBodyLimit {
		return response, solaceManagementHTTPError("budget_exhausted")
	}
	buffered := bufio.NewReaderSize(reader, 4096)
	headerBytes := 0
	statusLine, err := readSolaceManagementLine(ctx, buffered, &headerBytes, solaceManagementHeaderLimit)
	if err != nil {
		return response, err
	}
	if len(statusLine) < 12 || !strings.HasPrefix(statusLine, "HTTP/1.1 ") || len(statusLine) > 12 && statusLine[12] != ' ' {
		return response, solaceManagementHTTPError("invalid")
	}
	statusText := statusLine[9:12]
	for _, value := range statusText {
		if value < '0' || value > '9' {
			return response, solaceManagementHTTPError("invalid")
		}
	}
	response.status, _ = strconv.Atoi(statusText)
	if response.status < 200 || response.status > 599 {
		return response, solaceManagementHTTPError("unsupported")
	}
	values := map[string]string{}
	seen := map[string]bool{}
	for {
		line, lineErr := readSolaceManagementLine(ctx, buffered, &headerBytes, solaceManagementHeaderLimit)
		if lineErr != nil {
			return response, lineErr
		}
		if line == "" {
			break
		}
		key, value, parseErr := parseSolaceManagementHeader(line)
		if parseErr != nil {
			return response, parseErr
		}
		if key == "set-cookie" {
			cookie, cookieErr := solaceManagementSessionCookie(value, route, logout)
			if cookieErr != nil {
				response.sessionUncertain = true
				return response, cookieErr
			}
			if cookie != nil {
				if response.session != nil {
					clear(cookie)
					response.sessionUncertain = true
					return response, solaceManagementHTTPError("invalid")
				}
				response.session = cookie
			}
		}
		// Unrelated Set-Cookie fields are discarded. Relevant framing/content
		// headers must be unambiguous even when duplicate values are identical.
		relevant := key == "content-length" || key == "transfer-encoding" || key == "content-encoding" || key == "content-type"
		if relevant && seen[key] {
			return response, solaceManagementHTTPError("invalid")
		}
		if relevant {
			seen[key] = true
			values[key] = value
		}
	}
	// Parse error envelopes only within the same bound; original bodies are
	// never retained or logged. Redirects are not followed or authenticated.
	if encoding := values["content-encoding"]; encoding != "" && !strings.EqualFold(encoding, "identity") {
		return response, solaceManagementHTTPError("unsupported")
	}
	if contentType := values["content-type"]; contentType != "" {
		media, parameters, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil || media != "application/json" {
			return response, solaceManagementHTTPError("unsupported")
		}
		if charset := parameters["charset"]; charset != "" && !strings.EqualFold(charset, "utf-8") {
			return response, solaceManagementHTTPError("unsupported")
		}
	}
	transfer, length := values["transfer-encoding"], values["content-length"]
	if seen["transfer-encoding"] && transfer == "" || seen["content-length"] && length == "" || seen["transfer-encoding"] && seen["content-length"] {
		return response, solaceManagementHTTPError("invalid")
	}
	switch {
	case transfer != "":
		if !strings.EqualFold(transfer, "chunked") {
			return response, solaceManagementHTTPError("unsupported")
		}
		response.body, err = readSolaceManagementChunked(ctx, buffered, bodyLimit, &headerBytes)
	case length != "":
		for _, value := range length {
			if value < '0' || value > '9' {
				return response, solaceManagementHTTPError("invalid")
			}
		}
		count, parseErr := strconv.ParseUint(length, 10, 63)
		if parseErr != nil || count > uint64(bodyLimit) {
			return response, solaceManagementHTTPError("budget_exhausted")
		}
		response.body = make([]byte, int(count))
		n, readErr := io.ReadFull(buffered, response.body)
		if readErr != nil {
			response.body = response.body[:n]
			err = readErr
		}
	default:
		response.body, err = io.ReadAll(io.LimitReader(buffered, int64(bodyLimit)+1))
		if len(response.body) > bodyLimit {
			err = solaceManagementHTTPError("budget_exhausted")
		}
	}
	if ctx.Err() != nil {
		return response, ctx.Err()
	}
	return response, err
}
func readSolaceManagementLine(ctx context.Context, reader *bufio.Reader, used *int, limit int) (string, error) {
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > limit-*used {
			return "", solaceManagementHTTPError("budget_exhausted")
		}
		*used += len(fragment)
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(line) < 2 || line[len(line)-2] != '\r' {
			return "", solaceManagementHTTPError("invalid")
		}
		for _, value := range line[:len(line)-2] {
			if value < 0x20 && value != '\t' || value == 0x7f {
				return "", solaceManagementHTTPError("invalid")
			}
		}
		return string(line[:len(line)-2]), nil
	}
}
func parseSolaceManagementHeader(line string) (string, string, error) {
	at := strings.IndexByte(line, ':')
	if at < 1 || at > 256 || line[0] == ' ' || line[0] == '\t' {
		return "", "", solaceManagementHTTPError("invalid")
	}
	key := strings.ToLower(line[:at])
	for _, value := range key {
		if !(value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", value)) {
			return "", "", solaceManagementHTTPError("invalid")
		}
	}
	return key, strings.TrimSpace(line[at+1:]), nil
}
func readSolaceManagementChunked(ctx context.Context, reader *bufio.Reader, limit int, headerBytes *int) (body []byte, err error) {
	framingBytes := 0
	for chunk := 0; chunk < solaceManagementChunkLimit; chunk++ {
		line, lineErr := readSolaceManagementLine(ctx, reader, &framingBytes, solaceManagementHeaderLimit)
		if lineErr != nil {
			return body, lineErr
		}
		sizeText := strings.SplitN(line, ";", 2)[0]
		if len(sizeText) < 1 || len(sizeText) > 16 {
			return body, solaceManagementHTTPError("invalid")
		}
		for _, value := range sizeText {
			if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F') {
				return body, solaceManagementHTTPError("invalid")
			}
		}
		size, parseErr := strconv.ParseUint(sizeText, 16, 63)
		if parseErr != nil || size > uint64(limit-len(body)) {
			return body, solaceManagementHTTPError("budget_exhausted")
		}
		if size == 0 {
			for {
				trailer, trailerErr := readSolaceManagementLine(ctx, reader, headerBytes, solaceManagementHeaderLimit)
				if trailerErr != nil {
					return body, trailerErr
				}
				if trailer == "" {
					return body, nil
				}
				key, _, parseErr := parseSolaceManagementHeader(trailer)
				if parseErr != nil {
					return body, parseErr
				}
				if key == "content-length" || key == "transfer-encoding" || key == "authorization" || key == "content-encoding" || key == "content-type" || key == "set-cookie" || key == "cookie" {
					return body, solaceManagementHTTPError("invalid")
				}
			}
		}
		previous := len(body)
		body = append(body, make([]byte, int(size))...)
		n, readErr := io.ReadFull(reader, body[previous:])
		if readErr != nil {
			return body[:previous+n], readErr
		}
		var ending [2]byte
		if _, readErr := io.ReadFull(reader, ending[:]); readErr != nil {
			return body, readErr
		}
		if ending != [2]byte{'\r', '\n'} {
			return body, solaceManagementHTTPError("invalid")
		}
	}
	return body, solaceManagementHTTPError("budget_exhausted")
}

// Pure bounded cookie parsing uses no cookie jar, network or origin inference.
// Only the compiled Session name is retained; all other cookie metadata dies.
func solaceManagementSessionCookie(header string, route solaceManagementRoute, logout bool) ([]byte, error) {
	if len(header) > 4096 {
		return nil, solaceManagementHTTPError("budget_exhausted")
	}
	cookie, err := http.ParseSetCookie(header)
	if err != nil {
		return nil, solaceManagementHTTPError("invalid")
	}
	if cookie.Name != "Session" {
		return nil, nil
	}
	// A returned token can only be used on the exact approved TLS origin.
	// Explicit cookie scope must cover both the entity and own-session logout.
	if cookie.Domain != "" && !strings.EqualFold(strings.TrimPrefix(cookie.Domain, "."), route.serverName) {
		return nil, solaceManagementHTTPError("permission_denied")
	}
	if cookie.Path != "" && (!solaceManagementCookiePath(cookie.Path, strings.SplitN(route.requestPath, "?", 2)[0]) || !solaceManagementCookiePath(cookie.Path, "/SEMP/v2/action/about/user/logout")) {
		return nil, solaceManagementHTTPError("permission_denied")
	}
	deleted := cookie.MaxAge < 0 || !cookie.Expires.IsZero() && !cookie.Expires.After(time.Now())
	if logout {
		// Successful fixed logout may delete the existing Session cookie; a
		// fresh token is not logout proof and cannot be silently discarded.
		if deleted || cookie.Value == "" {
			return nil, nil
		}
		return nil, solaceManagementHTTPError("invalid")
	}
	if cookie.Value == "" || len(cookie.Value) > 4000 || deleted {
		return nil, solaceManagementHTTPError("invalid")
	}
	for _, value := range []byte(cookie.Value) {
		if value < 0x21 || value > 0x7e || value == '"' || value == ',' || value == ';' || value == '\\' {
			return nil, solaceManagementHTTPError("invalid")
		}
	}
	return []byte("Session=" + cookie.Value), nil
}
func solaceManagementCookiePath(scope, path string) bool {
	return strings.HasPrefix(scope, "/") && (scope == path || strings.HasPrefix(path, scope) && (strings.HasSuffix(scope, "/") || len(path) > len(scope) && path[len(scope)] == '/'))
}
