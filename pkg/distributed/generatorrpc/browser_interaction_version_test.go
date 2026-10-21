package generatorrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
)

type interactionVersionRPC struct {
	pb.GeneratorControlClient
	request    *pb.WorkAssignment
	ackVersion uint32
}

func (r *interactionVersionRPC) AssignWork(_ context.Context, q *pb.WorkAssignment, _ ...grpc.CallOption) (*pb.AssignmentAck, error) {
	r.request = q
	return &pb.AssignmentAck{Accepted: true, BrowserInteractionEvidenceVersion: r.ackVersion}, nil
}

func TestBrowserInteractionVersionNegotiatesBeforeTraffic(t *testing.T) {
	for _, tc := range []struct {
		name            string
		features        []string
		ack             uint32
		calls, accepted bool
	}{
		{"old_peer", []string{FeatureBrowserInteractionEvidenceV1, FeaturePreparationV1}, 0, false, false},
		{"no_preparation", []string{FeatureBrowserInteractionEvidenceV2}, 2, false, false},
		{"rollback_ack", []string{FeatureBrowserInteractionEvidenceV2, FeaturePreparationV1}, 0, true, false},
		{"current", []string{FeatureBrowserInteractionEvidenceV2, FeaturePreparationV1}, 2, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &interactionVersionRPC{ackVersion: tc.ack}
			client := &Client{rpc: rpc, connection: &sharedClientConnection{}}
			client.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: tc.features})
			result, err := client.AssignWork(context.Background(), Assignment{BrowserInteractionEvidenceVersion: 2})
			if (rpc.request != nil) != tc.calls || (err == nil && result.Accepted) != tc.accepted {
				t.Fatalf("request=%+v result=%+v err=%v", rpc.request, result, err)
			}
			if rpc.request != nil && (!rpc.request.PrepareOnly || rpc.request.GetBrowserInteractionEvidenceVersion() != 2) {
				t.Fatal("v2 did not stage without traffic")
			}
		})
	}
}

func TestBrowserInteractionVersionLegacyAndStaleNegotiation(t *testing.T) {
	rpc := &interactionVersionRPC{}
	client := &Client{rpc: rpc, connection: &sharedClientConnection{}}
	if _, err := client.AssignWork(context.Background(), Assignment{}); err != nil || rpc.request.GetPrepareOnly() || rpc.request.GetBrowserInteractionEvidenceVersion() != 0 {
		t.Fatalf("legacy assignment changed: %v", err)
	}
	client.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: []string{FeatureBrowserInteractionEvidenceV2, FeaturePreparationV1}})
	client.connection.browserInteractionObserved = time.Now().Add(-31 * time.Second)
	if client.browserInteractionVersionNegotiated() {
		t.Fatal("stale capability accepted")
	}
	client.observeBrowserInteractionVersion(nil)
	if client.browserInteractionVersionNegotiated() {
		t.Fatal("disconnected capability accepted")
	}
	for _, tc := range []struct {
		version  uint32
		prepared bool
		valid    bool
		output   int
	}{
		{0, false, true, 1}, {1, false, true, 1}, {2, false, false, 2}, {2, true, true, 2}, {3, true, false, 1},
	} {
		q := &pb.WorkAssignment{BrowserInteractionEvidenceVersion: tc.version, PrepareOnly: tc.prepared}
		if (validateBrowserInteractionVersion(q) == nil) != tc.valid || assignedBrowserInteractionVersion(q) != tc.output {
			t.Fatalf("version=%d prepared=%t", tc.version, tc.prepared)
		}
	}
	first := &pb.WorkAssignment{PrepareOnly: true}
	legacy := workAssignmentFingerprint(first)
	first.BrowserInteractionEvidenceVersion = 2
	if workAssignmentFingerprint(first) == legacy {
		t.Fatal("version upgrade reused a legacy assignment fingerprint")
	}
}

func TestBrowserInteractionVersionStagesExactOutputWithoutTargetTraffic(t *testing.T) {
	clearTransportEnv(t)
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	service := newIsolatedService(t, 10, WithExecution())
	for _, version := range []uint32{0, 1, 2} {
		id := fmt.Sprintf("interaction-version-%d", version)
		script := fmt.Sprintf("{\"name\":\"versioned\",\"protocol\":\"browser\",\"config\":{\"base_url\":%q,\"browser\":{\"capture_interactions\":true}},\"actions\":[{\"name\":\"open\",\"type\":\"browser_navigate\",\"target\":\"/\"}]}", target.URL)
		q := &pb.WorkAssignment{TestId: id, GroupName: "web", ScriptId: id, ScriptContent: []byte(script), VuserCount: 1, PrepareOnly: true, BrowserInteractionEvidenceVersion: version}
		ack, err := service.AssignWork(context.Background(), q)
		if err != nil || !ack.GetAccepted() || ack.GetBrowserInteractionEvidenceVersion() != version {
			t.Fatalf("version=%d ack=%+v err=%v", version, ack, err)
		}
		service.mu.RLock()
		got := service.executions[id]["web"].stagedConfig.Groups[0].BrowserInteractionEvidenceVersion
		service.mu.RUnlock()
		if got != assignedBrowserInteractionVersion(q) {
			t.Fatalf("staged version=%d want=%d", got, assignedBrowserInteractionVersion(q))
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("staging emitted %d target requests", requests.Load())
	}
}
