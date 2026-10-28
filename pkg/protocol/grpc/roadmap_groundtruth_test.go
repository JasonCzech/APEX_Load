package grpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/grpcclient"
	"github.com/jasonczech/apexload/pkg/protocol"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	interop "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// These literal protobuf encodings are the independent byte oracle. The
// expected lengths are not obtained from the instrumented client or proto.Size.
// Payload.body is field 2; request payload is field 3 (unary/output stream) or
// field 1 (input stream), response payload is field 1.
const groundTruthBody = `{"payload":{"body":"AP+AeA=="}}`

var groundTruthPayload = []byte{0, 255, 128, 'x'}

type groundTruthService struct {
	interop.UnimplementedTestServiceServer
	mu                    sync.Mutex
	received, sent, calls int
	failures              int
	abortAfterInput       int
	blockUntilDone        bool
	wrongPayload          bool
}

func (s *groundTruthService) receive(payload *interop.Payload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received++
	if !bytes.Equal(payload.GetBody(), groundTruthPayload) {
		s.wrongPayload = true
	}
}
func (s *groundTruthService) send() { s.mu.Lock(); s.sent++; s.mu.Unlock() }
func (s *groundTruthService) UnaryCall(ctx context.Context, req *interop.SimpleRequest) (*interop.SimpleResponse, error) {
	s.receive(req.Payload)
	s.mu.Lock()
	s.calls++
	fail := s.calls <= s.failures
	block := s.blockUntilDone
	s.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if fail {
		return nil, status.Error(codes.Unavailable, "controlled retry fixture")
	}
	s.send()
	return &interop.SimpleResponse{Payload: &interop.Payload{Body: append([]byte(nil), groundTruthPayload...)}}, nil
}
func (s *groundTruthService) StreamingOutputCall(req *interop.StreamingOutputCallRequest, stream interop.TestService_StreamingOutputCallServer) error {
	s.receive(req.Payload)
	for range 2 {
		if err := stream.Send(&interop.StreamingOutputCallResponse{Payload: req.Payload}); err != nil {
			return err
		}
		s.send()
	}
	return nil
}
func (s *groundTruthService) StreamingInputCall(stream interop.TestService_StreamingInputCallServer) error {
	var size int32
	var msgCount int
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			if err = stream.SendAndClose(&interop.StreamingInputCallResponse{AggregatedPayloadSize: size}); err == nil {
				s.send()
			}
			return err
		}
		if err != nil {
			return err
		}
		s.receive(req.Payload)
		msgCount++
		s.mu.Lock()
		abortAt := s.abortAfterInput
		s.mu.Unlock()
		if abortAt > 0 && msgCount >= abortAt {
			return status.Error(codes.ResourceExhausted, "controlled partial input stream abort")
		}
		size += int32(len(req.Payload.GetBody()))
	}
}
func (s *groundTruthService) FullDuplexCall(stream interop.TestService_FullDuplexCallServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		s.receive(req.Payload)
		if err = stream.Send(&interop.StreamingOutputCallResponse{Payload: req.Payload}); err != nil {
			return err
		}
		s.send()
	}
}
func groundTruthClient(t *testing.T, failures int, retry bool) (*GRPC, *groundTruthService) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &groundTruthService{failures: failures}
	server := googlegrpc.NewServer()
	interop.RegisterTestServiceServer(server, service)
	reflection.Register(server)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	client := &GRPC{}
	if err := client.Init(context.Background(), map[string]any{"base_url": listener.Addr().String(), "grpc_plain_text": true, "grpc_use_reflection": true, "grpc_timeout_ms": 3000}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Teardown(context.Background()) })
	if retry {
		// Test-only resolver policy exercises grpc-go attempt statistics. The
		// product does not gain a new authored retry option from this fixture.
		_ = client.conn.Close()
		conn, err := grpcclient.New(listener.Addr().String(), googlegrpc.WithTransportCredentials(insecure.NewCredentials()), googlegrpc.WithStatsHandler(rpcStatsHandler{}), googlegrpc.WithDefaultServiceConfig(`{"methodConfig":[{"name":[{"service":"grpc.testing.TestService","method":"UnaryCall"}],"retryPolicy":{"maxAttempts":3,"initialBackoff":"0.01s","maxBackoff":"0.01s","backoffMultiplier":1,"retryableStatusCodes":["UNAVAILABLE"]}}]}`))
		if err != nil {
			t.Fatal(err)
		}
		client.conn = conn
	}
	return client, service
}
func assertGroundTruth(t *testing.T, result *protocol.Result, service *groundTruthService, sent, received, requestBytes, responseBytes int64, code codes.Code) {
	t.Helper()
	if result == nil || result.ServerTelemetry == nil || result.ServerTelemetry.GRPC == nil {
		t.Fatal("missing client observations")
	}
	d := result.ServerTelemetry.GRPC
	if !d.Valid() || d.MessagesSent != sent || d.MessagesReceived != received || d.RequestBytes != requestBytes || d.ResponseBytes != responseBytes || d.StatusCode == nil || *d.StatusCode != int32(code) {
		t.Fatalf("client observation differs from literal payload/count oracle: %+v", d)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.wrongPayload || int64(service.received) != sent || int64(service.sent) != received {
		t.Fatalf("independent endpoint counts differ: received=%d sent=%d wrong=%v", service.received, service.sent, service.wrongPayload)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "DO_NOT_RETAIN_GRPC_CREDENTIAL") || strings.Contains(string(b), "AP+AeA==") {
		t.Fatal("numeric diagnostics retained source content")
	}
}
func TestGRPCDiagnosticsLiteralWirePayloadGroundTruth(t *testing.T) {
	// Verify schema interpretation against the fixed independent wire fixtures.
	for _, tc := range []struct {
		encoded string
		message proto.Message
	}{
		{"1a06120400ff8078", &interop.SimpleRequest{Payload: &interop.Payload{Body: groundTruthPayload}}},
		{"1a06120400ff8078", &interop.StreamingOutputCallRequest{Payload: &interop.Payload{Body: groundTruthPayload}}},
		{"0a06120400ff8078", &interop.StreamingInputCallRequest{Payload: &interop.Payload{Body: groundTruthPayload}}},
		{"0a06120400ff8078", &interop.SimpleResponse{Payload: &interop.Payload{Body: groundTruthPayload}}},
		{"0a06120400ff8078", &interop.StreamingOutputCallResponse{Payload: &interop.Payload{Body: groundTruthPayload}}},
		{"0808", &interop.StreamingInputCallResponse{AggregatedPayloadSize: 8}},
	} {
		expected, err := hex.DecodeString(tc.encoded)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := proto.Marshal(tc.message)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("literal protobuf fixture changed for %T: %x %v", tc.message, actual, err)
		}
	}
	for _, tc := range []struct {
		kind, method                                string
		sent, received, requestBytes, responseBytes int64
		params                                      map[string]any
	}{
		{"unary", "UnaryCall", 1, 1, 8, 8, nil},
		{"server_stream", "StreamingOutputCall", 1, 2, 8, 16, map[string]any{"max_messages": 3}},
		{"client_stream", "StreamingInputCall", 2, 1, 16, 2, map[string]any{"messages": []string{groundTruthBody, groundTruthBody}}},
		{"bidi_stream", "FullDuplexCall", 2, 2, 16, 16, map[string]any{"messages": []string{groundTruthBody, groundTruthBody}, "max_messages": 3}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			client, service := groundTruthClient(t, 0, false)
			result, err := client.Execute(context.Background(), &protocol.Action{Type: "grpc_" + tc.kind, Target: "grpc.testing.TestService/" + tc.method, Body: []byte(groundTruthBody), Params: tc.params, Headers: map[string]string{"authorization": "DO_NOT_RETAIN_GRPC_CREDENTIAL"}})
			if err != nil || !result.Success {
				t.Fatalf("execute %v %+v", err, result)
			}
			assertGroundTruth(t, result, service, tc.sent, tc.received, tc.requestBytes, tc.responseBytes, codes.OK)
		})
	}
}
func TestGRPCDiagnosticsRetriesKeepAttemptPayloadsAndFinalStatus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures int
		retry    bool
		attempts int64
		code     codes.Code
	}{
		{"default-no-configured-retry", 2, false, 1, codes.Unavailable},
		{"configured-library-retry-success", 2, true, 3, codes.OK},
		{"configured-library-retry-exhausted", 3, true, 3, codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, service := groundTruthClient(t, tc.failures, tc.retry)
			result, err := client.Execute(context.Background(), &protocol.Action{Type: "grpc_unary", Target: "grpc.testing.TestService/UnaryCall", Body: []byte(groundTruthBody)})
			if err != nil {
				t.Fatal(err)
			}
			responses := int64(0)
			if tc.code == codes.OK {
				responses = 1
			}
			assertGroundTruth(t, result, service, tc.attempts, responses, tc.attempts*8, responses*8, tc.code)
			if result.Success != (tc.code == codes.OK) {
				t.Fatal("outcome mismatch")
			}
			if responses == 0 && result.ServerTelemetry.GRPC.FirstMessageMs != nil {
				t.Fatal("failed attempts invented first response")
			}
		})
	}
}
func TestGRPCDiagnosticsConcurrentCallsRetainIndependentPopulations(t *testing.T) {
	client, service := groundTruthClient(t, 0, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const calls = 12
	results := make(chan *protocol.Result, calls)
	errs := make(chan error, calls)
	var wg sync.WaitGroup
	for range calls {
		wg.Go(func() {
			r, err := client.Execute(ctx, &protocol.Action{Type: "grpc_unary", Target: "grpc.testing.TestService/UnaryCall", Body: []byte(groundTruthBody)})
			results <- r
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var sent, received int64
	for r := range results {
		if r == nil || !r.Success || r.ServerTelemetry == nil || r.ServerTelemetry.GRPC == nil {
			t.Fatal("missing concurrent observation")
		}
		d := r.ServerTelemetry.GRPC
		if !d.Valid() || d.StatusCode == nil || *d.StatusCode != int32(codes.OK) || d.MessagesSent != 1 || d.MessagesReceived != 1 || d.RequestBytes != 8 || d.ResponseBytes != 8 || d.EncodeMs == nil || d.DecodeMs == nil {
			t.Fatalf("call populations crossed: %+v", d)
		}
		sent += d.MessagesSent
		received += d.MessagesReceived
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if sent != calls || received != calls || service.received != calls || service.sent != calls || service.wrongPayload {
		t.Fatal("concurrent endpoint accounting mismatch")
	}
}

func TestGRPCControlledEndpointStreamingOverlapPartialSendAndClientAnalysis(t *testing.T) {
	// 1. Locally capped server stream (max_messages=1 while server sends 2).
	t.Run("locally_capped_stream", func(t *testing.T) {
		client, service := groundTruthClient(t, 0, false)
		res, err := client.Execute(context.Background(), &protocol.Action{
			Type:   "grpc_server_stream",
			Target: "grpc.testing.TestService/StreamingOutputCall",
			Body:   []byte(groundTruthBody),
			Params: map[string]any{"max_messages": 1},
		})
		if err != nil || !res.Success || res.ServerTelemetry == nil || res.ServerTelemetry.GRPC == nil {
			t.Fatalf("locally capped stream failed: %+v err=%v", res, err)
		}
		d := res.ServerTelemetry.GRPC
		if !d.Valid() || !d.ReceiveLimitReached || d.StatusCode != nil || d.MessagesSent != 1 || d.MessagesReceived != 1 || d.RequestBytes != 8 || d.ResponseBytes != 8 {
			t.Fatalf("unexpected locally capped stream diagnostics: %+v", d)
		}
		service.mu.Lock()
		gotRecv := service.received
		service.mu.Unlock()
		if gotRecv != 1 {
			t.Fatalf("endpoint received %d requests, want 1", gotRecv)
		}
	})

	// 2. Partial sends (server early abort on client stream + malformed later message on bidi stream).
	t.Run("partial_sends", func(t *testing.T) {
		client, service := groundTruthClient(t, 0, false)
		service.mu.Lock()
		service.abortAfterInput = 1
		service.mu.Unlock()

		resAbort, err := client.Execute(context.Background(), &protocol.Action{
			Type:   "grpc_client_stream",
			Target: "grpc.testing.TestService/StreamingInputCall",
			Params: map[string]any{"messages": []string{groundTruthBody, groundTruthBody, groundTruthBody}},
		})
		if err != nil || resAbort == nil || resAbort.Success || resAbort.ServerTelemetry == nil || resAbort.ServerTelemetry.GRPC == nil {
			t.Fatalf("expected partial client-stream failure, got %+v err=%v", resAbort, err)
		}
		dAbort := resAbort.ServerTelemetry.GRPC
		if !dAbort.Valid() || dAbort.StatusCode == nil || *dAbort.StatusCode != int32(codes.ResourceExhausted) || dAbort.MessagesReceived != 0 || dAbort.FirstMessageMs != nil || dAbort.MessagesSent < 1 || dAbort.RequestBytes != dAbort.MessagesSent*8 {
			t.Fatalf("unexpected partial client-stream diagnostics: %+v", dAbort)
		}
		service.mu.Lock()
		if service.received != 1 || service.sent != 0 || service.wrongPayload {
			t.Fatalf("endpoint partial-send counts mismatch: received=%d sent=%d wrong=%v", service.received, service.sent, service.wrongPayload)
		}
		service.abortAfterInput = 0
		service.received, service.sent = 0, 0
		service.mu.Unlock()

		resMalformed, err := client.Execute(context.Background(), &protocol.Action{
			Type:   "grpc_bidi_stream",
			Target: "grpc.testing.TestService/FullDuplexCall",
			Params: map[string]any{"messages": []string{groundTruthBody, "{malformed_json"}, "max_messages": 3},
		})
		if err != nil || resMalformed == nil || resMalformed.Success || resMalformed.ServerTelemetry == nil || resMalformed.ServerTelemetry.GRPC == nil {
			t.Fatalf("expected malformed bidi partial send failure, got %+v err=%v", resMalformed, err)
		}
		dMal := resMalformed.ServerTelemetry.GRPC
		if !dMal.Valid() || dMal.MessagesSent != 1 || dMal.RequestBytes != 8 {
			t.Fatalf("expected exactly 1 sent message (8 bytes) before local JSON error, got %+v", dMal)
		}
	})

	// 3. Context cancellation and deadline exceeded against controlled endpoint.
	t.Run("cancellation_and_deadline_exceeded", func(t *testing.T) {
		client, service := groundTruthClient(t, 0, false)
		if _, err := client.resolveMethod(context.Background(), "grpc.testing.TestService/UnaryCall"); err != nil {
			t.Fatal(err)
		}
		service.mu.Lock()
		service.blockUntilDone = true
		service.mu.Unlock()

		for _, cancelEarly := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			var timer *time.Timer
			if cancelEarly {
				timer = time.AfterFunc(30*time.Millisecond, cancel)
			}
			res, err := client.Execute(ctx, &protocol.Action{
				Type:   "grpc_unary",
				Target: "grpc.testing.TestService/UnaryCall",
				Body:   []byte(groundTruthBody),
				Params: map[string]any{"timeout_ms": 120},
			})
			if timer != nil {
				timer.Stop()
			}
			cancel()
			if err != nil || res == nil || res.Success || res.ServerTelemetry == nil || res.ServerTelemetry.GRPC == nil {
				t.Fatalf("cancelEarly=%v unexpected result: %+v err=%v", cancelEarly, res, err)
			}
			wantCode := codes.DeadlineExceeded
			if cancelEarly {
				wantCode = codes.Canceled
			}
			d := res.ServerTelemetry.GRPC
			if !d.Valid() || d.StatusCode == nil || *d.StatusCode != int32(wantCode) || d.MessagesSent != 1 || d.RequestBytes != 8 || d.MessagesReceived != 0 || d.ResponseBytes != 0 || d.FirstMessageMs != nil {
				t.Fatalf("cancelEarly=%v diagnostics=%+v want code=%v", cancelEarly, d, wantCode)
			}
		}
		service.mu.Lock()
		gotRecv, gotSent := service.received, service.sent
		service.mu.Unlock()
		if gotRecv != 2 || gotSent != 0 {
			t.Fatalf("endpoint received=%d sent=%d, want 2 and 0", gotRecv, gotSent)
		}
	})

	// 4. Bidi streaming ClientAnalysis (grpc_uncompressed_payload_bytes, grpc_client_call_rate) and concurrent codec overlap >100%.
	t.Run("bidi_client_analysis_and_unclamped_codec_overlap", func(t *testing.T) {
		client, service := groundTruthClient(t, 0, false)
		res, err := client.Execute(context.Background(), &protocol.Action{
			Type:   "grpc_bidi_stream",
			Target: "grpc.testing.TestService/FullDuplexCall",
			Params: map[string]any{"messages": []string{groundTruthBody, groundTruthBody}, "max_messages": 3},
		})
		if err != nil || res == nil || !res.Success {
			t.Fatalf("bidi execute failed: %+v err=%v", res, err)
		}
		assertGroundTruth(t, res, service, 2, 2, 16, 16, codes.OK)

		// Verify concurrent sender/receiver codec timing overlap (>100% of CallMs) is preserved unclamped through GRPCDiagnostics and GRPCSummary.ClientAnalysis().
		d := res.ServerTelemetry.GRPC.Clone()
		d.CallMs = max(d.CallMs, 10.0)
		encOverlap := d.CallMs * 0.70
		decOverlap := d.CallMs * 0.65
		d.EncodeMs = &encOverlap
		d.DecodeMs = &decOverlap
		if !d.Valid() {
			t.Fatalf("expected GRPCDiagnostics with overlapping streaming codec work (%.2f + %.2f > %.2f) to be valid", encOverlap, decOverlap, d.CallMs)
		}
		var summary *protocol.GRPCSummary
		protocol.MergeGRPCDiagnostic(&summary, d)
		ca := summary.ClientAnalysis()
		if ca.UncompressedRequestBytes == nil || *ca.UncompressedRequestBytes != 16 || ca.UncompressedResponseBytes == nil || *ca.UncompressedResponseBytes != 16 || ca.UncompressedPayloadBytes == nil || *ca.UncompressedPayloadBytes != 32 {
			t.Fatalf("unexpected grpc_uncompressed_payload_bytes: %+v", ca)
		}
		if ca.ClientCallsPerCallSecond == nil || *ca.ClientCallsPerCallSecond <= 0 {
			t.Fatalf("expected positive grpc_client_call_rate, got %+v", ca.ClientCallsPerCallSecond)
		}
		if ca.CodecOverlapRatio == nil || *ca.CodecOverlapRatio <= 1.0 || ca.CodecWorkToCallWallPct == nil || *ca.CodecWorkToCallWallPct <= 100.0 {
			t.Fatalf("expected unclamped grpc_codec_overlap_ratio > 1.0 (>100%%), got ratio=%v pct=%v", ca.CodecOverlapRatio, ca.CodecWorkToCallWallPct)
		}
		if ca.HTTP2TransportPhase != "unavailable" || ca.ServerProcessingPhase != "unavailable" {
			t.Fatalf("expected unavailable transport/server phases, got %+v", ca)
		}
	})
}
