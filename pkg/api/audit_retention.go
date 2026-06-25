package api

// audit_retention.go — bounded growth for the auth audit log and usage-accounting
// snapshots. Both tables otherwise grow forever (every mutating action writes a row),
// degrading the hot ListAuditEvents path and inflating the database indefinitely.
//
// Retention is opt-in: with APEXLOAD_AUDIT_RETENTION_DAYS unset or <= 0 nothing is
// pruned (audit history is preserved by default). When configured, a single
// leader-gated cadence prunes rows older than the cutoff so HA replicas never
// double-prune. A meta audit event records each prune for traceability.

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

func auditRetentionDays() int {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_AUDIT_RETENTION_DAYS"))
	if raw == "" {
		return 0
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 0 {
		return 0
	}
	return days
}

func auditRetentionInterval() time.Duration {
	return firstConfiguredDuration(6*time.Hour, "APEXLOAD_AUDIT_RETENTION_INTERVAL", "APEXLOAD_AUDIT_RETENTION_INTERVAL_SECONDS")
}

// pruneAuditRetention deletes audit events and usage snapshots older than the
// configured retention window. It is a no-op when retention is disabled.
func (s *Server) pruneAuditRetention(now time.Time) {
	if s == nil || s.store == nil {
		return
	}
	days := auditRetentionDays()
	if days <= 0 {
		return
	}
	cutoff := now.UTC().Add(-time.Duration(days) * 24 * time.Hour)
	auditPruned, err := s.store.DeleteAuditEventsBefore(cutoff)
	if err != nil {
		slog.Warn("audit retention prune failed", "table", "audit_events", "error", err)
	}
	usagePruned, err := s.store.DeleteUsageAccountingSnapshotsBefore(cutoff)
	if err != nil {
		slog.Warn("audit retention prune failed", "table", "usage_accounting_snapshots", "error", err)
	}
	if auditPruned <= 0 && usagePruned <= 0 {
		return
	}
	slog.Info("audit retention prune", "retention_days", days, "audit_events_pruned", auditPruned, "usage_snapshots_pruned", usagePruned)
	_ = s.store.SaveAuditEvent(&storage.AuditEvent{
		ID:           uuid.New().String(),
		Action:       "auth.audit.retention_prune",
		ActorType:    "system",
		ActorSubject: "audit-retention",
		ResourceType: "audit_events",
		Status:       "success",
		Details: map[string]any{
			"retention_days":         days,
			"audit_events_pruned":    auditPruned,
			"usage_snapshots_pruned": usagePruned,
			"cutoff":                 cutoff.Format(time.RFC3339),
		},
		CreatedAt: now.UTC(),
	})
}

// startAuditRetentionCadence launches the leader-gated retention loop. It is a no-op
// when retention is disabled so single-node and default deployments incur no cost.
func (s *Server) startAuditRetentionCadence() {
	if s == nil || s.store == nil || s.auditRetentionCancel != nil {
		return
	}
	if auditRetentionDays() <= 0 {
		return
	}
	interval := auditRetentionInterval()
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.auditRetentionCancel = cancel
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.isExecutionLeader() {
					s.pruneAuditRetention(time.Now().UTC())
				}
			}
		}
	}()
}
