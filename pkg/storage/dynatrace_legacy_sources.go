package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
)

// EnsureLegacyDynatraceMonitorSource translates one explicitly selected legacy
// connection. It creates no scenario bindings and cannot enable monitoring on
// scenarios that never selected Dynatrace.
func (s *SQLiteStore) EnsureLegacyDynatraceMonitorSource(project, connectionID string) (*MonitorSource, error) {
	for range 3 {
		source, err := s.ensureLegacyDynatraceMonitorSource(project, connectionID)
		if !errors.Is(err, ErrMonitorRevision) {
			return source, err
		}
	}
	return nil, ErrMonitorRevision
}
func (s *SQLiteStore) ensureLegacyDynatraceMonitorSource(project, connectionID string) (*MonitorSource, error) {
	c, err := s.GetDynatraceConnection(connectionID)
	if err != nil {
		return nil, err
	}
	if c.ProjectID != project {
		return nil, sql.ErrNoRows
	}
	id := "dynatrace-legacy:" + connectionID
	if len(id) > 256 {
		hash := sha256.Sum256([]byte(connectionID))
		id = "dynatrace-legacy:" + hex.EncodeToString(hash[:])
	}
	sel := c.DefaultSelection()
	if err = sel.Validate(); err != nil {
		return nil, err
	}
	candidate := MonitorSource{ID: id, ProjectID: project, Name: c.Name, Pack: "dynatrace", ExecutorMode: c.CollectionMode, ConnectionID: c.ID, LegacyConnectionRevision: c.Revision, CollectorID: c.CollectorAgentID, Dynatrace: &sel, IntervalSeconds: max(60, c.PollIntervalSeconds), Enabled: c.Enabled, Destination: DynatraceDestination(c.Snapshot())}
	var raw string
	err = s.db.QueryRow(`SELECT payload FROM monitor_sources WHERE id=? AND project_id=?`, id, project).Scan(&raw)
	if err == nil {
		var source MonitorSource
		if err = json.Unmarshal([]byte(raw), &source); err != nil {
			return nil, err
		}
		if source.LegacyConnectionRevision == candidate.LegacyConnectionRevision && source.Name == candidate.Name && source.ExecutorMode == candidate.ExecutorMode && source.CollectorID == candidate.CollectorID && source.Enabled == candidate.Enabled && source.IntervalSeconds == candidate.IntervalSeconds && source.Destination == candidate.Destination && reflect.DeepEqual(source.Dynatrace, candidate.Dynatrace) {
			return &source, nil
		}
		return s.SaveMonitorSource(candidate, source.Revision)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return s.SaveMonitorSource(candidate, 0)
}
