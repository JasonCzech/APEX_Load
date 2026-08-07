package api

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultScheduleOccurrenceRetention = 90 * 24 * time.Hour
	scheduleOccurrenceRetentionBatch   = 1000
)

type scheduleOccurrenceRetentionStore interface {
	DeleteTerminalScenarioScheduleOccurrencesBefore(cutoff time.Time, limit int) (int64, error)
	DeleteTerminalExecutionPlanScheduleOccurrencesBefore(cutoff time.Time, limit int) (int64, error)
}

func scheduleOccurrenceRetention() time.Duration {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION"))
	if raw == "" {
		return defaultScheduleOccurrenceRetention
	}
	retention, err := time.ParseDuration(raw)
	if err != nil || retention < 0 {
		slog.Warn("invalid APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION; using default", "value", raw, "default", defaultScheduleOccurrenceRetention)
		return defaultScheduleOccurrenceRetention
	}
	return retention
}

func scheduleOccurrenceRetentionInterval() time.Duration {
	return firstConfiguredDuration(6*time.Hour,
		"APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION_INTERVAL",
		"APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION_INTERVAL_SECONDS")
}

func (s *Server) pruneScheduleOccurrenceRetention(now time.Time) {
	if s == nil || s.store == nil {
		return
	}
	retentionStore, ok := s.store.(scheduleOccurrenceRetentionStore)
	if !ok {
		return
	}
	retention := scheduleOccurrenceRetention()
	if retention <= 0 {
		return
	}
	cutoff := now.UTC().Add(-retention)
	scenarioPruned, scenarioErr := retentionStore.DeleteTerminalScenarioScheduleOccurrencesBefore(cutoff, scheduleOccurrenceRetentionBatch)
	if scenarioErr != nil {
		slog.Warn("schedule occurrence retention prune failed", "table", "scenario_schedule_occurrences", "error", scenarioErr)
	}
	planPruned, planErr := retentionStore.DeleteTerminalExecutionPlanScheduleOccurrencesBefore(cutoff, scheduleOccurrenceRetentionBatch)
	if planErr != nil {
		slog.Warn("schedule occurrence retention prune failed", "table", "execution_plan_schedule_occurrences", "error", planErr)
	}
	pruneErr := errors.Join(scenarioErr, planErr)
	if scenarioPruned <= 0 && planPruned <= 0 && pruneErr == nil {
		return
	}
	slog.Info("schedule occurrence retention prune",
		"retention", retention,
		"scenario_pruned", scenarioPruned,
		"execution_plan_pruned", planPruned)
	auditStatus := "success"
	if pruneErr != nil {
		auditStatus = "partial"
		if scenarioPruned <= 0 && planPruned <= 0 {
			auditStatus = "failure"
		}
	}
	details := map[string]any{
		"retention":             retention.String(),
		"scenario_pruned":       scenarioPruned,
		"execution_plan_pruned": planPruned,
		"cutoff":                cutoff.Format(time.RFC3339),
	}
	if pruneErr != nil {
		details["error"] = pruneErr.Error()
	}
	if err := s.store.SaveAuditEvent(&storage.AuditEvent{
		ID:           uuid.New().String(),
		Action:       "schedule_occurrence.retention_prune",
		ActorType:    "system",
		ActorSubject: "schedule-occurrence-retention",
		ResourceType: "schedule_occurrences",
		Status:       auditStatus,
		Details:      details,
		CreatedAt:    now.UTC(),
	}); err != nil {
		slog.Warn("failed to persist schedule occurrence retention audit event", "error", err)
	}
}

func (s *Server) startScheduleOccurrenceRetentionCadence() {
	if s == nil || s.store == nil || s.scheduleOccurrenceRetentionCancel != nil || scheduleOccurrenceRetention() <= 0 {
		return
	}
	if _, ok := s.store.(scheduleOccurrenceRetentionStore); !ok {
		return
	}
	interval := scheduleOccurrenceRetentionInterval()
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.scheduleOccurrenceRetentionCancel = cancel
	s.launchBackground(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.isExecutionLeader() {
					s.pruneScheduleOccurrenceRetention(time.Now().UTC())
				}
			}
		}
	})
}
