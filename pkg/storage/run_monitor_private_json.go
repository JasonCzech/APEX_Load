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
		Dynatrace     *DynatraceConnectionSnapshot    `json:"dynatrace_snapshot,omitempty"`
		Cursor        string                          `json:"provider_cursor,omitempty"`
		RunTraceIDs   []string                        `json:"run_trace_ids,omitempty"`
	}{monitorJobJSON(j), j.ConnectionSnapshot, j.QueryCoverage, j.StableSinceAt, j.DynatraceSnapshot, j.ProviderCursor, j.RunTraceIDs})
}
func (j *RunMonitorJob) UnmarshalJSON(body []byte) error {
	out := struct {
		*monitorJobJSON
		Connection    *AppDynamicsConnectionSnapshot  `json:"connection_snapshot,omitempty"`
		QueryCoverage map[string]appdynamics.Coverage `json:"query_coverage,omitempty"`
		StableSinceAt *time.Time                      `json:"stable_since_at,omitempty"`
		Dynatrace     *DynatraceConnectionSnapshot    `json:"dynatrace_snapshot,omitempty"`
		Cursor        string                          `json:"provider_cursor,omitempty"`
		RunTraceIDs   []string                        `json:"run_trace_ids,omitempty"`
	}{monitorJobJSON: (*monitorJobJSON)(j)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	j.ConnectionSnapshot = out.Connection
	j.QueryCoverage = out.QueryCoverage
	j.StableSinceAt = out.StableSinceAt
	j.DynatraceSnapshot = out.Dynatrace
	j.ProviderCursor = out.Cursor
	j.RunTraceIDs = out.RunTraceIDs
	return nil
}

type preparationMonitorJSON PreparationMonitorSnapshot

func (p PreparationMonitorSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		preparationMonitorJSON
		Connections          map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
		DynatraceConnections map[string]*DynatraceConnectionSnapshot   `json:"dynatrace_connections,omitempty"`
	}{preparationMonitorJSON(p), p.Connections, p.DynatraceConnections})
}
func (p *PreparationMonitorSnapshot) UnmarshalJSON(body []byte) error {
	out := struct {
		*preparationMonitorJSON
		Connections          map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
		DynatraceConnections map[string]*DynatraceConnectionSnapshot   `json:"dynatrace_connections,omitempty"`
	}{preparationMonitorJSON: (*preparationMonitorJSON)(p)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	p.Connections = out.Connections
	p.DynatraceConnections = out.DynatraceConnections
	return nil
}
