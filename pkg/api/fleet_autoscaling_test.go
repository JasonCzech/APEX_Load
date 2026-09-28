package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestFleetAutoscalingUsesReconciledCurrentTerm(t *testing.T) {
	t.Setenv("APEXLOAD_FLEET_ID", "cluster-a/performance")
	for _, tc := range []struct {
		name                                                string
		follower, reconciling, expired, changed, incomplete bool
	}{
		{name: "leader"}, {name: "follower", follower: true}, {name: "reconciling", reconciling: true},
		{name: "expired", expired: true}, {name: "term changed during observation", changed: true}, {name: "store observation failed", incomplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leader := newExecutionLeadership(nil, "controller")
			leader.leader.Store(!tc.follower)
			leader.reconciling.Store(tc.reconciling)
			leader.leaseOwnerID = "controller/7"
			leader.durableGeneration = 7
			leader.localLeaseDeadline = time.Now().Add(20 * time.Second)
			if tc.expired {
				leader.localLeaseDeadline = time.Now().Add(-time.Second)
			}
			observedOwner := leader.leaseOwnerID
			if tc.changed {
				observedOwner = "controller/6"
			}
			server := &Server{runner: &TestRunner{leadership: leader}}
			stats := scalingSignals{ActiveVUsers: 120, QueuedVUsers: 60, ActiveProtocolVUsers: map[string]int{"browser": 10}, QueuedProtocolVUsers: map[string]int{"browser": 5}}
			regions := map[string]generatorRegionStats{"east": {CapacityStates: map[string]int{"saturated": 2}}, "west": {CapacityStates: map[string]int{"saturated": 3}}}
			var body strings.Builder
			server.writeFleetAutoscalingMetrics(&body, observedOwner, stats, regions, 9, !tc.incomplete)
			if tc.follower || tc.reconciling || tc.expired || tc.changed || tc.incomplete {
				if body.Len() != 0 {
					t.Fatalf("unsafe observation exported: %s", body.String())
				}
				return
			}
			for _, expected := range []string{
				`fleet="cluster-a/performance",signal="total_vusers"} 180`,
				`fleet="cluster-a/performance",signal="active_vusers"} 120`,
				`fleet="cluster-a/performance",signal="total_browser_vusers"} 15`,
				`fleet="cluster-a/performance",signal="active_browser_vusers"} 10`,
				`fleet="cluster-a/performance",signal="saturated_generators"} 5`,
				`fleet="cluster-a/performance",signal="reserved_replica_floor"} 9`,
				`apexload_fleet_autoscaling_valid_until{fleet="cluster-a/performance"}`,
			} {
				if !strings.Contains(body.String(), expected) {
					t.Errorf("missing %s in %s", expected, body.String())
				}
			}
		})
	}
}

func TestFleetAutoscalingRequiresSafeInstallationIdentity(t *testing.T) {
	for _, id := range []string{"", "${APEXLOAD_FLEET_ID}", "CHANGE-ME-FLEET-ID", "bad\"selector", strings.Repeat("a", 129), "line\nbreak"} {
		t.Run(id, func(t *testing.T) {
			t.Setenv("APEXLOAD_FLEET_ID", id)
			var body strings.Builder
			(&Server{runner: &TestRunner{}}).writeFleetAutoscalingMetrics(&body, "", scalingSignals{}, nil, 0, true)
			if body.Len() != 0 {
				t.Fatal("invalid fleet identity exported")
			}
		})
	}
}

func TestPrometheusHandlerPublishesScopedDemandAndRetainsLegacyMetrics(t *testing.T) {
	t.Setenv("APEXLOAD_FLEET_ID", "test-installation")
	server := NewServer(ServerConfig{MaxConcurrent: 2})
	server.runner.tests["running"] = &ManagedTest{ID: "running", Status: "RUNNING", TotalVUsers: 120}
	server.runner.tests["queued"] = &ManagedTest{ID: "queued", Status: "PENDING", TotalVUsers: 60}
	resp := httptest.NewRecorder()
	server.handlePrometheusMetrics(resp, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{`apexload_fleet_autoscaling{fleet="test-installation",signal="total_vusers"} 180`, `apexload_scaling_vusers{state="active"} 120`} {
		if !strings.Contains(resp.Body.String(), expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	server.runner.leadership = newExecutionLeadership(nil, "follower")
	resp = httptest.NewRecorder()
	server.handlePrometheusMetrics(resp, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(resp.Body.String(), "apexload_fleet_autoscaling{") {
		t.Fatal("follower exports autoscaling demand")
	}
}

func TestAutoscalingRetainsDemandThroughStoppingAndShakeout(t *testing.T) {
	for _, state := range []string{"INITIALIZING", "STARTING", "SHAKEOUT", "SHAKEOUT_REVIEW", "RUNNING", "PAUSED", "STOPPING"} {
		t.Run(state, func(t *testing.T) {
			stats := scalingSignalStats([]*ManagedTest{{Status: state, TotalVUsers: 12, Groups: []TestGroup{{Protocol: "browser", VUsers: 12}}}})
			if stats.ActiveVUsers != 12 || stats.ActiveProtocolVUsers["browser"] != 12 {
				t.Fatalf("occupied demand was lost: %+v", stats)
			}
		})
	}
}

func TestPrometheusQueuedDemandIncludesAdmissionPeak(t *testing.T) {
	t.Setenv("APEXLOAD_FLEET_ID", "peak-test")
	for _, state := range []string{"QUEUED", "PENDING", "RESERVED"} {
		t.Run(state, func(t *testing.T) {
			server := NewServer(ServerConfig{MaxConcurrent: 2})
			server.runner.tests["staged"] = &ManagedTest{ID: "staged", Status: state, TotalVUsers: 10, Groups: []TestGroup{{Name: "browser", Protocol: "browser", VUsers: 10, Stages: []storage.LoadStage{{TargetVUsers: 100}}}}}
			server.runner.tests["legacy"] = &ManagedTest{ID: "legacy", Status: state, TotalVUsers: 20}
			resp := httptest.NewRecorder()
			server.handlePrometheusMetrics(resp, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			for _, want := range []string{`signal="total_vusers"} 120`, `signal="total_browser_vusers"} 100`} {
				if !strings.Contains(resp.Body.String(), want) {
					t.Fatalf("missing %s", want)
				}
			}
		})
	}
}

func TestDueScheduledWorkEmitsPeakAutoscalingDemand(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		status string
		at     *time.Time
		want   int
	}{
		{"future", "SCHEDULED", scheduledDemandTime(now.Add(time.Second)), 0},
		{"boundary", "SCHEDULED", scheduledDemandTime(now), 100},
		{"blocked due schedule", "SCHEDULED", scheduledDemandTime(now.Add(-time.Minute)), 100},
		{"missing schedule", "SCHEDULED", nil, 0},
		{"invalid zero schedule", "SCHEDULED", scheduledDemandTime(time.Time{}), 0},
		{"stopped schedule", "ABORTED", scheduledDemandTime(now.Add(-time.Minute)), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test := &ManagedTest{Status: tc.status, ScheduledStartAt: tc.at, TotalVUsers: 10, Groups: []TestGroup{{Name: "browser", Protocol: "browser", VUsers: 10, Stages: []storage.LoadStage{{TargetVUsers: 100}}}}}
			stats := scalingSignalStatsAt([]*ManagedTest{test}, now)
			if stats.QueuedVUsers != tc.want || stats.QueuedProtocolVUsers["browser"] != tc.want || stats.ActiveVUsers != 0 {
				t.Fatalf("wrong due demand: %+v", stats)
			}
			if hasQueuedScalingDemand(test, now) != (tc.want > 0) {
				t.Fatal("resource and VUser demand disagree")
			}
		})
	}
}

func TestPrometheusDueSchedulePublishesFleetAndResourceDemand(t *testing.T) {
	t.Setenv("APEXLOAD_FLEET_ID", "due-schedule")
	server := NewServer(ServerConfig{MaxConcurrent: 2})
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	server.runner.tests["due"] = &ManagedTest{ID: "due", Status: "SCHEDULED", ScheduledStartAt: &past, TotalVUsers: 10, Groups: []TestGroup{{Name: "load", Protocol: "http", VUsers: 10, Stages: []storage.LoadStage{{TargetVUsers: 320}}}}}
	server.runner.tests["future"] = &ManagedTest{ID: "future", Status: "SCHEDULED", ScheduledStartAt: &future, TotalVUsers: 1000, Groups: []TestGroup{{Name: "later", Protocol: "http", VUsers: 1000}}}
	response := httptest.NewRecorder()
	server.handlePrometheusMetrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{`signal="total_vusers"} 320`, `apexload_scaling_tests{state="queued"} 1`, `apexload_pod_capacity_demand{state="queued",resource="vusers"} 320`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
	server.runner.tests["due"].Status = "ABORTED"
	response = httptest.NewRecorder()
	server.handlePrometheusMetrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `signal="total_vusers"} 0`) {
		t.Fatal("cancelled schedule retained demand")
	}
}

func scheduledDemandTime(t time.Time) *time.Time { return &t }
