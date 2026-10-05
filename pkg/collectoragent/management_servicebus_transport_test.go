package collectoragent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestServiceBusManagementBoundedHTTPFraming(t *testing.T) {
	tests := []struct {
		name, response string
		limit          int
		reason         string
		status         int
		body           string
	}{
		{"content length", "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nContent-Type: application/atom+xml;type=entry;charset=utf-8\r\n\r\nxml", 3, "", 200, "xml"},
		{"chunked", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1\r\nx\r\n2;bounded=value\r\nml\r\n0\r\nX-Observed: ignored\r\n\r\n", 3, "", 200, "xml"},
		{"EOF body", "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\nxml", 3, "", 200, "xml"},
		{"zero length", "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n", 3, "", 200, ""},
		{"empty reason", "HTTP/1.1 200\r\nContent-Length: 0\r\n\r\n", 3, "", 200, ""},
		{"redirect not followed", "HTTP/1.1 302 Found\r\nLocation: https://unapproved.test/private\r\nContent-Length: 1000000000\r\n\r\n", 3, "", 302, ""},
		{"framing collision", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 3\r\n\r\n", 3, "invalid", 200, ""},
		{"empty length", "HTTP/1.1 200 OK\r\nContent-Length: \r\n\r\nxml", 3, "invalid", 200, ""},
		{"empty transfer", "HTTP/1.1 200 OK\r\nTransfer-Encoding: \r\n\r\nxml", 3, "invalid", 200, ""},
		{"empty transfer collision", "HTTP/1.1 200 OK\r\nTransfer-Encoding: \r\nContent-Length: 3\r\n\r\nxml", 3, "invalid", 200, ""},
		{"duplicate length", "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nContent-Length: 3\r\n\r\nxml", 3, "invalid", 200, ""},
		{"duplicate type", "HTTP/1.1 200 OK\r\nContent-Type: application/xml\r\nContent-Type: text/xml\r\n\r\nxml", 3, "invalid", 200, ""},
		{"folded header", "HTTP/1.1 200 OK\r\n Content-Length: 3\r\n\r\nxml", 3, "invalid", 200, ""},
		{"invalid newline", "HTTP/1.1 200 OK\nContent-Length: 3\n\nxml", 3, "invalid", 0, ""},
		{"unsolicited continuation", "HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\n\r\nxml", 3, "unsupported", 100, ""},
		{"overflow length", "HTTP/1.1 200 OK\r\nContent-Length: 9223372036854775808\r\n\r\n", 3, "budget_exhausted", 200, ""},
		{"body exceeds cap", "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nxmlx", 3, "budget_exhausted", 200, ""},
		{"negative length", "HTTP/1.1 200 OK\r\nContent-Length: -1\r\n\r\n", 3, "invalid", 200, ""},
		{"multiple coding", "HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip, chunked\r\n\r\n", 3, "unsupported", 200, ""},
		{"compressed body", "HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\n\r\n", 3, "unsupported", 200, ""},
		{"foreign charset", "HTTP/1.1 200 OK\r\nContent-Type: application/xml;charset=iso-8859-1\r\n\r\n", 3, "unsupported", 200, ""},
		{"foreign media", "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n", 3, "unsupported", 200, ""},
		{"signed chunk", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n+1\r\nx\r\n0\r\n\r\n", 3, "invalid", 200, ""},
		{"chunk overflow", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n4\r\nxmlx\r\n0\r\n\r\n", 3, "budget_exhausted", 200, ""},
		{"bad chunk ending", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nxmlXX0\r\n\r\n", 3, "invalid", 200, "xml"},
		{"framing trailer", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nContent-Length: 0\r\n\r\n", 3, "invalid", 200, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := readServiceBusManagementHTTPResponse(context.Background(), strings.NewReader(test.response), test.limit)
			defer clear(response.body)
			if reason := kafkaManagementErrorReason(err); reason != test.reason {
				t.Fatalf("reason %q, expected %q", reason, test.reason)
			}
			if response.status != test.status {
				t.Fatalf("status %d, expected %d", response.status, test.status)
			}
			if test.reason == "" && string(response.body) != test.body {
				t.Fatal("body framing changed")
			}
		})
	}
}
func TestServiceBusManagementHeadersAndChunksStayBounded(t *testing.T) {
	for _, response := range []string{
		"HTTP/1.1 200 OK\r\nX-Ignored: " + strings.Repeat("a", serviceBusManagementHeaderLimit) + "\r\n\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1;" + strings.Repeat("a", serviceBusManagementHeaderLimit) + "\r\nx\r\n0\r\n\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" + strings.Repeat("1\r\nx\r\n", serviceBusManagementChunkLimit) + "0\r\n\r\n",
	} {
		_, err := readServiceBusManagementHTTPResponse(context.Background(), strings.NewReader(response), serviceBusManagementBodyLimit)
		if kafkaManagementErrorReason(err) != "budget_exhausted" {
			t.Fatal("unbounded response grammar accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readServiceBusManagementHTTPResponse(ctx, strings.NewReader(""), 1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled parser read accepted")
	}
}
func TestServiceBusManagementPartialBodyReturnsActualConsumedBytes(t *testing.T) {
	response, err := readServiceBusManagementHTTPResponse(context.Background(), strings.NewReader("HTTP/1.1 200 OK\r\nContent-Length: 8\r\n\r\nxml"), 8)
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(response.body) != 3 {
		t.Fatal("truncated body's consumed bytes lost", err)
	}
	response, err = readServiceBusManagementHTTPResponse(context.Background(), strings.NewReader("HTTP/1.1 200 OK\r\n\r\nxmlx"), 3)
	if kafkaManagementErrorReason(err) != "budget_exhausted" || len(response.body) != 4 {
		t.Fatal("EOF body limit silently truncated", err)
	}
}

type serviceBusShortWriter struct {
	buffer bytes.Buffer
	zero   bool
	err    error
}

func (w *serviceBusShortWriter) Write(body []byte) (int, error) {
	if w.zero {
		return 0, w.err
	}
	n := min(3, len(body))
	_, _ = w.buffer.Write(body[:n])
	return n, w.err
}
func TestServiceBusManagementFixedGETAndShortWrite(t *testing.T) {
	route := serviceBusManagementRoute{requestPath: "/orders?api-version=2021-05", serverName: "namespace.example.test"}
	writer := &serviceBusShortWriter{}
	if err := writeServiceBusManagementGET(writer, route, "SharedAccessSignature bounded"); err != nil {
		t.Fatal(err)
	}
	expected := "GET /orders?api-version=2021-05 HTTP/1.1\r\nHost: namespace.example.test\r\nAccept: application/atom+xml\r\nAuthorization: SharedAccessSignature bounded\r\nConnection: close\r\n\r\n"
	if writer.buffer.String() != expected {
		t.Fatal("fixed read-only HTTP surface changed")
	}
	if err := writeServiceBusManagementGET(&serviceBusShortWriter{zero: true}, route, "bounded"); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("zero write not bounded")
	}
	if err := writeServiceBusManagementGET(io.Discard, route, "canary\r\nInjected: true"); err == nil {
		t.Fatal("authorization header injection accepted")
	}
	route.requestPath = "/orders HTTP/1.1\r\nInjected: true"
	if err := writeServiceBusManagementGET(io.Discard, route, "bounded"); err == nil {
		t.Fatal("request-target injection accepted")
	}
}
