package generatorrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/storage"
)

const ReverseTargetPrefix = "tunnel://"

// WebSocketConn projects a WebSocket as a byte-stream net.Conn. gRPC's HTTP/2
// transport already multiplexes every unary and streaming RPC on one
// connection, so an additional stream-multiplexer is unnecessary.
type WebSocketConn struct {
	ws        *websocket.Conn
	readMu    sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	reader    io.Reader
	done      chan struct{}
}

func NewWebSocketConn(ws *websocket.Conn) *WebSocketConn {
	return &WebSocketConn{ws: ws, done: make(chan struct{})}
}

func (c *WebSocketConn) Done() <-chan struct{} { return c.done }

func (c *WebSocketConn) Read(buffer []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if c.reader != nil {
			n, err := c.reader.Read(buffer)
			if err == nil || n > 0 {
				return n, err
			}
			if !errors.Is(err, io.EOF) {
				c.markClosed()
				return 0, err
			}
			c.reader = nil
		}
		messageType, reader, err := c.ws.NextReader()
		if err != nil {
			c.markClosed()
			return 0, err
		}
		if messageType != websocket.BinaryMessage {
			c.markClosed()
			return 0, fmt.Errorf("reverse generator channel requires binary WebSocket frames")
		}
		c.reader = reader
	}
}

func (c *WebSocketConn) Write(buffer []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	writer, err := c.ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		c.markClosed()
		return 0, err
	}
	n, writeErr := writer.Write(buffer)
	closeErr := writer.Close()
	if writeErr != nil {
		c.markClosed()
		return n, writeErr
	}
	if closeErr != nil {
		c.markClosed()
		return n, closeErr
	}
	return n, nil
}

func (c *WebSocketConn) Close() error {
	c.markClosed()
	return c.ws.Close()
}

func (c *WebSocketConn) markClosed() { c.closeOnce.Do(func() { close(c.done) }) }

func (c *WebSocketConn) LocalAddr() net.Addr  { return c.ws.UnderlyingConn().LocalAddr() }
func (c *WebSocketConn) RemoteAddr() net.Addr { return c.ws.UnderlyingConn().RemoteAddr() }
func (c *WebSocketConn) SetDeadline(deadline time.Time) error {
	if err := c.ws.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(deadline)
}
func (c *WebSocketConn) SetReadDeadline(deadline time.Time) error {
	return c.ws.SetReadDeadline(deadline)
}
func (c *WebSocketConn) SetWriteDeadline(deadline time.Time) error {
	return c.ws.SetWriteDeadline(deadline)
}

type reverseRegistryEntry struct {
	generation uint64
	available  net.Conn
	active     net.Conn
	notify     chan struct{}
}

// ReverseRegistry hands generator-originated connections to controller-side
// gRPC dialers. A duplicate identity replaces and closes its prior session;
// callers never observe two live transports for one generator ID.
type ReverseRegistry struct {
	mu             sync.Mutex
	sessions       map[string]*reverseRegistryEntry
	nextGeneration uint64
	clientsMu      sync.Mutex
	clients        map[string]*Client
}

func NewReverseRegistry() *ReverseRegistry {
	return &ReverseRegistry{sessions: make(map[string]*reverseRegistryEntry)}
}

func (r *ReverseRegistry) Register(generatorID string, conn net.Conn) (uint64, error) {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" || conn == nil {
		return 0, errors.New("generator id and connection are required")
	}
	if err := storage.ValidateReverseGeneratorIdentity(generatorID); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.sessions[generatorID]
	if entry == nil {
		entry = &reverseRegistryEntry{notify: make(chan struct{})}
		r.sessions[generatorID] = entry
	}
	// An entry may be deleted on disconnect. A registry-wide counter prevents
	// a delayed old disconnect from matching a later re-enrollment's generation.
	r.nextGeneration++
	entry.generation = r.nextGeneration
	if entry.available != nil {
		_ = entry.available.Close()
	}
	if entry.active != nil {
		_ = entry.active.Close()
	}
	entry.available = conn
	entry.active = nil
	close(entry.notify)
	entry.notify = make(chan struct{})
	return entry.generation, nil
}

func (r *ReverseRegistry) Dial(ctx context.Context, generatorID string) (net.Conn, error) {
	generatorID = strings.TrimSpace(strings.TrimPrefix(generatorID, ReverseTargetPrefix))
	if err := storage.ValidateReverseGeneratorIdentity(generatorID); err != nil {
		return nil, err
	}
	for {
		r.mu.Lock()
		entry := r.sessions[generatorID]
		if entry == nil {
			entry = &reverseRegistryEntry{notify: make(chan struct{})}
			r.sessions[generatorID] = entry
		}
		if entry.available != nil {
			conn := entry.available
			entry.available = nil
			entry.active = conn
			r.mu.Unlock()
			return conn, nil
		}
		notify := entry.notify
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notify:
		}
	}
}

func (r *ReverseRegistry) Disconnect(generatorID string, generation uint64) {
	generatorID = strings.TrimSpace(strings.TrimPrefix(generatorID, ReverseTargetPrefix))
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.sessions[generatorID]
	if entry == nil || entry.generation != generation {
		return
	}
	if entry.available != nil {
		_ = entry.available.Close()
	}
	if entry.active != nil {
		_ = entry.active.Close()
	}
	delete(r.sessions, generatorID)
	close(entry.notify)
}

func (r *ReverseRegistry) Connected(generatorID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.sessions[strings.TrimSpace(strings.TrimPrefix(generatorID, ReverseTargetPrefix))]
	return entry != nil && (entry.available != nil || entry.active != nil)
}

func ReverseGeneratorID(target string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(target), ReverseTargetPrefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(target), ReverseTargetPrefix))
	return id, storage.ValidateReverseGeneratorIdentity(id) == nil
}

var DefaultReverseRegistry = NewReverseRegistry()

// ConnListener lets a long-lived gRPC server accept a sequence of outbound
// reverse connections as the generator reconnects after ingress or controller
// restarts.
type ConnListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
	addr        net.Addr
}

func NewConnListener(addr net.Addr) *ConnListener {
	return &ConnListener{connections: make(chan net.Conn), closed: make(chan struct{}), addr: addr}
}

func (l *ConnListener) Offer(ctx context.Context, conn net.Conn) error {
	if l == nil || conn == nil {
		return errors.New("listener and connection are required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.closed:
		return net.ErrClosed
	case l.connections <- conn:
		return nil
	}
}

func (l *ConnListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case conn := <-l.connections:
		return conn, nil
	}
}

func (l *ConnListener) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *ConnListener) Addr() net.Addr {
	if l != nil && l.addr != nil {
		return l.addr
	}
	return reverseAddr("reverse-generator")
}

type reverseAddr string

func (a reverseAddr) Network() string { return "websocket" }
func (a reverseAddr) String() string  { return string(a) }
