package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// BrowserAcceptanceQualification pins the measurement cohort independently of
// the intelligence estimator. One acceptance record claims one metric cohort.
// Existing records without this contract remain historical evidence only for
// the purpose of promoting browser intelligence.
type BrowserAcceptanceQualification struct {
	Version              int                           `json:"version"`
	RuntimeSHA256        string                        `json:"runtime_sha256"`
	CapabilitySHA256     string                        `json:"capability_sha256"`
	Metric               string                        `json:"metric"`
	MeasurementAlgorithm string                        `json:"measurement_algorithm"`
	AnalysisAlgorithm    string                        `json:"analysis_algorithm,omitempty"`
	Dependencies         []BrowserAcceptanceDependency `json:"dependencies,omitempty"`
}

type BrowserAcceptanceDependency struct {
	RecordID     string `json:"record_id"`
	RecordSHA256 string `json:"record_sha256"`
}

type BrowserAcceptanceDependencyStore interface {
	// Append must check dependencies and insert the BI pass in one writer-fenced transaction.
	AppendBrowserIntelligenceAcceptanceRecord(*FeatureAcceptanceRecord) error
	CheckBrowserAcceptanceDependencies(*FeatureAcceptanceRecord) error
}

var ErrBrowserAcceptanceDependency = errors.New("browser evidence dependency is unavailable")
var browserAcceptanceHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var browserAcceptanceSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
var browserAcceptanceCommit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

func IsBrowserIntelligenceAcceptance(record *FeatureAcceptanceRecord) bool {
	return record != nil && strings.HasPrefix(record.RequirementID, "BI-")
}

func browserDependencyError(reason string) error {
	return fmt.Errorf("%w: %s", ErrBrowserAcceptanceDependency, reason)
}

func validateBrowserAcceptanceQualification(record *FeatureAcceptanceRecord) error {
	if record == nil {
		return browserDependencyError("record missing")
	}
	q := record.Qualification
	promotion := IsBrowserIntelligenceAcceptance(record) && record.Result == AcceptancePassed
	if q == nil {
		if promotion {
			return browserDependencyError("a versioned measurement cohort and accepted BE-4 evidence are required")
		}
		return nil
	}
	if q.Version != 1 || !browserAcceptanceHash.MatchString(q.RuntimeSHA256) || !browserAcceptanceHash.MatchString(q.CapabilitySHA256) || !browserAcceptanceSlug.MatchString(q.Metric) || !browserAcceptanceSlug.MatchString(q.MeasurementAlgorithm) {
		return browserDependencyError("invalid measurement cohort")
	}
	if !browserAcceptanceSlug.MatchString(record.Provider) || !browserAcceptanceSlug.MatchString(record.Profile) || !browserAcceptanceSlug.MatchString(record.Platform) || !browserAcceptanceCommit.MatchString(record.CommitSHA) || !browserAcceptanceHash.MatchString(record.BinarySHA256) {
		return browserDependencyError("exact engine, profile, platform, full commit and binary digest are required")
	}
	if q.AnalysisAlgorithm != "" && !browserAcceptanceSlug.MatchString(q.AnalysisAlgorithm) {
		return browserDependencyError("invalid analysis algorithm")
	}
	if len(q.Dependencies) > 8 {
		return browserDependencyError("at most eight dependencies are allowed")
	}
	seen := map[string]bool{}
	for _, dep := range q.Dependencies {
		if strings.TrimSpace(dep.RecordID) == "" || len(dep.RecordID) > 128 || !browserAcceptanceHash.MatchString(dep.RecordSHA256) || seen[dep.RecordID] {
			return browserDependencyError("invalid or duplicate dependency pin")
		}
		seen[dep.RecordID] = true
	}
	if promotion {
		if len(record.Artifacts) == 0 {
			return browserDependencyError("promotion artifact hashes are required")
		}
		for _, artifact := range record.Artifacts {
			if strings.TrimSpace(artifact.Name) == "" || !browserAcceptanceHash.MatchString(artifact.SHA256) {
				return browserDependencyError("promotion artifact digest is invalid")
			}
		}
	}
	if promotion && (q.AnalysisAlgorithm == "" || len(q.Dependencies) == 0) {
		return browserDependencyError("an analysis algorithm and accepted BE-4 evidence are required")
	}
	if !IsBrowserIntelligenceAcceptance(record) && q.AnalysisAlgorithm != "" {
		return browserDependencyError("only browser intelligence records can pin an analysis algorithm")
	}
	if !IsBrowserIntelligenceAcceptance(record) && len(q.Dependencies) > 0 {
		return browserDependencyError("only browser intelligence records can pin BE dependencies")
	}
	return nil
}

// BrowserAcceptanceCohortSHA256 identifies the measurement and analysis scope.
// A new evidence pin does not create another acceptance subject.
func BrowserAcceptanceCohortSHA256(q *BrowserAcceptanceQualification) string {
	if q == nil {
		return ""
	}
	scope := *q
	scope.Dependencies = nil
	raw, _ := json.Marshal(scope)
	sum := sha256.Sum256(append([]byte("browser_acceptance_cohort.v1\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

// BrowserAcceptanceRecordSHA256 binds the immutable record, including artifact
// hashes, actor, insertion sequence and cohort, with microsecond UTC timestamps.
// Its returned display digest is excluded from its own input.
func BrowserAcceptanceRecordSHA256(record *FeatureAcceptanceRecord) (string, error) {
	if record == nil || record.Seq <= 0 {
		return "", browserDependencyError("dependency has no durable insertion sequence")
	}
	copy := *record
	copy.RecordSHA256 = ""
	// PostgreSQL persists microsecond timestamps; normalize both dialects.
	copy.RecordedAt = copy.RecordedAt.UTC().Truncate(time.Microsecond)
	if copy.Artifacts == nil {
		copy.Artifacts = []FeatureAcceptanceArtifact{}
	}
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("browser_acceptance_record.v1\x00"), raw...))
	return hex.EncodeToString(sum[:]), nil
}

func checkBrowserAcceptanceDependencies(tx *storeTx, record *FeatureAcceptanceRecord) error {
	if !IsBrowserIntelligenceAcceptance(record) || record.Result != AcceptancePassed {
		return nil
	}
	if err := validateBrowserAcceptanceQualification(record); err != nil {
		return err
	}
	for _, pin := range record.Qualification.Dependencies {
		rows, err := tx.Query(`SELECT `+featureAcceptanceColumns+` FROM feature_acceptance_records WHERE id = ?`, pin.RecordID)
		if err != nil {
			return err
		}
		candidates, err := scanFeatureAcceptanceRecords(rows)
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(candidates) != 1 {
			return browserDependencyError("referenced BE record is missing")
		}
		base := candidates[0]
		if base.RequirementID != "BE-4" || base.Result != AcceptancePassed || len(base.Artifacts) == 0 || base.RecordedBy == "" || base.Qualification == nil {
			return browserDependencyError("referenced BE record is not an accepted cohort")
		}
		if err := validateBrowserAcceptanceQualification(base); err != nil {
			return err
		}
		for _, artifact := range base.Artifacts {
			if strings.TrimSpace(artifact.Name) == "" || !browserAcceptanceHash.MatchString(artifact.SHA256) {
				return browserDependencyError("BE artifact digest is invalid")
			}
		}
		digest, err := BrowserAcceptanceRecordSHA256(base)
		if err != nil {
			return err
		}
		if digest != pin.RecordSHA256 {
			return browserDependencyError("BE record digest changed")
		}
		a, b := record.Qualification, base.Qualification
		if record.CommitSHA != base.CommitSHA || record.BinarySHA256 != base.BinarySHA256 || record.Provider != base.Provider || record.Profile != base.Profile || record.Platform != base.Platform || a.RuntimeSHA256 != b.RuntimeSHA256 || a.CapabilitySHA256 != b.CapabilitySHA256 || a.Metric != b.Metric || a.MeasurementAlgorithm != b.MeasurementAlgorithm {
			return browserDependencyError("BE binary or measurement cohort does not match")
		}
		// A new exact-subject result supersedes the pin. Any newer covering
		// withdrawal still applies even when a later broader pass was inserted.
		var newer bool
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM feature_acceptance_records WHERE requirement_id = ? AND feature = ? AND seq > ? AND ((profile = ? AND provider = ? AND platform = ? AND cohort_sha256 = ?) OR (result <> ? AND (cohort_sha256 = '' OR cohort_sha256 = ?) AND (profile = '' OR profile = ?) AND (provider = '' OR provider = ?) AND (platform = '' OR platform = ?))))`, base.RequirementID, base.Feature, base.Seq, base.Profile, base.Provider, base.Platform, base.CohortSHA256, AcceptancePassed, base.CohortSHA256, base.Profile, base.Provider, base.Platform).Scan(&newer)
		if err != nil {
			return err
		}
		if newer {
			return browserDependencyError("BE evidence was superseded or withdrawn")
		}
	}
	return nil
}

// Status checks use one snapshot and never revise historical records. Appends
// run this same check after taking the sequence writer lock in their transaction.
func (s *SQLiteStore) CheckBrowserAcceptanceDependencies(record *FeatureAcceptanceRecord) error {
	if record == nil {
		return browserDependencyError("record missing")
	}
	var opts *sql.TxOptions
	if s.db.dialect == storeDialectPostgres {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	tx, err := s.db.BeginTx(context.Background(), opts)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkBrowserAcceptanceDependencies(tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

// AppendBrowserIntelligenceAcceptanceRecord advertises atomic promotion support
// to callers that cannot assume every FeatureAcceptanceStore implements it.
func (s *SQLiteStore) AppendBrowserIntelligenceAcceptanceRecord(record *FeatureAcceptanceRecord) error {
	if !IsBrowserIntelligenceAcceptance(record) || record.Result != AcceptancePassed {
		return browserDependencyError("atomic promotion requires a BI pass")
	}
	return s.AppendFeatureAcceptanceRecord(record)
}

var _ BrowserAcceptanceDependencyStore = (*SQLiteStore)(nil)
