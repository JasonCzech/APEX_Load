package generatorrpc

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	gproto "google.golang.org/protobuf/proto"
)

type pauseAckTestConn struct {
	ack     *pb.PauseAck
	err     error
	request *pb.PauseRequest
	calls   int
}

func (c *pauseAckTestConn) Invoke(_ context.Context, method string, request, response any, _ ...grpc.CallOption) error {
	if method != pb.GeneratorControl_SetPaused_FullMethodName {
		return errors.New("unexpected unary method")
	}
	c.calls++
	c.request = gproto.Clone(request.(*pb.PauseRequest)).(*pb.PauseRequest)
	if c.err != nil {
		return c.err
	}
	raw, err := gproto.Marshal(c.ack)
	if err != nil {
		return err
	}
	return gproto.Unmarshal(raw, response.(gproto.Message))
}

func (*pauseAckTestConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("unexpected streaming method")
}

func strictPauseControl() PauseControl {
	return PauseControl{TestID: "run", ExecutionGeneration: 4, Pause: true, RequireQuiescence: true, OperationID: "pause-1", OperationSequence: 8, RunSupervisionToken: []byte("fence")}
}

func strictPauseAck() *pb.PauseAck {
	confirmed := true
	return &pb.PauseAck{Accepted: true, CurrentPaused: true, AppliedOperationId: "pause-1", AppliedOperationSequence: 8, QuiescenceConfirmed: &confirmed}
}

func TestStrictPauseClientValidatesOptionalProofAndExactOperation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(*pb.PauseAck)
		wantErr bool
	}{
		{name: "confirmed"},
		{name: "idempotent exact operation"},
		{name: "old peer omits proof", edit: func(a *pb.PauseAck) { a.QuiescenceConfirmed = nil }, wantErr: true},
		{name: "explicit false proof", edit: func(a *pb.PauseAck) { a.QuiescenceConfirmed = gproto.Bool(false) }, wantErr: true},
		{name: "rejected despite proof", edit: func(a *pb.PauseAck) { a.Accepted = false }, wantErr: true},
		{name: "running despite proof", edit: func(a *pb.PauseAck) { a.CurrentPaused = false }, wantErr: true},
		{name: "different operation", edit: func(a *pb.PauseAck) { a.AppliedOperationId = "pause-older" }, wantErr: true},
		{name: "different sequence", edit: func(a *pb.PauseAck) { a.AppliedOperationSequence-- }, wantErr: true},
		{name: "error despite proof", edit: func(a *pb.PauseAck) { a.ErrorMessage = "pause failed" }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ack := strictPauseAck()
			if tc.edit != nil {
				tc.edit(ack)
			}
			conn := &pauseAckTestConn{ack: ack}
			client := &Client{rpc: pb.NewGeneratorControlClient(conn)}
			result, err := client.SetPaused(context.Background(), strictPauseControl())
			if (tc.wantErr && !errors.Is(err, ErrPauseQuiescenceUnconfirmed)) || (!tc.wantErr && err != nil) {
				t.Fatalf("result=%+v error=%v, want proof error %t", result, err, tc.wantErr)
			}
			if result.AppliedOperationID != ack.GetAppliedOperationId() || result.AppliedOperationSequence != ack.GetAppliedOperationSequence() || result.Accepted != ack.GetAccepted() {
				t.Fatal("proof failure lost the applied acknowledgement needed for compensation")
			}
			if !conn.request.GetRequireQuiescence() || conn.request.GetExecutionGeneration() != 4 || string(conn.request.GetRunSupervisionToken()) != "fence" {
				t.Fatal("strict request or execution fence lost on wire")
			}
			if (result.QuiescenceConfirmed == nil) != (ack.QuiescenceConfirmed == nil) {
				t.Fatal("optional proof presence changed")
			}
		})
	}
}

func TestOrdinaryPauseClientPreservesOldAndNewPeerCompatibility(t *testing.T) {
	for _, paused := range []bool{false, true} {
		for _, proof := range []*bool{nil, gproto.Bool(false), gproto.Bool(true)} {
			control := strictPauseControl()
			control.Pause, control.RequireQuiescence = paused, false
			ack := strictPauseAck()
			ack.CurrentPaused, ack.QuiescenceConfirmed = paused, proof
			conn := &pauseAckTestConn{ack: ack}
			result, err := (&Client{rpc: pb.NewGeneratorControlClient(conn)}).SetPaused(context.Background(), control)
			if err != nil || !result.Accepted || result.CurrentPaused != paused || conn.request.GetRequireQuiescence() {
				t.Fatalf("ordinary control changed compatibility: %+v %v", result, err)
			}
			if (result.QuiescenceConfirmed == nil) != (proof == nil) {
				t.Fatal("ordinary decode lost optional proof presence")
			}
		}
	}
}

func TestStrictPauseClientRejectsInvalidRequestBeforeMutation(t *testing.T) {
	for _, edit := range []func(*PauseControl){
		func(c *PauseControl) { c.Pause = false },
		func(c *PauseControl) { c.OperationID = "" },
		func(c *PauseControl) { c.OperationID = " pause-1 " },
		func(c *PauseControl) { c.OperationSequence = 0 },
	} {
		control := strictPauseControl()
		edit(&control)
		conn := &pauseAckTestConn{ack: strictPauseAck()}
		if _, err := (&Client{rpc: pb.NewGeneratorControlClient(conn)}).SetPaused(context.Background(), control); err == nil || conn.calls != 0 {
			t.Fatalf("invalid strict request reached RPC: calls=%d error=%v", conn.calls, err)
		}
	}
	// A metrics stream has no acknowledgement and cannot satisfy strict proof.
	if err := (*MetricsStream)(nil).SendPause(strictPauseControl()); err == nil {
		t.Fatal("strict pause used the unacknowledged stream")
	}
}

func TestPauseProofDecodeClonesPresenceAndPreservesRPCError(t *testing.T) {
	ack := strictPauseAck()
	result := pauseResultFromProto(ack)
	*result.QuiescenceConfirmed = false
	if !ack.GetQuiescenceConfirmed() {
		t.Fatal("decoded optional proof aliases the protobuf source")
	}
	transportErr := errors.New("transport failed")
	conn := &pauseAckTestConn{err: transportErr}
	result, err := (&Client{rpc: pb.NewGeneratorControlClient(conn)}).SetPaused(context.Background(), strictPauseControl())
	if !errors.Is(err, transportErr) || result.QuiescenceConfirmed != nil || result.Accepted {
		t.Fatalf("RPC error became a proof result: %+v %v", result, err)
	}
	if nilResult := pauseResultFromProto(nil); nilResult.QuiescenceConfirmed != nil || nilResult.Accepted {
		t.Fatal("nil ack manufactured proof")
	}
}

type pauseAdvertisementProtocol struct {
	name                               string
	available, supported, panicSupport bool
}

func (p *pauseAdvertisementProtocol) Name() string                             { return p.name }
func (*pauseAdvertisementProtocol) Init(context.Context, map[string]any) error { return nil }
func (*pauseAdvertisementProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*pauseAdvertisementProtocol) Teardown(context.Context) error    { return nil }
func (*pauseAdvertisementProtocol) HealthCheck(context.Context) error { return nil }
func (p *pauseAdvertisementProtocol) RuntimeCapabilities() protocol.RuntimeCapabilities {
	return protocol.RuntimeCapabilities{Available: p.available}
}
func (p *pauseAdvertisementProtocol) RuntimePauseSupported() bool {
	if p.panicSupport {
		panic("private support detail")
	}
	return p.supported
}

func TestMessagingQuiescenceAdvertisementRequiresAvailableSupportedRuntime(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		available, supported, panicSupport, want bool
	}{
		{"jms_quiescence_supported_test", true, true, false, true},
		{"jms_quiescence_unavailable_test", false, true, false, false},
		{"jms_quiescence_unsupported_test", true, false, false, false},
		{"jms_quiescence_panicking_test", true, true, true, false},
	} {
		// Test this individual runtime without assuming other bundled runtimes
		// do not legitimately provide the same global wire capability.
		instance := &pauseAdvertisementProtocol{name: tc.name, available: tc.available, supported: tc.supported, panicSupport: tc.panicSupport}
		if got := messagingRuntimeQuiescenceAvailable(instance, tc.available); got != tc.want {
			t.Errorf("%s supported=%t want%t", tc.name, got, tc.want)
		}
	}
	protocol.Register("jms_quiescence_supported_test", func() protocol.Protocol {
		return &pauseAdvertisementProtocol{name: "jms_quiescence_supported_test", available: true, supported: true}
	})
	protocols, features, _ := runtimeAdvertisementWithCapacities(context.Background(), false, nil, nil)
	if !containsProtocolName(protocols, "jms_quiescence_supported_test") || !containsProtocolName(features, FeatureMessagingRuntimeQuiescenceV1) {
		t.Fatal("supported available runtime did not advertise the implemented strict pause contract")
	}
}

type strictPauseServerStream struct {
	grpc.ServerStream
	ctx      context.Context
	signal   *pb.ControlSignal
	received bool
}

func (s *strictPauseServerStream) Context() context.Context  { return s.ctx }
func (*strictPauseServerStream) Send(*pb.MetricsBatch) error { return nil }
func (s *strictPauseServerStream) Recv() (*pb.ControlSignal, error) {
	if s.received {
		return nil, io.EOF
	}
	s.received = true
	return s.signal, nil
}

func TestStrictPauseServerRejectsUnacknowledgedStreamBeforeMutation(t *testing.T) {
	service := newIsolatedService(t, 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1}, vuserCount: 1}
	service.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream := &strictPauseServerStream{ctx: ctx, signal: &pb.ControlSignal{Signal: &pb.ControlSignal_Pause{Pause: &pb.PauseRequest{TestId: "run", Pause: true, RequireQuiescence: true}}}}
	if err := service.StreamMetrics(stream); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("strict stream pause error=%v, want InvalidArgument", err)
	}
	service.mu.Lock()
	assignment := service.assignments["run"]
	service.mu.Unlock()
	if assignment.paused || assignment.pauseOperationID != "" || assignment.pauseOperationSequence != 0 {
		t.Fatal("unacknowledged strict stream request mutated the assignment")
	}
}
