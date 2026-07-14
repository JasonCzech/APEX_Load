package generatorrpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type metricsSetupObservation struct {
	ctx         context.Context
	hasDeadline bool
}

type metricsSetupConn struct {
	block       bool
	observation chan metricsSetupObservation
	mu          sync.Mutex
	closeCalls  int
}

func (c *metricsSetupConn) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	return errors.New("unary RPC is not implemented")
}

func (c *metricsSetupConn) NewStream(ctx context.Context, _ *grpc.StreamDesc, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	_, hasDeadline := ctx.Deadline()
	c.observation <- metricsSetupObservation{ctx: ctx, hasDeadline: hasDeadline}
	if c.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &metricsSetupClientStream{ctx: ctx, onClose: func() {
		c.mu.Lock()
		c.closeCalls++
		c.mu.Unlock()
	}}, nil
}

func (c *metricsSetupConn) closes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls
}

type metricsSetupClientStream struct {
	ctx     context.Context
	onClose func()
}

func (s *metricsSetupClientStream) Header() (metadata.MD, error) { return nil, nil }
func (s *metricsSetupClientStream) Trailer() metadata.MD         { return nil }
func (s *metricsSetupClientStream) CloseSend() error {
	if s.onClose != nil {
		s.onClose()
	}
	return nil
}
func (s *metricsSetupClientStream) Context() context.Context { return s.ctx }
func (s *metricsSetupClientStream) SendMsg(any) error        { return nil }
func (s *metricsSetupClientStream) RecvMsg(any) error        { return io.EOF }

func TestOpenMetricsStreamSetupTimeoutDoesNotBecomeLifetimeDeadline(t *testing.T) {
	conn := &metricsSetupConn{block: true, observation: make(chan metricsSetupObservation, 1)}
	client := &Client{rpc: pb.NewGeneratorControlClient(conn)}

	started := time.Now()
	stream, err := client.OpenMetricsStreamWithSetupTimeout(context.Background(), 25*time.Millisecond)
	if stream != nil {
		t.Fatal("OpenMetricsStreamWithSetupTimeout returned a stream after setup timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("OpenMetricsStreamWithSetupTimeout error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("setup timeout returned after %v, want bounded return", elapsed)
	}

	observation := <-conn.observation
	if observation.hasDeadline {
		t.Fatal("metrics stream lifetime context inherited the setup deadline")
	}
	select {
	case <-observation.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("setup timeout did not cancel the pending stream creation")
	}
}

func TestOpenMetricsStreamCloseCancelsLifetimeContextOnce(t *testing.T) {
	conn := &metricsSetupConn{observation: make(chan metricsSetupObservation, 1)}
	client := &Client{rpc: pb.NewGeneratorControlClient(conn)}

	stream, err := client.OpenMetricsStreamWithSetupTimeout(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("OpenMetricsStreamWithSetupTimeout: %v", err)
	}
	observation := <-conn.observation
	if observation.hasDeadline {
		t.Fatal("metrics stream lifetime context inherited the setup deadline")
	}
	select {
	case <-observation.ctx.Done():
		t.Fatal("stream lifetime was canceled before CloseSend")
	default:
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("second CloseSend: %v", err)
	}
	select {
	case <-observation.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("CloseSend did not cancel the stream lifetime context")
	}
	if got := conn.closes(); got != 1 {
		t.Fatalf("underlying CloseSend calls = %d, want 1", got)
	}
}
