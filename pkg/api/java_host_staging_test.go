package api

import (
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type javaHostStagingPeer struct {
	pb.UnimplementedGeneratorControlServer
	caps        *pb.JavaHostCapabilities
	assignments atomic.Int32
	heartbeats  atomic.Int32
	badAck      bool
	prepared    bool
}

func (p *javaHostStagingPeer) Heartbeat(s pb.GeneratorControl_HeartbeatServer) error {
	for {
		_, err := s.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p.heartbeats.Add(1)
		features := []string{generatorrpc.FeatureJavaHostBrokerV1}
		if p.prepared {
			features = append(features, generatorrpc.FeatureJavaHostPreparedPopulationV1)
		}
		if err := s.Send(&pb.HeartbeatPong{GeneratorId: "gen-a", AvailableCapacity: 100, CompatibleFeatures: features, JavaHost: p.caps}); err != nil {
			return err
		}
	}
}
func (p *javaHostStagingPeer) AssignWork(ctx context.Context, q *pb.WorkAssignment) (*pb.AssignmentAck, error) {
	p.assignments.Add(1)
	// Assert that host preparation receives its own deadline, not the much
	// shorter ordinary RPC timeout. Do not rely on scheduler-sensitive sleeps.
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 30*time.Second {
		return &pb.AssignmentAck{Accepted: false, ErrorMessage: "wrong preparation deadline"}, nil
	}
	selection := proto.Clone(q.JavaHost).(*pb.JavaHostSelection)
	if p.badAck {
		selection.ProfileId = "drift"
	}
	return &pb.AssignmentAck{Accepted: true, JavaHost: &pb.JavaHostPlacement{Selection: selection, ReservationId: "reserved", Mode: "dedicated", Containment: "execution_pod", MaxHosts: 25, MaxVusers: 25}}, nil
}

func TestJavaHostControllerStagesFrozenSelectionAfterFreshHeartbeat(t *testing.T) {
	for _, name := range []string{"ready", "drift", "bad_ack", "prepared", "old_prepared_peer"} {
		t.Run(name, func(t *testing.T) {
			g, load := javaHostPlacementFixture(t)
			placement, err := javaHostPlacementForGroup(g, load)
			if err != nil {
				t.Fatal(err)
			}
			caps := testJavaHostCaps()
			caps.Profiles[0].ScriptSha256 = placement.Profile.ScriptSHA256
			peer := &javaHostStagingPeer{caps: caps, badAck: name == "bad_ack", prepared: name == "prepared"}
			if name == "prepared" || name == "old_prepared_peer" {
				placement.PreparedVUsers = 2
			}
			if name == "drift" {
				caps.Profiles[0].ProfileId = "changed"
			}
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
			defer client.Close()
			b := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil)
			plans := []distributedTargetAssignmentPlan{{target: "gen-a", client: client, shards: []GeneratorShardSummary{{Group: g.Name, Target: "gen-a", VUsers: 2, ShardCount: 1, JavaHost: placement}}}}
			// The AssignWork context must remain long enough for preparation. The
			// test's own cancellation still bounds the whole operation if it stalls.
			stageCtx, stageCancel := context.WithTimeout(context.Background(), time.Minute)
			defer stageCancel()
			out := b.assignDistributedGroupTargets(stageCtx, plans, generatorrpc.Script{}, generatorrpc.Assignment{TestID: "run", GroupName: g.Name, ScriptID: "approved", TargetDuration: time.Minute}, "approved", nil, nil)
			if len(out) != 1 {
				t.Fatal(out)
			}
			if name == "ready" || name == "prepared" {
				if out[0].err != nil || out[0].results[0].JavaHost.ReservationId != "reserved" {
					t.Fatalf("stage: %#v", out[0])
				}
			} else if out[0].err == nil {
				t.Fatal("drift accepted")
			}
			if peer.heartbeats.Load() != 1 {
				t.Fatal("missing fresh capability negotiation")
			}
			if (name == "drift" || name == "old_prepared_peer") && peer.assignments.Load() != 0 {
				t.Fatal("drifted selection reserved")
			}
			if placement.ReservationID != "" {
				t.Fatal("staging mutated frozen caller placement")
			}
		})
	}
}

func TestJavaHostControllerStagesEachShardWithItsFrozenProfile(t *testing.T) {
	g, load := javaHostPlacementFixture(t)
	b := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, nil)
	var plans []distributedTargetAssignmentPlan
	var peers []*javaHostStagingPeer
	for i, target := range []string{"gen-a", "gen-b"} {
		caps := testJavaHostCaps()
		caps.Profiles[0].ScriptSha256 = load.JavaHost.Profiles[0].ScriptSHA256
		if i == 1 {
			caps.Profiles[0].PolicySha256 = strings.Repeat("d", 64)
		}
		candidate := load
		candidate.JavaHost = javaHostCapabilitySummary(caps, load.CompatibleFeatures)
		placement, err := javaHostPlacementForGroup(g, candidate)
		if err != nil {
			t.Fatal(err)
		}
		peer := &javaHostStagingPeer{caps: caps}
		peers = append(peers, peer)
		listener := bufconn.Listen(1 << 20)
		server := grpc.NewServer()
		pb.RegisterGeneratorControlServer(server, peer)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() { server.Stop(); _ = listener.Close() })
		client, err := generatorrpc.Dial(t.Context(), target, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		plans = append(plans, distributedTargetAssignmentPlan{target: target, client: client, shards: []GeneratorShardSummary{{Group: g.Name, Target: target, VUsers: 2, ShardOrdinal: uint32(i), ShardCount: 2, JavaHost: placement}}})
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out := b.assignDistributedGroupTargets(ctx, plans, generatorrpc.Script{}, generatorrpc.Assignment{TestID: "run", GroupName: g.Name, ScriptID: "approved", TargetDuration: time.Minute}, "approved", nil, nil)
	if len(out) != 2 {
		t.Fatal(out)
	}
	for i, result := range out {
		if result.err != nil || len(result.results) != 1 || !javaHostPlacementAcknowledged(plans[i].shards[0].JavaHost, result.results[0].JavaHost) || peers[i].heartbeats.Load() != 1 || peers[i].assignments.Load() != 1 {
			t.Fatalf("shard %d lost profile or reservation: %+v", i, result)
		}
	}
}
