package generatorrpc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestPreparationReadinessDoesNotContactTargets(t *testing.T) {
	clearTransportEnv(t)
	var requests, connections atomic.Int64
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	target.Start()
	defer target.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			_ = conn.Close()
		}
	}()
	defer func() { _ = listener.Close(); <-done }()
	address := listener.Addr().String()
	fixtures := []struct{ name, script string }{
		{"http init", fmt.Sprintf(`{"name":"prepare","protocol":"http","config":{"base_url":%q},"init":[{"name":"initialize","type":"GET","target":"/init"}],"actions":[{"name":"load","type":"GET","target":"/load"}]}`, target.URL)},
		{"grpc reflection", fmt.Sprintf(`{"name":"prepare","protocol":"grpc","config":{"base_url":%q,"grpc":{"use_reflection":true,"plain_text":true}},"actions":[{"name":"call","type":"grpc_unary","target":"grpc.health.v1.Health/Check","body":"{}"}]}`, address)},
		{"websocket", fmt.Sprintf(`{"name":"prepare","protocol":"websocket","config":{"base_url":%q},"actions":[{"name":"connect","type":"ws_connect","target":%q}]}`, "ws://"+target.Listener.Addr().String(), "ws://"+target.Listener.Addr().String()+"/socket")},
		{"database", fmt.Sprintf(`{"name":"prepare","protocol":"database","config":{"database":{"driver":"postgres","dsn":%q}},"actions":[{"name":"query","type":"db_query","target":"SELECT 1"}]}`, "postgres://user:password@"+address+"/prepare?sslmode=disable")},
		{"network", fmt.Sprintf(`{"name":"prepare","protocol":"network","config":{"network":{"address":%q}},"actions":[{"name":"send","type":"tcp_request","target":%q,"body":"hello"}]}`, address, address)},
		{"browser navigation", fmt.Sprintf(`{"name":"prepare","protocol":"browser","actions":[{"name":"navigate","type":"browser_navigate","target":%q}]}`, target.URL)},
		{"kafka broker", fmt.Sprintf(`{"name":"prepare","protocol":"kafka","config":{"kafka":{"brokers":[%q],"mock":%t}},"actions":[{"name":"publish","type":"kafka_produce","target":"prepare","body":"hello"}]}`, address, !dsl.KafkaRealAdapterAvailable())},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			s := newIsolatedService(t, 10, WithExecution())
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := s.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			}()
			ack, err := s.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "prepared", GroupName: "load", ScriptId: "script", ScriptContent: []byte(fixture.script), VuserCount: 1, PrepareOnly: true})
			if err != nil || !ack.GetAccepted() {
				t.Fatalf("stage: %v %v", ack, err)
			}
			transport := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			pb.RegisterGeneratorControlServer(server, s)
			go func() { _ = server.Serve(transport) }()
			defer server.Stop()
			defer transport.Close()
			conn, err := grpc.NewClient("passthrough:///prepared", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return transport.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			client := &Client{rpc: pb.NewGeneratorControlClient(conn)}
			if err := client.CheckPreparation(context.Background(), "prepared", 0, map[string]int32{"load": 1}, nil); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 0 || connections.Load() != 0 {
				t.Fatalf("preparation contacted a target: HTTP=%d TCP=%d", requests.Load(), connections.Load())
			}
		})
	}
	if requests.Load() != 0 || connections.Load() != 0 {
		t.Fatal("preparation teardown contacted a target")
	}
}

func preparedReadinessFixture(t *testing.T, opts ...ServiceOption) (*Service, *pb.PreparationCheckRequest) {
	t.Helper()
	clearTransportEnv(t)
	s := newIsolatedService(t, 10, append([]ServiceOption{WithExecution()}, opts...)...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	ack, err := s.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "prepared", GroupName: "load", ScriptId: "script", ScriptContent: []byte(`{"name":"prepare","protocol":"http","config":{"base_url":"http://127.0.0.1:1"},"actions":[{"name":"load","type":"GET","target":"/"}]}`), VuserCount: 1, PrepareOnly: true})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage: %v %v", ack, err)
	}
	return s, &pb.PreparationCheckRequest{TestId: "prepared", ExpectedGroups: map[string]int32{"load": 1}, RenewStaging: true}
}

func TestPreparationReadinessRejectsLostReservations(t *testing.T) {
	for _, caseName := range []string{"missing", "wrong generation", "wrong count", "wrong group", "draining", "committed", "not prepare", "stopped group", "changed capacity", "token", "stage deadline", "empty groups", "too many groups", "cancelled context"} {
		t.Run(caseName, func(t *testing.T) {
			s, req := preparedReadinessFixture(t)
			ctx := context.Background()
			s.mu.Lock()
			assignment := s.assignments[req.TestId]
			switch caseName {
			case "missing":
				delete(s.assignments, req.TestId)
			case "wrong generation":
				req.ExecutionGeneration = 1
			case "wrong count":
				req.ExpectedGroups["load"] = 2
			case "wrong group":
				req.ExpectedGroups = map[string]int32{"other": 1}
			case "draining":
				s.draining = true
			case "committed":
				assignment.committed = true
				s.assignments[req.TestId] = assignment
			case "not prepare":
				assignment.prepareOnly = false
				s.assignments[req.TestId] = assignment
			case "stopped group":
				s.executions[req.TestId]["load"].stagedDoneOnce.Do(func() { close(s.executions[req.TestId]["load"].done) })
			case "changed capacity":
				assignment.preparationCapacityClaim = &pb.PodCapacityClaim{TestId: req.TestId, ReservationId: "lost"}
				s.assignments[req.TestId] = assignment
			case "token":
				assignment.runSupervisionTokenHash = runSupervisionTokenHash(supervisionTestToken(1))
				s.assignments[req.TestId] = assignment
			case "stage deadline":
				s.lifecycleCoordinator.stageExpiresAt[req.TestId] = time.Now().Add(-time.Second)
			case "empty groups":
				req.ExpectedGroups = nil
			case "too many groups":
				for i := 0; i < 1025; i++ {
					req.ExpectedGroups[fmt.Sprint(i)] = 1
				}
			case "cancelled context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			s.mu.Unlock()
			ack, err := s.CheckPreparation(ctx, req)
			if err == nil || ack.GetAccepted() {
				t.Fatalf("lost preparation accepted: %v %v", ack, err)
			}
		})
	}
}

func TestPreparationReadinessRenewsStagingWithoutResurrection(t *testing.T) {
	s, req := preparedReadinessFixture(t, WithAssignmentStageTTL(time.Second))
	s.mu.RLock()
	oldToken := s.lifecycleCoordinator.stageTokens[req.TestId]
	s.mu.RUnlock()
	ack, err := s.CheckPreparation(context.Background(), req)
	if err != nil || !ack.GetAccepted() {
		t.Fatal(err)
	}
	s.expireStagedAssignment(req.TestId, oldToken)
	ack, err = s.CheckPreparation(context.Background(), req)
	if err != nil || !ack.GetAccepted() {
		t.Fatal("stale expiry reclaimed renewed preparation", err)
	}
	s.mu.RLock()
	currentToken := s.lifecycleCoordinator.stageTokens[req.TestId]
	s.mu.RUnlock()
	s.expireStagedAssignment(req.TestId, currentToken)
	ack, err = s.CheckPreparation(context.Background(), req)
	if err == nil || ack.GetAccepted() {
		t.Fatal("expired preparation resurrected")
	}
	s.mu.RLock()
	executions := len(s.executions[req.TestId])
	_, assigned := s.assignments[req.TestId]
	s.mu.RUnlock()
	if executions != 0 || assigned {
		t.Fatal("expired preparation retained reservations")
	}
}

// Production controllers claim STAGED supervision before preparation can become
// ready. Checking that reservation must neither require ACTIVE nor activate it.
func TestPreparationReadinessValidatesStagedRunSupervision(t *testing.T) {
	for _, caseName := range []string{"staged", "unclaimed", "wrong token", "expired", "fenced", "stopping", "wrong generation", "changed claim token", "restart fence"} {
		t.Run(caseName, func(t *testing.T) {
			clearTransportEnv(t)
			s := newIsolatedService(t, 10, WithExecution(), WithRunSupervisionRequired(60*time.Second, 0))
			t.Cleanup(func() { shutdownSupervisionTestService(t, s) })
			token := supervisionTestToken(23)
			assignment := supervisedStageRequest("prepared", "script", 0, token)
			assignment.GroupName = "load"
			assignment.PrepareOnly = true
			if ack, err := s.AssignWork(context.Background(), assignment); err != nil || !ack.GetAccepted() {
				t.Fatalf("stage: %v %v", ack, err)
			}
			if caseName != "unclaimed" {
				ack, err := s.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{TestId: "prepared", RunSupervisionToken: token, RequestedTtlMilliseconds: 60000})
				if err != nil || !ack.GetAccepted() || ack.GetState() != pb.RunSupervisionState_RUN_SUPERVISION_STATE_STAGED {
					t.Fatalf("staged claim: %v %v", ack, err)
				}
			}
			req := &pb.PreparationCheckRequest{TestId: "prepared", ExpectedGroups: map[string]int32{"load": 1}, RunSupervisionToken: token, RenewStaging: true}
			s.mu.Lock()
			lease := s.runSupervisions[req.TestId]
			switch caseName {
			case "wrong token":
				req.RunSupervisionToken = supervisionTestToken(24)
			case "expired":
				lease.deadline = time.Now().Add(-time.Second)
			case "fenced":
				lease.state = pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED
			case "stopping":
				lease.state = pb.RunSupervisionState_RUN_SUPERVISION_STATE_STOPPING
			case "wrong generation":
				lease.generation = 1
			case "changed claim token":
				lease.tokenHash = runSupervisionTokenHash(supervisionTestToken(24))
			case "restart fence":
				s.executionEpochManager.runSupervisionRestartFenceInherited = true
			}
			stageToken := s.lifecycleCoordinator.stageTokens[req.TestId]
			var deadline time.Time
			var leaseTimerToken uint64
			if lease != nil {
				deadline, leaseTimerToken = lease.deadline, lease.timerToken
			}
			s.mu.Unlock()
			ack, err := s.CheckPreparation(context.Background(), req)
			if caseName == "staged" {
				if err != nil || !ack.GetAccepted() {
					t.Fatalf("valid staged preparation rejected: %v %v", ack, err)
				}
			} else if err == nil || ack.GetAccepted() {
				t.Fatalf("invalid supervised preparation accepted: %v %v", ack, err)
			}
			s.mu.RLock()
			defer s.mu.RUnlock()
			if s.assignments[req.TestId].committed || s.executions[req.TestId]["load"].started.Load() {
				t.Fatal("readiness crossed the execution boundary")
			}
			if lease != nil && (lease.timerToken != leaseTimerToken || !lease.deadline.Equal(deadline)) {
				t.Fatal("readiness renewed or armed run supervision")
			}
			if caseName == "staged" {
				if lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_STAGED || s.lifecycleCoordinator.stageTokens[req.TestId] == stageToken {
					t.Fatal("readiness failed to retain staged supervision and renew staging")
				}
			} else if s.lifecycleCoordinator.stageTokens[req.TestId] != stageToken {
				t.Fatal("invalid readiness renewed staging")
			}
		})
	}
}

// A staged group must not expire while another group of the same test is still
// preparing inside its AssignWork: shared JVMs can take minutes to start.
func TestStageExpiryWaitsForAnInFlightGroup(t *testing.T) {
	s, req := preparedReadinessFixture(t, WithAssignmentStageTTL(time.Hour))
	s.mu.Lock()
	token := s.lifecycleCoordinator.stageTokens[req.TestId]
	s.lifecycleCoordinator.pendingStages[req.TestId] = &pendingAssignmentStage{token: 99, done: make(chan struct{})}
	s.mu.Unlock()
	s.expireStagedAssignment(req.TestId, token)
	s.mu.Lock()
	_, assigned := s.assignments[req.TestId]
	rearmed := s.lifecycleCoordinator.stageTokens[req.TestId]
	s.lifecycleCoordinator.finishPendingStageLocked(req.TestId, 99)
	s.mu.Unlock()
	if !assigned || rearmed == 0 || rearmed == token {
		t.Fatalf("staged test expired or left unarmed while a group was preparing: assigned=%v token=%d->%d", assigned, token, rearmed)
	}
	s.expireStagedAssignment(req.TestId, rearmed)
	s.mu.RLock()
	_, assigned = s.assignments[req.TestId]
	s.mu.RUnlock()
	if assigned {
		t.Fatal("re-armed expiry did not fire once the group finished")
	}
}
