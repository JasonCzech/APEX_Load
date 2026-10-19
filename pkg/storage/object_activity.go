package storage

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/scrub"
	"strings"
	"time"
)

// ObjectActivityEvent is deliberately independent of raw administrative Details.
// It contains only bounded identity snapshots and names of changed sections.
type ObjectActivityEvent struct {
	ID               string    `json:"id"`
	Action           string    `json:"action"`
	ActorType        string    `json:"actor_type"`
	ActorSubject     string    `json:"actor_subject"`
	ActorDisplayName string    `json:"actor_display_name,omitempty"`
	InitiatedBy      string    `json:"initiated_by,omitempty"`
	ProjectID        string    `json:"project_id"`
	ResourceType     string    `json:"resource_type"`
	ResourceID       string    `json:"resource_id"`
	ResourceName     string    `json:"resource_name"`
	Status           string    `json:"status"`
	ChangedFields    []string  `json:"changed_fields,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	Deleted          bool      `json:"deleted,omitempty"`
}
type ObjectActivityFilter struct {
	ProjectID, ResourceType, ResourceID, ActorSubject, Action, Search string
	ResourceTypes, ResourceIDs                                        []string
	After, Before                                                     *time.Time
	Limit, Offset                                                     int
}
type ObjectActivityStore interface {
	SaveObjectActivity(*ObjectActivityEvent) error
	ListObjectActivity(ObjectActivityFilter) ([]*ObjectActivityEvent, error)
	ObjectActivitySummaries(projectID, resourceType string, ids []string) (map[string]ObjectActivitySummary, error)
	SetObjectArchived(kind, id string, expected time.Time, archived bool) (bool, error)
}
type ObjectActivitySummary struct {
	Creator      *ObjectActivityEvent `json:"creator,omitempty"`
	LastModifier *ObjectActivityEvent `json:"last_modifier,omitempty"`
	LastArchive  *ObjectActivityEvent `json:"last_archive,omitempty"`
}

func (s *SQLiteStore) SaveObjectActivity(event *ObjectActivityEvent) error {
	if event == nil {
		return fmt.Errorf("activity event is required")
	}
	copy := *event
	copy.ProjectID = normalizeProjectID(copy.ProjectID)
	copy.CreatedAt = copy.CreatedAt.UTC()
	copy.ResourceName = scrub.ValueLabel(copy.ResourceName, scrub.Policy{})
	if len(copy.ResourceName) > 256 {
		copy.ResourceName = string([]rune(copy.ResourceName)[:min(256, len([]rune(copy.ResourceName)))])
	}
	if copy.ActorType == "" {
		copy.ActorType = "unknown"
	}
	if copy.ActorSubject == "" {
		copy.ActorDisplayName = "Unknown"
	}
	event = &copy
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO object_activity_events(id,project_id,resource_type,resource_id,resource_name,action,actor_subject,created_at,status,payload) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, event.ID, normalizeProjectID(event.ProjectID), event.ResourceType, event.ResourceID, event.ResourceName, event.Action, event.ActorSubject, event.CreatedAt, event.Status, string(payload))
	return err
}
func objectActivityWhere(f ObjectActivityFilter) (string, []any) {
	q := ` WHERE project_id=?`
	args := []any{normalizeProjectID(f.ProjectID)}
	for _, v := range []struct{ k, v string }{{"resource_type", f.ResourceType}, {"resource_id", f.ResourceID}, {"actor_subject", f.ActorSubject}, {"action", f.Action}} {
		if v.v != "" {
			q += ` AND ` + v.k + `=?`
			args = append(args, v.v)
		}
	}
	for _, v := range []struct {
		k      string
		values []string
	}{{"resource_type", f.ResourceTypes}, {"resource_id", f.ResourceIDs}} {
		if v.values != nil {
			if len(v.values) == 0 {
				q += ` AND 1=0`
				continue
			}
			q += ` AND ` + v.k + ` IN (` + strings.TrimSuffix(strings.Repeat("?,", len(v.values)), ",") + `)`
			for _, value := range v.values {
				args = append(args, value)
			}
		}
	}
	if f.Search != "" {
		q += ` AND LOWER(resource_name) LIKE ? ESCAPE '\'`
		search := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(f.Search))
		args = append(args, "%"+search+"%")
	}
	if f.After != nil {
		q += ` AND created_at>=?`
		args = append(args, f.After.UTC())
	}
	if f.Before != nil {
		q += ` AND created_at<=?`
		args = append(args, f.Before.UTC())
	}
	return q, args
}
func (s *SQLiteStore) ListObjectActivity(f ObjectActivityFilter) ([]*ObjectActivityEvent, error) {
	where, args := objectActivityWhere(f)
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 251 {
		f.Limit = 251
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(`SELECT payload,COALESCE((SELECT action FROM object_activity_events latest WHERE latest.project_id=object_activity_events.project_id AND latest.resource_type=object_activity_events.resource_type AND latest.resource_id=object_activity_events.resource_id AND latest.status='success' AND latest.action IN ('create','delete') ORDER BY `+s.db.dialect.newestFirst("latest.")+` LIMIT 1),'') FROM object_activity_events`+where+` ORDER BY `+s.db.dialect.newestFirst("")+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []*ObjectActivityEvent{}
	for rows.Next() {
		var payload, latestAction string
		if err = rows.Scan(&payload, &latestAction); err != nil {
			return nil, err
		}
		var e ObjectActivityEvent
		if err = json.Unmarshal([]byte(payload), &e); err != nil {
			return nil, err
		}
		e.ProjectID = normalizeProjectID(e.ProjectID)
		e.Deleted = latestAction == "delete"
		events = append(events, &e)
	}
	return events, rows.Err()
}

// Window queries return at most three rows per object, regardless of history
// size. Only activity since the object's latest successful create counts, so a
// reused ID does not inherit its previous incarnation's history, ties included.
// A row that ties with that create but was inserted before it, such as, on
// SQLite, a copy an earlier release's backfill wrote newest first or, on
// PostgreSQL, a row from before insert_seq tied with a create made after it,
// now counts as older than the create and drops out. Between two PostgreSQL rows from before
// insert_seq the ID decides, as it does in the list.
func (s *SQLiteStore) ObjectActivitySummaries(projectID, kind string, ids []string) (map[string]ObjectActivitySummary, error) {
	result := map[string]ObjectActivitySummary{}
	if len(ids) == 0 {
		return result, nil
	}
	for start := 0; start < len(ids); start += 200 {
		end := start + 200
		if end > len(ids) {
			end = len(ids)
		}
		where, args := objectActivityWhere(ObjectActivityFilter{ProjectID: projectID, ResourceType: kind, ResourceIDs: ids[start:end]})
		rows, err := s.db.Query(`SELECT payload,category FROM (SELECT payload,resource_id,CASE WHEN action='create' THEN 'creator' WHEN action IN ('archive','restore') THEN 'archive' ELSE 'modifier' END AS category,ROW_NUMBER() OVER(PARTITION BY resource_id,CASE WHEN action='create' THEN 'creator' WHEN action IN ('archive','restore') THEN 'archive' ELSE 'modifier' END ORDER BY `+s.db.dialect.newestFirst("")+`) AS rank FROM object_activity_events`+where+` AND status IN ('success','accepted','pending','started') AND action IN ('create','modify','move','archive','restore','start','stop','abort','pause','resume') AND NOT EXISTS (SELECT 1 FROM object_activity_events c WHERE c.project_id=object_activity_events.project_id AND c.resource_type=object_activity_events.resource_type AND c.resource_id=object_activity_events.resource_id AND c.action='create' AND c.status='success' AND `+s.db.dialect.newerThan("c.", "object_activity_events.")+`) ) ranked WHERE rank=1`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var payload, category string
			if err = rows.Scan(&payload, &category); err != nil {
				rows.Close()
				return nil, err
			}
			var e ObjectActivityEvent
			if err = json.Unmarshal([]byte(payload), &e); err != nil {
				rows.Close()
				return nil, fmt.Errorf("decode activity: %w", err)
			}
			e.ProjectID = normalizeProjectID(e.ProjectID)
			v := result[e.ResourceID]
			switch category {
			case "creator":
				v.Creator = &e
			case "archive":
				v.LastArchive = &e
			default:
				v.LastModifier = &e
			}
			result[e.ResourceID] = v
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
