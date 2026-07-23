package generatorrpc

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stagingRejectingPreflightProtocol struct {
	preflightCalls *atomic.Int32
	initCalls      *atomic.Int32
	expectedConfig string
}

func (*stagingRejectingPreflightProtocol) Name() string { return "staging_preflight_rejection_test" }
func (p *stagingRejectingPreflightProtocol) Init(context.Context, map[string]any) error {
	p.initCalls.Add(1)
	return nil
}
func (*stagingRejectingPreflightProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*stagingRejectingPreflightProtocol) Teardown(context.Context) error    { return nil }
func (*stagingRejectingPreflightProtocol) HealthCheck(context.Context) error { return nil }
func (p *stagingRejectingPreflightProtocol) PreflightRuntime(_ context.Context, request protocol.RuntimePreflightRequest) (protocol.RuntimePreflightResult, error) {
	p.preflightCalls.Add(1)
	resolved := fmt.Sprint(request.Config["base_url"])
	if resolved != p.expectedConfig {
		return protocol.RuntimePreflightResult{}, fmt.Errorf("fixture received unresolved runtime configuration")
	}
	return protocol.RuntimePreflightResult{}, fmt.Errorf("fixture runtime preflight rejected %s", resolved)
}

func TestAssignmentRuntimePreflightRejectsBeforeAtomicCommitOrVUserInit(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcTLSCAEnvVar, "configured-ca")
	t.Setenv(grpcAuthTokenEnvVar, strings.Repeat("t", minProductionAuthTokenBytes))
	var preflightCalls atomic.Int32
	var initCalls atomic.Int32
	const resolvedSecret = "https://runtime-secret.example.test/sensitive"
	protocol.Register("staging_preflight_rejection_test", func() protocol.Protocol {
		return &stagingRejectingPreflightProtocol{preflightCalls: &preflightCalls, initCalls: &initCalls, expectedConfig: resolvedSecret}
	})
	service := newIsolatedService(t, 10, WithExecution())
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:         "runtime-preflight-reject",
		GroupName:      "default",
		ScriptId:       "runtime-preflight-script",
		ScriptContent:  []byte(`{"name":"preflight","protocol":"staging_preflight_rejection_test","config":{"base_url":"{{secret.RUNTIME_URL}}"},"actions":[{"name":"request","type":"request","method":"GET","target":"/"}]}`),
		VuserCount:     2,
		SecretBundle:   map[string]string{"RUNTIME_URL": resolvedSecret},
		SecretBundleId: "runtime-preflight-secrets",
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "runtime preflight failed") {
		t.Fatalf("assignment result = ack %#v, error %v (%s); want FailedPrecondition", ack, err, status.Code(err))
	}
	if ack != nil {
		t.Fatalf("assignment ack = %#v, want nil for a gRPC precondition failure", ack)
	}
	if strings.Contains(err.Error(), resolvedSecret) {
		t.Fatalf("assignment preflight error leaked a resolved secret: %s", err)
	}
	if got := preflightCalls.Load(); got != 1 {
		t.Fatalf("preflight calls = %d, want one", got)
	}
	if got := initCalls.Load(); got != 0 {
		t.Fatalf("protocol Init calls = %d, want zero", got)
	}
	service.mu.RLock()
	_, committed := service.assignments["runtime-preflight-reject"]
	_, pending := service.lifecycleCoordinator.pendingStages["runtime-preflight-reject"]
	service.mu.RUnlock()
	if committed || pending {
		t.Fatalf("rejected assignment committed=%v pending=%v", committed, pending)
	}
}
