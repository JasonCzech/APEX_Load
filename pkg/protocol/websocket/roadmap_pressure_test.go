package websocket

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// A single post-handshake transport write is held at a controlled boundary.
// This observes application writer pressure; it does not infer kernel queues,
// transport-buffer occupancy, network throughput or server processing time.
type pressureGateConn struct {
	net.Conn
	armed                    atomic.Bool
	entered, release, closed chan struct{}
	once                     sync.Once
	mu                       sync.Mutex
	deadline                 time.Time
}

func (c *pressureGateConn) SetWriteDeadline(at time.Time) error {
	c.mu.Lock()
	c.deadline = at
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(at)
}
func (c *pressureGateConn) Write(data []byte) (int, error) {
	if c.armed.CompareAndSwap(true, false) {
		close(c.entered)
		c.mu.Lock()
		deadline := c.deadline
		c.mu.Unlock()
		if deadline.IsZero() {
			return 0, errors.New("fixture requires a bounded transport write")
		}
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-c.release:
		case <-c.closed:
			return 0, net.ErrClosed
		case <-timer.C:
			return 0, &net.OpError{Op: "write", Net: "tcp", Err: os.ErrDeadlineExceeded}
		}
	}
	return c.Conn.Write(data)
}
func (c *pressureGateConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type pressureWriteResult struct {
	result *protocol.Result
	err    error
}

func pressureClient(t *testing.T, timeoutMS int) (*WebSocket, *pressureGateConn, <-chan string, func()) {
	t.Helper()
	messages := make(chan string, 4)
	var handlers sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		conn, err := (&gorilla.Upgrader{}).Upgrade(rw, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind != gorilla.TextMessage {
				continue
			}
			select {
			case messages <- string(body):
			default:
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	client := &WebSocket{}
	if err := client.Init(context.Background(), map[string]any{"websocket_write_timeout_ms": 3000, "websocket_reconnect": false}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Teardown(context.Background()) })
	captured := make(chan *pressureGateConn, 1)
	client.dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wrapped := &pressureGateConn{Conn: conn, entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
		captured <- wrapped
		return wrapped, nil
	}
	result, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: "ws" + strings.TrimPrefix(server.URL, "http")})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("connect %v %+v", err, result)
	}
	var gate *pressureGateConn
	select {
	case gate = <-captured:
	case <-time.After(5 * time.Second):
		t.Fatal("transport wrapper not captured")
	}
	client.writeTimeout = time.Duration(timeoutMS) * time.Millisecond
	gate.armed.Store(true)
	var finishOnce sync.Once
	finish := func() {
		t.Helper()
		finishOnce.Do(func() {
			_ = client.Teardown(context.Background())
			server.Close()
			joined := make(chan struct{})
			go func() { handlers.Wait(); close(joined) }()
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Error("endpoint handlers did not join")
			}
		})
	}
	t.Cleanup(finish)
	return client, gate, messages, finish
}
func waitPressureResult(t *testing.T, results <-chan pressureWriteResult) pressureWriteResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("bounded write did not return")
		return pressureWriteResult{}
	}
}
func TestWebSocketControlledWritePressureKeepsApplicationQueueAndEndpointCounts(t *testing.T) {
	client, gate, messages, finish := pressureClient(t, 3000)
	first, second := make(chan pressureWriteResult, 1), make(chan pressureWriteResult, 1)
	go func() {
		r, e := client.Execute(context.Background(), &protocol.Action{Type: "ws_send", Body: []byte("first")})
		first <- pressureWriteResult{r, e}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first transport write did not reach gate")
	}
	go func() {
		r, e := client.Execute(context.Background(), &protocol.Action{Type: "ws_send", Body: []byte("second")})
		second <- pressureWriteResult{r, e}
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for client.pendingWrites.Load() != 2 {
		select {
		case <-deadline.C:
			t.Fatal("second application write did not queue")
		case <-ticker.C:
		}
	}
	// The independent target has received neither authored application message.
	select {
	case got := <-messages:
		t.Fatalf("message bypassed transport gate: %s", got)
	default:
	}
	const hold = 25 * time.Millisecond
	<-time.After(hold)
	close(gate.release)
	for i, tc := range []struct {
		result  pressureWriteResult
		body    string
		pending int64
	}{{waitPressureResult(t, first), "first", 1}, {waitPressureResult(t, second), "second", 2}} {
		if tc.result.err != nil || tc.result.result == nil || !tc.result.result.Success || tc.result.result.ServerTelemetry == nil {
			t.Fatalf("write %v %+v", tc.result.err, tc.result.result)
		}
		d := tc.result.result.ServerTelemetry.WebSocket
		if d == nil || !d.Valid() || d.MessagesSent != 1 || d.BytesSent != int64(len(tc.body)) || d.PendingWrites != tc.pending || d.SendMs == nil || d.WriteWaitMs == nil || d.ReceiveMs != nil || len(d.PingRTTMs) != 0 {
			t.Fatalf("pressure evidence differs from authored operation: %+v", d)
		}
		if i == 0 && *d.SendMs < float64(hold)/float64(time.Millisecond) {
			t.Fatalf("send elapsed omitted the controlled write interval: %g", *d.SendMs)
		}
		select {
		case got := <-messages:
			if got != tc.body {
				t.Fatalf("independent endpoint got %q want %q", got, tc.body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("endpoint did not receive released message")
		}
	}
	finish()
	select {
	case body := <-messages:
		t.Fatalf("extra final endpoint delivery: %q", body)
	default:
	}
	if client.pendingWrites.Load() != 0 {
		t.Fatal("writer count leaked after completion")
	}
}
func TestWebSocketControlledWriteDeadlinePreservesUnavailableDelivery(t *testing.T) {
	client, gate, messages, finish := pressureClient(t, 30)
	results := make(chan pressureWriteResult, 1)
	go func() {
		r, e := client.Execute(context.Background(), &protocol.Action{Type: "ws_send", Body: []byte("never-delivered")})
		results <- pressureWriteResult{r, e}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("write did not reach gate")
	}
	got := waitPressureResult(t, results)
	if got.result == nil || got.result.Success || got.result.ServerTelemetry == nil {
		t.Fatalf("deadline outcome %+v %v", got.result, got.err)
	}
	d := got.result.ServerTelemetry.WebSocket
	if d == nil || !d.Valid() || d.MessagesSent != 0 || d.BytesSent != 0 || d.SendMs == nil || d.DisconnectReason != "timeout" {
		t.Fatalf("deadline invented delivery: %+v", d)
	}
	if client.pendingWrites.Load() != 0 {
		t.Fatal("deadline leaked pending writer")
	}
	finish()
	select {
	case body := <-messages:
		t.Fatalf("failed gate delivered %q", body)
	default:
	}
}

func TestWebSocketControlledWriteFailureRetainsSuccessfulReconnectEvidence(t *testing.T) {
	client, gate, messages, finish := pressureClient(t, 30)
	client.reconnect = true
	results := make(chan pressureWriteResult, 1)
	go func() {
		r, e := client.Execute(context.Background(), &protocol.Action{Type: "ws_send", Body: []byte("retry-delivered")})
		results <- pressureWriteResult{r, e}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("write did not reach gate")
	}
	got := waitPressureResult(t, results)
	if got.err != nil || got.result == nil || !got.result.Success || got.result.ServerTelemetry == nil {
		t.Fatalf("reconnect result %v %+v", got.err, got.result)
	}
	d := got.result.ServerTelemetry.WebSocket
	if d == nil || !d.Valid() || d.Reconnects != 1 || d.ReconnectMs == nil || d.MessagesSent != 1 || d.BytesSent != 15 || d.SendMs == nil || d.WriteWaitMs == nil {
		t.Fatalf("reconnect lost attempted/retained evidence: %+v", d)
	}
	select {
	case body := <-messages:
		if body != "retry-delivered" {
			t.Fatalf("endpoint got %q", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("endpoint missed successful retry")
	}
	finish()
	select {
	case body := <-messages:
		t.Fatalf("failed gated write duplicated delivery: %q", body)
	default:
	}
	if client.pendingWrites.Load() != 0 {
		t.Fatal("reconnect leaked pending writer")
	}
}
