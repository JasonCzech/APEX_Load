package storage

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/scrub"
	"strings"
)

// A one-time, bounded migration retains only stable legacy resource identifiers
// and safe names. No raw audit Details enter the reader-facing activity table.
func (s *SQLiteStore) backfillObjectActivity() error {
	var done int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM object_activity_migrations WHERE id='legacy_v1'`).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	// Oldest first, the exact reverse of ListAuditEvents: each copy is inserted
	// after the copies of the audit events it follows, so the copies list newest
	// first exactly as audit history does, ties included. Copies made by
	// releases before schema 65 are not reordered. They were inserted newest
	// first, so on SQLite their ties now list, and decide a tied Deleted flag,
	// in the reverse of the order those releases showed; neither order reflects
	// how the audit events were written. Offset paging assumes no older audit
	// row is deleted meanwhile; it runs once, when a database from before object
	// activity is first opened, and audit retention on another controller during
	// that copy could make it skip rows.
	for offset := 0; ; offset += 250 {
		events, err := s.listAuditEvents(AuditEventFilter{Limit: 250, Offset: offset}, s.db.dialect.oldestFirst(""))
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.ResourceType != "script" && event.ResourceType != "scenario" && event.ResourceType != "test" && event.ResourceType != "external_result_import" {
				continue
			}
			parts := strings.Split(event.Action, ".")
			action := parts[len(parts)-1]
			switch action {
			case "create", "delete", "move", "archive", "restore", "start", "stop", "abort", "pause", "resume":
			case "update", "replace", "adjust", "scale", "apply", "pin", "unpin":
				action = "modify"
			default:
				continue
			}
			if strings.HasPrefix(event.Action, "test.notes.") || strings.HasPrefix(event.Action, "run.annotation.") {
				action = "modify"
			}
			// Legacy GitHub/session import events sometimes used a script name as ID.
			// Those are ambiguous and intentionally do not invent object attribution.
			if strings.HasPrefix(event.Action, "github.script.import") || strings.Contains(event.Action, ".inspect") || strings.Contains(event.Action, ".authoring_session.") {
				continue
			}
			name, _ := event.Details["name"].(string)
			name = scrub.ValueLabel(name, scrub.Policy{})
			if len(name) > 256 {
				name = name[:256]
			}
			changed := []string{}
			if action == "modify" {
				changed = []string{"content_or_settings"}
				if strings.HasPrefix(event.Action, "run.annotation.") {
					changed = []string{"annotations"}
				}
			}
			if err = s.SaveObjectActivity(&ObjectActivityEvent{ID: "legacy:" + event.ID, Action: action, ActorType: event.ActorType, ActorSubject: event.ActorSubject, ActorDisplayName: event.ActorDisplayName, ProjectID: event.ProjectID, ResourceType: event.ResourceType, ResourceID: event.ResourceID, ResourceName: name, Status: event.Status, ChangedFields: changed, CreatedAt: event.CreatedAt, Deleted: action == "delete" && event.Status == "success"}); err != nil {
				return fmt.Errorf("backfill object activity: %w", err)
			}
		}
		if len(events) < 250 {
			break
		}
	}
	_, err := s.db.Exec(`INSERT INTO object_activity_migrations(id) VALUES('legacy_v1') ON CONFLICT(id) DO NOTHING`)
	return err
}
