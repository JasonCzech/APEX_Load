package generatorrpc

import (
	"context"
	"encoding/json"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type originalSourceRPC struct {
	pb.GeneratorControlClient
	request *pb.WorkAssignment
	ack     *pb.BrowserAuthoredDefinition
}

func (r *originalSourceRPC) AssignWork(_ context.Context, q *pb.WorkAssignment, _ ...grpc.CallOption) (*pb.AssignmentAck, error) {
	r.request = q
	return &pb.AssignmentAck{Accepted: true, BrowserAuthoredDefinition: r.ack}, nil
}
func originalWireSource() protocol.BrowserAuthoredDefinition {
	return protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
}
func TestBrowserOriginalSourceNegotiation(t *testing.T) {
	source := originalWireSource()
	for _, tc := range []struct {
		name                                  string
		features                              []string
		prepare, stale, ack, forward, success bool
	}{
		{"old", []string{FeaturePreparationV1}, true, false, false, false, true},
		{"no-preparation", []string{FeatureBrowserOriginalSourceV1}, true, false, false, false, true},
		{"immediate", []string{FeatureBrowserOriginalSourceV1, FeaturePreparationV1}, false, false, false, false, true},
		{"stale", []string{FeatureBrowserOriginalSourceV1, FeaturePreparationV1}, true, true, false, false, true},
		{"rollback-ack", []string{FeatureBrowserOriginalSourceV1, FeaturePreparationV1}, true, false, false, true, false},
		{"current", []string{FeatureBrowserOriginalSourceV1, FeaturePreparationV1}, true, false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &originalSourceRPC{}
			if tc.ack {
				rpc.ack = browserAuthoredDefinitionToProto(source)
			}
			c := &Client{rpc: rpc, connection: &sharedClientConnection{}}
			c.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: tc.features})
			if tc.stale {
				c.connection.browserInteractionObserved = time.Now().Add(-31 * time.Second)
			}
			result, err := c.AssignWork(context.Background(), Assignment{PrepareOnly: tc.prepare, BrowserAuthoredDefinition: source})
			if (err == nil && result.Accepted) != tc.success || rpc.request == nil || (rpc.request.BrowserAuthoredDefinition != nil) != tc.forward || rpc.request.PrepareOnly != tc.prepare {
				t.Fatalf("request=%+v result=%+v err=%v", rpc.request, result, err)
			}
			c.observeBrowserInteractionVersion(nil)
			if c.browserOriginalSourceNegotiated() {
				t.Fatal("disconnected proof capability")
			}
		})
	}
	rpc := &originalSourceRPC{}
	c := &Client{rpc: rpc}
	source.Version = 99
	if strconv.IntSize > 32 {
		oversized := uint64(1) << 32
		source.Version = int(oversized) + 1
	}
	if _, err := c.AssignWork(context.Background(), Assignment{BrowserAuthoredDefinition: source}); err == nil || rpc.request != nil {
		t.Fatal("invalid descriptor was silently downgraded")
	}
}
func TestBrowserOriginalSourceFingerprintAndValidation(t *testing.T) {
	q := &pb.WorkAssignment{PrepareOnly: true, BrowserAuthoredDefinition: browserAuthoredDefinitionToProto(originalWireSource())}
	original := workAssignmentFingerprint(q)
	for _, change := range []func(*pb.WorkAssignment){func(q *pb.WorkAssignment) { q.BrowserAuthoredDefinition = nil }, func(q *pb.WorkAssignment) { q.BrowserAuthoredDefinition.Version++ }, func(q *pb.WorkAssignment) { q.BrowserAuthoredDefinition.ScriptSha256 = strings.Repeat("c", 64) }, func(q *pb.WorkAssignment) { q.BrowserAuthoredDefinition.StructureSha256 = strings.Repeat("d", 64) }} {
		changed := proto.Clone(q).(*pb.WorkAssignment)
		change(changed)
		if workAssignmentFingerprint(changed) == original {
			t.Fatal("different source reused fingerprint")
		}
	}
	if err := validateBrowserOriginalSource(q); err != nil {
		t.Fatal(err)
	}
	q.PrepareOnly = false
	if validateBrowserOriginalSource(q) == nil {
		t.Fatal("source proof accepted without staging")
	}
	q.PrepareOnly = true
	q.BrowserAuthoredDefinition.Version = 2
	if validateBrowserOriginalSource(q) == nil {
		t.Fatal("unknown version accepted")
	}
}
func TestBrowserOriginalSourceStagesWithoutTargetTraffic(t *testing.T) {
	clearTransportEnv(t)
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	service := newIsolatedService(t, 10, WithExecution())
	script := &dsl.Script{Name: "original", Protocol: "browser", Config: dsl.ScriptConfig{BaseURL: target.URL}, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	source := engine.BrowserOriginalAuthoredDefinition(script)
	script.Config.Browser.HARArtifactDir = t.TempDir()
	body, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	q := &pb.WorkAssignment{TestId: "original", GroupName: "browser", ScriptId: "original", ScriptContent: body, VuserCount: 1, PrepareOnly: true, BrowserAuthoredDefinition: browserAuthoredDefinitionToProto(source)}
	ack, err := service.AssignWork(context.Background(), q)
	if err != nil || !ack.GetAccepted() || !proto.Equal(q.BrowserAuthoredDefinition, ack.GetBrowserAuthoredDefinition()) {
		t.Fatalf("ack=%+v err=%v", ack, err)
	}
	service.mu.RLock()
	got := service.executions["original"]["browser"].stagedConfig.Groups[0].BrowserAuthoredDefinition
	service.mu.RUnlock()
	if got != source {
		t.Fatal("staging lost source definition")
	}
	q = proto.Clone(q).(*pb.WorkAssignment)
	q.TestId = "mismatch"
	q.BrowserAuthoredDefinition.StructureSha256 = strings.Repeat("f", 64)
	if ack, err := service.AssignWork(context.Background(), q); err == nil && ack.GetAccepted() {
		t.Fatal("unmatched structure staged")
	}
	if requests.Load() != 0 {
		t.Fatal("staging emitted target traffic")
	}
}
