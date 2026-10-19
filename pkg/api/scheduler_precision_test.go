package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// The scheduler keeps its occurrence transition in memory while resolving a
// lost commit reply against the durable row. Exercise those exact matches on
// SQLite here and on PostgreSQL in the integration entrypoint.
func TestScenarioSchedulePrecisionAPI(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	exerciseScenarioSchedulePrecisionAPI(t, server, store)
}

type scenarioPrecisionOutcomeStore struct {
	*storage.SQLiteStore
	loseReply bool
	intended  *storage.ScenarioScheduleOccurrence
}

func (s *scenarioPrecisionOutcomeStore) CompareAndSwapScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence, revision int64) (bool, error) {
	if occurrence.MaterializedAt != nil {
		s.intended = cloneScenarioScheduleOccurrence(occurrence)
	}
	changed, err := s.SQLiteStore.CompareAndSwapScenarioScheduleOccurrence(occurrence, revision)
	if err == nil && changed && occurrence.MaterializedAt != nil && s.loseReply {
		return false, errOccurrenceReplyLost
	}
	return changed, err
}

func exerciseScenarioSchedulePrecisionAPI(t *testing.T, server *Server, store *storage.SQLiteStore) {
	for _, recoverRun := range []bool{false, true} {
		for _, loseReply := range []bool{false, true} {
			name := fmt.Sprintf("materialized_recovery_%t_lost_reply_%t", recoverRun, loseReply)
			t.Run(name, func(t *testing.T) {
				runner := server.runner
				wrapper := &scenarioPrecisionOutcomeStore{SQLiteStore: store, loseReply: loseReply}
				originalStore := runner.scenarioStore
				runner.scenarioStore = wrapper
				t.Cleanup(func() { runner.scenarioStore = originalStore })
				storePreflightScript(runner, name, storage.DefaultProjectID)
				scenario, err := runner.StoreScenario(CreateScenarioRequest{
					Name: name, Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: name, VUsers: 1}},
				})
				if err != nil {
					t.Fatal(err)
				}
				// Force a nonzero sub-microsecond part on every platform while
				// retaining the real clock's monotonic reading for the normal path.
				now := time.Now()
				now = now.Add(789 - time.Duration(now.Nanosecond()%int(time.Microsecond)))
				if recoverRun {
					now = now.In(time.FixedZone("IST", 5*60*60+30*60))
				}
				planned := now.UTC().Truncate(time.Minute).Add(-time.Minute)
				schedule := &storage.ScenarioSchedule{
					ID: name, ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Name: name,
					CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &planned,
					CreatedAt: planned.Add(-time.Hour), UpdatedAt: planned.Add(-time.Hour),
				}
				runner.mu.Lock()
				err = runner.saveScenarioScheduleLocked(schedule)
				runner.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				// This test prepares runs without dispatching a backend. Remove
				// their execution contexts before the server's normal shutdown.
				t.Cleanup(func() {
					var testIDs []string
					runner.mu.Lock()
					for id, run := range runner.tests {
						if run.ScenarioID == scenario.ID {
							if run.cancel != nil {
								run.cancel()
							}
							delete(runner.tests, id)
							testIDs = append(testIDs, id)
						}
					}
					runner.scenarios[scenario.ID].Schedules = nil
					runner.mu.Unlock()
					if err := store.DeleteScenarioSchedule(schedule.ID); err != nil {
						t.Errorf("delete schedule: %v", err)
					}
					for _, id := range testIDs {
						if err := store.DeleteTest(id); err != nil {
							t.Errorf("delete prepared run: %v", err)
						}
					}
				})
				if recoverRun {
					run, err := runner.CreateTest(CreateTestRequest{Name: name, ScenarioID: scenario.ID})
					if err != nil {
						t.Fatal(err)
					}
					runner.mu.Lock()
					runner.tests[run.ID].Status = "RUNNING"
					runner.mu.Unlock()
					if err := store.UpdateTestStatus(run.ID, "RUNNING", nil, ""); err != nil {
						t.Fatal(err)
					}
					occurrence := runner.buildScenarioScheduleOccurrence(schedule, planned, "materializing", "due", "", nil, planned)
					occurrence.TestID = run.ID
					if changed, err := store.CreateScenarioScheduleOccurrenceIfAbsent(occurrence); err != nil || !changed {
						t.Fatalf("create recovery occurrence = %t, %v", changed, err)
					}
				}
				runner.mu.Lock()
				_, err = runner.runScenarioScheduleLocked(schedule, now)
				runner.mu.Unlock()
				if err != nil {
					t.Fatalf("materialize schedule (recover=%t, lost reply=%t): %v", recoverRun, loseReply, err)
				}
				if wrapper.intended == nil || wrapper.intended.MaterializedAt == nil {
					t.Fatal("materialization was not persisted")
				}
				persisted, err := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, planned)
				if err != nil || persisted == nil || persisted.MaterializedAt == nil {
					t.Fatalf("stored materialization = %+v, %v", persisted, err)
				}
				if persisted.Status != "materialized" || schedule.LastOccurrence == nil || schedule.LastOccurrence.Status != "materialized" {
					t.Fatalf("materialization was not adopted: durable=%+v, cached=%+v", persisted, schedule.LastOccurrence)
				}
				held := *wrapper.intended.MaterializedAt
				requireAPIStoredTimestamp(t, "scenario occurrence materialized_at", held, *persisted.MaterializedAt)
				if held.Location() != now.Location() {
					t.Errorf("materialization changed zone from %v to %v", now.Location(), held.Location())
				}
				if !recoverRun && held == held.Round(0) {
					t.Error("materialization lost the sweep clock's monotonic reading")
				}
				if schedule.LastRunAt == nil {
					t.Fatal("schedule has no last_run_at")
				}
				requireAPIStoredTimestamp(t, "scenario schedule last_run_at", *schedule.LastRunAt, *persisted.MaterializedAt)
			})
		}
	}
	t.Run("failed_before_launch", func(t *testing.T) {
		runner := server.runner
		storePreflightScript(runner, "precision-scheduled-failure", storage.DefaultProjectID)
		run, err := runner.CreateTest(CreateTestRequest{Name: "failed scheduled run", ScriptID: "precision-scheduled-failure", VUsers: 1, Duration: "1m"})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		now = now.Add(789 - time.Duration(now.Nanosecond()%int(time.Microsecond)))
		runner.mu.Lock()
		err = runner.failScheduledTestBeforeLaunchLocked(runner.tests[run.ID], now, "schedule persistence failed")
		held := runner.tests[run.ID].EndedAt
		runner.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := store.GetTest(run.ID)
		if err != nil || persisted == nil || persisted.EndTime == nil || held == nil {
			t.Fatalf("failed run ended_at = %v, durable = %+v, %v", held, persisted, err)
		}
		requireAPIStoredTimestamp(t, "failed scheduled run ended_at", *held, *persisted.EndTime)
		// This path has always cloned its endpoint into UTC; it does not use
		// the endpoint's monotonic reading for elapsed-time calculations.
		if held.Location() != time.UTC {
			t.Error("pre-launch failure changed its UTC timestamp zone")
		}
	})
}
