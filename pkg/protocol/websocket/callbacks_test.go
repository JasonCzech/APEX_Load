package websocket

import (
	"context"
	"fmt"
	gorilla "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCallbackQueueOrderingCloseAndReconnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, e := (&gorilla.Upgrader{}).Upgrade(rw, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		for i := 0; i < 3; i++ {
			_ = c.WriteMessage(gorilla.TextMessage, []byte(fmt.Sprint(i)))
		}
		for {
			if _, _, e := c.ReadMessage(); e != nil {
				return
			}
		}
	}))
	defer server.Close()
	w := &WebSocket{}
	if e := w.Init(context.Background(), map[string]any{"websocket_callback_interval_ms": 1}); e != nil {
		t.Fatal(e)
	}
	defer w.Teardown(context.Background())
	connect := func() {
		r, e := w.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: "ws" + strings.TrimPrefix(server.URL, "http")})
		if e != nil || !r.Success {
			t.Fatalf("connect: %+v %v", r, e)
		}
	}
	connect()
	epoch := w.CallbackGeneration()
	r, _ := w.Execute(context.Background(), &protocol.Action{Type: "ws_on_message"})
	if !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	var messages []protocol.CallbackMessage
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for len(messages) < 3 {
		batch, e := w.DrainMessageCallbacks(ctx, true)
		if e != nil {
			t.Fatal(e)
		}
		messages = append(messages, batch...)
	}
	for i, m := range messages {
		if m.Body != fmt.Sprint(i) || m.Generation != epoch {
			t.Fatalf("order/generation: %+v", messages)
		}
	}
	w.Execute(context.Background(), &protocol.Action{Type: "ws_close"})
	if w.CallbackGeneration() == epoch {
		t.Fatal("generation not invalidated")
	}
	connect()
	batch, e := w.DrainMessageCallbacks(ctx, false)
	if e != nil || len(batch) != 0 {
		t.Fatal("old callbacks survived reconnect")
	}
}
func TestCallbackOverflowAndCancellation(t *testing.T) {
	w := &WebSocket{callbackInterval: time.Minute, connectionEpoch: 3}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.callbacks = &callbackState{epoch: 3, next: time.Now().Add(time.Minute), cancel: func() {}}
	if _, e := w.DrainMessageCallbacks(ctx, true); e == nil {
		t.Fatal("dispatch ignored cancellation")
	}
	w.callbacks.next = time.Now()
	w.callbacks.err = fmt.Errorf("WebSocket callback queue overflow")
	if _, e := w.DrainMessageCallbacks(context.Background(), false); e == nil || !strings.Contains(e.Error(), "overflow") {
		t.Fatal("overflow not surfaced")
	}
	if w.callbacks != nil {
		t.Fatal("failed queue retained")
	}
}

func TestCallbacksDeliverQueuedMessagesBeforePeerClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, e := (&gorilla.Upgrader{}).Upgrade(rw, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		c.WriteMessage(gorilla.TextMessage, []byte("last message"))
		c.WriteMessage(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, "done"))
	}))
	defer server.Close()
	w := &WebSocket{}
	if e := w.Init(context.Background(), map[string]any{"websocket_callback_interval_ms": 1}); e != nil {
		t.Fatal(e)
	}
	defer w.Teardown(context.Background())
	r, _ := w.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: "ws" + strings.TrimPrefix(server.URL, "http")})
	if !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	if e := w.registerCallback(context.Background()); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		closed := w.callbacks.err != nil
		w.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reader did not observe peer close")
		}
		time.Sleep(time.Millisecond)
	}
	messages, e := w.DrainMessageCallbacks(context.Background(), true)
	if e != nil || len(messages) != 1 || messages[0].Body != "last message" {
		t.Fatalf("queued message lost at close: %+v %v", messages, e)
	}
	if _, e = w.DrainMessageCallbacks(context.Background(), true); e == nil {
		t.Fatal("terminal error was lost")
	}
}

func TestFailedConnectClosesPreviousCallbackReader(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reject" {
			http.Error(rw, "reject", 400)
			return
		}
		c, e := (&gorilla.Upgrader{}).Upgrade(rw, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		c.ReadMessage()
		close(closed)
	}))
	defer server.Close()
	w := &WebSocket{}
	if e := w.Init(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	defer w.Teardown(context.Background())
	target := "ws" + strings.TrimPrefix(server.URL, "http")
	r, _ := w.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: target})
	if !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	if e := w.registerCallback(context.Background()); e != nil {
		t.Fatal(e)
	}
	r, _ = w.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: target + "/reject"})
	if r.Success {
		t.Fatal("expected handshake failure")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("old callback connection leaked after failed connect")
	}
}

func TestCallbackWaitEndsOnTeardown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &WebSocket{callbackInterval: time.Minute, connectionEpoch: 1, callbacks: &callbackState{epoch: 1, next: time.Now().Add(time.Minute), cancel: cancel, done: ctx.Done()}}
	finished := make(chan error, 1)
	go func() { _, err := w.DrainMessageCallbacks(context.Background(), true); finished <- err }()
	w.Teardown(context.Background())
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatch wait survived teardown")
	}
}

func TestCallbackActualOverflowFailsBeforeDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, e := (&gorilla.Upgrader{}).Upgrade(rw, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		for i := 0; i <= callbackQueueMessages; i++ {
			if c.WriteMessage(gorilla.TextMessage, []byte("message")) != nil {
				return
			}
		}
		c.ReadMessage()
	}))
	defer server.Close()
	w := &WebSocket{}
	if e := w.Init(context.Background(), map[string]any{"websocket_callback_interval_ms": 1}); e != nil {
		t.Fatal(e)
	}
	defer w.Teardown(context.Background())
	r, _ := w.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: "ws" + strings.TrimPrefix(server.URL, "http")})
	if !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	if e := w.registerCallback(context.Background()); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		failed := w.callbacks.err != nil
		w.mu.Unlock()
		if failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("overflow was not detected")
		}
		time.Sleep(time.Millisecond)
	}
	messages, e := w.DrainMessageCallbacks(context.Background(), true)
	if e != errCallbackQueueOverflow || len(messages) != 0 {
		t.Fatalf("overflow must fail before callbacks can reconnect: %d messages, %v", len(messages), e)
	}
}
