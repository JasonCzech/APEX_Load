package api

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type scheduleOccurrenceRetentionStoreSpy struct {
	storage.Store
	scenarioPruned int64
	planPruned     int64
	scenarioErr    error
	planErr        error
	scenarioCutoff time.Time
	planCutoff     time.Time
	scenarioLimit  int
	planLimit      int
	audit          *storage.AuditEvent
}

func (s *scheduleOccurrenceRetentionStoreSpy) DeleteTerminalScenarioScheduleOccurrencesBefore(cutoff time.Time, limit int) (int64, error) {
	s.scenarioCutoff, s.scenarioLimit = cutoff, limit
	return s.scenarioPruned, s.scenarioErr
}

func (s *scheduleOccurrenceRetentionStoreSpy) DeleteTerminalExecutionPlanScheduleOccurrencesBefore(cutoff time.Time, limit int) (int64, error) {
	s.planCutoff, s.planLimit = cutoff, limit
	return s.planPruned, s.planErr
}

func (s *scheduleOccurrenceRetentionStoreSpy) SaveAuditEvent(event *storage.AuditEvent) error {
	s.audit = event
	return nil
}

func TestScheduleOccurrenceRetentionConfiguration(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION", "")
		if got := scheduleOccurrenceRetention(); got != 90*24*time.Hour {
			t.Fatalf("retention = %s, want 90 days", got)
		}
	})
	t.Run("duration", func(t *testing.T) {
		t.Setenv("APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION", "720h")
		if got := scheduleOccurrenceRetention(); got != 30*24*time.Hour {
			t.Fatalf("retention = %s, want 30 days", got)
		}
	})
	t.Run("zero disables", func(t *testing.T) {
		t.Setenv("APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION", "0")
		if got := scheduleOccurrenceRetention(); got != 0 {
			t.Fatalf("retention = %s, want disabled", got)
		}
	})
	t.Run("invalid uses default", func(t *testing.T) {
		t.Setenv("APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION", "forever")
		if got := scheduleOccurrenceRetention(); got != defaultScheduleOccurrenceRetention {
			t.Fatalf("retention = %s, want default", got)
		}
	})
}

func TestPruneScheduleOccurrenceRetentionUsesOneBoundedBatchPerTable(t *testing.T) {
	t.Setenv("APEXLOAD_SCHEDULE_OCCURRENCE_RETENTION", "24h")
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store := &scheduleOccurrenceRetentionStoreSpy{
		scenarioPruned: 4,
		planErr:        errors.New("plan store unavailable"),
	}
	server := &Server{store: store}
	server.pruneScheduleOccurrenceRetention(now)

	wantCutoff := now.Add(-24 * time.Hour)
	if !store.scenarioCutoff.Equal(wantCutoff) || !store.planCutoff.Equal(wantCutoff) {
		t.Fatalf("cutoffs = %v / %v, want %v", store.scenarioCutoff, store.planCutoff, wantCutoff)
	}
	if store.scenarioLimit != 1000 || store.planLimit != 1000 {
		t.Fatalf("limits = %d / %d, want 1000 per table", store.scenarioLimit, store.planLimit)
	}
	if store.audit == nil || store.audit.Status != "partial" || store.audit.Action != "schedule_occurrence.retention_prune" {
		t.Fatalf("audit = %#v, want partial retention event", store.audit)
	}
	if got := store.audit.Details["error"]; got != "plan store unavailable" {
		t.Fatalf("audit error = %#v", got)
	}
}
