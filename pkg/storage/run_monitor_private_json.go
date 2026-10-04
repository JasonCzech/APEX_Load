package storage

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"time"
)

// Internal snapshots survive restarts while the exported type/schema excludes
// credential references. Public handlers explicitly remove private state.
type monitorJobJSON RunMonitorJob

func (j RunMonitorJob) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		monitorJobJSON
		Connection    *AppDynamicsConnectionSnapshot  `json:"connection_snapshot,omitempty"`
		QueryCoverage map[string]appdynamics.Coverage `json:"query_coverage,omitempty"`
		StableSinceAt *time.Time                      `json:"stable_since_at,omitempty"`
	}{monitorJobJSON(j), j.ConnectionSnapshot, j.QueryCoverage, j.StableSinceAt})
}
func (j *RunMonitorJob) UnmarshalJSON(body []byte) error {
	out := struct {
		*monitorJobJSON
		Connection    *AppDynamicsConnectionSnapshot  `json:"connection_snapshot,omitempty"`
		QueryCoverage map[string]appdynamics.Coverage `json:"query_coverage,omitempty"`
		StableSinceAt *time.Time                      `json:"stable_since_at,omitempty"`
	}{monitorJobJSON: (*monitorJobJSON)(j)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	j.ConnectionSnapshot = out.Connection
	j.QueryCoverage = out.QueryCoverage
	j.StableSinceAt = out.StableSinceAt
	return nil
}

type preparationMonitorJSON PreparationMonitorSnapshot

func (p PreparationMonitorSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		preparationMonitorJSON
		Connections map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
	}{preparationMonitorJSON(p), p.Connections})
}
func (p *PreparationMonitorSnapshot) UnmarshalJSON(body []byte) error {
	out := struct {
		*preparationMonitorJSON
		Connections map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
	}{preparationMonitorJSON: (*preparationMonitorJSON)(p)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	p.Connections = out.Connections
	return nil
}
