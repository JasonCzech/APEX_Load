package generatorrpc

import (
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// A generator counts every result that is not a business transaction as a
// request, failed ones included, and sends each group's count with the
// group: a script without transactions still reports what it sent.
func TestGeneratorBatchCarriesEachGroupsRequests(t *testing.T) {
	service := newIsolatedService(t, 50)
	assignment := assignedTest{
		testID: "run", committed: true, vuserCount: 2,
		groups: map[string]int32{"inventory": 1, "browse": 1},
	}
	execution := func(group string, outcomes ...bool) *groupExecution {
		results := &transactionBuffer{lastDrainAt: time.Now().Add(-time.Second)}
		sink := combineSinks(results)
		for _, success := range outcomes {
			if err := sink.RecordResult(&protocol.Result{ActionName: "GET /stock", Group: group, Success: success, StartTime: time.Now()}); err != nil {
				t.Fatalf("record %s request: %v", group, err)
			}
		}
		return &groupExecution{groupName: group, results: results}
	}
	assignment.metricsCollection = nextMetricsCollection()
	for group, exec := range map[string]*groupExecution{
		"inventory": execution("inventory", true, false, true, false),
		// No transaction has completed yet: the requests alone are a window.
		"browse": execution("browse", true, true, true),
	} {
		metrics, ok := service.collectExecutionMetricsWithIdle(exec, false)
		if !ok {
			t.Fatalf("%s's requests were not collected as a window", group)
		}
		applyExecutionMetricsToAssignment(&assignment, group, metrics)
	}
	batch := service.metricsBatchForAssignment("run", assignment, "RUNNING", "")
	if batch.GetRequestCountsVersion() != CurrentRequestCountsVersion {
		t.Fatalf("request counts version = %d, want %d", batch.GetRequestCountsVersion(), CurrentRequestCountsVersion)
	}
	got := map[string][2]int64{}
	for _, group := range groupMetricsFromProto(batch.GetGroups()) {
		got[group.Name] = [2]int64{group.Requests, group.RequestErrors}
	}
	if got["inventory"] != [2]int64{4, 2} || got["browse"] != [2]int64{3, 0} {
		t.Fatalf("group requests = %v, want inventory 4 / 2 and browse 3 / 0", got)
	}
	if batch.GetFleetMetrics().GetCount() != 0 {
		t.Fatalf("requests were counted as %d transactions", batch.GetFleetMetrics().GetCount())
	}
}

// The controller never reads negative requests, or more failed requests
// than attempted.
func TestGroupRequestsFromProtoAreBounded(t *testing.T) {
	groups := groupMetricsFromProto([]*pb.GroupMetric{
		{Name: "inventory", Requests: 3, RequestErrors: 9},
		{Name: "browse", Requests: -2, RequestErrors: 1},
	})
	if groups[0].Requests != 3 || groups[0].RequestErrors != 3 || groups[1].Requests != 0 || groups[1].RequestErrors != 0 {
		t.Fatalf("groups = %+v, want 3 / 3 and 0 / 0", groups)
	}
}
