package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPreparationTakeoverAndExpiredAuditRecovery(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "takeover", true: "expiry"}[expired], func(t *testing.T) {
			s, _, _ := monitorFixture(t)
			exercisePreparationAuditRecovery(t, s, expired)
		})
	}
}

func exercisePreparationAuditRecovery(t *testing.T, s *SQLiteStore, expired bool) {
	t.Helper()
	suffix := "takeover"
	if expired {
		suffix = "expiry"
	}
	now := time.Now().UTC()
	run := &TestRecord{ID: "recovery-run-" + suffix, ProjectID: "monitor-p", Name: "prepare", Status: "QUEUED", StartTime: now}
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	const lease = "controller-execution-leader"
	defer func() {
		_ = s.ReleaseSchedulerLease(lease, "new-controller")
		_ = s.ReleaseSchedulerLease(lease, "old-controller")
	}()
	if held, err := s.TryAcquireSchedulerLease(lease, "old-controller", now, time.Minute); err != nil || !held {
		t.Fatalf("old lease: %v %v", held, err)
	}
	p, err := s.CreatePreparation(Preparation{ID: "recovery-prep-" + suffix, ProjectID: run.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "recovery", OwnerFence: "old-controller", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.TransitionPreparation(p.ID, "preparing", "ready", p.Revision, p.OwnerFence, json.RawMessage(`[{"group":"load","target":"pod-a","vusers":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "INITIALIZING"
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseSchedulerLease(lease, p.OwnerFence); err != nil {
		t.Fatal(err)
	}
	if held, err := s.TryAcquireSchedulerLease(lease, "new-controller", now, time.Minute); err != nil || !held {
		t.Fatalf("successor lease: %v %v", held, err)
	}
	if _, err := s.RenewPreparation(p.ID, p.Revision, p.OwnerFence, time.Hour); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("stale owner renewed: %v", err)
	}
	if _, err := s.ConsumePreparation(p.ID, p.Revision, p.OwnerFence, "old-controller", "old"); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("stale owner consumed: %v", err)
	}
	if _, err := s.ConsumePreparation(p.ID, p.Revision, "new-controller", "new-controller", "new"); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("successor adopted missing reservation: %v", err)
	}
	if expired {
		p.ExpiresAt = now.Add(-time.Second)
		body, _ := json.Marshal(p)
		if _, err := s.db.Exec(`UPDATE preparations SET expires_at=?,payload=? WHERE id=?`, p.ExpiresAt, string(body), p.ID); err != nil {
			t.Fatal(err)
		}
	} else {
		run.Status = "FAILED"
		run.Error = "controller restarted before execution commit"
		if err := s.SaveTest(run); err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := s.LatestPreparation(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "failed"
	if expired {
		want = "expired"
	}
	if recovered.State != want || recovered.Revision != p.Revision+1 || string(recovered.Allocation) != string(p.Allocation) {
		t.Fatalf("recovered audit: %+v", recovered)
	}
	again, err := s.LatestPreparation(run.ID)
	if err != nil || again.Revision != recovered.Revision {
		t.Fatalf("audit recovery was not idempotent: %+v %v", again, err)
	}
}

func TestPreparationMonitorReadinessDoesNotIssueCollectionJobs(t *testing.T) {
	s, source, now := monitorFixture(t)
	if _, err := s.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMonitorBindings(MonitorBindings{ProjectID: source.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET status='INITIALIZING' WHERE id='monitor-run'`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.ValidateRunMonitorReadiness(source.ProjectID, "monitor-run", "monitor-scenario", now); err != nil {
			t.Fatal(err)
		}
	}
	var jobs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_monitor_jobs WHERE test_id='monitor-run'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatal("preparation readiness issued source polling jobs")
	}
	if _, err := s.db.Exec(`UPDATE tests SET status='RUNNING' WHERE id='monitor-run'`); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour))
	if err != nil || len(accepted) != 1 {
		t.Fatalf("execution monitoring: %v %v", accepted, err)
	}
}
