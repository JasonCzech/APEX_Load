package grpc

import (
	"context"
	"fmt"
	"time"

	googlegrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Per-call protobuf codec timing measures only client encoding/decoding work.
// It excludes JSON DSL conversion, compression, framing and server processing.
// Reflection uses the connection's ordinary codec and cannot affect the call.
type diagnosticCodec struct{ recorder *rpcDiagnosticRecorder }

func (d diagnosticCodec) Name() string { return "proto" }
func (d diagnosticCodec) Marshal(value any) ([]byte, error) {
	message, ok := value.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("gRPC request is not a protobuf message")
	}
	start := time.Now()
	data, err := proto.Marshal(message)
	d.observe(true, time.Since(start))
	return data, err
}
func (d diagnosticCodec) Unmarshal(data []byte, value any) error {
	message, ok := value.(proto.Message)
	if !ok {
		return fmt.Errorf("gRPC response is not a protobuf message")
	}
	start := time.Now()
	err := proto.Unmarshal(data, message)
	d.observe(false, time.Since(start))
	return err
}
func (d diagnosticCodec) observe(encode bool, elapsed time.Duration) {
	if d.recorder == nil {
		return
	}
	d.recorder.mu.Lock()
	defer d.recorder.mu.Unlock()
	value := float64(elapsed) / float64(time.Millisecond)
	field := &d.recorder.data.DecodeMs
	if encode {
		field = &d.recorder.data.EncodeMs
	}
	if *field == nil {
		*field = new(float64)
	}
	**field += value
}
func rpcCodecOption(ctx context.Context) googlegrpc.CallOption {
	recorder, _ := ctx.Value(diagnosticContextKey{}).(*rpcDiagnosticRecorder)
	return googlegrpc.ForceCodec(diagnosticCodec{recorder: recorder})
}
