package recorder

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestReadWebSocketFrameRejectsOversizedPayloadBeforeAllocation(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0x82)
	buf.WriteByte(127)
	if err := binary.Write(&buf, binary.BigEndian, uint64(maxWebSocketFramePayloadBytes+1)); err != nil {
		t.Fatal(err)
	}

	_, err := readWebSocketFrame(&buf)
	if err == nil {
		t.Fatal("expected oversized websocket frame error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized frame error = %v", err)
	}
}

func TestCloneWebSocketRequestsStripCompressionExtension(t *testing.T) {
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "ws", Host: "example.test", Path: "/ws"},
		Host:   "example.test",
		Header: http.Header{
			"Connection":               []string{"Upgrade"},
			"Upgrade":                  []string{"websocket"},
			"Sec-WebSocket-Extensions": []string{"permessage-deflate; client_max_window_bits"},
		},
	}

	target := &url.URL{Scheme: "ws", Host: "example.test", Path: "/ws"}
	upstream := cloneWebSocketUpstreamRequest(req, target)
	if got := upstream.Header.Get("Sec-WebSocket-Extensions"); got != "" {
		t.Fatalf("upstream compression extension = %q, want stripped", got)
	}
	recorded := cloneWebSocketRecordRequest(req, target)
	if got := recorded.Header.Get("Sec-WebSocket-Extensions"); got != "" {
		t.Fatalf("recorded compression extension = %q, want stripped", got)
	}
}
