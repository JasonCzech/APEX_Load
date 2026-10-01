package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPreparationCASIdempotencyAndExpiry(t *testing.T) {
	s, _, now := monitorFixture(t)
	if err := s.SaveTest(&TestRecord{ID: "prepare-run", ProjectID: "monitor-p", Name: "prepare", Status: "QUEUED", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	p := Preparation{ID: "prep-1", ProjectID: "monitor-p", TestID: "prepare-run", ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "request-1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	created, err := s.CreatePreparation(p)
	if err != nil {
		t.Fatal(err)
	}
	retry := p
	retry.ID = "retry"
	if got, err := s.CreatePreparation(retry); err != nil || got.ID != created.ID {
		t.Fatalf("retry: %+v %v", got, err)
	}
	retry.ExecutionRevision = strings.Repeat("b", 64)
	if _, err := s.CreatePreparation(retry); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("changed request: %v", err)
	}
	retry = p
	retry.IdempotencyKey = "request-2"
	retry.ID = "prep-2"
	if _, err := s.CreatePreparation(retry); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("parallel prepare: %v", err)
	}
	ready, err := s.TransitionPreparation(p.ID, "preparing", "ready", 1, "", json.RawMessage(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if ready.ReadyAt.IsZero() || ready.TTLSeconds != 3600 || ready.ExpiresAt.Sub(ready.ReadyAt) != time.Hour {
		t.Fatalf("ready lifetime: %+v", ready)
	}
	renewed, err := s.RenewPreparation(p.ID, ready.Revision, "", 2*time.Hour)
	if err != nil || renewed.ExpiresAt.Sub(ready.ReadyAt) < 2*time.Hour {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	if _, err = s.RenewPreparation(p.ID, ready.Revision, "", time.Hour); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("stale renewal: %v", err)
	}
	ready = renewed
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.TransitionPreparation(p.ID, "ready", "consumed", ready.Revision, "", nil)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrPreparationConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("consumers=%d", winners)
	}
	if _, err := s.TransitionPreparation(p.ID, "consumed", "cancelled", 3, "", nil); err == nil {
		t.Fatal("cancelled consumed preparation")
	}
	// A later short-lived preparation must supersede earlier long-lived history.
	retry.CreatedAt = now.Add(time.Second)
	retry.ExpiresAt = now.Add(10 * time.Minute)
	next, err := s.CreatePreparation(retry)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestPreparation(p.TestID)
	if err != nil || latest.ID != next.ID {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	next.ExpiresAt = now.Add(-time.Second)
	body, _ := json.Marshal(next)
	if _, err := s.db.Exec(`UPDATE preparations SET expires_at=?,payload=? WHERE id=?`, next.ExpiresAt, string(body), next.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionPreparation(next.ID, "preparing", "ready", next.Revision, "", nil); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("expired readiness: %v", err)
	}
	if _, err := s.TransitionPreparation(next.ID, "preparing", "expired", next.Revision, "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestPreparationForeignKeyOwnership(t *testing.T) {
	specs, err := postgresForeignKeySpecs()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"preparations", "analysis_imports", "monitor_sources", "run_monitor_jobs"} {
		found := false
		for _, spec := range specs {
			if spec.table == table && spec.column == "project_id" && spec.references == "projects" && spec.onDelete == "CASCADE" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing project ownership constraint for %s", table)
		}
	}
}

func TestPreparationConsumptionAttributesOnlyWinningStarter(t *testing.T) {
	s, _, now := monitorFixture(t)
	run := &TestRecord{ID: "actor-run", ProjectID: "monitor-p", Name: "prepare", Status: "QUEUED", StartTime: now, StartedBySubject: "preparer"}
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePreparation(Preparation{ID: "actor-prep", ProjectID: run.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "actor", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "INITIALIZING"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	p, err = s.TransitionPreparation(p.ID, "preparing", "ready", p.Revision, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumePreparation(p.ID, p.Revision, "", "starter", "Starter"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumePreparation(p.ID, p.Revision, "", "loser", "Loser"); !errors.Is(err, ErrPreparationConflict) {
		t.Fatal("duplicate consume", err)
	}
	got, err := s.GetTest(run.ID)
	if err != nil || got.StartedBySubject != "starter" || got.StartedByDisplayName != "Starter" {
		t.Fatalf("starter: %+v %v", got, err)
	}
}

func TestPreparationMonitorSnapshotRejectsStaleAndSurvivesPostConsumeEdit(t *testing.T) {
	for _, editBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "after"}[editBefore], func(t *testing.T) {
			s, source, now := monitorFixture(t)
			run := &TestRecord{ID: "monitor-run", Name: "prepared monitor", ProjectID: source.ProjectID, ScenarioID: "monitor-scenario", Status: "QUEUED", StartTime: now}
			if err := s.SaveTest(run); err != nil {
				t.Fatal(err)
			}
			saved, err := s.SaveMonitorSource(source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.SaveMonitorBindings(MonitorBindings{ProjectID: source.ProjectID, ScenarioID: run.ScenarioID, Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}, 0); err != nil {
				t.Fatal(err)
			}
			p, err := s.CreatePreparation(Preparation{ID: "snapshot", ProjectID: run.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("b", 64), IdempotencyKey: "snapshot", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			run.Status = "INITIALIZING"
			if err = s.SaveTest(run); err != nil {
				t.Fatal(err)
			}
			p, err = s.TransitionPreparation(p.ID, "preparing", "ready", p.Revision, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			edit := func() {
				saved.Enabled = false
				if _, err = s.SaveMonitorSource(*saved, saved.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if editBefore {
				edit()
			}
			_, err = s.ConsumePreparation(p.ID, p.Revision, "", "starter", "Starter")
			if editBefore {
				if !errors.Is(err, ErrPreparationConflict) {
					t.Fatalf("stale snapshot consumed: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			edit()
			jobs, err := s.StartRunMonitors(run.ProjectID, run.ID, run.ScenarioID, now, now.Add(time.Hour))
			if err != nil || len(jobs) != 1 || !jobs[0].Source.Enabled || jobs[0].Source.Revision != 1 || jobs[0].State != "collecting" {
				t.Fatalf("execution did not use consumed snapshot: %+v %v", jobs, err)
			}
		})
	}
}
