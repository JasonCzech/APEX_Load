package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSaveScenarioScheduleLockedRejectsStaleOverwrite(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &storage.Project{ID: "schedule-project", Name: "Schedules", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "schedule-script", ProjectID: project.ID, Name: "Schedule", Protocol: "http", Version: "1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Schedule", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	schedule := &storage.ScenarioSchedule{
		ID: "schedule-cas", ScenarioID: scenario.ID, ProjectID: project.ID, Revision: 1,
		Name: "Original", CronExpr: "0 * * * *", Timezone: "UTC", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	runner.mu.Lock()
	err = runner.saveScenarioScheduleLocked(schedule)
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	external := *schedule
	external.Revision = 2
	external.Name = "External winner"
	external.UpdatedAt = now.Add(time.Minute)
	if changed, err := store.UpdateScenarioScheduleIfRevision(&external, 1); err != nil || !changed {
		t.Fatalf("external update = %v, %v", changed, err)
	}
	stale := *schedule
	stale.Revision = 2
	stale.Name = "Stale overwrite"
	stale.UpdatedAt = now.Add(2 * time.Minute)
	runner.mu.Lock()
	err = runner.saveScenarioScheduleLocked(&stale)
	runner.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "changed or was deleted") {
		t.Fatalf("stale schedule update error = %v", err)
	}
	loaded, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "External winner" || loaded.Revision != 2 {
		t.Fatalf("stale schedule overwrote winner: %#v", loaded)
	}
}
