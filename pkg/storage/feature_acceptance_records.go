package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Feature acceptance results. A support claim follows the latest record for
// its requirement/feature/profile/provider/platform; revoked withdraws it.
// "Latest" is the database-assigned sequence, never a node clock.
const (
	AcceptancePassed  = "passed"
	AcceptanceFailed  = "failed"
	AcceptancePending = "pending"
	AcceptanceRevoked = "revoked"
)

type FeatureAcceptanceArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// FeatureAcceptanceRecord is one append-only qualification result: which
// commit and binaries ran which suite, with hashes of the sanitized evidence.
type FeatureAcceptanceRecord struct {
	ID string `json:"id"`
	// Seq orders records by insertion. Records written before it existed
	// have 0 and fall back to recorded_at.
	Seq           int64                       `json:"seq"`
	RequirementID string                      `json:"requirement_id"`
	Feature       string                      `json:"feature"`
	Profile       string                      `json:"profile,omitempty"`
	Provider      string                      `json:"provider,omitempty"`
	Platform      string                      `json:"platform,omitempty"`
	CommitSHA     string                      `json:"commit_sha"`
	BinarySHA256  string                      `json:"binary_sha256,omitempty"`
	Suite         string                      `json:"suite"`
	Result        string                      `json:"result"`
	Artifacts     []FeatureAcceptanceArtifact `json:"artifacts"`
	Notes         string                      `json:"notes,omitempty"`
	RecordedAt    time.Time                   `json:"recorded_at"`
	RecordedBy    string                      `json:"recorded_by"`
}

type FeatureAcceptanceStore interface {
	AppendFeatureAcceptanceRecord(record *FeatureAcceptanceRecord) error
	ListFeatureAcceptanceRecords(requirementID, feature string, limit, offset int) ([]*FeatureAcceptanceRecord, error)
	// LatestFeatureAcceptanceRecords returns the newest record of every
	// subject, at most limit subjects, and whether more existed.
	LatestFeatureAcceptanceRecords(requirementID, feature string, limit int) ([]*FeatureAcceptanceRecord, bool, error)
}

var _ FeatureAcceptanceStore = (*SQLiteStore)(nil)

func (s *SQLiteStore) AppendFeatureAcceptanceRecord(record *FeatureAcceptanceRecord) error {
	if record == nil || strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.RequirementID) == "" || strings.TrimSpace(record.Feature) == "" ||
		strings.TrimSpace(record.CommitSHA) == "" || strings.TrimSpace(record.Suite) == "" || strings.TrimSpace(record.RecordedBy) == "" {
		return errors.New("invalid feature acceptance record")
	}
	switch record.Result {
	case AcceptancePassed, AcceptanceFailed, AcceptancePending, AcceptanceRevoked:
	default:
		return errors.New("invalid feature acceptance result")
	}
	artifacts := record.Artifacts
	if artifacts == nil {
		artifacts = []FeatureAcceptanceArtifact{}
	}
	body, err := json.Marshal(artifacts)
	if err != nil {
		return err
	}
	// One statement takes the next sequence: SQLite serializes it, and a
	// concurrent PostgreSQL insert that took the same value retries.
	for attempt := 0; ; attempt++ {
		var seq int64
		err = s.db.QueryRow(`INSERT INTO feature_acceptance_records (id, requirement_id, feature, profile, provider, platform, commit_sha, binary_sha256, suite, result, artifacts, notes, recorded_at, recorded_by, seq)
			SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(MAX(seq), 0) + 1 FROM feature_acceptance_records RETURNING seq`,
			record.ID, record.RequirementID, record.Feature, record.Profile, record.Provider, record.Platform, record.CommitSHA, record.BinarySHA256,
			record.Suite, record.Result, string(body), record.Notes, record.RecordedAt.UTC(), record.RecordedBy).Scan(&seq)
		if err == nil {
			record.Seq, record.Artifacts = seq, artifacts
			return nil
		}
		if !isUniqueConstraintErr(err) || attempt >= 8 {
			return err
		}
	}
}

// ListFeatureAcceptanceRecords returns records newest first, optionally for
// one requirement and/or feature.
func (s *SQLiteStore) ListFeatureAcceptanceRecords(requirementID, feature string, limit, offset int) ([]*FeatureAcceptanceRecord, error) {
	if limit < 1 || limit > 500 || offset < 0 {
		return nil, errors.New("invalid feature acceptance page")
	}
	query := `SELECT ` + featureAcceptanceColumns + ` FROM feature_acceptance_records WHERE 1 = 1`
	args := []any{}
	if requirementID != "" {
		query += ` AND requirement_id = ?`
		args = append(args, requirementID)
	}
	if feature != "" {
		query += ` AND feature = ?`
		args = append(args, feature)
	}
	query += ` ORDER BY ` + featureAcceptanceNewestFirst + ` LIMIT ? OFFSET ?`
	rows, err := s.db.Query(query, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeatureAcceptanceRecords(rows)
}

const featureAcceptanceColumns = `id, requirement_id, feature, profile, provider, platform, commit_sha, binary_sha256, suite, result, artifacts, notes, recorded_at, recorded_by, seq`

// featureAcceptanceNewestFirst orders by insertion; records from before the
// sequence existed (seq 0) sort after every sequenced one, by time.
const featureAcceptanceNewestFirst = `seq DESC, recorded_at DESC, id DESC`

func scanFeatureAcceptanceRecords(rows *sql.Rows) ([]*FeatureAcceptanceRecord, error) {
	records := []*FeatureAcceptanceRecord{}
	for rows.Next() {
		var record FeatureAcceptanceRecord
		var artifacts string
		if err := rows.Scan(&record.ID, &record.RequirementID, &record.Feature, &record.Profile, &record.Provider, &record.Platform, &record.CommitSHA,
			&record.BinarySHA256, &record.Suite, &record.Result, &artifacts, &record.Notes, &record.RecordedAt, &record.RecordedBy, &record.Seq); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(artifacts), &record.Artifacts); err != nil {
			return nil, err
		}
		if record.Artifacts == nil {
			record.Artifacts = []FeatureAcceptanceArtifact{}
		}
		records = append(records, &record)
	}
	return records, rows.Err()
}

// LatestFeatureAcceptanceRecords picks each subject's newest record in SQL,
// so a status read is one bounded statement however many records exist.
func (s *SQLiteStore) LatestFeatureAcceptanceRecords(requirementID, feature string, limit int) ([]*FeatureAcceptanceRecord, bool, error) {
	if limit < 1 || limit > 10000 {
		return nil, false, errors.New("invalid feature acceptance subject limit")
	}
	filter := ``
	args := []any{}
	if requirementID != "" {
		filter += ` AND requirement_id = ?`
		args = append(args, requirementID)
	}
	if feature != "" {
		filter += ` AND feature = ?`
		args = append(args, feature)
	}
	rows, err := s.db.Query(`SELECT `+featureAcceptanceColumns+` FROM (
			SELECT `+featureAcceptanceColumns+`, ROW_NUMBER() OVER (PARTITION BY requirement_id, feature, profile, provider, platform ORDER BY `+featureAcceptanceNewestFirst+`) AS subject_rank
			FROM feature_acceptance_records WHERE 1 = 1`+filter+`
		) ranked WHERE subject_rank = 1 ORDER BY requirement_id, feature, profile, provider, platform LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	records, err := scanFeatureAcceptanceRecords(rows)
	if err != nil {
		return nil, false, err
	}
	if len(records) > limit {
		return records[:limit], true, nil
	}
	return records, false, nil
}
