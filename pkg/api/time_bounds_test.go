package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDurationBackedSettingsRejectIntegerOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name   string
		mutate func(*SystemSettings)
	}{
		{"metrics interval", func(s *SystemSettings) { s.General.MetricsIntervalSeconds = maxInt }},
		{"usage interval", func(s *SystemSettings) { s.General.UsageSnapshotIntervalSeconds = maxInt }},
		{"archive default", func(s *SystemSettings) { s.ArchiveRetention.DefaultRetentionDays = maxInt }},
		{"archive har", func(s *SystemSettings) { s.ArchiveRetention.HARRetentionDays = maxInt }},
		{"archive policy", func(s *SystemSettings) {
			s.ArchiveRetention.Policies = []ArchiveRetentionPolicy{{RetentionDays: maxInt}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := defaultSystemSettings(2)
			test.mutate(&settings)
			if err := validateSystemSettings(settings); err == nil {
				t.Fatal("validateSystemSettings accepted a duration-overflowing value")
			}
		})
	}

	project := storage.ProjectSettings{MaxTestDurationMinutes: maxInt}
	if err := validateProjectSettings(project); err == nil {
		t.Fatal("validateProjectSettings accepted a duration-overflowing max test duration")
	}
}

func TestRetentionOverflowFailsClosed(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	completed := time.Now().UTC().Add(-24 * time.Hour)
	candidates := planArchiveRetentionPrune(time.Now().UTC(), ArchiveRetentionSettings{
		Enabled:              true,
		DefaultRetentionDays: maxInt,
	}, []*ManagedTest{{
		ID: "run",
		Archive: &storage.RunArchiveRecord{
			TestID:      "run",
			ArchivePath: "/archive/run",
			CompletedAt: completed,
		},
	}})
	if len(candidates) != 0 {
		t.Fatalf("overflowing retention produced destructive prune candidates: %#v", candidates)
	}

	t.Setenv("APEXLOAD_AUDIT_RETENTION_DAYS", strconv.Itoa(maxInt))
	t.Setenv("APEXLOAD_TIMESLOT_RETENTION_DAYS", strconv.Itoa(maxInt))
	if got := auditRetentionDays(); got != 0 {
		t.Fatalf("auditRetentionDays = %d, want disabled", got)
	}
	if got := timeslotRetentionDays(); got != 0 {
		t.Fatalf("timeslotRetentionDays = %d, want disabled", got)
	}
}
