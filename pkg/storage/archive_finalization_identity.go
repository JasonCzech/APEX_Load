package storage

import (
	"errors"
	"time"
)

// ArchiveFinalizationRunIdentity is the terminal run inspected before archive
// I/O. Generator archive creation time can differ from this controller start time.
type ArchiveFinalizationRunIdentity struct {
	ProjectID  string
	StartTime  time.Time
	EndTime    time.Time
	Status     string
	LeaseName  string
	LeaseOwner string
	// RequireAdjunctRefresh conservatively reconciles skipped enrichment after
	// recovering an already uploaded immutable version with no local source.
	RequireAdjunctRefresh bool
}

func (s *SQLiteStore) CompleteArchiveFinalizationForRun(testID, intentToken, refreshToken string, record *RunArchiveRecord, identity ArchiveFinalizationRunIdentity) (bool, error) {
	if record == nil || identity.StartTime.IsZero() || identity.EndTime.IsZero() ||
		(identity.Status != "COMPLETE" && identity.Status != "FAILED" && identity.Status != "ABORTED") ||
		(identity.LeaseName == "") != (identity.LeaseOwner == "") {
		return false, errors.New("invalid archive finalization run identity")
	}
	end := identity.EndTime
	recordEnd := record.CompletedAt
	if s.db.dialect == storeDialectPostgres {
		end = end.Truncate(time.Microsecond)
		recordEnd = recordEnd.Truncate(time.Microsecond)
	}
	if !end.Equal(recordEnd) {
		return false, errors.New("archive completion time differs from terminal identity")
	}
	identity.ProjectID = normalizeProjectID(identity.ProjectID)
	return s.completeArchiveFinalization(testID, intentToken, refreshToken, record, &identity)
}

// SQLite preserves the original timestamp's textual offset. Compare instants
// after reading the locked row so equivalent offsets do not fail the SQL fence.
func archiveIdentityTimestampEqual(dialect storeDialect, a, b time.Time) bool {
	if dialect == storeDialectPostgres {
		a, b = a.Truncate(time.Microsecond), b.Truncate(time.Microsecond)
	}
	return a.Equal(b)
}
