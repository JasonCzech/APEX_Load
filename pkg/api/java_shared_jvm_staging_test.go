package api

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type sharedJVMStagingPeer struct {
	pb.UnimplementedGeneratorControlServer
	outcome    *pb.JavaVMOutcome
	deadlineOK atomic.Bool
}

func (p *sharedJVMStagingPeer) AssignWork(ctx context.Context, _ *pb.WorkAssignment) (*pb.AssignmentAck, error) {
	// Shared groups start their JVMs inside AssignWork, so the controller
	// must allow the generator's whole preparation budget.
	deadline, ok := ctx.Deadline()
	p.deadlineOK.Store(ok && time.Until(deadline) > engine.JavaSharedPrepareTimeout)
	return &pb.AssignmentAck{Accepted: true, JavaVm: p.outcome}, nil
}

func stageSharedJVMShard(t *testing.T, outcome *pb.JavaVMOutcome, script []byte) (generatorrpc.AssignmentResult, bool) {
	t.Helper()
	peer := &sharedJVMStagingPeer{outcome: outcome}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(ctx, "gen-a", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	b := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil)
	plans := []distributedTargetAssignmentPlan{{target: "gen-a", client: client, shards: []GeneratorShardSummary{{Group: "java", Target: "gen-a", VUsers: 500, ShardCount: 1}}}}
	stageCtx, stageCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer stageCancel()
	out := b.assignDistributedGroupTargets(stageCtx, plans, generatorrpc.Script{Content: script}, generatorrpc.Assignment{TestID: "run", GroupName: "java", ScriptID: "java", TargetDuration: time.Minute}, "java", nil, nil)
	if len(out) != 1 || out[0].err != nil {
		t.Fatalf("staging %#v", out)
	}
	return out[0].results[0], peer.deadlineOK.Load()
}

func TestSharedJVMStagingCarriesOutcomeAndLongDeadline(t *testing.T) {
	shared := sharedJVMTestGroup(500, dsl.JavaVuserJVMSharingShared)
	script, err := json.Marshal(shared.script)
	if err != nil {
		t.Fatal(err)
	}
	result, longDeadline := stageSharedJVMShard(t, &pb.JavaVMOutcome{Mode: "shared", Jvms: 2, VusersPerJvm: 250, Warnings: []string{"shared_jvm_script_heap_ignored"}}, script)
	if !longDeadline {
		t.Fatal("shared staging used the ordinary RPC deadline")
	}
	if result.JavaVM == nil || result.JavaVM.GetMode() != "shared" || result.JavaVM.GetJvms() != 2 {
		t.Fatalf("outcome %+v", result.JavaVM)
	}
	// Free text in a reason is dropped rather than shown: codes only.
	if result, _ := stageSharedJVMShard(t, &pb.JavaVMOutcome{Mode: "dedicated", Reason: "secret value leaked"}, script); result.JavaVM != nil {
		t.Fatalf("free-text outcome accepted %+v", result.JavaVM)
	}
	dedicated, err := json.Marshal(sharedJVMTestGroup(500, "").script)
	if err != nil {
		t.Fatal(err)
	}
	if _, longDeadline := stageSharedJVMShard(t, nil, dedicated); longDeadline {
		t.Fatal("dedicated staging was given the shared-JVM deadline")
	}
}

func TestGeneratorShardSummaryCloneOwnsJavaVM(t *testing.T) {
	in := []GeneratorShardSummary{{Group: "java", Target: "gen-a", JavaVM: &JavaVMShardOutcome{Mode: "dedicated", Reason: "shared_jvm_unavailable", Warnings: []string{"w"}}}}
	out := cloneGeneratorShardSummaries(in)
	out[0].JavaVM.Mode = "shared"
	out[0].JavaVM.Warnings[0] = "changed"
	if in[0].JavaVM.Mode != "dedicated" || in[0].JavaVM.Warnings[0] != "w" {
		t.Fatal("clone aliased the shard outcome")
	}
}

func TestRescaledShardsKeepTheirJavaVMOutcome(t *testing.T) {
	previous := []GeneratorShardSummary{{Group: "java", Target: "gen-a", VUsers: 500, ShardOrdinal: 1, ShardCount: 1, JavaVM: &JavaVMShardOutcome{Mode: "shared", JVMs: 2, VUsersPerJVM: 250}}}
	next, err := preserveShardCoordinatesByTarget([]GeneratorShardSummary{{Group: "java", Target: "gen-a", VUsers: 400}}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if next[0].JavaVM == nil || next[0].JavaVM.Mode != "shared" || next[0].JavaVM == previous[0].JavaVM {
		t.Fatalf("rescaled shard outcome %+v", next[0].JavaVM)
	}
}
