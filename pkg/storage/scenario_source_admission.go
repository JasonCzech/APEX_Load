package storage

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrScenarioSourceAdmissionConflict    = errors.New("scenario source evidence changed; reload before applying changes")
	ErrScenarioSourceAdmissionUnavailable = errors.New("scenario source evidence unavailable")
)

const maxScenarioSourceGroups = 256

// ScenarioSourceCondition is private durable evidence, never an authored field
// or a request-supplied claim that a source is published.
type ScenarioSourceCondition struct {
	ScriptID        string
	ProjectID       string
	CreatedAt       time.Time
	ContentRevision string
}
type ScenarioSourceAdmission struct {
	ProjectID   string
	Sources     []ScenarioSourceCondition
	Publication LibraryImportPublicationState
}
type ScenarioSourceAuthoringSnapshot struct {
	Sources   []*ScriptAuthoringSnapshot
	Admission ScenarioSourceAdmission
}

// This optional interface does not advertise public scenario CAS support.
// The compiler must use Sources from the durable initial snapshot; cached DSL
// and arbitrary callbacks cannot establish admission authority.
type ScenarioSourceAuthoringStore interface {
	GetScenarioSourceAuthoringSnapshot([]ScenarioScript) (*ScenarioSourceAuthoringSnapshot, error)
	CreateScenarioAuthoringWithSourcesIfAbsent(*Scenario, []ScenarioScript, []ScenarioDataPoolBinding, ScenarioSourceAdmission) (*ScenarioAuthoringMutationResult, error)
	UpdateScenarioAuthoringWithSourcesIfCurrent(*Scenario, []ScenarioScript, []ScenarioDataPoolBinding, ScenarioAuthoringPrecondition, ScenarioSourceAdmission) (*ScenarioAuthoringMutationResult, error)
}

var _ ScenarioSourceAuthoringStore = (*SQLiteStore)(nil)

func scenarioSourceIDs(groups []ScenarioScript) ([]string, error) {
	if len(groups) == 0 || len(groups) > maxScenarioSourceGroups {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	unique := make(map[string]bool, len(groups))
	for _, g := range groups {
		if g.ScriptID == "" || strings.TrimSpace(g.ScriptID) != g.ScriptID {
			return nil, ErrScenarioSourceAdmissionUnavailable
		}
		unique[g.ScriptID] = true
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func validPublicationCondition(state LibraryImportPublicationState) bool {
	if len(state.SHA256) != 64 {
		return false
	}
	for _, r := range state.SHA256 {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return state.Exists || state.SHA256 == libraryImportPublicationSnapshot("", false).State.SHA256
}

func cloneScenarioSourceAdmission(groups []ScenarioScript, input ScenarioSourceAdmission) (*ScenarioSourceAdmission, error) {
	ids, err := scenarioSourceIDs(groups)
	if err != nil {
		return nil, err
	}
	if input.ProjectID == "" || normalizeProjectID(input.ProjectID) != input.ProjectID || !validPublicationCondition(input.Publication) || len(input.Sources) != len(ids) {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	result := input
	result.Sources = append([]ScenarioSourceCondition(nil), input.Sources...)
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].ScriptID < result.Sources[j].ScriptID })
	for i, c := range result.Sources {
		if c.ScriptID != ids[i] || c.ProjectID != input.ProjectID || c.CreatedAt.IsZero() || !ValidScriptContentRevision(c.ContentRevision) {
			return nil, ErrScenarioSourceAdmissionUnavailable
		}
	}
	return &result, nil
}

func scenarioSourcesPublished(snapshot *LibraryImportPublicationSnapshot, ids []string) error {
	if snapshot == nil {
		return ErrScenarioSourceAdmissionUnavailable
	}
	facts, err := ParseLibraryImportPublication(snapshot.Raw)
	if err != nil {
		return ErrScenarioSourceAdmissionUnavailable
	}
	pending := make(map[string]bool)
	for _, f := range facts {
		if f.PendingDirectImport() {
			pending[LibraryImportAssetKey(f.Kind, f.LocalItemID)] = true
		}
	}
	for _, id := range ids {
		if pending[LibraryImportAssetKey("script", id)] {
			return ErrScenarioSourceAdmissionConflict
		}
	}
	return nil
}

// Called only after publication, project, and (for update) scenario locks. The
// condition list is exact, deduplicated, canonical, and sorted before this TX.
func admitScenarioSourcesTx(tx *storeTx, admission *ScenarioSourceAdmission, pools []ScenarioDataPoolBinding) error {
	ids := make([]string, 0, len(admission.Sources))
	for _, c := range admission.Sources {
		if err := claimScriptAuthoringTx(tx, c.ScriptID, ScriptAuthoringPrecondition{ProjectID: c.ProjectID, CreatedAt: c.CreatedAt, ContentRevision: c.ContentRevision}); err != nil {
			if errors.Is(err, ErrScriptAuthoringConflict) || errors.Is(err, ErrScriptAuthoringNotFound) {
				return ErrScenarioSourceAdmissionConflict
			}
			return ErrScenarioSourceAdmissionUnavailable
		}
		if err := ensureObjectActiveTx(tx, "script", c.ScriptID); err != nil {
			return ErrScenarioSourceAdmissionUnavailable
		}
		ids = append(ids, c.ScriptID)
	}
	if err := validateScenarioAuthoringPoolsActiveTx(tx, pools); err != nil {
		return ErrScenarioSourceAdmissionUnavailable
	}
	for _, pool := range pools {
		if pool.PoolID == "" || strings.TrimSpace(pool.PoolID) != pool.PoolID {
			return ErrScenarioSourceAdmissionUnavailable
		}
		var owner string
		if err := tx.QueryRow(`SELECT COALESCE(project_id,'') FROM shared_data_pools WHERE id=?`, pool.PoolID).Scan(&owner); err != nil || normalizeProjectID(owner) != admission.ProjectID {
			return ErrScenarioSourceAdmissionUnavailable
		}
	}
	snapshot, err := readLibraryImportPublicationTx(tx)
	if err != nil {
		return ErrScenarioSourceAdmissionUnavailable
	}
	if snapshot.State != admission.Publication {
		return ErrScenarioSourceAdmissionConflict
	}
	return scenarioSourcesPublished(snapshot, ids)
}

func (s *SQLiteStore) GetScenarioSourceAuthoringSnapshot(groups []ScenarioScript) (*ScenarioSourceAuthoringSnapshot, error) {
	ids, err := scenarioSourceIDs(append([]ScenarioScript(nil), groups...))
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	defer tx.Rollback()
	if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	// Discover the owning project, then recheck every owner under project/source
	// locks. The initial discovery read is never mutation authority by itself.
	var owner string
	if err := tx.QueryRow(`SELECT COALESCE(project_id,'') FROM scripts WHERE id=?`, ids[0]).Scan(&owner); err != nil {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	owner = normalizeProjectID(owner)
	if _, err := lockProjectForOwnedMutation(tx, owner); err != nil {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	result := &ScenarioSourceAuthoringSnapshot{Admission: ScenarioSourceAdmission{ProjectID: owner}}
	for _, id := range ids {
		if err := ensureObjectActiveTx(tx, "script", id); err != nil {
			return nil, ErrScenarioSourceAdmissionUnavailable
		}
		snapshot, err := readScriptAuthoringSnapshotTx(tx, id)
		if err != nil || normalizeProjectID(snapshot.Script.ProjectID) != owner {
			return nil, ErrScenarioSourceAdmissionUnavailable
		}
		result.Sources = append(result.Sources, snapshot)
		result.Admission.Sources = append(result.Admission.Sources, ScenarioSourceCondition{ScriptID: id, ProjectID: owner, CreatedAt: snapshot.Script.CreatedAt, ContentRevision: snapshot.ContentRevision})
	}
	publication, err := readLibraryImportPublicationTx(tx)
	if err != nil {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	if err := scenarioSourcesPublished(publication, ids); err != nil {
		return nil, err
	}
	result.Admission.Publication = publication.State
	if err := tx.Commit(); err != nil {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	return result, nil
}

func sourceAdmissionResult(result *ScenarioAuthoringMutationResult, err error) (*ScenarioAuthoringMutationResult, error) {
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrScenarioSourceAdmissionConflict) || errors.Is(err, ErrScenarioAuthoringConflict) {
		return nil, ErrScenarioSourceAdmissionConflict
	}
	return nil, ErrScenarioSourceAdmissionUnavailable
}

func (s *SQLiteStore) CreateScenarioAuthoringWithSourcesIfAbsent(input *Scenario, groups []ScenarioScript, pools []ScenarioDataPoolBinding, evidence ScenarioSourceAdmission) (*ScenarioAuthoringMutationResult, error) {
	admission, err := cloneScenarioSourceAdmission(groups, evidence)
	if err != nil {
		return nil, err
	}
	result, err := s.createScenarioAuthoringIfAbsent(input, groups, pools, admission)
	return sourceAdmissionResult(result, err)
}
func (s *SQLiteStore) UpdateScenarioAuthoringWithSourcesIfCurrent(input *Scenario, groups []ScenarioScript, pools []ScenarioDataPoolBinding, expected ScenarioAuthoringPrecondition, evidence ScenarioSourceAdmission) (*ScenarioAuthoringMutationResult, error) {
	admission, err := cloneScenarioSourceAdmission(groups, evidence)
	if err != nil {
		return nil, err
	}
	if normalizeProjectID(expected.ProjectID) != admission.ProjectID {
		return nil, ErrScenarioSourceAdmissionUnavailable
	}
	result, err := s.updateScenarioAuthoringIfCurrent(input, groups, pools, expected, admission)
	return sourceAdmissionResult(result, err)
}

func lockPrivateScenarioCreateTx(tx *storeTx, id string) error {
	query := `SELECT id FROM scenarios WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var existing string
	err := tx.QueryRow(query, id).Scan(&existing)
	if err == nil {
		return ErrScenarioSourceAdmissionConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}
