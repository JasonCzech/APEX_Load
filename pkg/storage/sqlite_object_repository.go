package storage

// SQLite store — object repositories. Objects are persisted inline as JSON. The
// same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) SaveObjectRepository(repo *ObjectRepository) error {
	if repo == nil {
		return fmt.Errorf("object repository is required")
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
	_, err = s.db.Exec(`INSERT INTO object_repositories (
			id, project_id, name, description, source, objects_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id, name=excluded.name, description=excluded.description,
			source=excluded.source, objects_json=excluded.objects_json, updated_at=excluded.updated_at`,
		repo.ID, repo.ProjectID, repo.Name, repo.Description, repo.Source, string(objectsJSON),
		repo.CreatedAt, repo.UpdatedAt)
	return err
}

const objectRepositorySelect = `SELECT id, COALESCE(project_id, ''), name, COALESCE(description, ''),
	COALESCE(source, ''), COALESCE(objects_json, ''), created_at, updated_at FROM object_repositories`

func scanObjectRepository(row capacityRowScanner) (*ObjectRepository, error) {
	var repo ObjectRepository
	var objectsJSON string
	if err := row.Scan(&repo.ID, &repo.ProjectID, &repo.Name, &repo.Description, &repo.Source,
		&objectsJSON, &repo.CreatedAt, &repo.UpdatedAt); err != nil {
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
	_, err := s.db.Exec(`DELETE FROM object_repositories WHERE id = ?`, id)
	return err
}
