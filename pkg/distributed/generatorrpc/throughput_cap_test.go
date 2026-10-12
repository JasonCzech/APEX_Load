package generatorrpc

import (
	"context"
	"math"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func throughputCapTestEngine(t *testing.T) *engine.MultiGroupEngine {
	t.Helper()
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name:   "group",
			VUsers: 1,
			Script: &dsl.Script{
				Name:     "s",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"},
				Actions:  []dsl.Action{{Name: "home", Type: "GET", Method: "GET", Target: "/"}},
			},
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	return mge
}

func TestSetThroughputCapMovesAStagedGroupsCap(t *testing.T) {
	service := newIsolatedService(t, 10, WithExecution())
	mge := throughputCapTestEngine(t)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", executionGeneration: 3, groups: map[string]int32{"group": 1}}
	service.executions["run"] = map[string]*groupExecution{"group": {groupName: "group", executionGeneration: 3, engine: mge}}
	service.mu.Unlock()

	if _, err := service.SetThroughputCap(context.Background(), &pb.ThroughputCapRequest{TestId: "run", ExecutionGeneration: 3, GroupName: "group", Enabled: true, Tps: 5}); err != nil {
		t.Fatalf("SetThroughputCap: %v", err)
	}
	if tps, enabled := mge.GroupThroughputCap("group"); !enabled || tps != 5 {
		t.Fatalf("engine cap = %v/%v, want 5 TPS", tps, enabled)
	}
	for _, tc := range []struct {
		name    string
		request *pb.ThroughputCapRequest
		code    codes.Code
	}{
		{"invalid rate", &pb.ThroughputCapRequest{TestId: "run", ExecutionGeneration: 3, GroupName: "group", Enabled: true, Tps: math.NaN()}, codes.InvalidArgument},
		{"negative rate", &pb.ThroughputCapRequest{TestId: "run", ExecutionGeneration: 3, GroupName: "group", Enabled: true, Tps: -1}, codes.InvalidArgument},
		{"missing group name", &pb.ThroughputCapRequest{TestId: "run", ExecutionGeneration: 3, Enabled: true, Tps: 1}, codes.InvalidArgument},
		{"unknown group", &pb.ThroughputCapRequest{TestId: "run", ExecutionGeneration: 3, GroupName: "other", Enabled: true, Tps: 1}, codes.NotFound},
	} {
		if _, err := service.SetThroughputCap(context.Background(), tc.request); status.Code(err) != tc.code {
			t.Fatalf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}
	if _, err := service.SetThroughputCap(context.Background(), &pb.ThroughputCapRequest{TestId: "run", ExecutionGeneration: 2, GroupName: "group", Enabled: true, Tps: 1}); err == nil {
		t.Fatal("a stale execution generation must not move the cap")
	}
	if tps, _ := mge.GroupThroughputCap("group"); tps != 5 {
		t.Fatalf("a rejected request moved the cap to %v", tps)
	}
}

// The cap is part of an assignment's retry identity only when set, so an
// uncapped assignment keeps the fingerprint older peers compute.
func TestAssignmentFingerprintCoversThroughputCapOnlyWhenSet(t *testing.T) {
	base := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", VuserCount: 2}
	uncapped := workAssignmentFingerprint(base)
	withZero := workAssignmentFingerprint(&pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", VuserCount: 2, ThroughputCapTps: 4})
	if uncapped != withZero {
		t.Fatal("a cap rate without the enable bit changed the fingerprint")
	}
	capped := workAssignmentFingerprint(&pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", VuserCount: 2, ThroughputCapEnabled: true, ThroughputCapTps: 4})
	other := workAssignmentFingerprint(&pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", VuserCount: 2, ThroughputCapEnabled: true, ThroughputCapTps: 5})
	if capped == uncapped || capped == other {
		t.Fatal("a set cap must be part of the fingerprint, and its rate too")
	}
}
