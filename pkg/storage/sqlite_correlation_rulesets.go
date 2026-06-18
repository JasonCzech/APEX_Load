package storage

// SQLite store — correlation rulesets. Rules are persisted inline as JSON. The
// same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) SaveCorrelationRuleset(ruleset *CorrelationRuleset) error {
	if ruleset == nil {
		return fmt.Errorf("correlation ruleset is required")
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
	_, err = s.db.Exec(`INSERT INTO correlation_rulesets (
			id, project_id, name, description, source, rules_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id, name=excluded.name, description=excluded.description,
			source=excluded.source, rules_json=excluded.rules_json, updated_at=excluded.updated_at`,
		ruleset.ID, ruleset.ProjectID, ruleset.Name, ruleset.Description, ruleset.Source,
		string(rulesJSON), ruleset.CreatedAt, ruleset.UpdatedAt)
	return err
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
