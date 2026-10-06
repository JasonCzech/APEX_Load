package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AnalysisDefinition revisions are append-only. Deletion hides the latest
// definition while preserving the revision that an import or comparison used.
type AnalysisDefinition struct {
	ID         string          `json:"id"`
	ProjectID  string          `json:"project_id"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Revision   int64           `json:"revision"`
	Definition json.RawMessage `json:"definition"`
	Deleted    bool            `json:"deleted"`
	CreatedAt  time.Time       `json:"created_at"`
}

// AnalysisDefinitionKindConnectionLinks holds one connection's deep link
// settings; it is not a user analytical definition.
const AnalysisDefinitionKindConnectionLinks = "connection_links"

// AnalysisDefinitionKindRCAMapping records an operator's decision that a
// run dependency is, or is not, a provider entity, for root-cause graphs.
const AnalysisDefinitionKindRCAMapping = "rca_mapping"

// AnalysisDefinitionKindBusinessKeyMapping is a project admin's approval to
// join a script extraction (a business key such as an order ID) to a log
// field of a monitor source.
const AnalysisDefinitionKindBusinessKeyMapping = "business_key_mapping"

// Policy kinds are revisioned project policies. A run pins the revisions in
// force when it starts, so a later edit never reinterprets a finished run.
const (
	AnalysisDefinitionKindTelemetryPolicy           = "telemetry_policy"
	AnalysisDefinitionKindTelemetryAcceptancePolicy = "telemetry_acceptance_policy"
	AnalysisDefinitionKindBrowserPolicy             = "browser_policy"
	AnalysisDefinitionKindJourneyPolicy             = "journey_policy"
	AnalysisDefinitionKindDerivedOperation          = "derived_operation"
)

// AnalysisPolicyKinds lists the policy kinds in a stable order.
var AnalysisPolicyKinds = []string{
	AnalysisDefinitionKindTelemetryPolicy,
	AnalysisDefinitionKindTelemetryAcceptancePolicy,
	AnalysisDefinitionKindBrowserPolicy,
	AnalysisDefinitionKindJourneyPolicy,
	AnalysisDefinitionKindDerivedOperation,
}

// ownBudgetAnalysisKinds have a definition budget of their own, apart from
// the mappings' and comparisons' shared budget.
var ownBudgetAnalysisKinds = map[string]bool{
	AnalysisDefinitionKindRCAMapping: true, AnalysisDefinitionKindBusinessKeyMapping: true,
	AnalysisDefinitionKindTelemetryPolicy: true, AnalysisDefinitionKindTelemetryAcceptancePolicy: true,
	AnalysisDefinitionKindBrowserPolicy: true, AnalysisDefinitionKindJourneyPolicy: true,
	AnalysisDefinitionKindDerivedOperation: true,
}

type AnalysisDefinitionStore interface {
	SaveAnalysisDefinition(AnalysisDefinition, int64) (*AnalysisDefinition, error)
	GetAnalysisDefinition(string, string, int64) (*AnalysisDefinition, error)
	ListAnalysisDefinitions(string, string, int, int) ([]AnalysisDefinition, error)
}

// AnalysisPolicyStore keeps at most one live policy per kind and project
// applying by default, decided inside the save transaction, and reads every
// live policy of a kind in one statement.
type AnalysisPolicyStore interface {
	SaveAnalysisPolicyDefinition(in AnalysisDefinition, expected int64, appliesByDefault func(json.RawMessage) (bool, error)) (*AnalysisDefinition, error)
	ListLatestAnalysisDefinitions(project, kind string) ([]AnalysisDefinition, error)
}

var _ AnalysisPolicyStore = (*SQLiteStore)(nil)

var (
	// ErrAnalysisDefinitionInvalid refuses a malformed definition envelope.
	ErrAnalysisDefinitionInvalid = errors.New("invalid analytical definition")
	// ErrAnalysisDefinitionLimit refuses a definition over its project or
	// revision budget.
	ErrAnalysisDefinitionLimit = errors.New("analytical definition limit reached")
	// ErrAnalysisPolicyDefaultConflict refuses a second live default policy of
	// the same kind in a project.
	ErrAnalysisPolicyDefaultConflict = errors.New("another policy of this kind already applies by default")
)

// maxAnalysisDefinitionsPerKind is each budget's distinct-definition limit.
const maxAnalysisDefinitionsPerKind = 1000

func (s *SQLiteStore) SaveAnalysisDefinition(in AnalysisDefinition, expected int64) (*AnalysisDefinition, error) {
	return s.saveAnalysisDefinition(in, expected, nil)
}

// SaveAnalysisPolicyDefinition saves a policy revision. When the revision
// applies by default, every other live policy of the kind is checked under the
// project lock, so two concurrent saves cannot both become the default.
func (s *SQLiteStore) SaveAnalysisPolicyDefinition(in AnalysisDefinition, expected int64, appliesByDefault func(json.RawMessage) (bool, error)) (*AnalysisDefinition, error) {
	if appliesByDefault == nil {
		return nil, ErrAnalysisDefinitionInvalid
	}
	return s.saveAnalysisDefinition(in, expected, appliesByDefault)
}

func (s *SQLiteStore) saveAnalysisDefinition(in AnalysisDefinition, expected int64, appliesByDefault func(json.RawMessage) (bool, error)) (*AnalysisDefinition, error) {
	if in.ID == "" || len(in.ID) > 128 || in.ProjectID == "" || strings.TrimSpace(in.Name) == "" || len(in.Name) > 256 || expected < 0 || expected >= 1<<62 || (in.Kind != "mapping" && in.Kind != "comparison" && in.Kind != AnalysisDefinitionKindConnectionLinks && !ownBudgetAnalysisKinds[in.Kind]) || !json.Valid(in.Definition) || len(in.Definition) > 128<<10 {
		return nil, ErrAnalysisDefinitionInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, in.ProjectID); err != nil {
		return nil, err
	}
	var revision int64
	var project, kind string
	err = tx.QueryRow(`SELECT project_id,kind,revision FROM analysis_definitions WHERE id=? ORDER BY revision DESC LIMIT 1`, in.ID).Scan(&project, &kind, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if revision != expected || revision > 0 && (project != in.ProjectID || kind != in.Kind) {
		return nil, ErrAnalysisRevision
	}
	// Connection link settings are one per connection and stay out of the
	// mappings' and comparisons' budget; root-cause and business-key mapping
	// decisions each have a budget of their own.
	if expected == 0 && in.Kind != AnalysisDefinitionKindConnectionLinks {
		var n int
		query := `SELECT COUNT(DISTINCT id) FROM analysis_definitions WHERE project_id=? AND kind IN (?, ?)`
		args := []any{in.ProjectID, "mapping", "comparison"}
		if ownBudgetAnalysisKinds[in.Kind] {
			query, args = `SELECT COUNT(DISTINCT id) FROM analysis_definitions WHERE project_id=? AND kind=?`, []any{in.ProjectID, in.Kind}
		}
		if err = tx.QueryRow(query, args...).Scan(&n); err != nil {
			return nil, err
		}
		if n >= maxAnalysisDefinitionsPerKind {
			return nil, fmt.Errorf("%w: project definitions", ErrAnalysisDefinitionLimit)
		}
	}
	if expected >= 1000 {
		return nil, fmt.Errorf("%w: revisions", ErrAnalysisDefinitionLimit)
	}
	if appliesByDefault != nil && !in.Deleted {
		isDefault, err := appliesByDefault(in.Definition)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAnalysisDefinitionInvalid, err)
		}
		if isDefault {
			others, err := latestAnalysisDefinitionsTx(tx, in.ProjectID, in.Kind)
			if err != nil {
				return nil, err
			}
			for _, other := range others {
				if other.ID == in.ID || other.Deleted {
					continue
				}
				// A stored row that no longer decodes cannot be proven not to be
				// a default; it blocks a new default until it is fixed or deleted.
				otherDefault, err := appliesByDefault(other.Definition)
				if err != nil || otherDefault {
					return nil, fmt.Errorf("%w: %q", ErrAnalysisPolicyDefaultConflict, other.Name)
				}
			}
		}
	}
	in.Revision = expected + 1
	in.CreatedAt = time.Now().UTC()
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO analysis_definitions(id,project_id,kind,revision,payload) VALUES(?,?,?,?,?)`, in.ID, in.ProjectID, in.Kind, in.Revision, string(body)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &in, nil
}

func (s *SQLiteStore) GetAnalysisDefinition(project, id string, revision int64) (*AnalysisDefinition, error) {
	var body string
	var err error
	if revision == 0 {
		err = s.db.QueryRow(`SELECT payload FROM analysis_definitions WHERE project_id=? AND id=? ORDER BY revision DESC LIMIT 1`, project, id).Scan(&body)
	} else {
		err = s.db.QueryRow(`SELECT payload FROM analysis_definitions WHERE project_id=? AND id=? AND revision=?`, project, id, revision).Scan(&body)
	}
	if err != nil {
		return nil, err
	}
	var in AnalysisDefinition
	err = json.Unmarshal([]byte(body), &in)
	return &in, err
}

// ListLatestAnalysisDefinitions returns the latest revision of every
// definition of a kind, deleted ones included, in one statement and ID order.
// The per-kind budget bounds the result.
func (s *SQLiteStore) ListLatestAnalysisDefinitions(project, kind string) ([]AnalysisDefinition, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return latestAnalysisDefinitionsTx(tx, project, kind)
}

func latestAnalysisDefinitionsTx(tx *storeTx, project, kind string) ([]AnalysisDefinition, error) {
	rows, err := tx.Query(`SELECT d.payload FROM analysis_definitions d WHERE d.project_id=? AND d.kind=? AND d.revision=(SELECT MAX(x.revision) FROM analysis_definitions x WHERE x.id=d.id) ORDER BY d.id LIMIT ?`, project, kind, maxAnalysisDefinitionsPerKind+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AnalysisDefinition{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var in AnalysisDefinition
		if err := json.Unmarshal([]byte(body), &in); err != nil {
			return nil, err
		}
		result = append(result, in)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) ListAnalysisDefinitions(project, kind string, limit, offset int) ([]AnalysisDefinition, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, errors.New("invalid analytical definitions page")
	}
	rows, err := s.db.Query(`SELECT d.payload FROM analysis_definitions d WHERE d.project_id=? AND d.kind=? AND d.revision=(SELECT MAX(x.revision) FROM analysis_definitions x WHERE x.id=d.id) ORDER BY d.id LIMIT ? OFFSET ?`, project, kind, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AnalysisDefinition{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var in AnalysisDefinition
		if err = json.Unmarshal([]byte(body), &in); err != nil {
			return nil, err
		}
		result = append(result, in)
	}
	return result, rows.Err()
}
