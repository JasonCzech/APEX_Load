package api

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const metricsGroupRampProtocolName = "api-metrics-group-ramp-test"

type metricsGroupRampProtocol struct{}

func (*metricsGroupRampProtocol) Name() string { return metricsGroupRampProtocolName }
func (*metricsGroupRampProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*metricsGroupRampProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	<-ctx.Done()
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*metricsGroupRampProtocol) Teardown(context.Context) error    { return nil }
func (*metricsGroupRampProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register(metricsGroupRampProtocolName, func() protocol.Protocol {
		return &metricsGroupRampProtocol{}
	})
}

func TestGroupActiveVUsersPrefersLiveCount(t *testing.T) {
	if got := groupActiveVUsers(&engine.GroupMetrics{ActiveVUsers: 7}, 100); got != 7 {
		t.Fatalf("live count = %d, want 7", got)
	}
	if got := groupActiveVUsers(&engine.GroupMetrics{ActiveVUsersReported: true}, 100); got != 0 {
		t.Fatalf("reported zero = %d, want 0", got)
	}
	// Legacy snapshots without a per-group gauge still use configured size.
	if got := groupActiveVUsers(&engine.GroupMetrics{}, 100); got != 100 {
		t.Fatalf("fallback = %d, want configured 100", got)
	}
	if got := groupActiveVUsers(nil, 42); got != 42 {
		t.Fatalf("nil fallback = %d, want 42", got)
	}
}

// buildLiveMetricsFromSnapshot must surface the engine's live per-group active
// count (the ramping value) rather than the static configured size — this is what
// keeps the group-filtered load-impact x-axis from collapsing.
func TestBuildLiveMetricsFromSnapshotUsesLiveGroupVUsers(t *testing.T) {
	snap := engine.MetricsSnapshot{
		ByGroup: map[string]*engine.GroupMetrics{
			"buyers":   {Name: "buyers", ActiveVUsers: 6, ActiveVUsersReported: true, Transactions: 3},
			"browsers": {Name: "browsers", ActiveVUsersReported: true},
			"legacy":   {Name: "legacy", Transactions: 1},
		},
	}
	// Configured sizes (static) differ from the live ramping counts.
	configured := map[string]int{"buyers": 50, "browsers": 30, "legacy": 20}

	live := buildLiveMetricsFromSnapshot(snap, configured)

	if live.GroupMetrics["buyers"].VUsers != 6 {
		t.Fatalf("buyers VUsers = %d, want live 6", live.GroupMetrics["buyers"].VUsers)
	}
	if live.GroupMetrics["browsers"].VUsers != 0 {
		t.Fatalf("browsers VUsers = %d, want reported zero", live.GroupMetrics["browsers"].VUsers)
	}
	if live.GroupMetrics["legacy"].VUsers != 20 {
		t.Fatalf("legacy VUsers = %d, want configured fallback 20", live.GroupMetrics["legacy"].VUsers)
	}
}

func TestBuildLiveMetricsFromRunningMultiGroupRamp(t *testing.T) {
	script := func(name string) *dsl.Script {
		return &dsl.Script{
			Name:     name,
			Protocol: metricsGroupRampProtocolName,
			Actions:  []dsl.Action{{Name: "hold", Type: "request", Target: "/"}},
		}
	}
	groups := []engine.GroupConfig{
		{Name: "buyers", Script: script("buyers"), VUsers: 3, RampUp: 30 * time.Second},
		{Name: "sellers", Script: script("sellers"), VUsers: 3, RampUp: 30 * time.Second},
		{Name: "browsers", Script: script("browsers"), VUsers: 3, RampUp: 30 * time.Second},
	}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{Groups: groups, Duration: 30 * time.Second})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	var runErr error
	go func() {
		_, runErr = mge.Run(ctx)
		close(finished)
	}()
	stop := func() {
		cancel()
		select {
		case <-finished:
			if runErr != nil {
				t.Errorf("Run after cancellation: %v", runErr)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("multi-group metrics engine did not stop")
		}
	}
	t.Cleanup(stop)

	configured := map[string]int{"buyers": 3, "sellers": 3, "browsers": 3}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, ok := mge.LatestSnapshot()
		if !ok {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		live := buildLiveMetricsFromSnapshot(snap, configured)
		if len(live.GroupMetrics) != len(configured) {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		for name := range configured {
			if got := live.GroupMetrics[name].VUsers; got != 1 {
				t.Fatalf("%s public live VUsers = %d, want first ramp step 1", name, got)
			}
		}
		stop()
		return
	}
	t.Fatal("timed out waiting for a public live snapshot from the running multi-group ramp")
}
