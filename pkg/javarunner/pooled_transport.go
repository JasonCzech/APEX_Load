package javarunner

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"
)

var ErrPooledHostReaped = errors.New("pooled_host_reaped")

// PooledTransport multiplexes independent slot invocation and control requests.
// Its owner must enforce the run lease, qualify artifacts/cleanup, and retain
// reservations until strong containment proves termination. It does not launch
// Java, negotiate production capabilities, or establish a security sandbox.
type PooledTransport struct {
	mu         sync.Mutex
	generation string
	secret     []byte
	input      io.ReadCloser
	output     io.WriteCloser
	writes     chan []byte
	done       chan struct{}
	pending    map[uint64]pooledPending
	sequence   uint64
	stopped    bool
	// Must begin whole-host containment cleanup immediately and return promptly.
	onFailure func(error)
}
type pooledPending struct {
	frame  PooledFrame
	result chan pooledReply
}
type pooledReply struct {
	frame PooledFrame
	err   error
}

// NewPooledTransport owns the authenticated duplex socket. The launcher must
// deliver the secret over a separate bootstrap pipe, authenticate the socket
// before calling this function, and bound acceptance/startup separately. Raw
// process stdout/stderr belong to independently drained, bounded diagnostics.
// The caller must bound readiness with Call(ctx, "", "ready", nil) and must call
// Close on lease loss/shutdown, even if the JVM has already exited.
func NewPooledTransport(input io.ReadCloser, output io.WriteCloser, generation string, secret []byte, onFailure func(error)) (*PooledTransport, error) {
	if input == nil || output == nil || !pooledID(generation, false) || len(secret) != 32 || onFailure == nil {
		return nil, ErrPooledFrame
	}
	t := &PooledTransport{generation: generation, secret: append([]byte(nil), secret...), input: input, output: output, writes: make(chan []byte, 64), done: make(chan struct{}), pending: make(map[uint64]pooledPending), onFailure: onFailure}
	go t.writeLoop()
	go t.readLoop()
	return t, nil
}
func (t *PooledTransport) Done() <-chan struct{} { return t.done }
func (t *PooledTransport) Close()                { t.fail(ErrPooledHostReaped) }
func (t *PooledTransport) fail(cause error) {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	close(t.done)
	for id, p := range t.pending {
		p.result <- pooledReply{err: ErrPooledHostReaped}
		delete(t.pending, id)
	}
	t.mu.Unlock()
	// Cleanup notification precedes pipe closure: a blocked JVM/output writer must
	// never postpone containment enforcement. The callback must not wait for us.
	t.onFailure(cause)
	_ = t.input.Close()
	_ = t.output.Close()
}
func (t *PooledTransport) writeLoop() {
	for {
		select {
		case <-t.done:
			return
		case data := <-t.writes:
			for len(data) > 0 {
				n, err := t.output.Write(data)
				if err != nil || n <= 0 {
					t.fail(errors.New("pooled_transport_lost"))
					return
				}
				data = data[n:]
			}
		}
	}
}
func validPooledReply(f PooledFrame) bool {
	if f.Fields["reap"] != "true" && f.Fields["reap"] != "false" {
		return false
	}
	switch f.Fields["code"] {
	case "ok", "slot_closed", "pooled_invocation_failed", "pooled_cleanup_uncertain", "pooled_operation_rejected":
	default:
		return false
	}
	if f.Fields["code"] != "ok" {
		return len(f.Fields) == 2
	}
	allowed := map[string]bool{"code": true, "reap": true}
	switch f.Operation {
	case "invoke":
		for _, key := range []string{"return_code", "event_fallback", "event_truncated", "exit_option", "exit_status"} {
			allowed[key] = true
		}
		if _, err := strconv.ParseInt(f.Fields["return_code"], 10, 32); err != nil {
			return false
		}
		for _, key := range []string{"event_fallback", "event_truncated"} {
			if f.Fields[key] != "true" && f.Fields[key] != "false" {
				return false
			}
		}
		option, hasOption := f.Fields["exit_option"]
		status, hasStatus := f.Fields["exit_status"]
		if hasOption != hasStatus {
			return false
		}
		if hasOption {
			if option != "1" && option != "2" {
				return false
			}
			if _, err := strconv.ParseInt(status, 10, 32); err != nil {
				return false
			}
		}
	case "ready", "health":
		allowed["sealed"] = true
		if f.Fields["sealed"] != "true" && f.Fields["sealed"] != "false" {
			return false
		}
		if f.Operation == "ready" {
			allowed["event_attribution"] = true
			if f.Fields["event_attribution"] != "slot_thread_v1" {
				return false
			}
		}
		if f.Operation == "health" {
			for _, key := range []string{"gc_pause_status", "gc_pause_events", "gc_pause_evicted"} {
				allowed[key] = true
			}
			if _, err := parseGCPauses(f.Fields); err != nil {
				return false
			}
			allowed["diagnostics_base64"] = true
			allowed["diagnostics_dropped"] = true
			diagnostic, exists := f.Fields["diagnostics_base64"]
			if !exists || len(diagnostic) > 43692 {
				return false
			}
			decoded, err := base64.StdEncoding.Strict().DecodeString(diagnostic)
			if err != nil || len(decoded) > 32768 {
				return false
			}
			if _, err := strconv.ParseUint(f.Fields["diagnostics_dropped"], 10, 63); err != nil {
				return false
			}
		}
	}
	for key := range f.Fields {
		if !allowed[key] {
			return false
		}
	}
	return true
}
func (t *PooledTransport) readLoop() {
	for {
		f, err := ReadPooledFrame(t.input, t.secret, true)
		if err != nil {
			t.fail(errors.New("pooled_transport_lost"))
			return
		}
		t.mu.Lock()
		p, ok := t.pending[f.Sequence]
		if !ok || f.Generation != p.frame.Generation || f.Slot != p.frame.Slot || f.Request != p.frame.Request || f.Operation != p.frame.Operation || !validPooledReply(f) {
			t.mu.Unlock()
			t.fail(ErrPooledFrame)
			return
		}
		delete(t.pending, f.Sequence)
		var resultErr error
		if f.Fields["code"] != "ok" {
			resultErr = errors.New(f.Fields["code"])
		}
		p.result <- pooledReply{frame: f, err: resultErr}
		t.mu.Unlock()
		if f.Fields["reap"] == "true" {
			t.fail(errors.New("pooled_cleanup_uncertain"))
			return
		}
	}
}
func (t *PooledTransport) send(slot, op string, fields map[string]string) (<-chan pooledReply, error) {
	if len(fields) > pooledMaxFields {
		return nil, ErrPooledFrame
	}
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return nil, ErrPooledHostReaped
	}
	if len(t.pending) >= 512 || t.sequence == 1<<63-1 {
		t.mu.Unlock()
		t.fail(errors.New("pooled_control_queue_overflow"))
		return nil, ErrPooledHostReaped
	}
	sequence := t.sequence + 1
	// Freeze caller-owned maps before serialization/pending registration.
	f := PooledFrame{Generation: t.generation, Slot: slot, Request: strconv.FormatUint(sequence, 10), Operation: op, Sequence: sequence, Fields: make(map[string]string, len(fields))}
	for k, v := range fields {
		f.Fields[k] = v
	}
	data, err := EncodePooledFrame(f, t.secret, false)
	if err != nil {
		t.mu.Unlock()
		return nil, err
	}
	// Correlation needs identities only; do not retain parameter bodies while
	// an invocation is blocked in Java. The bounded writer owns the encoded bytes.
	f.Fields = nil
	result := make(chan pooledReply, 1)
	select {
	case t.writes <- data:
		t.sequence = sequence
		t.pending[sequence] = pooledPending{frame: f, result: result}
		t.mu.Unlock()
		return result, nil
	default:
		t.mu.Unlock()
		t.fail(errors.New("pooled_control_queue_overflow"))
		return nil, ErrPooledHostReaped
	}
}

// Call is for bounded control operations. Deadline/transport uncertainty reaps
// the entire host. Use Invoke for ordinary per-slot cancellation with grace.
func (t *PooledTransport) Call(ctx context.Context, slot, op string, fields map[string]string) (PooledFrame, error) {
	if err := ctx.Err(); err != nil {
		return PooledFrame{}, err
	}
	pending, err := t.send(slot, op, fields)
	if err != nil {
		return PooledFrame{}, err
	}
	select {
	case result := <-pending:
		return result.frame, result.err
	case <-ctx.Done():
		t.fail(errors.New("pooled_control_timeout"))
		return PooledFrame{}, ctx.Err()
	}
}

// Invoke allows two seconds for qualified cooperative closure after cancellation.
// An ignored interrupt, failed cleanup, or blocked control pipe reaps the host.
func (t *PooledTransport) Invoke(ctx context.Context, slot, method string, parameters map[string]string) (PooledFrame, error) {
	frame, _, err := t.InvokeWithClosure(ctx, slot, method, parameters)
	return frame, err
}

// InvokeWithClosure additionally reports positively acknowledged cooperative
// slot closure after cancellation. It is not descendant termination evidence.
func (t *PooledTransport) InvokeWithClosure(ctx context.Context, slot, method string, parameters map[string]string) (PooledFrame, bool, error) {
	if err := ctx.Err(); err != nil {
		return PooledFrame{}, false, err
	}
	if len(parameters) >= pooledMaxFields {
		return PooledFrame{}, false, ErrPooledFrame
	}
	fields := map[string]string{"method": method}
	for k, v := range parameters {
		fields["p."+k] = v
	}
	pending, err := t.send(slot, "invoke", fields)
	if err != nil {
		return PooledFrame{}, false, err
	}
	select {
	case result := <-pending:
		return result.frame, false, result.err
	case <-ctx.Done():
		grace, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closed, err := t.Call(grace, slot, "interrupt", nil)
		return PooledFrame{}, err == nil && closed.Fields["reap"] == "false", ctx.Err()
	}
}
