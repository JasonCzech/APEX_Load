package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// SharedAssetScenarioBinding is one scenario's binding of a project's data
// pool or project live table: what a pool or table is used by.
type SharedAssetScenarioBinding struct {
	ScenarioID     string
	ScenarioName   string
	Archived       bool
	PoolID         string
	ProjectTableID string
	Alias          string
}

// SharedAssetScript is one script of a project with the object repositories
// its actions point at.
type SharedAssetScript struct {
	ID                  string
	Name                string
	Archived            bool
	ObjectRepositoryIDs []string
}

// ListProjectSharedAssetBindings lists every scenario binding of the
// project's data pools and project live tables. Scoped by the asset's own
// project, so it lists exactly the bindings that would keep a pool from being
// deleted.
func (s *SQLiteStore) ListProjectSharedAssetBindings(projectID string) ([]SharedAssetScenarioBinding, error) {
	project := normalizeProjectID(strings.TrimSpace(projectID))
	rows, err := s.db.Query(`SELECT b.scenario_id, sc.name, COALESCE(b.pool_id, ''), COALESCE(b.project_table_id, ''), b.alias,
		(SELECT archived_at FROM object_archive_states a WHERE a.resource_type='scenario' AND a.resource_id=sc.id AND `+archiveIncarnationMatch(s.db.dialect, "a.incarnation", "sc.created_at")+`)
		FROM scenario_data_pool_bindings b
		JOIN scenarios sc ON sc.id = b.scenario_id
		LEFT JOIN shared_data_pools p ON p.id = b.pool_id
		LEFT JOIN project_live_tables pt ON pt.id = b.project_table_id
		WHERE p.project_id = ? OR pt.project_id = ?
		ORDER BY sc.name, sc.id, b.alias`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []SharedAssetScenarioBinding{}
	for rows.Next() {
		var binding SharedAssetScenarioBinding
		var archivedAt *time.Time
		if err := rows.Scan(&binding.ScenarioID, &binding.ScenarioName, &binding.PoolID, &binding.ProjectTableID, &binding.Alias, &archivedAt); err != nil {
			return nil, err
		}
		binding.Archived = archivedAt != nil
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

// ListProjectSharedAssetScripts lists the project's scripts, archived ones
// included. With references, each carries the object repositories its actions
// point at (the same references that keep a repository from being deleted);
// that decodes every script's actions, so callers that need only names skip it.
func (s *SQLiteStore) ListProjectSharedAssetScripts(projectID string, references bool) ([]SharedAssetScript, error) {
	project := normalizeProjectID(strings.TrimSpace(projectID))
	actionColumns := `'', '', ''`
	if references {
		actionColumns = `COALESCE(init, ''), COALESCE(actions, ''), COALESCE(teardown, '')`
	}
	rows, err := s.db.Query(`SELECT id, COALESCE(name, ''), `+actionColumns+`,
		(SELECT archived_at FROM object_archive_states a WHERE a.resource_type='script' AND a.resource_id=scripts.id AND `+archiveIncarnationMatch(s.db.dialect, "a.incarnation", "scripts.created_at")+`)
		FROM scripts WHERE COALESCE(NULLIF(project_id, ''), ?) = ? ORDER BY name, id`, DefaultProjectID, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scripts := []SharedAssetScript{}
	for rows.Next() {
		var script SharedAssetScript
		var initJSON, actionsJSON, teardownJSON string
		var archivedAt *time.Time
		if err := rows.Scan(&script.ID, &script.Name, &initJSON, &actionsJSON, &teardownJSON, &archivedAt); err != nil {
			return nil, err
		}
		script.Archived = archivedAt != nil
		seen := map[string]bool{}
		script.ObjectRepositoryIDs = []string{}
		for _, raw := range []string{initJSON, actionsJSON, teardownJSON} {
			ids, err := actionJSONObjectRepositoryIDs(raw)
			if err != nil {
				return nil, fmt.Errorf("script %s: %w", script.ID, err)
			}
			for _, id := range ids {
				if !seen[id] {
					seen[id] = true
					script.ObjectRepositoryIDs = append(script.ObjectRepositoryIDs, id)
				}
			}
		}
		scripts = append(scripts, script)
	}
	return scripts, rows.Err()
}

// actionJSONObjectRepositoryIDs returns the object repositories a stored action
// list points at, nested control branches included, in first-use order.
func actionJSONObjectRepositoryIDs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return nil, nil
	}
	var actions []dsl.Action
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		return nil, fmt.Errorf("decode script actions while listing object repository references: %w", err)
	}
	var ids []string
	var walk func([]dsl.Action)
	walk = func(actions []dsl.Action) {
		for i := range actions {
			if id := objectRepositoryIDOfRef(actions[i].ObjectRef); id != "" {
				ids = append(ids, id)
			}
			if actions[i].Control != nil {
				for _, branch := range actions[i].Control.ActionBranches() {
					walk(branch.Actions)
				}
			}
		}
	}
	walk(actions)
	return ids, nil
}

// objectRepositoryIDOfRef is the repository an object_ref of the form
// "<repository id>:<logical name>" names, or "" for any other reference.
func objectRepositoryIDOfRef(ref string) string {
	prefix, _, qualified := strings.Cut(strings.TrimSpace(ref), ":")
	prefix = strings.TrimSpace(prefix)
	if !qualified || !strings.HasPrefix(prefix, "object-repo-") {
		return ""
	}
	return prefix
}
