package javarunner

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPooledReadyRequiresEventAttribution(t *testing.T) {
	for _, contract := range []string{"", "legacy", "slot_thread_v1"} {
		frame := PooledFrame{Operation: "ready", Fields: map[string]string{"code": "ok", "reap": "false", "sealed": "false"}}
		if contract != "" {
			frame.Fields["event_attribution"] = contract
		}
		if validPooledReply(frame) != (contract == "slot_thread_v1") {
			t.Fatalf("attribution contract %q not enforced", contract)
		}
	}
}

func TestPooledTransportRejectsMismatchedResponse(t *testing.T) {
	for _, mutate := range []string{"slot", "generation", "operation", "sequence", "code", "replay"} {
		t.Run(mutate, func(t *testing.T) {
			clientRead, serverWrite := io.Pipe()
			serverRead, clientWrite := io.Pipe()
			defer serverRead.Close()
			defer serverWrite.Close()
			var failures atomic.Int32
			notified := make(chan struct{})
			transport, err := NewPooledTransport(clientRead, clientWrite, "generation", make([]byte, 32), func(error) { failures.Add(1); close(notified) })
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				secret := make([]byte, 32)
				f, e := ReadPooledFrame(serverRead, secret, false)
				if e != nil {
					return
				}
				f.Fields = map[string]string{"code": "ok", "reap": "false", "sealed": "false", "event_attribution": "slot_thread_v1"}
				switch mutate {
				case "slot":
					f.Slot = "other"
				case "generation":
					f.Generation = "other"
				case "operation":
					f.Operation = "health"
				case "sequence":
					f.Sequence = 2
					f.Request = "2"
				case "code":
					f.Fields["code"] = "untrusted-detail"
				}
				wire, _ := EncodePooledFrame(f, secret, true)
				_, _ = serverWrite.Write(wire)
				if mutate == "replay" {
					_, _ = serverWrite.Write(wire)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = transport.Call(ctx, "", "ready", nil)
			if mutate != "replay" && !errors.Is(err, ErrPooledHostReaped) {
				t.Fatalf("%v", err)
			}
			select {
			case <-transport.Done():
			case <-ctx.Done():
				t.Fatal("host not reaped")
			}
			<-done
			select {
			case <-notified:
			case <-ctx.Done():
				t.Fatal("cleanup callback not delivered")
			}
			transport.Close()
			if failures.Load() != 1 {
				t.Fatalf("failure count %d", failures.Load())
			}
		})
	}
}
func TestPooledBlockedWriterDoesNotBlockDeadline(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer serverRead.Close()
	defer serverWrite.Close()
	reaped := make(chan error, 1)
	transport, err := NewPooledTransport(clientRead, clientWrite, "generation", make([]byte, 32), func(e error) { reaped <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = transport.Call(ctx, "", "ready", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-reaped:
	case <-time.After(time.Second):
		t.Fatal("blocked write delayed cleanup")
	}
}
func TestPooledQueueOverflowFailsClosed(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer serverRead.Close()
	defer serverWrite.Close()
	transport, err := NewPooledTransport(clientRead, clientWrite, "generation", make([]byte, 32), func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	for i := 0; i < 100; i++ {
		_, err = transport.send("", "health", nil)
		if err != nil {
			break
		}
	}
	if !errors.Is(err, ErrPooledHostReaped) {
		t.Fatal("overflow did not retire host", err)
	}
}

func TestPooledTransportQueuesScaleWithHostSlots(t *testing.T) {
	for _, tc := range []struct{ slots, writes, pending int }{{1, 66, 512}, {112, 288, 512}, {256, 576, 1088}, {MaxPooledSlotsPerHost, 2112, 4160}} {
		if writes, pending := pooledTransportBounds(tc.slots); writes != tc.writes || pending != tc.pending {
			t.Fatalf("bounds(%d) = %d/%d, want %d/%d", tc.slots, writes, pending, tc.writes, tc.pending)
		}
	}
	for _, slots := range []int{0, -1, MaxPooledSlotsPerHost + 1} {
		if _, err := NewPooledTransportForSlots(io.NopCloser(strings.NewReader("")), nopWriteCloser{}, "generation", make([]byte, 32), slots, func(error) {}); !errors.Is(err, ErrPooledFrame) {
			t.Fatalf("slots %d accepted: %v", slots, err)
		}
	}
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer serverRead.Close()
	defer serverWrite.Close()
	reaped := make(chan error, 1)
	transport, err := NewPooledTransportForSlots(clientRead, clientWrite, "generation", make([]byte, 32), MaxPooledSlotsPerHost, func(e error) { reaped <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	// Every slot submits an invocation and a closure while the JVM is not
	// reading: a full-host synchronized burst must queue, not reap the host.
	for i := 0; i < 2*MaxPooledSlotsPerHost; i++ {
		if _, err = transport.send("", "health", nil); err != nil {
			t.Fatalf("burst frame %d retired the host: %v", i, err)
		}
	}
	select {
	case cause := <-reaped:
		t.Fatalf("burst reaped host: %v", cause)
	default:
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

func TestPooledResponseSchema(t *testing.T) {
	good := PooledFrame{Operation: "invoke", Fields: map[string]string{"code": "ok", "reap": "false", "return_code": "0", "event_fallback": "false", "event_truncated": "false", "exit_option": "1", "exit_status": "-17"}}
	if !validPooledReply(good) {
		t.Fatal("valid controlled exit rejected")
	}
	for _, change := range []func(map[string]string){func(f map[string]string) { delete(f, "exit_status") }, func(f map[string]string) { f["exit_option"] = "7" }, func(f map[string]string) { f["return_code"] = "2147483648" }, func(f map[string]string) { f["event_truncated"] = "unknown" }, func(f map[string]string) { f["details"] = "untrusted" }} {
		f := good
		f.Fields = make(map[string]string)
		for k, v := range good.Fields {
			f.Fields[k] = v
		}
		change(f.Fields)
		if validPooledReply(f) {
			t.Fatal("invalid result accepted")
		}
	}
	for _, diagnostics := range []string{"!!!", "Zh=="} {
		f := PooledFrame{Operation: "health", Fields: map[string]string{"code": "ok", "reap": "false", "sealed": "false", "diagnostics_base64": diagnostics, "diagnostics_dropped": "0"}}
		if validPooledReply(f) {
			t.Fatal("invalid diagnostics accepted")
		}
	}
}

func TestPooledOversizedParametersRejectedBeforeAdmission(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer serverRead.Close()
	defer serverWrite.Close()
	tr, err := NewPooledTransport(clientRead, clientWrite, "generation", make([]byte, 32), func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	fields := make(map[string]string)
	for i := 0; i < pooledMaxFields; i++ {
		fields[strconv.Itoa(i)] = "value"
	}
	if _, err = tr.Invoke(context.Background(), "slot", "action", fields); !errors.Is(err, ErrPooledFrame) {
		t.Fatal(err)
	}
	fields["extra"] = "value"
	if _, err = tr.Call(context.Background(), "slot", "open", fields); !errors.Is(err, ErrPooledFrame) {
		t.Fatal(err)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.sequence != 0 || len(tr.pending) != 0 || tr.stopped {
		t.Fatal("invalid local request mutated host")
	}
}
