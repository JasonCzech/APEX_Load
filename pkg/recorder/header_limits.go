package recorder

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var errRecorderHeadersTooLarge = errors.New("recorder HTTP headers exceed configured limit")

// recorderHeaderLimitReader enforces a byte budget only while net/http parses
// a request or response head. Once parsing succeeds it is disabled, preserving
// any body or WebSocket frame bytes already buffered by bufio and allowing the
// returned reader to continue on the same connection.
type recorderHeaderLimitReader struct {
	source    io.Reader
	remaining int64
	disabled  bool
}

func (r *recorderHeaderLimitReader) Read(p []byte) (int, error) {
	if r == nil || r.source == nil {
		return 0, io.EOF
	}
	if r.disabled {
		return r.source.Read(p)
	}
	if r.remaining <= 0 {
		return 0, errRecorderHeadersTooLarge
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.source.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func readRecorderHTTPRequest(source io.Reader) (*http.Request, *bufio.Reader, error) {
	return readRecorderHTTPRequestWithLimit(source, maxRecorderHeaderBytes)
}

func readRecorderHTTPRequestWithLimit(source io.Reader, limit int64) (*http.Request, *bufio.Reader, error) {
	if source == nil || limit <= 0 {
		return nil, nil, fmt.Errorf("read recorder request headers: invalid source or limit")
	}
	limited := &recorderHeaderLimitReader{source: source, remaining: limit}
	reader := bufio.NewReader(limited)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("read recorder request headers: %w", err)
	}
	limited.disabled = true
	return request, reader, nil
}

func readRecorderHTTPResponse(source io.Reader, request *http.Request) (*http.Response, *bufio.Reader, error) {
	return readRecorderHTTPResponseWithLimit(source, request, maxRecorderHeaderBytes)
}

func readRecorderHTTPResponseWithLimit(source io.Reader, request *http.Request, limit int64) (*http.Response, *bufio.Reader, error) {
	if source == nil || limit <= 0 {
		return nil, nil, fmt.Errorf("read recorder response headers: invalid source or limit")
	}
	limited := &recorderHeaderLimitReader{source: source, remaining: limit}
	reader := bufio.NewReader(limited)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, nil, fmt.Errorf("read recorder response headers: %w", err)
	}
	limited.disabled = true
	return response, reader, nil
}
