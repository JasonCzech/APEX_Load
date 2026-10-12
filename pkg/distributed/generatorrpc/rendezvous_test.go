package generatorrpc

import (
	"context"
	"encoding/json"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"sync"
	"testing"
	"time"
)

func rendezvousChannelFixture(t *testing.T, reverse ...bool) (*Client, *rendezvousProxy, []byte) {
	t.Helper()
	service := newIsolatedService(t, 1024)
	proxy := newRendezvousProxy()
	token := supervisionTestToken(99)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", groups: map[string]int32{"group": 256}, runSupervisionTokenHash: runSupervisionTokenHash(token)}
	service.executions["run"] = map[string]*groupExecution{"group": {groupName: "group", stagedConfig: &engine.MultiGroupConfig{Groups: []engine.GroupConfig{{Name: "group", DistributedRendezvousClient: proxy}}}}}
	service.mu.Unlock()

	if len(reverse) > 0 && reverse[0] {
		for _, key := range []string{grpcTLSCertEnvVar, grpcTLSKeyEnvVar, grpcTLSCAEnvVar, grpcTLSServerNameEnvVar, grpcAuthTokenEnvVar, "APEXLOAD_ENV"} {
			t.Setenv(key, "")
		}
		prior := DefaultReverseRegistry
		DefaultReverseRegistry = NewReverseRegistry()
		t.Cleanup(func() { DefaultReverseRegistry = prior })
		controllerConn, generatorConn := net.Pipe()
		listener := NewConnListener(generatorConn.LocalAddr())
		server := grpc.NewServer()
		pb.RegisterGeneratorControlServer(server, service)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() { server.Stop(); _ = listener.Close(); _ = controllerConn.Close(); _ = generatorConn.Close() })
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if err := listener.Offer(ctx, generatorConn); err != nil {
			t.Fatal(err)
		}
		if _, err := DefaultReverseRegistry.Register("rendezvous-reverse", controllerConn); err != nil {
			t.Fatal(err)
		}
		client, err := Dial(ctx, "tunnel://rendezvous-reverse")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client, proxy, token
	}
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///rendezvous", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{rpc: pb.NewGeneratorControlClient(conn)}, proxy, token
}
func TestRendezvousTwoGeneratorStreamsBeyondLiveTableSlots(t *testing.T) {
	a, pa, token := rendezvousChannelFixture(t)
	b, pbProxy, _ := rendezvousChannelFixture(t)
	c, err := rendezvous.New([]rendezvous.MemberRange{{Count: 512}}, []rendezvous.Contract{{Key: "ready", Target: 512, TimeoutMS: 5000}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	if err = a.OpenRendezvous(ctx, "run", "group", 0, token, c); err != nil {
		t.Fatal(err)
	}
	if err = b.OpenRendezvous(ctx, "run", "group", 0, token, c); err != nil {
		t.Fatal(err)
	}
	aa := rendezvous.NewAggregator(pa, 256)
	ab := rendezvous.NewAggregator(pbProxy, 256)
	var wg sync.WaitGroup
	fail := make(chan error, 512)
	for i := 0; i < 512; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			agg := aa
			if id >= 256 {
				agg = ab
			}
			out, err := agg.Wait(ctx, "ready", 1, id)
			if err != nil {
				fail <- err
			} else if out.State != "released" || out.Arrived != 512 {
				fail <- rendezvous.ErrInvalid
			}
		}(uint64(i))
	}
	wg.Wait()
	close(fail)
	for err := range fail {
		t.Fatal(err)
	}
	bad, err := a.rpc.RendezvousChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = bad.Send(&pb.RendezvousReply{TestId: "run", GroupName: "group", ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil {
		t.Fatal(err)
	}
	if _, err = bad.Recv(); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale generation %v", err)
	}
}
func TestRendezvousChannelLostReplyRestoresSameOutcome(t *testing.T) {
	client, proxy, token := rendezvousChannelFixture(t)
	c, err := rendezvous.New([]rendezvous.MemberRange{{Count: 1}}, []rendezvous.Contract{{Key: "ready", Target: 1, TimeoutMS: 5000}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	committed := make(chan struct{})
	var once sync.Once
	delayed := rendezvous.ClientFunc(func(ctx context.Context, batch rendezvous.Batch) (rendezvous.Outcome, error) {
		out, err := c.Exchange(ctx, batch)
		once.Do(func() { close(committed) })
		<-ctx.Done()
		return out, err
	})
	if err = client.OpenRendezvous(first, "run", "group", 0, token, delayed); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan rendezvousResponse, 1)
	go func() {
		out, err := proxy.Exchange(ctx, rendezvous.Batch{Key: "ready", Round: 1, Arrivals: []uint64{0}})
		done <- rendezvousResponse{outcome: out, err: err}
	}()
	select {
	case <-committed:
	case <-ctx.Done():
		t.Fatal("did not commit")
	}
	cancelFirst()
	restored, err := rendezvous.Restore(c.Snapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.OpenRendezvous(ctx, "run", "group", 0, token, restored); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.outcome.State != "released" || got.outcome.Arrived != 1 {
		t.Fatalf("outcome %+v %v", got.outcome, got.err)
	}
	if restored.Snapshot().Revision != 1 {
		t.Fatal("reconnect executed duplicate arrival")
	}
}
func TestRendezvousProxyOldStreamCannotReply(t *testing.T) {
	p := newRendezvousProxy()
	p.attached = true
	p.epoch = 2
	body, _ := json.Marshal(rendezvous.Outcome{State: "released", Target: 1, Arrived: 1, Deadline: time.Now()})
	if err := p.receive(&pb.RendezvousReply{RequestId: 1, OutcomeJson: body}, 1); err == nil {
		t.Fatal("old stream not fenced")
	}
}

func TestRendezvousReverseTransport(t *testing.T) {
	client, proxy, token := rendezvousChannelFixture(t, true)
	c, err := rendezvous.New([]rendezvous.MemberRange{{Count: 1}}, []rendezvous.Contract{{Key: "ready", Target: 1, TimeoutMS: 1000}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err = client.OpenRendezvous(ctx, "run", "group", 0, token, c); err != nil {
		t.Fatal(err)
	}
	out, err := proxy.Exchange(ctx, rendezvous.Batch{Key: "ready", Round: 1, Arrivals: []uint64{0}})
	if err != nil || out.State != "released" {
		t.Fatalf("reverse barrier %+v %v", out, err)
	}
}
