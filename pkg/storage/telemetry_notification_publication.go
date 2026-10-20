package storage

import (
	"context"
	"errors"
	"time"
)

// TelemetryNotificationPublication is the bounded decision made from the exact
// verified manifest supplied to archive completion. Event and Decision are
// mutually exclusive. Rejected projection details must not contain source data.
type TelemetryNotificationPublication struct {
	Event    *TelemetryNotification
	Decision string
}

// TelemetryNotificationPublicationState retains one bounded publication summary
// per run plus cumulative suppression. Immutable admitted outcomes retain their
// own per-destination rows. It is not a history of rejected payloads.
type TelemetryNotificationPublicationState struct {
	ManifestSHA256         string           `json:"manifest_sha256"`
	EventID                string           `json:"event_id,omitempty"`
	Decision               string           `json:"decision"`
	SuppressedPublications int64            `json:"suppressed_publications"`
	UpdatedAt              time.Time        `json:"updated_at"`
	Deliveries             map[string]int64 `json:"deliveries"`
}

func (s *SQLiteStore) CompleteArchiveFinalizationWithTelemetry(testID, intentToken, refreshToken string, record *RunArchiveRecord, identity ArchiveFinalizationRunIdentity, publication TelemetryNotificationPublication) (bool, error) {
	if record == nil || !telemetryNotificationDigest(record.ManifestSHA256) {
		return false, errors.New("notification publication requires a manifest digest")
	}
	if publication.Event != nil {
		if publication.Decision != "" {
			return false, errors.New("ambiguous notification publication")
		}
		event, _, _, err := canonicalTelemetryNotification(*publication.Event)
		if err != nil {
			return false, err
		}
		if event.TestID != testID || event.ProjectID != normalizeProjectID(identity.ProjectID) || event.RunIncarnation != identity.RunIncarnation || !archiveIdentityTimestampEqual(s.db.dialect, event.CompletedAt, identity.EndTime) {
			return false, errors.New("notification does not match archive incarnation")
		}
		publication.Event = &event
	} else {
		switch publication.Decision {
		case "not_applicable", "projection_rejected", "identity_unavailable":
		default:
			return false, errors.New("invalid notification publication decision")
		}
	}
	return s.completeArchiveFinalizationForRunWithNotification(testID, intentToken, refreshToken, record, identity, &publication)
}

func (s *SQLiteStore) commitTelemetryNotificationPublicationTx(tx *storeTx, testID, projectID, manifestSHA256 string, publication TelemetryNotificationPublication) error {
	decision, id := publication.Decision, ""
	now := time.Now().UTC()
	if publication.Event != nil {
		event, eventID, raw, err := canonicalTelemetryNotification(*publication.Event)
		if err != nil {
			return err
		}
		id, decision, err = s.enqueueTelemetryNotificationTx(tx, event, eventID, raw, now)
		if errors.Is(err, errTelemetryNotificationCapacity) {
			decision = "capacity_exhausted"
			id = ""
		} else if err != nil {
			return err
		}
		if decision == "fenced" {
			return errors.New("notification incarnation changed before archive completion")
		}
		if decision == "enqueued" || decision == "replayed" {
			decision = "enqueued"
			if len(event.Destinations) == 0 {
				decision = "no_recipients"
			}
		}
		if decision == "legacy_claim" {
			id = ""
		}
	}
	suppressed := 0
	if decision == "capacity_exhausted" || decision == "projection_rejected" || decision == "identity_unavailable" {
		suppressed = 1
	}
	_, err := tx.Exec(`INSERT INTO telemetry_notification_publications(test_id,project_id,manifest_sha256,event_id,decision,suppressed_publications,updated_at)
 VALUES(?,?,?,?,?,?,?) ON CONFLICT(test_id) DO UPDATE SET
 project_id=excluded.project_id,manifest_sha256=excluded.manifest_sha256,event_id=excluded.event_id,decision=excluded.decision,
 suppressed_publications=telemetry_notification_publications.suppressed_publications+excluded.suppressed_publications,updated_at=excluded.updated_at`,
		testID, projectID, manifestSHA256, id, decision, suppressed, now)
	return err
}

// GetTelemetryNotificationPublication is internal storage, not an authorization
// check. Its caller must authorize the project and run before exposing metadata.
func (s *SQLiteStore) GetTelemetryNotificationPublication(ctx context.Context, projectID, testID string) (*TelemetryNotificationPublicationState, error) {
	out := &TelemetryNotificationPublicationState{Deliveries: map[string]int64{}}
	err := s.db.QueryRowContext(ctx, `SELECT manifest_sha256,event_id,decision,suppressed_publications,updated_at FROM telemetry_notification_publications WHERE test_id=? AND project_id=?`, testID, projectID).Scan(&out.ManifestSHA256, &out.EventID, &out.Decision, &out.SuppressedPublications, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.state,COUNT(*) FROM telemetry_notification_deliveries d JOIN telemetry_notification_events e ON e.id=d.event_id WHERE e.test_id=? AND e.project_id=? AND e.format_version=1 GROUP BY d.state`, testID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		out.Deliveries[state] = count
	}
	return out, rows.Err()
}
