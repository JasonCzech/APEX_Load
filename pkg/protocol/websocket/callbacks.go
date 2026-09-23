package websocket

import (
	"context"
	"errors"
	"fmt"
	gorilla "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/protocol"
	"time"
)

const callbackQueueBytes = 1 << 20
const callbackQueueMessages = 64

var errCallbackQueueOverflow = errors.New("WebSocket callback queue overflow")

type callbackState struct {
	epoch  uint64
	queue  []protocol.CallbackMessage
	bytes  int
	err    error
	next   time.Time
	cancel context.CancelFunc
	done   <-chan struct{}
}

func (w *WebSocket) stopCallbacksLocked() {
	if w.callbacks != nil {
		w.callbacks.cancel()
		w.callbacks = nil
	}
}
func (w *WebSocket) registerCallback(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn == nil {
		return fmt.Errorf("callback requires an open WebSocket")
	}
	if w.callbacks != nil {
		return fmt.Errorf("callback already registered; reconnect before replacing it")
	}
	if err := w.conn.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	readerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	state := &callbackState{epoch: w.connectionEpoch, cancel: cancel, done: readerCtx.Done(), next: time.Now().Add(w.callbackInterval)}
	w.callbacks = state
	conn := w.conn
	go w.readCallbacks(readerCtx, conn, state)
	return nil
}
func (w *WebSocket) readCallbacks(ctx context.Context, conn *gorilla.Conn, state *callbackState) {
	for {
		kind, body, err := conn.ReadMessage()
		w.mu.Lock()
		if ctx.Err() != nil || w.callbacks != state || w.connectionEpoch != state.epoch {
			w.mu.Unlock()
			return
		}
		if err != nil {
			state.err = err
			w.mu.Unlock()
			return
		}
		text := responseBody(kind, body)
		if len(state.queue) >= callbackQueueMessages || len(text) > callbackQueueBytes-state.bytes {
			state.err = errCallbackQueueOverflow
			w.mu.Unlock()
			_ = conn.Close()
			return
		}
		state.queue = append(state.queue, protocol.CallbackMessage{Generation: state.epoch, Body: text, MessageType: kind, BytesReceived: int64(len(body))})
		state.bytes += len(text)
		w.mu.Unlock()
	}
}
func (w *WebSocket) CallbackGeneration() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.connectionEpoch
}

// DrainMessageCallbacks is called on the VUser execution goroutine. The reader
// never executes user actions. Dispatch is due at safe action boundaries; an
// explicit ws_dispatch waits for the next interval when the script is idle.
func (w *WebSocket) DrainMessageCallbacks(ctx context.Context, wait bool) ([]protocol.CallbackMessage, error) {
	w.mu.Lock()
	state := w.callbacks
	if state == nil {
		w.mu.Unlock()
		return nil, nil
	}
	delay := time.Until(state.next)
	w.mu.Unlock()
	if delay > 0 {
		if !wait {
			return nil, nil
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-state.done:
			return nil, nil
		case <-timer.C:
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.callbacks != state || w.connectionEpoch != state.epoch {
		return nil, nil
	}
	// Overflow is fatal before executing any queued callback: a callback could
	// otherwise reconnect and erase the lost-message error with the old queue.
	if state.err != nil && (len(state.queue) == 0 || errors.Is(state.err, errCallbackQueueOverflow)) {
		err := state.err
		w.stopCallbacksLocked()
		return nil, err
	}
	messages := state.queue
	state.queue = nil
	state.bytes = 0
	state.next = time.Now().Add(w.callbackInterval)
	return messages, nil
}
