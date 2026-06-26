package api

// access_review_notifier.go — a leader-gated background nudge for the access-review
// posture. It periodically rebuilds the read-only access review (stale/unbound/disabled
// users, system-admin grants, overdue-rotation / expiring / expired / broad-scope service
// accounts, and recent auth denials) and, when the flagged set changes, broadcasts a
// content-free WebSocket ping so an admin viewing the dashboard refetches the (admin-gated)
// report. The ping carries no data — just a type — so it is safe to fan out to every
// connected client; the detail only ever flows through the authorized endpoint.
//
// It is opt-out: set APEXLOAD_ACCESS_REVIEW_NOTIFY_INTERVAL to a non-positive duration
// (e.g. "0") to disable. The cadence runs only on the HA execution leader so replicas > 1
// never double-notify.

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// accessReviewChangedMessage is the content-free ping. broadcast_all bypasses the hub's
// test/project filters so it reaches project-scoped dashboard sockets too; type is the
// only meaningful field a client reads.
type accessReviewChangedMessage struct {
	Type         string `json:"type"`
	BroadcastAll bool   `json:"broadcast_all"`
}

func accessReviewNotifyInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_ACCESS_REVIEW_NOTIFY_INTERVAL"))
	if raw == "" {
		return time.Hour
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return time.Hour
	}
	// A non-positive interval disables the cadence (handled by the caller).
	return parsed
}

// accessReviewSignature is a stable fingerprint of the flagged set + denial count,
// independent of the report's generation timestamp. The notifier only pings when this
// changes, so a steady-state posture produces no traffic.
func accessReviewSignature(review AccessReviewResponse) string {
	parts := make([]string, 0, len(review.Users)+len(review.ServiceAccounts)+1)
	for _, user := range review.Users {
		parts = append(parts, "u:"+user.ID+"="+strings.Join(user.Flags, ","))
	}
	for _, account := range review.ServiceAccounts {
		parts = append(parts, "s:"+account.ID+"="+strings.Join(account.Flags, ","))
	}
	sort.Strings(parts)
	parts = append(parts, "d:"+strconv.Itoa(review.RecentDenials))
	return strings.Join(parts, "|")
}

// accessReviewActionable reports whether there is anything an admin should look at.
func accessReviewActionable(review AccessReviewResponse) bool {
	return len(review.Users) > 0 || len(review.ServiceAccounts) > 0 || review.RecentDenials > 0
}

// notifyAccessReviewChange computes the current review and, when it is actionable and its
// signature differs from prev, broadcasts the ping. It returns the new signature so the
// caller can track change across ticks (empty when nothing was computed).
func (s *Server) notifyAccessReviewChange(now time.Time, prev string) string {
	if s == nil || s.store == nil {
		return prev
	}
	review, err := s.computeAccessReview(now)
	if err != nil {
		slog.Warn("access-review notifier compute failed", "error", err)
		return prev
	}
	if !accessReviewActionable(review) {
		// Nothing to flag — reset so re-appearance later pings again.
		return ""
	}
	sig := accessReviewSignature(review)
	if sig == prev {
		return sig
	}
	if s.hub != nil {
		s.hub.BroadcastJSON(accessReviewChangedMessage{Type: "access_review_changed", BroadcastAll: true})
	}
	return sig
}

// startAccessReviewNotifierCadence launches the leader-gated notifier loop. Disabled when
// the configured interval is non-positive.
func (s *Server) startAccessReviewNotifierCadence() {
	if s == nil || s.store == nil || s.accessReviewNotifyCancel != nil {
		return
	}
	interval := accessReviewNotifyInterval()
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.accessReviewNotifyCancel = cancel
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastSignature := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.isExecutionLeader() {
					lastSignature = s.notifyAccessReviewChange(time.Now().UTC(), lastSignature)
				}
			}
		}
	}()
}
