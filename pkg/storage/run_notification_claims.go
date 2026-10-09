package storage

import "time"

// RunNotificationClaimStore records a run's once-only notifications, so a
// repeated archive attach (an adjunct refresh, a recovered publication, a new
// leader) never announces the same run again.
type RunNotificationClaimStore interface {
	// ClaimRunNotification reports true for the first claim of a run's
	// notification kind and false once it was claimed.
	ClaimRunNotification(testID, kind string, now time.Time) (bool, error)
}

// RunNotificationClaimPrefix scopes a run's notification claims in
// app_settings; run and project deletion remove them.
func RunNotificationClaimPrefix(testID string) string {
	return "run:notifications:" + testID + ":"
}

func (s *SQLiteStore) ClaimRunNotification(testID, kind string, now time.Time) (bool, error) {
	result, err := s.db.Exec(`INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, RunNotificationClaimPrefix(testID)+kind, now.UTC().Format(time.RFC3339Nano), now.UTC())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
