package api

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

var errOccurrenceReplyLost = errors.New("occurrence reply lost")
var errOccurrenceReadUnavailable = errors.New("occurrence readback unavailable")

type occurrenceOutcomeStore struct {
	*storage.SQLiteStore
	commit      bool
	readFailure bool
	wrote       bool
}

func (s *occurrenceOutcomeStore) CompareAndSwapScenarioScheduleOccurrence(o *storage.ScenarioScheduleOccurrence, revision int64) (bool, error) {
	if s.commit {
		if _, err := s.SQLiteStore.CompareAndSwapScenarioScheduleOccurrence(o, revision); err != nil {
			return false, err
		}
	}
	s.wrote = true
	return false, errOccurrenceReplyLost
}
func (s *occurrenceOutcomeStore) GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(id string, planned time.Time) (*storage.ScenarioScheduleOccurrence, error) {
	if s.wrote && s.readFailure {
		return nil, errOccurrenceReadUnavailable
	}
	return s.SQLiteStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(id, planned)
}
func (s *occurrenceOutcomeStore) CompareAndSwapExecutionPlanScheduleOccurrence(o *storage.ExecutionPlanScheduleOccurrence, revision int64) (bool, error) {
	if s.commit {
		if _, err := s.SQLiteStore.CompareAndSwapExecutionPlanScheduleOccurrence(o, revision); err != nil {
			return false, err
		}
	}
	s.wrote = true
	return false, errOccurrenceReplyLost
}
func (s *occurrenceOutcomeStore) GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(id string, planned time.Time) (*storage.ExecutionPlanScheduleOccurrence, error) {
	if s.wrote && s.readFailure {
		return nil, errOccurrenceReadUnavailable
	}
	return s.SQLiteStore.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(id, planned)
}

func TestOccurrenceOutcomeAdaptersReconcileLostReplies(t *testing.T) {
	for _, kind := range []string{"scenario", "execution plan"} {
		for _, tc := range []struct {
			name                string
			commit, readFailure bool
			wantSuccess         bool
		}{
			{"committed exact transition", true, false, true},
			{"not committed", false, false, false},
			{"committed readback unavailable", true, true, false},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				base, _, planSchedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
				store := &occurrenceOutcomeStore{SQLiteStore: base, commit: tc.commit, readFailure: tc.readFailure}
				runner := NewTestRunner(1, NewHub(), store)
				const revision = int64(1)
				var gotRevision int64
				var err error
				if kind == "execution plan" {
					occurrence := saveMaterializingExecutionPlanOccurrence(t, base, planSchedule, "run-exact", now)
					occurrence.Status = "materialized"
					occurrence.ReasonCode = "started"
					err = runner.persistExecutionPlanScheduleOccurrenceTransitionWithTerm(occurrence, revision, executionLeaseTerm{})
					gotRevision = occurrence.Revision
				} else {
					if err := base.SaveScenario(&storage.Scenario{ID: "scenario-outcome", Name: "Outcome", Duration: "1m", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
						t.Fatal(err)
					}
					planned := now.Add(time.Hour)
					schedule := &storage.ScenarioSchedule{ID: "scenario-outcome-schedule", ScenarioID: "scenario-outcome", ProjectID: storage.DefaultProjectID, Name: "Outcome", CronExpr: "0 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &planned, CreatedAt: now, UpdatedAt: now}
					if err := base.SaveScenarioSchedule(schedule); err != nil {
						t.Fatal(err)
					}
					occurrence := &storage.ScenarioScheduleOccurrence{ID: "scenario-outcome-occurrence", ScheduleID: schedule.ID, ScenarioID: schedule.ScenarioID, ProjectID: schedule.ProjectID, ScheduleRevision: schedule.Revision, PlannedAt: planned, Status: "materializing", CreatedAt: now, UpdatedAt: now}
					if changed, err := base.CreateScenarioScheduleOccurrenceIfAbsent(occurrence); err != nil || !changed {
						t.Fatalf("create occurrence = %v, %v", changed, err)
					}
					occurrence.Status = "materialized"
					occurrence.ReasonCode = "started"
					runner.mu.Lock()
					err = runner.saveScenarioScheduleOccurrenceForSweepLocked(occurrence)
					runner.mu.Unlock()
					gotRevision = occurrence.Revision
				}
				if tc.wantSuccess {
					if err != nil || gotRevision != revision+1 {
						t.Fatalf("exact persisted transition not adopted: revision %d, error %v", gotRevision, err)
					}
				} else {
					if !errors.Is(err, errOccurrenceReplyLost) || gotRevision != revision {
						t.Fatalf("uncertain transition not preserved for retry: revision %d, error %v", gotRevision, err)
					}
				}
			})
		}
	}
}
