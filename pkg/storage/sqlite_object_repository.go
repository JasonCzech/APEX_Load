package storage

// SQLite store — object repositories. Objects are persisted inline as JSON. The
// same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *SQLiteStore) SaveObjectRepository(repo *ObjectRepository) error {
	if repo == nil {
		return fmt.Errorf("object repository is required")
	}
	if strings.TrimSpace(repo.ID) == "" {
		return errors.New("object repository id is required")
	}
	if strings.TrimSpace(repo.Name) == "" {
		return errors.New("object repository name is required")
	}
	repo.ProjectID = normalizeProjectID(repo.ProjectID)
	now := time.Now()
	if repo.CreatedAt.IsZero() {
		repo.CreatedAt = now
	}
	repo.UpdatedAt = now
	if repo.Objects == nil {
		repo.Objects = []RepositoryObject{}
	}
	objectsJSON, err := json.Marshal(repo.Objects)
	if err != nil {
		return fmt.Errorf("marshal object repository objects: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if repo.ProjectID, err = lockProjectForOwnedMutation(tx, repo.ProjectID); err != nil {
		return err
	}
	var currentProjectID string
	err = tx.QueryRow(`SELECT COALESCE(project_id, '') FROM object_repositories WHERE id = ?`, repo.ID).Scan(&currentProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(currentProjectID) != repo.ProjectID {
		return fmt.Errorf("object repository %q belongs to another project", repo.ID)
	}
	result, err := tx.Exec(`INSERT INTO object_repositories (
			id, project_id, name, description, source, objects_json, auto_promote_on_heal, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description,
			source=excluded.source, objects_json=excluded.objects_json,
			auto_promote_on_heal=excluded.auto_promote_on_heal, updated_at=excluded.updated_at
		WHERE COALESCE(NULLIF(object_repositories.project_id, ''), ?) = excluded.project_id`,
		repo.ID, repo.ProjectID, repo.Name, repo.Description, repo.Source, string(objectsJSON),
		repo.AutoPromoteOnHeal, repo.CreatedAt, repo.UpdatedAt, DefaultProjectID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("object repository %q belongs to another project", repo.ID)
	}
	return tx.Commit()
}

// UpdateObjectRepositoryIfUnchanged replaces editor-owned repository fields
// only when the durable updated_at value still matches the caller's snapshot.
// ProjectID is an immutable authorization boundary and is never rewritten.
func (s *SQLiteStore) UpdateObjectRepositoryIfUnchanged(repo *ObjectRepository, expectedUpdatedAt time.Time) (bool, error) {
	return s.UpdateObjectRepositoryIfUnchangedContext(context.Background(), repo, expectedUpdatedAt)
}

type objectRepositoryExecutionLeaseTerm struct {
	name       string
	ownerID    string
	generation uint64
}

// UpdateObjectRepositoryIfUnchangedContext is the cancelable form used by
// execution-epoch-bound replay completion hooks. Beginning the transaction
// with ctx keeps cancellation attached through Commit, so a controller that
// loses leadership cannot finish a delayed self-heal write afterward.
func (s *SQLiteStore) UpdateObjectRepositoryIfUnchangedContext(ctx context.Context, repo *ObjectRepository, expectedUpdatedAt time.Time) (bool, error) {
	return s.updateObjectRepositoryIfUnchangedContext(ctx, repo, expectedUpdatedAt, nil)
}

// UpdateObjectRepositoryIfUnchangedContextAndLeaseTermHeld combines the object
// repository revision CAS with an exact, unexpired execution-lease term check
// in the same transaction. Replay completion work from an old HA leader cannot
// commit after another controller replaces or expires its term even when the
// old process-local context has not yet observed that loss.
func (s *SQLiteStore) UpdateObjectRepositoryIfUnchangedContextAndLeaseTermHeld(
	ctx context.Context,
	repo *ObjectRepository,
	expectedUpdatedAt time.Time,
	leaseName string,
	leaseOwnerID string,
	leaseGeneration uint64,
) (bool, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" || leaseGeneration == 0 {
		return false, errors.New("lease name, owner token, and positive generation are required")
	}
	return s.updateObjectRepositoryIfUnchangedContext(ctx, repo, expectedUpdatedAt, &objectRepositoryExecutionLeaseTerm{
		name:       leaseName,
		ownerID:    leaseOwnerID,
		generation: leaseGeneration,
	})
}

func (s *SQLiteStore) updateObjectRepositoryIfUnchangedContext(
	ctx context.Context,
	repo *ObjectRepository,
	expectedUpdatedAt time.Time,
	leaseTerm *objectRepositoryExecutionLeaseTerm,
) (bool, error) {
	if ctx == nil {
		return false, errors.New("object repository update context is required")
	}
	if repo == nil || strings.TrimSpace(repo.ID) == "" {
		return false, errors.New("object repository id is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected object repository updated_at is required")
	}
	if repo.Objects == nil {
		repo.Objects = []RepositoryObject{}
	}
	objectsJSON, err := json.Marshal(repo.Objects)
	if err != nil {
		return false, fmt.Errorf("marshal object repository objects: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if leaseTerm != nil {
		held, err := schedulerLeaseTermHeldInTransaction(
			tx,
			s.db.dialect,
			leaseTerm.name,
			leaseTerm.ownerID,
			leaseTerm.generation,
		)
		if err != nil || !held {
			return false, err
		}
	}
	var currentProjectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT) FROM object_repositories WHERE id = ?`, repo.ID).Scan(&currentProjectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != normalizeProjectID(repo.ProjectID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	repo.ProjectID = normalizeProjectID(currentProjectID)
	repo.UpdatedAt = time.Now()
	result, err := tx.ExecContext(ctx, `UPDATE object_repositories
		SET name = ?, description = ?, source = ?, objects_json = ?, auto_promote_on_heal = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, repo.Name, repo.Description, repo.Source, string(objectsJSON), repo.AutoPromoteOnHeal, repo.UpdatedAt, repo.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

const objectRepositorySelect = `SELECT id, COALESCE(project_id, ''), name, COALESCE(description, ''),
	COALESCE(source, ''), COALESCE(objects_json, ''), COALESCE(auto_promote_on_heal, 0), created_at, updated_at FROM object_repositories`

func scanObjectRepository(row capacityRowScanner) (*ObjectRepository, error) {
	var repo ObjectRepository
	var objectsJSON string
	if err := row.Scan(&repo.ID, &repo.ProjectID, &repo.Name, &repo.Description, &repo.Source,
		&objectsJSON, &repo.AutoPromoteOnHeal, &repo.CreatedAt, &repo.UpdatedAt); err != nil {
		return nil, err
	}
	if objectsJSON != "" && objectsJSON != "null" {
		if err := json.Unmarshal([]byte(objectsJSON), &repo.Objects); err != nil {
			return nil, fmt.Errorf("unmarshal object repository objects: %w", err)
		}
	}
	if repo.Objects == nil {
		repo.Objects = []RepositoryObject{}
	}
	return &repo, nil
}

func (s *SQLiteStore) GetObjectRepository(id string) (*ObjectRepository, error) {
	return scanObjectRepository(s.db.QueryRow(objectRepositorySelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListObjectRepositories(projectID string, limit int, offset int) ([]*ObjectRepository, error) {
	query := objectRepositorySelect
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	repos := make([]*ObjectRepository, 0)
	for rows.Next() {
		repo, err := scanObjectRepository(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (s *SQLiteStore) DeleteObjectRepository(id string) error {
	deleted, err := s.DeleteObjectRepositoryIfUnreferencedAndUnchanged(id, time.Time{})
	if err != nil {
		return err
	}
	if !deleted {
		if _, loadErr := s.GetObjectRepository(id); errors.Is(loadErr, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("object repository %q is referenced by one or more scripts", id)
	}
	return nil
}

// DeleteObjectRepositoryIfUnreferencedAndUnchanged prevents repository removal
// from leaving qualified object_ref bindings dangling. The repository row lock
// pairs with script-write validation, so a concurrent adopter either commits
// before this reference scan (and blocks deletion) or observes the deletion and
// fails its own write.
func (s *SQLiteStore) DeleteObjectRepositoryIfUnreferencedAndUnchanged(id string, expectedUpdatedAt time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("object repository id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	query := `SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT) FROM object_repositories WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var projectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(query, id).Scan(&projectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !expectedUpdatedAt.IsZero() && !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	rows, err := tx.Query(`SELECT COALESCE(init, ''), COALESCE(actions, ''), COALESCE(teardown, '')
		FROM scripts WHERE COALESCE(NULLIF(project_id, ''), ?) = ?`, DefaultProjectID, normalizeProjectID(projectID))
	if err != nil {
		return false, err
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	referenced := false
	for rows.Next() {
		var initJSON, actionsJSON, teardownJSON string
		if err := rows.Scan(&initJSON, &actionsJSON, &teardownJSON); err != nil {
			rows.Close()
			return false, err
		}
		for _, raw := range []string{initJSON, actionsJSON, teardownJSON} {
			found, err := actionJSONReferencesObjectRepository(raw, id)
			if err != nil {
				rows.Close()
				return false, err
			}
			if found {
				referenced = true
				break
			}
		}
		if referenced {
			break
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if referenced {
		return false, nil
	}
	result, err := tx.Exec(`DELETE FROM object_repositories WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, id, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func actionJSONReferencesObjectRepository(raw string, repositoryID string) (bool, error) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return false, nil
	}
	var actions []dsl.Action
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		return false, fmt.Errorf("decode script actions while checking object repository references: %w", err)
	}
	var references func([]dsl.Action) bool
	references = func(actions []dsl.Action) bool {
		for i := range actions {
			prefix, _, qualified := strings.Cut(strings.TrimSpace(actions[i].ObjectRef), ":")
			if qualified && strings.HasPrefix(strings.TrimSpace(prefix), "object-repo-") && strings.TrimSpace(prefix) == repositoryID {
				return true
			}
			if actions[i].Control != nil {
				for _, branch := range actions[i].Control.ActionBranches() {
					if references(branch.Actions) {
						return true
					}
				}
			}
		}
		return false
	}
	return references(actions), nil
}
