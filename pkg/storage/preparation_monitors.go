package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// PreparationMonitorSnapshot is immutable execution evidence. Definitions may
// change after consumption; the execution commit must still use this snapshot.
type PreparationMonitorSnapshot struct {
	MonitorRules          []analysis.MonitorRule                    `json:"monitor_rules,omitempty"`
	RulesFrozen           bool                                      `json:"rules_frozen,omitempty"`
	PrometheusConnections map[string]*PrometheusConnectionSnapshot  `json:"-"`
	ScenarioID            string                                    `json:"scenario_id,omitempty"`
	Bindings              MonitorBindings                           `json:"bindings"`
	Sources               map[string]MonitorSource                  `json:"sources"`
	Connections           map[string]*AppDynamicsConnectionSnapshot `json:"-"`
	DynatraceConnections  map[string]*DynatraceConnectionSnapshot   `json:"-"`
	ElasticConnections    map[string]*ElasticConnectionSnapshot     `json:"-"`
}

// Capability verification is mutable health for one unchanged connection
// revision. Comparing it as configuration would reject a preparation whose own
// connectivity probe just verified its frozen profile.
func samePreparationMonitorConfiguration(a, b *PreparationMonitorSnapshot) bool {
	withoutHealth := func(p *PreparationMonitorSnapshot) *PreparationMonitorSnapshot {
		if p == nil {
			return nil
		}
		copy := *p
		copy.DynatraceConnections = maps.Clone(p.DynatraceConnections)
		for id, snapshot := range copy.DynatraceConnections {
			if snapshot == nil {
				continue
			}
			configuration := *snapshot
			configuration.Capabilities = dynatrace.Capabilities{}
			configuration.VerifiedRevision = 0
			copy.DynatraceConnections[id] = &configuration
		}
		return &copy
	}
	return reflect.DeepEqual(withoutHealth(a), withoutHealth(b))
}

func freezeVerifiedDynatracePreparationCapabilities(frozen, current *PreparationMonitorSnapshot) {
	if frozen == nil || current == nil {
		return
	}
	for id, snapshot := range frozen.DynatraceConnections {
		verified := current.DynatraceConnections[id]
		if snapshot == nil || verified == nil || snapshot.Revision != verified.Revision || snapshot.ConnectionID != verified.ConnectionID {
			continue
		}
		snapshot.VerifiedRevision = verified.VerifiedRevision
		snapshot.Capabilities = verified.Capabilities
		snapshot.Capabilities.Status = maps.Clone(verified.Capabilities.Status)
		snapshot.Capabilities.VerifiedOrigins = slices.Clone(verified.Capabilities.VerifiedOrigins)
	}
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
	var goalsRaw string
	if err = tx.QueryRow(`SELECT COALESCE(goal_config_json,'') FROM tests WHERE id=? AND project_id=?`, run, project).Scan(&goalsRaw); err != nil {
		return nil, err
	}
	if goalsRaw != "" {
		var goals struct {
			MonitorRules []analysis.MonitorRule `json:"monitor_rules"`
		}
		if err = json.Unmarshal([]byte(goalsRaw), &goals); err != nil {
			return nil, err
		}
		snapshot.MonitorRules = goals.MonitorRules
	}
	snapshot.RulesFrozen = true

	var body string
	for _, binding := range snapshot.Bindings.Sources {
		err = tx.QueryRow(`SELECT payload FROM monitor_sources WHERE project_id=? AND id=?`, project, binding.SourceID).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var source MonitorSource
		if err = json.Unmarshal([]byte(body), &source); err != nil {
			return nil, err
		}
		if source.Pack == monitorprovider.PackAppDynamics || source.AppDynamics != nil {
			return nil, ErrMonitorProviderRetired
		}
		snapshot.Sources[source.ID] = source
		if source.Pack == monitorprovider.PackPrometheusQuery {
			var raw string
			if err = tx.QueryRow(`SELECT payload_json FROM prometheus_connections WHERE id=? AND project_id=?`, source.ConnectionID, project).Scan(&raw); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				return nil, err
			}
			c, e := UnmarshalPrometheusConnectionPrivate([]byte(raw))
			if e != nil {
				return nil, e
			}
			if c.ConfigurationStatus() != "ready" {
				continue
			}
			if snapshot.PrometheusConnections == nil {
				snapshot.PrometheusConnections = map[string]*PrometheusConnectionSnapshot{}
			}
			snapshot.PrometheusConnections[source.ID] = c.Snapshot()
		}
		if source.Pack == monitorprovider.PackDynatrace {
			c, e := scanDynatraceConnection(tx.QueryRow(`SELECT `+dynatraceConnectionColumns+` FROM dynatrace_connections WHERE id=? AND project_id=?`, source.ConnectionID, project))
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return nil, e
			}
			if snapshot.DynatraceConnections == nil {
				snapshot.DynatraceConnections = map[string]*DynatraceConnectionSnapshot{}
			}
			snapshot.DynatraceConnections[source.ID] = c.Snapshot()
		}
		if source.Pack == monitorprovider.PackElasticQuery {
			connection, _, e := elasticConnectionSnapshotTx(tx, project, source.ConnectionID)
			if e != nil {
				return nil, e
			}
			if connection != nil {
				if snapshot.ElasticConnections == nil {
					snapshot.ElasticConnections = map[string]*ElasticConnectionSnapshot{}
				}
				snapshot.ElasticConnections[source.ID] = connection
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
