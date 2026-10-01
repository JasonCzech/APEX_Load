package grpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type diagnosticContextKey struct{}

type rpcDiagnosticRecorder struct {
	mu               sync.Mutex
	started          time.Time
	ended            time.Time
	data             protocol.GRPCDiagnostics
	terminalObserved bool
}

func beginRPC(ctx context.Context, method protoreflect.MethodDescriptor) context.Context {
	r, _ := ctx.Value(diagnosticContextKey{}).(*rpcDiagnosticRecorder)
	if r == nil {
		return ctx
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = time.Now()
	r.data.Method = fullMethodName(method)
	r.data.Mode = "unary"
	switch {
	case method.IsStreamingClient() && method.IsStreamingServer():
		r.data.Mode = "bidi_stream"
	case method.IsStreamingClient():
		r.data.Mode = "client_stream"
	case method.IsStreamingServer():
		r.data.Mode = "server_stream"
	}
	if deadline, ok := ctx.Deadline(); ok {
		ms := max(0, float64(deadline.Sub(r.started))/float64(time.Millisecond))
		r.data.DeadlineBudgetMs = &ms
	}
	return ctx
}

// Only the authored RPC receives a recorder. Reflection and connection events
// must not appear in the authored method's message counts or first-response time.
type rpcStatsHandler struct{}

func (rpcStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	r, _ := ctx.Value(diagnosticContextKey{}).(*rpcDiagnosticRecorder)
	if r != nil {
		r.mu.Lock()
		matches := !r.started.IsZero() && r.data.Method == info.FullMethodName
		r.mu.Unlock()
		if matches {
			return ctx
		}
	}
	return context.WithValue(ctx, diagnosticContextKey{}, (*rpcDiagnosticRecorder)(nil))
}

func (rpcStatsHandler) HandleRPC(ctx context.Context, event stats.RPCStats) {
	r, _ := ctx.Value(diagnosticContextKey{}).(*rpcDiagnosticRecorder)
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e := event.(type) {
	case *stats.Begin:
		// A transparent retry begins a new attempt; only its final status applies.
		if !r.terminalObserved {
			r.ended = time.Time{}
			r.data.StatusCode = nil
		}
	case *stats.OutPayload:
		r.data.MessagesSent++
		r.data.RequestBytes += int64(e.Length)
	case *stats.InPayload:
		r.data.MessagesReceived++
		r.data.ResponseBytes += int64(e.Length)
		if r.data.FirstMessageMs == nil {
			ms := max(0, float64(e.RecvTime.Sub(r.started))/float64(time.Millisecond))
			r.data.FirstMessageMs = &ms
		}
	case *stats.End:
		if !r.terminalObserved {
			r.ended = e.EndTime
			code := int32(status.Code(e.Error))
			r.data.StatusCode = &code
		}
	}
}

func (rpcStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (rpcStatsHandler) HandleConn(context.Context, stats.ConnStats)                       {}

// End statistics can arrive after RecvMsg returns. Record terminal transport
// observations synchronously too, without deriving RPC status from later JSON
// decoding, extraction, or assertion failures.
func recordTransportEnd(ctx context.Context, err error) {
	r, _ := ctx.Value(diagnosticContextKey{}).(*rpcDiagnosticRecorder)
	if r == nil {
		return
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	transportStatus, transportError := status.FromError(err)
	code := int32(transportStatus.Code())
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = int32(status.FromContextError(err).Code())
	} else if err != nil && !transportError {
		// The send helper can also fail while decoding an authored message.
		// Such local failures do not establish an RPC status.
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.terminalObserved = true
	r.ended = time.Now()
	r.data.StatusCode = &code
}

func markReceiveLimit(ctx context.Context) {
	if r, _ := ctx.Value(diagnosticContextKey{}).(*rpcDiagnosticRecorder); r != nil {
		r.mu.Lock()
		r.data.ReceiveLimitReached = true
		r.mu.Unlock()
	}
}

func (r *rpcDiagnosticRecorder) snapshot() *protocol.ServerTelemetry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started.IsZero() {
		return nil
	}
	d := r.data.Clone()
	end := r.ended
	if !r.terminalObserved || d.ReceiveLimitReached || end.IsZero() {
		end = time.Now()
	}
	d.CallMs = max(0, float64(end.Sub(r.started))/float64(time.Millisecond))
	// A receive cap ends observation, not the remote RPC. Local cleanup may
	// already have emitted Canceled; do not present that as a server status.
	if d.ReceiveLimitReached {
		d.StatusCode = nil
	}
	if !d.Valid() {
		return nil
	}
	t := &protocol.ServerTelemetry{
		GRPC: d, MessagesSent: int(d.MessagesSent), MessagesReceived: int(d.MessagesReceived),
		RequestPayloadKB: float64(d.RequestBytes) / 1024, ResponsePayloadKB: float64(d.ResponseBytes) / 1024,
	}
	if d.FirstMessageMs != nil {
		t.FirstEventMs = *d.FirstMessageMs
	}
	return t
}
