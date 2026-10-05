package storage

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dynatrace"
)

func TestDynatraceOperationAndHealthRejectSupersededExecutionTerm(t *testing.T) {
	s, source, _ := dynatraceMonitorFixture(t)
	checkDynatraceOperationAndHealthRejectSupersededExecutionTerm(t, s, source)
}

func checkDynatraceOperationAndHealthRejectSupersededExecutionTerm(t *testing.T, s *SQLiteStore, source MonitorSource) {
	t.Helper()
	now := time.Now().UTC()
	connection, err := s.GetDynatraceConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	op := &DynatraceOperation{ID: "direct-fenced-operation", ProjectID: source.ProjectID, ConnectionID: connection.ID, ConnectionRevision: connection.Revision, Kind: "verify", PayloadJSON: `{}`, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := s.CreateDynatraceOperation(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimDynatraceOperation(op.ID, "operation-owner", now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	const leaseName = "dynatrace-operation-execution-fence"
	if ok, _, err := s.TryAcquireSchedulerLeaseTerm(leaseName, "controller-owner", now, time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	old, err := s.GetSchedulerLease(leaseName)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseSchedulerLease(leaseName, "controller-owner"); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := s.TryAcquireSchedulerLeaseTerm(leaseName, "intermediate-controller-owner", now, time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.ReleaseSchedulerLease(leaseName, "intermediate-controller-owner"); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := s.TryAcquireSchedulerLeaseTerm(leaseName, "controller-owner", now, time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	fresh, err := s.GetSchedulerLease(leaseName)
	if err != nil || fresh.Generation <= old.Generation {
		t.Fatal(fresh, err)
	}
	if _, err := s.CompleteDynatraceOperationFenced(op.ID, "operation-owner", `{}`, "", now, leaseName, "controller-owner", old.Generation); err == nil {
		t.Fatal("stale same-process execution term completed inspection")
	}
	if _, err := s.UpdateDynatraceConnectionHealthIfRevisionFenced(connection.ID, connection.ProjectID, connection.Revision, "error", "stale", dynatrace.Capabilities{}, now, leaseName, "controller-owner", old.Generation); err == nil {
		t.Fatal("stale same-process execution term overwrote health")
	}
	unchanged, err := s.GetDynatraceConnection(connection.ID)
	if err != nil || unchanged.LastCheckStatus != "connected" || !unchanged.Capabilities.Metrics {
		t.Fatal(unchanged, err)
	}
	stillRunning, err := s.GetDynatraceOperation(op.ID)
	if err != nil || stillRunning.State != "running" || stillRunning.ResultJSON != "" {
		t.Fatal(stillRunning, err)
	}
	if ok, err := s.CompleteDynatraceOperationFenced(op.ID, "wrong-operation-owner", `{}`, "", now, leaseName, "controller-owner", fresh.Generation); err != nil || ok {
		t.Fatal("execution ownership must not bypass the independent inspection lease", ok, err)
	}
	if ok, err := s.CompleteDynatraceOperationFenced(op.ID, "operation-owner", `{}`, "", now, leaseName, "controller-owner", fresh.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevisionFenced(connection.ID, connection.ProjectID, connection.Revision, "error", "fresh", dynatrace.Capabilities{}, now, leaseName, "controller-owner", fresh.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevisionFenced(connection.ID, connection.ProjectID, connection.Revision+1, "connected", "", dynatrace.Capabilities{Metrics: true}, now, leaseName, "controller-owner", fresh.Generation); err != nil || ok {
		t.Fatal("execution ownership must not bypass the connection revision", ok, err)
	}
}
