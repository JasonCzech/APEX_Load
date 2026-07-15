package storage

// SQLite store — correlation rulesets. Rules are persisted inline as JSON. The
// same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveCorrelationRuleset(ruleset *CorrelationRuleset) error {
	if ruleset == nil {
		return fmt.Errorf("correlation ruleset is required")
	}
	if strings.TrimSpace(ruleset.ID) == "" {
		return errors.New("correlation ruleset id is required")
	}
	if strings.TrimSpace(ruleset.Name) == "" {
		return errors.New("correlation ruleset name is required")
	}
	ruleset.ProjectID = normalizeProjectID(ruleset.ProjectID)
	now := time.Now()
	if ruleset.CreatedAt.IsZero() {
		ruleset.CreatedAt = now
	}
	ruleset.UpdatedAt = now
	if ruleset.Rules == nil {
		ruleset.Rules = []CorrelationRule{}
	}
	rulesJSON, err := json.Marshal(ruleset.Rules)
	if err != nil {
		return fmt.Errorf("marshal correlation ruleset rules: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if ruleset.ProjectID, err = lockProjectForOwnedMutation(tx, ruleset.ProjectID); err != nil {
		return err
	}
	var currentProjectID string
	err = tx.QueryRow(`SELECT COALESCE(project_id, '') FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(&currentProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(currentProjectID) != ruleset.ProjectID {
		return fmt.Errorf("correlation ruleset %q belongs to another project", ruleset.ID)
	}
	result, err := tx.Exec(`INSERT INTO correlation_rulesets (
			id, project_id, name, description, source, rules_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description,
			source=excluded.source, rules_json=excluded.rules_json, updated_at=excluded.updated_at
		WHERE COALESCE(NULLIF(correlation_rulesets.project_id, ''), ?) = excluded.project_id`,
		ruleset.ID, ruleset.ProjectID, ruleset.Name, ruleset.Description, ruleset.Source,
		string(rulesJSON), ruleset.CreatedAt, ruleset.UpdatedAt, DefaultProjectID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("correlation ruleset %q belongs to another project", ruleset.ID)
	}
	return tx.Commit()
}

// UpdateCorrelationRulesetIfUnchanged replaces editor-owned ruleset fields
// only when the durable updated_at value still matches the caller's snapshot.
// ProjectID is an immutable authorization boundary and is never rewritten.
func (s *SQLiteStore) UpdateCorrelationRulesetIfUnchanged(ruleset *CorrelationRuleset, expectedUpdatedAt time.Time) (bool, error) {
	if ruleset == nil || strings.TrimSpace(ruleset.ID) == "" {
		return false, errors.New("correlation ruleset id is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected correlation ruleset updated_at is required")
	}
	if ruleset.Rules == nil {
		ruleset.Rules = []CorrelationRule{}
	}
	rulesJSON, err := json.Marshal(ruleset.Rules)
	if err != nil {
		return false, fmt.Errorf("marshal correlation ruleset rules: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentProjectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT) FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(&currentProjectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != normalizeProjectID(ruleset.ProjectID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	ruleset.ProjectID = normalizeProjectID(currentProjectID)
	ruleset.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE correlation_rulesets
		SET name = ?, description = ?, source = ?, rules_json = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, ruleset.Name, ruleset.Description, ruleset.Source, string(rulesJSON), ruleset.UpdatedAt, ruleset.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

const correlationRulesetSelect = `SELECT id, COALESCE(project_id, ''), name, COALESCE(description, ''),
	COALESCE(source, ''), COALESCE(rules_json, ''), created_at, updated_at FROM correlation_rulesets`

func scanCorrelationRuleset(row capacityRowScanner) (*CorrelationRuleset, error) {
	var ruleset CorrelationRuleset
	var rulesJSON string
	if err := row.Scan(&ruleset.ID, &ruleset.ProjectID, &ruleset.Name, &ruleset.Description,
		&ruleset.Source, &rulesJSON, &ruleset.CreatedAt, &ruleset.UpdatedAt); err != nil {
		return nil, err
	}
	if rulesJSON != "" && rulesJSON != "null" {
		if err := json.Unmarshal([]byte(rulesJSON), &ruleset.Rules); err != nil {
			return nil, fmt.Errorf("unmarshal correlation ruleset rules: %w", err)
		}
	}
	if ruleset.Rules == nil {
		ruleset.Rules = []CorrelationRule{}
	}
	return &ruleset, nil
}

func (s *SQLiteStore) GetCorrelationRuleset(id string) (*CorrelationRuleset, error) {
	return scanCorrelationRuleset(s.db.QueryRow(correlationRulesetSelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListCorrelationRulesets(projectID string, limit int, offset int) ([]*CorrelationRuleset, error) {
	query := correlationRulesetSelect
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
	rulesets := make([]*CorrelationRuleset, 0)
	for rows.Next() {
		ruleset, err := scanCorrelationRuleset(rows)
		if err != nil {
			return nil, err
		}
		rulesets = append(rulesets, ruleset)
	}
	return rulesets, rows.Err()
}

func (s *SQLiteStore) DeleteCorrelationRuleset(id string) error {
	_, err := s.db.Exec(`DELETE FROM correlation_rulesets WHERE id = ?`, id)
	return err
}
