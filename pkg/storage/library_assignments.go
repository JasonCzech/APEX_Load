package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrLibraryAssignmentConflict = errors.New("library assignment changed; reload before applying changes")
var ErrLibraryAssignmentInvalid = errors.New("invalid library assignment")

type LibraryAssignmentApplication struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type LibraryAssignmentRelease struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	ApplicationID string     `json:"application_id"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
}
type LibraryAssignment struct {
	Application *LibraryAssignmentApplication `json:"application"`
	Releases    []LibraryAssignmentRelease    `json:"releases"`
	Revision    int64                         `json:"revision"`
}

// LibraryAssignmentStore keeps catalog placement independent of executable content.
type LibraryAssignmentStore interface {
	ListLibraryAssignments(kind string, ids []string) (map[string]*LibraryAssignment, error)
	ReplaceLibraryAssignment(kind, id, projectID string, applicationID *string, releaseIDs []string, expectedRevision int64) (*LibraryAssignment, *LibraryAssignment, error)
}

func libraryAssignmentTables(kind string) (string, string, error) {
	switch kind {
	case "script":
		return "script_library_assignments", "scripts", nil
	case "scenario":
		return "scenario_library_assignments", "scenarios", nil
	default:
		return "", "", fmt.Errorf("%w: unsupported item kind", ErrLibraryAssignmentInvalid)
	}
}
func emptyLibraryAssignment() *LibraryAssignment {
	return &LibraryAssignment{Releases: []LibraryAssignmentRelease{}}
}

type libraryAssignmentReader interface {
	Query(string, ...any) (*sql.Rows, error)
}

func readLibraryAssignments(db libraryAssignmentReader, table string, ids []string) (map[string]*LibraryAssignment, error) {
	result := make(map[string]*LibraryAssignment, len(ids))
	for _, id := range ids {
		result[id] = emptyLibraryAssignment()
	}
	for start := 0; start < len(ids); start += 400 {
		end := min(start+400, len(ids))
		args := make([]any, end-start)
		for i, id := range ids[start:end] {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
		rows, err := db.Query(`SELECT a.item_id, a.revision, p.id, p.name, r.id, r.name, r.application_id, r.archived_at
   FROM `+table+` a LEFT JOIN applications p ON p.id = a.application_id
   LEFT JOIN `+table+`_releases ar ON ar.item_id = a.item_id
   LEFT JOIN releases r ON r.id = ar.release_id
   WHERE a.item_id IN (`+placeholders+`) ORDER BY a.item_id, r.name, r.id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var revision int64
			var appID, appName, releaseID, releaseName, releaseApp sql.NullString
			var archived sql.NullTime
			if err := rows.Scan(&id, &revision, &appID, &appName, &releaseID, &releaseName, &releaseApp, &archived); err != nil {
				rows.Close()
				return nil, err
			}
			assignment := result[id]
			assignment.Revision = revision
			if appID.Valid {
				assignment.Application = &LibraryAssignmentApplication{ID: appID.String, Name: appName.String}
			}
			if releaseID.Valid {
				release := LibraryAssignmentRelease{ID: releaseID.String, Name: releaseName.String, ApplicationID: releaseApp.String}
				if archived.Valid {
					t := archived.Time
					release.ArchivedAt = &t
				}
				assignment.Releases = append(assignment.Releases, release)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *SQLiteStore) ListLibraryAssignments(kind string, ids []string) (map[string]*LibraryAssignment, error) {
	table, _, err := libraryAssignmentTables(kind)
	if err != nil {
		return nil, err
	}
	return readLibraryAssignments(s.db, table, ids)
}

// ReplaceLibraryAssignment serializes with hierarchy deletion and content writes,
// and fences stale editors independently from content revisions.
func (s *SQLiteStore) ReplaceLibraryAssignment(kind, id, projectID string, applicationID *string, releaseIDs []string, expectedRevision int64) (*LibraryAssignment, *LibraryAssignment, error) {
	table, items, err := libraryAssignmentTables(kind)
	if err != nil {
		return nil, nil, err
	}
	invalid := func(message string) (*LibraryAssignment, *LibraryAssignment, error) {
		return nil, nil, fmt.Errorf("%w: %s", ErrLibraryAssignmentInvalid, message)
	}
	if expectedRevision < 0 || len(releaseIDs) > 1000 {
		return invalid("invalid revision or too many releases")
	}
	if applicationID == nil && len(releaseIDs) != 0 {
		return invalid("releases require an application")
	}
	if applicationID != nil && strings.TrimSpace(*applicationID) == "" {
		return invalid("application must be an ID or null")
	}
	projectID = normalizeProjectID(projectID)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	lock := func(query, id string) error {
		result, err := tx.Exec(query, id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return sql.ErrNoRows
		}
		return nil
	}
	if err := lock(`UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalid("project not found")
		}
		return nil, nil, err
	}
	if applicationID != nil {
		if err := lock(`UPDATE applications SET updated_at = updated_at WHERE id = ?`, *applicationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalid("application not found")
			}
			return nil, nil, err
		}
		var appProject string
		if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, *applicationID).Scan(&appProject); err != nil {
			return nil, nil, err
		}
		if normalizeProjectID(appProject) != projectID {
			return invalid("application belongs to another project")
		}
		if _, err := tx.Exec(`UPDATE releases SET updated_at = updated_at WHERE application_id = ?`, *applicationID); err != nil {
			return nil, nil, err
		}
	}
	if err := lock(`UPDATE `+items+` SET updated_at = updated_at WHERE id = ?`, id); err != nil {
		return nil, nil, err
	}
	var owner string
	if kind == "script" {
		err = tx.QueryRow(`SELECT COALESCE(project_id, '') FROM scripts WHERE id = ?`, id).Scan(&owner)
	} else {
		owner, _, err = storedScenarioProjectIDTx(tx, id)
	}
	if err != nil {
		return nil, nil, err
	}
	if normalizeProjectID(owner) != projectID {
		return invalid("item belongs to another project")
	}
	previous, err := readLibraryAssignments(tx, table, []string{id})
	if err != nil {
		return nil, nil, err
	}
	before := previous[id]
	if before.Revision != expectedRevision {
		return nil, nil, ErrLibraryAssignmentConflict
	}
	retained := map[string]bool{}
	for _, release := range before.Releases {
		retained[release.ID] = true
	}
	canonical := map[string]bool{}
	for _, releaseID := range releaseIDs {
		// Physical IDs are preferred; a project release family resolves only to this application's member.
		var resolved string
		var archived sql.NullTime
		if err := tx.QueryRow(`SELECT id, archived_at FROM releases WHERE application_id = ? AND (id = ? OR COALESCE(NULLIF(group_id, ''), id) = ?) ORDER BY id`, *applicationID, releaseID, releaseID).Scan(&resolved, &archived); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalid("release does not belong to the selected application")
			}
			return nil, nil, err
		}
		if archived.Valid && !retained[resolved] {
			return invalid("archived releases cannot be newly assigned")
		}
		canonical[resolved] = true
	}
	if _, err := tx.Exec(`INSERT INTO `+table+` (item_id, application_id, revision) VALUES (?, ?, 0) ON CONFLICT(item_id) DO NOTHING`, id, applicationID); err != nil {
		return nil, nil, err
	}
	updated, err := tx.Exec(`UPDATE `+table+` SET application_id = ?, revision = revision + 1 WHERE item_id = ? AND revision = ?`, applicationID, id, expectedRevision)
	if err != nil {
		return nil, nil, err
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return nil, nil, err
	}
	if count != 1 {
		return nil, nil, ErrLibraryAssignmentConflict
	}
	if _, err := tx.Exec(`DELETE FROM `+table+`_releases WHERE item_id = ?`, id); err != nil {
		return nil, nil, err
	}
	ordered := make([]string, 0, len(canonical))
	for releaseID := range canonical {
		ordered = append(ordered, releaseID)
	}
	sort.Strings(ordered)
	for _, releaseID := range ordered {
		if _, err := tx.Exec(`INSERT INTO `+table+`_releases (item_id, release_id) VALUES (?, ?)`, id, releaseID); err != nil {
			return nil, nil, err
		}
	}
	after, err := readLibraryAssignments(tx, table, []string{id})
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return before, after[id], nil
}
