package api

import (
	"context"
	"encoding/json"
	"errors"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

type preparationFixtureClient struct {
	generatorClient
	check func(context.Context, string, uint64, map[string]int32, []byte) error
}

func (c *preparationFixtureClient) CheckPreparation(ctx context.Context, id string, generation uint64, groups map[string]int32, token []byte) error {
	return c.check(ctx, id, generation, groups, token)
}

func TestPreparationRequiresReadinessCapability(t *testing.T) {
	for _, features := range [][]string{nil, {generatorrpc.FeaturePreparationV1}, {generatorrpc.FeaturePreparationReadinessV1}} {
		if err := preparationCapabilityError(features); err == nil {
			t.Fatalf("accepted unsupported peer: %v", features)
		}
	}
	if err := preparationCapabilityError([]string{generatorrpc.FeaturePreparationV1, generatorrpc.FeaturePreparationReadinessV1}); err != nil {
		t.Fatal(err)
	}
}

func TestDistributedPreparationSnapshotsAndRechecksReservations(t *testing.T) {
	tr := NewTestRunner(1, NewHub(), nil)
	b := &distributedExecutionBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{ID: "prepared", Status: "INITIALIZING", preparationID: "prep", executionCtx: ctx, executionBackend: b}
	tr.tests[test.ID] = test
	calls := 0
	fail := false
	client := &preparationFixtureClient{check: func(ctx context.Context, id string, generation uint64, groups map[string]int32, token []byte) error {
		// A control-plane RPC must not run under the runner state lock.
		tr.mu.Lock()
		tr.mu.Unlock() //nolint:staticcheck // acquiring tr.mu proves the RPC does not run under the runner lock
		calls++
		if id != test.ID || generation != 7 || !reflect.DeepEqual(groups, map[string]int32{"load": 2}) || len(token) != 3 || token[0] != 1 {
			t.Errorf("readiness identity: %s %d %v %v", id, generation, groups, token)
		}
		if fail {
			return errors.New("lost generator")
		}
		return nil
	}}
	shards := map[string][]GeneratorShardSummary{"load": {{Group: "load", Target: "pod-a", VUsers: 2}}}
	token := []byte{1, 2, 3}
	if err := b.installPreparationReadiness(tr, test, shards, map[string]generatorClient{"pod-a": client}, 7, token); err != nil {
		t.Fatal(err)
	}
	shards["load"][0].VUsers = 999
	token[0] = 9
	allocation, _ := json.Marshal(test.GeneratorShards)
	p := &storage.Preparation{ID: "prep", State: "ready", Allocation: allocation}
	if !tr.preparationReservationValid(test.ID, p) || calls != 1 {
		t.Fatal("valid staged reservation unavailable")
	}
	fail = true
	if tr.preparationReservationValid(test.ID, p) {
		t.Fatal("lost remote reservation remained ready")
	}
	fail = false
	tr.mu.Lock()
	test.GeneratorShards[0].Target = "replacement"
	tr.mu.Unlock()
	if tr.preparationReservationValid(test.ID, p) {
		t.Fatal("changed allocation remained ready")
	}
	tr.mu.Lock()
	test.GeneratorShards[0].Target = "pod-a"
	test.preparationPeerCheck = nil
	tr.mu.Unlock()
	if tr.preparationReservationValid(test.ID, p) {
		t.Fatal("distributed preparation without a peer check remained ready")
	}
}

func TestDistributedPreparationRejectsMissingAndDuplicatePlacement(t *testing.T) {
	tr := NewTestRunner(1, NewHub(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &ManagedTest{ID: "prepared", Status: "INITIALIZING", executionCtx: ctx}
	tr.tests[run.ID] = run
	b := &distributedExecutionBackend{}
	shard := GeneratorShardSummary{Group: "load", Target: "pod-a", VUsers: 1}
	for _, shards := range []map[string][]GeneratorShardSummary{nil, {"load": {shard}}, {"load": {shard, shard}}} {
		if err := b.installPreparationReadiness(tr, run, shards, map[string]generatorClient{"pod-a": &preparationFixtureClient{}}, 1, nil); err == nil && len(shards["load"]) != 1 {
			t.Fatalf("invalid placement accepted: %v", shards)
		}
	}
	if err := b.installPreparationReadiness(tr, run, map[string][]GeneratorShardSummary{"load": {shard}}, map[string]generatorClient{"pod-a": nil}, 1, nil); err == nil {
		t.Fatal("unsupported peer accepted")
	}
}

// Exercise the full controller -> real gRPC generator -> durable preparation
// gate with independent HTTP/TCP counters and a deliberately shorter stage TTL.
func TestDistributedPreparationZeroTrafficAndReadinessLoss(t *testing.T) {
	for _, action := range []string{"start", "peer_loss"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
			var requests, connections atomic.Int64
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
			target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			target.Start()
			defer target.Close()
			server, durable := newTimeslotServer(t)
			listener := bufconn.Listen(1 << 20)
			rpcServer := grpc.NewServer()
			service := generatorrpc.NewService("gen-preparation-"+action, 10, generatorrpc.WithExecution(), generatorrpc.WithAssignmentStageTTL(5*time.Second), generatorrpc.WithRunSupervisionRequired(60*time.Second, 0), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
			pb.RegisterGeneratorControlServer(rpcServer, service)
			go func() { _ = rpcServer.Serve(listener) }()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := service.Shutdown(ctx); err != nil {
					t.Error(err)
				}
				rpcServer.Stop()
				_ = listener.Close()
			})
			backend := newDistributedExecutionBackend([]string{"prepared-peer"}, 2*time.Second, func(ctx context.Context, target string) (generatorClient, error) {
				return generatorrpc.Dial(ctx, target, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
			})
			backend.enableRunSupervision(60*time.Second, 10*time.Second)
			WithExecutionBackend(backend)(server.runner)
			script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "distributed-prepare-script", Name: "prepare", Version: "1", Protocol: "http", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: target.URL}, Init: []dsl.Action{{Name: "init", Type: "GET", Target: "/init"}}, Actions: []dsl.Action{{Name: "load", Type: "GET", Target: "/load"}}})
			if err != nil {
				t.Fatal(err)
			}
			run, err := server.runner.CreateTest(CreateTestRequest{Name: "distributed prepare", ProjectID: "proj-a", ScriptID: script.ID, VUsers: 1, Duration: "1s", scriptShakeoutProvided: true, resourceBaselineProvided: true})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/tests/" + run.ID + "/preparation"
			response := timeslotRequest(t, server, "POST", path, nil)
			if response.Code != 202 {
				t.Fatalf("prepare: %d %s", response.Code, response.Body.String())
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			var preparation storage.Preparation
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				current := timeslotRequest(t, server, "GET", path, nil)
				if err := json.Unmarshal(current.Body.Bytes(), &preparation); err != nil {
					t.Fatal(err)
				}
				if preparation.State == "ready" || preparation.State == "failed" {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if preparation.State != "ready" {
				current, _ := server.runner.GetTest(run.ID)
				t.Fatalf("not ready: preparation=%s run=%s error=%s", preparation.State, current.Status, current.Error)
			}
			var allocation []GeneratorShardSummary
			if err := json.Unmarshal(preparation.Allocation, &allocation); err != nil || len(allocation) != 1 || allocation[0].Target != "prepared-peer" || allocation[0].VUsers != 1 {
				t.Fatalf("durable allocation: %s %v", preparation.Allocation, err)
			}
			time.Sleep(6 * time.Second) // Exceed the original unrenewed generator stage TTL.
			current, err := durable.(storage.PreparationStore).GetPreparation(preparation.ID)
			if err != nil || current.State != "ready" {
				t.Fatalf("reservation expired before preparation TTL: %+v %v", current, err)
			}
			if requests.Load() != 0 || connections.Load() != 0 {
				t.Fatal("preparation contacted target")
			}
			opts := StartTestOptions{PreparationID: preparation.ID, ExpectedPreparationRevision: preparation.ExecutionRevision}
			if action == "start" {
				if err := server.runner.StartTestWithOptions(run.ID, opts); err != nil {
					t.Fatal(err)
				}
				deadline = time.Now().Add(8 * time.Second)
				for requests.Load() < 2 && time.Now().Before(deadline) {
					time.Sleep(25 * time.Millisecond)
				}
				if requests.Load() < 2 || connections.Load() < 1 {
					t.Fatal("Start did not execute init and load")
				}
			} else {
				service.SetDraining(true)
				deadline = time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					current, err = durable.(storage.PreparationStore).GetPreparation(preparation.ID)
					if err == nil && current.State == "failed" {
						break
					}
					time.Sleep(25 * time.Millisecond)
				}
				if err != nil || current.State != "failed" {
					t.Fatalf("lost readiness not invalidated: %+v %v", current, err)
				}
				if err := server.runner.StartTestWithOptions(run.ID, opts); err == nil {
					t.Fatal("lost preparation consumed")
				}
				if requests.Load() != 0 || connections.Load() != 0 {
					t.Fatal("lost preparation contacted target")
				}
			}
		})
	}
}
