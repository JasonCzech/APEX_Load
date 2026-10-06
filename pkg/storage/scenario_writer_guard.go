package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ScenarioWriterGuard conditions one secondary writer of scenario-authored
// state (data-pool bindings, library placement, monitor selection, runtime
// apply-to-groups, delete) inside that writer's own transaction, after its
// project lock. A non-nil Expected is the caller's If-Match precondition: the
// scenario must still have that identity and content revision. A nil
// Expected is an unconditioned write, which the project's strict save policy
// refuses.
type ScenarioWriterGuard struct {
	Expected *ScenarioAuthoringPrecondition
}

// ScenarioGuardedWriterStore is implemented by stores with scenario content
// revisions. Every guarded writer returns the content revision its own
// transaction committed, so the response tag never includes another write.
type ScenarioGuardedWriterStore interface {
	UpdateScenarioDataPoolBindingsGuarded(scenarioID string, bindings []ScenarioDataPoolBinding, expectedUpdatedAt time.Time, guard ScenarioWriterGuard) (time.Time, string, bool, error)
	ReplaceScenarioLibraryAssignmentGuarded(id, projectID string, applicationID *string, releaseIDs []string, expectedRevision int64, guard ScenarioWriterGuard) (*LibraryAssignment, *LibraryAssignment, string, error)
	UpdateScenarioWithDataPoolBindingsGuarded(scenario *Scenario, scripts []ScenarioScript, bindings []ScenarioDataPoolBinding, expectedUpdatedAt time.Time, guard ScenarioWriterGuard) (bool, string, error)
	SaveMonitorBindingsGuarded(b MonitorBindings, expected int64, guard ScenarioWriterGuard) (*MonitorBindings, string, error)
	DeleteScenarioGuarded(id string, guard ScenarioWriterGuard) error
}

var _ ScenarioGuardedWriterStore = (*SQLiteStore)(nil)

// scenarioStrictSavePolicyTx reads the project's strict save policy inside
// the caller's transaction, after its project lock, so a policy change and an
// unconditioned write are ordered.
func scenarioStrictSavePolicyTx(tx *storeTx, project string) (bool, error) {
	var raw string
	if err := tx.QueryRow(`SELECT COALESCE(settings_json,'') FROM projects WHERE id=?`, project).Scan(&raw); err != nil {
		return false, err
	}
	var settings ProjectSettings
	if raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return false, err
		}
	}
	return settings.Authoring != nil && settings.Authoring.RequireScenarioSaveIfMatch, nil
}

func guardScenarioWriterTx(tx *storeTx, id string, guard *ScenarioWriterGuard) error {
	if guard == nil {
		return nil
	}
	query := `SELECT created_at,COALESCE(content_revision,'') FROM scenarios WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var created time.Time
	var revision string
	if err := tx.QueryRow(query, id).Scan(&created, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrScenarioAuthoringNotFound
		}
		return err
	}
	owner, err := scenarioAuthoringProjectIDTx(tx, id)
	if err != nil {
		return err
	}
	if guard.Expected == nil {
		strict, err := scenarioStrictSavePolicyTx(tx, owner)
		if err != nil {
			return err
		}
		if strict {
			return ErrScenarioAuthoringPreconditionRequired
		}
		return nil
	}
	if owner != normalizeProjectID(guard.Expected.ProjectID) || !created.Equal(guard.Expected.CreatedAt) {
		return ErrScenarioAuthoringNotFound
	}
	if !ValidScenarioContentRevision(revision) {
		return ErrScenarioAuthoringUnavailable
	}
	if revision != guard.Expected.ContentRevision {
		return ErrScenarioAuthoringConflict
	}
	return nil
}

// committedScenarioRevisionTx reads the revision the triggers assigned to this
// transaction's writes.
func committedScenarioRevisionTx(tx *storeTx, id string) (string, error) {
	var revision string
	if err := tx.QueryRow(`SELECT COALESCE(content_revision,'') FROM scenarios WHERE id=?`, id).Scan(&revision); err != nil {
		return "", err
	}
	if !ValidScenarioContentRevision(revision) {
		return "", ErrScenarioAuthoringUnavailable
	}
	return revision, nil
}

// commitScenarioWriterTx commits a guarded writer; a lost acknowledgement
// leaves its outcome unknown rather than failed.
func commitScenarioWriterTx(tx *storeTx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %v", ErrScenarioAuthoringOutcomeUnknown, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateScenarioDataPoolBindingsGuarded(scenarioID string, bindings []ScenarioDataPoolBinding, expectedUpdatedAt time.Time, guard ScenarioWriterGuard) (time.Time, string, bool, error) {
	return s.updateScenarioDataPoolBindings(scenarioID, bindings, expectedUpdatedAt, &guard)
}

func (s *SQLiteStore) ReplaceScenarioLibraryAssignmentGuarded(id, projectID string, applicationID *string, releaseIDs []string, expectedRevision int64, guard ScenarioWriterGuard) (*LibraryAssignment, *LibraryAssignment, string, error) {
	before, after, _, revision, err := s.replaceLibraryAssignment("scenario", id, projectID, applicationID, releaseIDs, expectedRevision, nil, &guard)
	return before, after, revision, err
}

func (s *SQLiteStore) UpdateScenarioWithDataPoolBindingsGuarded(scenario *Scenario, scripts []ScenarioScript, bindings []ScenarioDataPoolBinding, expectedUpdatedAt time.Time, guard ScenarioWriterGuard) (bool, string, error) {
	return s.updateScenarioWithDataPoolBindings(scenario, scripts, bindings, expectedUpdatedAt, &guard)
}

func (s *SQLiteStore) SaveMonitorBindingsGuarded(b MonitorBindings, expected int64, guard ScenarioWriterGuard) (*MonitorBindings, string, error) {
	return s.saveMonitorBindings(b, expected, &guard)
}

func (s *SQLiteStore) DeleteScenarioGuarded(id string, guard ScenarioWriterGuard) error {
	return s.deleteScenario(id, &guard)
}
