package appdynamics

import (
	"context"
	"errors"
	"strings"
)

// Entity names only identify catalog paths internally. Persisted scope always
// uses verified Controller IDs, including wildcard metric results.
type entityLookup struct {
	client    *Client
	ctx       context.Context
	rows      map[string][]map[string]any
	failures  map[string]error
	topology  map[string]Entity
	truncated bool
}

func (l *entityLookup) record(kind, id, name, parent, application string) {
	if !numericID.MatchString(id) || name == "" {
		return
	}
	if l.topology == nil {
		l.topology = map[string]Entity{}
	}
	key := kind + ":" + id + ":" + parent
	if _, exists := l.topology[key]; !exists && len(l.topology) >= 256 {
		l.truncated = true
		return
	}
	ids := map[string]string{"application": application, kind: id}
	if (kind == "node" || kind == "business_transaction") && numericID.MatchString(parent) {
		ids["tier"] = parent
	}
	credentials := Credentials{}
	if l.client != nil {
		credentials = l.client.credentials
	}
	l.topology[key] = Entity{ID: id, Name: safeLabel(name, 256, credentials), Type: kind, ParentID: parent, EntityIDs: ids}
}

func (l *entityLookup) discover(application, kind string) ([]map[string]any, error) {
	key := application + ":" + kind
	if rows, ok := l.rows[key]; ok {
		return rows, l.failures[key]
	}
	var rows []map[string]any
	err := l.client.query(l.ctx, "GET", "/rest/applications/"+application+"/"+kind, nil, nil, false, &rows)
	if len(rows) > 1000 {
		rows = nil
		err = errors.New("metric entity discovery exceeds bound")
	}
	l.rows[key], l.failures[key] = rows, err
	return rows, err
}
func (l *entityLookup) selection(m MetricSelection, row metricResponse) (MetricSelection, error) {
	l.record("application", m.ApplicationID, "Application "+m.ApplicationID, m.ApplicationID, m.ApplicationID)
	ids := map[string]string{}
	// Explicit IDs describe only an exact catalog selection. Wildcard results
	// require IDs supplied by the Controller rather than copied selection labels.
	if !strings.Contains(m.Path, "*") {
		for k, v := range m.EntityIDs {
			ids[k] = v
		}
	}
	bind := func(kind, id string) error {
		if !numericID.MatchString(id) {
			return nil
		}
		if expected := ids[kind]; expected != "" && expected != id {
			return errors.New("metric entity differs from selected scope")
		}
		ids[kind] = id
		return nil
	}
	for _, part := range strings.Split(row.Name, "|") {
		if kind, id, ok := strings.Cut(part, ":"); ok {
			switch kind {
			case "BT":
				if err := bind("business_transaction", id); err != nil {
					return m, err
				}
			case "Component":
				if err := bind("tier", id); err != nil {
					return m, err
				}
			case "Backend", "Exit":
				if err := bind("backend", id); err != nil {
					return m, err
				}
			}
		}
	}
	parts := strings.Split(row.Path, "|")
	if len(parts) >= 4 && parts[0] == "Business Transaction Performance" && parts[1] == "Business Transactions" {
		l.record("tier", ids["tier"], parts[2], m.ApplicationID, m.ApplicationID)
		l.record("business_transaction", ids["business_transaction"], parts[3], ids["tier"], m.ApplicationID)
	}
	if len(parts) >= 2 && parts[0] == "Application Infrastructure Performance" {
		kind := "tiers"
		if len(parts) >= 4 && parts[2] == "Individual Nodes" {
			kind = "nodes"
		}
		rows, err := l.discover(m.ApplicationID, kind)
		if err != nil {
			m.EntityIDs = ids
			return m, err
		}
		var match map[string]any
		for _, entity := range rows {
			name := scalar(entity["name"])
			found := kind == "tiers" && name == parts[1] || kind == "nodes" && name == parts[3] && scalar(entity["tierName"]) == parts[1]
			if !found {
				continue
			}
			if match != nil {
				return m, errors.New("ambiguous metric catalog entity")
			}
			match = entity
		}
		if match != nil {
			if kind == "tiers" {
				if err := bind("tier", scalar(match["id"])); err != nil {
					return m, err
				}
			} else {
				for raw, normalized := range map[string]string{"id": "node", "tierId": "tier", "machineId": "machine"} {
					if err := bind(normalized, scalar(match[raw])); err != nil {
						return m, err
					}
				}
			}
			l.record("tier", ids["tier"], parts[1], m.ApplicationID, m.ApplicationID)
			if kind == "nodes" {
				l.record("node", ids["node"], parts[3], ids["tier"], m.ApplicationID)
				l.record("machine", ids["machine"], scalar(match["machineName"]), m.ApplicationID, m.ApplicationID)
			}
		}
	}
	if len(parts) >= 2 && parts[0] == "Backend Performance" {
		rows, err := l.discover(m.ApplicationID, "backends")
		if err != nil {
			m.EntityIDs = ids
			return m, err
		}
		var match map[string]any
		for _, entity := range rows {
			if scalar(entity["name"]) != parts[1] {
				continue
			}
			if match != nil {
				return m, errors.New("ambiguous backend catalog entity")
			}
			match = entity
		}
		if match == nil {
			return m, errors.New("backend catalog identity could not be verified")
		}
		for raw, normalized := range map[string]string{"id": "backend", "tierId": "tier", "applicationComponentNodeId": "node"} {
			if err := bind(normalized, scalar(match[raw])); err != nil {
				return m, err
			}
		}
		l.record("backend", ids["backend"], parts[1], m.ApplicationID, m.ApplicationID)
	}
	m.EntityIDs = ids
	return m, nil
}
