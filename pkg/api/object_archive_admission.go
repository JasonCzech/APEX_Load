package api

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
	"time"
)

// Always consult durable source state at creation and launch. Executable run
// snapshots remain immutable; archiving never interrupts an active execution.
func (tr *TestRunner) checkArchivedExecution(scenarioID string, scriptIDs []string, plannedAt ...time.Time) error {
	if scenarioID != "" && tr.scenarioStore != nil {
		obj, _, err := tr.scenarioStore.GetScenario(scenarioID)
		if err == nil && obj.ArchivedAt != nil {
			return fmt.Errorf("scenario %s: %w", scenarioID, storage.ErrObjectArchived)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read scenario archive state: %w", err)
		}
		if err := checkRestoredScheduleSource(tr.scenarioStore, "scenario", scenarioID, plannedAt); err != nil {
			return err
		}
	}
	if source, ok := tr.scriptStore.(interface {
		GetScript(string) (*dsl.Script, error)
	}); ok {
		for _, id := range scriptIDs {
			if id == "" {
				continue
			}
			obj, err := source.GetScript(id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("read script archive state: %w", err)
			}
			if err == nil && obj.ArchivedAt != nil {
				return fmt.Errorf("script %s: %w", id, storage.ErrObjectArchived)
			}
			if err := checkRestoredScheduleSource(tr.scriptStore, "script", id, plannedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tr *TestRunner) checkArchivedExecutionPlan(plan *storage.ExecutionPlan, plannedAt ...time.Time) error {
	if plan == nil || tr.scenarioStore == nil {
		return nil
	}
	for _, entry := range plan.Entries {
		if !entry.IsEnabled() {
			continue
		}
		scenario, groups, err := tr.scenarioStore.GetScenario(entry.ScenarioID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read execution plan scenario archive state: %w", err)
		}
		if scenario.ArchivedAt != nil {
			return fmt.Errorf("scenario %s: %w", entry.ScenarioID, storage.ErrObjectArchived)
		}
		ids := make([]string, 0, len(groups))
		for _, group := range groups {
			ids = append(ids, group.ScriptID)
		}
		if err := tr.checkArchivedExecution(entry.ScenarioID, ids, plannedAt...); err != nil {
			return err
		}
	}
	return nil
}

func checkRestoredScheduleSource(source any, kind, id string, plannedAt []time.Time) error {
	if len(plannedAt) == 0 {
		return nil
	}
	store, ok := source.(interface {
		ObjectRestoredAfter(string, string, time.Time) (bool, error)
	})
	if !ok {
		return nil
	}
	restored, err := store.ObjectRestoredAfter(kind, id, plannedAt[0])
	if err != nil {
		return fmt.Errorf("read %s restoration schedule boundary: %w", kind, err)
	}
	if restored {
		return fmt.Errorf("%s %s: missed occurrence precedes restoration and cannot be replayed: %w", kind, id, storage.ErrObjectArchived)
	}
	return nil
}
