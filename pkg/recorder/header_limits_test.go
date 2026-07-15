package recorder

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReadRecorderHTTPRequestWithLimitPreservesBufferedBody(t *testing.T) {
	raw := "POST /upload HTTP/1.1\r\nHost: example.test\r\nContent-Length: 5\r\n\r\nhelloNEXT"
	request, reader, err := readRecorderHTTPRequestWithLimit(strings.NewReader(raw), 128)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("request body = %q, want hello", body)
	}
	next, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read bytes after body: %v", err)
	}
	if string(next) != "NEXT" {
		t.Fatalf("bytes after body = %q, want NEXT", next)
	}
}

func TestReadRecorderHTTPRequestWithLimitRejectsOversizedHeaders(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.test\r\nX-Fill: " + strings.Repeat("x", 256) + "\r\n\r\n"
	_, _, err := readRecorderHTTPRequestWithLimit(strings.NewReader(raw), 64)
	if !errors.Is(err, errRecorderHeadersTooLarge) {
		t.Fatalf("read request error = %v, want header limit", err)
	}
}

func TestReadRecorderHTTPResponseWithLimitPreservesBufferedFrames(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "http://example.test/socket", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	raw := "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\nFRAME"
	response, reader, err := readRecorderHTTPResponseWithLimit(strings.NewReader(raw), request, 128)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("response status = %d, want 101", response.StatusCode)
	}
	frame, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read buffered frame: %v", err)
	}
	if string(frame) != "FRAME" {
		t.Fatalf("buffered frame = %q, want FRAME", frame)
	}
}

func TestReadRecorderHTTPResponseWithLimitRejectsOversizedHeaders(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "http://example.test/socket", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	raw := "HTTP/1.1 101 Switching Protocols\r\nX-Fill: " + strings.Repeat("x", 256) + "\r\n\r\n"
	_, _, err = readRecorderHTTPResponseWithLimit(strings.NewReader(raw), request, 64)
	if !errors.Is(err, errRecorderHeadersTooLarge) {
		t.Fatalf("read response error = %v, want header limit", err)
	}
}
