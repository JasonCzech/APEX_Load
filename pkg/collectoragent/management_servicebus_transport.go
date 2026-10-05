package collectoragent

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	serviceBusManagementHeaderLimit   = 32 << 10
	serviceBusManagementBodyLimit     = 256 << 10
	serviceBusManagementPollBodyLimit = 8 << 20
	serviceBusManagementChunkLimit    = 8192
)

type serviceBusManagementDialFunc func(context.Context, serviceBusManagementRoute) (net.Conn, error)

func dialServiceBusManagementTCP(ctx context.Context, route serviceBusManagementRoute) (net.Conn, error) {
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
type serviceBusManagementUnprovenCloseError struct{}

func (*serviceBusManagementUnprovenCloseError) Error() string {
	return "management transport destruction remains unproven"
}

type serviceBusManagementPhysicalClose interface{ physicalClosed() bool }
type serviceBusManagementConnection struct {
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

func (c *serviceBusManagementConnection) close() error {
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
	if proof, ok := c.raw.(serviceBusManagementPhysicalClose); ok {
		proven = proven || proof.physicalClosed()
	}
	c.closeMu.Lock()
	if !proven {
		err = &serviceBusManagementUnprovenCloseError{}
	}
	c.closeErr, c.closeProven = err, proven
	c.closeMu.Unlock()
	if proven && c.release != nil {
		c.release()
	}
	close(done)
	return err
}
func (c *serviceBusManagementConnection) withIO(ctx context.Context, call func(context.Context) error) (err error) {
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	c.closeMu.Lock()
	closed := c.closeStarted
	c.closeMu.Unlock()
	if closed {
		return &managementObservationError{reason: "cancelled"}
	}
	if err = managementContextError(ctx); err != nil {
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
		if contextErr := managementContextError(ctx); contextErr != nil {
			err = contextErr
		}
		if err != nil {
			_ = c.close()
		}
	}()
	return call(context.WithoutCancel(ctx))
}

// open admits TCP and TLS independently. Retained ownership is registered
// before socket acquisition; no DNS, address-list fallback or detached work is
// possible in the typed static-IP production dial.
func openServiceBusManagementConnection(ctx context.Context, poll *managementObservation, route serviceBusManagementRoute, dial serviceBusManagementDialFunc) (connection *serviceBusManagementConnection, err error) {
	if dial == nil {
		dial = dialServiceBusManagementTCP
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
			connection = &serviceBusManagementConnection{raw: raw, release: release}
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
	connection.tls = tls.Client(connection.raw, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: poll.target.serviceBusApproval.roots.Clone(), ServerName: route.serverName, NextProtos: []string{"http/1.1"}})
	err = poll.request(ctx, route.origin, false, func(opCtx context.Context) error {
		return connection.withIO(opCtx, func(detached context.Context) error { return connection.tls.HandshakeContext(detached) })
	})
	return connection, err
}

func writeServiceBusManagementGET(writer io.Writer, route serviceBusManagementRoute, authorization string) error {
	if len(authorization) > 16<<10 || strings.ContainsAny(authorization, "\r\n") || strings.ContainsAny(route.requestPath, "\r\n ") || strings.ContainsAny(route.serverName, "\r\n ") {
		return &managementObservationError{reason: "invalid"}
	}
	// All fields derive from immutable local routes or the bounded SAS helper.
	// There is no caller-selected method, header map, body or relative URL.
	request := []byte("GET " + route.requestPath + " HTTP/1.1\r\nHost: " + route.serverName + "\r\nAccept: application/atom+xml\r\nAuthorization: " + authorization + "\r\nConnection: close\r\n\r\n")
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

type serviceBusManagementHTTPResponse struct {
	status int
	body   []byte
}

func serviceBusManagementHTTPError(reason string) error {
	return &managementObservationError{reason: reason}
}

// Each response is read synchronously on its exclusively owned connection.
// A bounded manual grammar avoids automatic GET retries, redirects, HTTP/2,
// decompression, environment proxies and background transport workers.
func readServiceBusManagementHTTPResponse(ctx context.Context, reader io.Reader, bodyLimit int) (response serviceBusManagementHTTPResponse, err error) {
	if bodyLimit < 0 || bodyLimit > serviceBusManagementBodyLimit {
		return response, serviceBusManagementHTTPError("budget_exhausted")
	}
	buffered := bufio.NewReaderSize(reader, 4096)
	headerBytes := 0
	statusLine, err := readServiceBusManagementLine(ctx, buffered, &headerBytes, serviceBusManagementHeaderLimit)
	if err != nil {
		return response, err
	}
	if len(statusLine) < 12 || !strings.HasPrefix(statusLine, "HTTP/1.1 ") || len(statusLine) > 12 && statusLine[12] != ' ' {
		return response, serviceBusManagementHTTPError("invalid")
	}
	statusText := statusLine[9:12]
	for _, value := range statusText {
		if value < '0' || value > '9' {
			return response, serviceBusManagementHTTPError("invalid")
		}
	}
	response.status, _ = strconv.Atoi(statusText)
	if response.status < 200 || response.status > 599 {
		return response, serviceBusManagementHTTPError("unsupported")
	}
	values := map[string]string{}
	seen := map[string]bool{}
	for {
		line, lineErr := readServiceBusManagementLine(ctx, buffered, &headerBytes, serviceBusManagementHeaderLimit)
		if lineErr != nil {
			return response, lineErr
		}
		if line == "" {
			break
		}
		key, value, parseErr := parseServiceBusManagementHeader(line)
		if parseErr != nil {
			return response, parseErr
		}
		// Repeated Set-Cookie fields are ignored. Relevant framing and content
		// headers must be unambiguous even when duplicate values are identical.
		relevant := key == "content-length" || key == "transfer-encoding" || key == "content-encoding" || key == "content-type"
		if relevant && seen[key] {
			return response, serviceBusManagementHTTPError("invalid")
		}
		if relevant {
			seen[key] = true
			values[key] = value
		}
	}
	// Error/redirect descriptions can contain credentials. Close without drain;
	// never follow Location or expose a provider body/error string.
	if response.status != 200 {
		return response, nil
	}
	if encoding := values["content-encoding"]; encoding != "" && !strings.EqualFold(encoding, "identity") {
		return response, serviceBusManagementHTTPError("unsupported")
	}
	if contentType := values["content-type"]; contentType != "" {
		media, parameters, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil || media != "application/atom+xml" && media != "application/xml" && media != "text/xml" {
			return response, serviceBusManagementHTTPError("unsupported")
		}
		if charset := parameters["charset"]; charset != "" && !strings.EqualFold(charset, "utf-8") {
			return response, serviceBusManagementHTTPError("unsupported")
		}
		if kind := parameters["type"]; kind != "" && kind != "entry" && kind != "feed" {
			return response, serviceBusManagementHTTPError("unsupported")
		}
	}
	transfer, length := values["transfer-encoding"], values["content-length"]
	if seen["transfer-encoding"] && transfer == "" || seen["content-length"] && length == "" || seen["transfer-encoding"] && seen["content-length"] {
		return response, serviceBusManagementHTTPError("invalid")
	}
	switch {
	case transfer != "":
		if !strings.EqualFold(transfer, "chunked") {
			return response, serviceBusManagementHTTPError("unsupported")
		}
		response.body, err = readServiceBusManagementChunked(ctx, buffered, bodyLimit, &headerBytes)
	case length != "":
		for _, value := range length {
			if value < '0' || value > '9' {
				return response, serviceBusManagementHTTPError("invalid")
			}
		}
		count, parseErr := strconv.ParseUint(length, 10, 63)
		if parseErr != nil || count > uint64(bodyLimit) {
			return response, serviceBusManagementHTTPError("budget_exhausted")
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
			err = serviceBusManagementHTTPError("budget_exhausted")
		}
	}
	if ctx.Err() != nil {
		return response, ctx.Err()
	}
	return response, err
}
func readServiceBusManagementLine(ctx context.Context, reader *bufio.Reader, used *int, limit int) (string, error) {
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > limit-*used {
			return "", serviceBusManagementHTTPError("budget_exhausted")
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
			return "", serviceBusManagementHTTPError("invalid")
		}
		for _, value := range line[:len(line)-2] {
			if value < 0x20 && value != '\t' || value == 0x7f {
				return "", serviceBusManagementHTTPError("invalid")
			}
		}
		return string(line[:len(line)-2]), nil
	}
}
func parseServiceBusManagementHeader(line string) (string, string, error) {
	at := strings.IndexByte(line, ':')
	if at < 1 || at > 256 || line[0] == ' ' || line[0] == '\t' {
		return "", "", serviceBusManagementHTTPError("invalid")
	}
	key := strings.ToLower(line[:at])
	for _, value := range key {
		if !(value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", value)) {
			return "", "", serviceBusManagementHTTPError("invalid")
		}
	}
	return key, strings.TrimSpace(line[at+1:]), nil
}
func readServiceBusManagementChunked(ctx context.Context, reader *bufio.Reader, limit int, headerBytes *int) (body []byte, err error) {
	framingBytes := 0
	for chunk := 0; chunk < serviceBusManagementChunkLimit; chunk++ {
		line, lineErr := readServiceBusManagementLine(ctx, reader, &framingBytes, serviceBusManagementHeaderLimit)
		if lineErr != nil {
			return body, lineErr
		}
		sizeText := strings.SplitN(line, ";", 2)[0]
		if len(sizeText) < 1 || len(sizeText) > 16 {
			return body, serviceBusManagementHTTPError("invalid")
		}
		for _, value := range sizeText {
			if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F') {
				return body, serviceBusManagementHTTPError("invalid")
			}
		}
		size, parseErr := strconv.ParseUint(sizeText, 16, 63)
		if parseErr != nil || size > uint64(limit-len(body)) {
			return body, serviceBusManagementHTTPError("budget_exhausted")
		}
		if size == 0 {
			for {
				trailer, trailerErr := readServiceBusManagementLine(ctx, reader, headerBytes, serviceBusManagementHeaderLimit)
				if trailerErr != nil {
					return body, trailerErr
				}
				if trailer == "" {
					return body, nil
				}
				key, _, parseErr := parseServiceBusManagementHeader(trailer)
				if parseErr != nil {
					return body, parseErr
				}
				if key == "content-length" || key == "transfer-encoding" || key == "authorization" || key == "content-encoding" || key == "content-type" {
					return body, serviceBusManagementHTTPError("invalid")
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
			return body, serviceBusManagementHTTPError("invalid")
		}
	}
	return body, serviceBusManagementHTTPError("budget_exhausted")
}
