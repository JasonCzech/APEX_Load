package websocket

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	gorilla "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type authoredPing struct {
	sent      time.Time
	ambiguous bool
}

func wsDiagnostics(result *protocol.Result) *protocol.WebSocketDiagnostics {
	if result.ServerTelemetry == nil {
		result.ServerTelemetry = &protocol.ServerTelemetry{}
	}
	if result.ServerTelemetry.WebSocket == nil {
		result.ServerTelemetry.WebSocket = &protocol.WebSocketDiagnostics{}
	}
	return result.ServerTelemetry.WebSocket
}

func wsElapsed(start time.Time) *float64 {
	v := float64(time.Since(start)) / float64(time.Millisecond)
	return &v
}
func wsMessageType(kind int) string {
	switch kind {
	case gorilla.TextMessage:
		return "text"
	case gorilla.BinaryMessage:
		return "binary"
	case gorilla.PingMessage:
		return "ping"
	case gorilla.CloseMessage:
		return "close"
	}
	return ""
}

func (w *WebSocket) finishDiagnostics(ctx context.Context, action *protocol.Action, result *protocol.Result, err error) {
	d := wsDiagnostics(result)
	d.Operation = action.Type
	d.BytesSent = result.BytesSent
	d.BytesReceived = result.BytesReceived
	w.mu.Lock()
	d.CallbackMode = w.callbacks != nil
	d.PingRTTMs = append([]float64(nil), w.pongRTTs...)
	w.pongRTTs = nil
	w.mu.Unlock()
	if err == nil {
		return
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	var closed *gorilla.CloseError
	var timeout net.Error
	switch {
	case errors.Is(err, context.Canceled):
		d.DisconnectReason = "cancelled"
	case errors.As(err, &closed):
		code := int32(closed.Code)
		d.CloseCode = &code
		d.DisconnectReason = "peer_close"
		if closed.Code == gorilla.CloseNormalClosure {
			d.DisconnectReason = "normal"
		}
	case errors.As(err, &timeout) && timeout.Timeout() || errors.Is(err, context.DeadlineExceeded):
		d.DisconnectReason = "timeout"
	case errors.Is(err, errCallbackQueueOverflow):
		d.DisconnectReason = "callback_overflow"
	default:
		if errors.As(err, &timeout) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, gorilla.ErrCloseSent) {
			d.DisconnectReason = "transport_error"
		}
	}
}

func (w *WebSocket) discardAuthoredPing(payload []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.authoredPings, string(payload))
}

// Called before a reader owns the new connection. The pong handler is invoked
// by that reader; it never competes with receive/callback reads or sends probes.
func (w *WebSocket) instrumentPongs(conn *gorilla.Conn, epoch uint64) {
	conn.SetPongHandler(func(payload string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if epoch != w.connectionEpoch {
			return nil
		}
		ping, ok := w.authoredPings[payload]
		delete(w.authoredPings, payload)
		if ok && !ping.ambiguous && time.Since(ping.sent) <= time.Minute && len(w.pongRTTs) < 32 {
			w.pongRTTs = append(w.pongRTTs, float64(time.Since(ping.sent))/float64(time.Millisecond))
		}
		return nil
	})
}

func (w *WebSocket) noteAuthoredPing(payload []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if w.authoredPings == nil {
		w.authoredPings = map[string]authoredPing{}
	}
	for key, p := range w.authoredPings {
		if now.Sub(p.sent) > time.Minute {
			delete(w.authoredPings, key)
		}
	}
	key := string(payload)
	if p, exists := w.authoredPings[key]; exists {
		p.ambiguous = true
		w.authoredPings[key] = p
		return
	}
	if len(w.authoredPings) < 32 {
		w.authoredPings[key] = authoredPing{sent: now}
	}
}

func (w *WebSocket) measuredWrite(conn *gorilla.Conn, kind int, body []byte, result *protocol.Result) error {
	d := wsDiagnostics(result)
	d.MessageType = wsMessageType(kind)
	pending := w.pendingWrites.Add(1)
	defer w.pendingWrites.Add(-1)
	d.PendingWrites = max(d.PendingWrites, pending)
	wait := time.Now()
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	waitMs := wsElapsed(wait)
	if d.WriteWaitMs == nil {
		d.WriteWaitMs = waitMs
	} else {
		*d.WriteWaitMs += *waitMs
	}
	start := time.Now()
	err := w.writeMessage(conn, kind, body)
	elapsed := wsElapsed(start)
	if d.SendMs == nil {
		d.SendMs = elapsed
	} else {
		*d.SendMs += *elapsed
	}
	if err != nil {
		d.DisconnectReason = "transport_error"
	}
	return err
}
