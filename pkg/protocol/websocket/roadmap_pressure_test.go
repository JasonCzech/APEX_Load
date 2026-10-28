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

func TestWebSocketControlledEndpointLifecycleTimeoutAndClientAnalysis(t *testing.T) {
	var connections atomic.Int64
	var pingsReceived atomic.Int64
	var pongsSent atomic.Int64
	var textReceived atomic.Int64
	var normalClosesSent atomic.Int64
	var abnormalClosesSent atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		conn, err := (&gorilla.Upgrader{}).Upgrade(rw, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connNum := connections.Add(1)
		conn.SetPingHandler(func(payload string) error {
			pingsReceived.Add(1)
			// Emit 1 unmatched pong followed by 1 matched pong, then 1 text message after a brief delay so ReceiveMs > PingRTTMs.
			if err := conn.WriteControl(gorilla.PongMessage, []byte("unmatched-server-pong"), time.Now().Add(time.Second)); err != nil {
				return err
			}
			pongsSent.Add(1)
			if err := conn.WriteControl(gorilla.PongMessage, []byte(payload), time.Now().Add(time.Second)); err != nil {
				return err
			}
			pongsSent.Add(1)
			time.Sleep(15 * time.Millisecond)
			return conn.WriteMessage(gorilla.TextMessage, []byte("pong-ack"))
		})
		for {
			kind, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if kind != gorilla.TextMessage {
				continue
			}
			textReceived.Add(1)
			switch string(body) {
			case "trigger-normal-close":
				normalClosesSent.Add(1)
				_ = conn.WriteControl(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, "normal-bye"), time.Now().Add(time.Second))
				return
			case "trigger-abnormal-close":
				abnormalClosesSent.Add(1)
				_ = conn.WriteControl(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseInternalServerErr, "abnormal-err"), time.Now().Add(time.Second))
				return
			case "trigger-idle-timeout":
				// Hold connection open without sending any frame so ws_receive times out.
				time.Sleep(250 * time.Millisecond)
			default:
				_ = conn.WriteMessage(gorilla.TextMessage, []byte("conn-"+strings.TrimSpace(string(body))))
				_ = connNum
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client := &WebSocket{}
	if err := client.Init(context.Background(), map[string]any{
		"websocket_reconnect":        true,
		"websocket_read_timeout_ms":  2000,
		"websocket_write_timeout_ms": 2000,
	}); err != nil {
		t.Fatal(err)
	}
	defer client.Teardown(context.Background())

	var summary *protocol.WebSocketSummary
	record := func(res *protocol.Result) *protocol.WebSocketDiagnostics {
		t.Helper()
		if res == nil || res.ServerTelemetry == nil || res.ServerTelemetry.WebSocket == nil || !res.ServerTelemetry.WebSocket.Valid() {
			t.Fatalf("missing or invalid WebSocket diagnostics: %+v", res)
		}
		d := res.ServerTelemetry.WebSocket
		protocol.MergeWebSocketDiagnostic(&summary, d)
		return d
	}

	// 1. Connect + matched vs. unmatched ping/pong RTT vs. receive wait.
	resConn, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: wsURL})
	if err != nil || !resConn.Success {
		t.Fatalf("connect failed: %+v err=%v", resConn, err)
	}
	dConn := record(resConn)
	if dConn.HandshakeMs == nil {
		t.Fatalf("expected handshake timing: %+v", dConn)
	}

	resPing, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_ping", Body: []byte("authored-ping-1")})
	if err != nil || !resPing.Success {
		t.Fatalf("ping failed: %+v err=%v", resPing, err)
	}
	dPing := record(resPing)
	if len(dPing.PingRTTMs) != 0 {
		t.Fatalf("ping write should not invent RTT before pong read: %+v", dPing)
	}

	resRecvPong, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_receive"})
	if err != nil || !resRecvPong.Success {
		t.Fatalf("receive after ping failed: %+v err=%v", resRecvPong, err)
	}
	dRecvPong := record(resRecvPong)
	// Server sent 2 pongs (1 unmatched + 1 matched) and 1 text frame; only the 1 matched pong may record RTT.
	if pingsReceived.Load() != 1 || pongsSent.Load() != 2 || len(dRecvPong.PingRTTMs) != 1 || dRecvPong.ReceiveMs == nil {
		t.Fatalf("expected 1 matched pong RTT from 2 server pongs: pings=%d pongs=%d diag=%+v", pingsReceived.Load(), pongsSent.Load(), dRecvPong)
	}
	if dRecvPong.PingRTTMs[0] >= *dRecvPong.ReceiveMs {
		t.Fatalf("expected PingRTTMs (%g) < ReceiveMs (%g) after delayed text frame", dRecvPong.PingRTTMs[0], *dRecvPong.ReceiveMs)
	}

	// 2. Peer normal close (1000) -> DisconnectReason="normal", CloseCode=1000, followed by automatic reconnect on next send.
	if res, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_send", Body: []byte("trigger-normal-close")}); err != nil || !res.Success {
		t.Fatalf("send trigger-normal-close: %+v err=%v", res, err)
	} else {
		record(res)
	}
	resNormClose, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_receive"})
	if err != nil || resNormClose.Success {
		t.Fatalf("expected receive on peer normal close to end stream: %+v err=%v", resNormClose, err)
	}
	dNorm := record(resNormClose)
	if dNorm.DisconnectReason != "normal" || dNorm.CloseCode == nil || *dNorm.CloseCode != gorilla.CloseNormalClosure || normalClosesSent.Load() != 1 {
		t.Fatalf("unexpected peer normal close diagnostics: %+v", dNorm)
	}

	// Reconnects automatically on next send because close was peer-initiated, not ws_close action.
	resReconn1, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_send", Body: []byte("trigger-abnormal-close")})
	if err != nil || !resReconn1.Success {
		t.Fatalf("reconnect send after normal peer close failed: %+v err=%v", resReconn1, err)
	}
	dReconn1 := record(resReconn1)
	if dReconn1.Reconnects != 1 || dReconn1.ReconnectMs == nil || connections.Load() != 2 {
		t.Fatalf("expected reconnect to connection #2, got connections=%d diag=%+v", connections.Load(), dReconn1)
	}

	// 3. Peer abnormal close (1011) -> DisconnectReason="peer_close", CloseCode=1011.
	resAbnClose, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_receive"})
	if err != nil || resAbnClose.Success {
		t.Fatalf("expected receive on peer abnormal close to fail: %+v err=%v", resAbnClose, err)
	}
	dAbn := record(resAbnClose)
	if dAbn.DisconnectReason != "peer_close" || dAbn.CloseCode == nil || *dAbn.CloseCode != gorilla.CloseInternalServerErr || abnormalClosesSent.Load() != 1 {
		t.Fatalf("unexpected peer abnormal close diagnostics: %+v", dAbn)
	}

	// 4. Reconnect + idle read timeout (timeout_ms=40) -> DisconnectReason="timeout".
	resReconn2, err := client.Execute(context.Background(), &protocol.Action{Type: "ws_send", Body: []byte("trigger-idle-timeout")})
	if err != nil || !resReconn2.Success {
		t.Fatalf("reconnect send after abnormal close failed: %+v err=%v", resReconn2, err)
	}
	dReconn2 := record(resReconn2)
	if dReconn2.Reconnects != 1 || connections.Load() != 3 {
		t.Fatalf("expected reconnect to connection #3, got connections=%d diag=%+v", connections.Load(), dReconn2)
	}
	resIdle, err := client.Execute(context.Background(), &protocol.Action{
		Type:   "ws_receive",
		Params: map[string]any{"timeout_ms": 40},
	})
	if err != nil || resIdle.Success {
		t.Fatalf("expected idle receive timeout failure: %+v err=%v", resIdle, err)
	}
	dIdle := record(resIdle)
	if dIdle.DisconnectReason != "timeout" || dIdle.MessagesReceived != 0 || dIdle.BytesReceived != 0 || dIdle.ReceiveMs == nil || len(dIdle.PingRTTMs) != 0 {
		t.Fatalf("unexpected idle timeout diagnostics: %+v", dIdle)
	}

	// Verify merged summary and ClientAnalysis() non-conflation and counters.
	if summary.NormalCloses != 1 || summary.AbnormalCloses != 1 || summary.Timeouts != 1 || summary.Reconnects != 2 {
		t.Fatalf("unexpected lifecycle summary counters: %+v", summary)
	}
	ca := summary.ClientAnalysis()
	if ca.PingPongRTT.Count != 1 || ca.ReceiveWait.Count != 4 || ca.ApplicationPendingWrites < 1 || ca.PendingWritesScope != "application_writer_count" || ca.KernelTransportQueueDepth != "unavailable" {
		t.Fatalf("unexpected WebSocket ClientAnalysis: %+v", ca)
	}
}
