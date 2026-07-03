package recorder

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
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

func TestCopyWebSocketFramesUsesLongWebSocketIdleDeadline(t *testing.T) {
	var dst bytes.Buffer
	conn := &deadlineRecorderConn{}
	start := time.Now()

	New(":0").copyWebSocketFrames(&dst, bytes.NewReader([]byte{0x88, 0x00}), conn, nil, 0, webSocketDirectionServerToClient)

	if len(conn.readDeadlines) < 2 {
		t.Fatalf("read deadlines = %v, want set and clear", conn.readDeadlines)
	}
	if remaining := conn.readDeadlines[0].Sub(start); remaining < webSocketTunnelIdleTimeout-time.Second {
		t.Fatalf("first read deadline = %s from start, want close to %s", remaining, webSocketTunnelIdleTimeout)
	}
	if !conn.readDeadlines[1].IsZero() {
		t.Fatalf("second read deadline = %v, want zero clear", conn.readDeadlines[1])
	}
	if got := dst.Bytes(); !bytes.Equal(got, []byte{0x88, 0x00}) {
		t.Fatalf("forwarded bytes = %v, want close frame", got)
	}
}

type deadlineRecorderConn struct {
	readDeadlines []time.Time
}

func (c *deadlineRecorderConn) Read([]byte) (int, error)         { return 0, nil }
func (c *deadlineRecorderConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *deadlineRecorderConn) Close() error                     { return nil }
func (c *deadlineRecorderConn) LocalAddr() net.Addr              { return nil }
func (c *deadlineRecorderConn) RemoteAddr() net.Addr             { return nil }
func (c *deadlineRecorderConn) SetDeadline(time.Time) error      { return nil }
func (c *deadlineRecorderConn) SetWriteDeadline(time.Time) error { return nil }
func (c *deadlineRecorderConn) SetReadDeadline(t time.Time) error {
	c.readDeadlines = append(c.readDeadlines, t)
	return nil
}
