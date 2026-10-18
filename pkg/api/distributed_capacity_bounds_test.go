package api

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"strings"
	"sync/atomic"

	"context"
	"fmt"
	"testing"
	"time"
)

func TestLegacyPlacementEnforcesGeneratorCapacity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capacities []int32
		demand     int
		explicit   bool
		want       bool
	}{
		{"auto fits", []int32{100, 100}, 100, false, true},
		{"auto does not implicitly shard", []int32{100, 100, 100, 100}, 320, false, false},
		{"explicit shards fit", []int32{100, 100, 100, 100}, 320, true, true},
		{"explicit single exhausted", []int32{100}, 101, true, false},
		{"explicit aggregate exhausted", []int32{100, 100}, 201, true, false},
		{"zero capacity excluded from rounding", []int32{0, 100}, 100, true, true},
		{"small capacity not overfilled by seed", []int32{1, 99}, 100, true, true},
		{"negative capacity", []int32{-1, 100}, 100, true, true},
		{"all exhausted", []int32{0, 0}, 1, true, false},
		{"large demand cannot wrap int32", []int32{100}, 1 << 32, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loads := map[string]generatorLoadSnapshot{}
			targets := []string{}
			for i, c := range tc.capacities {
				target := fmt.Sprintf("gen-%d", i)
				targets = append(targets, target)
				loads[target] = generatorLoadSnapshot{AvailableCapacity: c, SupportedProtocols: []string{"http"}}
			}
			group := TestGroup{Name: "load", Protocol: "http", VUsers: tc.demand}
			if tc.explicit {
				group.Assignment = &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: targets}
			}
			shards := planDistributedGroupShards([]TestGroup{group}, loads, targets, nil, nil)[group.Name]
			if (len(shards) > 0) != tc.want {
				t.Fatalf("shards=%+v, want placement=%v", shards, tc.want)
			}
			total := 0
			for _, s := range shards {
				if s.VUsers <= 0 || s.VUsers > int(loads[s.Target].AvailableCapacity) {
					t.Fatalf("overcommitted shard %+v", s)
				}
				total += s.VUsers
			}
			if tc.want && total != tc.demand {
				t.Fatalf("assigned %d, want %d", total, tc.demand)
			}
		})
	}
}

func TestLegacyPlacementProjectsCapacityAcrossGroups(t *testing.T) {
	loads := map[string]generatorLoadSnapshot{"gen": {AvailableCapacity: 100, SupportedProtocols: []string{"http"}}}
	for _, explicit := range []bool{false, true} {
		groups := []TestGroup{{Name: "first", Protocol: "http", VUsers: 80}, {Name: "second", Protocol: "http", VUsers: 21}}
		if explicit {
			for i := range groups {
				groups[i].Assignment = &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{"gen"}}
			}
		}
		shards := planDistributedGroupShards(groups, loads, []string{"gen"}, nil, nil)
		if len(shards["first"]) != 1 || len(shards["second"]) != 0 {
			t.Fatalf("explicit=%v: %+v", explicit, shards)
		}
		if loads["gen"].AvailableCapacity != 100 {
			t.Fatal("planner modified input snapshot")
		}
	}
}

func TestCapacityWeightedZeroStartPreservesTargetsWithoutOvercommit(t *testing.T) {
	loads := map[string]generatorLoadSnapshot{"empty": {AvailableCapacity: 0}, "full": {AvailableCapacity: 10}}
	group := TestGroup{Name: "staged", Protocol: "http"}
	for _, n := range []int{0, 10, 11} {
		group.VUsers = n
		shards := capacityWeightedShardsForGroupIncludingZero(group, []string{"empty", "full"}, loads, "")
		if n == 11 {
			if len(shards) != 0 {
				t.Fatalf("overcommitted %+v", shards)
			}
			continue
		}
		if len(shards) != 2 || shards[0].VUsers != 0 || shards[1].VUsers != n {
			t.Fatalf("n=%d shards=%+v", n, shards)
		}
	}
}

func TestFailedGeneratorProbeDoesNotAdvertiseCapacity(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"unreachable"}, time.Second, nil)
	snapshot := backend.probeGeneratorLoad(context.Background(), "unreachable", &startupAssignmentFakeClient{})
	if snapshot.HeartbeatOK || snapshot.AvailableCapacity != 0 || snapshot.MaxVUsers != 0 {
		t.Fatalf("failed probe advertised capacity: %+v", snapshot)
	}
}

type capacityAdmissionService struct {
	*generatorrpc.Service
	assignments atomic.Int32
}

func (s *capacityAdmissionService) AssignWork(ctx context.Context, request *pb.WorkAssignment) (*pb.AssignmentAck, error) {
	s.assignments.Add(1)
	return s.Service.AssignWork(ctx, request)
}

func TestColdDiscoveryRejectsEntirePlacementBeforeAnyAssignment(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	service := &capacityAdmissionService{Service: generatorrpc.NewService("small", 10)}
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	runner := NewTestRunner(1, NewHub(), nil)
	runner.setExecutionBackend(newDistributedExecutionBackend([]string{"small"}, time.Second, func(ctx context.Context, target string) (generatorClient, error) {
		return generatorrpc.Dial(ctx, target, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	}))
	storePreflightScript(runner, "capacity-probe", "")
	run, err := runner.CreateTest(CreateTestRequest{Name: "Cold placement", Duration: "1m", Groups: []CreateGroupEntry{{Name: "first", ScriptID: "capacity-probe", VUsers: 6}, {Name: "second", ScriptID: "capacity-probe", VUsers: 6}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.StartTest(run.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := runner.GetTest(run.ID)
		if state.Status == "FAILED" {
			if !strings.Contains(state.Error, "complete placement") {
				t.Fatalf("wrong failure: %s", state.Error)
			}
			if service.assignments.Load() != 0 {
				t.Fatalf("staged %d assignments before rejecting placement", service.assignments.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cold placement did not fail within deadline")
}
