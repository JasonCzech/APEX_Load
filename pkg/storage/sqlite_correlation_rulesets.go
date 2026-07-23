package storage

// SQLite store — correlation rulesets. Rules are persisted inline as JSON. The
// same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// Correlation-ruleset writes are already constrained by the 256 KiB API
	// body limit. Enforce the same byte ceiling in storage so internal callers
	// and legacy rows cannot bypass it.
	maxCorrelationRulesetRulesJSONSize = 256 << 10
	// Detection can produce at most 4096 candidates per analysis. A larger
	// ruleset cannot be produced by the bounded engine and would make apply's
	// rule/suggestion matching path needlessly expensive.
	maxCorrelationRulesetRules           = 4096
	correlationRulesetProjectionVersion  = 1
	legacyCorrelationRulesetRepairPasses = 4
	// A fixed page turns the legacy scrub's PostgreSQL read path from two
	// round trips per row into one bounded query per page. Each payload is
	// independently capped at 256 KiB, so a page retains at most 16 MiB.
	legacyCorrelationRulesetRepairBatchRows = 64
)

func correlationRulesetJSONByteLengthSQL(dialect storeDialect, column string) string {
	if dialect == storeDialectPostgres {
		return fmt.Sprintf("OCTET_LENGTH(COALESCE(%s, ''))", column)
	}
	return fmt.Sprintf("LENGTH(CAST(COALESCE(%s, '') AS BLOB))", column)
}

func marshalCorrelationRulesetRules(rules []CorrelationRule) ([]byte, error) {
	if len(rules) > maxCorrelationRulesetRules {
		return nil, fmt.Errorf("correlation ruleset has %d rules; maximum is %d", len(rules), maxCorrelationRulesetRules)
	}
	// Bound caller-owned string material before json.Marshal allocates its
	// escaped output. The serialized form is never smaller than the combined
	// bytes of these values; structural bytes and escaping are checked against
	// the exact limit after marshal. This keeps an internal caller from forcing
	// an arbitrarily large temporary allocation only to be rejected afterward.
	remaining := maxCorrelationRulesetRulesJSONSize
	for _, rule := range rules {
		for _, value := range []string{rule.Name, rule.RuleID, rule.Source, rule.ExtractType, rule.Expression} {
			if len(value) > remaining {
				return nil, fmt.Errorf("correlation ruleset rules exceed %d bytes", maxCorrelationRulesetRulesJSONSize)
			}
			remaining -= len(value)
		}
	}
	payload, err := json.Marshal(rules)
	if err != nil {
		return nil, fmt.Errorf("marshal correlation ruleset rules: %w", err)
	}
	if len(payload) > maxCorrelationRulesetRulesJSONSize {
		return nil, fmt.Errorf("correlation ruleset rules exceed %d bytes", maxCorrelationRulesetRulesJSONSize)
	}
	return payload, nil
}

func correlationRulesetTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func nextCorrelationRulesetTimestamp(now, current time.Time) time.Time {
	next := correlationRulesetTimestamp(now)
	if !current.IsZero() && !next.After(current) {
		next = correlationRulesetTimestamp(current).Add(time.Microsecond)
	}
	return next
}

// decodeCorrelationRulesetRules streams the bounded rules array rather than
// unmarshalling directly into a caller-controlled-length slice. This matters
// for compact inputs such as thousands of empty objects, where a small JSON
// blob can otherwise trigger a much larger allocation.
func decodeCorrelationRulesetRules(payload string) ([]CorrelationRule, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "null" {
		return []CorrelationRule{}, nil
	}
	if len(payload) > maxCorrelationRulesetRulesJSONSize {
		return nil, fmt.Errorf("correlation ruleset rules exceed %d bytes", maxCorrelationRulesetRulesJSONSize)
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	open, ok := token.(json.Delim)
	if !ok || open != '[' {
		return nil, errors.New("correlation ruleset rules must be a JSON array")
	}
	rules := make([]CorrelationRule, 0, min(maxCorrelationRulesetRules, 64))
	for decoder.More() {
		if len(rules) >= maxCorrelationRulesetRules {
			return nil, fmt.Errorf("correlation ruleset has more than %d rules", maxCorrelationRulesetRules)
		}
		var rule CorrelationRule
		if err := decoder.Decode(&rule); err != nil {
			return nil, err
		}
		// json:"-" on SampleValue is the read-boundary guarantee for legacy
		// rows; keep this assignment explicit so a future tag change cannot
		// accidentally re-expose an already-decoded value.
		rule.SampleValue = ""
		rules = append(rules, rule)
	}
	token, err = decoder.Token()
	if err != nil {
		return nil, err
	}
	close, ok := token.(json.Delim)
	if !ok || close != ']' {
		return nil, errors.New("correlation ruleset rules must end with a JSON array delimiter")
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("correlation ruleset rules contain trailing JSON")
		}
		return nil, err
	}
	return rules, nil
}

// decodeLegacyCorrelationRulesetRules is migration-only. It recognizes the
// sample_value field that current CorrelationRule deliberately ignores, while
// returning the current disclosure-safe rule shape.
func decodeLegacyCorrelationRulesetRules(payload string) ([]CorrelationRule, bool, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "null" {
		return []CorrelationRule{}, false, nil
	}
	if len(payload) > maxCorrelationRulesetRulesJSONSize {
		return nil, false, fmt.Errorf("correlation ruleset rules exceed %d bytes", maxCorrelationRulesetRulesJSONSize)
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}
	open, ok := token.(json.Delim)
	if !ok || open != '[' {
		return nil, false, errors.New("correlation ruleset rules must be a JSON array")
	}
	rules := make([]CorrelationRule, 0, min(maxCorrelationRulesetRules, 64))
	hadSample := false
	for decoder.More() {
		if len(rules) >= maxCorrelationRulesetRules {
			return nil, false, fmt.Errorf("correlation ruleset has more than %d rules", maxCorrelationRulesetRules)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, false, err
		}
		var rule CorrelationRule
		if err := json.Unmarshal(raw, &rule); err != nil {
			return nil, false, err
		}
		var legacy struct {
			SampleValue json.RawMessage `json:"sample_value"`
		}
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return nil, false, err
		}
		hadSample = hadSample || len(legacy.SampleValue) > 0
		rule.SampleValue = ""
		rules = append(rules, rule)
	}
	token, err = decoder.Token()
	if err != nil {
		return nil, false, err
	}
	close, ok := token.(json.Delim)
	if !ok || close != ']' {
		return nil, false, errors.New("correlation ruleset rules must end with a JSON array delimiter")
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, false, errors.New("correlation ruleset rules contain trailing JSON")
		}
		return nil, false, err
	}
	return rules, hadSample, nil
}

type legacyCorrelationRulesetSnapshot struct {
	id                string
	payload           string
	updatedAtRaw      string
	projectionVersion int
	projectedAtRaw    sql.NullString
}

func (s *SQLiteStore) loadLegacyCorrelationRulesetRepairBatch(
	afterID string,
) ([]legacyCorrelationRulesetSnapshot, string, bool, error) {
	byteLengthSQL := correlationRulesetJSONByteLengthSQL(s.db.dialect, "rules_json")
	query := fmt.Sprintf(`SELECT id, COALESCE(rules_json, ''), CAST(updated_at AS TEXT),
			COALESCE(rules_projection_version, 0), CAST(rules_projected_at AS TEXT)
		FROM correlation_rulesets
		WHERE id > ?
			AND %s <= ?
			AND COALESCE(rules_projection_version, 0) <= ?
			AND (COALESCE(rules_projection_version, 0) < ?
				OR rules_projected_at IS NULL
				OR rules_projected_at != updated_at)
		ORDER BY id
		LIMIT ?`, byteLengthSQL)
	rows, err := s.db.Query(
		query,
		afterID,
		maxCorrelationRulesetRulesJSONSize,
		correlationRulesetProjectionVersion,
		correlationRulesetProjectionVersion,
		legacyCorrelationRulesetRepairBatchRows,
	)
	if err != nil {
		return nil, afterID, false, fmt.Errorf("list legacy correlation rulesets: %w", err)
	}
	snapshots := make([]legacyCorrelationRulesetSnapshot, 0, legacyCorrelationRulesetRepairBatchRows)
	for rows.Next() {
		var snapshot legacyCorrelationRulesetSnapshot
		if err := rows.Scan(
			&snapshot.id,
			&snapshot.payload,
			&snapshot.updatedAtRaw,
			&snapshot.projectionVersion,
			&snapshot.projectedAtRaw,
		); err != nil {
			rows.Close()
			return nil, afterID, false, fmt.Errorf("scan legacy correlation ruleset: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, afterID, false, fmt.Errorf("list legacy correlation rulesets: %w", err)
	}
	rows.Close()
	nextCursor := afterID
	if len(snapshots) > 0 {
		nextCursor = snapshots[len(snapshots)-1].id
	}
	return snapshots, nextCursor, len(snapshots) < legacyCorrelationRulesetRepairBatchRows, nil
}

// repairLegacyCorrelationRulesets removes learned sample literals written by
// older releases and clears rows that exceed the current bounded storage
// contract. The per-row projection witness is advanced by every current write.
// An older rolling-upgrade writer advances updated_at without the witness, so
// its row becomes eligible again. Rewrites preserve updated_at because
// SampleValue was never an editor-owned rule identity field; each update still
// compares the exact raw payload, timestamp, and witness so concurrent current-
// version writes win.
func (s *SQLiteStore) repairLegacyCorrelationRulesets() error {
	byteLengthSQL := correlationRulesetJSONByteLengthSQL(s.db.dialect, "rules_json")
	for pass := 0; pass < legacyCorrelationRulesetRepairPasses; pass++ {
		clearOversized := fmt.Sprintf(`UPDATE correlation_rulesets
			SET rules_json = '[]',
				rules_projection_version = ?,
				rules_projected_at = updated_at
			WHERE %s > ?
				AND COALESCE(rules_projection_version, 0) <= ?`, byteLengthSQL)
		if _, err := s.db.Exec(
			clearOversized,
			correlationRulesetProjectionVersion,
			maxCorrelationRulesetRulesJSONSize,
			correlationRulesetProjectionVersion,
		); err != nil {
			return fmt.Errorf("clear oversized legacy correlation rulesets: %w", err)
		}

		cursor := ""
		for {
			snapshots, nextCursor, done, err := s.loadLegacyCorrelationRulesetRepairBatch(cursor)
			if err != nil {
				return err
			}
			if !done && nextCursor == cursor {
				return fmt.Errorf("legacy correlation ruleset repair made no paging progress after %q", cursor)
			}
			for _, snapshot := range snapshots {
				rules, _, decodeErr := decodeLegacyCorrelationRulesetRules(snapshot.payload)
				if decodeErr != nil {
					// A malformed stale payload cannot safely retain an apparent
					// secret. Clear it rather than migrating a decoded prefix.
					rules = []CorrelationRule{}
				}
				safePayload, err := marshalCorrelationRulesetRules(rules)
				if err != nil {
					return fmt.Errorf("marshal repaired correlation ruleset %s: %w", snapshot.id, err)
				}
				if _, err := s.rewriteLegacyCorrelationRulesetIfUnchanged(snapshot, string(safePayload)); err != nil {
					return fmt.Errorf("repair legacy correlation ruleset %s: %w", snapshot.id, err)
				}
			}
			if done {
				break
			}
			cursor = nextCursor
		}
		stale, err := s.hasStaleCorrelationRulesetProjection()
		if err != nil {
			return err
		}
		if !stale {
			return nil
		}
	}
	return fmt.Errorf("legacy correlation ruleset repair did not converge after %d passes", legacyCorrelationRulesetRepairPasses)
}

func (s *SQLiteStore) hasStaleCorrelationRulesetProjection() (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM correlation_rulesets
		WHERE COALESCE(rules_projection_version, 0) <= ?
			AND (COALESCE(rules_projection_version, 0) < ?
				OR rules_projected_at IS NULL
				OR rules_projected_at != updated_at)
		ORDER BY id
		LIMIT 1`,
		correlationRulesetProjectionVersion,
		correlationRulesetProjectionVersion,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check legacy correlation ruleset projection convergence: %w", err)
	}
	return true, nil
}

func (s *SQLiteStore) rewriteLegacyCorrelationRulesetIfUnchanged(snapshot legacyCorrelationRulesetSnapshot, safePayload string) (bool, error) {
	query := `UPDATE correlation_rulesets
		SET rules_json = ?,
			rules_projection_version = ?,
			rules_projected_at = updated_at
		WHERE id = ?
			AND COALESCE(rules_json, '') = ?
			AND CAST(updated_at AS TEXT) = ?
			AND COALESCE(rules_projection_version, 0) = ?`
	args := []any{
		safePayload,
		correlationRulesetProjectionVersion,
		snapshot.id,
		snapshot.payload,
		snapshot.updatedAtRaw,
		snapshot.projectionVersion,
	}
	if snapshot.projectedAtRaw.Valid {
		query += ` AND CAST(rules_projected_at AS TEXT) = ?`
		args = append(args, snapshot.projectedAtRaw.String)
	} else {
		query += ` AND rules_projected_at IS NULL`
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

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
	now := correlationRulesetTimestamp(time.Now())
	if ruleset.CreatedAt.IsZero() {
		ruleset.CreatedAt = now
	} else {
		ruleset.CreatedAt = correlationRulesetTimestamp(ruleset.CreatedAt)
	}
	ruleset.UpdatedAt = now
	if ruleset.Rules == nil {
		ruleset.Rules = []CorrelationRule{}
	}
	rulesJSON, err := marshalCorrelationRulesetRules(ruleset.Rules)
	if err != nil {
		return err
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
	var currentUpdatedAt time.Time
	err = tx.QueryRow(`SELECT COALESCE(project_id, ''), updated_at FROM correlation_rulesets WHERE id = ?`, ruleset.ID).Scan(&currentProjectID, &currentUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(currentProjectID) != ruleset.ProjectID {
		return fmt.Errorf("correlation ruleset %q belongs to another project", ruleset.ID)
	}
	if err == nil {
		ruleset.UpdatedAt = nextCorrelationRulesetTimestamp(time.Now(), currentUpdatedAt)
	}
	result, err := tx.Exec(`INSERT INTO correlation_rulesets (
			id, project_id, name, description, source, rules_json,
			rules_projection_version, rules_projected_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description,
			source=excluded.source, rules_json=excluded.rules_json,
			rules_projection_version=excluded.rules_projection_version,
			rules_projected_at=excluded.rules_projected_at,
			updated_at=excluded.updated_at
		WHERE COALESCE(NULLIF(correlation_rulesets.project_id, ''), ?) = excluded.project_id`,
		ruleset.ID, ruleset.ProjectID, ruleset.Name, ruleset.Description, ruleset.Source,
		string(rulesJSON), correlationRulesetProjectionVersion, ruleset.UpdatedAt,
		ruleset.CreatedAt, ruleset.UpdatedAt, DefaultProjectID)
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
	if strings.TrimSpace(ruleset.Name) == "" {
		return false, errors.New("correlation ruleset name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected correlation ruleset updated_at is required")
	}
	if ruleset.Rules == nil {
		ruleset.Rules = []CorrelationRule{}
	}
	rulesJSON, err := marshalCorrelationRulesetRules(ruleset.Rules)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentProjectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	currentQuery := `SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT) FROM correlation_rulesets WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		currentQuery += ` FOR UPDATE`
	}
	if err := tx.QueryRow(currentQuery, ruleset.ID).Scan(&currentProjectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != normalizeProjectID(ruleset.ProjectID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	ruleset.ProjectID = normalizeProjectID(currentProjectID)
	ruleset.UpdatedAt = nextCorrelationRulesetTimestamp(time.Now(), currentUpdatedAt)
	result, err := tx.Exec(`UPDATE correlation_rulesets
		SET name = ?, description = ?, source = ?, rules_json = ?,
			rules_projection_version = ?,
			rules_projected_at = ?,
			updated_at = ?
		WHERE id = ?
			AND COALESCE(NULLIF(project_id, ''), ?) = ?
			AND CAST(updated_at AS TEXT) = ?`,
		ruleset.Name, ruleset.Description, ruleset.Source, string(rulesJSON),
		correlationRulesetProjectionVersion, ruleset.UpdatedAt, ruleset.UpdatedAt,
		ruleset.ID, DefaultProjectID, ruleset.ProjectID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func correlationRulesetSelect(dialect storeDialect) string {
	byteLengthSQL := correlationRulesetJSONByteLengthSQL(dialect, "rules_json")
	return fmt.Sprintf(`SELECT id, COALESCE(project_id, ''), name, COALESCE(description, ''),
		COALESCE(source, ''),
		CASE WHEN %s > %d THEN '' ELSE COALESCE(rules_json, '') END,
		CASE WHEN %s > %d THEN 1 ELSE 0 END,
		COALESCE(rules_projection_version, 0),
		CASE WHEN rules_projected_at IS NOT NULL
				AND rules_projected_at = updated_at
			THEN 1 ELSE 0 END,
		created_at, updated_at FROM correlation_rulesets`,
		byteLengthSQL, maxCorrelationRulesetRulesJSONSize,
		byteLengthSQL, maxCorrelationRulesetRulesJSONSize)
}

func scanCorrelationRuleset(row capacityRowScanner) (*CorrelationRuleset, error) {
	var ruleset CorrelationRuleset
	var rulesJSON string
	var rulesJSONOversized int
	var projectionVersion int
	var projectionCurrent int
	if err := row.Scan(&ruleset.ID, &ruleset.ProjectID, &ruleset.Name, &ruleset.Description,
		&ruleset.Source, &rulesJSON, &rulesJSONOversized, &projectionVersion, &projectionCurrent,
		&ruleset.CreatedAt, &ruleset.UpdatedAt); err != nil {
		return nil, err
	}
	if projectionVersion != correlationRulesetProjectionVersion {
		return nil, fmt.Errorf(
			"correlation ruleset %q rules projection version %d is unsupported",
			ruleset.ID,
			projectionVersion,
		)
	}
	if projectionCurrent == 0 {
		return nil, fmt.Errorf("correlation ruleset %q rules projection is stale", ruleset.ID)
	}
	if rulesJSONOversized != 0 {
		return nil, fmt.Errorf("correlation ruleset %q rules exceed %d bytes", ruleset.ID, maxCorrelationRulesetRulesJSONSize)
	}
	rules, err := decodeCorrelationRulesetRules(rulesJSON)
	if err != nil {
		return nil, fmt.Errorf("decode correlation ruleset %q rules: %w", ruleset.ID, err)
	}
	ruleset.Rules = rules
	return &ruleset, nil
}

func (s *SQLiteStore) GetCorrelationRuleset(id string) (*CorrelationRuleset, error) {
	return scanCorrelationRuleset(s.db.QueryRow(correlationRulesetSelect(s.db.dialect)+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListCorrelationRulesets(projectID string, limit int, offset int) ([]*CorrelationRuleset, error) {
	query := correlationRulesetSelect(s.db.dialect)
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
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("correlation ruleset id is required")
	}
	var projectID string
	var updatedAt time.Time
	err := s.db.QueryRow(`SELECT COALESCE(project_id, ''), updated_at
		FROM correlation_rulesets WHERE id = ?`, id).Scan(&projectID, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	deleted, err := s.DeleteCorrelationRulesetIfUnchanged(id, projectID, updatedAt)
	if err != nil {
		return err
	}
	if !deleted {
		return fmt.Errorf("correlation ruleset %q changed while deletion was in flight", id)
	}
	return nil
}

// DeleteCorrelationRulesetIfUnchanged removes only the authorized durable row
// version. The row lock prevents a same-ID replacement in another project from
// slipping between authorization and deletion on PostgreSQL; SQLite's
// single-connection transaction provides the equivalent serialization.
func (s *SQLiteStore) DeleteCorrelationRulesetIfUnchanged(id, expectedProjectID string, expectedUpdatedAt time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("correlation ruleset id is required")
	}
	expectedProjectID = normalizeProjectID(expectedProjectID)
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected correlation ruleset updated_at is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	query := `SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT)
		FROM correlation_rulesets WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var projectID string
	var updatedAt time.Time
	var updatedAtRaw string
	if err := tx.QueryRow(query, id).Scan(&projectID, &updatedAt, &updatedAtRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(projectID) != expectedProjectID || !updatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	result, err := tx.Exec(`DELETE FROM correlation_rulesets
		WHERE id = ?
			AND COALESCE(NULLIF(project_id, ''), ?) = ?
			AND CAST(updated_at AS TEXT) = ?`,
		id, DefaultProjectID, expectedProjectID, updatedAtRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}
