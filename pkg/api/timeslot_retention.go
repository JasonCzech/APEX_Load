package api

// timeslot_retention.go — bounded growth for capacity reservations (timeslots).
// The capacity_reservations table otherwise grows forever: a booking lingers even
// after its window has fully elapsed (it can never be consumed again) or after it
// was rejected. Retention is opt-in (APEXLOAD_TIMESLOT_RETENTION_DAYS unset or
// <= 0 prunes nothing, preserving history by default) and runs on a single
// leader-gated cadence so HA replicas never double-prune. A meta audit event
// records each prune for traceability.

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func timeslotRetentionDays() int {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_TIMESLOT_RETENTION_DAYS"))
	if raw == "" {
		return 0
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 0 {
		return 0
	}
	if _, ok := checkedDuration(days, 24*time.Hour); !ok {
		slog.Warn("invalid APEXLOAD_TIMESLOT_RETENTION_DAYS; retention disabled", "value", raw)
		return 0
	}
	return days
}

func timeslotRetentionInterval() time.Duration {
	return firstConfiguredDuration(6*time.Hour, "APEXLOAD_TIMESLOT_RETENTION_INTERVAL", "APEXLOAD_TIMESLOT_RETENTION_INTERVAL_SECONDS")
}

// pruneTimeslotRetention deletes capacity reservations whose window has fully
// elapsed (end_at < cutoff), plus rejected reservations last touched before the
// cutoff. It is a no-op when retention is disabled. Approved/bound future
// reservations are never touched.
func (s *Server) pruneTimeslotRetention(now time.Time) {
	if s == nil || s.store == nil {
		return
	}
	days := timeslotRetentionDays()
	if days <= 0 {
		return
	}
	retention, ok := checkedDuration(days, 24*time.Hour)
	if !ok || retention <= 0 {
		return
	}
	cutoff := now.UTC().Add(-retention)
	endedPruned, err := s.store.DeleteCapacityReservationsEndedBefore(cutoff)
	if err != nil {
		slog.Warn("timeslot retention prune failed", "table", "capacity_reservations", "reason", "ended", "error", err)
	}
	rejectedPruned, err := s.store.DeleteRejectedCapacityReservationsBefore(cutoff)
	if err != nil {
		slog.Warn("timeslot retention prune failed", "table", "capacity_reservations", "reason", "rejected", "error", err)
	}
	if endedPruned <= 0 && rejectedPruned <= 0 {
		return
	}
	slog.Info("timeslot retention prune", "retention_days", days, "ended_pruned", endedPruned, "rejected_pruned", rejectedPruned)
	if err := s.store.SaveAuditEvent(&storage.AuditEvent{
		ID:           uuid.New().String(),
		Action:       "timeslot.retention_prune",
		ActorType:    "system",
		ActorSubject: "timeslot-retention",
		ResourceType: "capacity_reservations",
		Status:       "success",
		Details: map[string]any{
			"retention_days":  days,
			"ended_pruned":    endedPruned,
			"rejected_pruned": rejectedPruned,
			"cutoff":          cutoff.Format(time.RFC3339),
		},
		CreatedAt: now.UTC(),
	}); err != nil {
		slog.Warn("failed to persist timeslot retention audit event", "error", err)
	}
}

// startTimeslotRetentionCadence launches the leader-gated retention loop. It is a
// no-op when retention is disabled so single-node and default deployments incur
// no cost.
func (s *Server) startTimeslotRetentionCadence() {
	if s == nil || s.store == nil || s.timeslotRetentionCancel != nil {
		return
	}
	if timeslotRetentionDays() <= 0 {
		return
	}
	interval := timeslotRetentionInterval()
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.timeslotRetentionCancel = cancel
	s.launchBackground(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.isExecutionLeader() {
					s.pruneTimeslotRetention(time.Now().UTC())
				}
			}
		}
	})
}
