package storage

import "time"

// RunNotificationClaimStore records a run's once-only notifications, so a
// repeated archive attach (an adjunct refresh, a recovered publication, a new
// leader) never announces the same run again.
type RunNotificationClaimStore interface {
	// ClaimRunNotification reports true for the first claim of a run's
	// notification kind, and false once it was claimed or when the run no
	// longer exists (a deletion that raced the announcement).
	ClaimRunNotification(testID, kind string, now time.Time) (bool, error)
}

// RunNotificationClaimPrefix scopes a run's notification claims in
// app_settings; run and project deletion remove them.
func RunNotificationClaimPrefix(testID string) string {
	return "run:notifications:" + testID + ":"
}

func (s *SQLiteStore) ClaimRunNotification(testID, kind string, now time.Time) (bool, error) {
	// The claim is written only while its run exists, so a deletion racing
	// the announcement leaves no claim behind and nothing is sent. SQLite
	// serializes writers; PostgreSQL's READ COMMITTED would let the check
	// read a run row a deletion has locked but not yet removed, so there it
	// takes a share lock and waits for the deletion to finish.
	exists := `SELECT 1 FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		exists += ` FOR SHARE`
	}
	result, err := s.db.Exec(`INSERT INTO app_settings(key,value,updated_at) SELECT ?,?,? WHERE EXISTS (`+exists+`) ON CONFLICT(key) DO NOTHING`, RunNotificationClaimPrefix(testID)+kind, now.UTC().Format(time.RFC3339Nano), now.UTC(), testID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
