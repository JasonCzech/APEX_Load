package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	interop "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type diagnosticTestService struct {
	interop.UnimplementedTestServiceServer
}

func (diagnosticTestService) UnaryCall(ctx context.Context, req *interop.SimpleRequest) (*interop.SimpleResponse, error) {
	if req.ResponseStatus != nil {
		if codes.Code(req.ResponseStatus.Code) == codes.DeadlineExceeded {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, status.Error(codes.Code(req.ResponseStatus.Code), "controlled failure")
	}
	return &interop.SimpleResponse{Payload: req.Payload}, nil
}

func (diagnosticTestService) StreamingOutputCall(req *interop.StreamingOutputCallRequest, stream interop.TestService_StreamingOutputCallServer) error {
	for i := 0; i < 2; i++ {
		if err := stream.Send(&interop.StreamingOutputCallResponse{Payload: req.Payload}); err != nil {
			return err
		}
	}
	if req.ResponseStatus != nil {
		return status.Error(codes.Code(req.ResponseStatus.Code), "controlled stream failure")
	}
	return nil
}

func (diagnosticTestService) StreamingInputCall(stream interop.TestService_StreamingInputCallServer) error {
	if md, _ := metadata.FromIncomingContext(stream.Context()); len(md.Get("x-deny")) > 0 {
		return status.Error(codes.PermissionDenied, "controlled early rejection")
	}
	var size int32
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&interop.StreamingInputCallResponse{AggregatedPayloadSize: size})
		}
		if err != nil {
			return err
		}
		size += int32(len(req.GetPayload().GetBody()))
	}
}

func (diagnosticTestService) FullDuplexCall(stream interop.TestService_FullDuplexCallServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&interop.StreamingOutputCallResponse{Payload: req.Payload}); err != nil {
			return err
		}
	}
}

func diagnosticClient(t *testing.T) *GRPC {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := googlegrpc.NewServer(googlegrpc.InitialWindowSize(65535), googlegrpc.InitialConnWindowSize(65535),
		googlegrpc.StreamInterceptor(func(srv any, stream googlegrpc.ServerStream, _ *googlegrpc.StreamServerInfo, handler googlegrpc.StreamHandler) error {
			if md, _ := metadata.FromIncomingContext(stream.Context()); len(md.Get("x-deny")) > 0 {
				return status.Error(codes.PermissionDenied, "controlled early rejection")
			}
			return handler(srv, stream)
		}))
	interop.RegisterTestServiceServer(server, diagnosticTestService{})
	reflection.Register(server)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	client := &GRPC{}
	if err := client.Init(context.Background(), map[string]any{"base_url": listener.Addr().String(), "grpc_plain_text": true, "grpc_use_reflection": true, "grpc_timeout_ms": 3000}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Teardown(context.Background()) })
	return client
}

func TestGRPCDiagnosticsAllModesAndFailures(t *testing.T) {
	client := diagnosticClient(t)
	for _, tc := range []struct {
		name, kind, method, body string
		params                   map[string]any
		sent, received           int64
		status                   codes.Code
		limited                  bool
	}{
		{"unary", "unary", "UnaryCall", `{"payload":{"body":"eA=="}}`, nil, 1, 1, codes.OK, false},
		{"unary error", "unary", "UnaryCall", `{"responseStatus":{"code":7}}`, nil, 1, 0, codes.PermissionDenied, false},
		{"server", "server_stream", "StreamingOutputCall", `{"payload":{"body":"eA=="}}`, map[string]any{"max_messages": 3}, 1, 2, codes.OK, false},
		{"server partial error", "server_stream", "StreamingOutputCall", `{"responseStatus":{"code":14}}`, map[string]any{"max_messages": 3}, 1, 2, codes.Unavailable, false},
		{"server limited", "server_stream", "StreamingOutputCall", `{}`, map[string]any{"max_messages": 1}, 1, 1, codes.OK, true},
		{"client", "client_stream", "StreamingInputCall", `{}`, map[string]any{"messages": []string{`{"payload":{"body":"eA=="}}`, `{}`}}, 2, 1, codes.OK, false},
		{"bidi", "bidi_stream", "FullDuplexCall", `{}`, map[string]any{"messages": []string{`{}`, `{}`}, "max_messages": 3}, 2, 2, codes.OK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := client.Execute(context.Background(), &protocol.Action{Type: "grpc_" + tc.kind, Target: "grpc.testing.TestService/" + tc.method, Body: []byte(tc.body), Params: tc.params})
			if err != nil || result.Success != (tc.status == codes.OK) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if result.ServerTelemetry == nil || result.ServerTelemetry.GRPC == nil {
				t.Fatalf("missing diagnostics: %+v", result)
			}
			d := result.ServerTelemetry.GRPC
			if d.Method != "/grpc.testing.TestService/"+tc.method || d.Mode != tc.kind || d.MessagesSent != tc.sent || d.MessagesReceived != tc.received || d.CallMs <= 0 || d.DeadlineBudgetMs == nil {
				t.Fatalf("diagnostics=%+v", d)
			}
			if (d.FirstMessageMs != nil) != (tc.received > 0) || d.ReceiveLimitReached != tc.limited {
				t.Fatalf("availability=%+v", d)
			}
			if tc.limited && d.StatusCode != nil || !tc.limited && (d.StatusCode == nil || *d.StatusCode != int32(tc.status)) {
				t.Fatalf("status=%v want %v (limited=%v)", d.StatusCode, tc.status, tc.limited)
			}
			if result.ServerTelemetry.ServerProcessingMs != 0 || result.ServerTelemetry.TransferMs != 0 {
				t.Fatal("client measurements must not fabricate server or transport phases")
			}
		})
	}
}

func TestGRPCDiagnosticsDeadlineAndCancellation(t *testing.T) {
	client := diagnosticClient(t)
	// Resolve outside the short deadline so the failure exercises the call itself.
	if _, err := client.resolveMethod(context.Background(), "grpc.testing.TestService/UnaryCall"); err != nil {
		t.Fatal(err)
	}
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan *protocol.Result, 1)
		go func() {
			r, _ := client.Execute(ctx, &protocol.Action{Type: "grpc_unary", Target: "grpc.testing.TestService/UnaryCall", Body: []byte(`{"responseStatus":{"code":4}}`), Params: map[string]any{"timeout_ms": 150}})
			done <- r
		}()
		if cancelled {
			time.AfterFunc(50*time.Millisecond, cancel)
		}
		r := <-done
		cancel()
		want := codes.DeadlineExceeded
		if cancelled {
			want = codes.Canceled
		}
		if r.Success || r.ServerTelemetry == nil || r.ServerTelemetry.GRPC == nil {
			t.Fatalf("result=%+v", r)
		}
		d := r.ServerTelemetry.GRPC
		if d.StatusCode == nil || *d.StatusCode != int32(want) || d.FirstMessageMs != nil || d.MessagesReceived != 0 {
			t.Fatalf("deadline diagnostics=%+v want %v", d, want)
		}
	}
}

func TestGRPCBidiReadsWhileSendingLargeMessages(t *testing.T) {
	client := diagnosticClient(t)
	req := &interop.StreamingOutputCallRequest{Payload: &interop.Payload{Body: bytes.Repeat([]byte("x"), 512<<10)}}
	body, err := protojson.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]string, 6)
	for i := range messages {
		messages[i] = string(body)
	}
	r, err := client.Execute(context.Background(), &protocol.Action{Type: "grpc_bidi_stream", Target: "grpc.testing.TestService/FullDuplexCall", Params: map[string]any{"messages": messages, "max_messages": 7}})
	if err != nil || !r.Success {
		t.Fatalf("full duplex transfer failed: result=%+v err=%v", r, err)
	}
	d := r.ServerTelemetry.GRPC
	responseBytes := 6 * proto.Size(&interop.StreamingOutputCallResponse{Payload: req.Payload})
	if d.MessagesSent != 6 || d.MessagesReceived != 6 || d.RequestBytes != int64(6*proto.Size(req)) || d.ResponseBytes != int64(responseBytes) {
		t.Fatalf("payload accounting=%+v", d)
	}
	var responses []json.RawMessage
	if err := json.Unmarshal([]byte(r.ResponseBody), &responses); err != nil || len(responses) != 6 {
		t.Fatalf("response count=%d err=%v", len(responses), err)
	}
}

func TestGRPCAssertionFailureDoesNotRewriteRPCStatus(t *testing.T) {
	client := diagnosticClient(t)
	r, err := client.Execute(context.Background(), &protocol.Action{
		Type: "grpc_unary", Target: "grpc.testing.TestService/UnaryCall", Body: []byte(`{}`),
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "absent-from-response"}},
	})
	if err != nil || r.Success || r.ServerTelemetry == nil || r.ServerTelemetry.GRPC == nil {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if status := r.ServerTelemetry.GRPC.StatusCode; status == nil || *status != int32(codes.OK) {
		t.Fatalf("assertion replaced observed RPC status: %v", status)
	}
}

func TestGRPCStreamByteLimitMarksPartialObservation(t *testing.T) {
	client := diagnosticClient(t)
	req := &interop.StreamingOutputCallRequest{Payload: &interop.Payload{Body: bytes.Repeat([]byte("x"), 3<<20)}}
	body, err := protojson.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// Each response fits the transport limit, but their JSON representation
	// exceeds the bounded response array before the client reads terminal status.
	result, err := client.Execute(context.Background(), &protocol.Action{
		Type: "grpc_server_stream", Target: "grpc.testing.TestService/StreamingOutputCall", Body: body,
		Params: map[string]any{"max_messages": 3},
	})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, "streamed response exceeds") {
		t.Fatalf("expected response byte limit: success=%v error=%q err=%v", result.Success, result.ErrorMessage, err)
	}
	if result.ServerTelemetry == nil || result.ServerTelemetry.GRPC == nil {
		t.Fatal("missing diagnostics for partial response")
	}
	d := result.ServerTelemetry.GRPC
	wantBytes := int64(2 * proto.Size(&interop.StreamingOutputCallResponse{Payload: req.Payload}))
	if !d.ReceiveLimitReached || d.StatusCode != nil || d.MessagesReceived != 2 || d.ResponseBytes != wantBytes || d.FirstMessageMs == nil {
		t.Fatalf("partial observation=%+v, want unavailable status and %d received bytes", d, wantBytes)
	}
}

func TestGRPCBidiInvalidLaterMessageCancelsReceiverAndPreservesPartialEvidence(t *testing.T) {
	client := diagnosticClient(t)
	start := time.Now()
	r, err := client.Execute(context.Background(), &protocol.Action{
		Type: "grpc_bidi_stream", Target: "grpc.testing.TestService/FullDuplexCall",
		Params: map[string]any{"messages": []string{`{}`, `{not JSON`}, "max_messages": 3},
	})
	if err != nil || r.Success || !strings.Contains(r.ErrorMessage, "decode grpc request body") {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("invalid sender left receiver waiting for the 3-second RPC deadline")
	}
	if r.ServerTelemetry == nil || r.ServerTelemetry.GRPC.MessagesSent != 1 || r.BytesSent != 2 {
		t.Fatalf("partial transfer accounting=%+v", r)
	}
}

func TestGRPCStreamEarlyRejectionRetainsTerminalStatus(t *testing.T) {
	client := diagnosticClient(t)
	body, err := protojson.Marshal(&interop.StreamingInputCallRequest{Payload: &interop.Payload{Body: bytes.Repeat([]byte("x"), 512<<10)}})
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]string, 6)
	for i := range messages {
		messages[i] = string(body)
	}
	for _, tc := range []struct{ kind, method string }{
		{"grpc_client_stream", "StreamingInputCall"}, {"grpc_server_stream", "StreamingOutputCall"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			r, err := client.Execute(context.Background(), &protocol.Action{
				Type: tc.kind, Target: "grpc.testing.TestService/" + tc.method, Body: body,
				Headers: map[string]string{"x-deny": "true"}, Params: map[string]any{"messages": messages},
			})
			if err != nil || r.Success || r.StatusCode != int(codes.PermissionDenied) {
				t.Fatalf("early rejection lost RPC status: result=%+v err=%v", r, err)
			}
			if r.ServerTelemetry == nil || r.ServerTelemetry.GRPC.StatusCode == nil || *r.ServerTelemetry.GRPC.StatusCode != int32(codes.PermissionDenied) {
				t.Fatalf("missing terminal diagnostic: %+v", r.ServerTelemetry)
			}
		})
	}
}

func TestGRPCObservedTerminalStatusWinsOverDelayedAttemptStatistics(t *testing.T) {
	r := &rpcDiagnosticRecorder{started: time.Now().Add(-time.Second), data: protocol.GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary"}}
	ctx := context.WithValue(context.Background(), diagnosticContextKey{}, r)
	recordTransportEnd(ctx, status.Error(codes.PermissionDenied, "denied"))
	rpcStatsHandler{}.HandleRPC(ctx, &stats.End{EndTime: time.Now(), Error: context.Canceled})
	d := r.snapshot().GRPC
	if d.StatusCode == nil || *d.StatusCode != int32(codes.PermissionDenied) {
		t.Fatalf("late attempt/cleanup replaced observed status: %+v", d)
	}
}

func TestGRPCReceiveLimitUsesFullObservationWindow(t *testing.T) {
	first := 5.0
	start := time.Now().Add(-time.Second)
	r := &rpcDiagnosticRecorder{started: start, ended: start.Add(time.Millisecond), data: protocol.GRPCDiagnostics{
		Method: "/example.Service/Watch", Mode: "bidi_stream", FirstMessageMs: &first, MessagesReceived: 1, ReceiveLimitReached: true,
	}}
	telemetry := r.snapshot()
	if telemetry == nil || telemetry.GRPC.CallMs < first || telemetry.GRPC.StatusCode != nil {
		t.Fatalf("partial observation lost to an earlier transport end: %+v", telemetry)
	}
}

func TestGRPCClientStreamDecodeFailureDoesNotInventRPCStatus(t *testing.T) {
	client := diagnosticClient(t)
	result, err := client.Execute(context.Background(), &protocol.Action{
		Type: "grpc_client_stream", Target: "grpc.testing.TestService/StreamingInputCall",
		Params: map[string]any{"messages": []string{`{}`, `{"payload":false}`}},
	})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, "decode grpc request body") {
		t.Fatalf("expected local decode failure: result=%+v err=%v", result, err)
	}
	if result.ServerTelemetry == nil || result.ServerTelemetry.GRPC == nil {
		t.Fatal("partial diagnostic evidence was lost")
	}
	d := result.ServerTelemetry.GRPC
	if d.MessagesSent != 1 || d.StatusCode != nil || d.FirstMessageMs != nil {
		t.Fatalf("local decode failure invented a transport outcome: %+v", d)
	}
}
