package generatorrpc

import (
	"context"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"google.golang.org/grpc"
	"testing"
	"time"
)

type documentProofRPC struct {
	pb.GeneratorControlClient
	request *pb.WorkAssignment
	ack     uint32
}

func (r *documentProofRPC) AssignWork(_ context.Context, q *pb.WorkAssignment, _ ...grpc.CallOption) (*pb.AssignmentAck, error) {
	r.request = q
	return &pb.AssignmentAck{Accepted: true, BrowserDocumentProofVersion: r.ack}, nil
}
func TestBrowserDocumentProofNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		features             []string
		prepared, prefer     bool
		requested, ack, want uint32
		accepted             bool
	}{
		{"legacy", nil, false, true, 0, 0, 0, true},
		{"old staged", []string{FeaturePreparationV1}, true, true, 0, 0, 0, true},
		{"new immediate", []string{FeaturePreparationV1, FeatureBrowserDocumentProofV1}, false, true, 0, 0, 0, true},
		{"negotiated", []string{FeaturePreparationV1, FeatureBrowserDocumentProofV1}, true, true, 0, 1, 1, true},
		{"explicit", []string{FeaturePreparationV1, FeatureBrowserDocumentProofV1}, false, false, 1, 1, 1, true},
		{"wrong ack", []string{FeaturePreparationV1, FeatureBrowserDocumentProofV1}, true, true, 0, 0, 1, false},
		{"unsupported", nil, true, false, 1, 0, 0, false},
		{"future", []string{FeaturePreparationV1, FeatureBrowserDocumentProofV1}, true, false, 3, 3, 0, false},
		{"v2 negotiated", []string{FeaturePreparationV1, FeatureBrowserDocumentProofV2}, true, true, 0, 2, 2, true},
		{"v2 on v1", []string{FeaturePreparationV1, FeatureBrowserDocumentProofV1}, true, false, 2, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &documentProofRPC{ack: tc.ack}
			c := &Client{rpc: rpc, connection: &sharedClientConnection{}}
			c.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: tc.features})
			result, err := c.AssignWork(context.Background(), Assignment{PrepareOnly: tc.prepared, PreferBrowserDocumentProof: tc.prefer, BrowserDocumentProofVersion: tc.requested})
			if (err == nil && result.Accepted) != tc.accepted {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if rpc.request != nil && rpc.request.BrowserDocumentProofVersion != tc.want {
				t.Fatal("unexpected version", rpc.request.BrowserDocumentProofVersion)
			}
			if tc.want == 1 && (rpc.request == nil || !rpc.request.PrepareOnly) {
				t.Fatal("proof skipped staging")
			}
		})
	}
	c := &Client{connection: &sharedClientConnection{}}
	c.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: []string{FeaturePreparationV1, FeatureBrowserDocumentProofV1}})
	c.connection.browserInteractionObserved = time.Now().Add(-31 * time.Second)
	if c.browserDocumentProofNegotiated() != 0 {
		t.Fatal("expired capability admitted")
	}
	c.observeBrowserInteractionVersion(nil)
	if c.browserDocumentProofNegotiated() != 0 {
		t.Fatal("disconnected capability admitted")
	}
}
func TestBrowserDocumentProofAssignmentValidation(t *testing.T) {
	for _, tc := range []struct {
		version         uint32
		prepared, valid bool
		assigned        int
	}{{0, false, true, -1}, {0, true, true, -1}, {1, false, false, 1}, {1, true, true, 1}, {2, true, true, 2}, {3, true, false, -1}} {
		q := &pb.WorkAssignment{BrowserDocumentProofVersion: tc.version, PrepareOnly: tc.prepared}
		if (validateBrowserDocumentProofVersion(q) == nil) != tc.valid || assignedBrowserDocumentProofVersion(q) != tc.assigned {
			t.Fatal(tc)
		}
	}
}

func TestBrowserDocumentProofRuntimeAdmission(t *testing.T) {
	on := true
	script := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{CaptureDocumentMetrics: &on}}}
	if err := validateBrowserDocumentProofRuntime(script, &browser.Browser{}); err != nil {
		t.Fatal(err)
	}
	if err := validateBrowserDocumentProofRuntime(script, nil); err == nil {
		t.Fatal("missing runtime acknowledged")
	}
	script.Config.Browser.CaptureDocumentMetrics = nil
	if err := validateBrowserDocumentProofRuntime(script, &browser.Browser{}); err == nil {
		t.Fatal("disabled capture acknowledged")
	}
}
