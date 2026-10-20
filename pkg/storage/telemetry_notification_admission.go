package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AuthorizeTelemetryNotificationDelivery rechecks the exact lease and terminal
// incarnation immediately before an external send. A request already admitted
// to a receiver cannot be recalled by a later deletion or lease replacement.
func (s *SQLiteStore) AuthorizeTelemetryNotificationDelivery(ctx context.Context, claim TelemetryNotificationDelivery, now time.Time) (bool, error) {
	if now.IsZero() || claim.OwnerToken == "" || claim.Attempts < 1 || claim.Attempts > MaxTelemetryNotificationAttempts {
		return false, errors.New("invalid telemetry delivery admission")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	held, err := s.lockTelemetryNotificationEvent(tx, claim.EventID)
	if err != nil {
		return false, err
	}
	if !held {
		return false, retireFencedTelemetryNotification(tx, claim.EventID)
	}
	var owner, state string
	var attempts int
	var expiry time.Time
	err = tx.QueryRow(`SELECT owner_token,state,attempts,next_attempt FROM telemetry_notification_deliveries WHERE event_id=? AND destination=?`, claim.EventID, claim.Destination).Scan(&owner, &state, &attempts, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state != "leased" || owner != claim.OwnerToken || attempts != claim.Attempts || !expiry.After(now) {
		return false, nil
	}
	return true, tx.Commit()
}
