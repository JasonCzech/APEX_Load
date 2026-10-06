package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// PreparationMonitorSnapshot is immutable execution evidence. Definitions may
// change after consumption; the execution commit must still use this snapshot.
type PreparationMonitorSnapshot struct {
	Warnings           []string                                  `json:"warnings,omitempty"`
	ElasticConnections map[string]*ElasticConnectionSnapshot     `json:"-"`
	ScenarioID         string                                    `json:"scenario_id,omitempty"`
	Bindings           MonitorBindings                           `json:"bindings"`
	Sources            map[string]MonitorSource                  `json:"sources"`
	Connections        map[string]*AppDynamicsConnectionSnapshot `json:"-"`
}

// SetPreparationMonitorWarnings records bounded readiness diagnostics without
// changing the prepared definitions or their optimistic execution revision.
// Consumed and expired preparations remain immutable.
func (s *SQLiteStore) SetPreparationMonitorWarnings(project, test string, warnings []string) error {
	if len(warnings) > MaxRunMonitorSources*3 {
		return errors.New("too many monitoring preparation warnings")
	}
	clean := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 512 || strings.IndexFunc(warning, unicode.IsControl) >= 0 {
			return errors.New("invalid monitoring preparation warning")
		}
		safe := analysis.ScrubProviderText(warning)
		if len(safe) > 512 {
			return errors.New("monitoring preparation warning exceeds scrubbed bound")
		}
		clean = append(clean, safe)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	var body string
	err = tx.QueryRow(`SELECT payload FROM preparations WHERE project_id=? AND test_id=? AND state IN ('preparing','ready') ORDER BY created_at DESC,id DESC LIMIT 1`, project, test).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	var prepared Preparation
	if err = json.Unmarshal([]byte(body), &prepared); err != nil {
		return err
	}
	if prepared.Monitors == nil || !prepared.ExpiresAt.After(time.Now()) {
		return tx.Commit()
	}
	seen := make(map[string]bool, len(prepared.Monitors.Warnings)+len(clean))
	for _, warning := range prepared.Monitors.Warnings {
		seen[warning] = true
	}
	for _, warning := range clean {
		if !seen[warning] && len(prepared.Monitors.Warnings) < MaxRunMonitorSources*3 {
			prepared.Monitors.Warnings = append(prepared.Monitors.Warnings, warning)
			seen[warning] = true
		}
	}
	updated, err := json.Marshal(prepared)
	if err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE preparations SET payload=? WHERE id=? AND project_id=? AND revision=? AND state=? AND payload=?`, string(updated), prepared.ID, project, prepared.Revision, prepared.State, body)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrPreparationConflict
	}
	return tx.Commit()
}

func preparationMonitorSnapshotTx(tx *storeTx, project, run string) (*PreparationMonitorSnapshot, error) {
	snapshot := &PreparationMonitorSnapshot{Sources: map[string]MonitorSource{}}
	if err := tx.QueryRow(`SELECT COALESCE(scenario_id,'') FROM tests WHERE id=? AND project_id=?`, run, project).Scan(&snapshot.ScenarioID); err != nil {
		return nil, err
	}
	resolved, err := resolveRunMonitorBindingsTx(tx, project, run, snapshot.ScenarioID)
	if err != nil {
		return nil, err
	}
	snapshot.Bindings = *resolved

	warn := func(binding MonitorBinding, reason string) {
		if !binding.Required && len(snapshot.Warnings) < MaxRunMonitorSources*3 {
			snapshot.Warnings = append(snapshot.Warnings, fmt.Sprintf("monitor %s: %s", binding.SourceID, reason))
		}
	}
	var body string
	for _, binding := range snapshot.Bindings.Sources {
		err = tx.QueryRow(`SELECT payload FROM monitor_sources WHERE project_id=? AND id=?`, project, binding.SourceID).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			warn(binding, "source_unavailable")
			continue
		}
		if err != nil {
			return nil, err
		}
		var source MonitorSource
		if err = json.Unmarshal([]byte(body), &source); err != nil {
			return nil, err
		}
		snapshot.Sources[source.ID] = source
		if !source.Enabled {
			warn(binding, "source_disabled")
		} else if source.Validate() != nil {
			warn(binding, "source_invalid")
		}
		if source.Pack == "elastic" {
			c, e := scanELKConnection(tx.QueryRow(`SELECT `+elkConnectionColumns+` FROM elk_connections WHERE id=? AND project_id=?`, source.ConnectionID, project))
			if errors.Is(e, sql.ErrNoRows) {
				warn(binding, "connection_unavailable")
				continue
			}
			if e != nil {
				return nil, e
			}
			if snapshot.ElasticConnections == nil {
				snapshot.ElasticConnections = map[string]*ElasticConnectionSnapshot{}
			}
			snapshot.ElasticConnections[source.ID] = c.Snapshot()
			if c.ConfigurationStatus() != "ready" || !c.Enabled {
				warn(binding, "connection_not_ready")
			} else if ValidateElasticSourceConnection(source, c.Snapshot()) != nil {
				warn(binding, "connection_source_mismatch")
			}
		}
		if source.Pack == "appdynamics" {
			var raw string
			if err = tx.QueryRow(`SELECT payload_json FROM appdynamics_connections WHERE id=? AND project_id=?`, source.ConnectionID, project).Scan(&raw); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					warn(binding, "connection_unavailable")
					continue
				}
				return nil, err
			}
			c, e := UnmarshalAppDynamicsConnectionPrivate([]byte(raw))
			if e != nil {
				return nil, e
			}
			if snapshot.Connections == nil {
				snapshot.Connections = map[string]*AppDynamicsConnectionSnapshot{}
			}
			snapshot.Connections[source.ID] = c.Snapshot()
			if c.ConfigurationStatus() != "ready" || !c.Enabled {
				warn(binding, "connection_not_ready")
			} else if ValidateAppDynamicsSourceConnection(source, c.Snapshot()) != nil {
				warn(binding, "connection_source_mismatch")
			}
		}
	}
	return snapshot, nil
}

func consumedMonitorSnapshotTx(tx *storeTx, project, run string) (*PreparationMonitorSnapshot, error) {
	var body string
	err := tx.QueryRow(`SELECT payload FROM preparations WHERE project_id=? AND test_id=? AND state='consumed' ORDER BY created_at DESC,id DESC LIMIT 1`, project, run).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Preparation
	if err = json.Unmarshal([]byte(body), &p); err != nil {
		return nil, err
	}
	if p.Monitors == nil {
		return nil, ErrPreparationConflict
	}
	return p.Monitors, nil
}
